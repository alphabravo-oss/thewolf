package db

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

func TestFindingSnapshotsPostgres(t *testing.T) {
	s := newClusterPostgresStore(t)
	testFindingSnapshots(t, s, s.db)
}
func testFindingSnapshots(t *testing.T, s Store, database *sqlx.DB) {
	ctx := t.Context()
	user, repo := uuid.NewString(), uuid.NewString()
	if err := s.CreateUser(ctx, &models.User{ID: user, Email: user + "@example.test", PasswordHash: "hash"}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateRepo(ctx, &models.Repo{ID: repo, UserID: user, Name: "snapshot", SourceType: models.SourceTypeLocal, SourcePath: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	scan := &models.Scan{ID: uuid.NewString(), UserID: user, RepoID: repo, Status: models.ScanStatusCompleted}
	if err := s.CreateScan(ctx, scan); err != nil {
		t.Fatal(err)
	}
	f := models.Finding{ID: uuid.NewString(), ScanID: scan.ID, RepoID: repo, Title: "evidence", ToolName: "a", Status: models.StatusOpen}
	if err := s.CreateFinding(ctx, &f); err != nil {
		t.Fatal(err)
	}
	initial, _, err := s.GetScanFindingSnapshot(ctx, scan.ID)
	if err != nil {
		t.Fatal(err)
	}
	legacyGate := models.QualityGateResult{ScanStatus: models.ScanStatusCompleted, ID: uuid.NewString(), ScanID: scan.ID, PolicyID: "default", Status: "pass", SummaryJSON: `{}`, MatchedRulesJSON: `[]`, FindingsRevision: initial.FindingsRevision}
	if err := s.UpsertQualityGateResult(ctx, &legacyGate); err != nil {
		t.Fatal(err)
	}
	legacyArtifact := models.ScanArtifact{ID: uuid.NewString(), ScanID: scan.ID, ArtifactType: models.ArtifactJSON, FilePath: "legacy.json"}
	if err := s.CreateScanArtifact(ctx, &legacyArtifact); err != nil {
		t.Fatal(err)
	}
	// Additive migration preserves old rows and is repeatable on both backends.
	for _, table := range []string{"scans", "quality_gate_results", "scan_artifacts"} {
		if _, err := database.Exec("ALTER TABLE " + table + " DROP COLUMN findings_revision"); err != nil {
			t.Fatal(err)
		}
	}
	reconnectPostgresTestStore(s)
	if err := s.Migrate(); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(); err != nil {
		t.Fatal(err)
	}
	artifacts, err := s.ListScanArtifacts(ctx, scan.ID)
	if err != nil || len(artifacts) != 1 || artifacts[0].FindingsRevision != nil {
		t.Fatalf("legacy artifact revision: %+v %v", artifacts, err)
	}
	revision := int64(7)
	versionedArtifact := legacyArtifact
	versionedArtifact.ID = uuid.NewString()
	versionedArtifact.FindingsRevision = &revision
	versionedArtifact.StorageKey = "scan/snapshot/findings.json"
	versionedArtifact.ChecksumSHA256 = "checksum"
	if err := s.CreateScanArtifact(ctx, &versionedArtifact); err != nil {
		t.Fatal(err)
	}
	artifacts, err = s.ListScanArtifacts(ctx, scan.ID)
	if err != nil || len(artifacts) != 2 || artifacts[1].FindingsRevision == nil || *artifacts[1].FindingsRevision != revision || artifacts[1].StorageKey != versionedArtifact.StorageKey || artifacts[1].ChecksumSHA256 != versionedArtifact.ChecksumSHA256 {
		t.Fatalf("artifact metadata lost: %+v %v", artifacts, err)
	}
	snap, rows, err := s.GetScanFindingSnapshot(ctx, scan.ID)
	if err != nil || len(rows) != 1 || snap.FindingsRevision != 0 {
		t.Fatalf("upgrade snapshot: %+v %+v %v", snap, rows, err)
	}
	if cached, err := s.ListQualityGateResults(ctx, scan.ID); err != nil || len(cached) != 0 {
		t.Fatalf("legacy unversioned gate appeared current: %+v %v", cached, err)
	}
	gate := models.QualityGateResult{ScanStatus: models.ScanStatusCompleted, ID: uuid.NewString(), ScanID: scan.ID, PolicyID: "default", Status: "fail", SummaryJSON: `{}`, MatchedRulesJSON: `[]`, FindingsRevision: snap.FindingsRevision}
	if err := s.UpsertQualityGateResult(ctx, &gate); err != nil {
		t.Fatal(err)
	}
	f.Confidence = "low"
	if err := s.UpdateFinding(ctx, &f); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertQualityGateResult(ctx, &gate); !errors.Is(err, ErrStaleFindings) {
		t.Fatalf("stale gate accepted: %v", err)
	}
	if cached, err := s.ListQualityGateResults(ctx, scan.ID); err != nil || len(cached) != 0 {
		t.Fatalf("obsolete cached gate returned: %+v %v", cached, err)
	}
	// Ordinary progress updates carry old structs; they cannot reset the revision.
	if err := s.UpdateScan(ctx, scan); err != nil {
		t.Fatal(err)
	}
	snap, _, err = s.GetScanFindingSnapshot(ctx, scan.ID)
	if err != nil || snap.FindingsRevision != 1 {
		t.Fatalf("progress overwrote revision: %+v %v", snap, err)
	}
	// A failed batch must undo both its insert and its revision increment.
	duplicate := f
	fresh := f
	fresh.ID = uuid.NewString()
	if err := s.CreateFindings(ctx, []models.Finding{fresh, duplicate}); err == nil {
		t.Fatal("duplicate batch accepted")
	}
	after, rows, err := s.GetScanFindingSnapshot(ctx, scan.ID)
	if err != nil || len(rows) != 1 || after.FindingsRevision != snap.FindingsRevision {
		t.Fatalf("partial failed batch: %+v %+v %v", after, rows, err)
	}
	// Separate SELECTs must still observe the same committed revision and data.
	// The writer stores its sequence number in an ordinary persisted signal.
	base := after.FindingsRevision
	var wg sync.WaitGroup
	failures := make(chan error, 5)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 1; i <= 16; i++ {
			f.AIContextScore = float64(i)
			if err := s.UpdateFinding(ctx, &f); err != nil {
				failures <- err
				return
			}
		}
	}()
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 20 {
				snapshot, findings, err := s.GetScanFindingSnapshot(ctx, scan.ID)
				if err != nil {
					failures <- err
					return
				}
				if len(findings) != 1 || findings[0].AIContextScore != float64(snapshot.FindingsRevision-base) {
					failures <- fmt.Errorf("mixed snapshot revision %d, findings %+v", snapshot.FindingsRevision, findings)
					return
				}
				result := gate
				result.FindingsRevision = snapshot.FindingsRevision
				if err := s.UpsertQualityGateResult(ctx, &result); err != nil && !errors.Is(err, ErrStaleFindings) {
					failures <- err
					return
				}
			}
		}()
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	current, _, err := s.GetScanFindingSnapshot(ctx, scan.ID)
	if err != nil {
		t.Fatal(err)
	}
	gate.FindingsRevision = current.FindingsRevision
	if err := s.UpsertQualityGateResult(ctx, &gate); err != nil {
		t.Fatal(err)
	}
	cached, err := s.GetQualityGateResult(ctx, scan.ID, gate.PolicyID)
	if err != nil || cached.FindingsRevision != current.FindingsRevision {
		t.Fatalf("fresh gate: %+v %v", cached, err)
	}
	if err := s.UpdateFindingStatus(ctx, f.ID, models.StatusOpen); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetQualityGateResult(ctx, scan.ID, gate.PolicyID); err == nil {
		t.Fatal("triage did not invalidate gate")
	}
	leased := &models.Scan{ID: uuid.NewString(), RepoID: repo, UserID: user, Status: models.ScanStatusRunning, LeaseToken: "owner"}
	if err := s.CreateScan(ctx, leased); err != nil {
		t.Fatal(err)
	}
	foreign := f
	foreign.ID = uuid.NewString()
	if ok, err := s.CreateFindingsForScanLease(ctx, []models.Finding{foreign}, leased.ID, leased.LeaseToken); ok || !errors.Is(err, ErrStaleScanLease) {
		t.Fatalf("lease inserted evidence into a different scan: ok=%v err=%v", ok, err)
	}

	// Late artifact failures update status only, preserving current finding state.
	before, err := s.GetScanByID(ctx, scan.ID)
	if err != nil {
		t.Fatal(err)
	}
	before.FindingCount = 9
	if err := s.UpdateScan(ctx, before); err != nil {
		t.Fatal(err)
	}
	if err := s.FailCompletedScan(ctx, scan.ID, "artifact_persist_failed", "fixture"); err != nil {
		t.Fatal(err)
	}
	failed, err := s.GetScanByID(ctx, scan.ID)
	if err != nil || failed.Status != models.ScanStatusFailed || failed.FailureCode != "artifact_persist_failed" || failed.FindingCount != 9 || failed.FindingsRevision != before.FindingsRevision {
		t.Fatalf("late failure lost state: %+v %v", failed, err)
	}
	for _, status := range []models.ScanStatus{models.ScanStatusCancelled, models.ScanStatusRunning, models.ScanStatusFailed} {
		failed.Status = status
		failed.FailureCode = "original"
		if err := s.UpdateScan(ctx, failed); err != nil {
			t.Fatal(err)
		}
		if err := s.FailCompletedScan(ctx, scan.ID, "replacement", "fixture"); err != nil {
			t.Fatal(err)
		}
		got, err := s.GetScanByID(ctx, scan.ID)
		if err != nil || got.Status != status || got.FailureCode != "original" {
			t.Fatalf("late failure rewrote terminal/active status: %+v %v", got, err)
		}
	}

}
