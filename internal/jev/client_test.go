package jev

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ktsu2i/jevgate/internal/git"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testKey = "secret-key-sentinel"

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	require.NoError(t, err)
	return data
}

func testDiff() git.Diff {
	return git.Diff{
		BaseID: "base-sha-not-sent", HeadID: "head-sha-not-sent",
		Files: []git.File{{Change: git.Renamed, OldPath: "old name.txt", NewPath: "日本語\n\"new\".txt", OldMode: "100644", NewMode: "100755"}},
		Patch: "diff-sentinel\n-tyop\n+typo\n",
	}
}

func testClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client := NewClient(testKey, server.Client())
	client.endpoint = server.URL
	return client
}

func TestAssessRequest(t *testing.T) {
	t.Parallel()
	want := fixture(t, "request.json")
	response := fixture(t, "response.json")
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "Bearer "+testKey, r.Header.Get("Authorization"))
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		data, err := io.ReadAll(r.Body)
		assert.NoError(t, err)
		assert.JSONEq(t, string(want), string(data))
		fmt.Fprint(w, string(response))
	})
	got, err := client.Assess(context.Background(), testDiff(), "context-sentinel: docs/ contains documentation.")
	require.NoError(t, err)
	assert.Equal(t, Assessment{AIApprovalAllowedProbability: 0.972}, got)
}

func TestAssessResponses(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		body string
		want float64
		ok   bool
	}{
		{"zero", `{"answers":{"ai_approval_allowed":{"type":"noul","noul":0}}}`, 0, true},
		{"one", `{"answers":{"ai_approval_allowed":{"type":"noul","noul":1}}}`, 1, true},
		{"unrounded", `{"answers":{"ai_approval_allowed":{"type":"noul","noul":0.94999}}}`, 0.94999, true},
		{"metadata", `{"extra":{"nested":true},"answers":{"ai_approval_allowed":{"type":"noul","noul":0.5,"confidence":1,"extra":[]}}}`, 0.5, true},
		{"no answers", `{}`, 0, false},
		{"null answers", `{"answers":null}`, 0, false},
		{"wrong ID", `{"answers":{"other":{"type":"noul","noul":1}}}`, 0, false},
		{"null answer", `{"answers":{"ai_approval_allowed":null}}`, 0, false},
		{"missing value", `{"answers":{"ai_approval_allowed":{"type":"noul"}}}`, 0, false},
		{"null value", `{"answers":{"ai_approval_allowed":{"type":"noul","noul":null}}}`, 0, false},
		{"string", `{"answers":{"ai_approval_allowed":{"type":"noul","noul":"1"}}}`, 0, false},
		{"boolean", `{"answers":{"ai_approval_allowed":{"type":"noul","noul":true}}}`, 0, false},
		{"array", `{"answers":{"ai_approval_allowed":{"type":"noul","noul":[1]}}}`, 0, false},
		{"object", `{"answers":{"ai_approval_allowed":{"type":"noul","noul":{}}}}`, 0, false},
		{"below zero", `{"answers":{"ai_approval_allowed":{"type":"noul","noul":-0.01}}}`, 0, false},
		{"above one", `{"answers":{"ai_approval_allowed":{"type":"noul","noul":1.01}}}`, 0, false},
		{"overflow", `{"answers":{"ai_approval_allowed":{"type":"noul","noul":1e999}}}`, 0, false},
		{"NaN", `{"answers":{"ai_approval_allowed":{"type":"noul","noul":NaN}}}`, 0, false},
		{"infinity", `{"answers":{"ai_approval_allowed":{"type":"noul","noul":Infinity}}}`, 0, false},
		{"wrong type", `{"answers":{"ai_approval_allowed":{"type":"score","noul":1}}}`, 0, false},
		{"missing type", `{"answers":{"ai_approval_allowed":{"noul":1}}}`, 0, false},
		{"null type", `{"answers":{"ai_approval_allowed":{"type":null,"noul":1}}}`, 0, false},
		{"non JSON", `not-json secret-key-sentinel diff-sentinel context-sentinel`, 0, false},
		{"truncated", `{"answers":{"ai_approval_allowed":{"type":"noul","noul":1}`, 0, false},
		{"two documents", `{"answers":{"ai_approval_allowed":{"type":"noul","noul":1}}} {}`, 0, false},
		{"trailing junk", `{"answers":{"ai_approval_allowed":{"type":"noul","noul":1}}} x`, 0, false},
		{"empty", "", 0, false},
		{"null", `null`, 0, false},
		{"oversized", strings.Repeat(" ", maxResponseBytes) + `{}`, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				fmt.Fprint(w, tt.body)
			})
			got, err := client.Assess(context.Background(), testDiff(), "context-sentinel")
			if tt.ok {
				require.NoError(t, err)
				assert.Equal(t, tt.want, got.AIApprovalAllowedProbability)
			} else {
				require.Error(t, err)
				assert.Equal(t, Assessment{}, got)
				assert.ErrorContains(t, err, "HTTP 200")
				assertSafeError(t, err)
			}
			assert.EqualValues(t, 1, calls.Load())
		})
	}
}

func TestAssessRetryStatuses(t *testing.T) {
	t.Parallel()
	for _, status := range []int{400, 401, 403, 404, 413, 422, 429, 500, 502, 503, 504, 529} {
		for _, recover := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d/recover=%v", status, recover), func(t *testing.T) {
				t.Parallel()
				var calls atomic.Int32
				var delays []time.Duration
				client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
					call := calls.Add(1)
					data, err := io.ReadAll(r.Body)
					assert.NoError(t, err)
					assert.Contains(t, string(data), "diff-sentinel")
					if recover && call > 1 {
						fmt.Fprint(w, `{"answers":{"ai_approval_allowed":{"type":"noul","noul":1}}}`)
						return
					}
					w.WriteHeader(status)
					fmt.Fprint(w, "secret-key-sentinel diff-sentinel context-sentinel")
				})
				client.wait = func(ctx context.Context, delay time.Duration) error {
					delays = append(delays, delay)
					return nil
				}
				got, err := client.Assess(context.Background(), testDiff(), "context-sentinel")
				canRetry := status == 429 || status == 529 || status == 502 || status == 503 || status == 504
				wantCalls := 1
				if canRetry {
					wantCalls = 3
					if recover {
						wantCalls = 2
					}
				}
				if canRetry && recover {
					require.NoError(t, err)
					assert.Equal(t, 1.0, got.AIApprovalAllowedProbability)
				} else {
					require.Error(t, err)
					assert.ErrorContains(t, err, fmt.Sprintf("HTTP %d", status))
					assertSafeError(t, err)
					assert.Equal(t, Assessment{}, got)
				}
				assert.EqualValues(t, wantCalls, calls.Load())
				require.Len(t, delays, wantCalls-1)
				for i, delay := range delays {
					base := (500 * time.Millisecond) << i
					assert.GreaterOrEqual(t, delay, base)
					assert.Less(t, delay, base+base/2)
				}
			})
		}
	}
}

func TestAssessRetryAfter(t *testing.T) {
	t.Parallel()
	for _, header := range []string{"2", time.Now().Add(10 * time.Second).UTC().Format(http.TimeFormat)} {
		t.Run(header, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if calls.Add(1) == 1 {
					w.Header().Set("Retry-After", header)
					w.WriteHeader(429)
					return
				}
				fmt.Fprint(w, `{"answers":{"ai_approval_allowed":{"type":"noul","noul":0}}}`)
			})
			var waited time.Duration
			client.wait = func(ctx context.Context, delay time.Duration) error {
				waited = delay
				return nil
			}
			_, err := client.Assess(context.Background(), testDiff(), "")
			require.NoError(t, err)
			assert.GreaterOrEqual(t, waited, 2*time.Second)
			assert.EqualValues(t, 2, calls.Load())
		})
	}
}

func TestAssessRetryBeyondDeadline(t *testing.T) {
	t.Parallel()
	for _, header := range []string{"100", "9999999999999999999999999999"} {
		t.Run(header, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Retry-After", header)
				w.WriteHeader(529)
			})
			client.wait = func(context.Context, time.Duration) error {
				t.Error("must not wait beyond the assessment deadline")
				return nil
			}
			_, err := client.Assess(context.Background(), testDiff(), "")
			require.ErrorIs(t, err, context.DeadlineExceeded)
			assert.ErrorContains(t, err, "HTTP 529")
			assert.EqualValues(t, 1, calls.Load())
		})
	}
}

func TestAssessCancellationDuringWait(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls atomic.Int32
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(429)
	})
	waiting := make(chan struct{})
	client.wait = func(ctx context.Context, delay time.Duration) error {
		close(waiting)
		return waitRetry(ctx, delay)
	}
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

func TestAssessTimeouts(t *testing.T) {
	t.Parallel()
	for _, phase := range []string{"headers", "body"} {
		for _, limit := range []string{"request", "assessment", "caller", "http client"} {
			t.Run(phase+"/"+limit, func(t *testing.T) {
				t.Parallel()
				var calls atomic.Int32
				client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					io.Copy(io.Discard, r.Body)
					if phase == "body" {
						fmt.Fprint(w, `{"answers":`)
						w.(http.Flusher).Flush()
					}
					<-r.Context().Done()
				})
				ctx := context.Background()
				switch limit {
				case "request":
					client.requestTimeout = 100 * time.Millisecond
				case "assessment":
					client.assessmentTimeout = 100 * time.Millisecond
				case "caller":
					var cancel context.CancelFunc
					ctx, cancel = context.WithTimeout(ctx, 100*time.Millisecond)
					defer cancel()
				case "http client":
					client.httpClient.Timeout = 100 * time.Millisecond
				}
				_, err := client.Assess(ctx, testDiff(), "")
				require.ErrorIs(t, err, context.DeadlineExceeded)
				assert.EqualValues(t, 1, calls.Load())
			})
		}
	}
}

func TestAssessRejectsRedirects(t *testing.T) {
	t.Parallel()
	for _, status := range []int{301, 302, 303, 307, 308} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				http.Redirect(w, r, "/secret-key-sentinel/diff-sentinel", status)
			})
			_, err := client.Assess(context.Background(), testDiff(), "")
			require.ErrorContains(t, err, "redirect refused")
			assertSafeError(t, err)
			assert.EqualValues(t, 1, calls.Load())
		})
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestAssessTransportError(t *testing.T) {
	t.Parallel()
	calls := 0
	client := NewClient(testKey, &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("secret-key-sentinel diff-sentinel context-sentinel")
	})})
	_, err := client.Assess(context.Background(), testDiff(), "")
	require.ErrorContains(t, err, "transport error")
	assertSafeError(t, err)
	assert.Equal(t, 1, calls)
}

func TestAssessmentDeadlinePersistsAcrossAttempts(t *testing.T) {
	t.Parallel()
	var deadlines []time.Time
	client := NewClient(testKey, &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		deadline, ok := r.Context().Deadline()
		assert.True(t, ok)
		deadlines = append(deadlines, deadline)
		if len(deadlines) == 1 {
			return &http.Response{StatusCode: 503, Body: http.NoBody, Header: make(http.Header)}, nil
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"answers":{"ai_approval_allowed":{"type":"noul","noul":1}}}`))}, nil
	})})
	client.assessmentTimeout = 2 * time.Second
	client.wait = func(ctx context.Context, _ time.Duration) error {
		return waitRetry(ctx, time.Millisecond)
	}
	_, err := client.Assess(context.Background(), testDiff(), "")
	require.NoError(t, err)
	require.Len(t, deadlines, 2)
	assert.Equal(t, deadlines[0], deadlines[1], "retry must not reset the total time budget")
}

type endlessBody struct {
	read   int
	closed bool
}

func (b *endlessBody) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = ' '
	}
	b.read += len(p)
	return len(p), nil
}

func (b *endlessBody) Close() error { b.closed = true; return nil }

func TestResponseReadIsBounded(t *testing.T) {
	t.Parallel()
	body := &endlessBody{}
	client := NewClient(testKey, &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: body}, nil
	})})
	_, err := client.Assess(context.Background(), testDiff(), "")
	require.ErrorContains(t, err, "response exceeds 1 MiB")
	assert.Equal(t, (1<<20)+1, body.read)
	assert.True(t, body.closed)
}

type trackedBody struct {
	io.Reader
	closed bool
}

func (b *trackedBody) Close() error { b.closed = true; return nil }

func TestAssessClosesBodies(t *testing.T) {
	t.Parallel()
	for _, status := range []int{200, 401, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			t.Parallel()
			var bodies []*trackedBody
			client := NewClient(testKey, &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				for _, body := range bodies {
					assert.True(t, body.closed, "previous attempt must close before retrying")
				}
				body := &trackedBody{Reader: strings.NewReader("invalid JSON")}
				bodies = append(bodies, body)
				return &http.Response{StatusCode: status, Body: body, Header: make(http.Header)}, nil
			})})
			client.wait = func(context.Context, time.Duration) error { return nil }
			_, err := client.Assess(context.Background(), testDiff(), "")
			require.Error(t, err)
			for _, body := range bodies {
				assert.True(t, body.closed)
			}
		})
	}
}

func TestAssessTruncatedHTTPBody(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Length", "1000")
		fmt.Fprint(w, `{"answers":{"ai_approval_allowed":{"type":"noul","noul":1}}}`)
	})
	_, err := client.Assess(context.Background(), testDiff(), "")
	require.ErrorContains(t, err, "response read error")
	assert.EqualValues(t, 1, calls.Load())
}

func TestAssessInvalidInputDoesNotSend(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"missing key", "header injection", "oversized context", "oversized patch", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			client := NewClient(testKey, &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				t.Error("invalid input must not send a request")
				return nil, errors.New("unexpected request")
			})})
			diff := testDiff()
			repoContext := ""
			ctx := context.Background()
			switch mode {
			case "missing key":
				client.apiKey = " "
			case "header injection":
				client.apiKey = testKey + "\r\nInjected: value"
			case "oversized context":
				repoContext = strings.Repeat("x", maxRequestBytes)
			case "oversized patch":
				diff.Patch = strings.Repeat("x", maxRequestBytes)
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			got, err := client.Assess(ctx, diff, repoContext)
			require.Error(t, err)
			assertSafeError(t, err)
			assert.Equal(t, Assessment{}, got)
		})
	}
}

func assertSafeError(t *testing.T, err error) {
	t.Helper()
	for _, sentinel := range []string{testKey, "diff-sentinel", "context-sentinel"} {
		assert.NotContains(t, err.Error(), sentinel)
	}
}
