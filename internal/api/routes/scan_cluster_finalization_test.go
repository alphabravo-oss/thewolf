package routes

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alphabravocompany/thewolf/internal/artifacts"
	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
	"github.com/alphabravocompany/thewolf/internal/scantarget"
	"github.com/google/uuid"
)

type clusterFindingPlugin struct {
	profileTestPlugin
	failLogs bool
	lockfile string
}

func (p clusterFindingPlugin) Execute(_ context.Context, opts models.ExecuteOpts) ([]models.Finding, error) {
	if opts.OnOutput != nil {
		if p.failLogs {
			entries, err := os.ReadDir(artifacts.Global.Root())
			if err != nil {
				return nil, err
			}
			for _, entry := range entries {
				if entry.IsDir() {
					if err := os.MkdirAll(filepath.Join(artifacts.Global.Root(), entry.Name(), p.name+".log"), 0700); err != nil {
						return nil, err
					}
				}
			}
		}
		opts.OnOutput("fixture tool output")
	}
	lockfile := p.lockfile
	if lockfile == "" {
		lockfile = "deps/package-lock.json"
	}
	return []models.Finding{{ToolName: p.name, Category: models.CategorySCA, Severity: models.SeverityHigh, Status: models.StatusOpen, FilePath: lockfile, Title: "dependency", PackageName: "alpha", PackageVersion: "1", PackageEcosystem: "npm", RuleID: "CVE-2026-1001", AdvisoryID: "CVE-2026-1001"}}, nil
}

func TestScanFinalizationKeepsPersistedIdentityAndEnrichment(t *testing.T) {
	for _, backend := range []string{"sqlite", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			for _, scenario := range []string{"fresh", "cargo-lock", "go-sum", "leased", "resumed", "finalization-conflict", "continuous-conflict", "cancel-on-retry", "lease-loss-on-retry", "artifact-after-conflict", "policy-after-conflict", "insert-error", "read-error", "update-error", "canonical-error", "baseline-error", "auto-baseline-error", "gate-error", "artifact-error", "gate-artifact-error", "log-write-error"} {
				t.Run(scenario, func(t *testing.T) {
					var store db.Store
					if backend == "sqlite" {
						store = withScannerSupplyChainStore(t)
					} else {
						store = clusterPostgresStore(t)
					}
					previous := artifacts.Global
					if err := artifacts.Init(t.TempDir()); err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { artifacts.Global = previous })
					ctx := t.Context()
					path := t.TempDir()
					lockfile := "deps/package-lock.json"
					if scenario == "cargo-lock" {
						lockfile = "deps/Cargo.lock"
					}
					if scenario == "go-sum" {
						lockfile = "deps/go.sum"
					}
					if err := os.Mkdir(filepath.Join(path, "deps"), 0700); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(path, lockfile), []byte(`{}`), 0600); err != nil {
						t.Fatal(err)
					}
					user := &models.User{ID: uuid.NewString(), Email: uuid.NewString() + "@example.test", PasswordHash: "hash"}
					if err := store.CreateUser(ctx, user); err != nil {
						t.Fatal(err)
					}
					repo := &models.Repo{ID: uuid.NewString(), UserID: user.ID, Name: "cluster", SourceType: models.SourceTypeLocal, SourcePath: path, DefaultBranch: "main"}
					if err := store.CreateRepo(ctx, repo); err != nil {
						t.Fatal(err)
					}
					scan := &models.Scan{ID: uuid.NewString(), UserID: user.ID, RepoID: repo.ID, Branch: "main", Status: models.ScanStatusPending, PreparedWorkspace: path, SourceType: models.SourceTypeLocal, SourcePath: path, RequestJSON: `{"tools":["cluster-a","cluster-b"]}`}
					if err := store.CreateScan(ctx, scan); err != nil {
						t.Fatal(err)
					}
					if scenario == "resumed" {
						prepared, err := scantarget.PrepareExisting(ctx, path, models.SourceTypeLocal)
						if err != nil {
							t.Fatal(err)
						}
						defer prepared.Cleanup()
						scan.Attempt = 2
						scan.TreeDigest = prepared.TreeDigest
						scan.ToolsCompleted = `["cluster-a"]`
						if err := store.UpdateScan(ctx, scan); err != nil {
							t.Fatal(err)
						}
						if err := store.UpsertScannerRunRecord(ctx, &models.ScannerRunRecord{ScanID: scan.ID, ToolName: "cluster-a", Status: "completed", ParserStatus: "parsed", Attempt: 1}); err != nil {
							t.Fatal(err)
						}
						old, _ := (clusterFindingPlugin{profileTestPlugin: profileTestPlugin{name: "cluster-a"}}).Execute(ctx, models.ExecuteOpts{})
						old[0].ID = uuid.NewString()
						old[0].ScanID = scan.ID
						old[0].RepoID = repo.ID
						if err := store.CreateFinding(ctx, &old[0]); err != nil {
							t.Fatal(err)
						}
					}

					if scenario == "leased" || scenario == "lease-loss-on-retry" {
						claim, err := store.ClaimNextScan(ctx, "completion-worker", "docker", time.Now().Add(time.Minute))
						if err != nil || claim == nil {
							t.Fatalf("claim: %+v %v", claim, err)
						}
						scan = claim
					}
					registry := plugin.NewRegistry()
					for _, name := range []string{"cluster-a", "cluster-b"} {
						registry.Register(clusterFindingPlugin{profileTestPlugin: profileTestPlugin{name: name, category: models.CategorySCA}, failLogs: scenario == "log-write-error", lockfile: lockfile})
					}
					completionCalls := 0
					if err := ExecuteQueuedScan(ctx, &Handler{Store: clusterFailureStore{Store: store, step: scenario, completionCalls: &completionCalls}, Registry: registry}, scan); err != nil {
						t.Fatal(err)
					}
					final, err := store.GetScanByID(ctx, scan.ID)
					if err != nil {
						t.Fatal(err)
					}
					switch scenario {
					case "continuous-conflict", "cancel-on-retry", "lease-loss-on-retry", "artifact-after-conflict", "policy-after-conflict":
						want, calls := models.ScanStatusFailed, 1
						if scenario == "continuous-conflict" {
							calls = 3
						}
						if scenario == "cancel-on-retry" {
							want = models.ScanStatusCancelled
						}
						if scenario == "lease-loss-on-retry" {
							want = models.ScanStatusPending
						}
						if final.Status != want || completionCalls != calls {
							t.Fatalf("retry boundary: status=%s calls=%d, want %s/%d", final.Status, completionCalls, want, calls)
						}
						if _, err := store.GetScanBaselineByName(ctx, repo.ID, "main", "auto"); !errors.Is(err, sql.ErrNoRows) {
							t.Fatalf("unsuccessful retry published baseline: %v", err)
						}
						rows, err := store.ListFindingEvidenceByScan(ctx, scan.ID)
						if err != nil || len(rows) != 2 {
							t.Fatalf("retry lost evidence: %+v %v", rows, err)
						}
						return
					}
					if scenario == "finalization-conflict" && completionCalls != 2 {
						t.Fatalf("conflict retry calls = %d", completionCalls)
					}
					if scenario == "gate-error" {
						if final.Status != models.ScanStatusFailed || final.FailureCode != "scan_completion_failed" {
							t.Fatalf("gate failure reported success: %+v", final)
						}
						return
					}
					if scenario == "auto-baseline-error" {
						if final.Status != models.ScanStatusFailed || final.FailureCode != "scan_completion_failed" {
							t.Fatalf("auto baseline failure reported success: %+v", final)
						}
						return
					}
					if scenario == "artifact-error" || scenario == "gate-artifact-error" || scenario == "log-write-error" {
						expected := "artifact_persist_failed"
						if scenario == "gate-artifact-error" {
							expected = "scan_completion_failed"
						}
						if final.Status != models.ScanStatusFailed || final.FailureCode != expected {
							t.Fatalf("artifact failure reported success: %+v", final)
						}
						rows, err := store.ListFindingEvidenceByScan(ctx, scan.ID)
						if err != nil || len(rows) != 2 {
							t.Fatalf("artifact failure lost evidence: %+v %v", rows, err)
						}
						return
					}
					if scenario == "insert-error" || scenario == "read-error" || scenario == "update-error" || scenario == "canonical-error" || scenario == "baseline-error" {
						if final.Status != models.ScanStatusFailed {
							t.Fatalf("persistence failure reported success: %+v", final)
						}
						if scenario == "insert-error" && final.ToolsFailed != `["cluster-b"]` {
							t.Fatalf("failed persistence counted as completed tool: %+v", final)
						}
						return
					}
					if final.Status != models.ScanStatusCompleted {
						t.Fatalf("scan failed: %+v", final)
					}

					rows, err := store.ListFindingEvidenceByScan(ctx, scan.ID)
					if err != nil || len(rows) != 2 {
						t.Fatalf("evidence lost: %d %v", len(rows), err)
					}
					wantVisible := 1
					if scenario == "finalization-conflict" {
						wantVisible = 0
					}
					primaries, err := store.ListFindingsByScan(ctx, scan.ID)
					if err != nil || len(primaries) != 1 || final.FindingCount != wantVisible {
						t.Fatalf("cluster count mismatch: %+v %+v %v", primaries, final, err)
					}
					gate, err := store.GetQualityGateResult(ctx, scan.ID, "default")
					wantGate := "fail"
					if wantVisible == 0 {
						wantGate = "pass"
					}
					if err != nil || gate.Status != wantGate || gate.FindingsRevision != final.FindingsRevision {
						t.Fatalf("lockfile gate mismatch: %+v %v", gate, err)
					}
					current, err := store.ListCurrentOpenFindings(ctx, user.ID, false, "")
					if err != nil || len(current) != 1 {
						t.Fatalf("fleet still counts tool members: %d %v", len(current), err)
					}
					byID := map[string]models.Finding{}
					for _, f := range rows {
						byID[f.ID] = f
					}
					files, err := store.ListScanArtifacts(ctx, scan.ID)
					if err != nil {
						t.Fatal(err)
					}
					found := false
					parsedTools := map[string]bool{}
					for _, file := range files {
						if _, err := artifacts.ReadArtifact(file); err != nil {
							t.Fatal(err)
						}
						name := filepath.Base(file.FilePath)
						if name == "cluster-a.json" || name == "cluster-b.json" {
							data, err := os.ReadFile(file.FilePath)
							if err != nil {
								t.Fatal(err)
							}
							var parsed []models.Finding
							if err := json.Unmarshal(data, &parsed); err != nil {
								t.Fatal(err)
							}
							if len(parsed) != 1 || parsed[0].ClusterID != primaries[0].ID || byID[parsed[0].ID].ID == "" {
								t.Fatalf("tool evidence artifact lost: %s", data)
							}
							parsedTools[name] = true
						}
						if filepath.Base(file.FilePath) != "findings.json" || file.FindingsRevision == nil || *file.FindingsRevision != final.FindingsRevision {
							continue
						}
						found = true
						data, err := os.ReadFile(file.FilePath)
						if err != nil {
							t.Fatal(err)
						}
						var report struct {
							Findings []models.Finding `json:"findings"`
							Tools    []struct {
								Name     string `json:"name"`
								Findings int    `json:"findings"`
							} `json:"tools"`
						}
						if err := json.Unmarshal(data, &report); err != nil {
							t.Fatal(err)
						}
						if len(report.Findings) != 1 {
							t.Fatalf("report did not cluster: %d", len(report.Findings))
						}
						if len(report.Tools) != 2 {
							t.Fatalf("report lost contributors: %+v", report.Tools)
						}
						for _, tool := range report.Tools {
							if tool.Findings != 1 {
								t.Fatalf("report tool count lost cluster: %+v", tool)
							}
						}
						for _, f := range report.Findings {
							row, ok := byID[f.ID]
							if f.Suppressed != row.Suppressed || f.Title != row.Title {
								t.Fatalf("published outdated findings: %+v vs %+v", f, row)
							}
							if f.BaselineState == "" || f.BaselineState != row.BaselineState || f.IntroducedInScanID != row.IntroducedInScanID || f.FirstSeenAt == nil || row.FirstSeenAt == nil || !f.FirstSeenAt.Equal(*row.FirstSeenAt) {
								t.Fatalf("report lost persisted baseline: %+v vs %+v", f, row)
							}
							if f.ID == "" || !ok {
								t.Fatalf("report finding has no persisted identity: %+v", f)
							}
							if row.AIContextScore != 0 || f.AIContextScore != 0 {
								t.Fatalf("invented AI assessment: row=%v report=%v", row.AIContextScore, f.AIContextScore)
							}
							if f.ModuleName == "" || row.ModuleName != f.ModuleName || row.CompositeScore != f.CompositeScore || len(row.CorroboratedBy) != 2 {
								t.Fatalf("persisted cluster differs from report: row=%+v report=%+v", row, f)
							}
						}
					}
					if len(parsedTools) != 2 {
						t.Fatalf("missing tool evidence artifacts: %+v", parsedTools)
					}
					if !found {
						t.Fatal("missing findings artifact")
					}
				})
			}
		})
	}
}

type clusterFailureStore struct {
	db.Store
	step            string
	completionCalls *int
}

func (s clusterFailureStore) CreateFindings(ctx context.Context, findings []models.Finding) error {
	if s.step == "insert-error" && len(findings) > 0 && findings[0].ToolName == "cluster-b" {
		return errors.New("fixture persistence failure")
	}
	return s.Store.CreateFindings(ctx, findings)
}
func (s clusterFailureStore) ListFindingsByScan(ctx context.Context, id string) ([]models.Finding, error) {
	if s.step == "read-error" {
		return nil, errors.New("fixture persistence failure")
	}
	return s.Store.ListFindingsByScan(ctx, id)
}
func (s clusterFailureStore) UpdateFinding(ctx context.Context, f *models.Finding) error {
	if s.step == "update-error" {
		return errors.New("fixture persistence failure")
	}
	return s.Store.UpdateFinding(ctx, f)
}

func clusterPostgresStore(t *testing.T) db.Store {
	store, _ := clusterPostgresStoreDSN(t)
	return store
}

func clusterPostgresStoreDSN(t *testing.T) (db.Store, string) {
	t.Helper()
	dsn := os.Getenv("WOLF_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("WOLF_TEST_POSTGRES_DSN is not configured")
	}
	u, err := url.Parse(dsn)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		t.Fatal("WOLF_TEST_POSTGRES_DSN must be a PostgreSQL URL")
	}
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	schema := "cluster_" + uuid.NewString()
	if _, err := admin.Exec(`CREATE SCHEMA "` + schema + `"`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(`DROP SCHEMA "` + schema + `" CASCADE`); err != nil {
			t.Error(err)
		}
	})
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	store, err := db.NewPostgres(u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store, u.String()
}

func (s clusterFailureStore) FinalizeFindingClusters(ctx context.Context, scanID, leaseToken string, clusters []models.Finding) error {
	if s.step == "update-error" {
		return errors.New("fixture persistence failure")
	}
	return s.Store.FinalizeFindingClusters(ctx, scanID, leaseToken, clusters)
}
func (s clusterFailureStore) ListFindingEvidenceByScan(ctx context.Context, id string) ([]models.Finding, error) {
	if s.step == "read-error" {
		return nil, errors.New("fixture persistence failure")
	}
	return s.Store.ListFindingEvidenceByScan(ctx, id)
}

func (s clusterFailureStore) CreateScanArtifact(ctx context.Context, artifact *models.ScanArtifact) error {
	if (s.step == "artifact-after-conflict" && s.completionCalls != nil && *s.completionCalls > 0) || s.step == "artifact-error" || (s.step == "gate-artifact-error" && filepath.Base(artifact.FilePath) == "gate-result.json") {
		return errors.New("fixture artifact persistence failure")
	}
	return s.Store.CreateScanArtifact(ctx, artifact)
}

func (s clusterFailureStore) PersistFindingBaseline(ctx context.Context, scanID, leaseToken string, at time.Time) error {
	if s.step == "baseline-error" {
		return errors.New("fixture baseline persistence failure")
	}
	return s.Store.PersistFindingBaseline(ctx, scanID, leaseToken, at)
}

func (s clusterFailureStore) ListQualityPolicies(ctx context.Context, scope, id string) ([]models.QualityPolicy, error) {
	if s.step == "gate-error" || (s.step == "policy-after-conflict" && s.completionCalls != nil && *s.completionCalls > 0) {
		return nil, errors.New("fixture gate policy read failure")
	}
	return s.Store.ListQualityPolicies(ctx, scope, id)
}

func (s clusterFailureStore) CompleteScan(ctx context.Context, scan *models.Scan, completion db.ScanCompletion) (bool, error) {
	stored, err := s.Store.GetScanByID(ctx, scan.ID)
	if err != nil {
		return false, err
	}
	if stored.Status != models.ScanStatusRunning || stored.LeaseToken != scan.LeaseToken {
		return false, errors.New("scan published completion before required writes")
	}
	if s.step == "auto-baseline-error" || s.step == "gate-artifact-error" || s.step == "canonical-error" {
		return false, errors.New("fixture completion persistence failure")
	}
	if s.completionCalls != nil {
		(*s.completionCalls)++
	}
	if s.step == "finalization-conflict" || s.step == "continuous-conflict" || s.step == "cancel-on-retry" || s.step == "lease-loss-on-retry" || s.step == "artifact-after-conflict" || s.step == "policy-after-conflict" {
		rows, err := s.Store.ListFindingsByScan(ctx, scan.ID)
		if err != nil {
			return false, err
		}
		if len(rows) != 1 {
			return false, errors.New("fixture expects one primary")
		}
		if !rows[0].Suppressed || s.step == "continuous-conflict" {
			rows[0].Suppressed, rows[0].SuppressedReason = true, "concurrent triage"
			rows[0].Title += " updated while publishing"
			if err := s.Store.UpdateFinding(ctx, &rows[0]); err != nil {
				return false, err
			}
		}
	}
	ok, err := s.Store.CompleteScan(ctx, scan, completion)
	if errors.Is(err, db.ErrStaleFindings) && s.step == "cancel-on-retry" {
		if cancelErr := s.Store.RequestScanCancellation(ctx, scan.ID, time.Now()); cancelErr != nil {
			return false, cancelErr
		}
	}
	if errors.Is(err, db.ErrStaleFindings) && s.step == "lease-loss-on-retry" {
		if _, reclaimErr := s.Store.ReclaimStaleScans(ctx, time.Now().Add(time.Hour)); reclaimErr != nil {
			return false, reclaimErr
		}
	}
	return ok, err
}
