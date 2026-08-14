package github_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/github"
)

func TestGitHubListAccessibleRepos(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/user/repos" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `[{"name":"wolf","full_name":"acme/wolf","default_branch":"main","private":true}]`)
	}))
	defer srv.Close()
	c := &github.Client{BaseURL: srv.URL, Token: "ghp_x"}
	repos, err := c.ListAccessibleRepos(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(repos) != 1 || repos[0].FullName != "acme/wolf" {
		t.Fatalf("got %+v", repos)
	}
}

func TestGitHubListOrgRepos(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/orgs/acme/repos") {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `[{"name":"api","full_name":"acme/api","default_branch":"main","private":true,"archived":false},{"name":"web","full_name":"acme/web","default_branch":"main","private":false,"archived":false}]`)
	}))
	defer srv.Close()
	c := &github.Client{BaseURL: srv.URL, Token: "ghp_x"}
	repos, err := c.ListOrgRepos(context.Background(), "acme")
	if err != nil {
		t.Fatal(err)
	}
	if len(repos) != 2 || repos[0].FullName != "acme/api" {
		t.Errorf("got %+v", repos)
	}
}

func TestGitHubListOrgRepos_FallbackToUser(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/orgs/") {
			http.NotFound(w, r)
			return
		}
		if !strings.HasPrefix(r.URL.Path, "/users/alice/repos") {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `[{"name":"dotfiles","full_name":"alice/dotfiles","default_branch":"main","private":false,"archived":false}]`)
	}))
	defer srv.Close()
	c := &github.Client{BaseURL: srv.URL}
	repos, err := c.ListOrgRepos(context.Background(), "alice")
	if err != nil {
		t.Fatal(err)
	}
	if len(repos) != 1 || repos[0].FullName != "alice/dotfiles" {
		t.Errorf("got %+v", repos)
	}
}

func TestValidateToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/user" {
			t.Fatalf("path %q", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer ghp_x" {
			w.WriteHeader(401)
			return
		}
		w.Header().Set("X-OAuth-Scopes", "repo, workflow")
		fmt.Fprint(w, `{"login":"octo"}`)
	}))
	defer srv.Close()
	ok, err := (&github.Client{BaseURL: srv.URL, Token: "ghp_x"}).ValidateToken(context.Background())
	if err != nil || !ok.Valid || ok.Login != "octo" || len(ok.Scopes) != 2 {
		t.Fatalf("got %+v err=%v", ok, err)
	}
	bad, err := (&github.Client{BaseURL: srv.URL, Token: "nope"}).ValidateToken(context.Background())
	if err == nil || bad.Valid {
		t.Fatal("rejected token should error")
	}
}

func TestGitHubRepoPushInfo(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		status      int
		wantPush    bool
		wantArchive bool
		wantErr     bool
	}{
		{"push true", `{"archived":false,"permissions":{"push":true,"admin":false}}`, 200, true, false, false},
		{"admin implies push", `{"archived":false,"permissions":{"push":false,"admin":true}}`, 200, true, false, false},
		{"read only", `{"archived":false,"permissions":{"push":false,"admin":false}}`, 200, false, false, false},
		{"archived", `{"archived":true,"permissions":{"push":true}}`, 200, true, true, false},
		{"forbidden", `{"message":"Not Found"}`, 404, false, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/repos/acme/widget" {
					t.Fatalf("unexpected path %q", r.URL.Path)
				}
				if got := r.Header.Get("Authorization"); got != "Bearer ghp_x" {
					t.Fatalf("missing/bad auth header: %q", got)
				}
				w.WriteHeader(tt.status)
				fmt.Fprint(w, tt.body)
			}))
			defer srv.Close()
			c := &github.Client{BaseURL: srv.URL, Token: "ghp_x"}
			info, err := c.RepoPushInfo(context.Background(), "acme", "widget")
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if info.CanPush != tt.wantPush || info.Archived != tt.wantArchive {
				t.Fatalf("got %+v, want push=%v archived=%v", info, tt.wantPush, tt.wantArchive)
			}
		})
	}
}
