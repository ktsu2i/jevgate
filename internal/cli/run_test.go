package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// runCLI runs the CLI with args and returns the exit code and both streams.
func runCLI(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()

	var out, errOut bytes.Buffer
	code = Run(context.Background(), args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestExitCodes(t *testing.T) {
	if ExitAllow != 0 || ExitHumanReview != 1 || ExitError != 2 {
		t.Fatalf("exit codes changed: allow=%d humanReview=%d error=%d", ExitAllow, ExitHumanReview, ExitError)
	}
}

func TestRunHelp(t *testing.T) {
	// Help documents both revision forms and the exit codes, and it stays
	// available when it follows the revisions.
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
		if code != ExitAllow {
			t.Errorf("Run(%q) = %d, want %d (stderr: %q)", args, code, ExitAllow, stderr)
		}
		if stderr != "" {
			t.Errorf("Run(%q) wrote to stderr: %q", args, stderr)
		}
		for _, section := range sections {
			if !strings.Contains(stdout, section) {
				t.Errorf("Run(%q) help output is missing %q:\n%s", args, section, stdout)
			}
		}
	}
}

func TestRunVersion(t *testing.T) {
	code, stdout, stderr := runCLI(t, "--version")
	if code != ExitAllow {
		t.Errorf("Run(--version) = %d, want %d (stderr: %q)", code, ExitAllow, stderr)
	}
	if stderr != "" {
		t.Errorf("Run(--version) wrote to stderr: %q", stderr)
	}
	if want := "jevgate " + version + "\n"; stdout != want {
		t.Errorf("Run(--version) stdout = %q, want %q", stdout, want)
	}
}

func TestVersionDefaultsToDev(t *testing.T) {
	// Release builds override this variable at link time; development builds
	// must report "dev".
	if version != "dev" {
		t.Errorf("version = %q, want %q", version, "dev")
	}
}

func TestRunHelpAndVersionNeedNoRepositoryOrAPIKey(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("JEV_API_KEY", "")

	for _, args := range [][]string{{"--help"}, {"--version"}} {
		code, stdout, stderr := runCLI(t, args...)
		if code != ExitAllow {
			t.Errorf("Run(%q) outside a repository = %d, want %d (stderr: %q)", args, code, ExitAllow, stderr)
		}
		if stdout == "" {
			t.Errorf("Run(%q) outside a repository wrote nothing to stdout", args)
		}
	}
}

func TestRunEvaluationIsNotImplemented(t *testing.T) {
	// The evaluation path is wired up in a later task. It must fail with the
	// error code instead of reporting an allowed AI approval, and it must keep
	// stdout free of a decision so that --format json stays parsable.
	for _, args := range [][]string{
		{"main", "HEAD"},
		{"--base", "main", "--head", "HEAD"},
		{"main", "HEAD", "--format", "json"},
		{"--", "--help"},
	} {
		code, stdout, stderr := runCLI(t, args...)
		if code != ExitError {
			t.Errorf("Run(%q) = %d, want %d (stdout: %q)", args, code, ExitError, stdout)
		}
		if stdout != "" {
			t.Errorf("Run(%q) wrote to stdout: %q", args, stdout)
		}
		if !strings.Contains(stderr, "not implemented") {
			t.Errorf("Run(%q) stderr = %q, want a diagnostic about the unimplemented path", args, stderr)
		}
		if strings.Contains(stderr, "ALLOW") {
			t.Errorf("Run(%q) stderr reports a decision: %q", args, stderr)
		}
	}
}

func TestRunWithoutArguments(t *testing.T) {
	code, stdout, stderr := runCLI(t)
	if code != ExitError {
		t.Errorf("Run() = %d, want %d (stdout: %q)", code, ExitError, stdout)
	}
	if stdout != "" {
		t.Errorf("Run() wrote to stdout: %q", stdout)
	}
	if !strings.Contains(stderr, "required") {
		t.Errorf("Run() stderr = %q, want a diagnostic about the missing revisions", stderr)
	}
}
