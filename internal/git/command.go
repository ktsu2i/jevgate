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

const (
	maxOutputBytes   = 128 << 10
	maxStderrBytes   = 8 << 10
	maxAnswerBytes   = 4 << 10
	operationTimeout = 30 * time.Second
	killDelay        = 5 * time.Second
)

var errCommandFailed = errors.New("git command failed")

var errTooMuchOutput = errors.New("output limit exceeded")

func withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeoutCause(ctx, operationTimeout,
		fmt.Errorf("%w: the limit is %s", ErrTimeout, operationTimeout))
}

func run(ctx context.Context, dir string, limit int, args ...string) ([]byte, error) {
	if dir == "" {
		return nil, errors.New("no directory to run git in; use Discover to locate the repository")
	}

	// Cancel immediately on overflow; a partial patch must never be evaluated.
	parent := ctx
	ctx, stop := context.WithCancel(parent)
	defer stop()

	argv := append([]string{"-C", dir, "--no-pager"}, args...)
	cmd := exec.CommandContext(ctx, "git", argv...) //nolint:gosec // The executable is fixed; only its arguments vary, and they are passed as a vector.
	cmd.Env = environment()
	stdout := &boundedBuffer{limit: limit, stop: stop}
	stderr := &boundedBuffer{limit: maxStderrBytes, truncate: true}
	cmd.Stdout, cmd.Stderr = stdout, stderr
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

	detail := strings.TrimSpace(string(stderr.out))
	if detail == "" {
		detail = err.Error()
	}
	return nil, fmt.Errorf("%w: git %s: %s", errCommandFailed, args[0], detail)
}

func environment() []string {
	return append(os.Environ(),
		// CI must not wait for interactive credentials.
		"GIT_TERMINAL_PROMPT=0",
		"GIT_OPTIONAL_LOCKS=0",
	)
}

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
