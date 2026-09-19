package git_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ktsu2i/jevgate/internal/git"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	modeNone       = "000000"
	modeRegular    = "100644"
	modeExecutable = "100755"
	modeSymlink    = "120000"
)

func writeBase(r *repo) {
	r.write("kept.txt", "unchanged\n")
	r.write("edited.txt", "one\ntwo\nthree\n")
	r.write("moved.txt", "content that moves\n")
	r.write("removed.txt", "goes away\n")
	r.write("mode.txt", "keeps its content\n")
	r.symlink("kept.txt", "link")
}

func (r *repo) collect(base, head string) (git.Diff, error) {
	r.t.Helper()

	return r.discover().Collect(r.t.Context(), base, head)
}

func TestCollect(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		head  func(r *repo)
		want  []git.File
		patch []string
	}{
		{
			name: "added file",
			head: func(r *repo) { r.write("added.txt", "brand new\n") },
			want: []git.File{{Change: git.Added, NewPath: "added.txt", OldMode: modeNone, NewMode: modeRegular}},
			patch: []string{
				"new file mode 100644",
				"+brand new",
			},
		},
		{
			name:  "added empty file",
			head:  func(r *repo) { r.write("empty.txt", "") },
			want:  []git.File{{Change: git.Added, NewPath: "empty.txt", OldMode: modeNone, NewMode: modeRegular}},
			patch: []string{"new file mode 100644"},
		},
		{
			name:  "deleted file",
			head:  func(r *repo) { r.remove("removed.txt") },
			want:  []git.File{{Change: git.Deleted, OldPath: "removed.txt", OldMode: modeRegular, NewMode: modeNone}},
			patch: []string{"deleted file mode 100644", "-goes away"},
		},
		{
			name: "modified file",
			head: func(r *repo) { r.write("edited.txt", "one\nchanged\nthree\n") },
			want: []git.File{{Change: git.Modified, OldPath: "edited.txt", NewPath: "edited.txt", OldMode: modeRegular, NewMode: modeRegular}},
			patch: []string{
				"-two",
				"+changed",
			},
		},
		{
			name:  "whitespace only change",
			head:  func(r *repo) { r.write("edited.txt", "one \ntwo\nthree\n") },
			want:  []git.File{{Change: git.Modified, OldPath: "edited.txt", NewPath: "edited.txt", OldMode: modeRegular, NewMode: modeRegular}},
			patch: []string{"-one\n", "+one \n"},
		},
		{
			name:  "renamed file",
			head:  func(r *repo) { r.git("mv", "moved.txt", "renamed.txt") },
			want:  []git.File{{Change: git.Renamed, OldPath: "moved.txt", NewPath: "renamed.txt", OldMode: modeRegular, NewMode: modeRegular}},
			patch: []string{"rename from moved.txt", "rename to renamed.txt"},
		},
		{
			name:  "mode only change",
			head:  func(r *repo) { r.chmod("mode.txt", 0o700) },
			want:  []git.File{{Change: git.Modified, OldPath: "mode.txt", NewPath: "mode.txt", OldMode: modeRegular, NewMode: modeExecutable}},
			patch: []string{"old mode 100644", "new mode 100755"},
		},
		{
			name: "symbolic link retargeted",
			head: func(r *repo) {
				r.remove("link")
				r.symlink("edited.txt", "link")
			},
			want:  []git.File{{Change: git.Modified, OldPath: "link", NewPath: "link", OldMode: modeSymlink, NewMode: modeSymlink}},
			patch: []string{"-kept.txt", "+edited.txt"},
		},
		{
			name: "file replaced by a symbolic link",
			head: func(r *repo) {
				r.remove("kept.txt")
				r.symlink("edited.txt", "kept.txt")
			},
			want:  []git.File{{Change: git.TypeChanged, OldPath: "kept.txt", NewPath: "kept.txt", OldMode: modeRegular, NewMode: modeSymlink}},
			patch: []string{"deleted file mode 100644", "new file mode 120000"},
		},
		{
			name: "several files at once",
			head: func(r *repo) {
				r.write("added.txt", "brand new\n")
				r.remove("removed.txt")
				r.write("edited.txt", "one\nchanged\nthree\n")
			},
			want: []git.File{
				{Change: git.Added, NewPath: "added.txt", OldMode: modeNone, NewMode: modeRegular},
				{Change: git.Modified, OldPath: "edited.txt", NewPath: "edited.txt", OldMode: modeRegular, NewMode: modeRegular},
				{Change: git.Deleted, OldPath: "removed.txt", OldMode: modeRegular, NewMode: modeNone},
			},
			patch: []string{"+brand new", "+changed", "-goes away"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			r := newRepo(t)
			writeBase(r)
			base := r.commit("base")
			test.head(r)
			head := r.commit("head")

			diff, err := r.collect(base, head)
			require.NoError(t, err)

			assert.Equal(t, base, diff.BaseID)
			assert.Equal(t, head, diff.HeadID)
			assert.Equal(t, test.want, diff.Files)
			for _, want := range test.patch {
				assert.Contains(t, diff.Patch, want)
			}
		})
	}
}

func TestCollectReadsAwkwardPaths(t *testing.T) {
	t.Parallel()

	paths := []string{
		"with space.txt",
		"日本語.txt",
		"with\ttab.txt",
		"with\nnewline.txt",
		`with"quote.txt`,
		"nested dir/deep file.txt",
	}

	r := newRepo(t)
	r.write("kept.txt", "unchanged\n")
	base := r.commit("base")
	for _, path := range paths {
		r.write(path, "content of "+path+"\n")
	}
	head := r.commit("head")

	diff, err := r.collect(base, head)
	require.NoError(t, err)

	got := make([]string, 0, len(diff.Files))
	for _, file := range diff.Files {
		assert.Equal(t, git.Added, file.Change)
		got = append(got, file.NewPath)
	}
	assert.ElementsMatch(t, paths, got)
}

func TestCollectComparesTheCommitsDirectly(t *testing.T) {
	t.Parallel()

	r := newRepo(t)
	r.write("shared.txt", "start\n")
	fork := r.commit("fork")
	r.write("base-only.txt", "only on the base branch\n")
	base := r.commit("base")

	r.git("checkout", "--quiet", "-b", "feature", fork)
	r.write("head-only.txt", "only on the head branch\n")
	head := r.commit("head")

	diff, err := r.collect(base, head)
	require.NoError(t, err)

	assert.Equal(t, []git.File{
		{Change: git.Deleted, OldPath: "base-only.txt", OldMode: modeRegular, NewMode: modeNone},
		{Change: git.Added, NewPath: "head-only.txt", OldMode: modeNone, NewMode: modeRegular},
	}, diff.Files)
}

func TestCollectIgnoresTheWorkingTree(t *testing.T) {
	t.Parallel()

	r := newRepo(t)
	r.write("committed.txt", "one\n")
	base := r.commit("base")
	r.write("committed.txt", "two\n")
	head := r.commit("head")

	r.write("committed.txt", "edited in the working tree\n")
	r.write("staged.txt", "staged but not committed\n")
	r.git("add", "staged.txt")
	r.write("untracked.txt", "never added\n")
	r.write(".gitattributes", "*.txt -diff\n")

	diff, err := r.collect(base, head)
	require.NoError(t, err)

	assert.Equal(t, []git.File{
		{Change: git.Modified, OldPath: "committed.txt", NewPath: "committed.txt", OldMode: modeRegular, NewMode: modeRegular},
	}, diff.Files)
	assert.Contains(t, diff.Patch, "+two")
	assert.NotContains(t, diff.Patch, "working tree")
}

func sentinelProgram(t *testing.T) (program, sentinel string) {
	t.Helper()

	dir := t.TempDir()
	program = filepath.Join(dir, "record.sh")
	sentinel = filepath.Join(dir, "ran")
	body := "#!/bin/sh\necho ran > \"" + sentinel + "\"\n"
	require.NoError(t, os.WriteFile(program, []byte(body), 0o700), "writing the sentinel program")
	return program, sentinel
}

func assertNotRun(t *testing.T, sentinel string) {
	t.Helper()

	_, err := os.Stat(sentinel)
	assert.ErrorIs(t, err, os.ErrNotExist, "reading a diff must not run a program the repository configured")
}

func TestCollectDoesNotRunConfiguredPrograms(t *testing.T) {
	t.Parallel()

	program, sentinel := sentinelProgram(t)

	r := newRepo(t)
	r.git("config", "diff.external", program)
	r.write("a.txt", "one\n")
	base := r.commit("base")
	r.write("a.txt", "two\n")
	head := r.commit("head")

	diff, err := r.collect(base, head)
	require.NoError(t, err)

	assertNotRun(t, sentinel)
	assert.Contains(t, diff.Patch, "-one")
	assert.Contains(t, diff.Patch, "+two")
}

// This test mutates the process environment and cannot run in parallel.
func TestCollectDoesNotRunTheEnvironmentsDiffProgram(t *testing.T) {
	program, sentinel := sentinelProgram(t)
	t.Setenv("GIT_EXTERNAL_DIFF", program)

	r := newRepo(t)
	r.write("a.txt", "one\n")
	base := r.commit("base")
	r.write("a.txt", "two\n")
	head := r.commit("head")

	diff, err := r.collect(base, head)
	require.NoError(t, err)

	assertNotRun(t, sentinel)
	assert.Contains(t, diff.Patch, "+two")
}

func TestCollectRejectsIncompleteDiffs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		configure func(r *repo)
		want      string
	}{
		{
			name:      "attribute hides the contents",
			configure: func(r *repo) { r.write(".gitattributes", "secret.txt -diff\n") },
			want:      "-diff",
		},
		{
			name:      "attribute names a diff driver",
			configure: func(r *repo) { r.write(".gitattributes", "secret.txt diff=sentinel\n") },
			want:      "sentinel",
		},
		{
			name:      "attribute set outside the commits",
			configure: func(r *repo) { r.write(".git/info/attributes", "secret.txt -diff\n") },
			want:      "-diff",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			program, sentinel := sentinelProgram(t)

			r := newRepo(t)
			r.git("config", "diff.sentinel.textconv", program)
			test.configure(r)
			r.write("secret.txt", "one\n")
			base := r.commit("base")
			r.write("secret.txt", "two\n")
			head := r.commit("head")

			_, err := r.collect(base, head)
			require.ErrorIs(t, err, git.ErrUnsupported)
			assert.Contains(t, err.Error(), test.want)
			assert.Contains(t, err.Error(), "secret.txt")
			assertNotRun(t, sentinel)
		})
	}
}

func TestCollectRejectsUnevaluableChanges(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		head func(r *repo) string
		want string
	}{
		{
			name: "binary file",
			head: func(r *repo) string {
				r.write("image.bin", "\x00\x01\x02not text\n")
				return r.commit("head")
			},
			want: "binary",
		},
		{
			name: "submodule",
			head: func(r *repo) string {
				r.git("update-index", "--add", "--cacheinfo", "160000,"+strings.Repeat("0", 39)+"1,vendor")
				return r.commitIndex("head")
			},
			want: "submodule",
		},
		{
			name: "path that is not valid UTF-8",
			head: func(r *repo) string {
				r.write("source.txt", "content\n")
				blob := strings.TrimSpace(r.git("hash-object", "-w", "source.txt"))
				r.git("update-index", "--add", "--cacheinfo", "100644,"+blob+",bad\xffname.txt")
				return r.commitIndex("head")
			},
			want: "UTF-8",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			r := newRepo(t)
			r.write("a.txt", "one\n")
			base := r.commit("base")
			head := test.head(r)

			_, err := r.collect(base, head)
			require.ErrorIs(t, err, git.ErrUnsupported)
			assert.Contains(t, err.Error(), test.want)
		})
	}
}

func TestCollectRejectsIdenticalCommits(t *testing.T) {
	t.Parallel()

	r := newRepo(t)
	r.write("a.txt", "one\n")
	base := r.commit("base")
	r.git("commit", "--quiet", "--allow-empty", "--message", "head")
	head := strings.TrimSpace(r.git("rev-parse", "HEAD"))

	_, err := r.collect(base, head)
	require.ErrorIs(t, err, git.ErrNoChanges)
}

func TestCollectRejectsUnresolvedRevisions(t *testing.T) {
	t.Parallel()

	r := newRepo(t)
	r.write("a.txt", "one\n")
	base := r.commit("base")
	r.write("a.txt", "two\n")
	head := r.commit("head")
	repository := r.discover()

	tests := []struct {
		name       string
		base, head string
	}{
		{name: "base is a revision", base: "main~1", head: head},
		{name: "head is a revision", base: base, head: "HEAD"},
		{name: "base is abbreviated", base: base[:8], head: head},
		{name: "head is empty", base: base, head: ""},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := repository.Collect(t.Context(), test.base, test.head)
			require.ErrorIs(t, err, git.ErrRevision)
		})
	}
}

func TestCollectRejectsOversizedChanges(t *testing.T) {
	t.Parallel()

	r := newRepo(t)
	r.write("a.txt", "one\n")
	base := r.commit("base")
	r.write("large.txt", strings.Repeat("a line of a very large change\n", 8000))
	head := r.commit("head")

	_, err := r.collect(base, head)
	require.ErrorIs(t, err, git.ErrTooLarge)
}

func TestCollectRespectsCancellation(t *testing.T) {
	t.Parallel()

	r := newRepo(t)
	r.write("a.txt", "one\n")
	base := r.commit("base")
	r.write("a.txt", "two\n")
	head := r.commit("head")

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := r.discover().Collect(ctx, base, head)
	require.ErrorIs(t, err, context.Canceled)
}
