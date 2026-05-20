package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client is a thin HTTP client for the wolf /api/v1 surface. Every CLI
// management command goes through it, so the API stays the single source
// of truth and the CLI never duplicates business logic.
type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

// NewClient builds a client for a server base URL (with or without a
// trailing /api/v1) and a credential (JWT or wolf_ token).
func NewClient(server, token string) *Client {
	base := strings.TrimRight(server, "/")
	if !strings.HasSuffix(base, "/api/v1") {
		base += "/api/v1"
	}
	return &Client{
		BaseURL: base,
		Token:   token,
		HTTP:    &http.Client{Timeout: 5 * time.Minute},
	}
}

// Envelope is the standard wolf API response wrapper.
type Envelope struct {
	Data  json.RawMessage `json:"data"`
	Meta  *ListMeta       `json:"meta"`
	Error *APIErrorBody   `json:"error"`
}

// ListMeta is the pagination block on list responses.
type ListMeta struct {
	Total   int `json:"total"`
	Page    int `json:"page"`
	PerPage int `json:"per_page"`
}

// APIErrorBody is the body of an error response.
type APIErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// APIError is a non-2xx response surfaced as a Go error. Its StatusCode
// drives the process exit code (see ExitCodeFor).
type APIError struct {
	StatusCode int
	Code       string
	Message    string
}

func (e *APIError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("API error %d (%s): %s", e.StatusCode, e.Code, e.Message)
	}
	return fmt.Sprintf("API error %d: %s", e.StatusCode, e.Message)
}

// Do performs a JSON request and returns the parsed response envelope. A
// non-2xx status becomes an *APIError.
func (c *Client) Do(ctx context.Context, method, path string, body any) (*Envelope, error) {
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("encode request body: %w", err)
		}
		reqBody = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, reqBody)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusNoContent || len(raw) == 0 {
		if resp.StatusCode >= 400 {
			return nil, &APIError{StatusCode: resp.StatusCode, Message: http.StatusText(resp.StatusCode)}
		}
		return &Envelope{}, nil
	}

	var env Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		// Body was not the standard envelope (e.g. a raw report or SARIF).
		if resp.StatusCode >= 400 {
			return nil, &APIError{StatusCode: resp.StatusCode, Message: strings.TrimSpace(string(raw))}
		}
		return &Envelope{Data: json.RawMessage(raw)}, nil
	}
	if resp.StatusCode >= 400 {
		ae := &APIError{StatusCode: resp.StatusCode}
		if env.Error != nil {
			ae.Code = env.Error.Code
			ae.Message = env.Error.Message
		} else {
			ae.Message = http.StatusText(resp.StatusCode)
		}
		return nil, ae
	}
	return &env, nil
}

// Stream consumes a Server-Sent-Events endpoint, invoking onLine for every
// non-empty data line until the stream closes or the context is cancelled.
func (c *Client) Stream(ctx context.Context, path string, onLine func(string)) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	// SSE connections are long-lived; the shared 5-minute timeout would cut
	// them off, so use a client without one.
	streamClient := &http.Client{}
	resp, err := streamClient.Do(req)
	if err != nil {
		return fmt.Errorf("stream %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(resp.Body)
		return &APIError{StatusCode: resp.StatusCode, Message: strings.TrimSpace(string(raw))}
	}
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		line := scanner.Text()
		if data, ok := strings.CutPrefix(line, "data:"); ok {
			onLine(strings.TrimSpace(data))
		}
	}
	return scanner.Err()
}
