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

const (
	Added    Change = "added"
	Deleted  Change = "deleted"
	Modified Change = "modified"
	Renamed  Change = "renamed"
	Copied   Change = "copied"
	// TypeChanged changes the Git object type.
	TypeChanged Change = "type changed"
)

const modeGitlink = "160000"

var errMalformedOutput = errors.New("git produced output this tool cannot read")

// File is one file changed between the two commits.
type File struct {
	// Change identifies the operation.
	Change Change

	// OldPath and NewPath identify each side of the change.
	OldPath string
	NewPath string

	// OldMode and NewMode are Git file modes.
	OldMode string
	NewMode string

	// Binary reports that Git has no textual diff for the file.
	Binary bool
}

// Path returns the surviving path, or the old path for a deletion.
func (f *File) Path() string {
	if f.NewPath != "" {
		return f.NewPath
	}
	return f.OldPath
}

// Diff is the complete change between two commits.
type Diff struct {
	// BaseID and HeadID are the compared commit object IDs.
	BaseID string
	HeadID string

	// Files lists every changed file, in the order Git reported them.
	Files []File

	// Patch is the unified diff of the same change.
	Patch string
}

// Collect returns the direct change between two resolved commits.
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

func (c *collector) diff(ctx context.Context, args ...string) ([]byte, error) {
	return c.run(ctx, slices.Concat(diffArgs(c.headID), args, []string{c.baseID, c.headID, "--"})...)
}

func diffArgs(headID string) []string {
	return []string{
		// Pin attributes so the working tree cannot alter the reviewed diff.
		"-c", "attr.tree=" + headID,
		"-c", "core.quotePath=false",
		"diff",
		// The repository under review must not execute code.
		"--no-ext-diff",
		"--no-textconv",
		"--no-color",
		"--no-relative",
		"--find-renames",
		"--diff-algorithm=myers",
	}
}

func patchArgs() []string {
	return []string{
		"--patch",
		"--unified=3",
		"--inter-hunk-context=0",
		"--src-prefix=a/",
		"--dst-prefix=b/",
	}
}

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

func (c *collector) patch(ctx context.Context, files []File) (string, error) {
	// Check attributes first so a hidden text diff is not misreported as binary.
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

const (
	rawHeaderFields = 5
	numstatFields   = 3
	attrFields      = 3
)

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
		default:
			file.OldPath, file.NewPath = paths[0], paths[0]
		}
		files = append(files, file)
	}
	return files, nil
}

func parseRawHeader(header string) (File, int, error) {
	body, isHeader := strings.CutPrefix(header, ":")
	if !isHeader {
		return File{}, 0, fmt.Errorf("%w: %q is not a raw diff header", errMalformedOutput, header)
	}
	fields := strings.Fields(body)
	if len(fields) != rawHeaderFields {
		return File{}, 0, fmt.Errorf("%w: the raw diff header %q has %d field(s), want %d", errMalformedOutput, header, len(fields), rawHeaderFields)
	}

	change, paths, err := changeOf(fields[rawHeaderFields-1][0])
	if err != nil {
		return File{}, 0, err
	}
	return File{Change: change, OldMode: fields[0], NewMode: fields[1]}, paths, nil
}

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
			if i+1 >= len(fields) {
				return nil, fmt.Errorf("%w: the numstat record %q is missing its paths", errMalformedOutput, record)
			}
			path = fields[i+1]
			i += 2
		}
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
		case "unset":
			return fmt.Errorf("%w: the -diff attribute hides the contents of %q, so its change cannot be read", ErrUnsupported, path)
		default:
			return fmt.Errorf("%w: %q uses the diff driver %q, and what that driver shows cannot be verified here", ErrUnsupported, path, value)
		}
	}
	return nil
}

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

func records(data []byte) []string {
	text := strings.TrimSuffix(string(data), "\x00")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\x00")
}
