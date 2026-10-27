// Package gitlab is a tiny GitLab REST client (gitlab.com or self-hosted) used
// by the fix pipeline's writability preflight and merge-request publication.
package gitlab

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	gitpkg "github.com/alphabravocompany/thewolf/internal/gitops"
	"github.com/alphabravocompany/thewolf/internal/scantarget"
)

// Client talks to one GitLab instance with one access token.
type Client struct {
	// BaseURL is the API root, e.g. https://gitlab.example.com/api/v4.
	BaseURL string
	Token   string
	HTTP    *http.Client
	// CheckHost vets the destination before any request is sent. New wires the
	// shared Git network boundary (private ranges need WOLF_GIT_ALLOWED_CIDRS);
	// tests that use a local server set it to nil.
	CheckHost func(ctx context.Context, host string) error
}

// APIError is a non-2xx GitLab response.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string { return fmt.Sprintf("gitlab %d: %s", e.Status, e.Message) }

// New builds a client for host (with an optional :port).
func New(host, token string) (*Client, error) {
	pool, err := gitpkg.CAPool()
	if err != nil {
		return nil, err
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if pool != nil {
		transport.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	}
	return &Client{
		BaseURL: "https://" + host + "/api/v4",
		Token:   token,
		HTTP: &http.Client{
			Timeout:   30 * time.Second,
			Transport: transport,
		},
		CheckHost: scantarget.ValidateRemoteDestination,
	}, nil
}

// ProjectPath is the API path segment for a project's full path.
func ProjectPath(project string) string { return "/projects/" + url.PathEscape(project) }

// Do sends a JSON request and decodes a JSON response into out (when non-nil).
func (c *Client) Do(ctx context.Context, method, path string, body, out any) error {
	target, err := url.Parse(c.BaseURL)
	if err != nil {
		return err
	}
	if c.CheckHost != nil {
		if err := c.CheckHost(ctx, target.Hostname()); err != nil {
			return err
		}
	}
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("PRIVATE-TOKEN", c.Token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	// A redirect would carry the token to another host, so never follow one.
	client := *c.HTTP
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		return c.redact(err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		return &APIError{Status: resp.StatusCode, Message: c.redactText(apiMessage(data))}
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(data, out)
}

func apiMessage(data []byte) string {
	var payload struct {
		Message any    `json:"message"`
		Error   string `json:"error"`
	}
	if json.Unmarshal(data, &payload) == nil {
		switch message := payload.Message.(type) {
		case string:
			return message
		case nil:
		default:
			if encoded, err := json.Marshal(message); err == nil {
				return string(encoded)
			}
		}
		if payload.Error != "" {
			return payload.Error
		}
	}
	text := strings.TrimSpace(string(data))
	if len(text) > 300 {
		text = text[:300] + "…"
	}
	return text
}

func (c *Client) redact(err error) error { return fmt.Errorf("%s", c.redactText(err.Error())) }

func (c *Client) redactText(text string) string {
	if c.Token == "" {
		return text
	}
	return strings.ReplaceAll(text, c.Token, "[REDACTED]")
}

// Project is the slice of a GitLab project the fixer cares about.
type Project struct {
	ID            int64
	DefaultBranch string
	Archived      bool
	// CanPush is true when the token's role is Developer or higher.
	CanPush bool
	// MergeRequestsEnabled is false when the project disabled merge requests.
	MergeRequestsEnabled bool
}

// Project reads project details and the token's access level on it.
func (c *Client) Project(ctx context.Context, project string) (Project, error) {
	var payload struct {
		ID                       int64  `json:"id"`
		DefaultBranch            string `json:"default_branch"`
		Archived                 bool   `json:"archived"`
		MergeRequestsAccessLevel string `json:"merge_requests_access_level"`
		Permissions              struct {
			ProjectAccess *struct {
				AccessLevel int `json:"access_level"`
			} `json:"project_access"`
			GroupAccess *struct {
				AccessLevel int `json:"access_level"`
			} `json:"group_access"`
		} `json:"permissions"`
	}
	if err := c.Do(ctx, http.MethodGet, ProjectPath(project), nil, &payload); err != nil {
		return Project{}, err
	}
	level := 0
	if access := payload.Permissions.ProjectAccess; access != nil && access.AccessLevel > level {
		level = access.AccessLevel
	}
	if access := payload.Permissions.GroupAccess; access != nil && access.AccessLevel > level {
		level = access.AccessLevel
	}
	return Project{
		ID: payload.ID, DefaultBranch: payload.DefaultBranch, Archived: payload.Archived,
		CanPush:              level >= 30, // Developer
		MergeRequestsEnabled: payload.MergeRequestsAccessLevel != "disabled",
	}, nil
}
