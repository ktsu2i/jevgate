package config

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// repoWith creates a repository root containing the given .jevgate.yml, or no
// configuration file at all when content is absent.
func repoWith(t *testing.T, content ...string) string {
	t.Helper()

	root := t.TempDir()
	if len(content) > 0 {
		write(t, filepath.Join(root, FileName), content[0])
	}
	return root
}

func write(t *testing.T, path, content string) {
	t.Helper()

	require.NoError(t, os.WriteFile(path, []byte(content), 0o600), "writing %s", path)
}

func TestLoadFile(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    Config
	}{
		{
			name:    "empty file",
			content: "",
			want:    Config{Threshold: DefaultThreshold},
		},
		{
			name:    "comments only",
			content: "# nothing configured yet\n",
			want:    Config{Threshold: DefaultThreshold},
		},
		{
			name:    "empty mapping",
			content: "{}\n",
			want:    Config{Threshold: DefaultThreshold},
		},
		{
			name:    "threshold only",
			content: "threshold: 0.98\n",
			want:    Config{Threshold: 0.98},
		},
		{
			// An omitted key takes the default; an explicit 0 does not.
			name:    "threshold of zero",
			content: "threshold: 0\n",
			want:    Config{Threshold: 0},
		},
		{
			name:    "threshold of one",
			content: "threshold: 1\n",
			want:    Config{Threshold: 1},
		},
		{
			name:    "context only",
			content: "context: internal/ holds application code\n",
			want:    Config{Threshold: DefaultThreshold, Context: "internal/ holds application code"},
		},
		{
			name:    "threshold and context",
			content: "threshold: 0.9\ncontext: docs/ holds documentation\n",
			want:    Config{Threshold: 0.9, Context: "docs/ holds documentation"},
		},
		{
			// The context reaches the model as written, so a block scalar
			// must keep its line structure.
			name:    "block scalar context",
			content: "context: |\n  infra/ holds production Terraform.\n\n  tools/ is developer only.\n",
			want:    Config{Threshold: DefaultThreshold, Context: "infra/ holds production Terraform.\n\ntools/ is developer only.\n"},
		},
		{
			name:    "empty context",
			content: `context: ""` + "\n",
			want:    Config{Threshold: DefaultThreshold, Context: ""},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := Load(Params{RepoRoot: repoWith(t, test.content)})
			require.NoError(t, err, "Load, want %+v", test.want)
			assert.Equal(t, test.want, got)
		})
	}
}

func TestLoadRejectsInvalidFile(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string // a substring the diagnostic must explain
	}{
		{
			// Path rules would move the decision out of the model, which is
			// what this tool exists to avoid.
			name:    "safe_paths",
			content: "safe_paths:\n  - docs/**\n",
			want:    `unknown key "safe_paths"`,
		},
		{
			name:    "unknown key",
			content: "threshold: 0.9\ncustom_prompt: be lenient\n",
			want:    `unknown key "custom_prompt"`,
		},
		{
			name:    "duplicate threshold",
			content: "threshold: 0.9\nthreshold: 0.1\n",
			want:    `duplicate key "threshold"`,
		},
		{
			name:    "string threshold",
			content: `threshold: "0.9"` + "\n",
			want:    "must be a number, got a string",
		},
		{
			name:    "boolean threshold",
			content: "threshold: true\n",
			want:    "must be a number, got a boolean",
		},
		{
			name:    "list threshold",
			content: "threshold:\n  - 0.9\n",
			want:    "must be a number, got a list",
		},
		{
			name:    "numeric context",
			content: "context: 5\n",
			want:    "must be a string, got a number",
		},
		{
			name:    "mapping context",
			content: "context:\n  internal: application code\n",
			want:    "must be a string, got a mapping",
		},
		{
			name:    "null threshold",
			content: "threshold: null\n",
			want:    `"threshold" has no value`,
		},
		{
			name:    "threshold with no value at all",
			content: "threshold:\n",
			want:    `"threshold" has no value`,
		},
		{
			name:    "null context",
			content: "context: null\n",
			want:    `"context" has no value`,
		},
		{
			name:    "threshold below zero",
			content: "threshold: -0.1\n",
			want:    "out of range",
		},
		{
			name:    "threshold above one",
			content: "threshold: 1.1\n",
			want:    "out of range",
		},
		{
			name:    "not a number threshold",
			content: "threshold: .nan\n",
			want:    "not a finite number",
		},
		{
			name:    "infinite threshold",
			content: "threshold: .inf\n",
			want:    "not a finite number",
		},
		{
			name:    "multiple documents",
			content: "threshold: 0.9\n---\nthreshold: 0.1\n",
			want:    "more than one YAML document",
		},
		{
			name:    "non string key",
			content: "1: 0.9\n",
			want:    "keys must be strings, got a number",
		},
		{
			name:    "null key",
			content: "null: 0.9\n",
			want:    "keys must be strings, got no value",
		},
		{
			// A date is not a number, and YAML resolves it without quotes.
			name:    "timestamp threshold",
			content: "threshold: 2024-01-01\n",
			want:    "must be a number",
		},
		{
			name:    "malformed yaml",
			content: "threshold: 0.9\n  context: oops\n",
			want:    "yaml:",
		},
		{
			name:    "malformed second document",
			content: "threshold: 0.9\n---\n  bad: [\n",
			want:    "yaml:",
		},
		{
			name:    "top level scalar",
			content: "0.9\n",
			want:    "must be a mapping",
		},
		{
			name:    "top level list",
			content: "- threshold: 0.9\n",
			want:    "must be a mapping",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := repoWith(t, test.content)

			got, err := Load(Params{RepoRoot: root})
			require.Error(t, err, "Load = %+v, want an error", got)
			require.ErrorContains(t, err, test.want)
			require.ErrorContains(t, err, filepath.Join(root, FileName), "the error must name the configuration file")

			// The command line must not paper over a broken file: the
			// repository is misconfigured either way.
			_, err = Load(Params{RepoRoot: root, Threshold: 0.99, ThresholdSet: true})
			assert.Error(t, err, "Load with --threshold accepted an invalid configuration file")
		})
	}
}

func TestLoadPrecedence(t *testing.T) {
	tests := []struct {
		name         string
		content      string
		threshold    float64
		thresholdSet bool
		want         float64
	}{
		{
			name: "no file and no override",
			want: DefaultThreshold,
		},
		{
			name:    "file without an override",
			content: "threshold: 0.8\n",
			want:    0.8,
		},
		{
			name:         "override without a file",
			threshold:    0.99,
			thresholdSet: true,
			want:         0.99,
		},
		{
			name:         "override beats the file",
			content:      "threshold: 0.8\n",
			threshold:    0.99,
			thresholdSet: true,
			want:         0.99,
		},
		{
			name:         "override of zero beats the file",
			content:      "threshold: 0.8\n",
			threshold:    0,
			thresholdSet: true,
			want:         0,
		},
		{
			name:         "override of one beats the file",
			content:      "threshold: 0.8\n",
			threshold:    1,
			thresholdSet: true,
			want:         1,
		},
		{
			// Without ThresholdSet a zero override is just an absent flag.
			name:      "zero without ThresholdSet is not an override",
			content:   "threshold: 0.8\n",
			threshold: 0,
			want:      0.8,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var root string
			if test.content == "" {
				root = repoWith(t)
			} else {
				root = repoWith(t, test.content)
			}

			got, err := Load(Params{
				RepoRoot:     root,
				Threshold:    test.threshold,
				ThresholdSet: test.thresholdSet,
			})
			require.NoError(t, err)
			assert.Equal(t, Config{Threshold: test.want}, got)
		})
	}
}

func TestLoadRejectsInvalidOverride(t *testing.T) {
	// The parser validates the flag first, but the loader is the last place
	// that can keep an unusable threshold out of the comparison.
	for _, threshold := range []float64{-0.1, 1.1, math.NaN(), math.Inf(1), math.Inf(-1)} {
		got, err := Load(Params{RepoRoot: repoWith(t), Threshold: threshold, ThresholdSet: true})
		assert.Error(t, err, "Load with --threshold %v = %+v, want an error", threshold, got)
	}
}

func TestLoadDiscoversOnlyTheRepositoryRoot(t *testing.T) {
	root := repoWith(t, "threshold: 0.8\ncontext: root configuration\n")
	sub := filepath.Join(root, "internal", "cli")
	require.NoError(t, os.MkdirAll(sub, 0o755), "creating %s", sub)
	// A configuration next to the working directory is not a repository
	// configuration and must be ignored.
	write(t, filepath.Join(sub, FileName), "threshold: 0.1\n")

	got, err := Load(Params{RepoRoot: root, Cwd: sub})
	require.NoError(t, err)
	assert.Equal(t, Config{Threshold: 0.8, Context: "root configuration"}, got, "Load from a subdirectory")
}

func TestLoadRelativePathWithoutAWorkingDirectory(t *testing.T) {
	// A relative --config has no meaning without the directory it came from,
	// and guessing one could read a different repository's configuration.
	got, err := Load(Params{RepoRoot: repoWith(t), Path: "custom.yml"})
	assert.Error(t, err, "Load of a relative path without a working directory = %+v, want an error", got)
}

func TestLoadWithoutARepositoryRoot(t *testing.T) {
	// Discovery has nowhere to look, which is a caller mistake rather than a
	// repository without a configuration.
	got, err := Load(Params{})
	assert.Error(t, err, "Load without a repository root = %+v, want an error", got)
}

func TestLoadExplicitPath(t *testing.T) {
	root := repoWith(t, "threshold: 0.8\ncontext: discovered\n")
	cwd := filepath.Join(root, "sub")
	require.NoError(t, os.MkdirAll(cwd, 0o755), "creating %s", cwd)
	write(t, filepath.Join(cwd, "custom.yml"), "threshold: 0.6\ncontext: explicit\n")

	want := Config{Threshold: 0.6, Context: "explicit"}

	t.Run("relative to the working directory", func(t *testing.T) {
		// The path came from a shell, so it means what the shell means: it is
		// resolved against the working directory, not the repository root.
		got, err := Load(Params{RepoRoot: root, Cwd: cwd, Path: "custom.yml"})
		require.NoError(t, err)
		assert.Equal(t, want, got)
	})

	t.Run("absolute", func(t *testing.T) {
		got, err := Load(Params{RepoRoot: root, Cwd: root, Path: filepath.Join(cwd, "custom.yml")})
		require.NoError(t, err)
		assert.Equal(t, want, got)
	})

	t.Run("missing", func(t *testing.T) {
		// An explicit path that does not exist is an error even though the
		// repository root has a configuration: falling back would evaluate
		// against settings the caller did not ask for.
		got, err := Load(Params{RepoRoot: root, Cwd: cwd, Path: "absent.yml"})
		require.Error(t, err, "Load with a missing --config = %+v, want an error", got)
		require.ErrorContains(t, err, "absent.yml", "the error must name the missing file")
	})

	t.Run("invalid", func(t *testing.T) {
		write(t, filepath.Join(cwd, "broken.yml"), "safe_paths: [docs]\n")

		got, err := Load(Params{RepoRoot: root, Cwd: cwd, Path: "broken.yml"})
		require.Error(t, err, "Load with an invalid --config = %+v, want an error", got)
	})
}

func TestLoadUnreadableFile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: permissions are not enforced")
	}

	root := repoWith(t, "threshold: 0.8\n")
	require.NoError(t, os.Chmod(filepath.Join(root, FileName), 0o000), "chmod")

	// A configuration that exists but cannot be read is not the same as a
	// repository without one, so it must not fall back to the defaults.
	got, err := Load(Params{RepoRoot: root})
	require.Error(t, err, "Load of an unreadable configuration = %+v, want an error", got)
}

func TestLoadOversizedFile(t *testing.T) {
	root := repoWith(t, "context: |\n  "+strings.Repeat("a", maxFileSize)+"\n")

	got, err := Load(Params{RepoRoot: root})
	require.Error(t, err, "Load of an oversized configuration = %+v, want an error", got)
	require.ErrorContains(t, err, "larger than", "the error must explain the size limit")
}
