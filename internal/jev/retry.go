package jev

import (
	"context"
	"math"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	maxAttempts = 3
	baseBackoff = 500 * time.Millisecond
)

func retryable(status int) bool {
	switch status {
	case 429, 529, 502, 503, 504:
		return true
	default:
		return false
	}
}

// retryDelay never shortens Retry-After. Positive jitter avoids synchronized
// retries while retaining the 0.5s, 1s exponential minimum delays.
func retryDelay(attempt int, header string, now time.Time) time.Duration {
	backoff := baseBackoff << attempt
	// This jitter spreads retry load; it does not generate a secret or make a
	// security decision, so a cryptographic random source is unnecessary.
	delay := backoff + time.Duration(rand.Int64N(int64(backoff/2))) //nolint:gosec // Retry jitter is not security-sensitive randomness.
	if after := retryAfter(header, now); after > delay {
		delay = after
	}
	return delay
}

func retryAfter(header string, now time.Time) time.Duration {
	header = strings.TrimSpace(header)
	if header == "" {
		return 0
	}
	// Parse decimal digits explicitly so enormous valid delays cannot overflow
	// a duration and become an immediate retry. Invalid headers use backoff.
	if strings.IndexFunc(header, func(r rune) bool { return r < '0' || r > '9' }) == -1 {
		seconds, err := strconv.ParseUint(header, 10, 64)
		if err != nil || seconds > uint64(math.MaxInt64/int64(time.Second)) {
			return time.Duration(math.MaxInt64)
		}
		return time.Duration(seconds) * time.Second
	}
	if date, err := http.ParseTime(header); err == nil && date.After(now) {
		return date.Sub(now)
	}
	return 0
}

func waitRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
