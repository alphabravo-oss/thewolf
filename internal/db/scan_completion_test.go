package db

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

func TestScanCompletionSQLite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "completion.db")
	s, err := NewSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	testScanCompletion(t, s, s.db, func() *sqlx.DB {
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		reopened, err := NewSQLite(path)
		if err != nil {
			t.Fatal(err)
		}
		*s = *reopened
		return s.db
	})
}
func TestScanCompletionPostgres(t *testing.T) {
	s := newClusterPostgresStore(t)
	testScanCompletion(t, s, s.db, func() *sqlx.DB { reconnectPostgresTestStore(s); return s.db })
}

func testScanCompletion(t *testing.T, s Store, database *sqlx.DB, restart func() *sqlx.DB) {
	ctx := t.Context()
	user, repo := uuid.NewString(), uuid.NewString()
	if err := s.CreateUser(ctx, &models.User{ID: user, Email: user + "@example.test", PasswordHash: "hash"}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateRepo(ctx, &models.Repo{ID: repo, UserID: user, Name: "completion"}); err != nil {
		t.Fatal(err)
	}
	candidate := func(lease string) (*models.Scan, ScanCompletion) {
		t.Helper()
		now := time.Now().UTC()
		scan := &models.Scan{ID: uuid.NewString(), RepoID: repo, UserID: user, Branch: uuid.NewString(), Status: models.ScanStatusRunning, Profile: "standard", ToolsSelected: `["a"]`, ToolsCompleted: `["a"]`, ToolsFailed: `[]`, LeaseToken: lease, ClaimedBy: "fixture", LeaseExpiresAt: &now, HeartbeatAt: &now}
		if err := s.CreateScan(ctx, scan); err != nil {
			t.Fatal(err)
		}
		if err := s.CreateFinding(ctx, &models.Finding{ID: uuid.NewString(), ScanID: scan.ID, RepoID: repo, StableFingerprint: scan.ID, ToolName: "a", Severity: models.SeverityHigh, Title: "native evidence"}); err != nil {
			t.Fatal(err)
		}
		snapshot, _, err := s.GetScanFindingSnapshot(ctx, scan.ID)
		if err != nil {
			t.Fatal(err)
		}
		scan.FindingsRevision = snapshot.FindingsRevision
		scan.Status, scan.Phase, scan.CompletedAt = models.ScanStatusCompleted, "completed", &now
		gate := &models.QualityGateResult{FindingsRevision: scan.FindingsRevision, ScanStatus: models.ScanStatusCompleted, ID: uuid.NewString(), ScanID: scan.ID, PolicyID: "default", Status: "pass", SummaryJSON: `{}`, MatchedRulesJSON: `[]`}
		artifact := &models.ScanArtifact{ID: uuid.NewString(), ScanID: scan.ID, ArtifactType: models.ArtifactJSON, FilePath: "gate-result.json", StorageKey: "fixture/gate-result.json", ChecksumSHA256: "fixture", FindingsRevision: &scan.FindingsRevision}
		imp := &models.SARIFImport{ID: uuid.NewString(), RepoID: repo, ScanID: scan.ID, CreatedBy: user, ChecksumSHA256: uuid.NewString()}
		return scan, ScanCompletion{Gate: gate, GateArtifact: artifact, AutoBaseline: true, SARIFImport: imp}
	}
	snapshot := func() string {
		t.Helper()
		var scans []models.Scan
		var gates []models.QualityGateResult
		var artifacts []models.ScanArtifact
		var baselines []models.ScanBaseline
		var imports []models.SARIFImport
		var vulnerabilities []models.Vulnerability
		var evidence []models.VulnerabilityEvidence
		for _, q := range []struct {
			dest  any
			query string
		}{{&scans, "SELECT * FROM scans ORDER BY id"}, {&gates, "SELECT * FROM quality_gate_results ORDER BY id"}, {&artifacts, "SELECT * FROM scan_artifacts ORDER BY id"}, {&baselines, "SELECT * FROM scan_baselines ORDER BY id"}, {&imports, "SELECT * FROM sarif_imports ORDER BY id"}, {&vulnerabilities, "SELECT * FROM vulnerabilities ORDER BY id"}, {&evidence, "SELECT * FROM vulnerability_evidence ORDER BY id"}} {
			if err := database.Select(q.dest, q.query); err != nil {
				t.Fatal(err)
			}
		}
		data, err := json.Marshal([]any{scans, gates, artifacts, baselines, imports, vulnerabilities, evidence})
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	scan, completion := candidate("current-lease")
	old := *completion.Gate
	old.ID, old.Status, old.ScanStatus = uuid.NewString(), "pending", models.ScanStatusRunning
	if err := s.UpsertQualityGateResult(ctx, &old); err != nil {
		t.Fatal(err)
	}
	// Fail each write; the last trigger fires after terminal state, gate, artifact
	// and baseline writes. Every failure must restore the full prior database state.
	for _, table := range []string{"vulnerabilities", "vulnerability_evidence", "quality_gate_results", "scan_artifacts", "scan_baselines", "sarif_imports"} {
		before := snapshot()
		query := "CREATE TRIGGER reject_completion BEFORE INSERT ON " + table + " BEGIN SELECT RAISE(ABORT,'fixture completion failure'); END"
		if database.DriverName() == "postgres" {
			if _, err := database.Exec(`CREATE OR REPLACE FUNCTION reject_completion_fn() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'fixture completion failure'; END $$`); err != nil {
				t.Fatal(err)
			}
			query = "CREATE TRIGGER reject_completion BEFORE INSERT ON " + table + " FOR EACH ROW EXECUTE FUNCTION reject_completion_fn()"
		}
		if _, err := database.Exec(query); err != nil {
			t.Fatal(err)
		}
		if ok, err := s.CompleteScan(ctx, scan, completion); err == nil || ok {
			t.Fatalf("%s failure ignored: %v %v", table, ok, err)
		}
		if snapshot() != before {
			t.Fatalf("%s failure left partial completion", table)
		}
		drop := "DROP TRIGGER reject_completion"
		if database.DriverName() == "postgres" {
			drop += " ON " + table
		}
		if _, err := database.Exec(drop); err != nil {
			t.Fatal(err)
		}
	}
	beforeRestart := snapshot()
	database = restart()
	if snapshot() != beforeRestart {
		t.Fatal("rollback state did not survive reopening")
	}
	if ok, err := s.CompleteScan(ctx, scan, completion); err != nil || !ok {
		t.Fatalf("retry: %v %v", ok, err)
	}
	got, err := s.GetScanByID(ctx, scan.ID)
	if err != nil || got.Status != models.ScanStatusCompleted || got.LeaseToken != "" || got.ClaimedBy != "" || got.LeaseExpiresAt != nil || got.HeartbeatAt != nil {
		t.Fatalf("incomplete commit: %+v %v", got, err)
	}
	gate, err := s.GetQualityGateResult(ctx, scan.ID, "default")
	if err != nil || gate.Status != "pass" || gate.ID != completion.Gate.ID {
		t.Fatalf("published gate identity differs: %+v %v", gate, err)
	}
	beforeRestart = snapshot()
	database = restart()
	if snapshot() != beforeRestart {
		t.Fatal("completion did not survive reopening")
	}
	if err := s.UpsertQualityGateResult(ctx, &old); !errors.Is(err, ErrStaleFindings) {
		t.Fatalf("running gate overwrote terminal evaluation: %v", err)
	}
	before := snapshot()
	if ok, err := s.CompleteScan(ctx, scan, completion); err != nil || ok {
		t.Fatalf("terminal replay mutated completion: %v %v", ok, err)
	}
	if snapshot() != before {
		t.Fatal("terminal replay changed completed data")
	}
	// The first valid import owns its checksum. A duplicate must roll back every
	// terminal write, including its automatic baseline, before it can report success.
	duplicate, dupCompletion := candidate("duplicate-lease")
	dupCompletion.SARIFImport.ChecksumSHA256 = completion.SARIFImport.ChecksumSHA256
	before = snapshot()
	if ok, err := s.CompleteScan(ctx, duplicate, dupCompletion); err == nil || ok {
		t.Fatalf("duplicate committed: %v %v", ok, err)
	}
	if snapshot() != before {
		t.Fatal("duplicate import left partial completion")
	}
	for _, mode := range []string{"stale-lease", "cancelled", "revision", "foreign-artifact", "inline"} {
		t.Run(mode, func(t *testing.T) {
			lease := "lease"
			if mode == "inline" {
				lease = ""
			}
			scan, completion := candidate(lease)
			switch mode {
			case "stale-lease":
				scan.LeaseToken = "stale"
			case "cancelled":
				if err := s.RequestScanCancellation(ctx, scan.ID, time.Now()); err != nil {
					t.Fatal(err)
				}
			case "revision":
				if err := s.CreateFinding(ctx, &models.Finding{ID: uuid.NewString(), ScanID: scan.ID, RepoID: repo, ToolName: "a", Title: "late", Severity: models.SeverityHigh}); err != nil {
					t.Fatal(err)
				}
			case "foreign-artifact":
				completion.GateArtifact.ScanID = "other"
			}
			before := snapshot()
			ok, err := s.CompleteScan(ctx, scan, completion)
			if mode == "inline" {
				if err != nil || !ok {
					t.Fatalf("inline completion: %v %v", ok, err)
				}
				return
			}
			if ok {
				t.Fatal("invalid completion accepted")
			}
			if mode == "revision" && !errors.Is(err, ErrStaleFindings) {
				t.Fatalf("revision: %v", err)
			}
			if mode == "foreign-artifact" && err == nil {
				t.Fatal("artifact scope not validated")
			}
			if snapshot() != before {
				t.Fatal("rejected completion changed database")
			}
		})
	}
	if _, err := database.Exec("ALTER TABLE quality_gate_results DROP COLUMN scan_status"); err != nil {
		t.Fatal(err)
	}
	reconnectPostgresTestStore(s)
	if err := s.Migrate(); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(); err != nil {
		t.Fatal(err)
	}
	legacy, err := s.ListQualityGateResults(ctx, scan.ID)
	if err != nil || len(legacy) != 0 {
		t.Fatalf("unbound legacy gate reused: %+v %v", legacy, err)
	}
	if err := s.UpsertQualityGateResult(ctx, completion.Gate); err != nil {
		t.Fatal(err)
	}
	fresh, err := s.GetQualityGateResult(ctx, scan.ID, "default")
	if err != nil || fresh.ScanStatus != models.ScanStatusCompleted {
		t.Fatalf("gate requalification: %+v %v", fresh, err)
	}

}
