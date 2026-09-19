package git

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMain isolates the tests from the machine's own Git configuration. A
// global diff driver or a system-wide setting must not be able to decide
// whether these tests pass, and no test may write to either file: everything
// a test configures goes into its own repository.
func TestMain(m *testing.M) {
	for _, name := range []string{"GIT_CONFIG_GLOBAL", "GIT_CONFIG_SYSTEM"} {
		if err := os.Setenv(name, os.DevNull); err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", name, err)
			os.Exit(1)
		}
	}
	os.Exit(m.Run())
}

// repo is a Git repository created for one test.
type repo struct {
	t    *testing.T
	root string
}

// newRepo creates an empty repository. The identity it commits with is
// written to that repository's own configuration, so running the tests
// changes nothing outside the temporary directory.
func newRepo(t *testing.T) *repo {
	t.Helper()

	r := &repo{t: t, root: resolve(t, t.TempDir())}
	r.git("init", "--quiet", "--initial-branch=main")
	r.git("config", "user.name", "JevGate Test")
	r.git("config", "user.email", "test@example.invalid")
	return r
}

// resolve returns the path Git reports for a directory. A temporary directory
// is reached through a symbolic link on some systems, and Git answers with
// the directory itself.
func resolve(t *testing.T, path string) string {
	t.Helper()

	resolved, err := filepath.EvalSymlinks(path)
	require.NoError(t, err, "resolving %s", path)
	return resolved
}

// git runs one Git command in the repository and returns its output.
func (r *repo) git(args ...string) string {
	r.t.Helper()

	out, err := exec.CommandContext(r.t.Context(), "git", append([]string{"-C", r.root}, args...)...).CombinedOutput()
	require.NoErrorf(r.t, err, "git %s: %s", strings.Join(args, " "), out)
	return string(out)
}

// write creates or replaces a file in the working tree.
func (r *repo) write(path, content string) {
	r.t.Helper()

	full := filepath.Join(r.root, path)
	require.NoError(r.t, os.MkdirAll(filepath.Dir(full), 0o750), "creating the directory of %s", path)
	require.NoError(r.t, os.WriteFile(full, []byte(content), 0o600), "writing %s", path)
}

// remove deletes a file from the working tree.
func (r *repo) remove(path string) {
	r.t.Helper()

	require.NoError(r.t, os.Remove(filepath.Join(r.root, path)), "removing %s", path)
}

// symlink creates a symbolic link in the working tree.
func (r *repo) symlink(target, path string) {
	r.t.Helper()

	require.NoError(r.t, os.Symlink(target, filepath.Join(r.root, path)), "linking %s", path)
}

// chmod changes a file's permissions, of which Git records only whether the
// file is executable.
func (r *repo) chmod(path string, mode os.FileMode) {
	r.t.Helper()

	require.NoError(r.t, os.Chmod(filepath.Join(r.root, path), mode), "changing the mode of %s", path)
}

// commit records the whole working tree and returns the new commit's ID.
func (r *repo) commit(message string) string {
	r.t.Helper()

	r.git("add", "--all")
	return r.commitIndex(message)
}

// commitIndex records what is staged, leaving the working tree out of it.
func (r *repo) commitIndex(message string) string {
	r.t.Helper()

	r.git("commit", "--quiet", "--message", message)
	return strings.TrimSpace(r.git("rev-parse", "HEAD"))
}

// discover returns the package's view of the repository.
func (r *repo) discover() Repository {
	r.t.Helper()

	repository, err := Discover(r.t.Context(), r.root)
	require.NoError(r.t, err, "Discover")
	return repository
}

// shallowClone clones the repository with a truncated history, as a CI
// checkout does by default.
func (r *repo) shallowClone(depth int) *repo {
	r.t.Helper()

	dir := filepath.Join(r.t.TempDir(), "shallow")
	out, err := exec.CommandContext(r.t.Context(), "git", "clone", "--quiet",
		"--depth", strconv.Itoa(depth), "file://"+r.root, dir).CombinedOutput()
	require.NoErrorf(r.t, err, "git clone: %s", out)

	return &repo{t: r.t, root: resolve(r.t, dir)}
}

func TestDiscover(t *testing.T) {
	t.Parallel()

	r := newRepo(t)
	r.write("nested/deep/file.txt", "content\n")
	r.commit("one")

	for _, dir := range []string{".", "nested", "nested/deep"} {
		t.Run(dir, func(t *testing.T) {
			t.Parallel()

			repository, err := Discover(t.Context(), filepath.Join(r.root, dir))
			require.NoError(t, err)
			assert.Equal(t, r.root, repository.Root(), "the repository root is the same from every directory in it")
		})
	}
}

func TestDiscoverRejectsNonRepository(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cwd  func(t *testing.T) string
		want error
	}{
		{
			name: "outside a repository",
			cwd:  func(t *testing.T) string { t.Helper(); return resolve(t, t.TempDir()) },
			want: ErrNotRepository,
		},
		{
			name: "directory that does not exist",
			cwd:  func(t *testing.T) string { t.Helper(); return filepath.Join(t.TempDir(), "missing") },
			want: ErrNotRepository,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := Discover(t.Context(), test.cwd(t))
			require.ErrorIs(t, err, test.want)
		})
	}
}

func TestDiscoverWithoutWorkingDirectory(t *testing.T) {
	t.Parallel()

	_, err := Discover(t.Context(), "")
	require.Error(t, err, "an empty working directory must not be answered with the process's own")
}

func TestResolveCommit(t *testing.T) {
	t.Parallel()

	r := newRepo(t)
	r.write("a.txt", "one\n")
	first := r.commit("one")
	r.write("a.txt", "two\n")
	second := r.commit("two")
	r.git("branch", "feature", first)
	r.git("tag", "light", first)
	r.git("tag", "--annotate", "annotated", "--message", "release", second)

	repository := r.discover()

	tests := []struct {
		name     string
		revision string
		want     string
	}{
		{name: "HEAD", revision: "HEAD", want: second},
		{name: "ancestor of HEAD", revision: "HEAD~1", want: first},
		{name: "branch", revision: "feature", want: first},
		{name: "lightweight tag", revision: "light", want: first},
		{name: "annotated tag", revision: "annotated", want: second},
		{name: "object ID", revision: second, want: second},
		{name: "abbreviated object ID", revision: second[:8], want: second},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := repository.ResolveCommit(t.Context(), test.revision)
			require.NoError(t, err)
			assert.Equal(t, test.want, got)
			assert.True(t, isObjectID(got), "%q is a complete object ID", got)
		})
	}
}

func TestResolveCommitRejects(t *testing.T) {
	t.Parallel()

	r := newRepo(t)
	r.write("a.txt", "one\n")
	r.commit("one")
	repository := r.discover()

	tests := []struct {
		name     string
		revision string
	}{
		{name: "unknown revision", revision: "does-not-exist"},
		{name: "empty revision", revision: ""},
		{name: "a tree", revision: "HEAD^{tree}"},
		{name: "a blob", revision: "HEAD:a.txt"},
		{name: "a range", revision: "HEAD~1..HEAD"},
		// A revision that looks like an option has to stay a revision: Git
		// must never be told to do something else by the value being resolved.
		{name: "an option", revision: "--help"},
		{name: "an option with a value", revision: "--git-dir=/nonexistent"},
		{name: "a short option", revision: "-n"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			id, err := repository.ResolveCommit(t.Context(), test.revision)
			require.ErrorIs(t, err, ErrRevision)
			assert.Empty(t, id)
		})
	}
}

func TestResolveCommitInShallowClone(t *testing.T) {
	t.Parallel()

	r := newRepo(t)
	r.write("a.txt", "one\n")
	r.commit("one")
	r.write("a.txt", "two\n")
	head := r.commit("two")

	clone := r.shallowClone(1)
	repository := clone.discover()

	got, err := repository.ResolveCommit(t.Context(), "HEAD")
	require.NoError(t, err, "the commit the clone does have resolves")
	assert.Equal(t, head, got)

	// The missing commit exists, so the revision is not wrong; saying so is
	// the difference between fetching more history and editing a workflow.
	_, err = repository.ResolveCommit(t.Context(), "HEAD~1")
	require.ErrorIs(t, err, ErrRevision)
	assert.Contains(t, err.Error(), "fetch")
}

func TestResolveCommitRespectsCancellation(t *testing.T) {
	t.Parallel()

	r := newRepo(t)
	r.write("a.txt", "one\n")
	r.commit("one")
	repository := r.discover()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := repository.ResolveCommit(ctx, "HEAD")
	require.ErrorIs(t, err, context.Canceled)
}

func TestUndiscoveredRepository(t *testing.T) {
	t.Parallel()

	var repository Repository
	_, err := repository.ResolveCommit(t.Context(), "HEAD")
	require.Error(t, err, "a repository with no root must not run Git in the process's own directory")
}

func TestIsObjectID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		id   string
		want bool
	}{
		{name: "SHA-1", id: strings.Repeat("a1", 20), want: true},
		{name: "SHA-256", id: strings.Repeat("b2", 32), want: true},
		{name: "abbreviated", id: strings.Repeat("a", 7), want: false},
		{name: "empty", id: "", want: false},
		{name: "uppercase", id: strings.Repeat("A", 40), want: false},
		{name: "not hexadecimal", id: strings.Repeat("g", 40), want: false},
		{name: "revision", id: "HEAD", want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, test.want, isObjectID(test.id))
		})
	}
}
