package api_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

// TestRBACNonAdminIsRestricted verifies the core of the users-vs-admins model:
// the second registered user is a regular user and is blocked from the admin
// surface and from modifying resources it did not create.
func TestRBACNonAdminIsRestricted(t *testing.T) {
	srv, _, adminJWT := newTestServer(t)

	// The first user (adminJWT) is the admin. Register a second user → regular.
	body, _ := json.Marshal(map[string]string{"email": "user2@example.com", "password": "password1234"})
	w := request(srv, http.MethodPost, "/api/v1/auth/register", "", body)
	if w.Code != http.StatusCreated {
		t.Fatalf("register user2: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var reg struct {
		Data struct {
			AccessToken string `json:"access_token"`
			User        struct {
				ID   string `json:"id"`
				Role string `json:"role"`
			} `json:"user"`
		} `json:"data"`
	}
	mustJSON(t, w.Body.Bytes(), &reg)
	userJWT := reg.Data.AccessToken
	if reg.Data.User.Role != "user" {
		t.Errorf("second user should be role=user, got %q", reg.Data.User.Role)
	}

	// Admin surface is forbidden for the regular user.
	for _, ep := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/users"},
		{http.MethodPut, "/api/v1/settings"},
		{http.MethodGet, "/api/v1/audit-log"},
	} {
		var b []byte
		if ep.method != http.MethodGet {
			b = []byte("{}")
		}
		if rw := request(srv, ep.method, ep.path, userJWT, b); rw.Code != http.StatusForbidden {
			t.Errorf("non-admin %s %s: expected 403, got %d", ep.method, ep.path, rw.Code)
		}
	}

	// The admin creates a repo; the regular user must not be able to delete it.
	repoBody, _ := json.Marshal(map[string]string{
		"name": "admins-repo", "source_type": "local", "source_path": "/tmp/admins-repo",
	})
	rw := request(srv, http.MethodPost, "/api/v1/repos", adminJWT, repoBody)
	if rw.Code != http.StatusCreated {
		t.Fatalf("admin create repo: expected 201, got %d: %s", rw.Code, rw.Body.String())
	}
	var repoResp struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	mustJSON(t, rw.Body.Bytes(), &repoResp)

	if dw := request(srv, http.MethodDelete, "/api/v1/repos/"+repoResp.Data.ID, userJWT, nil); dw.Code != http.StatusForbidden {
		t.Errorf("non-admin deleting another user's repo: expected 403, got %d: %s", dw.Code, dw.Body.String())
	}

	// The admin can delete its own repo.
	if dw := request(srv, http.MethodDelete, "/api/v1/repos/"+repoResp.Data.ID, adminJWT, nil); dw.Code != http.StatusOK && dw.Code != http.StatusNoContent {
		t.Errorf("admin deleting own repo: expected 200/204, got %d", dw.Code)
	}
}
