// Package cli implements the jevgate command line interface.
//
// The package deliberately contains no os.Exit call: Run returns the process
// exit code so that cmd/jevgate stays trivial and tests can inspect the result
// without terminating the test binary.
package cli

import (
	"context"
	"fmt"
	"io"
)

// Exit codes returned by Run. Exit code 1 is a normal negative decision rather
// than a failure, so callers must keep it distinct from 2.
const (
	// ExitAllow reports that AI approval is allowed for the change. It is also
	// returned by successful --help and --version runs.
	ExitAllow = 0
	// ExitHumanReview reports that a human approval is still required.
	ExitHumanReview = 1
	// ExitError reports an input, configuration, or API error.
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
	return run(ctx, args, stdout, stderr)
}

// run carries the implementation of Run. Later tasks widen it with the config
// loader, git, and Jev dependencies so that the exported signature stays
// narrow and the dependencies remain replaceable in tests.
func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	switch {
	case hasFlag(args, "-h", "--help"):
		fmt.Fprint(stdout, helpText)
		return ExitAllow
	case hasFlag(args, "--version"):
		fmt.Fprintf(stdout, "jevgate %s\n", version)
		return ExitAllow
	}

	if len(args) == 0 {
		fmt.Fprintln(stderr, "jevgate: <base> and <head> are required")
		fmt.Fprintln(stderr, `jevgate: run "jevgate --help" for usage`)
		return ExitError
	}

	// Evaluating a change is wired up in a later task. Until then the only
	// honest answer is an error: a missing evaluation must never be reported as
	// an allowed AI approval.
	fmt.Fprintln(stderr, "jevgate: evaluating a change is not implemented yet; no decision was made")
	return ExitError
}

// hasFlag reports whether one of names appears in args before a "--"
// terminator. Arguments after "--" are revisions, never flags.
func hasFlag(args []string, names ...string) bool {
	for _, arg := range args {
		if arg == "--" {
			return false
		}
		for _, name := range names {
			if arg == name {
				return true
			}
		}
	}
	return false
}
