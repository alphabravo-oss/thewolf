package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Repo is a minimal representation of a GitHub repository as returned by the
// /orgs/{org}/repos and /users/{user}/repos endpoints.
type Repo struct {
	Name          string `json:"name"`
	FullName      string `json:"full_name"`
	DefaultBranch string `json:"default_branch"`
	Private       bool   `json:"private"`
	Archived      bool   `json:"archived"`
	Language      string `json:"language"`
}

// Client is a tiny GitHub REST client used for repo discovery during the
// Import-from-GitHub flow.
type Client struct {
	BaseURL string // defaults to https://api.github.com
	Token   string
	HTTP    *http.Client
}

// DefaultBaseURL, when non-empty, overrides the production GitHub host. Tests
// use this to redirect New() at an httptest server without rewriting callers.
var DefaultBaseURL = ""

// New constructs a Client with sensible defaults.
func New(token string) *Client {
	base := "https://api.github.com"
	if DefaultBaseURL != "" {
		base = DefaultBaseURL
	}
	return &Client{BaseURL: base, Token: token, HTTP: &http.Client{Timeout: 30 * time.Second}}
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

// ListOrgRepos lists every repository in the given org, transparently paging
// through results. If the org returns 404, it falls back to ListUserRepos so
// the same handler works for both org and user accounts.
func (c *Client) ListOrgRepos(ctx context.Context, org string) ([]Repo, error) {
	var all []Repo
	page := 1
	for {
		url := fmt.Sprintf("%s/orgs/%s/repos?per_page=100&page=%d&type=all", c.BaseURL, org, page)
		req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
		req.Header.Set("Accept", "application/vnd.github+json")
		if c.Token != "" {
			req.Header.Set("Authorization", "Bearer "+c.Token)
		}
		resp, err := c.httpClient().Do(req)
		if err != nil {
			return nil, err
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode == 404 {
			// Try as user instead of org.
			return c.ListUserRepos(ctx, org)
		}
		if resp.StatusCode >= 400 {
			return nil, fmt.Errorf("github %d: %s", resp.StatusCode, string(body))
		}
		var batch []Repo
		if err := json.Unmarshal(body, &batch); err != nil {
			return nil, err
		}
		all = append(all, batch...)
		if len(batch) < 100 {
			break
		}
		page++
	}
	return all, nil
}

// PushInfo is the slice of a repo's metadata the writability preflight needs:
// whether the authenticated token can push, and whether the repo is archived.
type PushInfo struct {
	CanPush  bool
	Archived bool
}

// RepoPushInfo fetches GET /repos/{owner}/{repo} and reports whether the
// authenticated token has push permission and whether the repo is archived.
// The "permissions" object is only present (and populated) for an
// authenticated request, which is exactly the writability question we want to
// answer.
func (c *Client) RepoPushInfo(ctx context.Context, owner, repo string) (PushInfo, error) {
	url := fmt.Sprintf("%s/repos/%s/%s", c.BaseURL, owner, repo)
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return PushInfo{}, err
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode >= 400 {
		return PushInfo{}, fmt.Errorf("github %d: %s", resp.StatusCode, string(body))
	}
	var payload struct {
		Archived    bool `json:"archived"`
		Permissions struct {
			Push  bool `json:"push"`
			Admin bool `json:"admin"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return PushInfo{}, err
	}
	return PushInfo{
		CanPush:  payload.Permissions.Push || payload.Permissions.Admin,
		Archived: payload.Archived,
	}, nil
}

// TokenInfo is the save-time validation of a GitHub PAT or fine-grained token.
type TokenInfo struct {
	Login  string   `json:"login"`
	Scopes []string `json:"scopes,omitempty"`
	Valid  bool     `json:"valid"`
}

// ValidateToken calls GET /user and reports whether the token authenticates.
// A 401/403 is a hard invalid token. Network failures are returned as errors
// so the caller can decide whether to store the secret anyway (air-gapped).
func (c *Client) ValidateToken(ctx context.Context) (TokenInfo, error) {
	url := fmt.Sprintf("%s/user", c.BaseURL)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return TokenInfo{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return TokenInfo{}, err
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return TokenInfo{Valid: false}, fmt.Errorf("github %d: token was rejected", resp.StatusCode)
	}
	if resp.StatusCode >= 400 {
		return TokenInfo{}, fmt.Errorf("github %d: %s", resp.StatusCode, string(body))
	}
	var payload struct {
		Login string `json:"login"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return TokenInfo{}, err
	}
	scopes := splitScopes(resp.Header.Get("X-OAuth-Scopes"))
	return TokenInfo{Login: payload.Login, Scopes: scopes, Valid: payload.Login != ""}, nil
}

func splitScopes(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// ListAccessibleRepos lists every repository the token can see (owned,
// collaborator, and org membership), paging through results.
func (c *Client) ListAccessibleRepos(ctx context.Context) ([]Repo, error) {
	var all []Repo
	page := 1
	for {
		url := fmt.Sprintf("%s/user/repos?per_page=100&page=%d&affiliation=owner,collaborator,organization_member&sort=full_name", c.BaseURL, page)
		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/vnd.github+json")
		if c.Token != "" {
			req.Header.Set("Authorization", "Bearer "+c.Token)
		}
		resp, err := c.httpClient().Do(req)
		if err != nil {
			return nil, err
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode >= 400 {
			return nil, fmt.Errorf("github %d: %s", resp.StatusCode, string(body))
		}
		var batch []Repo
		if err := json.Unmarshal(body, &batch); err != nil {
			return nil, err
		}
		all = append(all, batch...)
		if len(batch) < 100 {
			break
		}
		page++
	}
	return all, nil
}

// ListUserRepos lists every repository for the given user, paging through
// results.
func (c *Client) ListUserRepos(ctx context.Context, user string) ([]Repo, error) {
	var all []Repo
	page := 1
	for {
		url := fmt.Sprintf("%s/users/%s/repos?per_page=100&page=%d&type=all", c.BaseURL, user, page)
		req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
		req.Header.Set("Accept", "application/vnd.github+json")
		if c.Token != "" {
			req.Header.Set("Authorization", "Bearer "+c.Token)
		}
		resp, err := c.httpClient().Do(req)
		if err != nil {
			return nil, err
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode >= 400 {
			return nil, fmt.Errorf("github %d: %s", resp.StatusCode, string(body))
		}
		var batch []Repo
		if err := json.Unmarshal(body, &batch); err != nil {
			return nil, err
		}
		all = append(all, batch...)
		if len(batch) < 100 {
			break
		}
		page++
	}
	return all, nil
}
