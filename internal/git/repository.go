package git

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

var (
	// ErrGitMissing means Git could not be run.
	ErrGitMissing = errors.New("git is not installed or is not in PATH")

	// ErrNotRepository means no Git work tree was found.
	ErrNotRepository = errors.New("not inside a Git repository")

	// ErrRevision means a revision could not be resolved to a commit.
	ErrRevision = errors.New("unusable revision")

	// ErrNoChanges means the commits have identical contents.
	ErrNoChanges = errors.New("there is no change to evaluate")

	// ErrUnsupported means a change cannot be represented faithfully.
	ErrUnsupported = errors.New("the change cannot be evaluated")

	// ErrTooLarge means the change exceeds an output limit.
	ErrTooLarge = errors.New("the change is too large to evaluate")

	// ErrTimeout means Git exceeded the operation deadline.
	ErrTimeout = errors.New("git did not finish in time")
)

const (
	sha1Length   = 40
	sha256Length = 64
)

// Repository is a discovered Git work tree.
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

	// Preserve trailing spaces because they may be part of the path.
	root := strings.TrimSuffix(string(out), "\n")
	if root == "" {
		return Repository{}, fmt.Errorf("%w: %s is in a Git repository that has no work tree", ErrNotRepository, cwd)
	}
	return Repository{root: root}, nil
}

// Root returns the absolute repository root.
func (r Repository) Root() string {
	return r.root
}

// ResolveCommit resolves a revision to a stable commit object ID.
func (r Repository) ResolveCommit(ctx context.Context, revision string) (string, error) {
	ctx, cancel := withTimeout(ctx)
	defer cancel()

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

func (r Repository) explain(ctx context.Context, err error) error {
	if !errors.Is(err, errCommandFailed) || !r.isShallow(ctx) {
		return err
	}
	return fmt.Errorf("%w; the repository is a shallow clone and may be missing history, so fetch it before evaluating", err)
}

func (r Repository) isShallow(ctx context.Context) bool {
	out, err := run(ctx, r.root, maxAnswerBytes, "rev-parse", "--is-shallow-repository")
	return err == nil && strings.TrimSuffix(string(out), "\n") == "true"
}

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
