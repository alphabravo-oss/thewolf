package db

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/models"
)

func TestUpsertVulnerabilityMergesOnCanonicalKey(t *testing.T) {
	store, err := NewSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	repoID := uuid.NewString()
	first := &models.Vulnerability{
		ID: uuid.NewString(), RepoID: repoID, ScanID: "s1", CanonicalKey: "k1",
		Title: "one", Severity: models.SeverityLow, FindingIDs: []string{"f1"}, EvidenceCount: 1,
	}
	if err := store.UpsertVulnerability(ctx, first); err != nil {
		t.Fatal(err)
	}
	second := &models.Vulnerability{
		ID: uuid.NewString(), RepoID: repoID, ScanID: "s2", CanonicalKey: "k1",
		Title: "two", Severity: models.SeverityHigh, FindingIDs: []string{"f1", "f2"}, EvidenceCount: 2,
	}
	if err := store.UpsertVulnerability(ctx, second); err != nil {
		t.Fatal(err)
	}
	got, err := store.ListVulnerabilitiesByRepo(ctx, repoID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 row, got %+v", got)
	}
	if got[0].ID != first.ID {
		t.Fatalf("id changed on conflict: %s -> %s", first.ID, got[0].ID)
	}
	if got[0].Title != "two" || got[0].ScanID != "s2" || got[0].Severity != models.SeverityHigh {
		t.Fatalf("updated fields = %+v", got[0])
	}
	if got[0].EvidenceCount != 1 || len(got[0].FindingIDs) != 1 || got[0].FindingIDs[0] != "f1" {
		t.Fatalf("membership must stay until evidence moves: %+v", got[0])
	}
}
