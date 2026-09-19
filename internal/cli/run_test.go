package cli

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func runCLI(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()

	var out, errOut bytes.Buffer
	code = Run(context.Background(), args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestExitCodes(t *testing.T) {
	t.Parallel()

	assert.Equal(t, 0, ExitAllow, "the allow exit code changed")
	assert.Equal(t, 1, ExitHumanReview, "the human review exit code changed")
	assert.Equal(t, 2, ExitError, "the error exit code changed")
}

func TestRunHelp(t *testing.T) {
	t.Parallel()

	sections := []string{
		"jevgate <base> <head>",
		"jevgate --base <base> --head <head>",
		"main..HEAD",
		"--threshold",
		"--format <text|json>",
		"JEV_API_KEY",
		"Exit codes:",
		"0  AI approval allowed",
		"1  human approval required",
		"2  input, configuration, or API error",
	}

	for _, args := range [][]string{
		{"--help"},
		{"-h"},
		{"main", "HEAD", "--help"},
		{"--version", "--help"},
		{"--help", "--version"},
	} {
		code, stdout, stderr := runCLI(t, args...)
		assert.Equal(t, ExitAllow, code, "Run(%q) (stderr: %q)", args, stderr)
		assert.Empty(t, stderr, "Run(%q) wrote to stderr", args)
		for _, section := range sections {
			assert.Contains(t, stdout, section, "Run(%q) help output", args)
		}
	}
}

func TestRunVersion(t *testing.T) {
	t.Parallel()

	code, stdout, stderr := runCLI(t, "--version")
	assert.Equal(t, ExitAllow, code, "Run(--version) (stderr: %q)", stderr)
	assert.Empty(t, stderr, "Run(--version) wrote to stderr")
	assert.Equal(t, "jevgate "+version+"\n", stdout, "Run(--version) stdout")
}

func TestVersionDefaultsToDev(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "dev", version)
}

// Process-wide state prevents this test from running in parallel.
func TestRunHelpAndVersionNeedNoRepositoryOrAPIKey(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("JEV_API_KEY", "")

	for _, args := range [][]string{{"--help"}, {"--version"}} {
		code, stdout, stderr := runCLI(t, args...)
		assert.Equal(t, ExitAllow, code, "Run(%q) outside a repository (stderr: %q)", args, stderr)
		assert.NotEmpty(t, stdout, "Run(%q) outside a repository wrote nothing to stdout", args)
	}
}

func TestRunEvaluationIsNotImplemented(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{
		{"main", "HEAD"},
		{"--base", "main", "--head", "HEAD"},
		{"main", "HEAD", "--format", "json"},
		{"--", "--help"},
	} {
		code, stdout, stderr := runCLI(t, args...)
		assert.Equal(t, ExitError, code, "Run(%q) (stdout: %q)", args, stdout)
		assert.Empty(t, stdout, "Run(%q) wrote to stdout", args)
		assert.Contains(t, stderr, "not implemented", "Run(%q) stderr, want a diagnostic about the unimplemented path", args)
		assert.NotContains(t, stderr, "ALLOW", "Run(%q) stderr reports a decision", args)
	}
}

func TestRunWithoutArguments(t *testing.T) {
	t.Parallel()

	code, stdout, stderr := runCLI(t)
	assert.Equal(t, ExitError, code, "Run() (stdout: %q)", stdout)
	assert.Empty(t, stdout, "Run() wrote to stdout")
	assert.Contains(t, stderr, "required", "Run() stderr, want a diagnostic about the missing revisions")
}
