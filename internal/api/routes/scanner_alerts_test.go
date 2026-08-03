package routes

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/scannerrelease"
)

func TestScannerAlertRoutesDefaultToOpenAndValidateFilters(t *testing.T) {
	store := withScannerSupplyChainStore(t)
	repository := store.ScannerReleases()
	ctx := t.Context()
	policy := &scannerrelease.Policy{
		ID: uuid.NewString(), Scope: "global", Revision: 1, Enabled: true,
		ScheduleJSON: `{}`, RulesJSON: `{}`, CreatedBy: "test",
	}
	if err := repository.CreatePolicy(ctx, policy); err != nil {
		t.Fatalf("CreatePolicy: %v", err)
	}
	target := &scannerrelease.RegistryTarget{
		ID: uuid.NewString(), Name: "mirror", Type: scannerrelease.RegistryMirror,
		Host: "mirror.example.test", Namespace: "wolf", Enabled: true,
		PlatformPolicyJSON: `{}`, CreatedBy: "test",
	}
	if err := repository.CreateRegistryTarget(ctx, target); err != nil {
		t.Fatalf("CreateRegistryTarget: %v", err)
	}
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	if err := repository.UpdateRegistryObservation(
		ctx, target.ID, scannerrelease.RegistryObservation{
			CheckedAt: now, HealthStatus: "degraded",
			DigestParityStatus: "mismatched", DetailJSON: `{}`,
		},
	); err != nil {
		t.Fatalf("UpdateRegistryObservation: %v", err)
	}
	if _, err := repository.EvaluateAlerts(
		ctx, scannerrelease.AlertEvaluationRequest{
			PolicyID: policy.ID, PolicyRevision: 1, MirrorDrift: true,
		}, now,
	); err != nil {
		t.Fatalf("EvaluateAlerts: %v", err)
	}

	list := scannerRouteRequest(
		t, ScannerSupplyChainListAlerts, http.MethodGet,
		"/alerts?severity=critical", "", nil, nil,
	)
	if list.Code != http.StatusOK ||
		!strings.Contains(list.Body.String(), `"mirror_drift"`) ||
		!strings.Contains(list.Body.String(), `"evidence":{`) {
		t.Fatalf("list alerts = %d body=%s", list.Code, list.Body)
	}
	page, err := repository.ListAlerts(
		ctx, scannerrelease.AlertFilter{State: scannerrelease.AlertOpen},
		scannerrelease.PageRequest{Limit: 1},
	)
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("ListAlerts fixture = %#v err=%v", page, err)
	}
	show := scannerRouteRequest(
		t, ScannerSupplyChainGetAlert, http.MethodGet,
		"/alerts/"+page.Items[0].ID, "", nil,
		map[string]string{"id": page.Items[0].ID},
	)
	if show.Code != http.StatusOK || show.Header().Get("ETag") != `"1"` {
		t.Fatalf("show alert = %d headers=%v body=%s", show.Code, show.Header(), show.Body)
	}
	invalid := scannerRouteRequest(
		t, ScannerSupplyChainListAlerts, http.MethodGet,
		"/alerts?state=acknowledged", "", nil, nil,
	)
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid filter status=%d body=%s", invalid.Code, invalid.Body)
	}
}
