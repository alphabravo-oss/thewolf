// Package scannerrelease defines the durable domain used by scanner release
// discovery, build, publication, and rollout orchestration.
//
// The package deliberately contains no database or transport dependencies.
// Definitions and locks remain Git-owned; these records describe operational
// state and immutable publication evidence.
package scannerrelease

import "time"

// Policy is a versioned scanner supply-chain policy. Policy revisions are
// immutable: changing policy creates another row and disables the prior active
// revision.
type Policy struct {
	ID           string    `db:"id" json:"id"`
	Scope        string    `db:"scope" json:"scope"`
	Revision     int64     `db:"revision" json:"revision"`
	Enabled      bool      `db:"enabled" json:"enabled"`
	ScheduleJSON string    `db:"schedule_json" json:"schedule"`
	RulesJSON    string    `db:"rules_json" json:"rules"`
	CreatedBy    string    `db:"created_by" json:"created_by"`
	CreatedAt    time.Time `db:"created_at" json:"created_at"`
	UpdatedAt    time.Time `db:"updated_at" json:"updated_at"`
}

type RegistryType string

const (
	RegistryManaged RegistryType = "managed"
	RegistryMirror  RegistryType = "mirror"
	RegistryPrivate RegistryType = "private"
	RegistryAirGap  RegistryType = "air_gap"
)

// RegistryTarget stores references to secrets and trust policies, never secret
// material itself.
type RegistryTarget struct {
	ID                 string       `db:"id" json:"id"`
	Name               string       `db:"name" json:"name"`
	Type               RegistryType `db:"registry_type" json:"type"`
	Host               string       `db:"host" json:"host"`
	Namespace          string       `db:"namespace" json:"namespace"`
	SecretReference    string       `db:"secret_reference" json:"secret_reference,omitempty"`
	TrustPolicyRef     string       `db:"trust_policy_reference" json:"trust_policy_reference,omitempty"`
	PlatformPolicyJSON string       `db:"platform_policy_json" json:"platform_policy"`
	Enabled            bool         `db:"enabled" json:"enabled"`
	HealthStatus       string       `db:"health_status" json:"health,omitempty"`
	LastCheckedAt      *time.Time   `db:"last_checked_at" json:"last_checked_at,omitempty"`
	LastError          string       `db:"last_error" json:"error,omitempty"`
	LatencyMS          int64        `db:"latency_ms" json:"latency_ms,omitempty"`
	DigestParityStatus string       `db:"digest_parity_status" json:"digest_parity_status,omitempty"`
	MirrorLagSeconds   int64        `db:"mirror_lag_seconds" json:"mirror_lag_seconds,omitempty"`
	HealthDetailJSON   string       `db:"health_detail_json" json:"health_detail,omitempty"`
	Version            int64        `db:"version" json:"version"`
	CreatedBy          string       `db:"created_by" json:"created_by"`
	CreatedAt          time.Time    `db:"created_at" json:"created_at"`
	UpdatedAt          time.Time    `db:"updated_at" json:"updated_at"`
}

type RegistryObservation struct {
	HealthStatus       string
	CheckedAt          time.Time
	Error              string
	LatencyMS          int64
	DigestParityStatus string
	MirrorLagSeconds   int64
	DetailJSON         string
}

type RegistryJobKind string

const (
	RegistryJobReconcile RegistryJobKind = "reconcile"
	RegistryJobRepair    RegistryJobKind = "repair"
	RegistryJobCleanup   RegistryJobKind = "cleanup"
)

type RegistryJobState string

const (
	RegistryJobQueued     RegistryJobState = "queued"
	RegistryJobClaimed    RegistryJobState = "claimed"
	RegistryJobRetry      RegistryJobState = "retry"
	RegistryJobCompleted  RegistryJobState = "completed"
	RegistryJobDeadLetter RegistryJobState = "dead_letter"
	RegistryJobCancelled  RegistryJobState = "cancelled"
)

type RegistryReSignPolicy string

const (
	RegistryReSignPreserve  RegistryReSignPolicy = "preserve"
	RegistryReSignRequired  RegistryReSignPolicy = "required"
	RegistryReSignForbidden RegistryReSignPolicy = "forbidden"
)

// RegistryJob is a durable reconciliation command. A source target is
// required for repair and optional for read-only reconciliation. Cleanup jobs
// never infer permission to delete: each quarantine row is authorized again
// immediately before deletion.
type RegistryJob struct {
	ID                     string               `db:"id" json:"id"`
	RegistryTargetID       string               `db:"registry_target_id" json:"registry_target_id"`
	SourceRegistryTargetID string               `db:"source_registry_target_id" json:"source_registry_target_id,omitempty"`
	ReleaseID              string               `db:"release_id" json:"release_id,omitempty"`
	Kind                   RegistryJobKind      `db:"job_kind" json:"kind"`
	ReSignPolicy           RegistryReSignPolicy `db:"re_sign_policy" json:"re_sign_policy"`
	State                  RegistryJobState     `db:"state" json:"state"`
	Actor                  string               `db:"actor" json:"actor"`
	Reason                 string               `db:"reason" json:"reason"`
	IdempotencyKey         string               `db:"idempotency_key" json:"idempotency_key"`
	Attempt                int                  `db:"attempt" json:"attempt"`
	MaxAttempts            int                  `db:"max_attempts" json:"max_attempts"`
	AvailableAt            time.Time            `db:"available_at" json:"available_at"`
	WorkerID               string               `db:"worker_id" json:"worker_id,omitempty"`
	LeaseToken             string               `db:"lease_token" json:"-"`
	LeaseExpiresAt         *time.Time           `db:"lease_expires_at" json:"lease_expires_at,omitempty"`
	HeartbeatAt            *time.Time           `db:"heartbeat_at" json:"heartbeat_at,omitempty"`
	SummaryJSON            string               `db:"summary_json" json:"summary"`
	ErrorClass             string               `db:"error_class" json:"error_class,omitempty"`
	ErrorDetail            string               `db:"error_detail" json:"error_detail,omitempty"`
	Version                int64                `db:"version" json:"version"`
	StartedAt              *time.Time           `db:"started_at" json:"started_at,omitempty"`
	CompletedAt            *time.Time           `db:"completed_at" json:"completed_at,omitempty"`
	DeadLetteredAt         *time.Time           `db:"dead_lettered_at" json:"dead_lettered_at,omitempty"`
	CreatedAt              time.Time            `db:"created_at" json:"created_at"`
	UpdatedAt              time.Time            `db:"updated_at" json:"updated_at"`
}

type RegistryJobFilter struct {
	RegistryTargetID string
	ReleaseID        string
	State            RegistryJobState
	Kind             RegistryJobKind
}

type RegistryJobLeaseStatus struct {
	Current bool             `db:"current" json:"current"`
	State   RegistryJobState `db:"state" json:"state"`
	Version int64            `db:"version" json:"version"`
}

type RegistryJobReclaimSummary struct {
	Retried      int `json:"retried"`
	DeadLettered int `json:"dead_lettered"`
}

type RegistryImageObservation struct {
	ID                          string    `db:"id" json:"id"`
	JobID                       string    `db:"job_id" json:"job_id"`
	ImageKey                    string    `db:"image_key" json:"image_key"`
	SourceReference             string    `db:"source_reference" json:"source_reference,omitempty"`
	DestinationReference        string    `db:"destination_reference" json:"destination_reference"`
	ExpectedDigest              string    `db:"expected_digest" json:"expected_digest"`
	SourceDigest                string    `db:"source_digest" json:"source_digest,omitempty"`
	DestinationDigest           string    `db:"destination_digest" json:"destination_digest,omitempty"`
	ExpectedSignatureDigest     string    `db:"expected_signature_digest" json:"expected_signature_digest,omitempty"`
	DestinationSignatureDigest  string    `db:"destination_signature_digest" json:"destination_signature_digest,omitempty"`
	ExpectedProvenanceDigest    string    `db:"expected_provenance_digest" json:"expected_provenance_digest,omitempty"`
	DestinationProvenanceDigest string    `db:"destination_provenance_digest" json:"destination_provenance_digest,omitempty"`
	ExpectedSBOMDigest          string    `db:"expected_sbom_digest" json:"expected_sbom_digest,omitempty"`
	DestinationSBOMDigest       string    `db:"destination_sbom_digest" json:"destination_sbom_digest,omitempty"`
	State                       string    `db:"state" json:"state"`
	DetailJSON                  string    `db:"detail_json" json:"detail"`
	CheckedAt                   time.Time `db:"checked_at" json:"checked_at"`
	CreatedAt                   time.Time `db:"created_at" json:"created_at"`
	UpdatedAt                   time.Time `db:"updated_at" json:"updated_at"`
}

type RegistryQuarantineObject struct {
	ID                     string     `db:"id" json:"id"`
	RegistryTargetID       string     `db:"registry_target_id" json:"registry_target_id"`
	CandidateID            string     `db:"candidate_id" json:"candidate_id,omitempty"`
	Repository             string     `db:"repository" json:"repository"`
	Digest                 string     `db:"digest" json:"digest"`
	ObjectKind             string     `db:"object_kind" json:"object_kind"`
	State                  string     `db:"state" json:"state"`
	Protected              bool       `db:"protected" json:"protected"`
	RetentionClass         string     `db:"retention_class" json:"retention_class"`
	RetainUntil            *time.Time `db:"retain_until" json:"retain_until,omitempty"`
	DiscoveredAt           time.Time  `db:"discovered_at" json:"discovered_at"`
	LastReferencedAt       *time.Time `db:"last_referenced_at" json:"last_referenced_at,omitempty"`
	DeletionWorkerID       string     `db:"deletion_worker_id" json:"deletion_worker_id,omitempty"`
	DeletionLeaseToken     string     `db:"deletion_lease_token" json:"-"`
	DeletionLeaseExpiresAt *time.Time `db:"deletion_lease_expires_at" json:"deletion_lease_expires_at,omitempty"`
	DeletionVerifiedAt     *time.Time `db:"deletion_verified_at" json:"deletion_verified_at,omitempty"`
	ErrorDetail            string     `db:"error_detail" json:"error_detail,omitempty"`
	MetadataJSON           string     `db:"metadata_json" json:"metadata"`
	Version                int64      `db:"version" json:"version"`
	CreatedAt              time.Time  `db:"created_at" json:"created_at"`
	UpdatedAt              time.Time  `db:"updated_at" json:"updated_at"`
}

type RegistryQuarantineDecision struct {
	Eligible bool     `json:"eligible"`
	Reasons  []string `json:"reasons,omitempty"`
}

type SignerProvider string

const (
	SignerAWSKMS         SignerProvider = "aws_kms"
	SignerGCPKMS         SignerProvider = "gcp_kms"
	SignerAzureKeyVault  SignerProvider = "azure_key_vault"
	SignerPKCS11         SignerProvider = "pkcs11"
	SignerKeyless        SignerProvider = "keyless"
	SignerOffline        SignerProvider = "offline"
	SignerManagedKeyless SignerProvider = "managed_keyless"
)

type SignerState string

const (
	SignerActive   SignerState = "active"
	SignerDisabled SignerState = "disabled"
	SignerRevoked  SignerState = "revoked"
)

// SignerProfile stores only opaque provider and secret references. Private
// signing material is deliberately absent from the control-plane model.
type SignerProfile struct {
	ID                 string         `db:"id" json:"id"`
	Name               string         `db:"name" json:"name"`
	Provider           SignerProvider `db:"provider" json:"provider"`
	Algorithm          string         `db:"algorithm" json:"algorithm"`
	KeyReference       string         `db:"key_reference" json:"-"`
	SecretReference    string         `db:"secret_reference" json:"-"`
	WorkloadIdentity   bool           `db:"workload_identity" json:"workload_identity"`
	Identity           string         `db:"identity" json:"identity"`
	Issuer             string         `db:"issuer" json:"issuer"`
	Subject            string         `db:"subject" json:"subject"`
	TrustRootReference string         `db:"trust_root_reference" json:"-"`
	State              SignerState    `db:"state" json:"state"`
	Revision           int64          `db:"revision" json:"revision"`
	RotatedFromID      string         `db:"rotated_from_id" json:"rotated_from_id,omitempty"`
	RevocationReason   string         `db:"revocation_reason" json:"revocation_reason,omitempty"`
	RevokedBy          string         `db:"revoked_by" json:"revoked_by,omitempty"`
	RevokedAt          *time.Time     `db:"revoked_at" json:"revoked_at,omitempty"`
	CreatedBy          string         `db:"created_by" json:"created_by"`
	CreatedAt          time.Time      `db:"created_at" json:"created_at"`
	UpdatedAt          time.Time      `db:"updated_at" json:"updated_at"`
}

type DiscoveryTrigger string

const (
	DiscoveryScheduled DiscoveryTrigger = "scheduled"
	DiscoveryOnDemand  DiscoveryTrigger = "on_demand"
	DiscoverySecurity  DiscoveryTrigger = "security"
)

type DiscoveryRun struct {
	ID                string           `db:"id" json:"id"`
	Trigger           DiscoveryTrigger `db:"trigger" json:"trigger"`
	SchedulePeriod    string           `db:"schedule_period" json:"schedule_period,omitempty"`
	DefinitionCommit  string           `db:"definition_commit" json:"definition_commit"`
	DefinitionDigest  string           `db:"definition_digest" json:"definition_digest,omitempty"`
	LockDigest        string           `db:"lock_digest" json:"lock_digest,omitempty"`
	PolicyID          string           `db:"policy_id" json:"policy_id"`
	PolicyRevision    int64            `db:"policy_revision" json:"policy_revision"`
	ScopeJSON         string           `db:"scope_json" json:"scope"`
	State             DiscoveryState   `db:"state" json:"state"`
	Coverage          float64          `db:"coverage" json:"coverage"`
	TotalCount        int              `db:"total_count" json:"total_count"`
	CoveredCount      int              `db:"covered_count" json:"covered_count"`
	CurrentCount      int              `db:"current_count" json:"current_count"`
	AvailableCount    int              `db:"available_count" json:"available_count"`
	UnreachableCount  int              `db:"unreachable_count" json:"unreachable_count"`
	UnsupportedCount  int              `db:"unsupported_count" json:"unsupported_count"`
	HeldCount         int              `db:"held_count" json:"held_count"`
	YankedCount       int              `db:"yanked_count" json:"yanked_count"`
	UnknownCount      int              `db:"unknown_count" json:"unknown_count"`
	SelectedCount     int              `db:"selected_count" json:"selected_count"`
	WorkerID          string           `db:"worker_id" json:"worker_id,omitempty"`
	LeaseToken        string           `db:"lease_token" json:"-"`
	LeaseExpiresAt    *time.Time       `db:"lease_expires_at" json:"lease_expires_at,omitempty"`
	HeartbeatAt       *time.Time       `db:"heartbeat_at" json:"heartbeat_at,omitempty"`
	CancelRequestedAt *time.Time       `db:"cancel_requested_at" json:"cancel_requested_at,omitempty"`
	Attempt           int              `db:"attempt" json:"attempt"`
	MaxAttempts       int              `db:"max_attempts" json:"max_attempts"`
	ErrorClass        string           `db:"error_class" json:"error_class,omitempty"`
	ErrorDetail       string           `db:"error_detail" json:"error_detail,omitempty"`
	Actor             string           `db:"actor" json:"actor"`
	IdempotencyKey    string           `db:"idempotency_key" json:"idempotency_key"`
	Version           int64            `db:"version" json:"version"`
	StartedAt         *time.Time       `db:"started_at" json:"started_at,omitempty"`
	CompletedAt       *time.Time       `db:"completed_at" json:"completed_at,omitempty"`
	CreatedAt         time.Time        `db:"created_at" json:"created_at"`
	UpdatedAt         time.Time        `db:"updated_at" json:"updated_at"`
}

// DiscoveryLeaseStatus is returned by heartbeats so workers can distinguish
// cooperative cancellation from a stale or transferred lease without another
// database read.
type DiscoveryLeaseStatus struct {
	Current         bool  `db:"current" json:"current"`
	CancelRequested bool  `db:"cancel_requested" json:"cancel_requested"`
	Version         int64 `db:"version" json:"version"`
}

type ComponentType string

const (
	ComponentTool          ComponentType = "tool"
	ComponentUpstreamImage ComponentType = "upstream_image"
	ComponentBaseImage     ComponentType = "base_image"
	ComponentToolchain     ComponentType = "toolchain"
	ComponentPackage       ComponentType = "package"
)

type RiskClass string

const (
	RiskNone     RiskClass = "none"
	RiskLow      RiskClass = "low"
	RiskMedium   RiskClass = "medium"
	RiskHigh     RiskClass = "high"
	RiskCritical RiskClass = "critical"
)

type UpdateItem struct {
	ID                 string        `db:"id" json:"id"`
	DiscoveryRunID     string        `db:"discovery_run_id" json:"discovery_run_id"`
	ComponentType      ComponentType `db:"component_type" json:"component_type"`
	ComponentName      string        `db:"component_name" json:"component_name"`
	CurrentValue       string        `db:"current_value" json:"current_value"`
	AvailableValue     string        `db:"available_value" json:"available_value"`
	AvailableDigest    string        `db:"available_digest" json:"available_digest,omitempty"`
	Status             string        `db:"status" json:"status"`
	SourceEvidenceJSON string        `db:"source_evidence_json" json:"source_evidence"`
	RiskClass          RiskClass     `db:"risk_class" json:"risk_class"`
	CompatibilityJSON  string        `db:"compatibility_json" json:"compatibility"`
	SelectionState     string        `db:"selection_state" json:"selection_state"`
	ErrorClass         string        `db:"error_class" json:"error_class,omitempty"`
	ErrorDetail        string        `db:"error_detail" json:"error_detail,omitempty"`
	Resolver           string        `db:"resolver" json:"resolver,omitempty"`
	Attempts           int           `db:"attempts" json:"attempts"`
	RetryAt            *time.Time    `db:"retry_at" json:"retry_at,omitempty"`
	CheckedAt          *time.Time    `db:"checked_at" json:"checked_at,omitempty"`
	CreatedAt          time.Time     `db:"created_at" json:"created_at"`
	UpdatedAt          time.Time     `db:"updated_at" json:"updated_at"`
}

type Candidate struct {
	ID                     string         `db:"id" json:"id"`
	DiscoveryRunID         string         `db:"discovery_run_id" json:"discovery_run_id,omitempty"`
	SelectionJSON          string         `db:"selection_json" json:"selection"`
	DefinitionCommit       string         `db:"definition_commit" json:"definition_commit"`
	ProposedCommit         string         `db:"proposed_commit" json:"proposed_commit,omitempty"`
	ProposalURL            string         `db:"proposal_url" json:"proposal_url,omitempty"`
	LockDigest             string         `db:"lock_digest" json:"lock_digest,omitempty"`
	LockURI                string         `db:"lock_uri" json:"lock_uri,omitempty"`
	RiskSummaryJSON        string         `db:"risk_summary_json" json:"risk_summary"`
	State                  CandidateState `db:"state" json:"state"`
	RequiredGatesJSON      string         `db:"required_gates_json" json:"required_gates"`
	PolicyDecision         string         `db:"policy_decision" json:"policy_decision"`
	PolicyID               string         `db:"policy_id" json:"policy_id"`
	PolicyRevision         int64          `db:"policy_revision" json:"policy_revision"`
	ProposalWorkerID       string         `db:"proposal_worker_id" json:"proposal_worker_id,omitempty"`
	ProposalLeaseToken     string         `db:"proposal_lease_token" json:"-"`
	ProposalLeaseExpiresAt *time.Time     `db:"proposal_lease_expires_at" json:"proposal_lease_expires_at,omitempty"`
	ProposalHeartbeatAt    *time.Time     `db:"proposal_heartbeat_at" json:"proposal_heartbeat_at,omitempty"`
	ProposalAttempt        int            `db:"proposal_attempt" json:"proposal_attempt"`
	ProposalMaxAttempts    int            `db:"proposal_max_attempts" json:"proposal_max_attempts"`
	ProposalErrorClass     string         `db:"proposal_error_class" json:"proposal_error_class,omitempty"`
	ProposalErrorDetail    string         `db:"proposal_error_detail" json:"proposal_error_detail,omitempty"`
	ProposalStartedAt      *time.Time     `db:"proposal_started_at" json:"proposal_started_at,omitempty"`
	ProposalCompletedAt    *time.Time     `db:"proposal_completed_at" json:"proposal_completed_at,omitempty"`
	Actor                  string         `db:"actor" json:"actor"`
	IdempotencyKey         string         `db:"idempotency_key" json:"idempotency_key"`
	ErrorClass             string         `db:"error_class" json:"error_class,omitempty"`
	ErrorDetail            string         `db:"error_detail" json:"error_detail,omitempty"`
	Version                int64          `db:"version" json:"version"`
	CreatedAt              time.Time      `db:"created_at" json:"created_at"`
	UpdatedAt              time.Time      `db:"updated_at" json:"updated_at"`
}

// CandidateProposalLeaseStatus reports whether a proposal worker still owns
// the candidate. State is returned to distinguish cancellation or rejection
// races from an ordinary expired or transferred lease.
type CandidateProposalLeaseStatus struct {
	Current bool           `db:"current" json:"current"`
	Version int64          `db:"version" json:"version"`
	State   CandidateState `db:"state" json:"state"`
}

type BuildRun struct {
	ID                string     `db:"id" json:"id"`
	CandidateID       string     `db:"candidate_id" json:"candidate_id"`
	Attempt           int        `db:"attempt" json:"attempt"`
	WorkerID          string     `db:"worker_id" json:"worker_id,omitempty"`
	State             BuildState `db:"state" json:"state"`
	PlatformsJSON     string     `db:"platforms_json" json:"platforms"`
	LeaseToken        string     `db:"lease_token" json:"-"`
	LeaseExpiresAt    *time.Time `db:"lease_expires_at" json:"lease_expires_at,omitempty"`
	HeartbeatAt       *time.Time `db:"heartbeat_at" json:"heartbeat_at,omitempty"`
	CancelRequestedAt *time.Time `db:"cancel_requested_at" json:"cancel_requested_at,omitempty"`
	ErrorClass        string     `db:"error_class" json:"error_class,omitempty"`
	ErrorDetail       string     `db:"error_detail" json:"error_detail,omitempty"`
	Version           int64      `db:"version" json:"version"`
	StartedAt         *time.Time `db:"started_at" json:"started_at,omitempty"`
	CompletedAt       *time.Time `db:"completed_at" json:"completed_at,omitempty"`
	CreatedAt         time.Time  `db:"created_at" json:"created_at"`
	UpdatedAt         time.Time  `db:"updated_at" json:"updated_at"`
}

// BuildLeaseStatus is returned by heartbeats so a worker can cooperatively
// stop without a second database read. Current=false means the lease was
// cancelled, expired, reclaimed, or transferred.
type BuildLeaseStatus struct {
	Current         bool  `db:"current" json:"current"`
	CancelRequested bool  `db:"cancel_requested" json:"cancel_requested"`
	Version         int64 `db:"version" json:"version"`
}

type BuildStep struct {
	ID             string     `db:"id" json:"id"`
	BuildRunID     string     `db:"build_run_id" json:"build_run_id"`
	StepKey        string     `db:"step_key" json:"step_key"`
	State          BuildState `db:"state" json:"state"`
	Attempt        int        `db:"attempt" json:"attempt"`
	OutputURI      string     `db:"output_uri" json:"output_uri,omitempty"`
	OutputDigest   string     `db:"output_digest" json:"output_digest,omitempty"`
	SummaryJSON    string     `db:"summary_json" json:"summary"`
	RetentionClass string     `db:"retention_class" json:"retention_class"`
	RetainUntil    *time.Time `db:"retain_until" json:"retain_until,omitempty"`
	Protected      bool       `db:"protected" json:"protected"`
	ErrorClass     string     `db:"error_class" json:"error_class,omitempty"`
	ErrorDetail    string     `db:"error_detail" json:"error_detail,omitempty"`
	Version        int64      `db:"version" json:"version"`
	StartedAt      *time.Time `db:"started_at" json:"started_at,omitempty"`
	CompletedAt    *time.Time `db:"completed_at" json:"completed_at,omitempty"`
	CreatedAt      time.Time  `db:"created_at" json:"created_at"`
	UpdatedAt      time.Time  `db:"updated_at" json:"updated_at"`
}

type Release struct {
	ID               string       `db:"id" json:"id"`
	Name             string       `db:"release_name" json:"name"`
	CandidateID      string       `db:"candidate_id" json:"candidate_id"`
	LockDigest       string       `db:"lock_digest" json:"lock_digest"`
	ManifestDigest   string       `db:"manifest_digest" json:"manifest_digest"`
	ManifestURI      string       `db:"manifest_uri" json:"manifest_uri"`
	State            ReleaseState `db:"state" json:"state"`
	SignerIdentity   string       `db:"signer_identity" json:"signer_identity"`
	PolicyID         string       `db:"policy_id" json:"policy_id"`
	PolicyRevision   int64        `db:"policy_revision" json:"policy_revision"`
	DefinitionCommit string       `db:"definition_commit" json:"definition_commit"`
	Imported         bool         `db:"imported" json:"imported"`
	Legacy           bool         `db:"legacy" json:"legacy"`
	Protected        bool         `db:"protected" json:"protected"`
	RollbackEligible bool         `db:"rollback_eligible" json:"rollback_eligible"`
	RetentionClass   string       `db:"retention_class" json:"retention_class"`
	RetainUntil      *time.Time   `db:"retain_until" json:"retain_until,omitempty"`
	Version          int64        `db:"version" json:"version"`
	PublishedAt      time.Time    `db:"published_at" json:"published_at"`
	DeprecatedAt     *time.Time   `db:"deprecated_at" json:"deprecated_at,omitempty"`
	RevokedAt        *time.Time   `db:"revoked_at" json:"revoked_at,omitempty"`
	CreatedAt        time.Time    `db:"created_at" json:"created_at"`
	UpdatedAt        time.Time    `db:"updated_at" json:"updated_at"`
}

type ReleaseTool struct {
	ID                  string    `db:"id" json:"id"`
	ReleaseID           string    `db:"release_id" json:"release_id"`
	ToolKey             string    `db:"tool_key" json:"tool_key"`
	Version             string    `db:"tool_version" json:"version"`
	SourceReference     string    `db:"source_reference" json:"source_reference"`
	SourceDigest        string    `db:"source_digest" json:"source_digest,omitempty"`
	Checksum            string    `db:"checksum" json:"checksum,omitempty"`
	ParserCompatibility string    `db:"parser_compatibility" json:"parser_compatibility"`
	MetadataJSON        string    `db:"metadata_json" json:"metadata"`
	CreatedAt           time.Time `db:"created_at" json:"created_at"`
}

type ReleaseImage struct {
	ID                         string    `db:"id" json:"id"`
	ReleaseID                  string    `db:"release_id" json:"release_id"`
	ImageKey                   string    `db:"image_key" json:"image_key"`
	ImageKind                  string    `db:"image_kind" json:"image_kind"`
	RegistryTargetID           string    `db:"registry_target_id" json:"registry_target_id"`
	Repository                 string    `db:"repository" json:"repository"`
	Digest                     string    `db:"digest" json:"digest"`
	PlatformDigests            string    `db:"platform_digests_json" json:"platform_digests"`
	SizeBytes                  int64     `db:"size_bytes" json:"size_bytes"`
	SignatureStatus            string    `db:"signature_status" json:"signature_status"`
	SignatureDigest            string    `db:"signature_digest" json:"signature_digest"`
	SignatureArtifactURI       string    `db:"signature_artifact_uri" json:"signature_artifact_uri"`
	SignatureArtifactDigest    string    `db:"signature_artifact_digest" json:"signature_artifact_digest"`
	SignatureMediaType         string    `db:"signature_media_type" json:"signature_media_type"`
	SignatureArtifactSizeBytes int64     `db:"signature_artifact_size_bytes" json:"signature_artifact_size_bytes"`
	SignatureCertificateDigest string    `db:"signature_certificate_digest" json:"signature_certificate_digest,omitempty"`
	SignatureIdentity          string    `db:"signature_identity" json:"signature_identity"`
	SignatureIssuer            string    `db:"signature_issuer" json:"signature_issuer"`
	SignatureSubject           string    `db:"signature_subject" json:"signature_subject"`
	SignatureTrustRoot         string    `db:"signature_trust_root" json:"signature_trust_root"`
	SignatureOperationID       string    `db:"signature_operation_id" json:"signature_operation_id"`
	ProvenanceDigest           string    `db:"provenance_digest" json:"provenance_digest"`
	SBOMDigest                 string    `db:"sbom_digest" json:"sbom_digest"`
	CreatedAt                  time.Time `db:"created_at" json:"created_at"`
}

const (
	ReleaseImageScanner = "scanner"
	ReleaseImageFixer   = "fixer"
)

// NormalizedImageKind preserves compatibility with inventories published
// before image_kind was introduced. Those inventories only contained scanner
// runtime images, so an empty value has the unambiguous meaning "scanner".
func NormalizedImageKind(image ReleaseImage) string {
	if image.ImageKind == "" {
		return ReleaseImageScanner
	}
	return image.ImageKind
}

func IsRuntimeScannerImage(image ReleaseImage) bool {
	return NormalizedImageKind(image) == ReleaseImageScanner
}

type ReleaseArtifact struct {
	ID             string     `db:"id" json:"id"`
	ReleaseID      string     `db:"release_id" json:"release_id,omitempty"`
	CandidateID    string     `db:"candidate_id" json:"candidate_id,omitempty"`
	ArtifactType   string     `db:"artifact_type" json:"artifact_type"`
	MediaType      string     `db:"media_type" json:"media_type"`
	URI            string     `db:"uri" json:"uri"`
	Digest         string     `db:"digest" json:"digest"`
	SizeBytes      int64      `db:"size_bytes" json:"size_bytes"`
	RetentionClass string     `db:"retention_class" json:"retention_class"`
	RetainUntil    *time.Time `db:"retain_until" json:"retain_until,omitempty"`
	Protected      bool       `db:"protected" json:"protected"`
	CreatedAt      time.Time  `db:"created_at" json:"created_at"`
}

type Approval struct {
	ID                  string     `db:"id" json:"id"`
	CandidateID         string     `db:"candidate_id" json:"candidate_id,omitempty"`
	ReleaseID           string     `db:"release_id" json:"release_id,omitempty"`
	Actor               string     `db:"actor" json:"actor"`
	Action              string     `db:"action" json:"action"`
	Reason              string     `db:"reason" json:"reason"`
	ExceptionScope      string     `db:"exception_scope" json:"exception_scope,omitempty"`
	ExceptionOwner      string     `db:"exception_owner_id" json:"exception_owner_id,omitempty"`
	CompensatingControl string     `db:"compensating_control" json:"compensating_control,omitempty"`
	EvidenceDigest      string     `db:"evidence_digest" json:"evidence_digest"`
	PolicyDecision      string     `db:"policy_decision" json:"policy_decision"`
	ExpiresAt           *time.Time `db:"expires_at" json:"expires_at,omitempty"`
	IdempotencyKey      string     `db:"idempotency_key" json:"idempotency_key"`
	CreatedAt           time.Time  `db:"created_at" json:"created_at"`
}

type Rollout struct {
	ID                  string       `db:"id" json:"id"`
	Target              string       `db:"target" json:"target"`
	FromReleaseID       string       `db:"from_release_id" json:"from_release_id,omitempty"`
	ToReleaseID         string       `db:"to_release_id" json:"to_release_id"`
	Strategy            string       `db:"strategy" json:"strategy"`
	State               RolloutState `db:"state" json:"state"`
	PolicySnapshotJSON  string       `db:"policy_snapshot_json" json:"policy_snapshot"`
	Actor               string       `db:"actor" json:"actor"`
	IdempotencyKey      string       `db:"idempotency_key" json:"idempotency_key"`
	RollbackOfRolloutID string       `db:"rollback_of_rollout_id" json:"rollback_of_rollout_id,omitempty"`
	ErrorClass          string       `db:"error_class" json:"error_class,omitempty"`
	ErrorDetail         string       `db:"error_detail" json:"error_detail,omitempty"`
	Version             int64        `db:"version" json:"version"`
	StartedAt           *time.Time   `db:"started_at" json:"started_at,omitempty"`
	CompletedAt         *time.Time   `db:"completed_at" json:"completed_at,omitempty"`
	CreatedAt           time.Time    `db:"created_at" json:"created_at"`
	UpdatedAt           time.Time    `db:"updated_at" json:"updated_at"`
}

type RolloutCohort struct {
	ID                string     `db:"id" json:"id"`
	RolloutID         string     `db:"rollout_id" json:"rollout_id"`
	Name              string     `db:"cohort_name" json:"name"`
	Ordinal           int        `db:"ordinal" json:"ordinal"`
	DesiredReleaseID  string     `db:"desired_release_id" json:"desired_release_id"`
	ObservedReleaseID string     `db:"observed_release_id" json:"observed_release_id,omitempty"`
	State             string     `db:"state" json:"state"`
	TotalWorkers      int        `db:"total_workers" json:"total_workers"`
	ReadyWorkers      int        `db:"ready_workers" json:"ready_workers"`
	FailedWorkers     int        `db:"failed_workers" json:"failed_workers"`
	HealthSummaryJSON string     `db:"health_summary_json" json:"health_summary"`
	Deadline          *time.Time `db:"deadline" json:"deadline,omitempty"`
	StartedAt         *time.Time `db:"started_at" json:"started_at,omitempty"`
	HealthObservedAt  *time.Time `db:"health_observed_at" json:"health_observed_at,omitempty"`
	CompletedAt       *time.Time `db:"completed_at" json:"completed_at,omitempty"`
	Version           int64      `db:"version" json:"version"`
	CreatedAt         time.Time  `db:"created_at" json:"created_at"`
	UpdatedAt         time.Time  `db:"updated_at" json:"updated_at"`
}

type RolloutClaimState string

const (
	RolloutClaimActive   RolloutClaimState = "active"
	RolloutClaimReleased RolloutClaimState = "released"
)

// RolloutClaim is operational ownership for one reconciliation pass. The
// opaque lease token, not worker identity alone, proves ownership.
type RolloutClaim struct {
	RolloutID    string            `db:"rollout_id" json:"rollout_id"`
	WorkerID     string            `db:"worker_id" json:"worker_id"`
	LeaseToken   string            `db:"lease_token" json:"-"`
	State        RolloutClaimState `db:"state" json:"state"`
	LeaseExpires time.Time         `db:"lease_expires_at" json:"lease_expires_at"`
	HeartbeatAt  time.Time         `db:"heartbeat_at" json:"heartbeat_at"`
	AvailableAt  time.Time         `db:"available_at" json:"available_at"`
	Attempt      int               `db:"attempt" json:"attempt"`
	Version      int64             `db:"version" json:"version"`
	CreatedAt    time.Time         `db:"created_at" json:"created_at"`
	UpdatedAt    time.Time         `db:"updated_at" json:"updated_at"`
	Reclaimed    bool              `db:"-" json:"-"`
}

type RolloutLeaseStatus struct {
	Current        bool         `db:"current" json:"current"`
	RolloutVersion int64        `db:"rollout_version" json:"rollout_version"`
	State          RolloutState `db:"rollout_state" json:"rollout_state"`
}

type WorkerReleaseStatus struct {
	WorkerID              string     `db:"worker_id" json:"worker_id"`
	Cohort                string     `db:"cohort" json:"cohort"`
	DesiredReleaseID      string     `db:"desired_release_id" json:"desired_release_id,omitempty"`
	ObservedReleaseID     string     `db:"observed_release_id" json:"observed_release_id,omitempty"`
	CachedDigestsJSON     string     `db:"cached_digests_json" json:"cached_digests"`
	VerificationState     string     `db:"verification_state" json:"verification_state"`
	VerificationError     string     `db:"verification_error" json:"verification_error,omitempty"`
	CapabilitiesJSON      string     `db:"capabilities_json" json:"capabilities"`
	AssignmentOperationID string     `db:"assignment_operation_id" json:"assignment_operation_id,omitempty"`
	AssignedAt            *time.Time `db:"assigned_at" json:"assigned_at,omitempty"`
	EvidenceObservedAt    *time.Time `db:"evidence_observed_at" json:"evidence_observed_at,omitempty"`
	Version               int64      `db:"version" json:"version"`
	LastHeartbeat         time.Time  `db:"last_heartbeat" json:"last_heartbeat"`
	CreatedAt             time.Time  `db:"created_at" json:"created_at"`
	UpdatedAt             time.Time  `db:"updated_at" json:"updated_at"`
}

type Event struct {
	ID                string    `db:"id" json:"id"`
	AggregateType     string    `db:"aggregate_type" json:"aggregate_type"`
	AggregateID       string    `db:"aggregate_id" json:"aggregate_id"`
	Sequence          int64     `db:"sequence" json:"sequence"`
	EventType         string    `db:"event_type" json:"event_type"`
	PriorState        string    `db:"prior_state" json:"prior_state,omitempty"`
	NewState          string    `db:"new_state" json:"new_state,omitempty"`
	Actor             string    `db:"actor" json:"actor"`
	Reason            string    `db:"reason" json:"reason,omitempty"`
	PolicyRevision    int64     `db:"policy_revision" json:"policy_revision,omitempty"`
	IdempotencyKey    string    `db:"idempotency_key" json:"idempotency_key"`
	PayloadJSON       string    `db:"payload_json" json:"payload"`
	TraceID           string    `db:"trace_id" json:"trace_id,omitempty"`
	OperationID       string    `db:"operation_id" json:"operation_id,omitempty"`
	ParentOperationID string    `db:"parent_operation_id" json:"parent_operation_id,omitempty"`
	CreatedAt         time.Time `db:"created_at" json:"created_at"`
}

// OperationCorrelation binds a durable aggregate to the API/scheduler
// operation that created it. Values are opaque, bounded identifiers and never
// carry actors, URLs, source names, or credentials.
type OperationCorrelation struct {
	AggregateType     string    `db:"aggregate_type" json:"aggregate_type"`
	AggregateID       string    `db:"aggregate_id" json:"aggregate_id"`
	TraceID           string    `db:"trace_id" json:"trace_id"`
	OperationID       string    `db:"operation_id" json:"operation_id"`
	ParentOperationID string    `db:"parent_operation_id" json:"parent_operation_id,omitempty"`
	OriginComponent   string    `db:"origin_component" json:"origin_component"`
	CreatedAt         time.Time `db:"created_at" json:"created_at"`
}

type NotificationState string

const (
	NotificationPending    NotificationState = "pending"
	NotificationDelivering NotificationState = "delivering"
	NotificationRetry      NotificationState = "retry"
	NotificationDelivered  NotificationState = "delivered"
	NotificationDeadLetter NotificationState = "dead_letter"
)

type NotificationDestinationType string

const (
	NotificationDestinationUI      NotificationDestinationType = "ui"
	NotificationDestinationWebhook NotificationDestinationType = "webhook"
	NotificationDestinationEmail   NotificationDestinationType = "email"
	NotificationDestinationSIEM    NotificationDestinationType = "siem"
)

// Notification is both the UI-visible notification record and the durable
// external-delivery outbox. Endpoint addresses and credentials are deliberately
// absent; DestinationRef is an opaque alias resolved by the selected adapter.
type Notification struct {
	ID               string                      `db:"id" json:"id"`
	EventID          string                      `db:"event_id" json:"event_id"`
	AggregateType    string                      `db:"aggregate_type" json:"aggregate_type"`
	AggregateID      string                      `db:"aggregate_id" json:"aggregate_id"`
	EventType        string                      `db:"event_type" json:"event_type"`
	NotificationType string                      `db:"notification_type" json:"notification_type"`
	DestinationType  NotificationDestinationType `db:"destination_type" json:"destination_type"`
	DestinationRef   string                      `db:"destination_ref" json:"destination_ref"`
	PolicyID         string                      `db:"policy_id" json:"policy_id,omitempty"`
	PolicyRevision   int64                       `db:"policy_revision" json:"policy_revision,omitempty"`
	State            NotificationState           `db:"state" json:"state"`
	PayloadJSON      string                      `db:"payload_json" json:"payload"`
	Attempt          int                         `db:"attempt" json:"attempt"`
	MaxAttempts      int                         `db:"max_attempts" json:"max_attempts"`
	AvailableAt      time.Time                   `db:"available_at" json:"available_at"`
	WorkerID         string                      `db:"worker_id" json:"worker_id,omitempty"`
	LeaseToken       string                      `db:"lease_token" json:"-"`
	LeaseExpiresAt   *time.Time                  `db:"lease_expires_at" json:"lease_expires_at,omitempty"`
	HeartbeatAt      *time.Time                  `db:"heartbeat_at" json:"heartbeat_at,omitempty"`
	DeliveredAt      *time.Time                  `db:"delivered_at" json:"delivered_at,omitempty"`
	DeadLetteredAt   *time.Time                  `db:"dead_lettered_at" json:"dead_lettered_at,omitempty"`
	ErrorClass       string                      `db:"error_class" json:"error_class,omitempty"`
	ErrorDetail      string                      `db:"error_detail" json:"error_detail,omitempty"`
	Version          int64                       `db:"version" json:"version"`
	CreatedAt        time.Time                   `db:"created_at" json:"created_at"`
	UpdatedAt        time.Time                   `db:"updated_at" json:"updated_at"`
}

type NotificationLeaseStatus struct {
	Current bool              `db:"current" json:"current"`
	State   NotificationState `db:"state" json:"state"`
	Version int64             `db:"version" json:"version"`
}

type NotificationReclaimSummary struct {
	Retried      int `json:"retried"`
	DeadLettered int `json:"dead_lettered"`
}

type NotificationCounts struct {
	Pending    int `json:"pending"`
	Delivering int `json:"delivering"`
	Retry      int `json:"retry"`
	Delivered  int `json:"delivered"`
	DeadLetter int `json:"dead_letter"`
}

type AlertKind string

const (
	AlertMissedDiscovery     AlertKind = "missed_discovery"
	AlertStaleStableRelease  AlertKind = "stale_stable_release"
	AlertQueueBacklog        AlertKind = "queue_backlog"
	AlertLeaseChurn          AlertKind = "lease_churn"
	AlertRepeatedGateFailure AlertKind = "repeated_gate_failure"
	AlertMirrorDrift         AlertKind = "mirror_drift"
	AlertRolloutFailure      AlertKind = "rollout_failure"
	AlertSignatureHealth     AlertKind = "signature_health"
)

type AlertSeverity string

const (
	AlertWarning  AlertSeverity = "warning"
	AlertCritical AlertSeverity = "critical"
)

type AlertState string

const (
	AlertOpen     AlertState = "open"
	AlertResolved AlertState = "resolved"
)

type Alert struct {
	ID               string        `db:"id" json:"id"`
	Fingerprint      string        `db:"fingerprint" json:"fingerprint"`
	Kind             AlertKind     `db:"kind" json:"kind"`
	Severity         AlertSeverity `db:"severity" json:"severity"`
	State            AlertState    `db:"state" json:"state"`
	ScopeType        string        `db:"scope_type" json:"scope_type"`
	ScopeID          string        `db:"scope_id" json:"scope_id"`
	Summary          string        `db:"summary" json:"summary"`
	EvidenceJSON     string        `db:"evidence_json" json:"evidence"`
	PolicyID         string        `db:"policy_id" json:"policy_id,omitempty"`
	PolicyScope      string        `db:"policy_scope" json:"policy_scope"`
	PolicyRevision   int64         `db:"policy_revision" json:"policy_revision,omitempty"`
	TriggerCount     int           `db:"trigger_count" json:"trigger_count"`
	Generation       int           `db:"generation" json:"generation"`
	Version          int64         `db:"version" json:"version"`
	FirstTriggeredAt time.Time     `db:"first_triggered_at" json:"first_triggered_at"`
	LastTriggeredAt  time.Time     `db:"last_triggered_at" json:"last_triggered_at"`
	ResolvedAt       *time.Time    `db:"resolved_at" json:"resolved_at,omitempty"`
	CreatedAt        time.Time     `db:"created_at" json:"created_at"`
	UpdatedAt        time.Time     `db:"updated_at" json:"updated_at"`
}

type AlertFilter struct {
	State    AlertState
	Kind     AlertKind
	Severity AlertSeverity
}

type AlertPage struct {
	Items      []Alert
	NextCursor string
}

type AlertCounts struct {
	OpenWarning  int `json:"open_warning"`
	OpenCritical int `json:"open_critical"`
	Resolved     int `json:"resolved"`
}

type AlertDurationThreshold struct {
	Enabled bool
	After   time.Duration
}

type AlertQueueThreshold struct {
	Enabled  bool
	MaxDepth int
	MaxAge   time.Duration
}

type AlertCountThreshold struct {
	Enabled bool
	Count   int
	Window  time.Duration
}

type AlertEvaluationRequest struct {
	PolicyID            string
	PolicyScope         string
	PolicyRevision      int64
	MissedDiscovery     AlertDurationThreshold
	StaleStableRelease  AlertDurationThreshold
	QueueBacklog        AlertQueueThreshold
	LeaseChurn          AlertCountThreshold
	RepeatedGateFailure AlertCountThreshold
	MirrorDrift         bool
	RolloutFailure      bool
	SignatureHealth     bool
}

type AlertEvaluationSummary struct {
	Opened   int         `json:"opened"`
	Reopened int         `json:"reopened"`
	Resolved int         `json:"resolved"`
	Active   AlertCounts `json:"active"`
}

type LeaseState string

const (
	LeaseActive    LeaseState = "active"
	LeaseCompleted LeaseState = "completed"
	LeaseFailed    LeaseState = "failed"
)

type ScheduleLease struct {
	ScheduleKey  string     `db:"schedule_key" json:"schedule_key"`
	PeriodKey    string     `db:"period_key" json:"period_key"`
	Owner        string     `db:"owner" json:"owner"`
	Token        string     `db:"lease_token" json:"-"`
	State        LeaseState `db:"state" json:"state"`
	LeaseExpires time.Time  `db:"lease_expires_at" json:"lease_expires_at"`
	HeartbeatAt  time.Time  `db:"heartbeat_at" json:"heartbeat_at"`
	CompletedAt  *time.Time `db:"completed_at" json:"completed_at,omitempty"`
	ResultRef    string     `db:"result_ref" json:"result_ref,omitempty"`
	Version      int64      `db:"version" json:"version"`
	CreatedAt    time.Time  `db:"created_at" json:"created_at"`
	UpdatedAt    time.Time  `db:"updated_at" json:"updated_at"`
}

// TransitionCommand is persisted alongside every state change.
type TransitionCommand struct {
	Actor             string
	Reason            string
	PolicyRevision    int64
	IdempotencyKey    string
	PayloadJSON       string
	TraceID           string
	OperationID       string
	ParentOperationID string
	OriginComponent   string
}

type ReleaseInventory struct {
	Release   Release
	Tools     []ReleaseTool
	Images    []ReleaseImage
	Artifacts []ReleaseArtifact
}

type PageRequest struct {
	Limit  int
	Cursor string
}

type DiscoveryFilter struct {
	State   DiscoveryState
	Trigger DiscoveryTrigger
}

type CandidateFilter struct {
	State CandidateState
}

type ReleaseFilter struct {
	State     ReleaseState
	Protected *bool
}

type RolloutFilter struct {
	State  RolloutState
	Target string
}

type EventFilter struct {
	AggregateType string
	EventType     string
	Actor         string
	TraceID       string
	OperationID   string
}

type NotificationFilter struct {
	State            NotificationState
	DestinationType  NotificationDestinationType
	NotificationType string
}

type DiscoveryPage struct {
	Items      []DiscoveryRun
	NextCursor string
}

type CandidatePage struct {
	Items      []Candidate
	NextCursor string
}

type ReleasePage struct {
	Items      []Release
	NextCursor string
}

type RolloutPage struct {
	Items      []Rollout
	NextCursor string
}

type EventPage struct {
	Items      []Event
	NextCursor string
}

type NotificationPage struct {
	Items      []Notification
	NextCursor string
}
