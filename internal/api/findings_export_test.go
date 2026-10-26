package api_test

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/auth"
	"github.com/alphabravocompany/thewolf/internal/auth/apikey"
	"github.com/alphabravocompany/thewolf/internal/models"
)

func TestFindingExportAuthorization(t *testing.T) {
	srv, store, adminJWT := newTestServer(t)
	ctx := context.Background()
	admin, err := auth.ValidateToken(adminJWT)
	if err != nil {
		t.Fatal(err)
	}
	owner := &models.User{ID: uuid.NewString(), Email: "export-owner@example.com", PasswordHash: "unused", Role: models.RoleUser}
	if err := store.CreateUser(ctx, owner); err != nil {
		t.Fatal(err)
	}
	pair, err := auth.GenerateToken(owner.ID, owner.Email)
	if err != nil {
		t.Fatal(err)
	}
	ownerToken := createToken(t, srv, pair.AccessToken, []string{apikey.ScopeReadFindings})
	weakToken := createToken(t, srv, pair.AccessToken, []string{apikey.ScopeReadScans})
	adminToken := createToken(t, srv, adminJWT, []string{apikey.ScopeReadFindings})
	var scans []string
	var titles []string
	for _, userID := range []string{owner.ID, admin.UserID} {
		repo := &models.Repo{ID: uuid.NewString(), UserID: userID, Name: "export", SourceType: models.SourceTypeLocal, SourcePath: t.TempDir(), DefaultBranch: "main"}
		if err := store.CreateRepo(ctx, repo); err != nil {
			t.Fatal(err)
		}
		now := time.Now()
		scan := &models.Scan{ID: uuid.NewString(), UserID: userID, RepoID: repo.ID, Status: models.ScanStatusCompleted, CreatedAt: now, UpdatedAt: now}
		if err := store.CreateScan(ctx, scan); err != nil {
			t.Fatal(err)
		}
		title := "private-evidence-" + uuid.NewString()
		if err := store.CreateFinding(ctx, &models.Finding{ID: uuid.NewString(), ScanID: scan.ID, RepoID: repo.ID, ToolName: "test", RuleID: "R1", FilePath: "app.go", Title: title, Severity: models.SeverityHigh, Category: models.CategorySAST, Status: models.StatusOpen, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
		scans = append(scans, scan.ID)
		titles = append(titles, title)
	}

	for _, format := range []string{"csv", "json"} {
		for _, suppressed := range []string{"false", "true"} {
			for _, tc := range []struct {
				name, token, scanID string
				status              int
				wantTitle           string
			}{
				{"owner session", pair.AccessToken, scans[0], http.StatusOK, titles[0]},
				{"owner token", ownerToken, scans[0], http.StatusOK, titles[0]},
				{"foreign session", pair.AccessToken, scans[1], http.StatusNotFound, ""},
				{"foreign token", ownerToken, scans[1], http.StatusNotFound, ""},
				{"admin session", adminJWT, scans[0], http.StatusOK, titles[0]},
				{"admin token cannot borrow role", adminToken, scans[0], http.StatusNotFound, ""},
				{"wrong scope", weakToken, scans[0], http.StatusForbidden, ""},
				{"anonymous", "", scans[0], http.StatusUnauthorized, ""},
				{"missing", ownerToken, uuid.NewString(), http.StatusNotFound, ""},
				{"all owned", ownerToken, "", http.StatusOK, titles[0]},
			} {
				t.Run(format+"/suppressed="+suppressed+"/"+tc.name, func(t *testing.T) {
					w := request(srv, http.MethodGet, "/api/v1/findings/export?format="+format+"&include_suppressed="+suppressed+"&scan_id="+tc.scanID, tc.token, nil)
					if w.Code != tc.status {
						t.Fatalf("status = %d, want %d: %s", w.Code, tc.status, w.Body.String())
					}
					if tc.status != http.StatusOK {
						if w.Header().Get("Content-Disposition") != "" || strings.Contains(w.Body.String(), "private-evidence-") {
							t.Fatalf("denied export leaked content: %s", w.Body.String())
						}
						return
					}
					if !strings.Contains(w.Body.String(), tc.wantTitle) || strings.Contains(w.Body.String(), titles[1]) {
						t.Fatalf("wrong exported evidence: %s", w.Body.String())
					}
					if format == "json" {
						var rows []models.Finding
						if err := json.Unmarshal(w.Body.Bytes(), &rows); err != nil || len(rows) != 1 {
							t.Fatalf("JSON export: %d rows, %v", len(rows), err)
						}
					} else if rows, err := csv.NewReader(strings.NewReader(w.Body.String())).ReadAll(); err != nil || len(rows) != 2 {
						t.Fatalf("CSV export: %d rows, %v", len(rows), err)
					}
				})
			}
		}
	}
	// Revoking the human role must take effect without issuing another JWT.
	u, err := store.GetUserByID(ctx, admin.UserID)
	if err != nil {
		t.Fatal(err)
	}
	u.Role = models.RoleUser
	if err := store.UpdateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	w := request(srv, http.MethodGet, "/api/v1/findings/export?format=json&scan_id="+scans[0], adminJWT, nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("demoted session export: %d %s", w.Code, w.Body.String())
	}
}
