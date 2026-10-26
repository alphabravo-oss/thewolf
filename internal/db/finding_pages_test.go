package db

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/trace"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

func TestScanFindingPagesPreserveVisibilityAndOrdering(t *testing.T) {
	for _, backend := range []string{"sqlite", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var store Store
			var database *sqlx.DB
			if backend == "sqlite" {
				s := newTestStore(t)
				store, database = s, s.db
			} else {
				s := newClusterPostgresStore(t)
				store, database = s, s.db
			}
			indexQuery := "SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='idx_findings_scan_visible_severity'"
			if backend == "postgres" {
				indexQuery = "SELECT COUNT(*) FROM pg_indexes WHERE schemaname=current_schema() AND indexname='idx_findings_scan_visible_severity'"
			}
			var indexes int
			if err := database.Get(&indexes, indexQuery); err != nil || indexes != 1 {
				t.Fatalf("page index missing: %d %v", indexes, err)
			}
			if err := execAdditiveMigration(database, migration070SQL); err != nil {
				t.Fatalf("index migration replay: %v", err)
			}
			ctx := t.Context()
			user, repo, id := uuid.NewString(), uuid.NewString(), uuid.NewString()
			root := t.TempDir()
			if err := store.CreateUser(ctx, &models.User{ID: user, Email: user + "@example.test", PasswordHash: "unused"}); err != nil {
				t.Fatal(err)
			}
			if err := store.CreateRepo(ctx, &models.Repo{ID: repo, UserID: user, Name: "pages", SourcePath: root}); err != nil {
				t.Fatal(err)
			}
			scan := &models.Scan{ID: id, RepoID: repo, UserID: user, Status: models.ScanStatusRunning}
			if err := store.CreateScan(ctx, scan); err != nil {
				t.Fatal(err)
			}
			rows := make([]models.Finding, 8)
			for i := range rows {
				rows[i] = models.Finding{ID: string(rune('a' + i)), ScanID: id, RepoID: repo, ToolName: "alpha", Category: models.CategorySAST, Severity: models.SeverityHigh, Title: "same", FilePath: "src/app.go", RuleID: string(rune('A' + i)), Status: models.StatusOpen, SARIFData: strings.Repeat("native", 1000)}
			}
			rows[0].Title = "100%_ secure"
			rows[1].Suppressed, rows[1].SuppressedReason = true, "fixture accepted risk"
			rows[2].Severity, rows[2].ToolName, rows[2].CorroboratedBy = models.SeverityMedium, "beta", []string{"beta", "semgrep"}
			rows[4].Severity, rows[4].FilePath = models.SeverityCritical, "vendor/app.go"
			rows[5].Severity, rows[5].Status = models.SeverityLow, models.StatusFalsePositive
			rows[6].RuleID = "A"
			rows[7].Category, rows[7].FilePath = models.CategorySCA, "Cargo.lock"
			if err := store.CreateFindings(ctx, rows); err != nil {
				t.Fatal(err)
			}
			if _, err := database.Exec(database.Rebind("UPDATE findings SET cluster_id='a' WHERE id IN ('a','g')")); err != nil {
				t.Fatal(err)
			}
			if _, err := store.QueryScanFindings(ctx, id, FindingListQuery{}); !errors.Is(err, ErrLiveFindingPolicy) {
				t.Fatalf("live policy bypassed: %v", err)
			}
			if err := store.CaptureScanIgnorePolicy(ctx, id, "", root); err != nil {
				t.Fatal(err)
			}
			checks := []struct {
				name              string
				q                 FindingListQuery
				want              string
				total, suppressed int
			}{
				{"stable page", FindingListQuery{SortBy: "severity", Desc: true, Limit: 2, Offset: 1}, "dh", 5, 2},
				{"contributor", FindingListQuery{Tools: []string{"semgrep"}}, "c", 1, 2},
				{"exact tool", FindingListQuery{Tools: []string{"a"}}, "", 0, 2},
				{"literal search", FindingListQuery{Search: "%_"}, "a", 1, 2},
				{"category severity status", FindingListQuery{Categories: []string{"sca"}, Severities: []string{"high"}, Statuses: []string{"open"}}, "h", 1, 2},
				{"empty page", FindingListQuery{Offset: 99999}, "", 5, 2},
				{"with suppressed", FindingListQuery{IncludeSuppressed: true}, "abcdefh", 7, 0},
			}
			for _, tc := range checks {
				t.Run(tc.name, func(t *testing.T) {
					page, err := store.QueryScanFindings(ctx, id, tc.q)
					if err != nil {
						t.Fatal(err)
					}
					got := ""
					for _, f := range page.Findings {
						got += f.ID
						if f.SARIFData != "" {
							t.Fatal("native SARIF leaked into list")
						}
					}
					if got != tc.want || page.Total != tc.total || page.Suppressed != tc.suppressed || page.Findings == nil {
						t.Fatalf("got %s total=%d suppressed=%d; want %+v", got, page.Total, page.Suppressed, tc)
					}
				})
			}
			page, err := store.QueryScanFindings(ctx, id, FindingListQuery{IncludeSARIF: true, Search: "%_"})
			if err != nil || len(page.Findings) != 1 || page.Findings[0].SARIFData != rows[0].SARIFData {
				t.Fatalf("explicit SARIF: %+v %v", page, err)
			}
			stats, err := store.QueryScanFindings(ctx, id, FindingListQuery{StatsOnly: true})
			if err != nil || stats.Stats.Total != 5 || stats.Stats.BySeverity["high"] != 3 || stats.Stats.ByTool["alpha"] != 4 || stats.Stats.ByTool["semgrep"] != 1 {
				t.Fatalf("aggregate parity: %+v %v", stats, err)
			}
			if err := os.WriteFile(filepath.Join(root, ".wolfignore"), []byte("**\n"), 0600); err != nil {
				t.Fatal(err)
			}
			rule := &models.FindingSuppression{ID: uuid.NewString(), RepoID: repo, CreatedBy: user, ScopeType: models.SuppressionScopeRule, ScopeValue: "A", Reason: "accepted"}
			if err := store.CreateFindingSuppression(ctx, rule); err != nil {
				t.Fatal(err)
			}
			page, err = store.QueryScanFindings(ctx, id, FindingListQuery{})
			if err != nil || page.Total != 4 {
				t.Fatalf("durable suppression: %+v %v", page, err)
			}
			if err := store.RevokeFindingSuppression(ctx, rule.ID, user); err != nil {
				t.Fatal(err)
			}
			page, err = store.QueryScanFindings(ctx, id, FindingListQuery{})
			if err != nil || page.Total != 5 {
				t.Fatalf("revoke/frozen source: %+v %v", page, err)
			}
			global, err := store.QueryCurrentFindings(ctx, user, false, "", repo, FindingListQuery{SortBy: "severity", Desc: true, Limit: 2, Offset: 1})
			if err != nil || global.Total != 4 || len(global.Findings) != 2 || global.Findings[0].ID != "d" || global.Findings[1].ID != "h" {
				t.Fatalf("global page: %+v %v", global, err)
			}
			global, err = store.QueryCurrentFindings(ctx, user, false, "", "", FindingListQuery{Tools: []string{"semgrep"}})
			if err != nil || global.Total != 1 || len(global.Findings) != 1 || global.Findings[0].ID != "c" || global.Findings[0].SARIFData != "" {
				t.Fatalf("global contributor: %+v %v", global, err)
			}
			global, err = store.QueryCurrentFindings(ctx, "another-user", false, "", "", FindingListQuery{})
			if err != nil || global.Total != 0 || global.Findings == nil {
				t.Fatalf("global ownership: %+v %v", global, err)
			}
			global, err = store.QueryCurrentFindings(ctx, user, false, "missing-collection", "", FindingListQuery{})
			if err != nil || global.Total != 0 {
				t.Fatalf("global collection: %+v %v", global, err)
			}
			if _, err := database.Exec(database.Rebind("UPDATE scans SET ignore_policy_json='{' WHERE id=?"), id); err != nil {
				t.Fatal(err)
			}
			if _, err := store.QueryScanFindings(ctx, id, FindingListQuery{Offset: 9999}); err == nil {
				t.Fatal("corrupt policy became empty success")
			}
		})
	}
}

func TestVulnerabilityPagesFilterBeforePaging(t *testing.T) {
	for _, backend := range []string{"sqlite", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var store Store
			if backend == "sqlite" {
				store = newTestStore(t)
			} else {
				store = newClusterPostgresStore(t)
			}
			ctx := t.Context()
			user, repo := uuid.NewString(), uuid.NewString()
			if err := store.CreateUser(ctx, &models.User{ID: user, Email: user + "@example.test", PasswordHash: "unused"}); err != nil {
				t.Fatal(err)
			}
			if err := store.CreateRepo(ctx, &models.Repo{ID: repo, UserID: user, Name: "pages"}); err != nil {
				t.Fatal(err)
			}
			for _, v := range []models.Vulnerability{
				{ID: "a", RepoID: repo, CanonicalKey: "a", Title: "same", Severity: models.SeverityHigh, EvidenceCount: 1},
				{ID: "b", RepoID: repo, CanonicalKey: "b", Title: "same", Severity: models.SeverityHigh, EvidenceCount: 2},
				{ID: "c", RepoID: repo, CanonicalKey: "c", Title: "100%_ needle", Severity: models.SeverityCritical, EvidenceCount: 3},
				{ID: "private", RepoID: "not-owned", CanonicalKey: "private", Title: "100%_ needle", Severity: models.SeverityCritical},
			} {
				if err := store.UpsertVulnerability(ctx, &v); err != nil {
					t.Fatal(err)
				}
			}
			for _, tc := range []struct {
				q     VulnListQuery
				ids   string
				total int
			}{
				{VulnListQuery{UserID: user, SortBy: "severity", Desc: true, Limit: 1}, "c", 3},
				{VulnListQuery{UserID: user, SortBy: "severity", Desc: true, Limit: 1, Offset: 1}, "a", 3},
				{VulnListQuery{UserID: user, SortBy: "severity", Desc: true, Limit: 1, Offset: 2}, "b", 3},
				{VulnListQuery{UserID: user, Search: "%_", Limit: 1}, "c", 1},
				{VulnListQuery{UserID: user, Search: "absent"}, "", 0},
				{VulnListQuery{UserID: "other"}, "", 0},
				{VulnListQuery{UserID: user, SortBy: "evidence", Desc: true, Limit: 2}, "cb", 3},
			} {
				rows, total, err := store.ListVulnerabilitiesPage(ctx, tc.q)
				ids := ""
				for _, row := range rows {
					ids += row.ID
				}
				if err != nil || total != tc.total || ids != tc.ids || rows == nil {
					t.Fatalf("query %+v: %q total=%d err=%v", tc.q, ids, total, err)
				}
			}
		})
	}
}

// Run explicitly: WOLF_RUN_FINDING_PAGE_LOAD=1 go test ./internal/db -run TestFindingPageLoad -v -count=1.
// This is a local regression measurement, not production capacity certification.
func TestFindingPageLoad(t *testing.T) {
	if os.Getenv("WOLF_RUN_FINDING_PAGE_LOAD") != "1" {
		t.Skip("set WOLF_RUN_FINDING_PAGE_LOAD=1 for the 55k fixture")
	}
	if os.Getenv("WOLF_TEST_POSTGRES_DSN") == "" {
		t.Fatal("WOLF_TEST_POSTGRES_DSN is required: supported-load qualification must exercise both stores")
	}
	for _, backend := range []string{"sqlite", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var store Store
			var database *sqlx.DB
			if backend == "sqlite" {
				s, err := NewSQLite(filepath.Join(t.TempDir(), "load.db"))
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = s.Close() })
				store, database = s, s.db
			} else {
				s := newClusterPostgresStore(t)
				store, database = s, s.db
			}
			ctx := t.Context()
			user := uuid.NewString()
			if err := store.CreateUser(ctx, &models.User{ID: user, Email: user + "@example.test", PasswordHash: "unused"}); err != nil {
				t.Fatal(err)
			}
			for _, repos := range []int{1, 10} {
				// Each fixture owns 55,000 findings; the ten-repository case includes the
				// first fixture in the fleet, exercising 110,000 total current identities.
				var scanID string
				for r := 0; r < repos; r++ {
					repoID := uuid.NewString()
					scanID = uuid.NewString()
					if err := store.CreateRepo(ctx, &models.Repo{ID: repoID, UserID: user, Name: fmt.Sprintf("repo-%d-%d", repos, r)}); err != nil {
						t.Fatal(err)
					}
					if err := store.CreateScan(ctx, &models.Scan{ID: scanID, RepoID: repoID, UserID: user, Status: models.ScanStatusCompleted}); err != nil {
						t.Fatal(err)
					}
					_, err := database.ExecContext(ctx, database.Rebind(`WITH RECURSIVE numbers(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM numbers WHERE n < ?)
     INSERT INTO findings(id, scan_id, repo_id, fingerprint, tool_name, category, severity, title, file_path, line_start, rule_id, cwe_id, sarif_data, suppressed)
     SELECT ? || CAST(n AS TEXT), ?, ?, CAST(n AS TEXT), 'semgrep', 'sast', 'high', 'same title', 'src/app.go', n, 'rule', '', ?, (n % 10 = 0) FROM numbers`), 55000/repos, scanID+"-", scanID, repoID, strings.Repeat("x", 1024))
					if err != nil {
						t.Fatal(err)
					}
				}
				if _, err := database.ExecContext(ctx, "ANALYZE findings"); err != nil {
					t.Fatal(err)
				}
				explain := "EXPLAIN "
				if backend == "sqlite" {
					explain = "EXPLAIN QUERY PLAN "
				}
				query := explain + "SELECT " + findingPageColumns + " FROM findings WHERE scan_id = ? AND (cluster_id = '' OR cluster_id = id) AND suppressed = FALSE ORDER BY CASE severity WHEN 'critical' THEN 5 WHEN 'high' THEN 4 WHEN 'medium' THEN 3 WHEN 'low' THEN 2 WHEN 'info' THEN 1 ELSE 0 END DESC, id ASC LIMIT 50"
				plan, err := database.QueryxContext(ctx, database.Rebind(query), scanID)
				if err != nil {
					t.Fatal(err)
				}
				for plan.Next() {
					row := map[string]any{}
					if err := plan.MapScan(row); err != nil {
						t.Fatal(err)
					}
					t.Logf("repos_added=%d explain=%v", repos, row)
				}
				if err := plan.Err(); err != nil {
					t.Fatal(err)
				}
				_ = plan.Close()
				for _, mode := range []string{"scan_page", "scan_stats", "fleet_page"} {
					read := func() (FindingPage, error) {
						q := FindingListQuery{Limit: 50, SortBy: "severity", Desc: true}
						switch mode {
						case "scan_page":
							return store.QueryScanFindings(ctx, scanID, q)
						case "scan_stats":
							q.StatsOnly = true
							return store.QueryScanFindings(ctx, scanID, q)
						default:
							return store.QueryCurrentFindings(ctx, user, false, "", "", q)
						}
					}
					if backend == "sqlite" {
						disk := store.(*SQLiteStore)
						disk.db.SetMaxIdleConns(0)
						disk.reader.SetMaxIdleConns(0)
						if disk.db.Stats().OpenConnections != 0 || disk.reader.Stats().OpenConnections != 0 {
							t.Fatal("SQLite cold read still has cached connections")
						}
						disk.db.SetMaxIdleConns(2)
						disk.reader.SetMaxIdleConns(4)
						t.Log("cold cache: all SQLite connection/page caches closed; OS page cache retained")
					} else {
						var schema string
						if err := database.Get(&schema, "SELECT current_schema()"); err != nil {
							t.Fatal(err)
						}
						if !strings.HasPrefix(schema, "clusters_") {
							t.Fatal("refusing cache eviction outside the owned test schema")
						}
						var evicted struct {
							Evicted int `db:"evicted"`
							Skipped int `db:"skipped"`
						}
						if err := database.Get(&evicted, `SELECT COALESCE(SUM(e.buffers_evicted),0) AS evicted, COALESCE(SUM(e.buffers_skipped),0) AS skipped
                          FROM pg_class c CROSS JOIN LATERAL public.pg_buffercache_evict_relation(c.oid::regclass) e
                          WHERE c.relnamespace=(SELECT oid FROM pg_namespace WHERE nspname=current_schema()) AND c.relkind IN ('r','i','m')`); err != nil {
							t.Fatalf("cold qualification requires PostgreSQL 18 pg_buffercache in the disposable database: %v", err)
						}
						if evicted.Skipped != 0 {
							t.Fatalf("cold cache retained %d pinned buffers", evicted.Skipped)
						}
						t.Logf("cold cache: evicted %d PostgreSQL buffers belonging to the owned test schema; OS page cache retained", evicted.Evicted)
					}
					first := time.Now()
					if _, err := read(); err != nil {
						t.Fatal(err)
					}
					coldDuration := time.Since(first)
					t.Logf("repos_added=%d mode=%s cold_database_cache_ms=%.2f", repos, mode, float64(coldDuration.Microseconds())/1000)
					runtime.GC()
					var before, after runtime.MemStats
					runtime.ReadMemStats(&before)
					done, peakResult := make(chan struct{}), make(chan uint64, 1)
					go func() {
						ticker := time.NewTicker(5 * time.Millisecond)
						defer ticker.Stop()
						peak := before.HeapAlloc
						for {
							select {
							case <-ticker.C:
								var sample runtime.MemStats
								runtime.ReadMemStats(&sample)
								peak = max(peak, sample.HeapAlloc)
							case <-done:
								peakResult <- peak
								return
							}
						}
					}()
					type measurement struct {
						duration time.Duration
						size     int
						err      error
					}
					results := make(chan measurement, 20)
					var workers sync.WaitGroup
					for worker := 0; worker < 2; worker++ {
						workers.Go(func() {
							for n := 0; n < 10; n++ {
								start := time.Now()
								page, err := read()
								region := trace.StartRegion(ctx, "finding.serialization")
								encoded, encodeErr := json.Marshal(page)
								region.End()
								if err == nil {
									err = encodeErr
								}
								if len(page.Findings) > 50 {
									err = fmt.Errorf("unbounded page: %d", len(page.Findings))
								}
								results <- measurement{time.Since(start), len(encoded), err}
							}
						})
					}
					workers.Wait()
					close(results)
					close(done)
					peak := <-peakResult
					runtime.ReadMemStats(&after)
					latencies := make([]time.Duration, 0, 20)
					responseBytes := 0
					for result := range results {
						if result.err != nil {
							t.Fatal(result.err)
						}
						latencies = append(latencies, result.duration)
						responseBytes = max(responseBytes, result.size)
					}
					slices.Sort(latencies)
					limit := 500 * time.Millisecond
					if mode == "scan_stats" {
						limit = time.Second
					}
					if mode == "fleet_page" {
						limit = 5 * time.Second
						if repos == 10 {
							limit = 10 * time.Second
						}
					}
					t.Logf("repos_added=%d mode=%s concurrency=2 warm_samples=20 p50_ms=%.2f p95_ms=%.2f mean_allocated_bytes=%d peak_heap_bytes=%d response_bytes=%d slo_p95_ms=%d", repos, mode, float64(latencies[9].Microseconds())/1000, float64(latencies[18].Microseconds())/1000, (after.TotalAlloc-before.TotalAlloc)/20, peak, responseBytes, limit.Milliseconds())
					if coldDuration > 2*limit {
						t.Errorf("%s cold database read %s exceeds 2x warm SLO", mode, coldDuration)
					}
					if latencies[18] > limit {
						t.Errorf("%s p95 %s exceeds supported-load SLO %s", mode, latencies[18], limit)
					}
					if peak > 1536*1024*1024 {
						t.Errorf("peak Go heap %d exceeds 1.5 GiB supported-load limit", peak)
					}
				}
			}
		})
	}
}
