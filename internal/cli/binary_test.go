package cli_test

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/ktsu2i/jevgate/internal/cli"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuiltBinaryInformationAndInputError(t *testing.T) {
	binaryName := "jevgate"
	if runtime.GOOS == "windows" {
		binaryName += ".exe"
	}
	binary := filepath.Join(t.TempDir(), binaryName)
	root := filepath.Clean(filepath.Join("..", ".."))
	build := exec.CommandContext(t.Context(), "go", "build", "-o", binary, "./cmd/jevgate")
	build.Dir = root
	output, err := build.CombinedOutput()
	require.NoError(t, err, "go build: %s", output)

	tests := []struct {
		name     string
		args     []string
		wantCode int
		contains string
	}{
		{name: "help", args: []string{"--help"}, wantCode: cli.ExitAllow, contains: "jevgate <base> <head>"},
		{name: "version", args: []string{"--version"}, wantCode: cli.ExitAllow, contains: "jevgate dev\n"},
		{name: "input error", wantCode: cli.ExitError, contains: "exactly 2 revisions"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, code := runBinary(t, binary, test.args...)
			assert.Equal(t, test.wantCode, code, got)
			assert.Contains(t, got, test.contains)
		})
	}
}

func runBinary(t *testing.T, binary string, args ...string) (output string, exitCode int) {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), binary, args...)
	data, err := cmd.CombinedOutput()
	if err == nil {
		return string(data), 0
	}
	var exitError *exec.ExitError
	require.ErrorAs(t, err, &exitError)
	return string(data), exitError.ExitCode()
}
