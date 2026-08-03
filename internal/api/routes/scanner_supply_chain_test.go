package routes

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/artifacts"
	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/scannercontrol"
	"github.com/alphabravocompany/thewolf/internal/scannerpolicy"
	"github.com/alphabravocompany/thewolf/internal/scannerrelease"
)

func withScannerSupplyChainStore(t *testing.T) *db.SQLiteStore {
	t.Helper()
	store, err := db.NewSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	previous := DefaultHandler
	SetHandler(store, nil)
	t.Cleanup(func() {
		DefaultHandler = previous
		_ = store.Close()
	})
	return store
}

func scannerRouteRequest(
	t *testing.T,
	handler http.HandlerFunc,
	method, path, body string,
	headers map[string]string,
	params map[string]string,
) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	if len(params) != 0 {
		routeContext := chi.NewRouteContext()
		for key, value := range params {
			routeContext.URLParams.Add(key, value)
		}
		request = request.WithContext(context.WithValue(request.Context(), chi.RouteCtxKey, routeContext))
	}
	recorder := httptest.NewRecorder()
	handler(recorder, request)
	return recorder
}

func decodeScannerResponse(t *testing.T, recorder *httptest.ResponseRecorder, target any) {
	t.Helper()
	if err := json.Unmarshal(recorder.Body.Bytes(), target); err != nil {
		t.Fatalf("decode response %q: %v", recorder.Body.String(), err)
	}
}

func TestScannerCandidateRequestAcceptsAndAuditsOperatorReason(t *testing.T) {
	store := withScannerSupplyChainStore(t)
	repository := store.ScannerReleases()
	rules := scannerpolicy.Default()
	rulesJSON, err := json.Marshal(rules)
	if err != nil {
		t.Fatal(err)
	}
	policy := &scannerrelease.Policy{
		ID: uuid.NewString(), Scope: "global", Revision: 1, Enabled: true,
		ScheduleJSON: "{}", RulesJSON: string(rulesJSON), CreatedBy: "test",
	}
	if err := repository.CreatePolicy(context.Background(), policy); err != nil {
		t.Fatal(err)
	}
	discovery := &scannerrelease.DiscoveryRun{
		ID: uuid.NewString(), Trigger: scannerrelease.DiscoveryOnDemand,
		DefinitionCommit: "0123456789abcdef", PolicyID: policy.ID,
		PolicyRevision: policy.Revision, ScopeJSON: `{"mode":"complete"}`,
		State: scannerrelease.DiscoveryCompleted, Actor: "api",
		IdempotencyKey: "candidate-reason-discovery",
	}
	if err := repository.CreateDiscoveryRun(context.Background(), discovery, scannerrelease.TransitionCommand{
		Actor: "api", Reason: "route fixture", IdempotencyKey: discovery.IdempotencyKey, PayloadJSON: "{}",
	}); err != nil {
		t.Fatal(err)
	}
	if err := repository.AddUpdateItems(context.Background(), discovery.ID, []scannerrelease.UpdateItem{{
		ComponentType: scannerrelease.ComponentTool, ComponentName: "semgrep",
		CurrentValue: "1.0.0", AvailableValue: "1.0.1", Status: "update_available",
		RiskClass: scannerrelease.RiskLow, SelectionState: "unselected",
		SourceEvidenceJSON: `{}`, CompatibilityJSON: `{}`,
	}}); err != nil {
		t.Fatal(err)
	}
	items, err := repository.ListUpdateItems(context.Background(), discovery.ID)
	if err != nil || len(items) != 1 {
		t.Fatalf("items = %#v err=%v", items, err)
	}
	withoutReason := scannerRouteRequest(
		t, ScannerSupplyChainCreateCandidate, http.MethodPost, "/candidates",
		fmt.Sprintf(`{"discovery_run_id":%q,"definition_commit":"0123456789abcdef","selected_items":[%q]}`, discovery.ID, items[0].ID),
		map[string]string{"Idempotency-Key": "candidate-without-reason"}, nil,
	)
	if withoutReason.Code != http.StatusUnprocessableEntity || !strings.Contains(withoutReason.Body.String(), "reason_required") {
		t.Fatalf("candidate without reason = %d body=%s", withoutReason.Code, withoutReason.Body)
	}
	const reason = "Apply reviewed weekly scanner updates"
	created := scannerRouteRequest(
		t, ScannerSupplyChainCreateCandidate, http.MethodPost, "/candidates",
		fmt.Sprintf(`{"discovery_run_id":%q,"definition_commit":"0123456789abcdef","selected_items":[%q],"reason":%q}`, discovery.ID, items[0].ID, reason),
		map[string]string{"Idempotency-Key": "candidate-with-reason"}, nil,
	)
	if created.Code != http.StatusAccepted {
		t.Fatalf("candidate with reason = %d body=%s", created.Code, created.Body)
	}
	var response scannerCommandResponse
	decodeScannerResponse(t, created, &response)
	events, err := repository.ListEvents(context.Background(), "candidate", response.ID, 0, 10)
	if err != nil || len(events) == 0 || events[0].Reason != reason {
		t.Fatalf("candidate events = %#v err=%v", events, err)
	}
}

func TestScannerCandidateExceptionPersistsCompleteExpiringApprovalContext(t *testing.T) {
	store := withScannerSupplyChainStore(t)
	repository := store.ScannerReleases()
	rules := scannerpolicy.Default()
	rulesJSON, err := json.Marshal(rules)
	if err != nil {
		t.Fatal(err)
	}
	policy := &scannerrelease.Policy{
		ID: uuid.NewString(), Scope: "global", Revision: 1, Enabled: true,
		ScheduleJSON: "{}", RulesJSON: string(rulesJSON), CreatedBy: "test",
	}
	if err := repository.CreatePolicy(context.Background(), policy); err != nil {
		t.Fatal(err)
	}
	candidate := &scannerrelease.Candidate{
		ID: uuid.NewString(), DefinitionCommit: "0123456789abcdef",
		LockDigest: "sha256:" + strings.Repeat("a", 64), State: scannerrelease.CandidateBlocked,
		PolicyID: policy.ID, PolicyRevision: policy.Revision, Actor: "candidate-creator",
		IdempotencyKey: "candidate-exception-route",
	}
	if err := repository.CreateCandidate(context.Background(), candidate, scannerrelease.TransitionCommand{
		Actor: "candidate-creator", Reason: "exception route fixture",
		PolicyRevision: 1, IdempotencyKey: candidate.IdempotencyKey, PayloadJSON: "{}",
	}); err != nil {
		t.Fatal(err)
	}
	expires := time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339)
	body := fmt.Sprintf(`{
		"gate":"vulnerability",
		"owner_id":"security-owner",
		"reason":"temporary upstream advisory",
		"compensating_control":"quarantine candidate registry",
		"evidence_digest":"sha256:%s",
		"expires_at":%q
	}`, strings.Repeat("b", 64), expires)
	recorder := scannerRouteRequest(
		t, ScannerSupplyChainCreateCandidateException, http.MethodPost,
		"/candidates/"+candidate.ID+"/exceptions", body,
		map[string]string{"Idempotency-Key": "candidate-exception-route-1"},
		map[string]string{"id": candidate.ID},
	)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create exception = %d: %s", recorder.Code, recorder.Body.String())
	}
	approvals, err := repository.ListApprovals(context.Background(), candidate.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(approvals) != 1 || approvals[0].Action != "exception" ||
		approvals[0].ExceptionScope != "vulnerability" ||
		approvals[0].ExceptionOwner != "security-owner" ||
		approvals[0].CompensatingControl != "quarantine candidate registry" ||
		approvals[0].ExpiresAt == nil {
		t.Fatalf("persisted exception = %#v", approvals)
	}
	if err := scannercontrol.EnqueueCandidateBuildPlan(
		context.Background(), repository, candidate, nil,
	); err != nil {
		t.Fatal(err)
	}
	retry := scannerRouteRequest(
		t, ScannerSupplyChainRetryCandidate, http.MethodPost,
		"/candidates/"+candidate.ID+"/retry", `{}`,
		map[string]string{
			"Idempotency-Key": "candidate-exception-retry-1",
			"If-Match":        `"1"`,
		},
		map[string]string{"id": candidate.ID},
	)
	if retry.Code != http.StatusAccepted {
		t.Fatalf("retry exception candidate = %d: %s", retry.Code, retry.Body.String())
	}
	builds, err := repository.ListBuildRuns(context.Background(), candidate.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(builds) != 2 || builds[0].Attempt != 2 || builds[0].State != scannerrelease.BuildQueued {
		t.Fatalf("retry build attempts = %#v", builds)
	}
}

func TestScannerPolicyRequiresRevisionAndCreatesImmutableRevision(t *testing.T) {
	withScannerSupplyChainStore(t)

	get := scannerRouteRequest(t, ScannerSupplyChainGetPolicy, http.MethodGet, "/policy", "", nil, nil)
	if get.Code != http.StatusOK || get.Header().Get("ETag") != `"1"` {
		t.Fatalf("GET policy = %d, etag %q, body %s", get.Code, get.Header().Get("ETag"), get.Body)
	}

	body := `{
		"schedule":{"timezone":"UTC","daily_discovery":{"frequency":"daily","at":"02:00"}},
		"rules":{
			"schema_version":"wolf.scanner-policy/v1","revision":2,
			"approval_mode":"manual","required_approvals":1,"separate_creator":true,
			"auto_promote_risks":["low"],"auto_promote_changes":["rebuild_only","patch"],
			"required_gates":["lock","artifacts","platforms","smoke","parser","vulnerability","license","sbom","signature","provenance","source","secret_scan","compose","kubernetes"],
			"allow_exceptions":{"vulnerability":true,"license":true},"exception_max_age":"720h0m0s"
		}
	}`
	missing := scannerRouteRequest(t, ScannerSupplyChainPutPolicy, http.MethodPut, "/policy", body, nil, nil)
	if missing.Code != http.StatusPreconditionRequired {
		t.Fatalf("missing If-Match = %d, body %s", missing.Code, missing.Body)
	}
	updated := scannerRouteRequest(t, ScannerSupplyChainPutPolicy, http.MethodPut, "/policy", body, map[string]string{"If-Match": `"1"`}, nil)
	if updated.Code != http.StatusOK || updated.Header().Get("ETag") != `"2"` {
		t.Fatalf("PUT policy = %d, etag %q, body %s", updated.Code, updated.Header().Get("ETag"), updated.Body)
	}
	stale := scannerRouteRequest(t, ScannerSupplyChainPutPolicy, http.MethodPut, "/policy", body, map[string]string{"If-Match": `"1"`}, nil)
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale policy update = %d, body %s", stale.Code, stale.Body)
	}
}

func TestScannerCandidateSelectionViewAllowListsFreshnessEvidence(t *testing.T) {
	for _, reason := range []string{
		"no_stable_release",
		"maximum_stable_image_age_exceeded",
		"policy_forced_weekly_rebuild",
		"stable_release_within_maximum_age",
	} {
		view := scannerCandidateSelectionView(fmt.Sprintf(
			`{"force_rebuild":true,"rebuild_reason":%q,"no_op_if_unchanged":false,"credential":"must-not-leak"}`,
			reason,
		))
		if view["rebuild_reason"] != reason || view["force_rebuild"] != true {
			t.Fatalf("selection view for %q = %#v", reason, view)
		}
		if _, leaked := view["credential"]; leaked {
			t.Fatalf("selection view leaked an unrecognized field: %#v", view)
		}
	}
	unknown := scannerCandidateSelectionView(`{"rebuild_reason":"attacker-controlled"}`)
	if _, exists := unknown["rebuild_reason"]; exists {
		t.Fatalf("unknown rebuild reason was reflected: %#v", unknown)
	}
}

func TestScannerCandidateVerificationAggregatesActualPipelineStepKeys(t *testing.T) {
	for _, key := range []string{"signature/default", "signature/fixer-codex", "release-manifest-signature"} {
		if !scannerCandidateSignatureStep(key) {
			t.Errorf("real signature step %q was not recognized", key)
		}
	}
	for _, key := range []string{"provenance/default", "provenance/fixer-codex"} {
		if !scannerCandidateProvenanceStep(key) {
			t.Errorf("real provenance step %q was not recognized", key)
		}
	}
	if scannerCandidateSignatureStep("candidate-publish/default") ||
		scannerCandidateProvenanceStep("aggregate-sbom") {
		t.Fatal("unrelated evidence step was classified as signature/provenance")
	}

	completed := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	selected := make(map[string]candidateVerificationStep)
	for index, key := range []string{
		"signature/default",
		"signature/fixer-codex",
		"release-manifest-signature",
	} {
		selectCandidateVerificationStep(selected, 2, scannerrelease.BuildStep{
			StepKey: key, State: scannerrelease.BuildCompleted, Attempt: index + 1,
			OutputDigest: fmt.Sprintf("sha256:%064x", index+1), CompletedAt: &completed,
		})
	}
	// An older failed attempt for the same key must not poison the current
	// complete aggregate.
	selectCandidateVerificationStep(selected, 1, scannerrelease.BuildStep{
		StepKey: "signature/default", State: scannerrelease.BuildFailed, Attempt: 99,
	})
	aggregate := scannerCandidateVerificationAggregate("signature", selected)
	if aggregate["state"] != "verified" || aggregate["verified_count"] != 3 ||
		aggregate["total_count"] != 3 || aggregate["failed_count"] != 0 {
		t.Fatalf("signature aggregate = %#v", aggregate)
	}
	if digests, ok := aggregate["digests"].([]string); !ok || len(digests) != 3 {
		t.Fatalf("signature aggregate digests = %#v", aggregate["digests"])
	}
}

func TestScannerPolicyValidationReturnsTrustedPreviewAndRejectsOverlap(t *testing.T) {
	t.Parallel()

	valid := `{
		"schedule":{
			"timezone":"America/New_York",
			"daily_discovery":{"enabled":false,"frequency":"daily","at":"02:00","jitter":"20m","catch_up":"6h"},
			"weekly_candidate":{"enabled":true,"frequency":"weekly","weekday":"Sunday","at":"03:00","jitter":"20m","catch_up":"48h"},
			"maintenance_windows":[
				{"id":"first","name":"First","cron":"0 3 * * 1","duration":"1h"},
				{"id":"second","name":"Second","cron":"0 5 * * 1","duration":"1h"}
			]
		},
		"rules":{
			"schema_version":"wolf.scanner-policy/v1","revision":1,
			"approval_mode":"manual","required_approvals":1,"separate_creator":true,
			"required_gates":["lock","artifacts","platforms","smoke","parser","vulnerability","license","sbom","signature","provenance","source","secret_scan","compose","kubernetes"]
		}
	}`
	recorder := scannerRouteRequest(
		t, ScannerSupplyChainValidatePolicy, http.MethodPost, "/policy/validate",
		valid, nil, nil,
	)
	if recorder.Code != http.StatusOK {
		t.Fatalf("validate policy = %d, body %s", recorder.Code, recorder.Body)
	}
	var responseBody struct {
		Data struct {
			Valid         bool `json:"valid"`
			NextExecution struct {
				Daily       string `json:"daily_discovery"`
				Weekly      string `json:"weekly_candidate"`
				Maintenance []struct {
					ID string `json:"id"`
					At string `json:"at"`
				} `json:"maintenance_windows"`
			} `json:"next_execution"`
		} `json:"data"`
	}
	decodeScannerResponse(t, recorder, &responseBody)
	if !responseBody.Data.Valid || responseBody.Data.NextExecution.Daily != "" ||
		responseBody.Data.NextExecution.Weekly == "" ||
		len(responseBody.Data.NextExecution.Maintenance) != 2 {
		t.Fatalf("validation response = %#v", responseBody.Data)
	}

	overlapping := strings.Replace(valid, `"cron":"0 5 * * 1"`, `"cron":"30 3 * * 1"`, 1)
	recorder = scannerRouteRequest(
		t, ScannerSupplyChainValidatePolicy, http.MethodPost, "/policy/validate",
		overlapping, nil, nil,
	)
	var invalid struct {
		Data struct {
			Valid  bool     `json:"valid"`
			Errors []string `json:"errors"`
		} `json:"data"`
	}
	decodeScannerResponse(t, recorder, &invalid)
	if invalid.Data.Valid || len(invalid.Data.Errors) == 0 ||
		!strings.Contains(invalid.Data.Errors[0], "overlap") {
		t.Fatalf("overlap validation = %#v", invalid.Data)
	}
}

func TestScannerDiscoveryIdempotencyCancellationAndSSEReplay(t *testing.T) {
	withScannerSupplyChainStore(t)
	body := `{"scope":{"type":"all"},"reason":"operator freshness check","definition_commit":"abc123"}`

	missing := scannerRouteRequest(t, ScannerSupplyChainCreateDiscovery, http.MethodPost, "/discovery-runs", body, nil, nil)
	if missing.Code != http.StatusPreconditionRequired {
		t.Fatalf("missing idempotency = %d, body %s", missing.Code, missing.Body)
	}
	headers := map[string]string{"Idempotency-Key": "discovery-test-1"}
	created := scannerRouteRequest(t, ScannerSupplyChainCreateDiscovery, http.MethodPost, "/discovery-runs", body, headers, nil)
	if created.Code != http.StatusAccepted || created.Header().Get("Retry-After") == "" {
		t.Fatalf("create discovery = %d, body %s", created.Code, created.Body)
	}
	var command scannerCommandResponse
	decodeScannerResponse(t, created, &command)
	if command.ID == "" || command.State != "queued" {
		t.Fatalf("command = %+v", command)
	}
	replayed := scannerRouteRequest(t, ScannerSupplyChainCreateDiscovery, http.MethodPost, "/discovery-runs", body, headers, nil)
	var replay scannerCommandResponse
	decodeScannerResponse(t, replayed, &replay)
	if replayed.Code != http.StatusAccepted || replay.ID != command.ID {
		t.Fatalf("replay = %d %+v, want id %s", replayed.Code, replay, command.ID)
	}

	params := map[string]string{"id": command.ID}
	detail := scannerRouteRequest(t, ScannerSupplyChainGetDiscovery, http.MethodGet, "/discovery-runs/"+command.ID, "", nil, params)
	if detail.Code != http.StatusOK || detail.Header().Get("ETag") != `"1"` {
		t.Fatalf("detail = %d, etag %q, body %s", detail.Code, detail.Header().Get("ETag"), detail.Body)
	}
	cancelHeaders := map[string]string{"Idempotency-Key": "cancel-test-1", "If-Match": `"1"`}
	cancelled := scannerRouteRequest(t, ScannerSupplyChainCancelDiscovery, http.MethodPost, "/cancel", "", cancelHeaders, params)
	if cancelled.Code != http.StatusAccepted {
		t.Fatalf("cancel = %d, body %s", cancelled.Code, cancelled.Body)
	}

	stream := scannerRouteRequest(t, ScannerSupplyChainDiscoveryEvents, http.MethodGet, "/events", "", map[string]string{
		"Accept": "text/event-stream", "Last-Event-ID": "1",
	}, params)
	if stream.Code != http.StatusOK ||
		stream.Header().Get("Content-Type") != "text/event-stream" ||
		!bytes.Contains(stream.Body.Bytes(), []byte("discovery.cancelled")) ||
		bytes.Contains(stream.Body.Bytes(), []byte("discovery.created")) {
		t.Fatalf("SSE replay headers=%v body=%s", stream.Header(), stream.Body)
	}
}

func TestScannerNotificationsListShowAndRetryDeadLetter(t *testing.T) {
	store := withScannerSupplyChainStore(t)
	repository := store.ScannerReleases()
	ctx := context.Background()
	policy := &scannerrelease.Policy{
		ID: uuid.NewString(), Scope: "notification-route", Revision: 1, Enabled: true,
		ScheduleJSON: `{"timezone":"UTC"}`,
		RulesJSON:    `{"notifications":{"destinations":["webhook:security"]}}`,
		CreatedBy:    "admin@example.test",
	}
	if err := repository.CreatePolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	candidate := &scannerrelease.Candidate{
		ID: uuid.NewString(), DefinitionCommit: "0123456789abcdef",
		RiskSummaryJSON: `{"highest":"low"}`, State: scannerrelease.CandidateDraft,
		RequiredGatesJSON: `[]`, PolicyDecision: "approval_required",
		PolicyID: policy.ID, PolicyRevision: policy.Revision,
		Actor: "operator@example.test", IdempotencyKey: uuid.NewString(),
	}
	transitionCommand := func(key string) scannerrelease.TransitionCommand {
		return scannerrelease.TransitionCommand{
			Actor: "operator@example.test", Reason: "route test",
			PolicyRevision: 1, IdempotencyKey: key,
		}
	}
	if err := repository.CreateCandidate(
		ctx, candidate, transitionCommand("create:"+candidate.ID),
	); err != nil {
		t.Fatal(err)
	}
	version := candidate.Version
	for _, state := range []scannerrelease.CandidateState{
		scannerrelease.CandidateQueued, scannerrelease.CandidateBuilding,
		scannerrelease.CandidateTesting, scannerrelease.CandidateSecurityReview,
		scannerrelease.CandidateAwaitingApproval,
	} {
		updated, err := repository.TransitionCandidate(
			ctx, candidate.ID, version, state,
			transitionCommand("transition:"+string(state)+":"+candidate.ID),
		)
		if err != nil {
			t.Fatalf("advance to %s: %v", state, err)
		}
		version = updated.Version
	}
	now := time.Now().UTC().Add(time.Second)
	claimed, err := repository.ClaimNextNotification(
		ctx, "route-worker", now, now.Add(time.Minute),
	)
	if err != nil || claimed == nil {
		t.Fatalf("claim notification = %#v err=%v", claimed, err)
	}
	dead, err := repository.FinalizeNotification(
		ctx, claimed.ID, "route-worker", claimed.LeaseToken,
		scannerrelease.NotificationDeadLetter, now,
		"delivery_rejected", "safe diagnostic", now.Add(time.Second),
	)
	if err != nil {
		t.Fatal(err)
	}

	list := scannerRouteRequest(
		t, ScannerSupplyChainListNotifications, http.MethodGet,
		"/notifications?state=dead_letter&destination_type=webhook", "", nil, nil,
	)
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), dead.ID) {
		t.Fatalf("list notifications = %d body %s", list.Code, list.Body)
	}
	params := map[string]string{"id": dead.ID}
	show := scannerRouteRequest(
		t, ScannerSupplyChainGetNotification, http.MethodGet,
		"/notifications/"+dead.ID, "", nil, params,
	)
	if show.Code != http.StatusOK ||
		show.Header().Get("ETag") != fmt.Sprintf(`"%d"`, dead.Version) ||
		strings.Contains(show.Body.String(), "lease-") {
		t.Fatalf("show notification = %d headers=%v body=%s", show.Code, show.Header(), show.Body)
	}
	missing := scannerRouteRequest(
		t, ScannerSupplyChainRetryNotification, http.MethodPost,
		"/notifications/"+dead.ID+"/retry", `{"reason":"routing repaired"}`, nil, params,
	)
	if missing.Code != http.StatusPreconditionRequired {
		t.Fatalf("retry without preconditions = %d body %s", missing.Code, missing.Body)
	}
	retry := scannerRouteRequest(
		t, ScannerSupplyChainRetryNotification, http.MethodPost,
		"/notifications/"+dead.ID+"/retry", `{"reason":"routing repaired"}`,
		map[string]string{
			"Idempotency-Key": "route-retry-1",
			"If-Match":        fmt.Sprintf(`"%d"`, dead.Version),
		},
		params,
	)
	if retry.Code != http.StatusOK ||
		!strings.Contains(retry.Body.String(), `"state":"retry"`) {
		t.Fatalf("retry notification = %d body %s", retry.Code, retry.Body)
	}
}

func TestScannerRegistryCRUDUsesOptimisticSoftDelete(t *testing.T) {
	withScannerSupplyChainStore(t)
	body := `{"name":"primary","type":"managed","host":"registry.example","namespace":"security","platform_policy":{"required":["linux/amd64"]}}`
	created := scannerRouteRequest(t, ScannerSupplyChainCreateRegistry, http.MethodPost, "/registries", body, nil, nil)
	if created.Code != http.StatusCreated {
		t.Fatalf("create registry = %d, body %s", created.Code, created.Body)
	}
	var envelope struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	decodeScannerResponse(t, created, &envelope)
	params := map[string]string{"id": envelope.Data.ID}
	cleanup := scannerRouteRequest(
		t, ScannerSupplyChainCreateRegistryCleanupJob, http.MethodPost,
		"/registries/"+envelope.Data.ID+"/cleanup-jobs",
		`{"reason":"remove expired unreferenced quarantine objects"}`,
		map[string]string{"Idempotency-Key": "registry-cleanup-route-1"}, params,
	)
	if cleanup.Code != http.StatusAccepted ||
		!strings.Contains(cleanup.Body.String(), `"state":"queued"`) {
		t.Fatalf("queue registry cleanup = %d body %s", cleanup.Code, cleanup.Body)
	}
	var cleanupEnvelope scannerCommandResponse
	decodeScannerResponse(t, cleanup, &cleanupEnvelope)
	jobParams := map[string]string{"id": cleanupEnvelope.ID}
	job := scannerRouteRequest(
		t, ScannerSupplyChainGetRegistryJob, http.MethodGet,
		"/registry-jobs/"+cleanupEnvelope.ID, "", nil, jobParams,
	)
	if job.Code != http.StatusOK || job.Header().Get("ETag") != `"1"` ||
		!strings.Contains(job.Body.String(), `"kind":"cleanup"`) {
		t.Fatalf("get registry cleanup = %d headers=%v body=%s", job.Code, job.Header(), job.Body)
	}
	jobs := scannerRouteRequest(
		t, ScannerSupplyChainListRegistryJobs, http.MethodGet,
		"/registry-jobs?registry_target_id="+envelope.Data.ID, "", nil, nil,
	)
	if jobs.Code != http.StatusOK || !strings.Contains(jobs.Body.String(), cleanupEnvelope.ID) {
		t.Fatalf("list registry jobs = %d body=%s", jobs.Code, jobs.Body)
	}
	patch := scannerRouteRequest(t, ScannerSupplyChainPatchRegistry, http.MethodPatch, "/registries/"+envelope.Data.ID, `{"namespace":"security-prod"}`, map[string]string{"If-Match": `"1"`}, params)
	if patch.Code != http.StatusOK || patch.Header().Get("ETag") != `"2"` {
		t.Fatalf("patch registry = %d, etag %q, body %s", patch.Code, patch.Header().Get("ETag"), patch.Body)
	}
	stale := scannerRouteRequest(t, ScannerSupplyChainDeleteRegistry, http.MethodDelete, "/registries/"+envelope.Data.ID, "", map[string]string{"If-Match": `"1"`}, params)
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale delete = %d, body %s", stale.Code, stale.Body)
	}
	deleted := scannerRouteRequest(t, ScannerSupplyChainDeleteRegistry, http.MethodDelete, "/registries/"+envelope.Data.ID, "", map[string]string{"If-Match": `"2"`}, params)
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("delete registry = %d, body %s", deleted.Code, deleted.Body)
	}
	list := scannerRouteRequest(t, ScannerSupplyChainListRegistries, http.MethodGet, "/registries", "", nil, nil)
	var listed struct {
		Data []any `json:"data"`
	}
	decodeScannerResponse(t, list, &listed)
	if len(listed.Data) != 0 {
		t.Fatalf("enabled registry list after soft delete = %#v", listed.Data)
	}
}

func TestScannerRegistryCredentialReferenceIsWriteOnlyAndOpaque(t *testing.T) {
	store := withScannerSupplyChainStore(t)
	secretID := uuid.NewString()
	body := fmt.Sprintf(
		`{"name":"private","type":"private","host":"registry.example","namespace":"security","secret_reference":"secret:%s","platform_policy":{}}`,
		secretID,
	)
	created := scannerRouteRequest(
		t, ScannerSupplyChainCreateRegistry, http.MethodPost, "/registries",
		body, nil, nil,
	)
	if created.Code != http.StatusCreated {
		t.Fatalf("create registry = %d, body %s", created.Code, created.Body)
	}
	if strings.Contains(created.Body.String(), secretID) ||
		strings.Contains(created.Body.String(), "secret_reference") ||
		!strings.Contains(created.Body.String(), `"credential_reference_configured":true`) ||
		!strings.Contains(created.Body.String(), `"credential_reference_kind":"wolf_secret"`) {
		t.Fatalf("create response exposed or omitted credential metadata: %s", created.Body)
	}
	var envelope struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	decodeScannerResponse(t, created, &envelope)
	stored, err := store.ScannerReleases().GetRegistryTarget(context.Background(), envelope.Data.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.SecretReference != "secret:"+secretID {
		t.Fatalf("stored credential reference = %q", stored.SecretReference)
	}

	get := scannerRouteRequest(
		t, ScannerSupplyChainGetRegistry, http.MethodGet,
		"/registries/"+envelope.Data.ID, "", nil,
		map[string]string{"id": envelope.Data.ID},
	)
	list := scannerRouteRequest(
		t, ScannerSupplyChainListRegistries, http.MethodGet, "/registries", "", nil, nil,
	)
	for name, recorder := range map[string]*httptest.ResponseRecorder{"get": get, "list": list} {
		if strings.Contains(recorder.Body.String(), secretID) ||
			strings.Contains(recorder.Body.String(), "secret_reference") {
			t.Fatalf("%s response exposed credential reference: %s", name, recorder.Body)
		}
	}
}

func TestScannerRegistryRejectsPlaintextLikeCredentialReferencesWithoutEcho(t *testing.T) {
	store := withScannerSupplyChainStore(t)
	values := []string{
		"hunter2",
		"username:password",
		"https://user:token@registry.example",
		"wolf-secret://registry-prod",
		"/run/secrets/registry",
		"secret:not-a-uuid",
		"secret:" + uuid.NewString() + "\nAuthorization: Bearer token",
	}
	for index, value := range values {
		body, err := json.Marshal(map[string]any{
			"name": fmt.Sprintf("private-%d", index), "type": "private",
			"host": "registry.example", "namespace": "security",
			"secret_reference": value, "platform_policy": map[string]any{},
		})
		if err != nil {
			t.Fatal(err)
		}
		recorder := scannerRouteRequest(
			t, ScannerSupplyChainCreateRegistry, http.MethodPost, "/registries",
			string(body), nil, nil,
		)
		if recorder.Code != http.StatusUnprocessableEntity ||
			!strings.Contains(recorder.Body.String(), "registry_credential_reference_invalid") {
			t.Fatalf("value %d accepted: status=%d body=%s", index, recorder.Code, recorder.Body)
		}
		if strings.Contains(recorder.Body.String(), value) {
			t.Fatalf("value %d echoed in error: %s", index, recorder.Body)
		}
	}
	targets, err := store.ScannerReleases().ListRegistryTargets(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 0 {
		t.Fatalf("invalid credential references were persisted: %#v", targets)
	}
}

func TestScannerDecodeRejectsTrailingJSON(t *testing.T) {
	withScannerSupplyChainStore(t)
	recorder := scannerRouteRequest(
		t, ScannerSupplyChainCreateRegistry, http.MethodPost, "/registries",
		`{"name":"one","type":"managed","host":"registry.example"} {"name":"two"}`, nil, nil,
	)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("trailing JSON = %d, body %s", recorder.Code, recorder.Body)
	}
}

func TestScannerRegistryTokenHostsAreExplicitAndDockerAware(t *testing.T) {
	t.Parallel()

	target := &scannerrelease.RegistryTarget{
		PlatformPolicyJSON: `{"token_hosts":["tokens.registry.example","tokens.registry.example"]}`,
	}
	hosts, err := scannerRegistryTokenHosts(target, "registry.example")
	if err != nil {
		t.Fatal(err)
	}
	if len(hosts) != 1 || hosts[0] != "tokens.registry.example" {
		t.Fatalf("custom token hosts = %#v", hosts)
	}
	hosts, err = scannerRegistryTokenHosts(
		&scannerrelease.RegistryTarget{PlatformPolicyJSON: "{}"},
		"registry-1.docker.io",
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(hosts) != 1 || hosts[0] != "auth.docker.io" {
		t.Fatalf("Docker token hosts = %#v", hosts)
	}
}

func TestScannerRegistryTokenHostsRejectURLAndCredentialForms(t *testing.T) {
	t.Parallel()

	for _, value := range []string{
		`{"token_hosts":["https://tokens.example"]}`,
		`{"token_hosts":["user@tokens.example"]}`,
		`{"token_hosts":["tokens.example/path"]}`,
	} {
		_, err := scannerRegistryTokenHosts(
			&scannerrelease.RegistryTarget{PlatformPolicyJSON: value},
			"registry.example",
		)
		if err == nil {
			t.Errorf("token host policy %s unexpectedly succeeded", value)
		}
	}
}

func TestScannerArtifactDiffViewerIsBoundedAndRootConfined(t *testing.T) {
	store := withScannerSupplyChainStore(t)
	root := t.TempDir()
	previousArtifacts := artifacts.Global
	if err := artifacts.Init(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { artifacts.Global = previousArtifacts })

	policy := &scannerrelease.Policy{
		ID: "policy-diff", Scope: "global", Revision: 1, Enabled: true,
		CreatedBy: "test",
	}
	if err := store.ScannerReleases().CreatePolicy(
		context.Background(), policy,
	); err != nil {
		t.Fatal(err)
	}
	candidate := &scannerrelease.Candidate{
		ID: "candidate-diff", DefinitionCommit: "abcdef",
		PolicyID: policy.ID, PolicyRevision: policy.Revision,
		Actor: "test", IdempotencyKey: "candidate-diff",
	}
	command := scannerrelease.TransitionCommand{
		Actor: "test", Reason: "diff viewer test",
		PolicyRevision: 1, IdempotencyKey: "diff-viewer-test",
		PayloadJSON: "{}",
	}
	if err := store.ScannerReleases().CreateCandidate(
		context.Background(), candidate, command,
	); err != nil {
		t.Fatal(err)
	}

	diffDir := filepath.Join(root, "scanner-diffs")
	if err := os.MkdirAll(diffDir, 0o750); err != nil {
		t.Fatal(err)
	}
	manifestContent := []byte(strings.Repeat("+safe manifest change\n", 20_000))
	manifestPath := filepath.Join(diffDir, "manifest.diff")
	if err := os.WriteFile(manifestPath, manifestContent, 0o640); err != nil {
		t.Fatal(err)
	}
	outsideRoot := t.TempDir()
	outsideContent := []byte("+outside secret must not be returned\n")
	outsidePath := filepath.Join(outsideRoot, "lock.diff")
	if err := os.WriteFile(outsidePath, outsideContent, 0o640); err != nil {
		t.Fatal(err)
	}
	for _, artifact := range []*scannerrelease.ReleaseArtifact{
		{
			ID: "candidate-manifest-diff", CandidateID: candidate.ID,
			ArtifactType: "manifest_diff", MediaType: "text/x-diff; charset=utf-8",
			URI:    filepath.ToSlash(filepath.Join("scanner-diffs", "manifest.diff")),
			Digest: scannerDiffTestDigest(manifestContent), SizeBytes: int64(len(manifestContent)),
		},
		{
			ID: "candidate-lock-diff", CandidateID: candidate.ID,
			ArtifactType: "lock_diff", MediaType: "text/x-diff",
			URI: outsidePath, Digest: scannerDiffTestDigest(outsideContent),
			SizeBytes: int64(len(outsideContent)),
		},
	} {
		if err := store.ScannerReleases().AddArtifact(
			context.Background(), artifact,
		); err != nil {
			t.Fatal(err)
		}
	}

	release := scannerrelease.Release{
		ID: "release-diff", Name: "scanner-set-2026.31.1",
		CandidateID: candidate.ID, LockDigest: "sha256:lock",
		ManifestDigest: scannerDiffTestDigest(manifestContent),
		ManifestURI:    "artifact://release-manifest",
		State:          scannerrelease.ReleasePublished, SignerIdentity: "test",
		PolicyID: policy.ID, PolicyRevision: policy.Revision,
		DefinitionCommit: candidate.DefinitionCommit,
	}
	if err := store.ScannerReleases().CreateRelease(
		context.Background(),
		&scannerrelease.ReleaseInventory{
			Release: release,
			Artifacts: []scannerrelease.ReleaseArtifact{{
				ID: "release-manifest-diff", ArtifactType: "manifest_diff",
				MediaType: "text/x-diff",
				URI:       filepath.ToSlash(filepath.Join("scanner-diffs", "manifest.diff")),
				Digest:    scannerDiffTestDigest(manifestContent),
				SizeBytes: int64(len(manifestContent)),
			}},
		},
		scannerrelease.TransitionCommand{
			Actor: "test", Reason: "publish diff fixture",
			PolicyRevision: 1, IdempotencyKey: "release-diff",
			PayloadJSON: "{}",
		},
	); err != nil {
		t.Fatal(err)
	}

	candidateDiff := scannerRouteRequest(
		t, ScannerSupplyChainGetCandidateDiff, http.MethodGet,
		"/candidate-diff/diffs/manifest", "", nil,
		map[string]string{"id": candidate.ID, "kind": "manifest"},
	)
	if candidateDiff.Code != http.StatusOK ||
		candidateDiff.Header().Get("Cache-Control") != "private, no-store" ||
		candidateDiff.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf(
			"candidate diff status=%d headers=%v body=%s",
			candidateDiff.Code, candidateDiff.Header(), candidateDiff.Body,
		)
	}
	var candidateEnvelope struct {
		Data scannerArtifactDiffResponse `json:"data"`
	}
	decodeScannerResponse(t, candidateDiff, &candidateEnvelope)
	got := candidateEnvelope.Data
	if !got.Available || !got.Truncated ||
		got.ReturnedBytes > maxScannerDiffResponseBytes ||
		got.TotalBytes != int64(len(manifestContent)) ||
		got.OwnerType != "candidate" || got.OwnerID != candidate.ID ||
		!strings.HasPrefix(got.Content, "+safe manifest change") {
		t.Fatalf("candidate diff = %#v", got)
	}

	outside := scannerRouteRequest(
		t, ScannerSupplyChainGetCandidateDiff, http.MethodGet,
		"/candidate-diff/diffs/lock", "", nil,
		map[string]string{"id": candidate.ID, "kind": "lock"},
	)
	if outside.Code != http.StatusUnprocessableEntity ||
		strings.Contains(outside.Body.String(), string(outsideContent)) ||
		strings.Contains(outside.Body.String(), outsidePath) {
		t.Fatalf("outside-root diff status=%d body=%s", outside.Code, outside.Body)
	}

	releaseDiff := scannerRouteRequest(
		t, ScannerSupplyChainGetReleaseDiff, http.MethodGet,
		"/release-diff/diffs/manifest", "", nil,
		map[string]string{"id": release.ID, "kind": "manifest"},
	)
	var releaseEnvelope struct {
		Data scannerArtifactDiffResponse `json:"data"`
	}
	decodeScannerResponse(t, releaseDiff, &releaseEnvelope)
	if releaseDiff.Code != http.StatusOK ||
		!releaseEnvelope.Data.Available ||
		releaseEnvelope.Data.OwnerType != "release" {
		t.Fatalf("release diff status=%d data=%#v", releaseDiff.Code, releaseEnvelope.Data)
	}

	empty := scannerRouteRequest(
		t, ScannerSupplyChainGetReleaseDiff, http.MethodGet,
		"/release-diff/diffs/lock", "", nil,
		map[string]string{"id": release.ID, "kind": "lock"},
	)
	var emptyEnvelope struct {
		Data scannerArtifactDiffResponse `json:"data"`
	}
	decodeScannerResponse(t, empty, &emptyEnvelope)
	if empty.Code != http.StatusOK || emptyEnvelope.Data.Available ||
		emptyEnvelope.Data.Content != "" {
		t.Fatalf("empty release diff status=%d data=%#v", empty.Code, emptyEnvelope.Data)
	}

	invalid := scannerRouteRequest(
		t, ScannerSupplyChainGetReleaseDiff, http.MethodGet,
		"/release-diff/diffs/raw", "", nil,
		map[string]string{"id": release.ID, "kind": "raw"},
	)
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid diff kind status=%d body=%s", invalid.Code, invalid.Body)
	}
}

func scannerDiffTestDigest(value []byte) string {
	sum := sha256.Sum256(value)
	return fmt.Sprintf("sha256:%x", sum)
}
