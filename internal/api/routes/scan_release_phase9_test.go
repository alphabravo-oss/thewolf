package routes

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/auth"
	"github.com/alphabravocompany/thewolf/internal/models"
	scannercontainer "github.com/alphabravocompany/thewolf/internal/plugin/container"
	"github.com/alphabravocompany/thewolf/internal/scannerrelease"
)

func TestCreateReleaseRescanIsDistinctPinnedAndIdempotent(t *testing.T) {
	t.Setenv("WOLF_SCAN_EXECUTION_MODE", "queue")
	store := withScannerSupplyChainStore(t)
	ctx := context.Background()
	user := &models.User{
		ID: uuid.NewString(), Email: "rescan@example.test", PasswordHash: "hash",
	}
	if err := store.CreateUser(ctx, user); err != nil {
		t.Fatal(err)
	}
	repo := &models.Repo{
		ID: uuid.NewString(), UserID: user.ID, Name: "rescan-repo",
		SourceType: models.SourceTypeLocal, SourcePath: t.TempDir(), DefaultBranch: "main",
	}
	if err := store.CreateRepo(ctx, repo); err != nil {
		t.Fatal(err)
	}
	persistence := store.ScannerReleases()
	policy := &scannerrelease.Policy{
		ID: uuid.NewString(), Scope: "rescan", Revision: 1, Enabled: true,
		ScheduleJSON: "{}", RulesJSON: "{}", CreatedBy: user.Email,
	}
	if err := persistence.CreatePolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	candidate := &scannerrelease.Candidate{
		ID: uuid.NewString(), DefinitionCommit: "rescan-definition",
		LockDigest: digestForTest("a"), RiskSummaryJSON: "{}",
		State: scannerrelease.CandidatePublished, RequiredGatesJSON: "[]",
		PolicyID: policy.ID, PolicyRevision: 1, Actor: user.Email,
		IdempotencyKey: uuid.NewString(),
	}
	if err := persistence.CreateCandidate(ctx, candidate, scannerrelease.TransitionCommand{
		Actor: user.Email, IdempotencyKey: candidate.IdempotencyKey,
	}); err != nil {
		t.Fatal(err)
	}
	registry := &scannerrelease.RegistryTarget{
		ID: uuid.NewString(), Name: "rescan registry", Type: scannerrelease.RegistryManaged,
		Host: "registry.example", Namespace: "security", PlatformPolicyJSON: "{}",
		Enabled: true, Version: 1, CreatedBy: user.Email,
	}
	if err := persistence.CreateRegistryTarget(ctx, registry); err != nil {
		t.Fatal(err)
	}
	release := scannerrelease.Release{
		ID: uuid.NewString(), Name: "legacy-test-release-name",
		CandidateID: candidate.ID, LockDigest: candidate.LockDigest,
		ManifestDigest: digestForTest("b"), ManifestURI: "oci://release",
		State: scannerrelease.ReleaseStable, SignerIdentity: "signer",
		PolicyID: policy.ID, PolicyRevision: 1, DefinitionCommit: candidate.DefinitionCommit,
		Protected: true, RollbackEligible: true,
	}
	if err := persistence.CreateRelease(ctx, &scannerrelease.ReleaseInventory{
		Release: release,
		Images: []scannerrelease.ReleaseImage{{
			ID: uuid.NewString(), ImageKey: "default", RegistryTargetID: registry.ID,
			Repository: "wolf-scanners", Digest: digestForTest("e"),
			PlatformDigests: `{"linux/amd64":"` + digestForTest("f") + `"}`,
			SignatureStatus: "verified", ProvenanceDigest: digestForTest("1"),
			SBOMDigest: digestForTest("2"),
		}},
	},
		scannerrelease.TransitionCommand{Actor: user.Email, IdempotencyKey: uuid.NewString()}); err != nil {
		t.Fatal(err)
	}
	source := &models.Scan{
		ID: uuid.NewString(), UserID: user.ID, RepoID: repo.ID, Branch: "main",
		Status: models.ScanStatusCompleted, RequestJSON: `{"repo_id":"` + repo.ID + `"}`,
		RequestDigest: digestForTest("c"), ScannerReleaseID: "old-release",
		ReleaseManifestDigest: digestForTest("d"), ToolsSelected: `["semgrep"]`,
	}
	if err := store.CreateScan(ctx, source); err != nil {
		t.Fatal(err)
	}

	body := `{"release_id":"` + release.ID + `","reason":"compare new scanner rules"}`
	first := authenticatedRouteRequest(t, CreateReleaseRescan, http.MethodPost,
		"/scans/"+source.ID+"/release-rescans", body, user, source.ID, "rescan-key")
	if first.Code != http.StatusCreated {
		t.Fatalf("create re-scan = %d, body=%s", first.Code, first.Body)
	}
	var envelope struct {
		Data models.Scan `json:"data"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	rescan := envelope.Data
	if rescan.ID == "" || rescan.ID == source.ID || rescan.RescanOfScanID != source.ID ||
		rescan.ScannerReleaseID != release.ID ||
		rescan.ReleaseManifestDigest != release.ManifestDigest ||
		rescan.ReleaseSelectionReason != "compare new scanner rules" {
		t.Fatalf("unexpected re-scan: %#v", rescan)
	}
	replayed := authenticatedRouteRequest(t, CreateReleaseRescan, http.MethodPost,
		"/scans/"+source.ID+"/release-rescans", body, user, source.ID, "rescan-key")
	var replayEnvelope struct {
		Data models.Scan `json:"data"`
	}
	_ = json.Unmarshal(replayed.Body.Bytes(), &replayEnvelope)
	if replayed.Code != http.StatusCreated || replayEnvelope.Data.ID != rescan.ID {
		t.Fatalf("idempotent replay = %d %#v", replayed.Code, replayEnvelope.Data)
	}
	persistedSource, err := store.GetScanByID(ctx, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	if persistedSource.ScannerReleaseID != "old-release" ||
		persistedSource.ReleaseManifestDigest != digestForTest("d") {
		t.Fatalf("source assignment changed: %#v", persistedSource)
	}
}

func TestLegacyConfigImportIsImmutableObserveOnlyAndIdempotent(t *testing.T) {
	store := withScannerSupplyChainStore(t)
	previous := scannercontainer.Default()
	scannercontainer.SetDefault(&scannercontainer.Config{
		Image: "docker.io/wolf/scanners:legacy",
		ImageOverrides: map[string]string{
			"codeql": "ghcr.io/wolf/codeql@sha256:" + strings.Repeat("b", 64),
		},
		UpstreamTools: map[string]scannercontainer.ToolImageSpec{
			"trivy": {Image: "docker.io/aquasec/trivy:latest", Entrypoint: "trivy"},
		},
	})
	t.Cleanup(func() { scannercontainer.SetDefault(previous) })
	if err := store.SetSetting(context.Background(), "desired_scanner_release_id", "existing-release"); err != nil {
		t.Fatal(err)
	}
	body := `{"reason":"record pre-control-plane state","resolved_digests":{` +
		`"default":"sha256:` + strings.Repeat("a", 64) + `",` +
		`"upstream-trivy":"sha256:` + strings.Repeat("c", 64) + `"}}`
	headers := map[string]string{"Idempotency-Key": "legacy-import-1"}
	first := scannerRouteRequest(t, ScannerSupplyChainImportLegacyConfig, http.MethodPost,
		"/legacy-release-imports", body, headers, nil)
	if first.Code != http.StatusCreated {
		t.Fatalf("legacy import = %d, body=%s", first.Code, first.Body)
	}
	var envelope struct {
		Data struct {
			Release                   scannerrelease.Release `json:"release"`
			Created                   bool                   `json:"created"`
			RuntimeAssignmentsChanged bool                   `json:"runtime_assignments_changed"`
		} `json:"data"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if !envelope.Data.Created || !envelope.Data.Release.Legacy ||
		!envelope.Data.Release.Imported || envelope.Data.Release.RollbackEligible ||
		envelope.Data.RuntimeAssignmentsChanged {
		t.Fatalf("legacy snapshot flags = %#v", envelope.Data)
	}
	desired, err := store.GetSetting(context.Background(), "desired_scanner_release_id")
	if err != nil || desired != "existing-release" {
		t.Fatalf("legacy import changed desired release: %q err=%v", desired, err)
	}
	replay := scannerRouteRequest(t, ScannerSupplyChainImportLegacyConfig, http.MethodPost,
		"/legacy-release-imports", body, headers, nil)
	if replay.Code != http.StatusOK {
		t.Fatalf("legacy replay = %d, body=%s", replay.Code, replay.Body)
	}
}

func authenticatedRouteRequest(
	t *testing.T,
	handler http.HandlerFunc,
	method, path, body string,
	user *models.User,
	scanID, idempotencyKey string,
) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", idempotencyKey)
	routeContext := chi.NewRouteContext()
	routeContext.URLParams.Add("id", scanID)
	ctx := context.WithValue(request.Context(), chi.RouteCtxKey, routeContext)
	ctx = context.WithValue(ctx, auth.UserContextKey, &auth.Claims{
		UserID: user.ID, Email: user.Email, Role: models.RoleAdmin,
	})
	request = request.WithContext(ctx)
	recorder := httptest.NewRecorder()
	handler(recorder, request)
	return recorder
}
