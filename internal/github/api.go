package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
