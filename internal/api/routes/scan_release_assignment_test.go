package routes

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/models"
	scannercontainer "github.com/alphabravocompany/thewolf/internal/plugin/container"
	"github.com/alphabravocompany/thewolf/internal/scan/report"
	"github.com/alphabravocompany/thewolf/internal/scannerrelease"
)

func TestResolveScanReleaseFreezesDigestPinnedPerScanRuntime(t *testing.T) {
	store := withScannerSupplyChainStore(t)
	ctx := context.Background()
	previousRuntime := scannercontainer.Default()
	base := scannercontainer.DefaultConfig()
	base.Network = "none"
	base.Memory = "3g"
	scannercontainer.SetDefault(base)
	t.Cleanup(func() { scannercontainer.SetDefault(previousRuntime) })

	user := &models.User{ID: uuid.NewString(), Email: "release-scan@example.com", PasswordHash: "hash"}
	if err := store.CreateUser(ctx, user); err != nil {
		t.Fatal(err)
	}
	repo := &models.Repo{
		ID: uuid.NewString(), UserID: user.ID, Name: "repo", SourceType: models.SourceTypeLocal,
		SourcePath: t.TempDir(), DefaultBranch: "main",
	}
	if err := store.CreateRepo(ctx, repo); err != nil {
		t.Fatal(err)
	}
	persistence := store.ScannerReleases()
	policy := &scannerrelease.Policy{
		ID: "policy-runtime", Scope: "global", Revision: 1, Enabled: true,
		ScheduleJSON: "{}", RulesJSON: "{}", CreatedBy: "test",
	}
	if err := persistence.CreatePolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	candidate := &scannerrelease.Candidate{
		ID: "candidate-runtime", DefinitionCommit: "commit", LockDigest: digestForTest("a"),
		RiskSummaryJSON: "{}", State: scannerrelease.CandidatePublished, RequiredGatesJSON: "[]",
		PolicyID: policy.ID, PolicyRevision: 1, Actor: "test", IdempotencyKey: "candidate-runtime",
	}
	if err := persistence.CreateCandidate(ctx, candidate, scannerrelease.TransitionCommand{Actor: "test", IdempotencyKey: "candidate-runtime"}); err != nil {
		t.Fatal(err)
	}
	registry := &scannerrelease.RegistryTarget{
		ID: "registry-runtime", Name: "primary", Type: scannerrelease.RegistryManaged,
		Host: "registry.example", Namespace: "security", PlatformPolicyJSON: "{}",
		Enabled: true, Version: 1, CreatedBy: "test",
	}
	if err := persistence.CreateRegistryTarget(ctx, registry); err != nil {
		t.Fatal(err)
	}
	defaultDigest := digestForTest("b")
	trivyDigest := digestForTest("c")
	inventory := &scannerrelease.ReleaseInventory{
		Release: scannerrelease.Release{
			ID: "release-runtime", Name: "scanner-set-1", CandidateID: candidate.ID,
			LockDigest: candidate.LockDigest, ManifestDigest: digestForTest("d"),
			ManifestURI: "oci://registry.example/security/release@" + digestForTest("d"),
			State:       scannerrelease.ReleaseStable, SignerIdentity: "test-signer",
			PolicyID: policy.ID, PolicyRevision: 1, DefinitionCommit: "commit",
			Protected: true, RollbackEligible: true, RetentionClass: "published",
		},
		Tools: []scannerrelease.ReleaseTool{
			{ID: uuid.NewString(), ToolKey: "semgrep", Version: "1", MetadataJSON: `{"image_key":"default","kind":"wolf"}`},
			{ID: uuid.NewString(), ToolKey: "trivy", Version: "1", MetadataJSON: `{"image_key":"trivy","kind":"upstream","entrypoint":"trivy"}`},
		},
		Images: []scannerrelease.ReleaseImage{
			{
				ID: uuid.NewString(), ImageKey: "default", RegistryTargetID: registry.ID,
				Repository: "wolf-scanners", Digest: defaultDigest,
				PlatformDigests: `{"linux/amd64":"` + digestForTest("e") + `"}`,
				SignatureStatus: "verified", ProvenanceDigest: digestForTest("f"),
				SBOMDigest: digestForTest("0"),
			},
			{
				ID: uuid.NewString(), ImageKey: "trivy", RegistryTargetID: registry.ID,
				Repository: "trivy", Digest: trivyDigest,
				PlatformDigests: `{"linux/amd64":"` + digestForTest("1") + `"}`,
				SignatureStatus: "verified", ProvenanceDigest: digestForTest("2"),
				SBOMDigest: digestForTest("3"),
			},
		},
	}
	if err := persistence.CreateRelease(ctx, inventory, scannerrelease.TransitionCommand{Actor: "test", IdempotencyKey: "release-runtime"}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetSetting(ctx, "desired_scanner_release_id", inventory.Release.ID); err != nil {
		t.Fatal(err)
	}
	scan := &models.Scan{
		ID: uuid.NewString(), UserID: user.ID, RepoID: repo.ID, Branch: "main",
		Status: models.ScanStatusPending, ToolsSelected: "[]", ToolsCompleted: "[]",
		ToolsFailed: "[]", ToolsErrors: "{}", Categories: "[]", IncludePaths: "[]",
		ExcludePaths: "[]", MaxAttempts: 2,
	}
	if err := store.CreateScan(ctx, scan); err != nil {
		t.Fatal(err)
	}
	snapshot, runtimeConfig, err := resolveScanRelease(ctx, DefaultHandler, scan)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.ReleaseID != inventory.Release.ID ||
		runtimeConfig.Image != "registry.example/security/wolf-scanners@"+defaultDigest ||
		runtimeConfig.ImageFor("trivy") != "registry.example/security/trivy@"+trivyDigest ||
		runtimeConfig.Network != "none" || runtimeConfig.Memory != "3g" {
		t.Fatalf("snapshot=%#v runtime=%#v", snapshot, runtimeConfig)
	}
	persisted, err := store.GetScanByID(ctx, scan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.ScannerReleaseID != inventory.Release.ID ||
		persisted.ReleaseManifestDigest != inventory.Release.ManifestDigest {
		t.Fatalf("persisted assignment = %#v", persisted)
	}
	plan := &report.ScannerPlan{Run: []report.ScannerPlanDecision{{Tool: "trivy"}}}
	applyReleaseToScannerPlan(plan, snapshot)
	record := scannerRunRecordQueued(scan.ID, "trivy", plan)
	if record.ScannerReleaseID != inventory.Release.ID ||
		record.ReleaseManifestDigest != inventory.Release.ManifestDigest ||
		record.ImageDigest != trivyDigest {
		t.Fatalf("run record provenance = %#v", record)
	}

	// A rollout can move desired state while the scan is active. Every
	// concurrent resolver must continue reading the assignment frozen above.
	persisted.Status = models.ScanStatusRunning
	if err := store.UpdateScan(ctx, persisted); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errorsSeen := make(chan error, 32)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 16; i++ {
			if err := store.SetSetting(ctx, "desired_scanner_release_id", "rollout-moved-release"); err != nil {
				errorsSeen <- err
			}
		}
	}()
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			current, err := store.GetScanByID(ctx, persisted.ID)
			if err != nil {
				errorsSeen <- err
				return
			}
			resolved, _, err := resolveScanRelease(ctx, DefaultHandler, current)
			if err != nil {
				errorsSeen <- err
				return
			}
			if resolved.ReleaseID != inventory.Release.ID ||
				resolved.ManifestDigest != inventory.Release.ManifestDigest {
				errorsSeen <- fmt.Errorf("active scan assignment changed: %#v", resolved)
			}
		}()
	}
	wg.Wait()
	close(errorsSeen)
	for err := range errorsSeen {
		t.Error(err)
	}
	afterRollout, err := store.GetScanByID(ctx, persisted.ID)
	if err != nil {
		t.Fatal(err)
	}
	if afterRollout.ScannerReleaseID != inventory.Release.ID ||
		afterRollout.ReleaseManifestDigest != inventory.Release.ManifestDigest {
		t.Fatalf("rollout changed active assignment: %#v", afterRollout)
	}
	afterRollout.ScannerReleaseID = "attempted-reassignment"
	afterRollout.ReleaseManifestDigest = digestForTest("9")
	if err := store.UpdateScan(ctx, afterRollout); err != nil {
		t.Fatal(err)
	}
	immutable, err := store.GetScanByID(ctx, persisted.ID)
	if err != nil {
		t.Fatal(err)
	}
	if immutable.ScannerReleaseID != inventory.Release.ID ||
		immutable.ReleaseManifestDigest != inventory.Release.ManifestDigest {
		t.Fatalf("persisted release assignment was mutable: %#v", immutable)
	}
}

func digestForTest(character string) string {
	return "sha256:" + strings.Repeat(character, 64)
}
