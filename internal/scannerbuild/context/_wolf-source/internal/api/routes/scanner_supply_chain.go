package routes

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/api/response"
	"github.com/alphabravocompany/thewolf/internal/auth"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/scannercontrol"
	"github.com/alphabravocompany/thewolf/internal/scannerfeature"
	"github.com/alphabravocompany/thewolf/internal/scannerpipeline"
	"github.com/alphabravocompany/thewolf/internal/scannerpolicy"
	"github.com/alphabravocompany/thewolf/internal/scannerregistry"
	"github.com/alphabravocompany/thewolf/internal/scannerrelease"
	"github.com/alphabravocompany/thewolf/internal/scannerrollout"
	"github.com/alphabravocompany/thewolf/internal/secrets"
)

const scannerSupplyChainBase = "/api/v1/scanner-supply-chain"

type scannerCursorMeta struct {
	NextCursor string `json:"next_cursor,omitempty"`
}

type scannerCursorResponse struct {
	Data any               `json:"data"`
	Meta scannerCursorMeta `json:"meta"`
	Run  any               `json:"run,omitempty"`
}

type scannerCommandResponse struct {
	ID        string `json:"id"`
	State     string `json:"state"`
	StatusURL string `json:"status_url"`
	EventsURL string `json:"events_url,omitempty"`
}

type scannerpipelineAPIImage struct {
	Key       string                    `json:"key"`
	Kind      scannerpipeline.ImageKind `json:"kind,omitempty"`
	Platforms []string                  `json:"platforms"`
	DependsOn []string                  `json:"depends_on,omitempty"`
}

func scannerPipelineImages(images []scannerpipelineAPIImage) []scannerpipeline.Image {
	result := make([]scannerpipeline.Image, 0, len(images))
	for _, image := range images {
		result = append(result, scannerpipeline.Image{
			Key: image.Key, Kind: image.Kind, Platforms: image.Platforms,
			DependsOn: image.DependsOn,
		})
	}
	return result
}

func scannerReleaseStore() (scannerrelease.Persistence, error) {
	if DefaultHandler == nil || DefaultHandler.Store == nil {
		return nil, errors.New("scanner release persistence is unavailable")
	}
	return DefaultHandler.Store.ScannerReleases(), nil
}

func scannerActor(r *http.Request) string {
	if claims := auth.GetUserFromContext(r.Context()); claims != nil {
		if claims.Email != "" {
			return claims.Email
		}
		if claims.UserID != "" {
			return claims.UserID
		}
	}
	return "api"
}

func scannerIdempotencyKey(w http.ResponseWriter, r *http.Request) (string, bool) {
	value := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if value == "" {
		response.WriteError(w, http.StatusPreconditionRequired, "idempotency_key_required", "Idempotency-Key is required")
		return "", false
	}
	if len(value) > 200 || strings.ContainsAny(value, "\r\n") {
		response.WriteError(w, http.StatusBadRequest, "invalid_idempotency_key", "Idempotency-Key must be at most 200 characters")
		return "", false
	}
	return value, true
}

func scannerExpectedVersion(w http.ResponseWriter, r *http.Request) (int64, bool) {
	value := strings.TrimSpace(r.Header.Get("If-Match"))
	if value == "" {
		response.WriteError(w, http.StatusPreconditionRequired, "if_match_required", "If-Match is required")
		return 0, false
	}
	value = strings.TrimPrefix(value, "W/")
	value = strings.Trim(value, `"`)
	version, err := strconv.ParseInt(value, 10, 64)
	if err != nil || version < 1 {
		response.WriteError(w, http.StatusBadRequest, "invalid_if_match", "If-Match must contain a positive resource version")
		return 0, false
	}
	return version, true
}

func scannerDecode(w http.ResponseWriter, r *http.Request, destination any) bool {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		response.WriteError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return false
	}
	if err := decoder.Decode(&struct{}{}); errors.Is(err, io.EOF) {
		return true
	}
	response.WriteError(w, http.StatusBadRequest, "invalid_request", "request body must contain one JSON value")
	return false
}

func scannerWriteError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, sql.ErrNoRows):
		response.WriteError(w, http.StatusNotFound, "scanner_resource_not_found", "scanner supply-chain resource not found")
	case errors.Is(err, scannerrelease.ErrVersionConflict):
		response.WriteError(w, http.StatusConflict, "stale_revision", "resource changed; reload and retry with its current version")
	case errors.Is(err, scannerrelease.ErrIdempotencyConflict):
		response.WriteError(w, http.StatusConflict, "idempotency_conflict", err.Error())
	case errors.Is(err, scannerrelease.ErrInvalidTransition),
		errors.Is(err, scannercontrol.ErrCandidateNotReady),
		errors.Is(err, scannercontrol.ErrApprovalStale),
		errors.Is(err, scannercontrol.ErrReleaseUnavailable):
		response.WriteError(w, http.StatusConflict, "invalid_scanner_state", err.Error())
	case errors.Is(err, scannercontrol.ErrValidation):
		response.WriteError(w, http.StatusUnprocessableEntity, "scanner_validation_failed", err.Error())
	default:
		response.WriteError(w, http.StatusInternalServerError, "scanner_supply_chain_error", err.Error())
	}
}

func scannerPage(r *http.Request) scannerrelease.PageRequest {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	return scannerrelease.PageRequest{Limit: limit, Cursor: r.URL.Query().Get("cursor")}
}

func scannerOperationAccepted(w http.ResponseWriter, result scannerCommandResponse) {
	w.Header().Set("Retry-After", "2")
	response.WriteJSON(w, http.StatusAccepted, result)
}

// ScannerSupplyChainOverview returns a bounded summary. Each history query is
// limited to one row, while worker health uses a five-minute active window.
func ScannerSupplyChainOverview(w http.ResponseWriter, r *http.Request) {
	mode, err := scannerfeature.Parse(os.Getenv(scannerfeature.EnvironmentVariable))
	if err != nil {
		response.WriteError(w, http.StatusServiceUnavailable, "scanner_release_mode_invalid", "scanner release management mode is invalid")
		return
	}
	store, err := scannerReleaseStore()
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	ctx := r.Context()
	releases, err := store.ListReleases(ctx, scannerrelease.ReleaseFilter{State: scannerrelease.ReleaseStable}, scannerrelease.PageRequest{Limit: 1})
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	candidates, err := store.ListCandidates(ctx, scannerrelease.CandidateFilter{}, scannerrelease.PageRequest{Limit: 1})
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	rollouts, err := store.ListRollouts(ctx, scannerrelease.RolloutFilter{}, scannerrelease.PageRequest{Limit: 1})
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	discoveries, err := store.ListDiscoveryRuns(ctx, scannerrelease.DiscoveryFilter{}, scannerrelease.PageRequest{Limit: 1})
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	workers, err := store.ListWorkerReleaseStatuses(ctx, "", time.Now().UTC().Add(-5*time.Minute))
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	registries, err := store.ListRegistryTargets(ctx, true)
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	alerts, err := store.AlertCounts(ctx)
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	policy, err := scannercontrol.Service{Store: store}.EnsureDefaultPolicy(ctx, scannerActor(r))
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	var activeRelease *scannerrelease.Release
	if len(releases.Items) != 0 {
		activeRelease = &releases.Items[0]
	}
	var pendingCandidate *scannerrelease.Candidate
	if len(candidates.Items) != 0 && !scannerrelease.IsTerminalCandidateState(candidates.Items[0].State) {
		pendingCandidate = &candidates.Items[0]
	}
	var activeRollout *scannerrelease.Rollout
	if len(rollouts.Items) != 0 && !scannerrelease.IsTerminalRolloutState(rollouts.Items[0].State) {
		activeRollout = &rollouts.Items[0]
	}
	var latestDiscovery *scannerrelease.DiscoveryRun
	if len(discoveries.Items) != 0 {
		latestDiscovery = &discoveries.Items[0]
	}
	freshness := map[string]any{
		"status": "unknown", "current": 0, "updates_available": 0,
		"incomplete": 0, "failed": 0, "total": 0,
	}
	pendingUpdates := map[string]int{
		"none": 0, "low": 0, "medium": 0, "high": 0, "critical": 0,
	}
	if latestDiscovery != nil {
		age := time.Since(latestDiscovery.UpdatedAt)
		status := "current"
		if age > 8*24*time.Hour {
			status = "stale"
		} else if age > 48*time.Hour {
			status = "aging"
		}
		items, listErr := store.ListUpdateItems(ctx, latestDiscovery.ID)
		if listErr != nil {
			scannerWriteError(w, listErr)
			return
		}
		current, available, incomplete := 0, 0, 0
		for _, item := range items {
			switch item.SelectionState {
			case "current":
				current++
			case "update_available", "available", "selected", "held", "yanked":
				available++
				pendingUpdates[string(item.RiskClass)]++
			default:
				incomplete++
			}
		}
		failed := 0
		if latestDiscovery.State == scannerrelease.DiscoveryFailed {
			failed = 1
			status = "failed"
		} else if incomplete != 0 && status == "current" {
			status = "incomplete"
		}
		freshness = map[string]any{
			"status": status, "last_checked_at": latestDiscovery.UpdatedAt,
			"age_seconds":       int64(age.Seconds()),
			"current":           current,
			"updates_available": available,
			"incomplete":        incomplete,
			"failed":            failed,
			"total":             len(items),
		}
	}
	cohorts, readyWorkers, driftedWorkers, failedWorkers := scannerWorkerOverview(workers)
	schedule := scannerScheduleOverview(policy, latestDiscovery, candidates.Items)
	registryHealthy, registryDegraded, registryFailed, registryUnknown := 0, 0, 0, 0
	for _, target := range registries {
		switch target.HealthStatus {
		case "healthy":
			registryHealthy++
		case "degraded":
			registryDegraded++
		case "failed":
			registryFailed++
		default:
			registryUnknown++
		}
	}
	stableAge := int64(0)
	if activeRelease != nil {
		stableAge = int64(time.Since(activeRelease.PublishedAt).Seconds())
		if stableAge < 0 {
			stableAge = 0
		}
	}
	alertHealth := "healthy"
	if alerts.OpenCritical > 0 {
		alertHealth = "critical"
	} else if alerts.OpenWarning > 0 {
		alertHealth = "warning"
	}
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: map[string]any{
		"active_release":             activeRelease,
		"capabilities":               mode.Capabilities(),
		"stable_release":             activeRelease,
		"stable_release_age_seconds": stableAge,
		"pending_candidate":          pendingCandidate,
		"active_rollout":             activeRollout,
		"latest_discovery":           latestDiscovery,
		"freshness":                  freshness,
		"pending_updates":            pendingUpdates,
		"cohorts":                    cohorts,
		"worker_health": map[string]any{
			"active": len(workers), "total": len(workers), "ready": readyWorkers,
			"drifted": driftedWorkers, "failed": failedWorkers, "workers": workers,
		},
		"registry_health": map[string]any{
			"configured": len(registries), "total": len(registries),
			"healthy": registryHealthy, "degraded": registryDegraded,
			"failed": registryFailed, "unknown": registryUnknown,
			"targets": registries,
		},
		"discovery_schedule": schedule["discovery"],
		"candidate_schedule": schedule["candidate"],
		"alerts":             alerts,
		"alert_health":       alertHealth,
		"generated_at":       time.Now().UTC(),
	}})
}

func scannerWorkerOverview(workers []scannerrelease.WorkerReleaseStatus) ([]map[string]any, int, int, int) {
	type counts struct {
		total, ready, drifted, failed int
		desired, observed             string
	}
	grouped := make(map[string]*counts)
	ready, drifted, failed := 0, 0, 0
	for _, worker := range workers {
		cohort := worker.Cohort
		if cohort == "" {
			cohort = "unassigned"
		}
		current := grouped[cohort]
		if current == nil {
			current = &counts{desired: worker.DesiredReleaseID, observed: worker.ObservedReleaseID}
			grouped[cohort] = current
		}
		current.total++
		switch {
		case worker.VerificationError != "":
			current.failed++
			failed++
		case worker.DesiredReleaseID != "" &&
			worker.DesiredReleaseID == worker.ObservedReleaseID &&
			worker.VerificationState == "verified":
			current.ready++
			ready++
		default:
			current.drifted++
			drifted++
		}
	}
	names := make([]string, 0, len(grouped))
	for name := range grouped {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]map[string]any, 0, len(names))
	for _, name := range names {
		value := grouped[name]
		state := "healthy"
		if value.failed != 0 {
			state = "failed"
		} else if value.drifted != 0 {
			state = "degraded"
		}
		result = append(result, map[string]any{
			"name": name, "state": state, "total_workers": value.total,
			"ready_workers": value.ready, "failed_workers": value.failed,
			"desired_release_id": value.desired, "observed_release_id": value.observed,
		})
	}
	return result, ready, drifted, failed
}

func scannerScheduleOverview(
	policy *scannerrelease.Policy,
	latestDiscovery *scannerrelease.DiscoveryRun,
	candidates []scannerrelease.Candidate,
) map[string]any {
	result := map[string]any{
		"discovery": map[string]any{"state": "unavailable"},
		"candidate": map[string]any{"state": "unavailable"},
	}
	schedule, err := scannerpolicy.ValidateScheduleJSON([]byte(policy.ScheduleJSON))
	if err != nil {
		return result
	}
	now := time.Now().UTC()
	discovery := map[string]any{
		"state": "healthy",
		"next_run_at": scannerNextPolicyTime(
			now, schedule.Timezone, schedule.DailyDiscovery.At, "",
		),
	}
	if latestDiscovery != nil && latestDiscovery.State == scannerrelease.DiscoveryCompleted {
		discovery["last_success_at"] = latestDiscovery.CompletedAt
	}
	candidate := map[string]any{
		"state": "healthy",
		"next_run_at": scannerNextPolicyTime(
			now, schedule.Timezone, schedule.WeeklyCandidate.At,
			schedule.WeeklyCandidate.Weekday,
		),
	}
	for _, current := range candidates {
		if scannerrelease.IsTerminalCandidateState(current.State) {
			candidate["last_success_at"] = current.UpdatedAt
			break
		}
	}
	result["discovery"] = discovery
	result["candidate"] = candidate
	return result
}

func scannerNextPolicyTime(now time.Time, timezone, clock, weekday string) time.Time {
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return time.Time{}
	}
	parsed, err := time.Parse("15:04", clock)
	if err != nil {
		return time.Time{}
	}
	local := now.In(location)
	next := time.Date(local.Year(), local.Month(), local.Day(), parsed.Hour(), parsed.Minute(), 0, 0, location)
	if weekday == "" {
		if !next.After(local) {
			next = next.AddDate(0, 0, 1)
		}
		return next.UTC()
	}
	weekdays := map[string]time.Weekday{
		"Sunday": time.Sunday, "Monday": time.Monday, "Tuesday": time.Tuesday,
		"Wednesday": time.Wednesday, "Thursday": time.Thursday,
		"Friday": time.Friday, "Saturday": time.Saturday,
	}
	target, ok := weekdays[weekday]
	if !ok {
		return time.Time{}
	}
	days := (int(target) - int(local.Weekday()) + 7) % 7
	next = next.AddDate(0, 0, days)
	if !next.After(local) {
		next = next.AddDate(0, 0, 7)
	}
	return next.UTC()
}

func ScannerSupplyChainGetPolicy(w http.ResponseWriter, r *http.Request) {
	store, err := scannerReleaseStore()
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	service := scannercontrol.Service{Store: store}
	policy, err := service.EnsureDefaultPolicy(r.Context(), scannerActor(r))
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	w.Header().Set("ETag", fmt.Sprintf(`"%d"`, policy.Revision))
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: scannerPolicyView(*policy)})
}

func scannerPolicyView(policy scannerrelease.Policy) map[string]any {
	var schedule, rules any
	_ = json.Unmarshal([]byte(policy.ScheduleJSON), &schedule)
	_ = json.Unmarshal([]byte(policy.RulesJSON), &rules)
	return map[string]any{
		"id": policy.ID, "scope": policy.Scope, "revision": policy.Revision,
		"enabled": policy.Enabled, "schedule": schedule, "rules": rules,
		"created_by": policy.CreatedBy, "created_at": policy.CreatedAt, "updated_at": policy.UpdatedAt,
	}
}

func ScannerSupplyChainPutPolicy(w http.ResponseWriter, r *http.Request) {
	expected, ok := scannerExpectedVersion(w, r)
	if !ok {
		return
	}
	var request struct {
		Schedule json.RawMessage `json:"schedule"`
		Rules    json.RawMessage `json:"rules"`
		Reason   string          `json:"reason"`
	}
	if !scannerDecode(w, r, &request) {
		return
	}
	store, err := scannerReleaseStore()
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	current, err := scannercontrol.Service{Store: store}.EnsureDefaultPolicy(r.Context(), scannerActor(r))
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	if current.Revision != expected {
		scannerWriteError(w, scannerrelease.ErrVersionConflict)
		return
	}
	if !jsonObject(request.Schedule) || !jsonObject(request.Rules) {
		response.WriteError(w, http.StatusUnprocessableEntity, "scanner_policy_invalid", "schedule and rules must be JSON objects")
		return
	}
	validatedSchedule, err := scannerpolicy.ValidateScheduleJSON(request.Schedule)
	if err != nil {
		response.WriteError(w, http.StatusUnprocessableEntity, "scanner_policy_invalid", err.Error())
		return
	}
	var rules scannerpolicy.Policy
	if err := json.Unmarshal(request.Rules, &rules); err != nil {
		response.WriteError(w, http.StatusUnprocessableEntity, "scanner_policy_invalid", err.Error())
		return
	}
	if err := rules.Normalize(); err != nil {
		response.WriteError(w, http.StatusUnprocessableEntity, "scanner_policy_invalid", err.Error())
		return
	}
	rules.Revision = current.Revision + 1
	normalizedRules, err := json.Marshal(rules)
	if err != nil {
		response.WriteError(w, http.StatusUnprocessableEntity, "scanner_policy_invalid", err.Error())
		return
	}
	normalizedSchedule, err := json.Marshal(validatedSchedule)
	if err != nil {
		response.WriteError(w, http.StatusUnprocessableEntity, "scanner_policy_invalid", err.Error())
		return
	}
	next := &scannerrelease.Policy{
		ID: uuid.NewString(), Scope: current.Scope, Revision: current.Revision + 1, Enabled: true,
		ScheduleJSON: string(normalizedSchedule), RulesJSON: string(normalizedRules), CreatedBy: scannerActor(r),
	}
	if err := store.CreatePolicy(r.Context(), next); err != nil {
		scannerWriteError(w, err)
		return
	}
	w.Header().Set("ETag", fmt.Sprintf(`"%d"`, next.Revision))
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: scannerPolicyView(*next)})
}

func ScannerSupplyChainValidatePolicy(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Schedule json.RawMessage `json:"schedule"`
		Rules    json.RawMessage `json:"rules"`
	}
	if !scannerDecode(w, r, &request) {
		return
	}
	result := map[string]any{"valid": true, "errors": []string{}, "warnings": []string{}}
	var validationErrors []string
	var validatedSchedule scannerpolicy.SchedulePolicy
	scheduleValid := false
	if !jsonObject(request.Schedule) {
		validationErrors = append(validationErrors, "schedule must be a JSON object")
	} else {
		var err error
		validatedSchedule, err = scannerpolicy.ValidateScheduleJSON(request.Schedule)
		if err != nil {
			validationErrors = append(validationErrors, err.Error())
		} else {
			scheduleValid = true
		}
	}
	var rules scannerpolicy.Policy
	if err := json.Unmarshal(request.Rules, &rules); err != nil {
		validationErrors = append(validationErrors, "rules: "+err.Error())
	} else if err := rules.Normalize(); err != nil {
		validationErrors = append(validationErrors, err.Error())
	}
	if len(validationErrors) != 0 {
		result["valid"] = false
		result["errors"] = validationErrors
	}
	if scheduleValid {
		result["next_execution"] = scannerPolicySchedulePreview(time.Now().UTC(), validatedSchedule)
	}
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: result})
}

func scannerPolicySchedulePreview(now time.Time, schedule scannerpolicy.SchedulePolicy) map[string]any {
	result := map[string]any{}
	if schedule.DailyDiscovery.IsEnabled() {
		result["daily_discovery"] = scannerNextPolicyTime(
			now, schedule.Timezone, schedule.DailyDiscovery.At, "",
		)
	}
	if schedule.WeeklyCandidate.IsEnabled() {
		result["weekly_candidate"] = scannerNextPolicyTime(
			now, schedule.Timezone, schedule.WeeklyCandidate.At,
			schedule.WeeklyCandidate.Weekday,
		)
	}
	occurrences, err := schedule.NextMaintenanceWindows(now)
	if err == nil {
		windows := make([]map[string]any, 0, len(occurrences))
		for _, occurrence := range occurrences {
			windows = append(windows, map[string]any{
				"id": occurrence.ID, "name": occurrence.Name,
				"at": occurrence.At, "duration": occurrence.Duration.String(),
			})
		}
		result["maintenance_windows"] = windows
	}
	return result
}

func ScannerSupplyChainListPolicyRevisions(w http.ResponseWriter, r *http.Request) {
	store, err := scannerReleaseStore()
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	policies, err := store.ListPolicies(r.Context(), "global", false)
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	result := make([]map[string]any, 0, len(policies))
	for _, policy := range policies {
		result = append(result, scannerPolicyView(policy))
	}
	response.WriteJSON(w, http.StatusOK, scannerCursorResponse{Data: result, Meta: scannerCursorMeta{}})
}

func ScannerSupplyChainRestorePolicy(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Reason string `json:"reason"`
	}
	if !scannerDecode(w, r, &request) {
		return
	}
	if strings.TrimSpace(request.Reason) == "" {
		response.WriteError(w, http.StatusUnprocessableEntity, "reason_required", "reason is required")
		return
	}
	revision, err := strconv.ParseInt(chi.URLParam(r, "revision"), 10, 64)
	if err != nil || revision < 1 {
		response.WriteError(w, http.StatusBadRequest, "invalid_revision", "policy revision must be positive")
		return
	}
	store, err := scannerReleaseStore()
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	all, err := store.ListPolicies(r.Context(), "global", false)
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	var source *scannerrelease.Policy
	var currentRevision int64
	for index := range all {
		if all[index].Revision > currentRevision {
			currentRevision = all[index].Revision
		}
		if all[index].Revision == revision {
			copy := all[index]
			source = &copy
		}
	}
	if source == nil {
		response.WriteError(w, http.StatusNotFound, "policy_revision_not_found", "policy revision not found")
		return
	}
	var rules scannerpolicy.Policy
	if err := json.Unmarshal([]byte(source.RulesJSON), &rules); err != nil {
		scannerWriteError(w, err)
		return
	}
	rules.Revision = currentRevision + 1
	rulesJSON, _ := json.Marshal(rules)
	restored := &scannerrelease.Policy{
		ID: uuid.NewString(), Scope: source.Scope, Revision: currentRevision + 1, Enabled: true,
		ScheduleJSON: source.ScheduleJSON, RulesJSON: string(rulesJSON), CreatedBy: scannerActor(r),
	}
	if err := store.CreatePolicy(r.Context(), restored); err != nil {
		scannerWriteError(w, err)
		return
	}
	w.Header().Set("ETag", fmt.Sprintf(`"%d"`, restored.Revision))
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: scannerPolicyView(*restored)})
}

func ScannerSupplyChainPolicyDryRun(w http.ResponseWriter, r *http.Request) {
	var request struct {
		CandidateID string          `json:"candidate_id"`
		Schedule    json.RawMessage `json:"schedule"`
		Rules       json.RawMessage `json:"rules"`
	}
	if !scannerDecode(w, r, &request) {
		return
	}
	store, err := scannerReleaseStore()
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	candidate, err := store.GetCandidate(r.Context(), request.CandidateID)
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	schedule, err := scannerpolicy.ValidateScheduleJSON(request.Schedule)
	if err != nil {
		response.WriteError(w, http.StatusUnprocessableEntity, "scanner_policy_invalid", err.Error())
		return
	}
	now := time.Now().UTC()
	maintenanceWindowOpen, _, err := schedule.MaintenanceWindowStatus(now)
	if err != nil {
		response.WriteError(w, http.StatusUnprocessableEntity, "scanner_policy_invalid", err.Error())
		return
	}
	var policy scannerpolicy.Policy
	if err := json.Unmarshal(request.Rules, &policy); err != nil {
		response.WriteError(w, http.StatusUnprocessableEntity, "scanner_policy_invalid", err.Error())
		return
	}
	if policy.Revision < 1 {
		policy.Revision = candidate.PolicyRevision
	}
	if err := policy.Normalize(); err != nil {
		response.WriteError(w, http.StatusUnprocessableEntity, "scanner_policy_invalid", err.Error())
		return
	}
	risk := scannerpolicy.RiskHigh
	var riskSummary struct {
		Highest string `json:"highest_risk"`
		Risk    string `json:"risk"`
	}
	_ = json.Unmarshal([]byte(candidate.RiskSummaryJSON), &riskSummary)
	if value := firstScannerValue(riskSummary.Highest, riskSummary.Risk); value != "" {
		risk = scannerpolicy.Risk(value)
	}
	gateStatus := scannerpolicy.GatePending
	switch candidate.State {
	case scannerrelease.CandidateAwaitingApproval, scannerrelease.CandidateApproved,
		scannerrelease.CandidatePublishing, scannerrelease.CandidatePublished:
		gateStatus = scannerpolicy.GatePassed
	}
	evidenceDigest := candidate.PolicyDecision
	if evidenceDigest == "" {
		evidenceDigest = candidate.LockDigest
	}
	gates := make([]scannerpolicy.Gate, 0, len(policy.RequiredGates))
	for _, name := range policy.RequiredGates {
		gates = append(gates, scannerpolicy.Gate{
			Name: name, Status: gateStatus, EvidenceDigest: evidenceDigest,
		})
	}
	approvals, err := store.ListApprovals(r.Context(), candidate.ID, "")
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	policyApprovals := make([]scannerpolicy.Approval, 0, len(approvals))
	for _, approval := range approvals {
		if approval.Action == "approve" {
			policyApprovals = append(policyApprovals, scannerpolicy.Approval{
				ActorID: approval.Actor, LockDigest: candidate.LockDigest,
				PolicyDecisionDigest: approval.PolicyDecision, CreatedAt: approval.CreatedAt,
			})
		}
	}
	decision, err := scannerpolicy.Evaluate(scannerpolicy.Candidate{
		ID: candidate.ID, LockDigest: candidate.LockDigest, PolicyRevision: policy.Revision,
		CreatorID: candidate.Actor, Risk: risk, Gates: gates, Approvals: policyApprovals,
		MaintenanceWindowOpen: maintenanceWindowOpen,
	}, policy, now)
	if err != nil {
		response.WriteError(w, http.StatusUnprocessableEntity, "scanner_policy_dry_run_failed", err.Error())
		return
	}
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: decision})
}

func firstScannerValue(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func jsonObject(value json.RawMessage) bool {
	var object map[string]any
	return len(value) != 0 && json.Unmarshal(value, &object) == nil && object != nil
}

func ScannerSupplyChainCreateDiscovery(w http.ResponseWriter, r *http.Request) {
	key, ok := scannerIdempotencyKey(w, r)
	if !ok {
		return
	}
	var request struct {
		Scope            any    `json:"scope"`
		Reason           string `json:"reason"`
		DefinitionCommit string `json:"definition_commit"`
	}
	if !scannerDecode(w, r, &request) {
		return
	}
	store, err := scannerReleaseStore()
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	if request.DefinitionCommit == "" {
		request.DefinitionCommit = scannerDefinitionIdentity()
	}
	run, err := (scannercontrol.Service{Store: store}).CreateDiscovery(r.Context(), scannercontrol.DiscoveryCommand{
		Trigger: scannerrelease.DiscoveryOnDemand, DefinitionCommit: request.DefinitionCommit,
		Actor: scannerActor(r), IdempotencyKey: key, Scope: request.Scope,
	})
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	base := scannerSupplyChainBase + "/discovery-runs/" + run.ID
	scannerOperationAccepted(w, scannerCommandResponse{ID: run.ID, State: string(run.State), StatusURL: base, EventsURL: base + "/events"})
}

func scannerDefinitionIdentity() string {
	if configured := strings.TrimSpace(os.Getenv("WOLF_SCANNER_DEFINITION_COMMIT")); configured != "" {
		return configured
	}
	content, err := os.ReadFile("scanners/scanner-lock.yaml")
	if err != nil {
		return "runtime:unresolved"
	}
	sum := sha256.Sum256(content)
	return "lock:sha256:" + hex.EncodeToString(sum[:])
}

func ScannerSupplyChainListDiscoveries(w http.ResponseWriter, r *http.Request) {
	store, err := scannerReleaseStore()
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	page, err := store.ListDiscoveryRuns(r.Context(), scannerrelease.DiscoveryFilter{
		State:   scannerrelease.DiscoveryState(r.URL.Query().Get("state")),
		Trigger: scannerrelease.DiscoveryTrigger(r.URL.Query().Get("trigger")),
	}, scannerPage(r))
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, scannerCursorResponse{Data: page.Items, Meta: scannerCursorMeta{NextCursor: page.NextCursor}})
}

func ScannerSupplyChainGetDiscovery(w http.ResponseWriter, r *http.Request) {
	store, err := scannerReleaseStore()
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	id := chi.URLParam(r, "id")
	run, err := store.GetDiscoveryRun(r.Context(), id)
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	items, err := store.ListUpdateItems(r.Context(), id)
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	w.Header().Set("ETag", fmt.Sprintf(`"%d"`, run.Version))
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: map[string]any{"run": run, "items": items}})
}

func ScannerSupplyChainCancelDiscovery(w http.ResponseWriter, r *http.Request) {
	key, ok := scannerIdempotencyKey(w, r)
	if !ok {
		return
	}
	expected, ok := scannerExpectedVersion(w, r)
	if !ok {
		return
	}
	store, err := scannerReleaseStore()
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	id := chi.URLParam(r, "id")
	run, err := store.TransitionDiscovery(r.Context(), id, expected, scannerrelease.DiscoveryCancelled, scannerrelease.TransitionCommand{
		Actor: scannerActor(r), Reason: "cancelled by operator", IdempotencyKey: key, PayloadJSON: "{}",
	})
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	base := scannerSupplyChainBase + "/discovery-runs/" + run.ID
	scannerOperationAccepted(w, scannerCommandResponse{ID: run.ID, State: string(run.State), StatusURL: base, EventsURL: base + "/events"})
}

func ScannerSupplyChainCreateCandidate(w http.ResponseWriter, r *http.Request) {
	key, ok := scannerIdempotencyKey(w, r)
	if !ok {
		return
	}
	var request struct {
		DiscoveryRunID   string                    `json:"discovery_run_id"`
		DefinitionCommit string                    `json:"definition_commit"`
		ProposedCommit   string                    `json:"proposed_commit"`
		ProposalURL      string                    `json:"proposal_url"`
		LockDigest       string                    `json:"lock_digest"`
		LockURI          string                    `json:"lock_uri"`
		RiskSummary      any                       `json:"risk_summary"`
		SelectedItems    []string                  `json:"selected_items"`
		Images           []scannerpipelineAPIImage `json:"images"`
		Reason           string                    `json:"reason"`
	}
	if !scannerDecode(w, r, &request) {
		return
	}
	request.Reason = strings.TrimSpace(request.Reason)
	if request.Reason == "" {
		response.WriteError(w, http.StatusUnprocessableEntity, "reason_required", "reason is required")
		return
	}
	if request.DefinitionCommit == "" {
		request.DefinitionCommit = scannerDefinitionIdentity()
	}
	store, err := scannerReleaseStore()
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	candidate, err := (scannercontrol.Service{Store: store}).CreateCandidate(r.Context(), scannercontrol.CandidateCommand{
		DiscoveryRunID: request.DiscoveryRunID, DefinitionCommit: request.DefinitionCommit,
		ProposedCommit: request.ProposedCommit, ProposalURL: request.ProposalURL,
		LockDigest: request.LockDigest, LockURI: request.LockURI, RiskSummary: request.RiskSummary,
		SelectedItems: request.SelectedItems, Actor: scannerActor(r), Reason: request.Reason, IdempotencyKey: key,
		Images: scannerPipelineImages(request.Images),
	})
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	base := scannerSupplyChainBase + "/candidates/" + candidate.ID
	scannerOperationAccepted(w, scannerCommandResponse{ID: candidate.ID, State: string(candidate.State), StatusURL: base, EventsURL: base + "/events"})
}

func ScannerSupplyChainListUpdates(w http.ResponseWriter, r *http.Request) {
	store, err := scannerReleaseStore()
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	runID := strings.TrimSpace(r.URL.Query().Get("run_id"))
	var run *scannerrelease.DiscoveryRun
	if runID == "" {
		page, listErr := store.ListDiscoveryRuns(r.Context(), scannerrelease.DiscoveryFilter{}, scannerrelease.PageRequest{Limit: 1})
		if listErr != nil {
			scannerWriteError(w, listErr)
			return
		}
		if len(page.Items) == 0 {
			response.WriteJSON(w, http.StatusOK, scannerCursorResponse{Data: []scannerrelease.UpdateItem{}, Meta: scannerCursorMeta{}})
			return
		}
		run = &page.Items[0]
		runID = run.ID
	} else {
		run, err = store.GetDiscoveryRun(r.Context(), runID)
		if err != nil {
			scannerWriteError(w, err)
			return
		}
	}
	items, err := store.ListUpdateItems(r.Context(), runID)
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	status := firstScannerValue(
		strings.TrimSpace(r.URL.Query().Get("status")),
		strings.TrimSpace(r.URL.Query().Get("state")),
	)
	risk := strings.TrimSpace(r.URL.Query().Get("risk"))
	componentType := strings.TrimSpace(r.URL.Query().Get("component_type"))
	source := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("source")))
	integrationTier := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("integration_tier")))
	search := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	filtered := make([]scannerrelease.UpdateItem, 0, len(items))
	for _, item := range items {
		if status != "" && item.SelectionState != status {
			continue
		}
		if risk != "" && string(item.RiskClass) != risk {
			continue
		}
		if componentType != "" && string(item.ComponentType) != componentType {
			continue
		}
		sourceName, tier := scannerUpdateMetadata(item)
		if source != "" && !strings.Contains(strings.ToLower(sourceName), source) {
			continue
		}
		if integrationTier != "" && strings.ToLower(tier) != integrationTier {
			continue
		}
		if search != "" && !strings.Contains(strings.ToLower(item.ComponentName+" "+item.CurrentValue+" "+item.AvailableValue), search) {
			continue
		}
		filtered = append(filtered, item)
	}
	cursor := r.URL.Query().Get("cursor")
	start := 0
	if cursor != "" {
		found := false
		for index := range filtered {
			if filtered[index].ID == cursor {
				start, found = index+1, true
				break
			}
		}
		if !found {
			response.WriteError(w, http.StatusBadRequest, "invalid_cursor", "cursor does not belong to this discovery result")
			return
		}
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	end := start + limit
	next := ""
	if end < len(filtered) {
		next = filtered[end-1].ID
	} else {
		end = len(filtered)
	}
	views := make([]map[string]any, 0, end-start)
	for _, item := range filtered[start:end] {
		sourceName, tier := scannerUpdateMetadata(item)
		encoded, _ := json.Marshal(item)
		var view map[string]any
		_ = json.Unmarshal(encoded, &view)
		if sourceName != "" {
			view["source"] = sourceName
		}
		if tier != "" {
			view["integration_tier"] = tier
		}
		views = append(views, view)
	}
	response.WriteJSON(w, http.StatusOK, scannerCursorResponse{
		Data: views, Meta: scannerCursorMeta{NextCursor: next}, Run: run,
	})
}

func scannerUpdateMetadata(item scannerrelease.UpdateItem) (string, string) {
	var evidence struct {
		Source          string            `json:"source"`
		SourceURL       string            `json:"source_url"`
		URL             string            `json:"url"`
		Reference       string            `json:"reference"`
		IntegrationTier string            `json:"integration_tier"`
		Attributes      map[string]string `json:"attributes"`
		Metadata        map[string]string `json:"metadata"`
	}
	_ = json.Unmarshal([]byte(item.SourceEvidenceJSON), &evidence)
	source := firstScannerValue(evidence.Source, evidence.SourceURL, evidence.URL, evidence.Reference)
	tier := evidence.IntegrationTier
	if tier == "" {
		tier = firstScannerValue(evidence.Attributes["integration_tier"], evidence.Metadata["integration_tier"])
	}
	return source, tier
}

func ScannerSupplyChainListCandidates(w http.ResponseWriter, r *http.Request) {
	store, err := scannerReleaseStore()
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	page, err := store.ListCandidates(r.Context(), scannerrelease.CandidateFilter{
		State: scannerrelease.CandidateState(r.URL.Query().Get("state")),
	}, scannerPage(r))
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	views := make([]map[string]any, 0, len(page.Items))
	for _, candidate := range page.Items {
		encoded, _ := json.Marshal(candidate)
		var view map[string]any
		_ = json.Unmarshal(encoded, &view)
		view["selection"] = scannerCandidateSelectionView(candidate.SelectionJSON)
		views = append(views, view)
	}
	response.WriteJSON(w, http.StatusOK, scannerCursorResponse{Data: views, Meta: scannerCursorMeta{NextCursor: page.NextCursor}})
}

func ScannerSupplyChainGetCandidate(w http.ResponseWriter, r *http.Request) {
	store, err := scannerReleaseStore()
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	id := chi.URLParam(r, "id")
	candidate, err := store.GetCandidate(r.Context(), id)
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	builds, err := store.ListBuildRuns(r.Context(), id)
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	steps := make(map[string][]scannerrelease.BuildStep, len(builds))
	for _, build := range builds {
		buildSteps, stepErr := store.ListBuildSteps(r.Context(), build.ID)
		if stepErr != nil {
			scannerWriteError(w, stepErr)
			return
		}
		steps[build.ID] = buildSteps
	}
	artifacts, err := store.ListArtifacts(r.Context(), "", id)
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	approvals, err := store.ListApprovals(r.Context(), id, "")
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	events, err := store.ListEvents(r.Context(), "candidate", id, 0, 200)
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	candidateView := scannerCandidateDetailView(r, store, candidate, builds, steps, artifacts, approvals, events)
	w.Header().Set("ETag", fmt.Sprintf(`"%d"`, candidate.Version))
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: map[string]any{
		"candidate": candidateView, "builds": builds, "steps": steps,
		"artifacts": artifacts, "approvals": approvals,
	}})
}

func scannerCandidateDetailView(
	r *http.Request,
	store scannerrelease.Persistence,
	candidate *scannerrelease.Candidate,
	builds []scannerrelease.BuildRun,
	steps map[string][]scannerrelease.BuildStep,
	artifacts []scannerrelease.ReleaseArtifact,
	approvals []scannerrelease.Approval,
	events []scannerrelease.Event,
) map[string]any {
	encoded, _ := json.Marshal(candidate)
	var view map[string]any
	_ = json.Unmarshal(encoded, &view)
	view["selection"] = scannerCandidateSelectionView(candidate.SelectionJSON)
	var requiredGates []string
	_ = json.Unmarshal([]byte(candidate.RequiredGatesJSON), &requiredGates)
	view["required_gates"] = requiredGates
	var risk struct {
		Highest string `json:"highest_risk"`
		Risk    string `json:"risk"`
	}
	_ = json.Unmarshal([]byte(candidate.RiskSummaryJSON), &risk)
	view["risk"] = firstScannerValue(risk.Highest, risk.Risk, "unknown")

	gates := make([]map[string]any, 0)
	logs := make([]map[string]any, 0, len(events))
	signatureSteps := make(map[string]candidateVerificationStep)
	provenanceSteps := make(map[string]candidateVerificationStep)
	publicationReceiptDigest := ""
	publicationBuildAttempt := -1
	publicationStepAttempt := -1
	for _, build := range builds {
		for _, step := range steps[build.ID] {
			gate := map[string]any{
				"name": step.StepKey, "state": step.State, "summary": step.SummaryJSON,
				"evidence_digest": step.OutputDigest, "evidence_uri": step.OutputURI,
				"started_at": step.StartedAt, "completed_at": step.CompletedAt,
			}
			gates = append(gates, gate)
			switch {
			case scannerCandidateSignatureStep(step.StepKey):
				selectCandidateVerificationStep(signatureSteps, build.Attempt, step)
			case scannerCandidateProvenanceStep(step.StepKey):
				selectCandidateVerificationStep(provenanceSteps, build.Attempt, step)
			case step.StepKey == "candidate-evidence-summary":
				if build.State == scannerrelease.BuildCompleted &&
					step.State == scannerrelease.BuildCompleted &&
					validScannerSHA256(step.OutputDigest) &&
					(build.Attempt > publicationBuildAttempt ||
						(build.Attempt == publicationBuildAttempt && step.Attempt > publicationStepAttempt)) {
					publicationReceiptDigest = step.OutputDigest
					publicationBuildAttempt = build.Attempt
					publicationStepAttempt = step.Attempt
				}
			}
		}
	}
	for _, event := range events {
		logs = append(logs, map[string]any{
			"id": event.ID, "sequence": event.Sequence, "timestamp": event.CreatedAt,
			"level": "info", "step": event.EventType,
			"message": firstScannerValue(event.Reason, event.EventType), "redacted": true,
		})
	}
	view["gates"] = gates
	view["logs"] = logs
	if publicationReceiptDigest != "" {
		view["publication_receipt_digest"] = publicationReceiptDigest
	}
	if signature := scannerCandidateVerificationAggregate("signature", signatureSteps); signature != nil {
		view["signature"] = signature
	}
	if provenance := scannerCandidateVerificationAggregate("provenance", provenanceSteps); provenance != nil {
		view["provenance"] = provenance
	}
	requiredApprovals := 1
	separateCreator := true
	if policy, err := store.GetPolicy(r.Context(), candidate.PolicyID); err == nil {
		var rules scannerpolicy.Policy
		if json.Unmarshal([]byte(policy.RulesJSON), &rules) == nil {
			requiredApprovals = rules.RequiredApprovals
			separateCreator = rules.SeparateCreator
		}
	}
	validActors := make(map[string]struct{})
	for _, approval := range approvals {
		if approval.Action == "approve" &&
			approval.PolicyDecision == candidate.PolicyDecision &&
			(!separateCreator || approval.Actor != candidate.Actor) {
			validActors[approval.Actor] = struct{}{}
		}
	}
	actor := scannerActor(r)
	view["separation_of_duties"] = map[string]any{
		"creator":                   candidate.Actor,
		"current_actor_can_approve": !separateCreator || actor != candidate.Actor,
		"required_approvals":        requiredApprovals, "valid_approvals": len(validActors),
	}
	return view
}

func scannerCandidateSelectionView(encoded string) map[string]any {
	var selection struct {
		ForceRebuild    bool   `json:"force_rebuild"`
		RebuildReason   string `json:"rebuild_reason"`
		NoOpIfUnchanged bool   `json:"no_op_if_unchanged"`
	}
	_ = json.Unmarshal([]byte(encoded), &selection)
	view := map[string]any{
		"force_rebuild":      selection.ForceRebuild,
		"no_op_if_unchanged": selection.NoOpIfUnchanged,
	}
	allowedReasons := map[string]bool{
		"no_stable_release":                 true,
		"maximum_stable_image_age_exceeded": true,
		"policy_forced_weekly_rebuild":      true,
		"stable_release_within_maximum_age": true,
	}
	if allowedReasons[selection.RebuildReason] {
		view["rebuild_reason"] = selection.RebuildReason
	}
	return view
}

func scannerCandidateSignatureStep(key string) bool {
	return key == "sign" || key == "signature" || key == "sign_images" ||
		strings.HasPrefix(key, "signature/") || key == "release-manifest-signature"
}

func scannerCandidateProvenanceStep(key string) bool {
	return key == "provenance" || key == "generate_provenance" ||
		strings.HasPrefix(key, "provenance/")
}

type candidateVerificationStep struct {
	BuildAttempt int
	Step         scannerrelease.BuildStep
}

func selectCandidateVerificationStep(
	selected map[string]candidateVerificationStep,
	buildAttempt int,
	step scannerrelease.BuildStep,
) {
	current, exists := selected[step.StepKey]
	if !exists || buildAttempt > current.BuildAttempt ||
		(buildAttempt == current.BuildAttempt && step.Attempt > current.Step.Attempt) {
		selected[step.StepKey] = candidateVerificationStep{
			BuildAttempt: buildAttempt,
			Step:         step,
		}
	}
}

func scannerCandidateVerificationAggregate(
	kind string,
	selected map[string]candidateVerificationStep,
) map[string]any {
	if len(selected) == 0 {
		return nil
	}
	keys := make([]string, 0, len(selected))
	for key := range selected {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	digests := make([]string, 0, len(keys))
	verified, failed, pending := 0, 0, 0
	var checkedAt *time.Time
	for _, key := range keys {
		step := selected[key].Step
		switch {
		case step.State == scannerrelease.BuildCompleted && validScannerSHA256(step.OutputDigest):
			verified++
			digests = append(digests, step.OutputDigest)
		case step.State == scannerrelease.BuildFailed ||
			step.State == scannerrelease.BuildCancelled ||
			step.State == scannerrelease.BuildCompleted:
			failed++
		default:
			pending++
		}
		if step.CompletedAt != nil && (checkedAt == nil || step.CompletedAt.After(*checkedAt)) {
			completed := *step.CompletedAt
			checkedAt = &completed
		}
	}
	state := "pending"
	if failed > 0 {
		state = "failed"
	} else if verified == len(keys) {
		state = "verified"
	}
	view := map[string]any{
		"state":          state,
		"total_count":    len(keys),
		"verified_count": verified,
		"failed_count":   failed,
		"pending_count":  pending,
		"keys":           keys,
		"digests":        digests,
		"detail":         fmt.Sprintf("%d of %d %s evidence steps verified", verified, len(keys), kind),
	}
	if checkedAt != nil {
		view["checked_at"] = checkedAt
	}
	return view
}

func validScannerSHA256(value string) bool {
	if len(value) != len("sha256:")+sha256.Size*2 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	decoded, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil && len(decoded) == sha256.Size
}

func scannerApprovalRequest(w http.ResponseWriter, r *http.Request, action string) {
	key, ok := scannerIdempotencyKey(w, r)
	if !ok {
		return
	}
	var request struct {
		LockDigest     string `json:"lock_digest"`
		PolicyDecision string `json:"policy_decision_digest"`
		EvidenceDigest string `json:"evidence_digest"`
		Decision       string `json:"decision"`
		Reason         string `json:"reason"`
	}
	if !scannerDecode(w, r, &request) {
		return
	}
	if request.Decision != "" && request.Decision != action {
		response.WriteError(w, http.StatusUnprocessableEntity, "decision_mismatch", "decision does not match requested action")
		return
	}
	store, err := scannerReleaseStore()
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	command := scannercontrol.ApprovalCommand{
		CandidateID: chi.URLParam(r, "id"), LockDigest: request.LockDigest,
		PolicyDecisionDigest: request.PolicyDecision, EvidenceDigest: request.EvidenceDigest,
		Actor: scannerActor(r), Reason: request.Reason, IdempotencyKey: key,
	}
	var candidate *scannerrelease.Candidate
	if action == "approve" {
		candidate, err = (scannercontrol.Service{Store: store}).ApproveCandidate(r.Context(), command)
	} else {
		candidate, err = (scannercontrol.Service{Store: store}).RejectCandidate(r.Context(), command)
	}
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	base := scannerSupplyChainBase + "/candidates/" + candidate.ID
	scannerOperationAccepted(w, scannerCommandResponse{ID: candidate.ID, State: string(candidate.State), StatusURL: base, EventsURL: base + "/events"})
}

func ScannerSupplyChainApproveCandidate(w http.ResponseWriter, r *http.Request) {
	scannerApprovalRequest(w, r, "approve")
}

func ScannerSupplyChainRejectCandidate(w http.ResponseWriter, r *http.Request) {
	scannerApprovalRequest(w, r, "reject")
}

func ScannerSupplyChainCreateCandidateException(w http.ResponseWriter, r *http.Request) {
	key, ok := scannerIdempotencyKey(w, r)
	if !ok {
		return
	}
	var request struct {
		Gate                string    `json:"gate"`
		OwnerID             string    `json:"owner_id"`
		Reason              string    `json:"reason"`
		CompensatingControl string    `json:"compensating_control"`
		EvidenceDigest      string    `json:"evidence_digest"`
		ExpiresAt           time.Time `json:"expires_at"`
	}
	if !scannerDecode(w, r, &request) {
		return
	}
	store, err := scannerReleaseStore()
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	approval, err := (scannercontrol.Service{Store: store}).AddCandidateException(
		r.Context(), scannercontrol.ExceptionCommand{
			CandidateID: chi.URLParam(r, "id"), Gate: request.Gate,
			OwnerID: request.OwnerID, Reason: request.Reason,
			CompensatingControl: request.CompensatingControl,
			EvidenceDigest:      request.EvidenceDigest, ExpiresAt: request.ExpiresAt,
			Actor: scannerActor(r), IdempotencyKey: key,
		},
	)
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	response.WriteJSON(w, http.StatusCreated, response.SuccessResponse{Data: approval})
}

func ScannerSupplyChainRetryCandidate(w http.ResponseWriter, r *http.Request) {
	key, ok := scannerIdempotencyKey(w, r)
	if !ok {
		return
	}
	expected, ok := scannerExpectedVersion(w, r)
	if !ok {
		return
	}
	store, err := scannerReleaseStore()
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	id := chi.URLParam(r, "id")
	candidate, err := store.GetCandidate(r.Context(), id)
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	if candidate.Version != expected {
		scannerWriteError(w, scannerrelease.ErrVersionConflict)
		return
	}
	if candidate.State != scannerrelease.CandidateBlocked {
		response.WriteError(w, http.StatusConflict, "retry_not_safe", "only a blocked candidate can be retried in place; failed candidates require a new candidate")
		return
	}
	builds, err := store.ListBuildRuns(r.Context(), id)
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	nextAttempt := 1
	var images []scannerpipeline.Image
	for _, build := range builds {
		if build.Attempt >= nextAttempt {
			nextAttempt = build.Attempt + 1
			images = nil
			if err := json.Unmarshal([]byte(build.PlatformsJSON), &images); err != nil {
				response.WriteError(w, http.StatusConflict, "retry_snapshot_invalid", "latest candidate build image/platform snapshot is invalid")
				return
			}
		}
	}
	if len(images) == 0 {
		response.WriteError(w, http.StatusConflict, "retry_snapshot_missing", "candidate has no immutable build image/platform snapshot")
		return
	}
	candidate, err = store.TransitionCandidate(r.Context(), id, expected, scannerrelease.CandidateQueued, scannerrelease.TransitionCommand{
		Actor: scannerActor(r), Reason: "operator requested safe retry", PolicyRevision: candidate.PolicyRevision,
		IdempotencyKey: key, PayloadJSON: "{}",
	})
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	if err := scannercontrol.EnqueueCandidateBuildPlanAttempt(
		r.Context(), store, candidate, images, nextAttempt,
	); err != nil {
		// Compensate the candidate state so it cannot remain queued without
		// runnable work after a storage failure. The failed enqueue itself is
		// idempotent and safe for an operator to retry.
		_, _ = store.TransitionCandidate(
			context.WithoutCancel(r.Context()), id, candidate.Version,
			scannerrelease.CandidateBlocked, scannerrelease.TransitionCommand{
				Actor: scannerActor(r), Reason: "candidate retry build enqueue failed",
				PolicyRevision: candidate.PolicyRevision,
				IdempotencyKey: key + "/compensate", PayloadJSON: "{}",
			},
		)
		scannerWriteError(w, err)
		return
	}
	base := scannerSupplyChainBase + "/candidates/" + id
	scannerOperationAccepted(w, scannerCommandResponse{ID: id, State: string(candidate.State), StatusURL: base, EventsURL: base + "/events"})
}

func ScannerSupplyChainCancelCandidate(w http.ResponseWriter, r *http.Request) {
	key, ok := scannerIdempotencyKey(w, r)
	if !ok {
		return
	}
	expected, ok := scannerExpectedVersion(w, r)
	if !ok {
		return
	}
	var request struct {
		Reason string `json:"reason"`
	}
	if !scannerDecode(w, r, &request) {
		return
	}
	if strings.TrimSpace(request.Reason) == "" {
		response.WriteError(w, http.StatusUnprocessableEntity, "reason_required", "reason is required")
		return
	}
	store, err := scannerReleaseStore()
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	id := chi.URLParam(r, "id")
	candidate, err := store.GetCandidate(r.Context(), id)
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	if candidate.Version != expected {
		scannerWriteError(w, scannerrelease.ErrVersionConflict)
		return
	}
	if scannerrelease.IsTerminalCandidateState(candidate.State) {
		response.WriteError(w, http.StatusConflict, "candidate_terminal", "terminal candidate cannot be cancelled")
		return
	}
	builds, err := store.ListBuildRuns(r.Context(), id)
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	requested := 0
	now := time.Now().UTC()
	for _, build := range builds {
		if scannerrelease.IsTerminalBuildState(build.State) {
			continue
		}
		ok, cancelErr := store.RequestBuildCancellation(r.Context(), build.ID, scannerrelease.TransitionCommand{
			Actor: scannerActor(r), Reason: request.Reason,
			IdempotencyKey: key + "/build/" + build.ID,
			PolicyRevision: candidate.PolicyRevision, PayloadJSON: `{"source":"api"}`,
		}, now)
		if cancelErr != nil {
			scannerWriteError(w, cancelErr)
			return
		}
		if ok {
			requested++
		}
	}
	state := "cancellation_requested"
	if requested == 0 {
		// A candidate with no executing build can terminate immediately. The
		// persisted reason distinguishes cancellation from review rejection.
		candidate, err = store.TransitionCandidate(
			r.Context(), id, expected, scannerrelease.CandidateRejected,
			scannerrelease.TransitionCommand{
				Actor: scannerActor(r), Reason: request.Reason,
				PolicyRevision: candidate.PolicyRevision, IdempotencyKey: key,
				PayloadJSON: `{"action":"cancel"}`,
			},
		)
		if err != nil {
			scannerWriteError(w, err)
			return
		}
		state = string(candidate.State)
	}
	base := scannerSupplyChainBase + "/candidates/" + id
	scannerOperationAccepted(w, scannerCommandResponse{ID: id, State: state, StatusURL: base, EventsURL: base + "/events"})
}

func ScannerSupplyChainPublishCandidate(w http.ResponseWriter, r *http.Request) {
	key, ok := scannerIdempotencyKey(w, r)
	if !ok {
		return
	}
	var request struct {
		Name          string `json:"name"`
		ReceiptDigest string `json:"receipt_digest"`
		Reason        string `json:"reason"`
	}
	if !scannerDecode(w, r, &request) {
		return
	}
	store, err := scannerReleaseStore()
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	release, err := (scannercontrol.Service{Store: store}).PublishCandidate(r.Context(), scannercontrol.PublicationCommand{
		CandidateID: chi.URLParam(r, "id"), Name: request.Name,
		ReceiptDigest: request.ReceiptDigest, Actor: scannerActor(r), Reason: request.Reason,
		IdempotencyKey: key,
	})
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	base := scannerSupplyChainBase + "/releases/" + release.ID
	scannerOperationAccepted(w, scannerCommandResponse{ID: release.ID, State: string(release.State), StatusURL: base, EventsURL: base + "/events"})
}

func ScannerSupplyChainListReleases(w http.ResponseWriter, r *http.Request) {
	store, err := scannerReleaseStore()
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	filter := scannerrelease.ReleaseFilter{State: scannerrelease.ReleaseState(r.URL.Query().Get("state"))}
	if value := r.URL.Query().Get("protected"); value != "" {
		parsed, parseErr := strconv.ParseBool(value)
		if parseErr != nil {
			response.WriteError(w, http.StatusBadRequest, "invalid_filter", "protected must be true or false")
			return
		}
		filter.Protected = &parsed
	}
	page, err := store.ListReleases(r.Context(), filter, scannerPage(r))
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, scannerCursorResponse{Data: page.Items, Meta: scannerCursorMeta{NextCursor: page.NextCursor}})
}

func ScannerSupplyChainGetRelease(w http.ResponseWriter, r *http.Request) {
	store, err := scannerReleaseStore()
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	inventory, err := store.GetReleaseInventory(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	w.Header().Set("ETag", fmt.Sprintf(`"%d"`, inventory.Release.Version))
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: map[string]any{
		"release": inventory.Release, "tools": inventory.Tools,
		"images": inventory.Images, "artifacts": inventory.Artifacts,
	}})
}

func ScannerSupplyChainCompareReleases(w http.ResponseWriter, r *http.Request) {
	fromID := strings.TrimSpace(r.URL.Query().Get("from"))
	toID := strings.TrimSpace(r.URL.Query().Get("to"))
	if fromID == "" || toID == "" {
		response.WriteError(w, http.StatusBadRequest, "release_comparison_invalid", "from and to release IDs are required")
		return
	}
	if fromID == toID {
		response.WriteError(w, http.StatusUnprocessableEntity, "release_comparison_invalid", "from and to must identify different releases")
		return
	}
	store, err := scannerReleaseStore()
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	from, err := store.GetReleaseInventory(r.Context(), fromID)
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	to, err := store.GetReleaseInventory(r.Context(), toID)
	if err != nil {
		scannerWriteError(w, err)
		return
	}

	type toolChange struct {
		Key    string `json:"key"`
		From   string `json:"from,omitempty"`
		To     string `json:"to,omitempty"`
		Change string `json:"change"`
	}
	type imageChange struct {
		Key           string `json:"key"`
		From          string `json:"from,omitempty"`
		To            string `json:"to,omitempty"`
		DigestChanged bool   `json:"digest_changed"`
	}
	fromTools := make(map[string]scannerrelease.ReleaseTool, len(from.Tools))
	toTools := make(map[string]scannerrelease.ReleaseTool, len(to.Tools))
	keys := make(map[string]struct{}, len(from.Tools)+len(to.Tools))
	for _, tool := range from.Tools {
		fromTools[tool.ToolKey] = tool
		keys[tool.ToolKey] = struct{}{}
	}
	for _, tool := range to.Tools {
		toTools[tool.ToolKey] = tool
		keys[tool.ToolKey] = struct{}{}
	}
	toolKeys := sortedScannerKeys(keys)
	tools := make([]toolChange, 0, len(toolKeys))
	for _, key := range toolKeys {
		before, beforeExists := fromTools[key]
		after, afterExists := toTools[key]
		change := "unchanged"
		switch {
		case !beforeExists:
			change = "added"
		case !afterExists:
			change = "removed"
		case before.Version != after.Version ||
			before.SourceDigest != after.SourceDigest ||
			before.Checksum != after.Checksum ||
			before.ParserCompatibility != after.ParserCompatibility:
			change = "changed"
		}
		tools = append(tools, toolChange{
			Key: key, From: before.Version, To: after.Version, Change: change,
		})
	}

	fromImages := make(map[string]scannerrelease.ReleaseImage, len(from.Images))
	toImages := make(map[string]scannerrelease.ReleaseImage, len(to.Images))
	keys = make(map[string]struct{}, len(from.Images)+len(to.Images))
	for _, image := range from.Images {
		fromImages[image.ImageKey] = image
		keys[image.ImageKey] = struct{}{}
	}
	for _, image := range to.Images {
		toImages[image.ImageKey] = image
		keys[image.ImageKey] = struct{}{}
	}
	imageKeys := sortedScannerKeys(keys)
	images := make([]imageChange, 0, len(imageKeys))
	changedTools := 0
	for _, change := range tools {
		if change.Change != "unchanged" {
			changedTools++
		}
	}
	changedImages := 0
	for _, key := range imageKeys {
		before, beforeExists := fromImages[key]
		after, afterExists := toImages[key]
		changed := !beforeExists || !afterExists ||
			before.Digest != after.Digest ||
			before.PlatformDigests != after.PlatformDigests
		if changed {
			changedImages++
		}
		images = append(images, imageChange{
			Key: key, From: before.Digest, To: after.Digest, DigestChanged: changed,
		})
	}
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: map[string]any{
		"from_release":            from.Release,
		"to_release":              to.Release,
		"tools":                   tools,
		"images":                  images,
		"policy_revision_changed": from.Release.PolicyRevision != to.Release.PolicyRevision,
		"summary": fmt.Sprintf(
			"%d of %d tools and %d of %d images changed",
			changedTools, len(tools), changedImages, len(images),
		),
	}})
}

func sortedScannerKeys(values map[string]struct{}) []string {
	keys := make([]string, 0, len(values))
	for value := range values {
		keys = append(keys, value)
	}
	sort.Strings(keys)
	return keys
}

// ScannerSupplyChainVerifyRelease verifies immutable local evidence without
// changing release identity. Registry parity is intentionally performed by the
// target-specific reconcile endpoint because it may require credentials.
func ScannerSupplyChainVerifyRelease(w http.ResponseWriter, r *http.Request) {
	if r.Body != nil && r.ContentLength != 0 {
		var request struct{}
		if !scannerDecode(w, r, &request) {
			return
		}
	}
	store, err := scannerReleaseStore()
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	inventory, err := store.GetReleaseInventory(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	checks := make([]map[string]any, 0, 3+len(inventory.Images)+len(inventory.Artifacts))
	valid := true
	addCheck := func(name string, passed bool, detail string) {
		check := map[string]any{"name": name, "passed": passed}
		if detail != "" {
			check["detail"] = detail
		}
		checks = append(checks, check)
		if !passed {
			valid = false
		}
	}
	addCheck("release_state", inventory.Release.State != scannerrelease.ReleaseRevoked, string(inventory.Release.State))
	addCheck("manifest_digest", scannerValidSHA256Digest(inventory.Release.ManifestDigest), inventory.Release.ManifestDigest)
	addCheck("lock_digest", scannerValidSHA256Digest(inventory.Release.LockDigest), inventory.Release.LockDigest)
	addCheck("signer_identity", strings.TrimSpace(inventory.Release.SignerIdentity) != "", inventory.Release.SignerIdentity)
	addCheck("tool_inventory", len(inventory.Tools) != 0, fmt.Sprintf("%d tools", len(inventory.Tools)))
	for _, image := range inventory.Images {
		digestValid := scannerValidSHA256Digest(image.Digest)
		evidenceValid := strings.EqualFold(image.SignatureStatus, "verified") &&
			scannerValidSHA256Digest(image.ProvenanceDigest) &&
			scannerValidSHA256Digest(image.SBOMDigest)
		platformsValid := scannerPlatformDigestsValid(image.PlatformDigests)
		addCheck("image:"+image.ImageKey, digestValid && evidenceValid && platformsValid,
			fmt.Sprintf("digest=%t signature=%s provenance=%t sbom=%t platforms=%t",
				digestValid, image.SignatureStatus,
				scannerValidSHA256Digest(image.ProvenanceDigest),
				scannerValidSHA256Digest(image.SBOMDigest), platformsValid,
			))
	}
	for _, artifact := range inventory.Artifacts {
		addCheck("artifact:"+artifact.ArtifactType, scannerValidSHA256Digest(artifact.Digest), artifact.Digest)
	}
	state := "verified"
	if !valid {
		state = "failed"
	}
	status := http.StatusOK
	if !valid {
		status = http.StatusUnprocessableEntity
	}
	response.WriteJSON(w, status, response.SuccessResponse{Data: map[string]any{
		"id": inventory.Release.ID, "state": state,
		"status_url":      scannerSupplyChainBase + "/releases/" + inventory.Release.ID,
		"manifest_digest": inventory.Release.ManifestDigest,
		"verified":        valid, "checks": checks, "checked_at": time.Now().UTC(),
	}})
}

func scannerValidSHA256Digest(value string) bool {
	encoded := strings.TrimPrefix(value, "sha256:")
	if len(encoded) != sha256.Size*2 || encoded == value {
		return false
	}
	_, err := hex.DecodeString(encoded)
	return err == nil
}

func scannerPlatformDigestsValid(value string) bool {
	var platforms map[string]string
	if json.Unmarshal([]byte(value), &platforms) != nil || len(platforms) == 0 {
		return false
	}
	for platform, digest := range platforms {
		if !strings.Contains(platform, "/") || !scannerValidSHA256Digest(digest) {
			return false
		}
	}
	return true
}

func ScannerSupplyChainPromoteRelease(w http.ResponseWriter, r *http.Request) {
	key, ok := scannerIdempotencyKey(w, r)
	if !ok {
		return
	}
	var request struct {
		Target   string `json:"target"`
		Strategy string `json:"strategy"`
		Reason   string `json:"reason"`
	}
	if !scannerDecode(w, r, &request) {
		return
	}
	store, err := scannerReleaseStore()
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	rollout, err := (scannercontrol.Service{Store: store}).PromoteRelease(r.Context(), scannercontrol.RolloutCommand{
		ReleaseID: chi.URLParam(r, "id"), Target: request.Target, Strategy: request.Strategy,
		Reason: request.Reason, Actor: scannerActor(r), IdempotencyKey: key,
	})
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	base := scannerSupplyChainBase + "/rollouts/" + rollout.ID
	scannerOperationAccepted(w, scannerCommandResponse{ID: rollout.ID, State: string(rollout.State), StatusURL: base, EventsURL: base + "/events"})
}

func scannerTransitionRelease(w http.ResponseWriter, r *http.Request, target scannerrelease.ReleaseState) {
	key, ok := scannerIdempotencyKey(w, r)
	if !ok {
		return
	}
	expected, ok := scannerExpectedVersion(w, r)
	if !ok {
		return
	}
	var request struct {
		Reason       string `json:"reason"`
		ImpactPolicy string `json:"impact_policy"`
	}
	if !scannerDecode(w, r, &request) {
		return
	}
	if strings.TrimSpace(request.Reason) == "" {
		response.WriteError(w, http.StatusUnprocessableEntity, "reason_required", "reason is required")
		return
	}
	store, err := scannerReleaseStore()
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	payload, _ := json.Marshal(map[string]string{"impact_policy": request.ImpactPolicy})
	release, err := store.TransitionRelease(r.Context(), chi.URLParam(r, "id"), expected, target, scannerrelease.TransitionCommand{
		Actor: scannerActor(r), Reason: request.Reason, IdempotencyKey: key, PayloadJSON: string(payload),
	})
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	base := scannerSupplyChainBase + "/releases/" + release.ID
	scannerOperationAccepted(w, scannerCommandResponse{ID: release.ID, State: string(release.State), StatusURL: base, EventsURL: base + "/events"})
}

func ScannerSupplyChainDeprecateRelease(w http.ResponseWriter, r *http.Request) {
	scannerTransitionRelease(w, r, scannerrelease.ReleaseDeprecated)
}

func ScannerSupplyChainRevokeRelease(w http.ResponseWriter, r *http.Request) {
	scannerTransitionRelease(w, r, scannerrelease.ReleaseRevoked)
}

func ScannerSupplyChainListRollouts(w http.ResponseWriter, r *http.Request) {
	store, err := scannerReleaseStore()
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	page, err := store.ListRollouts(r.Context(), scannerrelease.RolloutFilter{
		State: scannerrelease.RolloutState(r.URL.Query().Get("state")), Target: r.URL.Query().Get("target"),
	}, scannerPage(r))
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, scannerCursorResponse{Data: page.Items, Meta: scannerCursorMeta{NextCursor: page.NextCursor}})
}

func ScannerSupplyChainGetRollout(w http.ResponseWriter, r *http.Request) {
	store, err := scannerReleaseStore()
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	id := chi.URLParam(r, "id")
	rollout, err := store.GetRollout(r.Context(), id)
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	cohorts, err := store.ListRolloutCohorts(r.Context(), id)
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	events, err := store.ListEvents(r.Context(), "rollout", id, 0, 200)
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	workers, err := store.ListWorkerReleaseStatuses(r.Context(), "", time.Now().UTC().Add(-5*time.Minute))
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	var health any
	var syntheticHealth *scannerrollout.SyntheticHealthEvidence
	var realScanHealth *scannerrollout.RealScanHealthEvidence
	var healthObservedAt time.Time
	affectedWorkers := 0
	for index := range cohorts {
		affectedWorkers += cohorts[index].TotalWorkers
		observedAt := cohorts[index].UpdatedAt
		if cohorts[index].HealthObservedAt != nil {
			observedAt = cohorts[index].HealthObservedAt.UTC()
		}
		if health == nil || observedAt.After(healthObservedAt) {
			combined, synthetic, realScans, ok := rolloutHealthFromSummary(
				cohorts[index].HealthSummaryJSON,
			)
			if ok {
				health, syntheticHealth, realScanHealth = combined, synthetic, realScans
				healthObservedAt = observedAt
			}
		}
	}
	if affectedWorkers == 0 {
		for _, worker := range workers {
			if worker.DesiredReleaseID == rollout.ToReleaseID ||
				worker.ObservedReleaseID == rollout.ToReleaseID {
				affectedWorkers++
			}
		}
	}
	var policySnapshot any
	_ = json.Unmarshal([]byte(rollout.PolicySnapshotJSON), &policySnapshot)
	recommendation := "Monitor cohort convergence and required verification evidence."
	if rollout.State == scannerrelease.RolloutFailed {
		recommendation = "Inspect failed cohort evidence and roll back to the last verified release."
	} else if rollout.State == scannerrelease.RolloutPaused {
		recommendation = "Resolve the pause reason, re-evaluate policy, then resume or roll back."
	}
	w.Header().Set("ETag", fmt.Sprintf(`"%d"`, rollout.Version))
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: map[string]any{
		"rollout": rollout, "cohorts": cohorts, "events": events,
		"health": health, "policy_snapshot": policySnapshot,
		"synthetic_health": syntheticHealth, "real_scan_health": realScanHealth,
		"affected_workers": affectedWorkers, "recommendation": recommendation,
	}})
}

func rolloutHealthFromSummary(
	raw string,
) (
	map[string]any,
	*scannerrollout.SyntheticHealthEvidence,
	*scannerrollout.RealScanHealthEvidence,
	bool,
) {
	var persisted struct {
		Health          scannerrollout.CanaryHealth             `json:"health"`
		SyntheticHealth *scannerrollout.SyntheticHealthEvidence `json:"synthetic_health"`
		RealScanHealth  *scannerrollout.RealScanHealthEvidence  `json:"real_scan_health"`
	}
	if !jsonObject(json.RawMessage(raw)) ||
		json.Unmarshal([]byte(raw), &persisted) != nil {
		return nil, nil, nil, false
	}
	// Older cohorts stored CanaryHealth directly. Decode that form without
	// fabricating the newly separated evidence classes.
	if persisted.Health == (scannerrollout.CanaryHealth{}) {
		if json.Unmarshal([]byte(raw), &persisted.Health) != nil {
			return nil, nil, nil, false
		}
	}
	health := map[string]any{
		"samples":                        persisted.Health.Samples,
		"infrastructure_failures":        persisted.Health.InfrastructureFailures,
		"stable_samples":                 persisted.Health.StableSamples,
		"stable_infrastructure_failures": persisted.Health.StableInfrastructureFailures,
		"parser_failures":                persisted.Health.ParserFailures,
		"pull_failures":                  persisted.Health.PullFailures,
		"signature_failures":             persisted.Health.SignatureFailures,
		"manifest_failures":              persisted.Health.ManifestFailures,
		"expected_finding_losses":        persisted.Health.ExpectedFindingLosses,
		"crash_loops":                    persisted.Health.CrashLoops,
		"candidate_p95_duration_ms":      persisted.Health.CandidateP95Duration.Milliseconds(),
		"stable_p95_duration_ms":         persisted.Health.StableP95Duration.Milliseconds(),
	}
	return health, persisted.SyntheticHealth, persisted.RealScanHealth, true
}

func scannerTransitionRollout(w http.ResponseWriter, r *http.Request, action string) {
	key, ok := scannerIdempotencyKey(w, r)
	if !ok {
		return
	}
	expected, ok := scannerExpectedVersion(w, r)
	if !ok {
		return
	}
	var request struct {
		Reason string `json:"reason"`
	}
	if !scannerDecode(w, r, &request) {
		return
	}
	if request.Reason == "" {
		response.WriteError(w, http.StatusUnprocessableEntity, "reason_required", "reason is required")
		return
	}
	store, err := scannerReleaseStore()
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	id := chi.URLParam(r, "id")
	current, err := store.GetRollout(r.Context(), id)
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	if current.Version != expected {
		scannerWriteError(w, scannerrelease.ErrVersionConflict)
		return
	}
	var target scannerrelease.RolloutState
	switch action {
	case "pause":
		target = scannerrelease.RolloutPaused
	case "resume":
		target, err = scannerResumeState(r, store, current)
		if err != nil {
			scannerWriteError(w, err)
			return
		}
	case "rollback":
		target = scannerrelease.RolloutRollingBack
	default:
		panic("unsupported rollout action")
	}
	rollout, err := store.TransitionRollout(r.Context(), id, expected, target, scannerrelease.TransitionCommand{
		Actor: scannerActor(r), Reason: request.Reason, IdempotencyKey: key, PayloadJSON: "{}",
	})
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	base := scannerSupplyChainBase + "/rollouts/" + id
	scannerOperationAccepted(w, scannerCommandResponse{ID: id, State: string(rollout.State), StatusURL: base, EventsURL: base + "/events"})
}

func scannerResumeState(r *http.Request, store scannerrelease.Persistence, rollout *scannerrelease.Rollout) (scannerrelease.RolloutState, error) {
	events, err := store.ListEvents(r.Context(), "rollout", rollout.ID, 0, 200)
	if err != nil {
		return "", err
	}
	for index := len(events) - 1; index >= 0; index-- {
		if events[index].NewState != string(scannerrelease.RolloutPaused) {
			continue
		}
		prior := scannerrelease.RolloutState(events[index].PriorState)
		switch prior {
		case scannerrelease.RolloutPending:
			// Pending cannot be resumed to itself, so preparation is the first
			// safe executable boundary.
			return scannerrelease.RolloutPreparing, nil
		case scannerrelease.RolloutPreparing, scannerrelease.RolloutCanary,
			scannerrelease.RolloutVerifying, scannerrelease.RolloutRollingOut:
			return prior, nil
		default:
			return "", fmt.Errorf("%w: rollout pause event has invalid prior state %q", scannerrelease.ErrInvalidTransition, prior)
		}
	}
	return "", fmt.Errorf("%w: rollout has no persisted pause boundary", scannerrelease.ErrInvalidTransition)
}

func ScannerSupplyChainPauseRollout(w http.ResponseWriter, r *http.Request) {
	scannerTransitionRollout(w, r, "pause")
}

func ScannerSupplyChainResumeRollout(w http.ResponseWriter, r *http.Request) {
	scannerTransitionRollout(w, r, "resume")
}

func ScannerSupplyChainRollbackRollout(w http.ResponseWriter, r *http.Request) {
	scannerTransitionRollout(w, r, "rollback")
}

func ScannerSupplyChainListRegistries(w http.ResponseWriter, r *http.Request) {
	store, err := scannerReleaseStore()
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	enabledOnly := r.URL.Query().Get("include_disabled") != "true"
	targets, err := store.ListRegistryTargets(r.Context(), enabledOnly)
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	views := make([]map[string]any, 0, len(targets))
	for _, target := range targets {
		views = append(views, scannerRegistryTargetView(target))
	}
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: views})
}

func ScannerSupplyChainCreateRegistry(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Name            string                      `json:"name"`
		Type            scannerrelease.RegistryType `json:"type"`
		Host            string                      `json:"host"`
		Namespace       string                      `json:"namespace"`
		SecretReference string                      `json:"secret_reference"`
		TrustPolicyRef  string                      `json:"trust_policy_reference"`
		PlatformPolicy  any                         `json:"platform_policy"`
		Enabled         *bool                       `json:"enabled"`
	}
	if !scannerDecode(w, r, &request) {
		return
	}
	if request.Name == "" || request.Host == "" || !validRegistryType(request.Type) {
		response.WriteError(w, http.StatusUnprocessableEntity, "registry_invalid", "name, host, and a valid registry type are required")
		return
	}
	secretReference, err := scannerRegistrySecretReference(request.SecretReference)
	if err != nil {
		response.WriteError(w, http.StatusUnprocessableEntity, "registry_credential_reference_invalid", err.Error())
		return
	}
	platformPolicy, err := json.Marshal(request.PlatformPolicy)
	if err != nil {
		response.WriteError(w, http.StatusUnprocessableEntity, "registry_invalid", err.Error())
		return
	}
	enabled := true
	if request.Enabled != nil {
		enabled = *request.Enabled
	}
	target := &scannerrelease.RegistryTarget{
		ID: uuid.NewString(), Name: request.Name, Type: request.Type, Host: request.Host,
		Namespace: request.Namespace, SecretReference: secretReference,
		TrustPolicyRef: request.TrustPolicyRef, PlatformPolicyJSON: string(platformPolicy),
		Enabled: enabled, Version: 1, CreatedBy: scannerActor(r),
	}
	store, err := scannerReleaseStore()
	if err == nil {
		err = store.CreateRegistryTarget(r.Context(), target)
	}
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	w.Header().Set("ETag", `"1"`)
	response.WriteJSON(w, http.StatusCreated, response.SuccessResponse{Data: scannerRegistryTargetView(*target)})
}

func scannerRegistrySecretReference(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	const prefix = "secret:"
	if len(value) > len(prefix)+36 || !strings.HasPrefix(value, prefix) {
		return "", errors.New("credential reference must use secret:<uuid>; credential material is not accepted")
	}
	identifier, err := uuid.Parse(strings.TrimPrefix(value, prefix))
	if err != nil {
		return "", errors.New("credential reference must use secret:<uuid>; credential material is not accepted")
	}
	return prefix + identifier.String(), nil
}

func scannerRegistryTargetView(target scannerrelease.RegistryTarget) map[string]any {
	encoded, _ := json.Marshal(target)
	view := make(map[string]any)
	_ = json.Unmarshal(encoded, &view)
	delete(view, "secret_reference")
	view["credential_reference_configured"] = target.SecretReference != ""
	if target.SecretReference != "" {
		view["credential_reference_kind"] = "wolf_secret"
	}
	return view
}

func validRegistryType(value scannerrelease.RegistryType) bool {
	switch value {
	case scannerrelease.RegistryManaged, scannerrelease.RegistryMirror,
		scannerrelease.RegistryPrivate, scannerrelease.RegistryAirGap:
		return true
	default:
		return false
	}
}

func ScannerSupplyChainGetRegistry(w http.ResponseWriter, r *http.Request) {
	store, err := scannerReleaseStore()
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	target, err := store.GetRegistryTarget(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	w.Header().Set("ETag", fmt.Sprintf(`"%d"`, target.Version))
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: scannerRegistryTargetView(*target)})
}

func ScannerSupplyChainPatchRegistry(w http.ResponseWriter, r *http.Request) {
	expected, ok := scannerExpectedVersion(w, r)
	if !ok {
		return
	}
	var request struct {
		Name            *string                      `json:"name"`
		Type            *scannerrelease.RegistryType `json:"type"`
		Host            *string                      `json:"host"`
		Namespace       *string                      `json:"namespace"`
		SecretReference *string                      `json:"secret_reference"`
		TrustPolicyRef  *string                      `json:"trust_policy_reference"`
		PlatformPolicy  json.RawMessage              `json:"platform_policy"`
		Enabled         *bool                        `json:"enabled"`
	}
	if !scannerDecode(w, r, &request) {
		return
	}
	store, err := scannerReleaseStore()
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	target, err := store.GetRegistryTarget(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	if target.Version != expected {
		scannerWriteError(w, scannerrelease.ErrVersionConflict)
		return
	}
	if request.Name != nil {
		target.Name = *request.Name
	}
	if request.Type != nil {
		if !validRegistryType(*request.Type) {
			response.WriteError(w, http.StatusUnprocessableEntity, "registry_invalid", "invalid registry type")
			return
		}
		target.Type = *request.Type
	}
	if request.Host != nil {
		target.Host = *request.Host
	}
	if request.Namespace != nil {
		target.Namespace = *request.Namespace
	}
	if request.SecretReference != nil {
		reference, validationErr := scannerRegistrySecretReference(*request.SecretReference)
		if validationErr != nil {
			response.WriteError(w, http.StatusUnprocessableEntity, "registry_credential_reference_invalid", validationErr.Error())
			return
		}
		target.SecretReference = reference
	}
	if request.TrustPolicyRef != nil {
		target.TrustPolicyRef = *request.TrustPolicyRef
	}
	if len(request.PlatformPolicy) != 0 {
		if !jsonObject(request.PlatformPolicy) {
			response.WriteError(w, http.StatusUnprocessableEntity, "registry_invalid", "platform_policy must be an object")
			return
		}
		target.PlatformPolicyJSON = string(request.PlatformPolicy)
	}
	if request.Enabled != nil {
		target.Enabled = *request.Enabled
	}
	if target.Name == "" || target.Host == "" {
		response.WriteError(w, http.StatusUnprocessableEntity, "registry_invalid", "name and host are required")
		return
	}
	if err := store.UpdateRegistryTarget(r.Context(), target, expected); err != nil {
		scannerWriteError(w, err)
		return
	}
	w.Header().Set("ETag", fmt.Sprintf(`"%d"`, target.Version))
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: scannerRegistryTargetView(*target)})
}

// ScannerSupplyChainDeleteRegistry uses a versioned soft-delete so release
// evidence never loses the registry identity it references.
func ScannerSupplyChainDeleteRegistry(w http.ResponseWriter, r *http.Request) {
	expected, ok := scannerExpectedVersion(w, r)
	if !ok {
		return
	}
	store, err := scannerReleaseStore()
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	target, err := store.GetRegistryTarget(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	target.Enabled = false
	if err := store.UpdateRegistryTarget(r.Context(), target, expected); err != nil {
		scannerWriteError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func ScannerSupplyChainCheckRegistry(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	store, err := scannerReleaseStore()
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	target, err := store.GetRegistryTarget(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	client, registryHost, err := scannerRegistryClient(r.Context(), target)
	if err != nil {
		_ = store.UpdateRegistryObservation(r.Context(), target.ID, scannerrelease.RegistryObservation{
			HealthStatus: "failed", CheckedAt: time.Now().UTC(),
			Error:     err.Error(),
			LatencyMS: time.Since(started).Milliseconds(),
		})
		response.WriteError(w, http.StatusServiceUnavailable, "registry_credentials_unavailable", err.Error())
		return
	}
	if err := client.Check(r.Context(), registryHost); err != nil {
		checkedAt := time.Now().UTC()
		_ = store.UpdateRegistryObservation(r.Context(), target.ID, scannerrelease.RegistryObservation{
			HealthStatus: "failed", CheckedAt: checkedAt, Error: err.Error(),
			LatencyMS: time.Since(started).Milliseconds(),
		})
		response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: map[string]any{
			"registry_id": target.ID, "reachable": false, "error": err.Error(),
			"checked_at": checkedAt, "latency_ms": time.Since(started).Milliseconds(),
		}})
		return
	}
	checkedAt := time.Now().UTC()
	if err := store.UpdateRegistryObservation(r.Context(), target.ID, scannerrelease.RegistryObservation{
		HealthStatus: "healthy", CheckedAt: checkedAt,
		LatencyMS: time.Since(started).Milliseconds(),
	}); err != nil {
		scannerWriteError(w, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: map[string]any{
		"registry_id": target.ID, "reachable": true,
		"checked_at": checkedAt, "latency_ms": time.Since(started).Milliseconds(),
	}})
}

func ScannerSupplyChainReconcileRegistry(w http.ResponseWriter, r *http.Request) {
	var request struct {
		ReleaseID string `json:"release_id"`
	}
	if !scannerDecode(w, r, &request) {
		return
	}
	if request.ReleaseID == "" {
		response.WriteError(w, http.StatusUnprocessableEntity, "release_required", "release_id is required")
		return
	}
	store, err := scannerReleaseStore()
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	target, err := store.GetRegistryTarget(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	client, registryHost, err := scannerRegistryClient(r.Context(), target)
	if err != nil {
		_ = store.UpdateRegistryObservation(r.Context(), target.ID, scannerrelease.RegistryObservation{
			HealthStatus: "failed", CheckedAt: time.Now().UTC(),
			Error: err.Error(),
		})
		response.WriteError(w, http.StatusServiceUnavailable, "registry_credentials_unavailable", err.Error())
		return
	}
	inventory, err := store.GetReleaseInventory(r.Context(), request.ReleaseID)
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	results := make([]map[string]any, 0, len(inventory.Images))
	matched := true
	for _, image := range inventory.Images {
		repository := strings.Trim(image.Repository, "/")
		if target.Namespace != "" && !strings.HasPrefix(repository, strings.Trim(target.Namespace, "/")+"/") {
			repository = strings.Trim(target.Namespace, "/") + "/" + repository
		}
		reference, parseErr := scannerregistry.ParseReference(registryHost + "/" + repository + "@" + image.Digest)
		if parseErr != nil {
			matched = false
			results = append(results, map[string]any{"image_key": image.ImageKey, "matched": false, "error": parseErr.Error()})
			continue
		}
		manifest, fetchErr := client.FetchManifest(r.Context(), reference)
		if fetchErr != nil {
			matched = false
			results = append(results, map[string]any{"image_key": image.ImageKey, "matched": false, "error": fetchErr.Error()})
			continue
		}
		results = append(results, map[string]any{"image_key": image.ImageKey, "matched": true, "digest": manifest.Digest})
	}
	checkedAt := time.Now().UTC()
	detailJSON, _ := json.Marshal(map[string]any{
		"release_id": request.ReleaseID, "images": results,
	})
	health := "healthy"
	parity := "matched"
	if !matched {
		health = "degraded"
		parity = "mismatched"
	}
	if err := store.UpdateRegistryObservation(r.Context(), target.ID, scannerrelease.RegistryObservation{
		HealthStatus: health, CheckedAt: checkedAt, DigestParityStatus: parity,
		DetailJSON: string(detailJSON),
	}); err != nil {
		scannerWriteError(w, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: map[string]any{
		"registry_id": target.ID, "release_id": request.ReleaseID, "matched": matched,
		"images": results, "checked_at": checkedAt,
	}})
}

// ScannerSupplyChainCreateRegistryJob is the durable, UI-ready reconciliation
// path. The legacy synchronous /reconcile route remains unchanged for existing
// UI and automation compatibility.
func ScannerSupplyChainCreateRegistryJob(w http.ResponseWriter, r *http.Request) {
	key, ok := scannerIdempotencyKey(w, r)
	if !ok {
		return
	}
	var request struct {
		Kind             scannerrelease.RegistryJobKind      `json:"kind"`
		ReleaseID        string                              `json:"release_id"`
		SourceRegistryID string                              `json:"source_registry_id"`
		ReSignPolicy     scannerrelease.RegistryReSignPolicy `json:"re_sign_policy"`
		Reason           string                              `json:"reason"`
		MaxAttempts      int                                 `json:"max_attempts"`
	}
	if !scannerDecode(w, r, &request) {
		return
	}
	if request.Kind == "" {
		request.Kind = scannerrelease.RegistryJobReconcile
	}
	if strings.TrimSpace(request.Reason) == "" {
		response.WriteError(w, http.StatusUnprocessableEntity, "reason_required", "reason is required")
		return
	}
	store, err := scannerReleaseStore()
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	targetID := chi.URLParam(r, "id")
	if _, err := store.GetRegistryTarget(r.Context(), targetID); err != nil {
		scannerWriteError(w, err)
		return
	}
	if request.SourceRegistryID != "" {
		if _, err := store.GetRegistryTarget(r.Context(), request.SourceRegistryID); err != nil {
			scannerWriteError(w, err)
			return
		}
	}
	if request.ReleaseID != "" {
		if _, err := store.GetRelease(r.Context(), request.ReleaseID); err != nil {
			scannerWriteError(w, err)
			return
		}
	}
	job := &scannerrelease.RegistryJob{
		RegistryTargetID: targetID, SourceRegistryTargetID: request.SourceRegistryID,
		ReleaseID: request.ReleaseID, Kind: request.Kind,
		ReSignPolicy: request.ReSignPolicy, State: scannerrelease.RegistryJobQueued,
		Actor: scannerActor(r), Reason: request.Reason, IdempotencyKey: key,
		MaxAttempts: request.MaxAttempts,
	}
	if err := store.CreateRegistryJob(r.Context(), job, scannerrelease.TransitionCommand{
		Actor: job.Actor, Reason: job.Reason, IdempotencyKey: key,
		PayloadJSON: "{}",
	}); err != nil {
		scannerWriteError(w, err)
		return
	}
	base := scannerSupplyChainBase + "/registry-jobs/" + job.ID
	scannerOperationAccepted(w, scannerCommandResponse{
		ID: job.ID, State: string(job.State), StatusURL: base,
		EventsURL: base + "/events",
	})
}

func ScannerSupplyChainCreateRegistryCleanupJob(w http.ResponseWriter, r *http.Request) {
	key, ok := scannerIdempotencyKey(w, r)
	if !ok {
		return
	}
	var request struct {
		Reason      string `json:"reason"`
		MaxAttempts int    `json:"max_attempts"`
	}
	if !scannerDecode(w, r, &request) {
		return
	}
	if strings.TrimSpace(request.Reason) == "" {
		response.WriteError(w, http.StatusUnprocessableEntity, "reason_required", "reason is required")
		return
	}
	store, err := scannerReleaseStore()
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	targetID := chi.URLParam(r, "id")
	if _, err := store.GetRegistryTarget(r.Context(), targetID); err != nil {
		scannerWriteError(w, err)
		return
	}
	job := &scannerrelease.RegistryJob{
		RegistryTargetID: targetID, Kind: scannerrelease.RegistryJobCleanup,
		ReSignPolicy: scannerrelease.RegistryReSignForbidden,
		State:        scannerrelease.RegistryJobQueued, Actor: scannerActor(r),
		Reason: request.Reason, IdempotencyKey: key, MaxAttempts: request.MaxAttempts,
	}
	if err := store.CreateRegistryJob(r.Context(), job, scannerrelease.TransitionCommand{
		Actor: job.Actor, Reason: job.Reason, IdempotencyKey: key, PayloadJSON: "{}",
	}); err != nil {
		scannerWriteError(w, err)
		return
	}
	base := scannerSupplyChainBase + "/registry-jobs/" + job.ID
	scannerOperationAccepted(w, scannerCommandResponse{
		ID: job.ID, State: string(job.State), StatusURL: base,
		EventsURL: base + "/events",
	})
}

func ScannerSupplyChainListRegistryJobs(w http.ResponseWriter, r *http.Request) {
	store, err := scannerReleaseStore()
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	jobs, err := store.ListRegistryJobs(r.Context(), scannerrelease.RegistryJobFilter{
		RegistryTargetID: strings.TrimSpace(r.URL.Query().Get("registry_target_id")),
		ReleaseID:        strings.TrimSpace(r.URL.Query().Get("release_id")),
		State:            scannerrelease.RegistryJobState(r.URL.Query().Get("state")),
		Kind:             scannerrelease.RegistryJobKind(r.URL.Query().Get("kind")),
	}, limit)
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: jobs})
}

func ScannerSupplyChainGetRegistryJob(w http.ResponseWriter, r *http.Request) {
	store, err := scannerReleaseStore()
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	job, err := store.GetRegistryJob(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	observations, err := store.ListRegistryImageObservations(r.Context(), job.ID)
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	w.Header().Set("ETag", fmt.Sprintf(`"%d"`, job.Version))
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: map[string]any{
		"job": job, "images": observations,
		"events_url": scannerSupplyChainBase + "/registry-jobs/" + job.ID + "/events",
	}})
}

func ScannerSupplyChainRetryRegistryJob(w http.ResponseWriter, r *http.Request) {
	key, ok := scannerIdempotencyKey(w, r)
	if !ok {
		return
	}
	expected, ok := scannerExpectedVersion(w, r)
	if !ok {
		return
	}
	var request struct {
		Reason string `json:"reason"`
	}
	if !scannerDecode(w, r, &request) {
		return
	}
	if strings.TrimSpace(request.Reason) == "" {
		response.WriteError(w, http.StatusUnprocessableEntity, "reason_required", "reason is required")
		return
	}
	store, err := scannerReleaseStore()
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	job, err := store.RetryDeadLetterRegistryJob(
		r.Context(), chi.URLParam(r, "id"), expected,
		scannerrelease.TransitionCommand{
			Actor: scannerActor(r), Reason: request.Reason,
			IdempotencyKey: key, PayloadJSON: "{}",
		}, time.Now().UTC(),
	)
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	base := scannerSupplyChainBase + "/registry-jobs/" + job.ID
	scannerOperationAccepted(w, scannerCommandResponse{
		ID: job.ID, State: string(job.State), StatusURL: base,
		EventsURL: base + "/events",
	})
}

func ScannerSupplyChainListRegistryQuarantine(w http.ResponseWriter, r *http.Request) {
	store, err := scannerReleaseStore()
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	objects, err := store.ListRegistryQuarantineObjects(
		r.Context(), strings.TrimSpace(r.URL.Query().Get("registry_target_id")),
		strings.TrimSpace(r.URL.Query().Get("state")), limit,
	)
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: objects})
}

func scannerRegistryClient(
	ctx context.Context,
	target *scannerrelease.RegistryTarget,
) (scannerregistry.Client, string, error) {
	raw := strings.TrimSpace(target.Host)
	if raw == "" {
		return scannerregistry.Client{}, "", errors.New("registry host is empty")
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") ||
		parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" ||
		parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return scannerregistry.Client{}, "", errors.New("registry host must be an HTTP(S) origin without credentials or a path")
	}
	registryHost := parsed.Host
	client := scannerregistry.Client{
		Endpoints: map[string]scannerregistry.Endpoint{
			registryHost: {BaseURL: parsed.Scheme + "://" + registryHost},
		},
	}
	tokenHosts, err := scannerRegistryTokenHosts(target, registryHost)
	if err != nil {
		return scannerregistry.Client{}, "", err
	}
	client.TokenHosts = map[string][]string{registryHost: tokenHosts}
	if target.SecretReference == "" {
		return client, registryHost, nil
	}
	validatedReference, err := scannerRegistrySecretReference(target.SecretReference)
	if err != nil {
		return scannerregistry.Client{}, "", errors.New("registry credential reference is invalid")
	}
	if DefaultHandler == nil || DefaultHandler.Store == nil {
		return scannerregistry.Client{}, "", errors.New("registry credential store is unavailable")
	}
	secretID := strings.TrimPrefix(validatedReference, "secret:")
	credential, err := DefaultHandler.Store.GetSecretByID(ctx, secretID)
	if err != nil {
		return scannerregistry.Client{}, "", errors.New("registry credential reference was not found")
	}
	if !scannerSecretAllowsHost(credential, registryHost) {
		return scannerregistry.Client{}, "", errors.New("registry credential is not authorized for this host")
	}
	value, err := secrets.Decrypt(credential.EncryptedValue)
	if err != nil {
		return scannerregistry.Client{}, "", errors.New("registry credential could not be decrypted")
	}
	authorization, err := scannerRegistryAuthorization(credential, value, registryHost)
	if err != nil {
		return scannerregistry.Client{}, "", err
	}
	client.Credentials = scannerregistry.CredentialProviderFunc(
		func(context.Context, string) (string, error) { return authorization, nil },
	)
	return client, registryHost, nil
}

func scannerRegistryTokenHosts(
	target *scannerrelease.RegistryTarget,
	registryHost string,
) ([]string, error) {
	var policy struct {
		TokenHosts []string `json:"token_hosts"`
	}
	if strings.TrimSpace(target.PlatformPolicyJSON) != "" {
		if err := json.Unmarshal([]byte(target.PlatformPolicyJSON), &policy); err != nil {
			return nil, errors.New("registry platform policy is invalid")
		}
	}
	if strings.EqualFold(registryHost, "registry-1.docker.io") ||
		strings.EqualFold(registryHost, "docker.io") ||
		strings.EqualFold(registryHost, "index.docker.io") {
		policy.TokenHosts = append(policy.TokenHosts, "auth.docker.io")
	}
	seen := make(map[string]struct{}, len(policy.TokenHosts))
	out := make([]string, 0, len(policy.TokenHosts))
	for _, value := range policy.TokenHosts {
		value = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(value), "."))
		if value == "" || strings.ContainsAny(value, "/?#@") {
			return nil, errors.New("registry token host must be a hostname with an optional port")
		}
		if parsed, err := url.Parse("https://" + value); err != nil ||
			parsed.Host != value || parsed.Hostname() == "" {
			return nil, errors.New("registry token host is invalid")
		}
		if _, duplicate := seen[value]; duplicate {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out, nil
}

func scannerSecretAllowsHost(credential *models.Secret, host string) bool {
	var allowed []string
	if json.Unmarshal([]byte(credential.AllowedHosts), &allowed) == nil {
		host = strings.ToLower(strings.TrimSuffix(host, "."))
		for _, value := range allowed {
			value = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(value), "."))
			if host == value ||
				(strings.HasPrefix(value, "*.") &&
					strings.HasSuffix(host, strings.TrimPrefix(value, "*")) &&
					host != strings.TrimPrefix(value, "*.")) {
				return true
			}
		}
	}
	if credential.KeyType == models.KeyTypeDockerHubToken {
		switch strings.ToLower(host) {
		case "docker.io", "registry-1.docker.io", "index.docker.io":
			return true
		}
	}
	return false
}

func scannerRegistryAuthorization(
	credential *models.Secret,
	secretValue, host string,
) (string, error) {
	if strings.ContainsAny(secretValue, "\r\n") {
		return "", errors.New("registry credential contains an invalid newline")
	}
	var username string
	switch credential.KeyType {
	case models.KeyTypeGitHTTPS:
		var metadata struct {
			Username string `json:"username"`
		}
		_ = json.Unmarshal([]byte(credential.MetadataJSON), &metadata)
		username = strings.TrimSpace(metadata.Username)
	case models.KeyTypeDockerHubToken:
		username = strings.TrimSpace(credential.KeyName)
	default:
		return "", fmt.Errorf("credential type %q is not supported for OCI registry basic authentication", credential.KeyType)
	}
	if username == "" || strings.ContainsAny(username, ":\r\n") {
		return "", fmt.Errorf("registry credential for %s has no valid username", host)
	}
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(username+":"+secretValue)), nil
}

func ScannerSupplyChainAudit(w http.ResponseWriter, r *http.Request) {
	store, err := scannerReleaseStore()
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	page, err := store.ListAllEvents(r.Context(), scannerEventFilter(r), scannerPage(r))
	if err != nil {
		response.WriteError(w, http.StatusBadRequest, "invalid_cursor", err.Error())
		return
	}
	response.WriteJSON(w, http.StatusOK, scannerCursorResponse{
		Data: page.Items, Meta: scannerCursorMeta{NextCursor: page.NextCursor},
	})
}

func ScannerSupplyChainAuditExport(w http.ResponseWriter, r *http.Request) {
	format := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("format")))
	if format != "" && format != "jsonl" && format != "ndjson" {
		response.WriteError(w, http.StatusBadRequest, "unsupported_export_format", "format must be jsonl or ndjson")
		return
	}
	store, err := scannerReleaseStore()
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Content-Disposition", `attachment; filename="scanner-release-audit.jsonl"`)
	w.Header().Set("Cache-Control", "no-store")
	encoder := json.NewEncoder(w)
	filter := scannerEventFilter(r)
	cursor := ""
	for {
		if err := r.Context().Err(); err != nil {
			return
		}
		page, listErr := store.ListAllEvents(r.Context(), filter, scannerrelease.PageRequest{
			Limit: 200, Cursor: cursor,
		})
		if listErr != nil {
			// Headers may already be committed; abort the stream without
			// writing a non-JSON error into the export.
			return
		}
		for _, event := range page.Items {
			if encodeErr := encoder.Encode(event); encodeErr != nil {
				return
			}
		}
		if page.NextCursor == "" {
			return
		}
		cursor = page.NextCursor
	}
}

func scannerEventFilter(r *http.Request) scannerrelease.EventFilter {
	return scannerrelease.EventFilter{
		AggregateType: strings.TrimSpace(r.URL.Query().Get("aggregate_type")),
		EventType:     strings.TrimSpace(r.URL.Query().Get("event_type")),
		Actor:         strings.TrimSpace(r.URL.Query().Get("actor")),
		TraceID:       strings.TrimSpace(r.URL.Query().Get("trace_id")),
		OperationID:   strings.TrimSpace(r.URL.Query().Get("operation_id")),
	}
}

func ScannerSupplyChainDiscoveryEvents(w http.ResponseWriter, r *http.Request) {
	scannerSupplyChainEvents(w, r, "discovery")
}

func ScannerSupplyChainCandidateEvents(w http.ResponseWriter, r *http.Request) {
	scannerSupplyChainEvents(w, r, "candidate")
}

func ScannerSupplyChainReleaseEvents(w http.ResponseWriter, r *http.Request) {
	scannerSupplyChainEvents(w, r, "release")
}

func ScannerSupplyChainRolloutEvents(w http.ResponseWriter, r *http.Request) {
	scannerSupplyChainEvents(w, r, "rollout")
}

func ScannerSupplyChainRegistryJobEvents(w http.ResponseWriter, r *http.Request) {
	scannerSupplyChainEvents(w, r, "registry_job")
}

func scannerSupplyChainEvents(w http.ResponseWriter, r *http.Request, aggregateType string) {
	if !strings.Contains(strings.ToLower(r.Header.Get("Accept")), "text/event-stream") {
		response.WriteError(w, http.StatusNotAcceptable, "event_stream_required", "set Accept: text/event-stream to consume durable scanner events")
		return
	}
	store, err := scannerReleaseStore()
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	after := int64(0)
	if value := strings.TrimSpace(r.Header.Get("Last-Event-ID")); value != "" {
		after, err = strconv.ParseInt(value, 10, 64)
		if err != nil || after < 0 {
			response.WriteError(w, http.StatusBadRequest, "invalid_last_event_id", "Last-Event-ID must be a non-negative event sequence")
			return
		}
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher, ok := w.(http.Flusher)
	if !ok {
		response.WriteError(w, http.StatusInternalServerError, "streaming_unsupported", "streaming is unavailable")
		return
	}
	ticker := time.NewTicker(time.Second)
	heartbeat := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	defer heartbeat.Stop()
	terminal := false
	writePending := func() error {
		events, listErr := store.ListEvents(r.Context(), aggregateType, chi.URLParam(r, "id"), after, 200)
		if listErr != nil {
			return listErr
		}
		for _, event := range events {
			encoded, marshalErr := json.Marshal(event)
			if marshalErr != nil {
				return marshalErr
			}
			if _, writeErr := fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", event.Sequence, event.EventType, encoded); writeErr != nil {
				return writeErr
			}
			after = event.Sequence
			terminal = scannerEventTerminal(aggregateType, event.NewState)
		}
		if len(events) != 0 {
			flusher.Flush()
		}
		return nil
	}
	if err := writePending(); err != nil {
		return
	}
	if terminal {
		return
	}
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			if err := writePending(); err != nil || terminal {
				return
			}
		case now := <-heartbeat.C:
			if _, err := fmt.Fprintf(w, "event: heartbeat\ndata: {\"at\":%q}\n\n", now.UTC().Format(time.RFC3339)); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func scannerEventTerminal(aggregateType, state string) bool {
	switch aggregateType {
	case "discovery":
		return scannerrelease.IsTerminalDiscoveryState(scannerrelease.DiscoveryState(state))
	case "candidate":
		return scannerrelease.IsTerminalCandidateState(scannerrelease.CandidateState(state))
	case "rollout":
		return scannerrelease.IsTerminalRolloutState(scannerrelease.RolloutState(state))
	case "release":
		return state == string(scannerrelease.ReleaseDeprecated) || state == string(scannerrelease.ReleaseRevoked)
	case "registry_job":
		return state == string(scannerrelease.RegistryJobCompleted) ||
			state == string(scannerrelease.RegistryJobDeadLetter) ||
			state == string(scannerrelease.RegistryJobCancelled)
	default:
		return false
	}
}
