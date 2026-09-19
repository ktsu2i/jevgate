package git

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Bounds on what a single Git operation may cost.
//
// A repository is not a trusted input: the change under review decides how
// much Git has to print, and a gate that can be made to exhaust memory or to
// hang is a gate that can be made to stop answering. The output limit is
// shared by the metadata and the patch of one comparison rather than applied
// per command, so that adding a command cannot raise the total.
const (
	// maxOutputBytes is the combined metadata and patch a single comparison
	// may read. It is the change a reviewer is expected to have read, not the
	// largest change Git can produce; 04 sends no more than this.
	maxOutputBytes = 128 << 10

	// maxStderrBytes is how much of a diagnostic is kept. Standard error only
	// explains a failure, so it is truncated rather than treated as an error
	// of its own.
	maxStderrBytes = 8 << 10

	// maxAnswerBytes is the limit for the commands that answer with a single
	// path, object ID, or flag.
	maxAnswerBytes = 4 << 10

	// operationTimeout bounds one exported operation, including every command
	// Collect runs, so that a slow repository cannot multiply the wait by the
	// number of commands. A caller with a shorter deadline keeps it.
	operationTimeout = 30 * time.Second

	// killDelay is how long a Git that has been canceled may keep its pipes
	// open before it is abandoned.
	killDelay = 5 * time.Second
)

// errCommandFailed marks a Git command that ran and exited non-zero, as
// opposed to one that could not be started at all. Only a command that ran
// says something about the repository, which is what the messages built on
// top of it rely on.
var errCommandFailed = errors.New("git command failed")

// errTooMuchOutput stops the reader once a command has written more than it
// was allowed to. It never reaches a caller: run reports ErrTooLarge instead.
var errTooMuchOutput = errors.New("output limit exceeded")

// withTimeout gives one operation its time budget. The deadline carries its
// own cause so that a caller can tell Git running out of time from the
// caller's own context being canceled.
func withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeoutCause(ctx, operationTimeout,
		fmt.Errorf("%w: the limit is %s", ErrTimeout, operationTimeout))
}

// run executes one Git command in dir and returns its standard output.
//
// The command is an argument vector and never a shell string, and -C pins
// where Git runs, so neither a revision nor a path can become a command of
// its own. Output is read under limit: a command that writes more is stopped
// and what it wrote is discarded, because half a patch is not a smaller
// change, it is a different one.
func run(ctx context.Context, dir string, limit int, args ...string) ([]byte, error) {
	if dir == "" {
		return nil, errors.New("no directory to run git in; use Discover to locate the repository")
	}

	// A command that keeps writing past the limit has to be stopped rather
	// than read to the end, so it gets a cancellation of its own.
	parent := ctx
	ctx, stop := context.WithCancel(parent)
	defer stop()

	argv := append([]string{"-C", dir, "--no-pager"}, args...)
	cmd := exec.CommandContext(ctx, "git", argv...) //nolint:gosec // The executable is fixed; only its arguments vary, and they are passed as a vector.
	cmd.Env = environment()
	stdout := &boundedBuffer{limit: limit, stop: stop}
	stderr := &boundedBuffer{limit: maxStderrBytes, truncate: true}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	// A Git that neither exits nor closes its pipes must not hold the run open
	// after the context is done.
	cmd.WaitDelay = killDelay

	err := cmd.Run()
	switch {
	case stdout.overflow:
		return nil, fmt.Errorf("%w: git %s wrote more than %d bytes", ErrTooLarge, args[0], limit)
	case err == nil:
		return stdout.out, nil
	case context.Cause(parent) != nil:
		return nil, context.Cause(parent)
	case errors.Is(err, exec.ErrNotFound):
		return nil, fmt.Errorf("%w: %w", ErrGitMissing, err)
	}

	// Git explains itself on standard error; the exit status only repeats
	// that something went wrong.
	detail := strings.TrimSpace(string(stderr.out))
	if detail == "" {
		detail = err.Error()
	}
	return nil, fmt.Errorf("%w: git %s: %s", errCommandFailed, args[0], detail)
}

// environment returns the environment for a Git command.
//
// Git inherits the caller's environment, because a repository that needs this
// machine's configuration to be readable at all must stay readable. Only the
// two settings that would let reading a diff block or write are turned off;
// what the configuration does to the diff itself is pinned on the command
// line instead, where an unknown setting cannot be silently ignored.
func environment() []string {
	return append(os.Environ(),
		// Nothing here may wait for a human: this runs in CI as often as in a
		// terminal.
		"GIT_TERMINAL_PROMPT=0",
		// Reading two commits is not a reason to take the index lock.
		"GIT_OPTIONAL_LOCKS=0",
	)
}

// boundedBuffer collects the output of a command up to a limit.
//
// Standard output either arrives whole or not at all, so exceeding the limit
// stops the command. Standard error is a diagnostic, so it is truncated and
// the command is left to finish.
type boundedBuffer struct {
	limit    int
	truncate bool
	stop     context.CancelFunc

	out      []byte
	overflow bool
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	room := b.limit - len(b.out)
	if len(p) <= room {
		b.out = append(b.out, p...)
		return len(p), nil
	}

	b.overflow = true
	if !b.truncate {
		b.stop()
		return 0, errTooMuchOutput
	}
	if room > 0 {
		b.out = append(b.out, p[:room]...)
	}
	return len(p), nil
}
