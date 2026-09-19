package jev_test

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAssessRetryBeyondDeadline(t *testing.T) {
	t.Parallel()
	for _, header := range []string{"100", "9999999999999999999999999999"} {
		t.Run(header, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			client := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.Header().Set("Retry-After", header)
				w.WriteHeader(529)
			})
			_, err := client.Assess(context.Background(), testDiff(), "")
			require.ErrorIs(t, err, context.DeadlineExceeded)
			require.ErrorContains(t, err, "HTTP 529")
			assert.EqualValues(t, 1, calls.Load())
		})
	}
}

func TestAssessCancellationDuringRetryWait(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls atomic.Int32
	waiting := make(chan struct{})
	client := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
		close(waiting)
	})
	done := make(chan error, 1)
	go func() {
		_, err := client.Assess(ctx, testDiff(), "")
		done <- err
	}()
	select {
	case <-waiting:
	case <-time.After(3 * time.Second):
		t.Fatal("did not reach retry wait")
	}
	cancel()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("retry wait ignored cancellation")
	}
	assert.EqualValues(t, 1, calls.Load())
}
