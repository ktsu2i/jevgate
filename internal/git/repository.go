// Package git collects the change between two commits of a Git repository.
//
// The package only reads. It never writes to the repository, checks anything
// out, or runs a program that the change under review could choose: external
// diff drivers and textconv filters are disabled, commands are argument
// vectors rather than shell strings, and the attributes that decide how a
// diff is rendered are read from the head commit instead of from the working
// tree. What it reports is therefore a property of the two commits alone, and
// neither an edited working tree nor a reviewer's configuration can change
// the change that is evaluated.
//
// Git 2.40 or newer is required, for pinning the attribute source of a diff
// to a commit.
package git

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Errors reported by this package. They name the different reasons to stop so
// that the caller can say which one applies: an unusable repository, an
// unusable revision, and a change this tool cannot evaluate are three
// different things to tell someone, and none of them is a decision about the
// change.
var (
	// ErrGitMissing reports that Git itself could not be run.
	ErrGitMissing = errors.New("git is not installed or is not in PATH")

	// ErrNotRepository reports that there is no Git work tree to read.
	ErrNotRepository = errors.New("not inside a Git repository")

	// ErrRevision reports a revision Git cannot resolve to a commit, which
	// includes history a shallow clone does not have.
	ErrRevision = errors.New("unusable revision")

	// ErrNoChanges reports that the two commits have the same contents.
	ErrNoChanges = errors.New("there is no change to evaluate")

	// ErrUnsupported reports a change this tool cannot represent faithfully,
	// such as a binary file or a submodule.
	ErrUnsupported = errors.New("the change cannot be evaluated")

	// ErrTooLarge reports that reading the change would exceed the limits.
	ErrTooLarge = errors.New("the change is too large to evaluate")

	// ErrTimeout reports that Git did not finish within the time budget.
	ErrTimeout = errors.New("git did not finish in time")
)

// Hexadecimal lengths of a Git object ID, for the two hash algorithms Git
// supports. A repository may use either, so an ID is not assumed to be 40
// characters.
const (
	sha1Length   = 40
	sha256Length = 64
)

// Repository is a Git work tree that Discover has located. The zero value has
// no root and cannot run commands.
type Repository struct {
	root string
}

// Discover locates the repository that contains cwd.
func Discover(ctx context.Context, cwd string) (Repository, error) {
	if cwd == "" {
		return Repository{}, errors.New("a working directory is required to discover the repository")
	}

	ctx, cancel := withTimeout(ctx)
	defer cancel()

	out, err := run(ctx, cwd, maxAnswerBytes, "rev-parse", "--show-toplevel")
	if err != nil {
		if errors.Is(err, errCommandFailed) {
			return Repository{}, fmt.Errorf("%w: %s: %w", ErrNotRepository, cwd, err)
		}
		return Repository{}, err
	}

	// Only the terminating newline is removed: a repository may well live in
	// a directory whose name ends in a space.
	root := strings.TrimSuffix(string(out), "\n")
	if root == "" {
		return Repository{}, fmt.Errorf("%w: %s is in a Git repository that has no work tree", ErrNotRepository, cwd)
	}
	return Repository{root: root}, nil
}

// Root is the absolute path of the repository root. It is where the
// configuration file is discovered and where every Git command runs.
func (r Repository) Root() string {
	return r.root
}

// ResolveCommit resolves a revision to the object ID of a commit.
//
// Everything Git accepts is accepted here, including branches, tags, and
// expressions such as HEAD~1, but the result is always a commit: a revision
// that names a tree or a blob is rejected rather than compared, because a
// diff of something that is not a commit is not the change anyone reviewed.
// The returned ID is what the rest of the run uses, so that a branch moving
// mid-run cannot mix two different comparisons.
func (r Repository) ResolveCommit(ctx context.Context, revision string) (string, error) {
	ctx, cancel := withTimeout(ctx)
	defer cancel()

	// --end-of-options keeps a revision that begins with a dash from being
	// read as an option, --verify demands exactly one object, and ^{commit}
	// peels a tag and refuses anything that is not a commit.
	out, err := run(ctx, r.root, maxAnswerBytes, "rev-parse", "--verify", "--end-of-options", revision+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("%w %q: %w", ErrRevision, revision, r.explain(ctx, err))
	}

	id := strings.TrimSuffix(string(out), "\n")
	if !isObjectID(id) {
		return "", fmt.Errorf("%w %q: git answered %q, which is not an object ID", ErrRevision, revision, id)
	}
	return id, nil
}

// explain adds what the repository can say about a Git failure.
//
// An object that a truncated clone never received looks exactly like an
// object that does not exist, and the difference decides whether the caller
// should fetch more history or correct the revision. The history is never
// fetched here: a gate that reaches the network to make a change evaluable
// would be deciding what it is allowed to see.
func (r Repository) explain(ctx context.Context, err error) error {
	if !errors.Is(err, errCommandFailed) || !r.isShallow(ctx) {
		return err
	}
	return fmt.Errorf("%w; the repository is a shallow clone and may be missing history, so fetch it before evaluating", err)
}

// isShallow reports whether the repository was cloned with a truncated
// history. It only ever explains a failure, so a repository that cannot
// answer is reported as not shallow rather than replacing the failure this
// was meant to describe.
func (r Repository) isShallow(ctx context.Context) bool {
	out, err := run(ctx, r.root, maxAnswerBytes, "rev-parse", "--is-shallow-repository")
	return err == nil && strings.TrimSuffix(string(out), "\n") == "true"
}

// isObjectID reports whether id is a full Git object ID.
func isObjectID(id string) bool {
	if len(id) != sha1Length && len(id) != sha256Length {
		return false
	}
	for i := range len(id) {
		switch c := id[i]; {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f':
		default:
			return false
		}
	}
	return true
}
