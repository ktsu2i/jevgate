package jev

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/ktsu2i/jevgate/internal/git"
)

const (
	endpoint          = "https://api.typesafe.ai/v1/systemone"
	requestTimeout    = 30 * time.Second
	assessmentTimeout = 90 * time.Second
	maxResponseBytes  = 1 << 20
	maxRequestBytes   = 128 << 10
)

// Client evaluates changes with a fixed question and model.
type Client struct {
	apiKey            string
	httpClient        *http.Client
	endpoint          string
	requestTimeout    time.Duration
	assessmentTimeout time.Duration
	wait              func(context.Context, time.Duration) error
}

// NewClient creates a client without mutating the supplied HTTP client.
func NewClient(apiKey string, httpClient *http.Client) *Client {
	client := http.Client{}
	if httpClient != nil {
		client = *httpClient
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &Client{
		apiKey: apiKey, httpClient: &client, endpoint: endpoint,
		requestTimeout: requestTimeout, assessmentTimeout: assessmentTimeout,
		wait: waitRetry,
	}
}

// Assess returns the probability that AI approval is sufficient.
func (c *Client) Assess(ctx context.Context, diff git.Diff, repoContext string) (Assessment, error) {
	ctx, cancel := context.WithTimeout(ctx, c.assessmentTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return Assessment{}, failure(0, "assessment interrupted", err)
	}
	if strings.TrimSpace(c.apiKey) == "" || strings.ContainsAny(c.apiKey, "\r\n") {
		return Assessment{}, failure(0, "invalid API key", nil)
	}
	data, err := encodeRequest(diff, repoContext)
	if err != nil {
		return Assessment{}, err
	}
	for attempt := range maxAttempts {
		assessment, status, after, err := c.attempt(ctx, data)
		if ctx.Err() != nil {
			return Assessment{}, failure(status, "assessment interrupted", ctx.Err())
		}
		if err == nil {
			return assessment, nil
		}
		if !retryable(status) || attempt == maxAttempts-1 {
			return Assessment{}, err
		}
		delay := retryDelay(attempt, after, time.Now())
		if deadline, ok := ctx.Deadline(); ok && delay >= time.Until(deadline) {
			return Assessment{}, failure(status, "retry exceeds assessment deadline", context.DeadlineExceeded)
		}
		if err := c.wait(ctx, delay); err != nil {
			return Assessment{}, failure(status, "retry interrupted", err)
		}
	}
	panic("unreachable: bounded attempt loop always returns")
}

func (c *Client) attempt(ctx context.Context, data []byte) (assessment Assessment, status int, retryAfterHeader string, err error) {
	ctx, cancel := context.WithTimeout(ctx, c.requestTimeout)
	defer cancel()
	if contextErr := ctx.Err(); contextErr != nil {
		return Assessment{}, 0, "", failure(0, "request interrupted", contextErr)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(data))
	if err != nil {
		return Assessment{}, 0, "", failure(0, "invalid request", nil)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		// Transport errors may contain credentials or request data.
		return Assessment{}, 0, "", communicationFailure(ctx, 0, "transport error", err)
	}
	defer resp.Body.Close()
	status = resp.StatusCode
	if status < 200 || status >= 300 {
		// Error bodies may echo the complete input.
		return Assessment{}, status, resp.Header.Get("Retry-After"), failure(status, statusCategory(status), nil)
	}
	data, err = io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return Assessment{}, status, "", communicationFailure(ctx, status, "response read error", err)
	}
	if contextErr := ctx.Err(); contextErr != nil {
		return Assessment{}, status, "", failure(status, "request interrupted", contextErr)
	}
	if len(data) > maxResponseBytes {
		return Assessment{}, status, "", failure(status, "response exceeds 1 MiB", nil)
	}
	assessment, err = decodeAssessment(data)
	if err != nil {
		return Assessment{}, status, "", failure(status, "invalid response: "+err.Error(), nil)
	}
	return assessment, status, "", nil
}

func decodeAssessment(data []byte) (Assessment, error) {
	var response struct {
		Answers map[string]struct {
			Type string   `json:"type"`
			Noul *float64 `json:"noul"`
		} `json:"answers"`
	}
	// JSON errors may quote sensitive response data.
	if err := json.Unmarshal(data, &response); err != nil {
		return Assessment{}, errors.New("malformed JSON or answer schema")
	}
	answer, ok := response.Answers[questionID]
	if !ok || answer.Type != "noul" || answer.Noul == nil {
		return Assessment{}, errors.New("missing ai_approval_allowed Noul answer")
	}
	p := *answer.Noul
	if math.IsNaN(p) || math.IsInf(p, 0) || p < 0 || p > 1 {
		return Assessment{}, errors.New("probability must be finite and within [0, 1]")
	}
	return Assessment{AIApprovalAllowedProbability: p}, nil
}

func statusCategory(status int) string {
	switch {
	case status == 401 || status == 403:
		return "authentication failed"
	case status == 400 || status == 422:
		return "request rejected"
	case status == 429:
		return "rate limited"
	case retryable(status):
		return "service unavailable"
	case status >= 300 && status < 400:
		return "redirect refused"
	default:
		return "unexpected HTTP status"
	}
}

func communicationFailure(ctx context.Context, status int, category string, err error) error {
	if ctx.Err() != nil {
		return failure(status, "request interrupted", ctx.Err())
	}
	var timeout net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &timeout) && timeout.Timeout()) {
		return failure(status, "request timeout", context.DeadlineExceeded)
	}
	if errors.Is(err, context.Canceled) {
		return failure(status, "request canceled", context.Canceled)
	}
	return failure(status, category, nil)
}

func failure(status int, category string, cause error) error {
	message := "jev: " + category
	if status != 0 {
		message += fmt.Sprintf(" (HTTP %d)", status)
	}
	if errors.Is(cause, context.Canceled) {
		return fmt.Errorf("%s: %w", message, context.Canceled)
	}
	if errors.Is(cause, context.DeadlineExceeded) {
		return fmt.Errorf("%s: %w", message, context.DeadlineExceeded)
	}
	return errors.New(message)
}
