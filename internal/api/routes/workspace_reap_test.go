package routes_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/scantarget"
)

func TestWorkspaceReapValidatesAgeRetainsActiveAndReportsFailures(t *testing.T) {
	env := setupTestEnv(t)
	defer env.Store.Close()
	root := t.TempDir()
	t.Setenv("WOLF_WORKSPACE_ROOT", root)
	paths := []string{filepath.Join(root, "wolf-git-scan-idle"), filepath.Join(root, "wolf-ssh-scan-busy")}
	old := time.Now().Add(-96 * time.Hour)
	for _, path := range paths {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}
	prepared, err := scantarget.PrepareExisting(t.Context(), paths[1], models.SourceTypeSSH)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(prepared.Cleanup)
	request := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/admin/workspaces/reap", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+env.Token)
		response := httptest.NewRecorder()
		env.Router.ServeHTTP(response, req)
		return response
	}
	for _, body := range []string{
		`{`, `[]`, `{"max_age_hours":0}`, `{"max_age_hours":-1}`, `{"max_age_hours":2562048}`,
		`{"max_age_hours":999999999999999999999}`, `{"max_age_hours":72} {}`, `{"max_age_hors":72}`,
		strings.Repeat(" ", 1025),
	} {
		if response := request(body); response.Code != http.StatusBadRequest {
			t.Fatalf("unsafe retention request accepted: %d %s", response.Code, response.Body.String())
		}
		for _, path := range paths {
			if _, err := os.Stat(path); err != nil {
				t.Fatal("invalid request changed a workspace")
			}
		}
	}
	for i := range 2 {
		response := request(`{"max_age_hours":72}`)
		if response.Code != http.StatusOK {
			t.Fatalf("reap failed: %d %s", response.Code, response.Body.String())
		}
		var result struct {
			Data struct {
				Removed  int `json:"removed"`
				Retained int `json:"retained_in_use"`
			} `json:"data"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result.Data.Removed != 1 || result.Data.Retained != 1-i {
			t.Fatalf("wrong cleanup counts: %s", response.Body.String())
		}
		if i == 0 {
			if _, err := os.Stat(paths[1]); err != nil {
				t.Fatal("active workspace deleted")
			}
			prepared.Cleanup()
		}
	}
	t.Setenv("WOLF_WORKSPACE_ROOT", filepath.Join(root, "missing"))
	if response := request(""); response.Code != http.StatusInternalServerError || !strings.Contains(response.Body.String(), "cleanup_failed") {
		t.Fatalf("filesystem failure reported success: %d %s", response.Code, response.Body.String())
	}
}
