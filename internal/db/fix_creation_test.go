package db

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

func TestFixCreationAtomicReplayAndScope(t *testing.T) {
	for _, backend := range []string{"sqlite", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var store Store
			var database *sqlx.DB
			var reopen func()
			var writers []Store
			if backend == "sqlite" {
				path := filepath.Join(t.TempDir(), "fixes.db")
				open := func() {
					s, err := NewSQLite(path)
					if err != nil {
						t.Fatal(err)
					}
					store, database = s, s.db
					t.Cleanup(func() { _ = s.Close() })
				}
				open()
				peer, err := NewSQLite(path)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = peer.Close() })
				writers = []Store{store, peer}
				reopen = open
			} else {
				s := newClusterPostgresStore(t)
				store, database = s, s.db
				writers = []Store{store}
				reopen = func() {
					var schema string
					if err := database.Get(&schema, "SELECT current_schema()"); err != nil {
						t.Fatal(err)
					}
					if err := store.Close(); err != nil {
						t.Fatal(err)
					}
					reopened, err := NewPostgres(postgresDSNWithSearchPath(t, os.Getenv("WOLF_TEST_POSTGRES_DSN"), schema))
					if err != nil {
						t.Fatal(err)
					}
					store, database = reopened, reopened.db
					t.Cleanup(func() { _ = reopened.Close() })
				}
			}
			ctx := t.Context()
			user, repo, scan := uuid.NewString(), uuid.NewString(), uuid.NewString()
			if err := store.CreateUser(ctx, &models.User{ID: user, Email: user + "@example.test", PasswordHash: "unused"}); err != nil {
				t.Fatal(err)
			}
			if err := store.CreateRepo(ctx, &models.Repo{ID: repo, UserID: user, Name: "fixes", SourcePath: t.TempDir()}); err != nil {
				t.Fatal(err)
			}
			if err := store.CreateScan(ctx, &models.Scan{ID: scan, RepoID: repo, UserID: user, Branch: "release", Status: models.ScanStatusCompleted}); err != nil {
				t.Fatal(err)
			}
			finding := models.Finding{ID: uuid.NewString(), ScanID: scan, RepoID: repo, ToolName: "gosec", RuleID: "G1"}
			if err := store.CreateFindings(ctx, []models.Finding{finding}); err != nil {
				t.Fatal(err)
			}
			newJob := func(key, digest string) *models.FixJob {
				return &models.FixJob{ID: uuid.NewString(), UserID: user, RepoID: repo, ScanID: scan, Type: "fix", Mode: models.FixModeDryRun, IdempotencyKey: key, RequestDigest: digest}
			}
			const count = 8
			results := make([]*models.FixJob, count)
			replayed := make([]bool, count)
			failures := make([]error, count)
			var group sync.WaitGroup
			for i := range count {
				group.Go(func() {
					results[i], replayed[i], failures[i] = writers[i%len(writers)].CreateFixJob(ctx, newJob("same-request", "hash-one"), true)
				})
			}
			group.Wait()
			created := 0
			for i, result := range results {
				if failures[i] != nil {
					t.Fatal(failures[i])
				}
				if result.ID != results[0].ID {
					t.Fatal("duplicate request created another job")
				}
				if !replayed[i] {
					created++
				}
			}
			if created != 1 {
				t.Fatalf("new jobs=%d", created)
			}
			first := results[0]
			if first.BaseBranch != "release" || len(first.FindingIDList) != 1 || first.FindingIDList[0] != finding.ID {
				t.Fatalf("lost scan scope: %+v", first)
			}
			countRows := func(table string) int {
				var n int
				if err := database.Get(&n, "SELECT COUNT(*) FROM "+table); err != nil {
					t.Fatal(err)
				}
				return n
			}
			if countRows("fix_jobs") != 1 || countRows("remediations") != 1 {
				t.Fatal("duplicate/orphan creation")
			}
			if _, _, err := store.CreateFixJob(ctx, newJob("same-request", "changed"), true); !errors.Is(err, ErrFixIdempotencyConflict) {
				t.Fatalf("changed intent accepted: %v", err)
			}
			if _, _, err := store.CreateFixJob(ctx, newJob("distinct", "hash-two"), true); !errors.Is(err, ErrFixBusy) {
				t.Fatalf("parallel agent accepted: %v", err)
			}
			if backend == "sqlite" {
				if err := writers[1].Close(); err != nil {
					t.Fatal(err)
				}
				if err := store.Close(); err != nil {
					t.Fatal(err)
				}
			}
			reopen()
			replay, again, err := store.CreateFixJob(ctx, newJob("same-request", "hash-one"), true)
			if err != nil || !again || replay.ID != first.ID {
				t.Fatalf("replay lost after reopen/migration: %+v %v", replay, err)
			}
			first.HumanInTheLoop = true
			first.Pushed = true
			first.Status = models.FixJobAwaitingPush
			if err := store.UpdateFixJob(ctx, first); err != nil {
				t.Fatal(err)
			}
			updated, err := store.GetFixJobByID(ctx, first.ID)
			if err != nil || !updated.HumanInTheLoop || !updated.Pushed {
				t.Fatalf("boolean job state lost: %+v %v", updated, err)
			}
			attempt := &models.FixAttempt{ID: uuid.NewString(), JobID: first.ID, FindingID: finding.ID, Built: true, FindingCleared: true, Outcome: models.FixOutcomeKept}
			if err := store.CreateFixAttempt(ctx, attempt); err != nil {
				t.Fatal(err)
			}
			attempts, err := store.ListFixAttempts(ctx, first.ID)
			if err != nil || len(attempts) != 1 || !attempts[0].Built || !attempts[0].FindingCleared {
				t.Fatalf("verification state lost: %+v %v", attempts, err)
			}
			// Fail after a new remediation was inserted; the failed job must leave no session.
			secondScan := uuid.NewString()
			if err := store.CreateScan(ctx, &models.Scan{ID: secondScan, RepoID: repo, UserID: user, Status: models.ScanStatusCompleted}); err != nil {
				t.Fatal(err)
			}
			collision := newJob("collision", "hash-collision")
			collision.ID = first.ID
			collision.ScanID = secondScan
			if _, _, err := store.CreateFixJob(ctx, collision, true); err == nil {
				t.Fatal("job collision accepted")
			}
			if countRows("fix_jobs") != 1 || countRows("remediations") != 1 {
				t.Fatal("failed enqueue retained orphan session")
			}
			childID := uuid.NewString()
			if err := store.CreateScan(ctx, &models.Scan{ID: childID, RepoID: repo, UserID: user, OriginScanID: scan, Status: models.ScanStatusCompleted}); err != nil {
				t.Fatal(err)
			}
			childFinding := finding
			childFinding.ID = uuid.NewString()
			childFinding.ScanID = childID
			if err := store.CreateFindings(ctx, []models.Finding{childFinding}); err != nil {
				t.Fatal(err)
			}
			selected := newJob("selected", "hash-selected")
			selected.FindingIDList = []string{finding.ID}
			selected, _, err = store.CreateFixJob(ctx, selected, false)
			if err != nil {
				t.Fatal(err)
			}
			if selected.ScanID != scan || len(selected.FindingIDList) != 1 || selected.FindingIDList[0] != finding.ID {
				t.Fatal("explicit finding selection expanded to child scan")
			}
			old, err := store.GetFixJobByID(ctx, first.ID)
			if err != nil || old.Status != models.FixJobSuperseded {
				t.Fatalf("review handoff not atomic: %+v %v", old, err)
			}
			selected.Status = models.FixJobSucceeded
			if err := store.UpdateFixJob(ctx, selected); err != nil {
				t.Fatal(err)
			}
			all, _, err := store.CreateFixJob(ctx, newJob("all", "hash-all"), true)
			if err != nil {
				t.Fatal(err)
			}
			if all.ScanID != childID || len(all.FindingIDList) != 1 || all.FindingIDList[0] != childFinding.ID {
				t.Fatalf("whole scan did not use latest completed child: %+v", all)
			}
			otherUser, otherRepo, otherScan := uuid.NewString(), uuid.NewString(), uuid.NewString()
			if err := store.CreateUser(ctx, &models.User{ID: otherUser, Email: otherUser + "@example.test", PasswordHash: "unused"}); err != nil {
				t.Fatal(err)
			}
			if err := store.CreateRepo(ctx, &models.Repo{ID: otherRepo, UserID: otherUser, Name: "other", SourcePath: t.TempDir()}); err != nil {
				t.Fatal(err)
			}
			if err := store.CreateScan(ctx, &models.Scan{ID: otherScan, RepoID: otherRepo, UserID: otherUser, Status: models.ScanStatusCompleted}); err != nil {
				t.Fatal(err)
			}
			other := newJob("same-request", "hash-one")
			other.UserID, other.RepoID, other.ScanID = otherUser, otherRepo, otherScan
			independent, wasReplay, err := store.CreateFixJob(ctx, other, true)
			if err != nil || wasReplay || independent.ID == first.ID {
				t.Fatalf("idempotency crossed owner boundary: %+v %v", independent, err)
			}

			foreign := newJob("foreign", "hash-foreign")
			foreign.ScanID = ""
			foreign.RepoID = uuid.NewString()
			foreign.FindingIDList = []string{finding.ID}
			if _, _, err := store.CreateFixJob(ctx, foreign, false); !errors.Is(err, ErrFixScope) {
				t.Fatalf("foreign scope accepted: %v", err)
			}
		})
	}
}

func TestFindingOnlyFixPinsBranchAndRejectsMixedBranches(t *testing.T) {
	store := newTestStore(t)
	ctx := t.Context()
	user, repo := uuid.NewString(), uuid.NewString()
	if err := store.CreateUser(ctx, &models.User{ID: user, Email: user + "@example.test", PasswordHash: "unused"}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateRepo(ctx, &models.Repo{ID: repo, UserID: user, Name: "branches", DefaultBranch: "main"}); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, branch := range []string{"release", "main"} {
		scan, finding := uuid.NewString(), uuid.NewString()
		if err := store.CreateScan(ctx, &models.Scan{ID: scan, RepoID: repo, UserID: user, Branch: branch, Status: models.ScanStatusCompleted}); err != nil {
			t.Fatal(err)
		}
		if err := store.CreateFinding(ctx, &models.Finding{ID: finding, ScanID: scan, RepoID: repo, ToolName: "fixture", RuleID: "rule"}); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, finding)
	}
	job := &models.FixJob{ID: uuid.NewString(), UserID: user, RepoID: repo, FindingIDList: ids[:1]}
	created, _, err := store.CreateFixJob(ctx, job, false)
	if err != nil || created.BaseBranch != "release" {
		t.Fatalf("finding source branch lost: %+v %v", created, err)
	}
	saved, err := store.GetFixJobByID(ctx, created.ID)
	if err != nil || saved.BaseBranch != "release" {
		t.Fatalf("branch not durable: %+v %v", saved, err)
	}
	job = &models.FixJob{ID: uuid.NewString(), UserID: user, RepoID: repo, FindingIDList: ids}
	if _, _, err := store.CreateFixJob(ctx, job, false); !errors.Is(err, ErrFixSourceBranchConflict) {
		t.Fatalf("mixed source branches accepted: %v", err)
	}
}
