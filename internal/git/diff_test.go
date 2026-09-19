package git

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Git file modes, as the raw diff prints them.
const (
	modeNone       = "000000"
	modeRegular    = "100644"
	modeExecutable = "100755"
	modeSymlink    = "120000"
)

// writeBase builds the commit that every case in TestCollect starts from.
func writeBase(r *repo) {
	r.write("kept.txt", "unchanged\n")
	r.write("edited.txt", "one\ntwo\nthree\n")
	r.write("moved.txt", "content that moves\n")
	r.write("removed.txt", "goes away\n")
	r.write("mode.txt", "keeps its content\n")
	r.symlink("kept.txt", "link")
}

// collect compares two commits of the repository.
func (r *repo) collect(base, head string) (Diff, error) {
	r.t.Helper()

	return r.discover().Collect(r.t.Context(), base, head)
}

func TestCollect(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		head  func(r *repo)
		want  []File
		patch []string // text the patch has to contain
	}{
		{
			name: "added file",
			head: func(r *repo) { r.write("added.txt", "brand new\n") },
			want: []File{{Change: Added, NewPath: "added.txt", OldMode: modeNone, NewMode: modeRegular}},
			patch: []string{
				"new file mode 100644",
				"+brand new",
			},
		},
		{
			// An empty file carries no patch text at all, so the file list is
			// the only place the change can be seen.
			name:  "added empty file",
			head:  func(r *repo) { r.write("empty.txt", "") },
			want:  []File{{Change: Added, NewPath: "empty.txt", OldMode: modeNone, NewMode: modeRegular}},
			patch: []string{"new file mode 100644"},
		},
		{
			name:  "deleted file",
			head:  func(r *repo) { r.remove("removed.txt") },
			want:  []File{{Change: Deleted, OldPath: "removed.txt", OldMode: modeRegular, NewMode: modeNone}},
			patch: []string{"deleted file mode 100644", "-goes away"},
		},
		{
			name: "modified file",
			head: func(r *repo) { r.write("edited.txt", "one\nchanged\nthree\n") },
			want: []File{{Change: Modified, OldPath: "edited.txt", NewPath: "edited.txt", OldMode: modeRegular, NewMode: modeRegular}},
			patch: []string{
				"-two",
				"+changed",
			},
		},
		{
			// A change that only adds a space is still a change, and a diff
			// that quietly drops it would be evaluated as something else.
			name:  "whitespace only change",
			head:  func(r *repo) { r.write("edited.txt", "one \ntwo\nthree\n") },
			want:  []File{{Change: Modified, OldPath: "edited.txt", NewPath: "edited.txt", OldMode: modeRegular, NewMode: modeRegular}},
			patch: []string{"-one\n", "+one \n"},
		},
		{
			name:  "renamed file",
			head:  func(r *repo) { r.git("mv", "moved.txt", "renamed.txt") },
			want:  []File{{Change: Renamed, OldPath: "moved.txt", NewPath: "renamed.txt", OldMode: modeRegular, NewMode: modeRegular}},
			patch: []string{"rename from moved.txt", "rename to renamed.txt"},
		},
		{
			name:  "mode only change",
			head:  func(r *repo) { r.chmod("mode.txt", 0o700) },
			want:  []File{{Change: Modified, OldPath: "mode.txt", NewPath: "mode.txt", OldMode: modeRegular, NewMode: modeExecutable}},
			patch: []string{"old mode 100644", "new mode 100755"},
		},
		{
			name: "symbolic link retargeted",
			head: func(r *repo) {
				r.remove("link")
				r.symlink("edited.txt", "link")
			},
			want:  []File{{Change: Modified, OldPath: "link", NewPath: "link", OldMode: modeSymlink, NewMode: modeSymlink}},
			patch: []string{"-kept.txt", "+edited.txt"},
		},
		{
			name: "file replaced by a symbolic link",
			head: func(r *repo) {
				r.remove("kept.txt")
				r.symlink("edited.txt", "kept.txt")
			},
			want:  []File{{Change: TypeChanged, OldPath: "kept.txt", NewPath: "kept.txt", OldMode: modeRegular, NewMode: modeSymlink}},
			patch: []string{"deleted file mode 100644", "new file mode 120000"},
		},
		{
			name: "several files at once",
			head: func(r *repo) {
				r.write("added.txt", "brand new\n")
				r.remove("removed.txt")
				r.write("edited.txt", "one\nchanged\nthree\n")
			},
			want: []File{
				{Change: Added, NewPath: "added.txt", OldMode: modeNone, NewMode: modeRegular},
				{Change: Modified, OldPath: "edited.txt", NewPath: "edited.txt", OldMode: modeRegular, NewMode: modeRegular},
				{Change: Deleted, OldPath: "removed.txt", OldMode: modeRegular, NewMode: modeNone},
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

	// The file list comes from Git's NUL separated output, so a path is
	// carried through exactly as it is stored, including the characters that
	// the patch itself has to escape to stay readable.
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
		assert.Equal(t, Added, file.Change)
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

	// Comparing against the merge base would report only the added file. The
	// reviewer approved the head as a replacement for the base, so what the
	// head lacks is part of the change.
	assert.Equal(t, []File{
		{Change: Deleted, OldPath: "base-only.txt", OldMode: modeRegular, NewMode: modeNone},
		{Change: Added, NewPath: "head-only.txt", OldMode: modeNone, NewMode: modeRegular},
	}, diff.Files)
}

func TestCollectIgnoresTheWorkingTree(t *testing.T) {
	t.Parallel()

	r := newRepo(t)
	r.write("committed.txt", "one\n")
	base := r.commit("base")
	r.write("committed.txt", "two\n")
	head := r.commit("head")

	// None of this is part of either commit, and a comparison of two commits
	// that reported it would evaluate a change nobody proposed.
	r.write("committed.txt", "edited in the working tree\n")
	r.write("staged.txt", "staged but not committed\n")
	r.git("add", "staged.txt")
	r.write("untracked.txt", "never added\n")
	// An attribute file that is not committed either: the attributes that
	// decide how a diff is shown come from the head commit.
	r.write(".gitattributes", "*.txt -diff\n")

	diff, err := r.collect(base, head)
	require.NoError(t, err)

	assert.Equal(t, []File{
		{Change: Modified, OldPath: "committed.txt", NewPath: "committed.txt", OldMode: modeRegular, NewMode: modeRegular},
	}, diff.Files)
	assert.Contains(t, diff.Patch, "+two")
	assert.NotContains(t, diff.Patch, "working tree")
}

// sentinelProgram writes a program that records having been run, and returns
// it together with the file it would create.
func sentinelProgram(t *testing.T) (program, sentinel string) {
	t.Helper()

	dir := t.TempDir()
	program = filepath.Join(dir, "record.sh")
	sentinel = filepath.Join(dir, "ran")
	body := "#!/bin/sh\necho ran > \"" + sentinel + "\"\n"
	require.NoError(t, os.WriteFile(program, []byte(body), 0o700), "writing the sentinel program")
	return program, sentinel
}

// assertNotRun fails when the sentinel program left its mark.
func assertNotRun(t *testing.T, sentinel string) {
	t.Helper()

	_, err := os.Stat(sentinel)
	assert.ErrorIs(t, err, os.ErrNotExist, "reading a diff must not run a program the repository configured")
}

func TestCollectDoesNotRunConfiguredPrograms(t *testing.T) {
	t.Parallel()

	program, sentinel := sentinelProgram(t)

	r := newRepo(t)
	// A repository can configure a program for every file it shows. The
	// change under review is data to be read, never something to execute.
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

// TestCollectDoesNotRunTheEnvironmentsDiffProgram cannot run in parallel: it
// sets the variable for the whole process, which is the only way Git reads it.
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
		want      string // text the diagnostic has to explain
	}{
		{
			// "-diff" turns a text file into "Binary files differ", which
			// would be sent for evaluation as a change with no content.
			name:      "attribute hides the contents",
			configure: func(r *repo) { r.write(".gitattributes", "secret.txt -diff\n") },
			want:      "-diff",
		},
		{
			// A named driver decides for itself what a diff of the file looks
			// like, including declaring it binary.
			name:      "attribute names a diff driver",
			configure: func(r *repo) { r.write(".gitattributes", "secret.txt diff=sentinel\n") },
			want:      "sentinel",
		},
		{
			// The same attribute set where only this checkout can see it. It
			// is not part of either commit, but Git still applies it, so
			// reading the committed .gitattributes alone would miss it.
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
			require.ErrorIs(t, err, ErrUnsupported)
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
		head func(r *repo) string // records the head commit
		want string               // text the diagnostic has to explain
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
				// A gitlink records another repository's commit. Its own
				// change is not in this diff, so it cannot be evaluated here.
				r.git("update-index", "--add", "--cacheinfo", "160000,"+strings.Repeat("0", 39)+"1,vendor")
				return r.commitIndex("head")
			},
			want: "submodule",
		},
		{
			name: "path that is not valid UTF-8",
			head: func(r *repo) string {
				// The path never reaches the file system: encoding it for the
				// API would replace the bytes with U+FFFD, and the evaluated
				// path would no longer be the one that changed.
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
			require.ErrorIs(t, err, ErrUnsupported)
			assert.Contains(t, err.Error(), test.want)
		})
	}
}

func TestCollectRejectsIdenticalCommits(t *testing.T) {
	t.Parallel()

	r := newRepo(t)
	r.write("a.txt", "one\n")
	base := r.commit("base")
	// A commit that changes nothing but its message has the same contents.
	r.git("commit", "--quiet", "--allow-empty", "--message", "head")
	head := strings.TrimSpace(r.git("rev-parse", "HEAD"))

	_, err := r.collect(base, head)
	require.ErrorIs(t, err, ErrNoChanges)
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

			// Resolving happens once, before anything is compared: a branch
			// that moves between two commands would otherwise let one run
			// compare two different pairs of commits.
			_, err := repository.Collect(t.Context(), test.base, test.head)
			require.ErrorIs(t, err, ErrRevision)
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
	require.ErrorIs(t, err, ErrTooLarge)
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

func TestRecords(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		data string
		want []string
	}{
		{name: "empty", data: "", want: nil},
		{name: "one record", data: "a\x00", want: []string{"a"}},
		{name: "several records", data: "a\x00b\x00", want: []string{"a", "b"}},
		{name: "empty record", data: "a\x00\x00b\x00", want: []string{"a", "", "b"}},
		{name: "no terminator", data: "a", want: []string{"a"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, test.want, records([]byte(test.data)))
		})
	}
}

func TestParseRawRejectsMalformedOutput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		data string
	}{
		{name: "no header", data: "a.txt\x00"},
		{name: "short header", data: ":100644 100644 M\x00a.txt\x00"},
		{name: "missing path", data: ":100644 100644 aaaaaaa bbbbbbb M\x00"},
		{name: "missing second path", data: ":100644 100644 aaaaaaa bbbbbbb R100\x00old.txt\x00"},
		{name: "unmerged", data: ":100644 100644 aaaaaaa bbbbbbb U\x00a.txt\x00"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := parseRaw([]byte(test.data))
			require.Error(t, err, "output that cannot be read must not become a partial file list")
		})
	}
}
