package git

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"
)

// Change classifies what happened to one file between the two commits.
type Change string

// The changes Git reports between two commits.
const (
	Added    Change = "added"
	Deleted  Change = "deleted"
	Modified Change = "modified"
	Renamed  Change = "renamed"
	Copied   Change = "copied"
	// TypeChanged is a change between a regular file, a symbolic link, and a
	// submodule, which Git reports apart from a change of contents.
	TypeChanged Change = "type changed"
)

// modeGitlink is the file mode Git prints for a submodule.
const modeGitlink = "160000"

// errMalformedOutput reports output from Git that this package cannot read.
// It means the two commands that described one change disagreed, or that the
// format changed; either way the change is not known in full, which is a
// reason to stop rather than to report the part that was understood.
var errMalformedOutput = errors.New("git produced output this tool cannot read")

// File is one file changed between the two commits.
//
// The metadata comes from Git's raw diff and not from the patch: the patch is
// written to be read by a person, and recovering the file list from its
// headers would guess at something Git has already answered exactly.
type File struct {
	// Change is what happened to the file.
	Change Change

	// OldPath and NewPath are the paths on each side of the change. An added
	// file has no old path and a deleted file has no new path; a rename or a
	// copy has two different ones.
	OldPath string
	NewPath string

	// OldMode and NewMode are the file modes as Git prints them: "100644" for
	// a regular file, "100755" for an executable one, "120000" for a symbolic
	// link, "160000" for a submodule, and "000000" for the side that does not
	// exist. A change that only moves a file or only flips its mode carries
	// no patch text, so the modes are how such a change is described at all.
	OldMode string
	NewMode string

	// Binary reports that Git has no textual diff for the file.
	Binary bool
}

// Path is the file's path in the head commit, or the path it had in the base
// commit when it was deleted.
func (f *File) Path() string {
	if f.NewPath != "" {
		return f.NewPath
	}
	return f.OldPath
}

// Diff is the complete change between two commits.
type Diff struct {
	// BaseID and HeadID are the commits that were compared, as object IDs.
	// They are what was actually read, not the revisions someone typed.
	BaseID string
	HeadID string

	// Files lists every changed file, in the order Git reported them.
	Files []File

	// Patch is the unified diff of the same change.
	Patch string
}

// Collect returns the change between two commits.
//
// Both arguments are object IDs from ResolveCommit rather than revisions: a
// branch that moved between two of the commands below would otherwise let one
// run compare two different pairs of commits. The comparison is direct,
// between the two commits themselves, and never against their merge base:
// what a reviewer approves is the difference from the base that was named,
// not the difference from wherever the branches happened to part.
func (r Repository) Collect(ctx context.Context, baseID, headID string) (Diff, error) {
	if !isObjectID(baseID) {
		return Diff{}, fmt.Errorf("%w %q: the base must be a resolved object ID", ErrRevision, baseID)
	}
	if !isObjectID(headID) {
		return Diff{}, fmt.Errorf("%w %q: the head must be a resolved object ID", ErrRevision, headID)
	}

	ctx, cancel := withTimeout(ctx)
	defer cancel()

	collect := &collector{root: r.root, baseID: baseID, headID: headID, remaining: maxOutputBytes}

	files, err := collect.files(ctx)
	if err != nil {
		return Diff{}, r.explain(ctx, err)
	}

	patch, err := collect.patch(ctx, files)
	if err != nil {
		return Diff{}, err
	}

	return Diff{BaseID: baseID, HeadID: headID, Files: files, Patch: patch}, nil
}

// collector runs the Git commands of one Collect call.
//
// It exists to keep the output budget in one place: the metadata and the
// patch share a single limit, so every command has to account for what the
// commands before it already spent.
type collector struct {
	root      string
	baseID    string
	headID    string
	remaining int
}

func (c *collector) run(ctx context.Context, args ...string) ([]byte, error) {
	out, err := run(ctx, c.root, c.remaining, args...)
	c.remaining -= len(out)
	return out, err
}

// diff runs one form of the comparison. The two commits are always passed the
// same way, and a trailing "--" keeps an object ID from being read as a path.
func (c *collector) diff(ctx context.Context, args ...string) ([]byte, error) {
	return c.run(ctx, slices.Concat(diffArgs(c.headID), args, []string{c.baseID, c.headID, "--"})...)
}

// diffArgs are the options every comparison shares.
//
// They pin everything about the output that Git would otherwise take from
// configuration or from the working tree. A gate whose answer depends on the
// reviewer's ~/.gitconfig is not a gate, and a repository must not be able to
// decide how much of its own change is shown.
func diffArgs(headID string) []string {
	return []string{
		// Attributes decide whether a file has a textual diff at all.
		// Reading them from the head commit keeps an edited or untracked
		// .gitattributes out of a comparison of two commits.
		"-c", "attr.tree=" + headID,
		// Paths are written as they are rather than escaped, so the patch
		// reads the same as the repository.
		"-c", "core.quotePath=false",
		"diff",
		// No program that the repository configures may run while a diff is
		// read. The change under review is data, never something to execute.
		"--no-ext-diff",
		"--no-textconv",
		// The rest is fixed so that the same two commits always produce the
		// same bytes, whatever the repository or the reviewer configured.
		"--no-color",
		"--no-relative",
		"--find-renames",
		"--diff-algorithm=myers",
	}
}

// patchArgs are the options that shape the patch text.
//
// They are kept apart from the options every command shares because asking
// Git for a number of context lines also asks it for a patch, and the
// commands that read metadata must receive metadata alone.
func patchArgs() []string {
	return []string{
		"--patch",
		"--unified=3",
		"--inter-hunk-context=0",
		"--src-prefix=a/",
		"--dst-prefix=b/",
	}
}

// files lists what changed between the two commits.
//
// The list comes from Git's own description of the change and not from the
// patch: a patch is written to be read by a person, and recovering the file
// list from its headers would guess at something Git has already answered.
func (c *collector) files(ctx context.Context) ([]File, error) {
	raw, err := c.diff(ctx, "--raw", "-z")
	if err != nil {
		return nil, err
	}
	files, err := parseRaw(raw)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("%w: %s and %s have the same contents", ErrNoChanges, c.baseID, c.headID)
	}

	numstat, err := c.diff(ctx, "--numstat", "-z")
	if err != nil {
		return nil, err
	}
	return applyNumstat(files, numstat)
}

// patch returns the unified diff, once the change is known to be one that can
// be shown in full.
func (c *collector) patch(ctx context.Context, files []File) (string, error) {
	// The attributes are checked before the files themselves, because an
	// attribute that hides a text diff makes Git report the file as binary,
	// and "this file is binary" would then be the wrong explanation.
	if err := c.checkAttributes(ctx, files); err != nil {
		return "", err
	}
	if err := checkSupported(files); err != nil {
		return "", err
	}

	patch, err := c.diff(ctx, patchArgs()...)
	if err != nil {
		return "", err
	}
	if !utf8.Valid(patch) {
		return "", fmt.Errorf("%w: the patch is not valid UTF-8, and encoding it for the API would replace the characters that were changed", ErrUnsupported)
	}
	return string(patch), nil
}

// Fields of the records Git prints for each changed file.
const (
	rawHeaderFields = 5 // old mode, new mode, old ID, new ID, status
	numstatFields   = 3 // added, deleted, path
	attrFields      = 3 // path, attribute, value
)

// parseRaw reads the raw diff: a NUL terminated header per changed file,
// followed by the one or two paths that header applies to.
func parseRaw(data []byte) ([]File, error) {
	fields := records(data)

	var files []File
	for i := 0; i < len(fields); {
		header := fields[i]
		file, wanted, err := parseRawHeader(header)
		if err != nil {
			return nil, err
		}

		paths := fields[i+1:]
		if len(paths) < wanted {
			return nil, fmt.Errorf("%w: %q is followed by %d path(s), want %d", errMalformedOutput, header, len(paths), wanted)
		}
		paths = paths[:wanted]
		i += 1 + wanted

		switch file.Change {
		case Added:
			file.NewPath = paths[0]
		case Deleted:
			file.OldPath = paths[0]
		case Renamed, Copied:
			file.OldPath, file.NewPath = paths[0], paths[1]
		default: // Modified and TypeChanged keep the one path they have.
			file.OldPath, file.NewPath = paths[0], paths[0]
		}
		files = append(files, file)
	}
	return files, nil
}

// parseRawHeader reads one ":<old mode> <new mode> <old ID> <new ID> <status>"
// header and reports how many paths belong to it.
func parseRawHeader(header string) (File, int, error) {
	body, isHeader := strings.CutPrefix(header, ":")
	if !isHeader {
		return File{}, 0, fmt.Errorf("%w: %q is not a raw diff header", errMalformedOutput, header)
	}
	fields := strings.Fields(body)
	if len(fields) != rawHeaderFields {
		return File{}, 0, fmt.Errorf("%w: the raw diff header %q has %d field(s), want %d", errMalformedOutput, header, len(fields), rawHeaderFields)
	}

	// A rename or a copy appends a similarity score to its status letter.
	change, paths, err := changeOf(fields[rawHeaderFields-1][0])
	if err != nil {
		return File{}, 0, err
	}
	return File{Change: change, OldMode: fields[0], NewMode: fields[1]}, paths, nil
}

// changeOf translates a raw diff status letter and reports how many paths it
// is printed with.
func changeOf(status byte) (Change, int, error) {
	switch status {
	case 'A':
		return Added, 1, nil
	case 'D':
		return Deleted, 1, nil
	case 'M':
		return Modified, 1, nil
	case 'T':
		return TypeChanged, 1, nil
	case 'R':
		return Renamed, 2, nil
	case 'C':
		return Copied, 2, nil
	default:
		return "", 0, fmt.Errorf("%w: git reported the change %q, which this tool cannot describe", ErrUnsupported, string(status))
	}
}

// applyNumstat marks the files Git has no textual diff for.
//
// Whether a file is binary is Git's answer and not a guess from the patch
// text. A file that one command listed and the other did not means the two
// did not see the same change, which is a reason to stop.
func applyNumstat(files []File, data []byte) ([]File, error) {
	fields := records(data)

	binary := make(map[string]bool, len(files))
	for i := 0; i < len(fields); {
		record := fields[i]
		i++

		counts := strings.SplitN(record, "\t", numstatFields)
		if len(counts) != numstatFields {
			return nil, fmt.Errorf("%w: %q is not a numstat record", errMalformedOutput, record)
		}

		path := counts[2]
		if path == "" {
			// A rename or a copy prints its two paths as separate records.
			if i+1 >= len(fields) {
				return nil, fmt.Errorf("%w: the numstat record %q is missing its paths", errMalformedOutput, record)
			}
			path = fields[i+1]
			i += 2
		}
		// Git prints a dash for each side it cannot count, which is how it
		// says the file has no textual diff.
		binary[path] = counts[0] == "-" && counts[1] == "-"
	}

	if len(binary) != len(files) {
		return nil, fmt.Errorf("%w: git listed %d changed file(s) but counted %d", errMalformedOutput, len(files), len(binary))
	}
	for i, file := range files {
		isBinary, counted := binary[file.Path()]
		if !counted {
			return nil, fmt.Errorf("%w: git listed %q as changed but did not count it", errMalformedOutput, file.Path())
		}
		files[i].Binary = isBinary
	}
	return files, nil
}

// checkAttributes rejects the files whose diff Git would not show in full.
//
// Disabling external diff drivers and textconv filters is not enough to know
// that a patch is complete. A "-diff" attribute makes Git treat a text file
// as binary, and a named diff driver can declare its files binary in the
// user's configuration, both of which turn a real change into "Binary files
// differ" without failing. The attributes are read from the head commit, the
// same source the diff uses, so the answer describes the commits rather than
// whatever the working tree currently holds.
func (c *collector) checkAttributes(ctx context.Context, files []File) error {
	paths := make([]string, 0, len(files))
	seen := make(map[string]bool, len(files))
	for _, file := range files {
		for _, path := range []string{file.OldPath, file.NewPath} {
			if path == "" || seen[path] {
				continue
			}
			seen[path] = true
			paths = append(paths, path)
		}
	}

	args := []string{"check-attr", "-z", "--source=" + c.headID, "diff", "--"}
	out, err := c.run(ctx, slices.Concat(args, paths)...)
	if err != nil {
		return err
	}

	fields := records(out)
	if len(fields) != len(paths)*attrFields {
		return fmt.Errorf("%w: git answered with %d attribute field(s) for %d path(s)", errMalformedOutput, len(fields), len(paths))
	}
	for i := 0; i < len(fields); i += attrFields {
		path, value := fields[i], fields[i+2]
		switch value {
		case "unspecified", "set":
			// No attribute, or one that asks for the ordinary text diff.
		case "unset":
			return fmt.Errorf("%w: the -diff attribute hides the contents of %q, so its change cannot be read", ErrUnsupported, path)
		default:
			return fmt.Errorf("%w: %q uses the diff driver %q, and what that driver shows cannot be verified here", ErrUnsupported, path, value)
		}
	}
	return nil
}

// checkSupported rejects the changes this tool has no honest answer for.
//
// A change that cannot be shown in full is not a change that may be approved,
// so each of these ends the run. Changes that carry no patch text of their
// own, such as a rename, a mode change, or an edited symbolic link, are not
// among them: their metadata describes them completely and is sent on.
func checkSupported(files []File) error {
	for _, file := range files {
		switch {
		case file.OldMode == modeGitlink || file.NewMode == modeGitlink:
			return fmt.Errorf("%w: %q is a submodule, and a submodule's own change is not part of this diff", ErrUnsupported, file.Path())
		case file.Binary:
			return fmt.Errorf("%w: %q is binary, and this tool evaluates text changes only", ErrUnsupported, file.Path())
		case !utf8.ValidString(file.OldPath) || !utf8.ValidString(file.NewPath):
			return fmt.Errorf("%w: the path %q is not valid UTF-8, and encoding it for the API would change it", ErrUnsupported, file.Path())
		}
	}
	return nil
}

// records splits Git's NUL terminated output into its fields.
func records(data []byte) []string {
	text := strings.TrimSuffix(string(data), "\x00")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\x00")
}
