// Package scannerpolicy evaluates scanner release candidates against a
// versioned, deterministic enterprise promotion policy.
package scannerpolicy

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

const PolicySchema = "wolf.scanner-policy/v1"

type Risk string

const (
	RiskLow      Risk = "low"
	RiskMedium   Risk = "medium"
	RiskHigh     Risk = "high"
	RiskCritical Risk = "critical"
)

type ApprovalMode string

const (
	ApprovalManual      ApprovalMode = "manual"
	ApprovalPolicyGated ApprovalMode = "policy_gated"
)

type GateStatus string

const (
	GatePending  GateStatus = "pending"
	GatePassed   GateStatus = "passed"
	GateFailed   GateStatus = "failed"
	GateExcepted GateStatus = "excepted"
)

type ChangeKind string

const (
	ChangeRebuildOnly ChangeKind = "rebuild_only"
	ChangePatch       ChangeKind = "patch"
	ChangeMinor       ChangeKind = "minor"
	ChangeMajor       ChangeKind = "major"
	ChangeParser      ChangeKind = "parser"
	ChangeLicense     ChangeKind = "license"
	ChangePlatform    ChangeKind = "platform"
	ChangeSource      ChangeKind = "source"
)

type Policy struct {
	SchemaVersion       string              `json:"schema_version"`
	Revision            int64               `json:"revision"`
	ApprovalMode        ApprovalMode        `json:"approval_mode"`
	RequiredApprovals   int                 `json:"required_approvals"`
	SeparateCreator     bool                `json:"separate_creator"`
	AutoPromoteRisks    []Risk              `json:"auto_promote_risks"`
	AutoPromoteChanges  []ChangeKind        `json:"auto_promote_changes"`
	RequiredGates       []string            `json:"required_gates"`
	AllowExceptions     map[string]bool     `json:"allow_exceptions"`
	ExceptionMaxAge     time.Duration       `json:"-"`
	ExceptionMaxAgeText string              `json:"exception_max_age"`
	Vulnerability       VulnerabilityPolicy `json:"vulnerability,omitempty"`
	License             LicensePolicy       `json:"license,omitempty"`
	Thresholds          EvidenceThresholds  `json:"thresholds,omitempty"`
	Canary              CanaryPolicy        `json:"canary,omitempty"`
	Rollback            RollbackPolicy      `json:"rollback,omitempty"`
	Retention           RetentionPolicy     `json:"retention,omitempty"`
	Notifications       NotificationPolicy  `json:"notifications,omitempty"`
	Alerts              AlertingPolicy      `json:"alerts,omitempty"`
}

type VulnerabilityPolicy struct {
	MaxCritical             int  `json:"max_critical,omitempty"`
	MaxHigh                 int  `json:"max_high,omitempty"`
	RequireDatabaseIdentity bool `json:"require_database_identity,omitempty"`
}

type LicensePolicy struct {
	Forbidden []string `json:"forbidden,omitempty"`
	Allowed   []string `json:"allowed,omitempty"`
}

type EvidenceThresholds struct {
	MaxParserFailures      int     `json:"max_parser_failures,omitempty"`
	MaxExpectedFindingLoss int     `json:"max_expected_finding_loss,omitempty"`
	MaxDurationRegression  float64 `json:"max_duration_regression,omitempty"`
	MaxResourceRegression  float64 `json:"max_resource_regression,omitempty"`
}

type CanaryPolicy struct {
	Size            int           `json:"size,omitempty"`
	MinimumSamples  int           `json:"minimum_samples,omitempty"`
	Observation     time.Duration `json:"-"`
	ObservationText string        `json:"observation,omitempty"`
}

type RollbackPolicy struct {
	Automatic                    bool    `json:"automatic"`
	MaxInfrastructureFailureRate float64 `json:"max_infrastructure_failure_rate,omitempty"`
	MaxDurationRegression        float64 `json:"max_duration_regression,omitempty"`
	MaxParserFailures            int     `json:"max_parser_failures,omitempty"`
}

type RetentionPolicy struct {
	Artifacts     time.Duration `json:"-"`
	Logs          time.Duration `json:"-"`
	ArtifactsText string        `json:"artifacts,omitempty"`
	LogsText      string        `json:"logs,omitempty"`
}

type NotificationPolicy struct {
	Destinations []string `json:"destinations,omitempty"`
}

type AlertingPolicy struct {
	MissedDiscovery     AlertDurationPolicy `json:"missed_discovery,omitempty"`
	StaleStableRelease  AlertDurationPolicy `json:"stale_stable_release,omitempty"`
	QueueBacklog        AlertQueuePolicy    `json:"queue_backlog,omitempty"`
	LeaseChurn          AlertCountPolicy    `json:"lease_churn,omitempty"`
	RepeatedGateFailure AlertCountPolicy    `json:"repeated_gate_failure,omitempty"`
	MirrorDrift         AlertSwitchPolicy   `json:"mirror_drift,omitempty"`
	RolloutFailure      AlertSwitchPolicy   `json:"rollout_failure,omitempty"`
	SignatureHealth     AlertSwitchPolicy   `json:"signature_health,omitempty"`
}

type AlertSwitchPolicy struct {
	Enabled bool `json:"enabled,omitempty"`
}

type AlertDurationPolicy struct {
	Enabled   bool          `json:"enabled,omitempty"`
	After     time.Duration `json:"-"`
	AfterText string        `json:"after,omitempty"`
}

type AlertQueuePolicy struct {
	Enabled    bool          `json:"enabled,omitempty"`
	MaxDepth   int           `json:"max_depth,omitempty"`
	MaxAge     time.Duration `json:"-"`
	MaxAgeText string        `json:"max_age,omitempty"`
}

type AlertCountPolicy struct {
	Enabled    bool          `json:"enabled,omitempty"`
	Count      int           `json:"count,omitempty"`
	Window     time.Duration `json:"-"`
	WindowText string        `json:"window,omitempty"`
}

type Candidate struct {
	ID                    string      `json:"id"`
	DefinitionCommit      string      `json:"definition_commit,omitempty"`
	LockDigest            string      `json:"lock_digest"`
	PolicyID              string      `json:"policy_id,omitempty"`
	PolicyRevision        int64       `json:"policy_revision"`
	CreatorID             string      `json:"creator_id"`
	Risk                  Risk        `json:"risk"`
	Changes               []Change    `json:"changes"`
	Gates                 []Gate      `json:"gates"`
	Exceptions            []Exception `json:"exceptions,omitempty"`
	Approvals             []Approval  `json:"approvals,omitempty"`
	MaintenanceWindowOpen bool        `json:"maintenance_window_open"`
	Evidence              *Evidence   `json:"evidence,omitempty"`
}

// Evidence contains the normalized, bounded values used by policy. Large
// reports remain content-addressed artifacts referenced by gate digests.
// Keeping these values in the approval binding prevents an evidence summary
// from changing underneath an existing approval.
type Evidence struct {
	Vulnerabilities VulnerabilityEvidence `json:"vulnerabilities,omitempty"`
	Licenses        LicenseEvidence       `json:"licenses,omitempty"`
	ParserFailures  int                   `json:"parser_failures,omitempty"`
	ExpectedLosses  int                   `json:"expected_finding_losses,omitempty"`
	DurationDelta   float64               `json:"duration_regression,omitempty"`
	ResourceDelta   float64               `json:"resource_regression,omitempty"`
}

type VulnerabilityEvidence struct {
	Critical         int    `json:"critical,omitempty"`
	High             int    `json:"high,omitempty"`
	DatabaseIdentity string `json:"database_identity,omitempty"`
}

type LicenseEvidence struct {
	Detected []string `json:"detected,omitempty"`
	Unknown  int      `json:"unknown,omitempty"`
}

type Change struct {
	Component string     `json:"component"`
	Kind      ChangeKind `json:"kind"`
	From      string     `json:"from,omitempty"`
	To        string     `json:"to,omitempty"`
}

type Gate struct {
	Name           string     `json:"name"`
	Status         GateStatus `json:"status"`
	EvidenceDigest string     `json:"evidence_digest,omitempty"`
	Summary        string     `json:"summary,omitempty"`
}

type Exception struct {
	ID                  string    `json:"id"`
	Gate                string    `json:"gate"`
	OwnerID             string    `json:"owner_id"`
	Reason              string    `json:"reason"`
	CompensatingControl string    `json:"compensating_control"`
	ApprovedBy          string    `json:"approved_by"`
	ExpiresAt           time.Time `json:"expires_at"`
}

type Approval struct {
	ActorID              string    `json:"actor_id"`
	LockDigest           string    `json:"lock_digest"`
	PolicyDecisionDigest string    `json:"policy_decision_digest,omitempty"`
	CreatedAt            time.Time `json:"created_at"`
}

type Outcome string

const (
	OutcomeBlocked          Outcome = "blocked"
	OutcomeAwaitingApproval Outcome = "awaiting_approval"
	OutcomeApproved         Outcome = "approved"
	OutcomeAutoApproved     Outcome = "auto_approved"
)

type Decision struct {
	CandidateID          string   `json:"candidate_id"`
	LockDigest           string   `json:"lock_digest"`
	PolicyRevision       int64    `json:"policy_revision"`
	PolicyDecisionDigest string   `json:"policy_decision_digest"`
	Outcome              Outcome  `json:"outcome"`
	AutoPromotion        bool     `json:"auto_promotion"`
	RequiredApprovals    int      `json:"required_approvals"`
	ValidApprovals       int      `json:"valid_approvals"`
	BlockingReasons      []string `json:"blocking_reasons,omitempty"`
	Advisories           []string `json:"advisories,omitempty"`
}

var nonBypassableGates = map[string]struct{}{
	"lock":        {},
	"artifacts":   {},
	"platforms":   {},
	"signature":   {},
	"provenance":  {},
	"parser":      {},
	"source":      {},
	"secret_scan": {},
}

// ExceptionEligible reports whether a policy gate may be covered by a
// scoped, approved, unexpired exception. Hard supply-chain trust gates are
// intentionally never exception-eligible.
func ExceptionEligible(gate string) bool {
	_, hard := nonBypassableGates[gate]
	return !hard
}

// Default returns the approval-by-default policy from the implementation plan.
func Default() Policy {
	return Policy{
		SchemaVersion:      PolicySchema,
		Revision:           1,
		ApprovalMode:       ApprovalManual,
		RequiredApprovals:  1,
		SeparateCreator:    true,
		AutoPromoteRisks:   []Risk{RiskLow},
		AutoPromoteChanges: []ChangeKind{ChangeRebuildOnly, ChangePatch},
		RequiredGates: []string{
			"lock", "artifacts", "platforms", "smoke", "parser",
			"vulnerability", "license", "sbom", "signature", "provenance",
			"source", "secret_scan", "compose", "kubernetes",
		},
		AllowExceptions:     map[string]bool{"vulnerability": true, "license": true},
		ExceptionMaxAge:     30 * 24 * time.Hour,
		ExceptionMaxAgeText: (30 * 24 * time.Hour).String(),
		Vulnerability:       VulnerabilityPolicy{MaxCritical: 0, MaxHigh: 0, RequireDatabaseIdentity: true},
		Thresholds: EvidenceThresholds{
			MaxParserFailures: 0, MaxExpectedFindingLoss: 0,
			MaxDurationRegression: 0.20, MaxResourceRegression: 0.20,
		},
		Canary: CanaryPolicy{
			Size: 1, MinimumSamples: 10, Observation: 15 * time.Minute,
			ObservationText: (15 * time.Minute).String(),
		},
		Rollback: RollbackPolicy{
			Automatic: true, MaxInfrastructureFailureRate: 0.02,
			MaxDurationRegression: 0.20, MaxParserFailures: 0,
		},
		Retention: RetentionPolicy{
			Artifacts: 90 * 24 * time.Hour, Logs: 30 * 24 * time.Hour,
			ArtifactsText: (90 * 24 * time.Hour).String(),
			LogsText:      (30 * 24 * time.Hour).String(),
		},
		Alerts: AlertingPolicy{
			MissedDiscovery: AlertDurationPolicy{
				AfterText: (72 * time.Hour).String(),
			},
			StaleStableRelease: AlertDurationPolicy{
				AfterText: (7 * 24 * time.Hour).String(),
			},
			QueueBacklog: AlertQueuePolicy{
				MaxDepth: 100, MaxAgeText: time.Hour.String(),
			},
			LeaseChurn: AlertCountPolicy{
				Count: 5, WindowText: (15 * time.Minute).String(),
			},
			RepeatedGateFailure: AlertCountPolicy{
				Count: 3, WindowText: time.Hour.String(),
			},
		},
	}
}

func (p *Policy) Normalize() error {
	if p.SchemaVersion != PolicySchema {
		return fmt.Errorf("unsupported scanner policy schema %q", p.SchemaVersion)
	}
	if p.Revision <= 0 {
		return errors.New("policy revision must be positive")
	}
	switch p.ApprovalMode {
	case ApprovalManual, ApprovalPolicyGated:
	default:
		return fmt.Errorf("invalid approval mode %q", p.ApprovalMode)
	}
	if p.RequiredApprovals < 0 {
		return errors.New("required approvals must not be negative")
	}
	if p.Vulnerability.MaxCritical < 0 || p.Vulnerability.MaxHigh < 0 {
		return errors.New("vulnerability thresholds must not be negative")
	}
	if p.Thresholds.MaxParserFailures < 0 || p.Thresholds.MaxExpectedFindingLoss < 0 ||
		!validRatio(p.Thresholds.MaxDurationRegression) ||
		!validRatio(p.Thresholds.MaxResourceRegression) {
		return errors.New("evidence thresholds must be non-negative and regression ratios must be between 0 and 1")
	}
	if p.Canary.Size <= 0 {
		p.Canary.Size = 1
	}
	if p.Canary.MinimumSamples <= 0 {
		p.Canary.MinimumSamples = 10
	}
	if strings.TrimSpace(p.Canary.ObservationText) == "" {
		p.Canary.ObservationText = (15 * time.Minute).String()
	}
	observation, err := time.ParseDuration(p.Canary.ObservationText)
	if err != nil || observation <= 0 {
		return errors.New("canary.observation must be a positive Go duration")
	}
	p.Canary.Observation = observation
	if p.Rollback.MaxParserFailures < 0 ||
		!validRatio(p.Rollback.MaxInfrastructureFailureRate) ||
		!validRatio(p.Rollback.MaxDurationRegression) {
		return errors.New("rollback thresholds must be non-negative and rates must be between 0 and 1")
	}
	if strings.TrimSpace(p.Retention.ArtifactsText) == "" {
		p.Retention.ArtifactsText = (90 * 24 * time.Hour).String()
	}
	if strings.TrimSpace(p.Retention.LogsText) == "" {
		p.Retention.LogsText = (30 * 24 * time.Hour).String()
	}
	artifactsRetention, err := time.ParseDuration(p.Retention.ArtifactsText)
	if err != nil || artifactsRetention <= 0 {
		return errors.New("retention.artifacts must be a positive Go duration")
	}
	logRetention, err := time.ParseDuration(p.Retention.LogsText)
	if err != nil || logRetention <= 0 {
		return errors.New("retention.logs must be a positive Go duration")
	}
	p.Retention.Artifacts = artifactsRetention
	p.Retention.Logs = logRetention
	if err := validateUniqueStrings("license.forbidden", p.License.Forbidden); err != nil {
		return err
	}
	if err := validateUniqueStrings("license.allowed", p.License.Allowed); err != nil {
		return err
	}
	if err := validateUniqueStrings("notifications.destinations", p.Notifications.Destinations); err != nil {
		return err
	}
	for _, destination := range p.Notifications.Destinations {
		if _, _, err := ParseNotificationDestination(destination); err != nil {
			return err
		}
	}
	if err := p.Alerts.normalize(); err != nil {
		return err
	}
	if strings.TrimSpace(p.ExceptionMaxAgeText) == "" {
		p.ExceptionMaxAgeText = (30 * 24 * time.Hour).String()
	}
	duration, err := time.ParseDuration(p.ExceptionMaxAgeText)
	if err != nil || duration <= 0 {
		return errors.New("exception_max_age must be a positive Go duration")
	}
	p.ExceptionMaxAge = duration
	if len(p.RequiredGates) == 0 {
		return errors.New("at least one required gate is required")
	}
	required := make(map[string]struct{}, len(p.RequiredGates))
	for _, gate := range p.RequiredGates {
		gate = strings.TrimSpace(gate)
		if gate == "" {
			return errors.New("required gate name must not be empty")
		}
		if _, exists := required[gate]; exists {
			return fmt.Errorf("duplicate required gate %q", gate)
		}
		required[gate] = struct{}{}
	}
	for gate := range nonBypassableGates {
		if _, exists := required[gate]; !exists {
			return fmt.Errorf("non-bypassable gate %q must be required", gate)
		}
		if p.AllowExceptions[gate] {
			return fmt.Errorf("non-bypassable gate %q cannot allow exceptions", gate)
		}
	}
	for _, risk := range p.AutoPromoteRisks {
		if !validRisk(risk) {
			return fmt.Errorf("invalid auto-promotion risk %q", risk)
		}
		if risk == RiskHigh || risk == RiskCritical {
			return fmt.Errorf("risk %q cannot be automatically promoted", risk)
		}
	}
	for _, change := range p.AutoPromoteChanges {
		if !validChange(change) {
			return fmt.Errorf("invalid auto-promotion change %q", change)
		}
		switch change {
		case ChangeMajor, ChangeParser, ChangeLicense, ChangePlatform, ChangeSource:
			return fmt.Errorf("change %q cannot be automatically promoted", change)
		}
	}
	return nil
}

const (
	minAlertDuration = time.Minute
	maxAlertDuration = 365 * 24 * time.Hour
	maxAlertCount    = 1_000_000
)

func (p *AlertingPolicy) normalize() error {
	if err := normalizeAlertDuration(
		"alerts.missed_discovery", &p.MissedDiscovery,
	); err != nil {
		return err
	}
	if err := normalizeAlertDuration(
		"alerts.stale_stable_release", &p.StaleStableRelease,
	); err != nil {
		return err
	}
	if p.QueueBacklog.MaxDepth < 0 || p.QueueBacklog.MaxDepth > maxAlertCount {
		return errors.New("alerts.queue_backlog.max_depth must be between 0 and 1000000")
	}
	if strings.TrimSpace(p.QueueBacklog.MaxAgeText) != "" {
		duration, err := boundedAlertDuration(
			"alerts.queue_backlog.max_age", p.QueueBacklog.MaxAgeText,
		)
		if err != nil {
			return err
		}
		p.QueueBacklog.MaxAge = duration
	}
	if p.QueueBacklog.Enabled &&
		p.QueueBacklog.MaxDepth == 0 && p.QueueBacklog.MaxAge == 0 {
		return errors.New("enabled alerts.queue_backlog requires max_depth or max_age")
	}
	if err := normalizeAlertCount(
		"alerts.lease_churn", &p.LeaseChurn,
	); err != nil {
		return err
	}
	if err := normalizeAlertCount(
		"alerts.repeated_gate_failure", &p.RepeatedGateFailure,
	); err != nil {
		return err
	}
	return nil
}

func normalizeAlertDuration(name string, rule *AlertDurationPolicy) error {
	if strings.TrimSpace(rule.AfterText) != "" {
		duration, err := boundedAlertDuration(name+".after", rule.AfterText)
		if err != nil {
			return err
		}
		rule.After = duration
	}
	if rule.Enabled && rule.After == 0 {
		return fmt.Errorf("enabled %s requires after", name)
	}
	return nil
}

func normalizeAlertCount(name string, rule *AlertCountPolicy) error {
	if rule.Count < 0 || rule.Count > maxAlertCount {
		return fmt.Errorf("%s.count must be between 0 and 1000000", name)
	}
	if strings.TrimSpace(rule.WindowText) != "" {
		duration, err := boundedAlertDuration(name+".window", rule.WindowText)
		if err != nil {
			return err
		}
		rule.Window = duration
	}
	if rule.Enabled && (rule.Count == 0 || rule.Window == 0) {
		return fmt.Errorf("enabled %s requires a positive count and window", name)
	}
	return nil
}

func boundedAlertDuration(name, value string) (time.Duration, error) {
	duration, err := time.ParseDuration(strings.TrimSpace(value))
	if err != nil || duration < minAlertDuration || duration > maxAlertDuration {
		return 0, fmt.Errorf(
			"%s must be a Go duration between %s and %s",
			name, minAlertDuration, maxAlertDuration,
		)
	}
	return duration, nil
}

// ParseNotificationDestination validates an opaque adapter reference. Policy
// data names the adapter class and an alias only; URLs, email addresses, and
// credentials are deployment-owned and must never be embedded here.
func ParseNotificationDestination(value string) (string, string, error) {
	if len(value) > 160 {
		return "", "", errors.New("notification destination must be at most 160 characters")
	}
	kind, reference, found := strings.Cut(strings.TrimSpace(value), ":")
	if !found || reference == "" {
		return "", "", fmt.Errorf(
			"notification destination %q must use webhook:<ref>, email:<ref>, or siem:<ref>",
			value,
		)
	}
	switch kind {
	case "webhook", "email", "siem":
	default:
		return "", "", fmt.Errorf("unsupported notification destination type %q", kind)
	}
	if len(reference) > 128 {
		return "", "", errors.New("notification destination reference must be at most 128 characters")
	}
	for _, character := range reference {
		if character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' ||
			strings.ContainsRune("._/-", character) {
			continue
		}
		return "", "", fmt.Errorf(
			"notification destination reference %q contains a disallowed character",
			reference,
		)
	}
	return kind, reference, nil
}

func validRatio(value float64) bool {
	return value >= 0 && value <= 1
}

func validateUniqueStrings(field string, values []string) error {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			return fmt.Errorf("%s contains an empty value", field)
		}
		if _, duplicate := seen[value]; duplicate {
			return fmt.Errorf("%s contains duplicate value %q", field, value)
		}
		seen[value] = struct{}{}
	}
	return nil
}

func Evaluate(candidate Candidate, policy Policy, now time.Time) (Decision, error) {
	if err := policy.Normalize(); err != nil {
		return Decision{}, err
	}
	if candidate.ID == "" || candidate.LockDigest == "" {
		return Decision{}, errors.New("candidate ID and lock digest are required")
	}
	if candidate.PolicyRevision != policy.Revision {
		return Decision{}, fmt.Errorf("candidate policy revision %d does not match active revision %d", candidate.PolicyRevision, policy.Revision)
	}
	if !validRisk(candidate.Risk) {
		return Decision{}, fmt.Errorf("invalid candidate risk %q", candidate.Risk)
	}
	now = now.UTC()
	decision := Decision{
		CandidateID:       candidate.ID,
		LockDigest:        candidate.LockDigest,
		PolicyRevision:    policy.Revision,
		RequiredApprovals: policy.RequiredApprovals,
	}
	for _, change := range candidate.Changes {
		if !validChange(change.Kind) {
			return Decision{}, fmt.Errorf("invalid candidate change kind %q", change.Kind)
		}
	}
	bindingDigest, err := ApprovalBindingDigest(candidate, policy)
	if err != nil {
		return Decision{}, err
	}
	decision.PolicyDecisionDigest = bindingDigest

	gates := make(map[string]Gate, len(candidate.Gates))
	for _, gate := range candidate.Gates {
		if gate.Name == "" {
			return Decision{}, errors.New("gate name is required")
		}
		if _, duplicate := gates[gate.Name]; duplicate {
			return Decision{}, fmt.Errorf("duplicate gate result %q", gate.Name)
		}
		gates[gate.Name] = gate
	}
	exceptions := make(map[string]Exception, len(candidate.Exceptions))
	for _, exception := range candidate.Exceptions {
		if exception.Gate == "" {
			return Decision{}, errors.New("exception gate is required")
		}
		if _, duplicate := exceptions[exception.Gate]; duplicate {
			return Decision{}, fmt.Errorf("multiple exceptions for gate %q", exception.Gate)
		}
		exceptions[exception.Gate] = exception
	}

	if candidate.Evidence != nil {
		if err := validateEvidence(*candidate.Evidence); err != nil {
			return Decision{}, err
		}
		decision.BlockingReasons = append(
			decision.BlockingReasons,
			evidenceBlockingReasons(*candidate.Evidence, policy, gates)...,
		)
	}

	hasException := false
	for _, required := range policy.RequiredGates {
		gate, exists := gates[required]
		if !exists {
			decision.BlockingReasons = append(decision.BlockingReasons, "required gate "+required+" is missing")
			continue
		}
		switch gate.Status {
		case GatePassed:
			if gate.EvidenceDigest == "" {
				decision.BlockingReasons = append(decision.BlockingReasons, "required gate "+required+" has no evidence digest")
			}
		case GateExcepted:
			hasException = true
			if _, hard := nonBypassableGates[required]; hard {
				decision.BlockingReasons = append(decision.BlockingReasons, "non-bypassable gate "+required+" cannot be excepted")
				continue
			}
			if !policy.AllowExceptions[required] {
				decision.BlockingReasons = append(decision.BlockingReasons, "gate "+required+" does not allow exceptions")
				continue
			}
			exception, ok := exceptions[required]
			if !ok {
				decision.BlockingReasons = append(decision.BlockingReasons, "gate "+required+" is excepted without an exception record")
				continue
			}
			if reason := validateException(exception, now, policy.ExceptionMaxAge); reason != "" {
				decision.BlockingReasons = append(decision.BlockingReasons, reason)
			}
		case GatePending:
			decision.BlockingReasons = append(decision.BlockingReasons, "required gate "+required+" is pending")
		case GateFailed:
			decision.BlockingReasons = append(decision.BlockingReasons, "required gate "+required+" failed")
		default:
			decision.BlockingReasons = append(decision.BlockingReasons, "required gate "+required+" has invalid status")
		}
	}
	if len(decision.BlockingReasons) > 0 {
		sort.Strings(decision.BlockingReasons)
		decision.Outcome = OutcomeBlocked
		return decision, nil
	}

	autoEligible := policy.ApprovalMode == ApprovalPolicyGated &&
		candidate.MaintenanceWindowOpen &&
		!hasException &&
		containsRisk(policy.AutoPromoteRisks, candidate.Risk)
	if !candidate.MaintenanceWindowOpen {
		decision.Advisories = append(decision.Advisories, "maintenance window is closed")
	}
	for _, change := range candidate.Changes {
		if !containsChange(policy.AutoPromoteChanges, change.Kind) {
			autoEligible = false
		}
	}
	if autoEligible {
		decision.Outcome = OutcomeAutoApproved
		decision.AutoPromotion = true
		decision.RequiredApprovals = 0
		return decision, nil
	}

	actors := make(map[string]struct{})
	for _, approval := range candidate.Approvals {
		if approval.ActorID == "" ||
			approval.LockDigest != candidate.LockDigest ||
			approval.PolicyDecisionDigest != bindingDigest {
			continue
		}
		if policy.SeparateCreator && approval.ActorID == candidate.CreatorID {
			continue
		}
		actors[approval.ActorID] = struct{}{}
	}
	decision.ValidApprovals = len(actors)
	if decision.ValidApprovals >= decision.RequiredApprovals {
		decision.Outcome = OutcomeApproved
	} else {
		decision.Outcome = OutcomeAwaitingApproval
	}
	return decision, nil
}

// ApprovalBindingDigest identifies the exact release inputs, evidence, policy,
// and exception set an approver is authorizing. Approval records with any
// other digest become stale automatically.
func ApprovalBindingDigest(candidate Candidate, policy Policy) (string, error) {
	if err := policy.Normalize(); err != nil {
		return "", err
	}
	type binding struct {
		CandidateID           string       `json:"candidate_id"`
		DefinitionCommit      string       `json:"definition_commit,omitempty"`
		LockDigest            string       `json:"lock_digest"`
		PolicyID              string       `json:"policy_id,omitempty"`
		PolicyRevision        int64        `json:"policy_revision"`
		ApprovalMode          ApprovalMode `json:"approval_mode"`
		RequiredApprovals     int          `json:"required_approvals"`
		SeparateCreator       bool         `json:"separate_creator"`
		Risk                  Risk         `json:"risk"`
		Changes               []Change     `json:"changes"`
		Gates                 []Gate       `json:"gates"`
		Exceptions            []Exception  `json:"exceptions"`
		MaintenanceWindowOpen bool         `json:"maintenance_window_open"`
		Evidence              *Evidence    `json:"evidence,omitempty"`
	}
	changes := append([]Change(nil), candidate.Changes...)
	gates := append([]Gate(nil), candidate.Gates...)
	exceptions := append([]Exception(nil), candidate.Exceptions...)
	sort.Slice(changes, func(i, j int) bool {
		if changes[i].Component == changes[j].Component {
			return changes[i].Kind < changes[j].Kind
		}
		return changes[i].Component < changes[j].Component
	})
	sort.Slice(gates, func(i, j int) bool { return gates[i].Name < gates[j].Name })
	sort.Slice(exceptions, func(i, j int) bool {
		if exceptions[i].Gate == exceptions[j].Gate {
			return exceptions[i].ID < exceptions[j].ID
		}
		return exceptions[i].Gate < exceptions[j].Gate
	})
	value, err := json.Marshal(binding{
		CandidateID:           candidate.ID,
		DefinitionCommit:      candidate.DefinitionCommit,
		LockDigest:            candidate.LockDigest,
		PolicyID:              candidate.PolicyID,
		PolicyRevision:        policy.Revision,
		ApprovalMode:          policy.ApprovalMode,
		RequiredApprovals:     policy.RequiredApprovals,
		SeparateCreator:       policy.SeparateCreator,
		Risk:                  candidate.Risk,
		Changes:               changes,
		Gates:                 gates,
		Exceptions:            exceptions,
		MaintenanceWindowOpen: candidate.MaintenanceWindowOpen,
		Evidence:              candidate.Evidence,
	})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func validateEvidence(evidence Evidence) error {
	if evidence.Vulnerabilities.Critical < 0 || evidence.Vulnerabilities.High < 0 ||
		evidence.Licenses.Unknown < 0 || evidence.ParserFailures < 0 ||
		evidence.ExpectedLosses < 0 || evidence.DurationDelta < 0 ||
		evidence.ResourceDelta < 0 {
		return errors.New("candidate evidence values must not be negative")
	}
	if err := validateUniqueStrings("evidence.licenses.detected", evidence.Licenses.Detected); err != nil {
		return err
	}
	return nil
}

func evidenceBlockingReasons(
	evidence Evidence,
	policy Policy,
	gates map[string]Gate,
) []string {
	var reasons []string
	vulnerabilityExcepted := gates["vulnerability"].Status == GateExcepted
	if !vulnerabilityExcepted {
		if evidence.Vulnerabilities.Critical > policy.Vulnerability.MaxCritical {
			reasons = append(reasons, fmt.Sprintf(
				"critical vulnerability count %d exceeds policy maximum %d",
				evidence.Vulnerabilities.Critical, policy.Vulnerability.MaxCritical,
			))
		}
		if evidence.Vulnerabilities.High > policy.Vulnerability.MaxHigh {
			reasons = append(reasons, fmt.Sprintf(
				"high vulnerability count %d exceeds policy maximum %d",
				evidence.Vulnerabilities.High, policy.Vulnerability.MaxHigh,
			))
		}
	}
	if policy.Vulnerability.RequireDatabaseIdentity &&
		strings.TrimSpace(evidence.Vulnerabilities.DatabaseIdentity) == "" {
		reasons = append(reasons, "vulnerability database identity is missing")
	}
	if evidence.ParserFailures > policy.Thresholds.MaxParserFailures {
		reasons = append(reasons, fmt.Sprintf(
			"parser failures %d exceed policy maximum %d",
			evidence.ParserFailures, policy.Thresholds.MaxParserFailures,
		))
	}
	if evidence.ExpectedLosses > policy.Thresholds.MaxExpectedFindingLoss {
		reasons = append(reasons, fmt.Sprintf(
			"expected finding losses %d exceed policy maximum %d",
			evidence.ExpectedLosses, policy.Thresholds.MaxExpectedFindingLoss,
		))
	}
	if evidence.DurationDelta > policy.Thresholds.MaxDurationRegression {
		reasons = append(reasons, fmt.Sprintf(
			"duration regression %.4f exceeds policy maximum %.4f",
			evidence.DurationDelta, policy.Thresholds.MaxDurationRegression,
		))
	}
	if evidence.ResourceDelta > policy.Thresholds.MaxResourceRegression {
		reasons = append(reasons, fmt.Sprintf(
			"resource regression %.4f exceeds policy maximum %.4f",
			evidence.ResourceDelta, policy.Thresholds.MaxResourceRegression,
		))
	}
	forbidden := make(map[string]struct{}, len(policy.License.Forbidden))
	for _, value := range policy.License.Forbidden {
		forbidden[strings.ToLower(strings.TrimSpace(value))] = struct{}{}
	}
	for _, value := range evidence.Licenses.Detected {
		if _, blocked := forbidden[strings.ToLower(strings.TrimSpace(value))]; blocked {
			reasons = append(reasons, "forbidden license detected: "+value)
		}
	}
	if len(policy.License.Allowed) > 0 && evidence.Licenses.Unknown > 0 &&
		gates["license"].Status != GateExcepted {
		reasons = append(reasons, fmt.Sprintf(
			"unknown license count %d requires an exception", evidence.Licenses.Unknown,
		))
	}
	sort.Strings(reasons)
	return reasons
}

func (d Decision) Digest() (string, error) {
	canonical := d
	canonical.BlockingReasons = append([]string(nil), d.BlockingReasons...)
	canonical.Advisories = append([]string(nil), d.Advisories...)
	sort.Strings(canonical.BlockingReasons)
	sort.Strings(canonical.Advisories)
	value, err := json.Marshal(canonical)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func validateException(exception Exception, now time.Time, maxAge time.Duration) string {
	if exception.ID == "" || exception.OwnerID == "" || exception.ApprovedBy == "" ||
		strings.TrimSpace(exception.Reason) == "" || strings.TrimSpace(exception.CompensatingControl) == "" {
		return "exception for gate " + exception.Gate + " is incomplete"
	}
	if exception.ExpiresAt.IsZero() || !exception.ExpiresAt.After(now) {
		return "exception for gate " + exception.Gate + " is expired"
	}
	if exception.ExpiresAt.After(now.Add(maxAge)) {
		return "exception for gate " + exception.Gate + " exceeds maximum lifetime"
	}
	return ""
}

func validRisk(risk Risk) bool {
	switch risk {
	case RiskLow, RiskMedium, RiskHigh, RiskCritical:
		return true
	default:
		return false
	}
}

func validChange(change ChangeKind) bool {
	switch change {
	case ChangeRebuildOnly, ChangePatch, ChangeMinor, ChangeMajor,
		ChangeParser, ChangeLicense, ChangePlatform, ChangeSource:
		return true
	default:
		return false
	}
}

func containsRisk(values []Risk, want Risk) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func containsChange(values []ChangeKind, want ChangeKind) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
