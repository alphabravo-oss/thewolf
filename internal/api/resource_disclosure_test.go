package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/artifacts"
	"github.com/alphabravocompany/thewolf/internal/auth"
	"github.com/alphabravocompany/thewolf/internal/auth/apikey"
	"github.com/alphabravocompany/thewolf/internal/models"
)

func TestScanFindingAndImportResourceNonDisclosure(t *testing.T) {
	srv, store, adminJWT := newTestServer(t)
	ctx := context.Background()
	previous := artifacts.Global
	if err := artifacts.Init(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { artifacts.Global = previous })
	var repos, scans, findings, tokens []string
	for _, name := range []string{"caller", "foreign"} {
		u := &models.User{ID: uuid.NewString(), Email: name + "@example.test", PasswordHash: "unused"}
		if err := store.CreateUser(ctx, u); err != nil {
			t.Fatal(err)
		}
		pair, err := auth.GenerateToken(u.ID, u.Email)
		if err != nil {
			t.Fatal(err)
		}
		tokens = append(tokens, pair.AccessToken)
		r := &models.Repo{ID: uuid.NewString(), UserID: u.ID, Name: name, SourceType: models.SourceTypeLocal, SourcePath: t.TempDir(), DefaultBranch: "main"}
		if err := store.CreateRepo(ctx, r); err != nil {
			t.Fatal(err)
		}
		repos = append(repos, r.ID)
		now := time.Now().UTC()
		s := &models.Scan{ID: uuid.NewString(), UserID: u.ID, RepoID: r.ID, Status: models.ScanStatusCompleted, CreatedAt: now, UpdatedAt: now}
		if err := store.CreateScan(ctx, s); err != nil {
			t.Fatal(err)
		}
		scans = append(scans, s.ID)
		f := &models.Finding{ID: uuid.NewString(), ScanID: s.ID, RepoID: r.ID, Title: "private-" + name, FilePath: "app.go", RuleID: "R1", ToolName: "test", Category: models.CategorySAST, Severity: models.SeverityHigh, Status: models.StatusOpen, CreatedAt: now, UpdatedAt: now}
		if err := store.CreateFinding(ctx, f); err != nil {
			t.Fatal(err)
		}
		findings = append(findings, f.ID)
	}
	a, err := artifacts.Publish(ctx, scans[1], "test.log", strings.NewReader("private-foreign raw evidence"))
	if err != nil {
		t.Fatal(err)
	}
	a.ArtifactType = models.ArtifactLog
	if err := store.CreateScanArtifact(ctx, a); err != nil {
		t.Fatal(err)
	}
	readWrite := []string{apikey.ScopeWriteScans, apikey.ScopeWriteFindings, apikey.ScopeWriteRepos}
	callerToken := createToken(t, srv, tokens[0], readWrite)
	adminToken := createToken(t, srv, adminJWT, []string{apikey.ScopeAdmin})
	weakToken := createToken(t, srv, tokens[0], []string{apikey.ScopeReadConfig})
	cases := []struct{ name, method, path, body string }{
		{"repo", http.MethodGet, "/repos/{repo}", ""},
		{"repo baselines", http.MethodGet, "/repos/{repo}/baselines", ""},
		{"repo update", http.MethodPut, "/repos/{repo}", `{"name":"changed"}`},
		{"repo delete", http.MethodDelete, "/repos/{repo}", ""},
		{"finding", http.MethodGet, "/findings/{finding}", ""},
		{"finding triage", http.MethodPut, "/findings/{finding}/status", `{"status":"false_positive"}`},
		{"finding export", http.MethodGet, "/findings/export?scan_id={scan}&format=json&include_suppressed=true", ""},
		{"coverage import", http.MethodPut, "/scans/{scan}/test-coverage", `{}`},
		{"import", http.MethodPost, "/sarif/import", `{"repo_id":"{repo}","sarif":"invalid-until-authorized"}`},
		{"compare current", http.MethodGet, "/scans/{scan}/diff?baseline_scan_id=" + scans[0], ""},
		{"compare baseline", http.MethodGet, "/scans/" + scans[0] + "/diff?baseline_scan_id={scan}", ""},
		{"baseline attachment", http.MethodPost, "/repos/" + repos[0] + "/baselines", `{"name":"probe","scan_id":"{scan}"}`},
	}
	for _, suffix := range []string{"", "/lineage", "/result", "/findings", "/findings/stats", "/stream", "/report", "/manifest", "/sarif", "/coverage", "/test-coverage", "/test-coverage/files", "/gate", "/tools", "/scanner-runs", "/tools/test/output", "/artifacts/" + a.ID + "/download", "/ai-logs", "/tool-summaries", "/recommendations"} {
		cases = append(cases, struct{ name, method, path, body string }{"scan" + suffix, http.MethodGet, "/scans/{scan}" + suffix, ""})
	}
	for i, tc := range cases {
		ip := fmt.Sprintf("10.2.%d.%d", i/256, i%256)
		t.Run(tc.name, func(t *testing.T) {
			for _, credential := range []struct {
				name, token string
				status      int
			}{
				{"user session", tokens[0], http.StatusNotFound}, {"user token", callerToken, http.StatusNotFound}, {"admin token", adminToken, http.StatusNotFound}, {"weak token", weakToken, http.StatusForbidden}, {"anonymous", "", http.StatusUnauthorized},
			} {
				t.Run(credential.name, func(t *testing.T) {
					var bodies []string
					for _, missing := range []bool{false, true} {
						repo, scan, finding := repos[1], scans[1], findings[1]
						if missing {
							repo, scan, finding = uuid.NewString(), uuid.NewString(), uuid.NewString()
						}
						replace := strings.NewReplacer("{repo}", repo, "{scan}", scan, "{finding}", finding)
						w := reqFrom(srv, tc.method, "/api/v1"+replace.Replace(tc.path), credential.token, ip, []byte(replace.Replace(tc.body)))
						if w.Code != credential.status {
							t.Errorf("missing=%v: want %d: %d %s", missing, credential.status, w.Code, w.Body.String())
						}
						if strings.Contains(w.Body.String(), "private-foreign") || w.Header().Get("Content-Disposition") != "" {
							t.Errorf("private data exposed: %s", w.Body.String())
						}
						bodies = append(bodies, strings.NewReplacer(repo, "<repo>", scan, "<scan>", finding, "<finding>").Replace(w.Body.String()))
					}
					if bodies[0] != bodies[1] {
						t.Errorf("existence disclosed:\nforeign: %s\nmissing: %s", bodies[0], bodies[1])
					}
				})
			}
		})
	}
	f, err := store.GetFindingByID(ctx, findings[1])
	if err != nil || f.Status != models.StatusOpen {
		t.Fatalf("foreign triage changed: %+v %v", f, err)
	}
	r, err := store.GetRepoByID(ctx, repos[1])
	if err != nil || r.Name != "foreign" {
		t.Fatalf("foreign repository changed: %+v %v", r, err)
	}
	imports, err := store.ListSARIFImportsByRepo(ctx, repos[1])
	if err != nil || len(imports) != 0 {
		t.Fatalf("foreign imports: %+v %v", imports, err)
	}
	// Authorized session/token reads still expose the requested evidence.
	ownerToken := createToken(t, srv, tokens[1], readWrite)
	for _, bearer := range []string{tokens[1], ownerToken, adminJWT} {
		for _, path := range []string{"/scans/" + scans[1] + "/tools/test/output", "/scans/" + scans[1] + "/artifacts/" + a.ID + "/download"} {
			w := request(srv, http.MethodGet, "/api/v1"+path, bearer, nil)
			if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "private-foreign raw evidence") {
				t.Errorf("authorized raw read: %d %s", w.Code, w.Body.String())
			}
		}
		w := request(srv, http.MethodGet, "/api/v1/scans/"+scans[1]+"/gate", bearer, nil)
		if w.Code != http.StatusOK {
			t.Errorf("authorized gate: %d %s", w.Code, w.Body.String())
		}
		w = request(srv, http.MethodGet, "/api/v1/scans/"+scans[1]+"/diff?baseline_scan_id="+scans[1], bearer, nil)
		if w.Code != http.StatusOK {
			t.Errorf("authorized comparison: %d %s", w.Code, w.Body.String())
		}
	}
	// Import remains owner-only; malformed SARIF is parsed only after access.
	for _, repo := range []string{repos[1], uuid.NewString()} {
		w := request(srv, http.MethodPost, "/api/v1/sarif/import", adminJWT, []byte(fmt.Sprintf(`{"repo_id":%q,"sarif":"invalid"}`, repo)))
		if w.Code != http.StatusNotFound {
			t.Errorf("administrator foreign import: %d %s", w.Code, w.Body.String())
		}
	}
	valid := `{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"authorization-fixture"}},"results":[]}]}`
	for i, bearer := range []string{tokens[1], ownerToken} {
		body, err := json.Marshal(map[string]string{"repo_id": repos[1], "sarif": strings.Replace(valid, "authorization-fixture", fmt.Sprintf("authorization-fixture-%d", i), 1)})
		if err != nil {
			t.Fatal(err)
		}
		w := request(srv, http.MethodPost, "/api/v1/sarif/import", bearer, body)
		if w.Code != http.StatusCreated {
			t.Errorf("owner import: %d %s", w.Code, w.Body.String())
		}
	}
}
