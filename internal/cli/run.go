package cli

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/ktsu2i/jevgate/internal/config"
	"github.com/ktsu2i/jevgate/internal/gate"
	"github.com/ktsu2i/jevgate/internal/git"
	"github.com/ktsu2i/jevgate/internal/output"
)

const apiKeyEnvironment = "JEV_API_KEY" //nolint:gosec // This is an environment variable name, not a credential.

const (
	// ExitAllow indicates that AI approval is allowed.
	ExitAllow = 0
	// ExitHumanReview indicates that human approval is required.
	ExitHumanReview = 1
	// ExitError indicates that no decision was made.
	ExitError = 2
)

const helpText = `jevgate decides whether an AI code reviewer's approval may replace a human
approval for a change. It does not review code and it does not approve pull
requests.

Usage:
  jevgate <base> <head> [options]
  jevgate --base <base> --head <head> [options]

Examples:
  jevgate main HEAD
  jevgate origin/main HEAD --threshold 0.98
  jevgate --base abc123 --head def456 --format json

Revisions:
  Both revisions are required. Use either the positional form or the flag form,
  not both. Any revision git resolves (branch, tag, commit) is accepted;
  revision range expressions such as main..HEAD or main...HEAD are not.

Options:
  --config <path>       Configuration file (default: .jevgate.yml in the repository root)
  --threshold <float>   Minimum probability required to allow AI approval (default: 0.95)
  --format <text|json>  Output format (default: text)
  -h, --help            Print this help and exit
  --version             Print the version and exit

Environment:
  JEV_API_KEY           API key for the Jev API. Not needed by --help or --version.

Exit codes:
  0  AI approval allowed
  1  human approval required
  2  input, configuration, or API error
`

// Run executes the command line interface and returns the process exit code.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	return run(ctx, args, stdout, stderr, productionDependencies())
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer, deps dependencies) int {
	switch {
	case hasFlag(args, "-h", "--help"):
		return writeInformation(stdout, stderr, helpText)
	case hasFlag(args, "--version"):
		return writeInformation(stdout, stderr, fmt.Sprintf("jevgate %s\n", version))
	}

	opts, err := ParseOptions(args)
	if err != nil {
		return fail(stderr, err)
	}

	cwd, err := deps.getwd()
	if err != nil {
		return fail(stderr, fmt.Errorf("get working directory: %w", err))
	}
	repository, err := git.Discover(ctx, cwd)
	if err != nil {
		return fail(stderr, err)
	}

	configuration, err := config.Load(config.Params{
		RepoRoot:     repository.Root(),
		Cwd:          cwd,
		Path:         opts.ConfigPath,
		Threshold:    opts.Threshold,
		ThresholdSet: opts.ThresholdSet,
	})
	if err != nil {
		return fail(stderr, err)
	}

	baseID, err := repository.ResolveCommit(ctx, opts.Base)
	if err != nil {
		return fail(stderr, err)
	}
	headID, err := repository.ResolveCommit(ctx, opts.Head)
	if err != nil {
		return fail(stderr, err)
	}
	diff, err := repository.Collect(ctx, baseID, headID)
	if err != nil {
		return fail(stderr, err)
	}

	apiKey, found := deps.lookupEnv(apiKeyEnvironment)
	if !found || strings.TrimSpace(apiKey) == "" || strings.ContainsAny(apiKey, "\r\n") {
		return fail(stderr, fmt.Errorf("%s is not set or is empty; set it to your Jev API key", apiKeyEnvironment))
	}
	assessor := deps.newAssessor(apiKey)
	result, err := gate.Evaluate(ctx, assessor, diff, configuration.Context, configuration.Threshold)
	if err != nil {
		return fail(stderr, err)
	}

	switch opts.Format {
	case FormatText:
		err = output.WriteText(stdout, result)
	case FormatJSON:
		err = output.WriteJSON(stdout, result)
	default:
		panic("unreachable: ParseOptions validates the output format")
	}
	if err != nil {
		return fail(stderr, fmt.Errorf("write output: %w", err))
	}
	if result.AIApprovalAllowed {
		return ExitAllow
	}
	return ExitHumanReview
}

func writeInformation(stdout, stderr io.Writer, value string) int {
	written, err := io.WriteString(stdout, value)
	if err == nil && written != len(value) {
		err = io.ErrShortWrite
	}
	if err != nil {
		return fail(stderr, fmt.Errorf("write output: %w", err))
	}
	return ExitAllow
}

func fail(stderr io.Writer, err error) int {
	diagnostic := strings.NewReplacer("\r", " ", "\n", " ").Replace(err.Error())
	_, _ = fmt.Fprintf(stderr, "jevgate: %s\n", diagnostic)
	return ExitError
}

func hasFlag(args []string, names ...string) bool {
	for _, arg := range args {
		if arg == "--" {
			return false
		}
		if slices.Contains(names, arg) {
			return true
		}
	}
	return false
}
