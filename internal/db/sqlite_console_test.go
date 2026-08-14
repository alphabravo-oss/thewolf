package db

import (
	"context"
	"testing"
	"time"

	"github.com/alphabravocompany/thewolf/internal/models"
)

func TestFixerConsoleQueueClaimStdinAndReclaim(t *testing.T) {
	store, err := NewSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()

	c := &models.FixerConsole{
		ID:     "cons-1",
		UserID: "u1",
		Kind:   models.FixerConsoleLogin,
		Engine: "claude",
		Status: models.FixerConsoleQueued,
	}
	if err := store.EnqueueFixerConsole(ctx, c); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	claimed, err := store.ClaimNextFixerConsole(ctx, "w1")
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if claimed == nil || claimed.ID != "cons-1" || claimed.Status != models.FixerConsoleClaimed {
		t.Fatalf("claim = %+v", claimed)
	}
	empty, err := store.ClaimNextFixerConsole(ctx, "w2")
	if err != nil {
		t.Fatalf("second claim: %v", err)
	}
	if empty != nil {
		t.Fatalf("expected empty queue, got %+v", empty)
	}

	if err := store.AppendFixerConsoleStdin(ctx, claimed.ID, "y\n"); err != nil {
		t.Fatalf("stdin: %v", err)
	}
	if err := store.AppendFixerConsoleStdin(ctx, claimed.ID, "n\n"); err != nil {
		t.Fatalf("stdin: %v", err)
	}
	chunks, err := store.DrainFixerConsoleStdin(ctx, claimed.ID)
	if err != nil {
		t.Fatalf("drain: %v", err)
	}
	if len(chunks) != 2 || chunks[0] != "y\n" || chunks[1] != "n\n" {
		t.Fatalf("chunks = %#v", chunks)
	}
	again, err := store.DrainFixerConsoleStdin(ctx, claimed.ID)
	if err != nil {
		t.Fatalf("second drain: %v", err)
	}
	if len(again) != 0 {
		t.Fatalf("expected empty drain, got %#v", again)
	}

	old := time.Now().UTC().Add(-time.Hour)
	claimed.Status = models.FixerConsoleRunning
	claimed.HeartbeatAt = &old
	if err := store.UpdateFixerConsole(ctx, claimed); err != nil {
		t.Fatalf("update: %v", err)
	}
	n, err := store.ReclaimStaleConsoles(ctx, time.Now().UTC().Add(-time.Minute))
	if err != nil {
		t.Fatalf("reclaim: %v", err)
	}
	if n != 1 {
		t.Fatalf("reclaimed %d, want 1", n)
	}
	got, err := store.GetFixerConsoleByID(ctx, claimed.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.Status != models.FixerConsoleQueued {
		t.Fatalf("status = %s, want queued", got.Status)
	}
}
