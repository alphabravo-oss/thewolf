package routes

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/thewolf/internal/artifacts"
	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
	"github.com/google/uuid"
)

// This subprocess is intentionally killed by its parent at a publication boundary.
func TestScanCompletionProcessChild(t *testing.T) {
	if os.Getenv("WOLF_COMPLETION_PROCESS_CHILD") != "1" {
		t.Skip("parent-owned process fixture")
	}
	var store db.Store
	var err error
	if os.Getenv("WOLF_COMPLETION_BACKEND") == "postgres" {
		store, err = db.NewPostgres(os.Getenv("WOLF_COMPLETION_DSN"))
	} else {
		store, err = db.NewSQLite(os.Getenv("WOLF_COMPLETION_DSN"))
	}
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := artifacts.Init(os.Getenv("WOLF_COMPLETION_ARTIFACTS")); err != nil {
		t.Fatal(err)
	}
	scan, err := store.GetScanByID(t.Context(), os.Getenv("WOLF_COMPLETION_SCAN"))
	if err != nil {
		t.Fatal(err)
	}
	paused := completionPauseStore{Store: store, marker: os.Getenv("WOLF_COMPLETION_MARKER"), after: os.Getenv("WOLF_COMPLETION_AFTER") == "1"}
	if err := ExecuteQueuedScan(t.Context(), &Handler{Store: paused, Registry: completionRegistry(os.Getenv("WOLF_COMPLETION_COUNTER"))}, scan); err != nil {
		t.Fatal(err)
	}
	t.Fatal("executor returned before reaching the kill boundary")
}

type completionPauseStore struct {
	db.Store
	marker string
	after  bool
}

func (s completionPauseStore) CompleteScan(ctx context.Context, scan *models.Scan, completion db.ScanCompletion) (bool, error) {
	if s.after {
		if ok, err := s.Store.CompleteScan(ctx, scan, completion); err != nil || !ok {
			return ok, err
		}
	}
	if err := os.WriteFile(s.marker, []byte("ready"), 0600); err != nil {
		return false, err
	}
	<-ctx.Done()
	return false, ctx.Err()
}

type completionCountPlugin struct {
	clusterFindingPlugin
	counter string
}

func (p completionCountPlugin) Execute(ctx context.Context, opts models.ExecuteOpts) ([]models.Finding, error) {
	f, err := os.OpenFile(p.counter, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	_, err = f.WriteString(p.name + "\n")
	if err = errors.Join(err, f.Close()); err != nil {
		return nil, err
	}
	return p.clusterFindingPlugin.Execute(ctx, opts)
}
func completionRegistry(counter string) *plugin.Registry {
	r := plugin.NewRegistry()
	for _, name := range []string{"cluster-a", "cluster-b"} {
		r.Register(completionCountPlugin{clusterFindingPlugin: clusterFindingPlugin{profileTestPlugin: profileTestPlugin{name: name, category: models.CategorySCA}}, counter: counter})
	}
	return r
}

func TestScanCompletionSurvivesProcessKill(t *testing.T) {
	for _, backend := range []string{"sqlite", "postgres"} {
		for _, phase := range []string{"before", "after"} {
			t.Run(backend+"/"+phase, func(t *testing.T) {
				root := t.TempDir()
				var store db.Store
				var dsn string
				if backend == "postgres" {
					store, dsn = clusterPostgresStoreDSN(t)
				} else {
					dsn = filepath.Join(root, "state.db")
					s, err := db.NewSQLite(dsn)
					if err != nil {
						t.Fatal(err)
					}
					store = s
					t.Cleanup(func() { _ = store.Close() })
				}
				ctx := t.Context()
				repoPath := filepath.Join(root, "repo")
				if err := os.MkdirAll(filepath.Join(repoPath, "deps"), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(repoPath, "deps/package-lock.json"), []byte(`{}`), 0600); err != nil {
					t.Fatal(err)
				}
				user, repo := uuid.NewString(), uuid.NewString()
				if err := store.CreateUser(ctx, &models.User{ID: user, Email: user + "@example.test", PasswordHash: "hash"}); err != nil {
					t.Fatal(err)
				}
				if err := store.CreateRepo(ctx, &models.Repo{ID: repo, UserID: user, Name: "recovery", SourceType: models.SourceTypeLocal, SourcePath: repoPath, DefaultBranch: "main"}); err != nil {
					t.Fatal(err)
				}
				queued := &models.Scan{ID: uuid.NewString(), UserID: user, RepoID: repo, Branch: "main", Status: models.ScanStatusPending, SourceType: models.SourceTypeLocal, SourcePath: repoPath, PreparedWorkspace: repoPath, ExecutionBackend: "docker", RequestJSON: `{}`}
				if err := store.CreateScan(ctx, queued); err != nil {
					t.Fatal(err)
				}
				claim, err := store.ClaimNextScan(ctx, "first-worker", "docker", time.Now().Add(time.Minute))
				if err != nil || claim == nil {
					t.Fatalf("claim: %+v %v", claim, err)
				}
				marker, counter, artifactRoot := filepath.Join(root, "paused"), filepath.Join(root, "executed"), filepath.Join(root, "artifacts")
				exe, err := os.Executable()
				if err != nil {
					t.Fatal(err)
				}
				childCtx, cancel := context.WithTimeout(ctx, 40*time.Second)
				defer cancel()
				command := exec.CommandContext(childCtx, exe, "-test.run=^TestScanCompletionProcessChild$", "-test.timeout=35s")
				after := "0"
				if phase == "after" {
					after = "1"
				}
				command.Env = append(os.Environ(), "WOLF_COMPLETION_PROCESS_CHILD=1", "WOLF_COMPLETION_BACKEND="+backend, "WOLF_COMPLETION_DSN="+dsn, "WOLF_COMPLETION_ARTIFACTS="+artifactRoot, "WOLF_COMPLETION_SCAN="+claim.ID, "WOLF_COMPLETION_MARKER="+marker, "WOLF_COMPLETION_COUNTER="+counter, "WOLF_COMPLETION_AFTER="+after)
				output, err := os.Create(filepath.Join(root, "child.log"))
				if err != nil {
					t.Fatal(err)
				}
				defer output.Close()
				command.Stdout, command.Stderr = output, output
				if err := command.Start(); err != nil {
					t.Fatal(err)
				}
				done := make(chan error, 1)
				go func() { done <- command.Wait() }()
				ticker := time.NewTicker(20 * time.Millisecond)
				defer ticker.Stop()
				reached := false
				for !reached {
					select {
					case err := <-done:
						data, _ := os.ReadFile(output.Name())
						t.Fatalf("child exited before %s commit: %v\n%s", phase, err, data)
					case <-childCtx.Done():
						t.Fatal("child did not reach publication boundary")
					case <-ticker.C:
						_, err := os.Stat(marker)
						reached = err == nil
					}
				}
				before, err := store.ListFindingEvidenceByScan(ctx, claim.ID)
				if err != nil || len(before) != 2 {
					t.Fatalf("early evidence: %+v %v", before, err)
				}
				if err := command.Process.Kill(); err != nil {
					t.Fatal(err)
				}
				if err := <-done; err == nil {
					t.Fatal("process was not killed")
				}
				saved, err := store.GetScanByID(ctx, claim.ID)
				if err != nil {
					t.Fatal(err)
				}
				count, err := store.ReclaimStaleScans(ctx, time.Now().Add(time.Hour))
				if err != nil {
					t.Fatal(err)
				}
				previous := artifacts.Global
				if err := artifacts.Init(artifactRoot); err != nil {
					t.Fatal(err)
				}
				defer func() { artifacts.Global = previous }()
				if phase == "before" {
					if _, err := store.GetScanBaselineByName(ctx, repo, "main", "auto"); !errors.Is(err, sql.ErrNoRows) {
						t.Fatalf("precommit process published a baseline: %v", err)
					}
					if canonical, err := store.ListVulnerabilitiesByScan(ctx, claim.ID); err != nil || len(canonical) != 0 {
						t.Fatalf("precommit canonical publication: %+v %v", canonical, err)
					}
					if err := store.UpdateFindingStatus(ctx, before[0].ID, models.StatusFixed); err != nil {
						t.Fatal(err)
					}
					if saved.Status != models.ScanStatusRunning || count != 1 {
						t.Fatalf("precommit kill lost recoverability: %+v reclaimed=%d", saved, count)
					}
					next, err := store.ClaimNextScan(ctx, "second-worker", "docker", time.Now().Add(time.Minute))
					if err != nil || next == nil || next.ID != claim.ID || next.Attempt != 2 {
						t.Fatalf("reclaim: %+v %v", next, err)
					}
					if err := ExecuteQueuedScan(ctx, &Handler{Store: store, Registry: completionRegistry(counter)}, next); err != nil {
						t.Fatal(err)
					}
				} else {
					if saved.Status != models.ScanStatusCompleted || count != 0 {
						t.Fatalf("postcommit kill lost completion: %+v reclaimed=%d", saved, count)
					}
					if next, err := store.ClaimNextScan(ctx, "unneeded-worker", "docker", time.Now().Add(time.Minute)); err != nil || next != nil {
						t.Fatalf("completed scan was reclaimed: %+v %v", next, err)
					}
				}
				final, err := store.GetScanByID(ctx, claim.ID)
				if err != nil || final.Status != models.ScanStatusCompleted || final.FindingCount != 1 {
					t.Fatalf("recovered result: %+v %v", final, err)
				}
				afterRows, err := store.ListFindingEvidenceByScan(ctx, claim.ID)
				if err != nil || len(afterRows) != len(before) {
					t.Fatalf("recovery lost evidence: %+v %v", afterRows, err)
				}
				for i := range before {
					a, b := before[i], afterRows[i]
					if a.ID != b.ID || a.ClusterID != b.ClusterID || a.Confidence != b.Confidence || a.CompositeScore != b.CompositeScore || a.BaselineState != b.BaselineState || a.IntroducedInScanID != b.IntroducedInScanID || a.FirstSeenAt == nil || b.FirstSeenAt == nil || !a.FirstSeenAt.Equal(*b.FirstSeenAt) {
						t.Fatalf("recovery changed persisted identity/history: %+v vs %+v", a, b)
					}
					if phase == "before" && b.Status != models.StatusFixed {
						t.Fatalf("recovery discarded triage: %+v", b)
					}
				}
				calls, err := os.ReadFile(counter)
				if err != nil || len(strings.Fields(string(calls))) != 2 {
					t.Fatalf("successful scanners ran again: %q %v", calls, err)
				}
				gate, err := store.GetQualityGateResult(ctx, claim.ID, "default")
				if err != nil || gate.FindingsRevision != final.FindingsRevision || gate.ScanStatus != models.ScanStatusCompleted {
					t.Fatalf("recovered gate: %+v %v", gate, err)
				}
				baseline, err := store.GetScanBaselineByName(ctx, repo, "main", "auto")
				if err != nil || baseline.ScanID != claim.ID {
					t.Fatalf("recovered baseline: %+v %v", baseline, err)
				}
				files, err := store.ListScanArtifacts(ctx, claim.ID)
				if err != nil {
					t.Fatal(err)
				}
				finalBundle := map[string]bool{}
				for _, file := range files {
					if _, err := artifacts.ReadArtifact(file); err != nil {
						t.Fatal(err)
					}
					if file.FindingsRevision != nil && *file.FindingsRevision == final.FindingsRevision {
						finalBundle[filepath.Base(file.FilePath)] = true
					}
				}
				for _, name := range []string{"findings.json", "manifest.json", "combined.sarif", "gate-result.json"} {
					if !finalBundle[name] {
						t.Fatalf("missing final artifact %s", name)
					}
				}
			})
		}
	}
}
