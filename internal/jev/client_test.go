package jev_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ktsu2i/jevgate/internal/git"
	"github.com/ktsu2i/jevgate/internal/jev"
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

func testClient(t *testing.T, handler http.HandlerFunc) *jev.Client {
	t.Helper()
	return testClientWithTimeout(t, handler, 0)
}

func testClientWithTimeout(t *testing.T, handler http.HandlerFunc, timeout time.Duration) *jev.Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	target, err := url.Parse(server.URL)
	require.NoError(t, err)
	transport := server.Client().Transport
	return jev.NewClient(testKey, &http.Client{
		Timeout: timeout,
		Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			request = request.Clone(request.Context())
			request.URL.Scheme = target.Scheme
			request.URL.Host = target.Host
			return transport.RoundTrip(request)
		}),
	})
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
	assert.Equal(t, jev.Assessment{AIApprovalAllowedProbability: 0.972}, got)
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
		{"oversized", strings.Repeat(" ", 1<<20) + `{}`, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			client := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				fmt.Fprint(w, tt.body)
			})
			got, err := client.Assess(context.Background(), testDiff(), "context-sentinel")
			if tt.ok {
				require.NoError(t, err)
				assert.InDelta(t, tt.want, got.AIApprovalAllowedProbability, 0)
			} else {
				require.Error(t, err)
				assert.Equal(t, jev.Assessment{}, got)
				require.ErrorContains(t, err, "HTTP 200")
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
					assert.InDelta(t, 1.0, got.AIApprovalAllowedProbability, 0)
				} else {
					require.Error(t, err)
					require.ErrorContains(t, err, fmt.Sprintf("HTTP %d", status))
					assertSafeError(t, err)
					assert.Equal(t, jev.Assessment{}, got)
				}
				assert.EqualValues(t, wantCalls, calls.Load())
			})
		}
	}
}

func TestAssessTimeouts(t *testing.T) {
	t.Parallel()
	for _, phase := range []string{"headers", "body"} {
		for _, limit := range []string{"caller", "http client"} {
			t.Run(phase+"/"+limit, func(t *testing.T) {
				t.Parallel()
				var calls atomic.Int32
				timeout := time.Duration(0)
				if limit == "http client" {
					timeout = 100 * time.Millisecond
				}
				client := testClientWithTimeout(t, func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					_, err := io.Copy(io.Discard, r.Body)
					assert.NoError(t, err)
					if phase == "body" {
						fmt.Fprint(w, `{"answers":`)
						flusher, ok := w.(http.Flusher)
						assert.True(t, ok)
						if ok {
							flusher.Flush()
						}
					}
					<-r.Context().Done()
				}, timeout)
				ctx := context.Background()
				if limit == "caller" {
					var cancel context.CancelFunc
					ctx, cancel = context.WithTimeout(ctx, 100*time.Millisecond)
					defer cancel()
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
		t.Run(strconv.Itoa(status), func(t *testing.T) {
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
	client := jev.NewClient(testKey, &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
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
	client := jev.NewClient(testKey, &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		deadline, ok := r.Context().Deadline()
		assert.True(t, ok)
		deadlines = append(deadlines, deadline)
		if len(deadlines) == 1 {
			return &http.Response{StatusCode: http.StatusServiceUnavailable, Body: http.NoBody, Header: make(http.Header)}, nil
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"answers":{"ai_approval_allowed":{"type":"noul","noul":1}}}`))}, nil
	})})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := client.Assess(ctx, testDiff(), "")
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
	client := jev.NewClient(testKey, &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: body}, nil
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
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			t.Parallel()
			var bodies []*trackedBody
			client := jev.NewClient(testKey, &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
				for _, body := range bodies {
					assert.True(t, body.closed, "previous attempt must close before retrying")
				}
				body := &trackedBody{Reader: strings.NewReader("invalid JSON")}
				bodies = append(bodies, body)
				return &http.Response{StatusCode: status, Body: body, Header: make(http.Header)}, nil
			})})
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
	client := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
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
			apiKey := testKey
			switch mode {
			case "missing key":
				apiKey = " "
			case "header injection":
				apiKey = testKey + "\r\nInjected: value"
			}
			client := jev.NewClient(apiKey, &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				t.Error("invalid input must not send a request")
				return nil, errors.New("unexpected request")
			})})
			diff := testDiff()
			repoContext := ""
			ctx := context.Background()
			switch mode {
			case "oversized context":
				repoContext = strings.Repeat("x", 128<<10)
			case "oversized patch":
				diff.Patch = strings.Repeat("x", 128<<10)
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			got, err := client.Assess(ctx, diff, repoContext)
			require.Error(t, err)
			assertSafeError(t, err)
			assert.Equal(t, jev.Assessment{}, got)
		})
	}
}

func assertSafeError(t *testing.T, err error) {
	t.Helper()
	for _, sentinel := range []string{testKey, "diff-sentinel", "context-sentinel"} {
		assert.NotContains(t, err.Error(), sentinel)
	}
}
