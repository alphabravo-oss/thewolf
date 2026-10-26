package db

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/alphabravocompany/thewolf/internal/models"
)

func TestFixerConsoleClaimFencing(t *testing.T) {
	for _, backend := range []string{"sqlite", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var store Store
			if backend == "postgres" {
				store = newClusterPostgresStore(t)
			} else {
				store = newTestStore(t)
			}
			ctx := t.Context()
			queued := &models.FixerConsole{ID: "console", Kind: models.FixerConsoleShell}
			if err := store.EnqueueFixerConsole(ctx, queued); err != nil {
				t.Fatal(err)
			}
			claim, err := store.ClaimNextFixerConsole(ctx, "worker")
			if err != nil || claim == nil {
				t.Fatalf("claim: %+v %v", claim, err)
			}
			stale := *claim
			oldTime := claim.ClaimedAt.Add(-time.Second)
			stale.ClaimedAt = &oldTime // Even reuse of the worker label cannot renew another generation.
			if err := store.HeartbeatFixerConsole(ctx, stale.ID, stale.ClaimedBy, oldTime); !errors.Is(err, ErrFixerConsoleConflict) {
				t.Fatalf("old heartbeat: %v", err)
			}
			stale.Status = models.FixerConsoleExited
			if err := store.UpdateFixerConsole(ctx, &stale); !errors.Is(err, ErrFixerConsoleConflict) {
				t.Fatalf("old result: %v", err)
			}
			claim.Status, claim.LastURL = models.FixerConsoleRunning, "https://example.test/login"
			if err := store.UpdateFixerConsole(ctx, claim); err != nil {
				t.Fatal(err)
			}
			if err := store.HeartbeatFixerConsole(ctx, claim.ID, claim.ClaimedBy, *claim.ClaimedAt); err != nil {
				t.Fatal(err)
			}
			current, err := store.GetFixerConsoleByID(ctx, claim.ID)
			if err != nil || current.Status != models.FixerConsoleRunning || current.LastURL != claim.LastURL {
				t.Fatalf("heartbeat rewrote progress: %+v %v", current, err)
			}
			if err := store.AppendFixerConsoleStdin(ctx, claim.ID, "only once\n"); err != nil {
				t.Fatal(err)
			}
			if _, err := store.DrainFixerConsoleStdin(ctx, &stale); !errors.Is(err, ErrFixerConsoleConflict) {
				t.Fatalf("old claim consumed input: %v", err)
			}
			var wg sync.WaitGroup
			chunks := make(chan string, 2)
			for range 2 {
				wg.Go(func() {
					data, err := store.DrainFixerConsoleStdin(ctx, claim)
					if err != nil {
						t.Error(err)
					}
					for _, value := range data {
						chunks <- value
					}
				})
			}
			wg.Wait()
			close(chunks)
			if len(chunks) != 1 || <-chunks != "only once\n" {
				t.Fatal("input was lost or delivered twice")
			}
			current.Status = models.FixerConsoleCancelled
			if err := store.UpdateFixerConsole(ctx, current); err != nil {
				t.Fatal(err)
			}
			claim.Status = models.FixerConsoleExited
			if err := store.UpdateFixerConsole(ctx, claim); !errors.Is(err, ErrFixerConsoleConflict) {
				t.Fatalf("result overwrote cancellation: %v", err)
			}
			if err := store.HeartbeatFixerConsole(ctx, claim.ID, claim.ClaimedBy, *claim.ClaimedAt); !errors.Is(err, ErrFixerConsoleConflict) {
				t.Fatalf("cancelled heartbeat: %v", err)
			}
			if _, err := store.DrainFixerConsoleStdin(ctx, claim); !errors.Is(err, ErrFixerConsoleConflict) {
				t.Fatalf("cancelled input: %v", err)
			}

			queued.ID = "expired"
			if err := store.EnqueueFixerConsole(ctx, queued); err != nil {
				t.Fatal(err)
			}
			claim, err = store.ClaimNextFixerConsole(ctx, "worker")
			if err != nil || claim == nil {
				t.Fatalf("claim: %+v %v", claim, err)
			}
			if n, err := store.ReclaimStaleConsoles(ctx, time.Now().Add(time.Hour)); err != nil || n != 1 {
				t.Fatalf("expire: %d %v", n, err)
			}
			current, err = store.GetFixerConsoleByID(ctx, claim.ID)
			if err != nil || current.Status != models.FixerConsoleExited || current.Error == "" || current.FinishedAt == nil {
				t.Fatalf("expiry not explained: %+v %v", current, err)
			}
			claim.Status = models.FixerConsoleRunning
			if err := store.UpdateFixerConsole(ctx, claim); !errors.Is(err, ErrFixerConsoleConflict) {
				t.Fatalf("revived expired console: %v", err)
			}
			claim.Status = models.FixerConsoleExited
			if err := store.UpdateFixerConsole(ctx, claim); !errors.Is(err, ErrFixerConsoleConflict) {
				t.Fatalf("late result erased expiry error: %v", err)
			}
			if next, err := store.ClaimNextFixerConsole(ctx, "worker"); err != nil || next != nil {
				t.Fatalf("expired session replayed: %+v %v", next, err)
			}
			queued.ID, queued.Status = "cancel-before-claim", models.FixerConsoleQueued
			if err := store.EnqueueFixerConsole(ctx, queued); err != nil {
				t.Fatal(err)
			}
			queued.Status = models.FixerConsoleCancelled
			if err := store.UpdateFixerConsole(ctx, queued); err != nil {
				t.Fatalf("cancel unclaimed: %v", err)
			}
		})
	}
}
