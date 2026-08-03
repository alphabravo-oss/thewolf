package scannerpolicy

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestDefaultPolicyRequiresHumanApproval(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 30, 15, 0, 0, 0, time.UTC)
	policy := Default()
	candidate := passingCandidate(policy, now)
	decision, err := Evaluate(candidate, policy, now)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Outcome != OutcomeAwaitingApproval || decision.AutoPromotion {
		t.Fatalf("decision = %#v", decision)
	}

	candidate.Approvals = []Approval{
		{
			ActorID:              candidate.CreatorID,
			LockDigest:           candidate.LockDigest,
			PolicyDecisionDigest: decision.PolicyDecisionDigest,
			CreatedAt:            now,
		},
	}
	decision, err = Evaluate(candidate, policy, now)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Outcome != OutcomeAwaitingApproval {
		t.Fatalf("creator self-approval must not satisfy separation: %#v", decision)
	}

	candidate.Approvals = append(candidate.Approvals,
		Approval{
			ActorID:              "approver-1",
			LockDigest:           candidate.LockDigest,
			PolicyDecisionDigest: decision.PolicyDecisionDigest,
			CreatedAt:            now,
		},
	)
	decision, err = Evaluate(candidate, policy, now)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Outcome != OutcomeApproved || decision.ValidApprovals != 1 {
		t.Fatalf("approved decision = %#v", decision)
	}
}

func TestAlertPolicyDefaultsAreConservativeAndThresholdsAreValidated(t *testing.T) {
	t.Parallel()
	policy := Default()
	if policy.Alerts.MissedDiscovery.Enabled ||
		policy.Alerts.StaleStableRelease.Enabled ||
		policy.Alerts.QueueBacklog.Enabled ||
		policy.Alerts.LeaseChurn.Enabled ||
		policy.Alerts.RepeatedGateFailure.Enabled ||
		policy.Alerts.MirrorDrift.Enabled ||
		policy.Alerts.RolloutFailure.Enabled ||
		policy.Alerts.SignatureHealth.Enabled {
		t.Fatal("default alert rules must be disabled")
	}
	if err := policy.Normalize(); err != nil {
		t.Fatalf("default alert thresholds: %v", err)
	}
	if policy.Alerts.MissedDiscovery.After != 72*time.Hour ||
		policy.Alerts.QueueBacklog.MaxAge != time.Hour ||
		policy.Alerts.LeaseChurn.Window != 15*time.Minute {
		t.Fatalf("normalized alert thresholds = %#v", policy.Alerts)
	}

	tests := []struct {
		name   string
		mutate func(*Policy)
	}{
		{
			name: "missing duration",
			mutate: func(policy *Policy) {
				policy.Alerts.MissedDiscovery.Enabled = true
				policy.Alerts.MissedDiscovery.AfterText = ""
			},
		},
		{
			name: "short window",
			mutate: func(policy *Policy) {
				policy.Alerts.LeaseChurn.Enabled = true
				policy.Alerts.LeaseChurn.WindowText = "30s"
			},
		},
		{
			name: "missing queue threshold",
			mutate: func(policy *Policy) {
				policy.Alerts.QueueBacklog.Enabled = true
				policy.Alerts.QueueBacklog.MaxDepth = 0
				policy.Alerts.QueueBacklog.MaxAgeText = ""
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			current := Default()
			test.mutate(&current)
			if err := current.Normalize(); err == nil {
				t.Fatal("invalid enabled alert threshold was accepted")
			}
		})
	}
}

func TestApprovalBecomesStaleWhenEvidenceChanges(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 30, 15, 0, 0, 0, time.UTC)
	policy := Default()
	candidate := passingCandidate(policy, now)
	before, err := Evaluate(candidate, policy, now)
	if err != nil {
		t.Fatal(err)
	}
	candidate.Approvals = []Approval{{
		ActorID:              "approver-1",
		LockDigest:           candidate.LockDigest,
		PolicyDecisionDigest: before.PolicyDecisionDigest,
		CreatedAt:            now,
	}}
	candidate.Gates[0].EvidenceDigest = "sha256:" + strings.Repeat("f", 64)
	after, err := Evaluate(candidate, policy, now)
	if err != nil {
		t.Fatal(err)
	}
	if after.PolicyDecisionDigest == before.PolicyDecisionDigest {
		t.Fatal("evidence change did not change policy decision binding")
	}
	if after.Outcome != OutcomeAwaitingApproval || after.ValidApprovals != 0 {
		t.Fatalf("stale approval was accepted: %#v", after)
	}
}

func TestPolicyGatedAutoPromotionOnlyAllowsExplicitLowRiskChanges(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 30, 15, 0, 0, 0, time.UTC)
	policy := Default()
	policy.ApprovalMode = ApprovalPolicyGated
	candidate := passingCandidate(policy, now)
	candidate.Changes = []Change{{Component: "semgrep", Kind: ChangePatch}}
	decision, err := Evaluate(candidate, policy, now)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Outcome != OutcomeAutoApproved || !decision.AutoPromotion {
		t.Fatalf("low-risk patch decision = %#v", decision)
	}

	candidate.Changes[0].Kind = ChangeMajor
	decision, err = Evaluate(candidate, policy, now)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Outcome != OutcomeAwaitingApproval || decision.AutoPromotion {
		t.Fatalf("major change must await approval: %#v", decision)
	}

	candidate.Changes[0].Kind = ChangePatch
	candidate.Risk = RiskHigh
	decision, err = Evaluate(candidate, policy, now)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Outcome != OutcomeAwaitingApproval || decision.AutoPromotion {
		t.Fatalf("high-risk change must await approval: %#v", decision)
	}
}

func TestNonBypassableGatesCannotBeDisabledOrExcepted(t *testing.T) {
	t.Parallel()
	policy := Default()
	policy.RequiredGates = without(policy.RequiredGates, "signature")
	if err := policy.Normalize(); err == nil || !strings.Contains(err.Error(), "must be required") {
		t.Fatalf("missing signature validation error = %v", err)
	}

	policy = Default()
	policy.AllowExceptions["signature"] = true
	if err := policy.Normalize(); err == nil || !strings.Contains(err.Error(), "cannot allow exceptions") {
		t.Fatalf("signature exception validation error = %v", err)
	}

	now := time.Date(2026, 7, 30, 15, 0, 0, 0, time.UTC)
	policy = Default()
	candidate := passingCandidate(policy, now)
	for i := range candidate.Gates {
		if candidate.Gates[i].Name == "signature" {
			candidate.Gates[i].Status = GateExcepted
		}
	}
	candidate.Exceptions = []Exception{validException("signature", now)}
	decision, err := Evaluate(candidate, policy, now)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Outcome != OutcomeBlocked || !containsText(decision.BlockingReasons, "non-bypassable") {
		t.Fatalf("excepted signature decision = %#v", decision)
	}
}

func TestExpiringApprovedVulnerabilityExceptionNeedsHumanApproval(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 30, 15, 0, 0, 0, time.UTC)
	policy := Default()
	policy.ApprovalMode = ApprovalPolicyGated
	candidate := passingCandidate(policy, now)
	for i := range candidate.Gates {
		if candidate.Gates[i].Name == "vulnerability" {
			candidate.Gates[i].Status = GateExcepted
		}
	}
	candidate.Exceptions = []Exception{validException("vulnerability", now)}
	decision, err := Evaluate(candidate, policy, now)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Outcome != OutcomeAwaitingApproval || decision.AutoPromotion {
		t.Fatalf("exception-bearing candidate must await approval: %#v", decision)
	}

	candidate.Exceptions[0].ExpiresAt = now
	decision, err = Evaluate(candidate, policy, now)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Outcome != OutcomeBlocked || !containsText(decision.BlockingReasons, "expired") {
		t.Fatalf("expired exception decision = %#v", decision)
	}
}

func TestDecisionDigestIsStableAcrossReasonOrdering(t *testing.T) {
	t.Parallel()
	first := Decision{
		CandidateID:     "candidate-1",
		LockDigest:      "sha256:abc",
		PolicyRevision:  1,
		Outcome:         OutcomeBlocked,
		BlockingReasons: []string{"z", "a"},
		Advisories:      []string{"two", "one"},
	}
	second := first
	second.BlockingReasons = []string{"a", "z"}
	second.Advisories = []string{"one", "two"}
	firstDigest, err := first.Digest()
	if err != nil {
		t.Fatal(err)
	}
	secondDigest, err := second.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if firstDigest != secondDigest {
		t.Fatalf("decision digests differ: %s != %s", firstDigest, secondDigest)
	}
}

func TestExtendedEnterprisePolicyFieldsSurviveJSONRoundTrip(t *testing.T) {
	t.Parallel()

	policy := Default()
	policy.License = LicensePolicy{
		Forbidden: []string{"AGPL-3.0-only"},
		Allowed:   []string{"Apache-2.0", "MIT"},
	}
	policy.Notifications = NotificationPolicy{
		Destinations: []string{"webhook:security", "siem:primary"},
	}
	encoded, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Policy
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if err := decoded.Normalize(); err != nil {
		t.Fatal(err)
	}
	if decoded.Canary.Observation != 15*time.Minute ||
		decoded.Retention.Artifacts != 90*24*time.Hour ||
		len(decoded.License.Forbidden) != 1 ||
		len(decoded.Notifications.Destinations) != 2 {
		t.Fatalf("round-tripped policy = %#v", decoded)
	}
}

func TestNotificationDestinationsAreOpaqueAdapterReferences(t *testing.T) {
	t.Parallel()
	policy := Default()
	policy.Notifications.Destinations = []string{
		"webhook:release-operations",
		"email:security-on-call",
		"siem:soc/primary",
	}
	if err := policy.Normalize(); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{
		"https://hooks.example.test/secret",
		"email:admin@example.test",
		"pager:primary",
		"webhook:",
	} {
		policy := Default()
		policy.Notifications.Destinations = []string{invalid}
		if err := policy.Normalize(); err == nil {
			t.Fatalf("notification destination %q was accepted", invalid)
		}
	}
}

func TestEvidenceThresholdsAndForbiddenLicensesAreHardBlocks(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 30, 15, 0, 0, 0, time.UTC)
	policy := Default()
	policy.License.Forbidden = []string{"AGPL-3.0-only"}
	candidate := passingCandidate(policy, now)
	candidate.Evidence = &Evidence{
		Vulnerabilities: VulnerabilityEvidence{
			Critical: 1, High: 2, DatabaseIdentity: "trivy-db@sha256:abc",
		},
		Licenses:       LicenseEvidence{Detected: []string{"AGPL-3.0-only"}},
		ParserFailures: 1,
		ExpectedLosses: 1,
		DurationDelta:  0.21,
		ResourceDelta:  0.22,
	}
	decision, err := Evaluate(candidate, policy, now)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Outcome != OutcomeBlocked ||
		!containsText(decision.BlockingReasons, "critical vulnerability") ||
		!containsText(decision.BlockingReasons, "forbidden license") ||
		!containsText(decision.BlockingReasons, "parser failures") ||
		!containsText(decision.BlockingReasons, "duration regression") {
		t.Fatalf("threshold decision = %#v", decision)
	}
}

func TestEvidenceChangesInvalidateApprovalBinding(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 30, 15, 0, 0, 0, time.UTC)
	policy := Default()
	candidate := passingCandidate(policy, now)
	candidate.Evidence = &Evidence{
		Vulnerabilities: VulnerabilityEvidence{DatabaseIdentity: "db@sha256:one"},
	}
	first, err := ApprovalBindingDigest(candidate, policy)
	if err != nil {
		t.Fatal(err)
	}
	candidate.Evidence.Vulnerabilities.DatabaseIdentity = "db@sha256:two"
	second, err := ApprovalBindingDigest(candidate, policy)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("evidence did not change approval binding digest")
	}
}

func passingCandidate(policy Policy, now time.Time) Candidate {
	gates := make([]Gate, 0, len(policy.RequiredGates))
	for _, name := range policy.RequiredGates {
		gates = append(gates, Gate{
			Name:           name,
			Status:         GatePassed,
			EvidenceDigest: "sha256:" + strings.Repeat("a", 64),
		})
	}
	return Candidate{
		ID:                    "candidate-1",
		LockDigest:            "sha256:" + strings.Repeat("b", 64),
		PolicyRevision:        policy.Revision,
		CreatorID:             "creator-1",
		Risk:                  RiskLow,
		Changes:               []Change{{Component: "base", Kind: ChangeRebuildOnly}},
		Gates:                 gates,
		MaintenanceWindowOpen: true,
	}
}

func validException(gate string, now time.Time) Exception {
	return Exception{
		ID:                  "exception-1",
		Gate:                gate,
		OwnerID:             "owner-1",
		Reason:              "No fixed package is available",
		CompensatingControl: "Scanner worker has no inbound network access",
		ApprovedBy:          "approver-1",
		ExpiresAt:           now.Add(7 * 24 * time.Hour),
	}
}

func without(values []string, excluded string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value != excluded {
			out = append(out, value)
		}
	}
	return out
}

func containsText(values []string, substring string) bool {
	for _, value := range values {
		if strings.Contains(value, substring) {
			return true
		}
	}
	return false
}
