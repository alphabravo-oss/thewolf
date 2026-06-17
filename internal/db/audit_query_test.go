package db

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/models"
)

func TestQueryAuditLog(t *testing.T) {
	store, err := NewSQLite(":memory:")
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	defer store.Close()
	ctx := context.Background()

	// Seed: 3 GETs on /repos, 2 POSTs on /scans, with ascending status + time.
	seed := []struct {
		method, path string
		status       int
	}{
		{"GET", "/repos", 200},
		{"GET", "/repos/1", 200},
		{"GET", "/repos/2", 404},
		{"POST", "/scans", 201},
		{"POST", "/scans", 500},
	}
	base := time.Now().UTC().Add(-time.Hour)
	for i, e := range seed {
		// Classify the POSTs as security/critical so the filter test has data.
		cat, sev := "data", "info"
		if e.method == "POST" {
			cat, sev = "security", "critical"
		}
		if err := store.AppendAuditLog(ctx, &models.AuditLogEntry{
			ID: uuid.New().String(), UserID: "u", Action: "x",
			Method: e.method, Path: e.path, StatusCode: e.status,
			Category: cat, Severity: sev,
			CreatedAt: base.Add(time.Duration(i) * time.Minute),
		}); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}

	// Category + severity filters.
	if _, total, _ := store.QueryAuditLog(ctx, AuditQuery{Category: "security", Limit: 50}); total != 2 {
		t.Errorf("category=security: total=%d, want 2", total)
	}
	if _, total, _ := store.QueryAuditLog(ctx, AuditQuery{Severity: "critical", Method: "POST", Limit: 50}); total != 2 {
		t.Errorf("severity=critical+POST: total=%d, want 2", total)
	}

	// Search by path substring.
	es, total, err := store.QueryAuditLog(ctx, AuditQuery{Search: "repos", Limit: 50})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if total != 3 || len(es) != 3 {
		t.Errorf("search repos: total=%d len=%d, want 3/3", total, len(es))
	}

	// Method filter.
	_, total, _ = store.QueryAuditLog(ctx, AuditQuery{Method: "post", Limit: 50})
	if total != 2 {
		t.Errorf("method=post: total=%d, want 2", total)
	}

	// Pagination: page size 2 over all 5.
	es, total, _ = store.QueryAuditLog(ctx, AuditQuery{Limit: 2, Offset: 0, Desc: true})
	if total != 5 || len(es) != 2 {
		t.Errorf("page 1: total=%d len=%d, want 5/2", total, len(es))
	}
	es2, _, _ := store.QueryAuditLog(ctx, AuditQuery{Limit: 2, Offset: 4, Desc: true})
	if len(es2) != 1 {
		t.Errorf("last page: len=%d, want 1", len(es2))
	}

	// Sort by status descending → first row is the 500.
	es, _, _ = store.QueryAuditLog(ctx, AuditQuery{SortBy: "status", Desc: true, Limit: 1})
	if len(es) != 1 || es[0].StatusCode != 500 {
		t.Errorf("sort status desc: got %+v, want first=500", es)
	}
}
