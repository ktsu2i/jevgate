package jev

import (
	"context"
	"math"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRetryAfter(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 19, 1, 0, 0, 0, time.UTC)
	for _, tt := range []struct {
		header string
		want   time.Duration
	}{
		{"", 0}, {"0", 0}, {" 2 ", 2 * time.Second},
		{"-1", 0}, {"1.5", 0}, {"invalid", 0},
		{now.Add(3 * time.Second).Format(http.TimeFormat), 3 * time.Second},
		{now.Add(-3 * time.Second).Format(http.TimeFormat), 0},
		{"999999999999999999999999999999", time.Duration(math.MaxInt64)},
		{"9223372037", time.Duration(math.MaxInt64)},
	} {
		assert.Equal(t, tt.want, retryAfter(tt.header, now), tt.header)
	}
}

func TestWaitRetry(t *testing.T) {
	t.Parallel()
	require.NoError(t, waitRetry(context.Background(), time.Millisecond))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, waitRetry(ctx, time.Hour), context.Canceled)
}
