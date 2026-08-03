package cli

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
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
	Data      json.RawMessage `json:"data"`
	Meta      *ListMeta       `json:"meta"`
	Error     *APIErrorBody   `json:"error"`
	ID        string          `json:"id,omitempty"`
	State     string          `json:"state,omitempty"`
	StatusURL string          `json:"status_url,omitempty"`
	EventsURL string          `json:"events_url,omitempty"`
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

// TransferResult describes a streamed non-JSON response without retaining its
// body in memory.
type TransferResult struct {
	Bytes           int64
	ContentType     string
	ManifestDigest  string
	BundleDigest    string
	SignatureStatus string
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
	return c.DoWithHeaders(ctx, method, path, body, nil)
}

// DoWithHeaders performs a JSON request with command preconditions such as
// Idempotency-Key and If-Match. Header values are validated by net/http and
// never logged by this client.
func (c *Client) DoWithHeaders(
	ctx context.Context,
	method, path string,
	body any,
	headers map[string]string,
) (*Envelope, error) {
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("encode request body: %w", err)
		}
		reqBody = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.requestURL(path), reqBody)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	for key, value := range headers {
		req.Header.Set(key, value)
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

type StreamEvent struct {
	ID   string
	Type string
	Data string
}

// StreamEvents consumes one SSE connection and returns the most recent event
// ID. Callers can reconnect with that ID without replaying durable events.
func (c *Client) StreamEvents(
	ctx context.Context,
	path, lastEventID string,
	onEvent func(StreamEvent),
) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.requestURL(path), nil)
	if err != nil {
		return lastEventID, err
	}
	req.Header.Set("Accept", "text/event-stream")
	if lastEventID != "" {
		req.Header.Set("Last-Event-ID", lastEventID)
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	streamClient := *c.HTTP
	streamClient.Timeout = 0
	resp, err := streamClient.Do(req)
	if err != nil {
		return lastEventID, fmt.Errorf("stream %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(resp.Body)
		return lastEventID, &APIError{StatusCode: resp.StatusCode, Message: strings.TrimSpace(string(raw))}
	}
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	current := StreamEvent{}
	emit := func() {
		if current.Data == "" {
			current = StreamEvent{}
			return
		}
		if current.ID != "" {
			lastEventID = current.ID
		}
		if onEvent != nil {
			onEvent(current)
		}
		current = StreamEvent{}
	}
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return lastEventID, ctx.Err()
		default:
		}
		line := scanner.Text()
		if line == "" {
			emit()
			continue
		}
		if value, ok := strings.CutPrefix(line, "id:"); ok {
			current.ID = strings.TrimSpace(value)
		} else if value, ok := strings.CutPrefix(line, "event:"); ok {
			current.Type = strings.TrimSpace(value)
		} else if value, ok := strings.CutPrefix(line, "data:"); ok {
			if current.Data != "" {
				current.Data += "\n"
			}
			current.Data += strings.TrimSpace(value)
		}
	}
	emit()
	if err := scanner.Err(); err != nil {
		return lastEventID, err
	}
	return lastEventID, nil
}

func (c *Client) requestURL(path string) string {
	path = strings.TrimSpace(path)
	if path == "/api/v1" {
		path = ""
	} else {
		path = strings.TrimPrefix(path, "/api/v1/")
		if path != "" && !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
	}
	return c.BaseURL + path
}

// Raw issues a GET and returns the response body verbatim, for endpoints that
// serve a non-JSON payload (e.g. a fix diff as text/plain or a SARIF report).
func (c *Client) Raw(ctx context.Context, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return nil, err
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request GET %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, &APIError{StatusCode: resp.StatusCode, Message: strings.TrimSpace(string(raw))}
	}
	return raw, nil
}

// Download streams a response body to output. It is intended for potentially
// large immutable artifacts and therefore disables the client's wall-clock
// timeout; cancellation remains controlled by ctx.
func (c *Client) Download(ctx context.Context, path string, output io.Writer) (TransferResult, error) {
	if output == nil {
		return TransferResult{}, errors.New("download output is required")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return TransferResult{}, err
	}
	req.Header.Set("Accept", "application/vnd.wolf.scanner-release-bundle.v1+tar+zstd")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	client := transferHTTPClient(c.HTTP)
	resp, err := client.Do(req)
	if err != nil {
		return TransferResult{}, fmt.Errorf("request GET %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		return TransferResult{}, readTransferAPIError(resp)
	}
	hash := sha256.New()
	written, err := io.CopyBuffer(io.MultiWriter(output, hash), resp.Body, make([]byte, 128*1024))
	if err != nil {
		return TransferResult{}, fmt.Errorf("download %s after %d bytes: %w", path, written, err)
	}
	bundleDigest := "sha256:" + hex.EncodeToString(hash.Sum(nil))
	if expected := resp.Header.Get("X-Wolf-Bundle-Digest"); expected != "" && expected != bundleDigest {
		return TransferResult{}, fmt.Errorf("download %s digest mismatch: got %s, want %s", path, bundleDigest, expected)
	}
	return TransferResult{
		Bytes: written, ContentType: resp.Header.Get("Content-Type"),
		ManifestDigest:  resp.Header.Get("X-Wolf-Manifest-Digest"),
		BundleDigest:    bundleDigest,
		SignatureStatus: resp.Header.Get("X-Wolf-Bundle-Signature-Status"),
	}, nil
}

// Upload streams an arbitrary request body and decodes the standard JSON
// response envelope. A non-negative contentLength enables a fixed-length
// request; -1 uses HTTP streaming/chunked transfer.
func (c *Client) Upload(
	ctx context.Context,
	method, path, contentType string,
	body io.Reader,
	contentLength int64,
	headers map[string]string,
) (*Envelope, error) {
	if body == nil {
		return nil, errors.New("upload body is required")
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", contentType)
	if contentLength >= 0 {
		req.ContentLength = contentLength
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := transferHTTPClient(c.HTTP).Do(req)
	if err != nil {
		return nil, fmt.Errorf("request %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, fmt.Errorf("read upload response: %w", err)
	}
	if resp.StatusCode >= 400 {
		return nil, decodeTransferAPIError(resp.StatusCode, raw)
	}
	if len(raw) == 0 {
		return &Envelope{}, nil
	}
	var envelope Envelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("decode upload response: %w", err)
	}
	return &envelope, nil
}

func transferHTTPClient(client *http.Client) *http.Client {
	if client == nil {
		return &http.Client{}
	}
	copy := *client
	copy.Timeout = 0
	return &copy
}

func readTransferAPIError(resp *http.Response) error {
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	return decodeTransferAPIError(resp.StatusCode, raw)
}

func decodeTransferAPIError(status int, raw []byte) error {
	var envelope Envelope
	if json.Unmarshal(raw, &envelope) == nil && envelope.Error != nil {
		return &APIError{
			StatusCode: status, Code: envelope.Error.Code, Message: envelope.Error.Message,
		}
	}
	message := strings.TrimSpace(string(raw))
	if message == "" {
		message = http.StatusText(status)
	}
	return &APIError{StatusCode: status, Message: message}
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
