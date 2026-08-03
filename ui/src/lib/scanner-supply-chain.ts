import { ApiError, api } from "./api";
import type { Scan } from "./types";
import { rememberOperationReceipt } from "./operation-receipts";

export const SCANNER_SUPPLY_CHAIN_PATH = "/v1/scanner-supply-chain";

export type Risk = "none" | "low" | "medium" | "high" | "critical";
export type HealthState = "healthy" | "degraded" | "failed" | "unknown";

export interface Page<T> {
  items: T[];
  next_cursor?: string;
  total?: number;
}

export interface OperationReceipt {
  id: string;
  state: string;
  status_url?: string;
  events_url?: string;
}

function trackOperation<T>(value: T, label: string): T {
  if (value && typeof value === "object") {
    rememberOperationReceipt(value as unknown as OperationReceipt, label);
  }
  return value;
}

export interface ScheduleSummary {
  last_success_at?: string;
  next_run_at?: string;
  stale?: boolean;
  state?: string;
}

export interface ReleaseSummary {
  id: string;
  name: string;
  state: string;
  channels?: string[];
  definition_commit?: string;
  lock_digest?: string;
  manifest_digest?: string;
  signer_identity?: string;
  platforms?: string[];
  rollout_coverage?: number;
  published_at: string;
  deprecated_at?: string;
  revoked_at?: string;
  rollback_eligible?: boolean;
  protected?: boolean;
  legacy?: boolean;
  imported?: boolean;
  retention_class?: string;
  version?: number;
  policy_revision?: number;
}

export interface LegacyScannerConfig {
  image: string;
  image_overrides: Record<string, string> | null;
}

export interface LegacyScannerTool {
  name: string;
  display_name?: string;
  integration_tier: string;
  configured_image?: string;
}

export interface LegacyConfigurationSnapshot {
  config: LegacyScannerConfig;
  tools: LegacyScannerTool[];
}

export interface LegacyReleaseImportResult {
  release: ReleaseSummary;
  images: ReleaseImage[];
  created: boolean;
  provenance_limitations: string[];
  runtime_assignments_changed: false;
}

export type SignerProvider =
  | "aws_kms"
  | "gcp_kms"
  | "azure_key_vault"
  | "pkcs11"
  | "keyless"
  | "offline"
  | "managed_keyless";

export type SignerState = "active" | "disabled" | "revoked";

export interface SignerProfile {
  id: string;
  name: string;
  provider: SignerProvider;
  algorithm: string;
  key_reference: string;
  secret_reference?: string;
  secret_reference_configured: boolean;
  workload_identity: boolean;
  identity: string;
  issuer: string;
  subject: string;
  trust_root_reference: string;
  state: SignerState;
  revision: number;
  rotated_from_id?: string;
  revocation_reason?: string;
  revoked_by?: string;
  revoked_at?: string;
  created_by: string;
  created_at: string;
  updated_at: string;
}

export interface SignerProfileInput {
  name: string;
  provider: Exclude<SignerProvider, "managed_keyless">;
  algorithm: string;
  key_reference: string;
  secret_reference?: string;
  workload_identity: boolean;
  identity: string;
  issuer: string;
  subject: string;
  trust_root_reference: string;
}

export interface SignerProfileDetail {
  signer: SignerProfile;
  etag?: string;
}

export interface CohortSummary {
  id?: string;
  name: string;
  desired_release_id?: string;
  observed_release_id?: string;
  state: string;
  total_workers: number;
  ready_workers: number;
  failed_workers: number;
  health_summary?: Record<string, unknown> | string;
  deadline?: string;
}

export interface CandidateSummary {
  id: string;
  state: string;
  risk?: Risk;
  risk_summary?: RiskSummary | string;
  definition_commit: string;
  proposed_commit?: string;
  proposal_url?: string;
  lock_digest?: string;
  lock_uri?: string;
  policy_decision?: string;
  publication_receipt_digest?: string;
  policy_revision: number;
  actor?: string;
  version?: number;
  created_at: string;
  updated_at: string;
  error_class?: string;
  error_detail?: string;
  selection?: CandidateSelectionSummary;
}

export type CandidateRebuildReason =
  | "no_stable_release"
  | "maximum_stable_image_age_exceeded"
  | "policy_forced_weekly_rebuild"
  | "stable_release_within_maximum_age";

export interface CandidateSelectionSummary {
  force_rebuild: boolean;
  rebuild_reason?: CandidateRebuildReason;
  no_op_if_unchanged: boolean;
}

export interface RiskSummary {
  highest_risk?: Risk;
  risk?: Risk;
  reasons?: string[];
  changed_components?: number;
}

export interface RolloutSummary {
  id: string;
  target: string;
  from_release_id?: string;
  to_release_id: string;
  strategy?: string;
  state: string;
  version?: number;
  error_class?: string;
  error_detail?: string;
  started_at?: string;
  completed_at?: string;
  created_at: string;
  updated_at: string;
  cohorts?: CohortSummary[];
  health?: CanaryHealth;
  automatic_rollback?: boolean;
}

export interface CanaryHealth {
  outcome?: string;
  samples?: number;
  minimum_samples?: number;
  infrastructure_failures?: number;
  parser_failures?: number;
  pull_failures?: number;
  signature_failures?: number;
  manifest_failures?: number;
  crash_loops?: number;
  candidate_p95_duration_ms?: number;
  stable_p95_duration_ms?: number;
  reasons?: string[];
}

export interface RegistrySummary {
  id: string;
  name: string;
  type: "managed" | "mirror" | "private" | "air_gap" | string;
  host: string;
  namespace: string;
  enabled: boolean;
  credential_reference_configured?: boolean;
  credential_reference_kind?: "wolf_secret" | string;
  trust_policy_reference?: string;
  platform_policy?: Record<string, unknown> | string;
  version?: number;
  health?: HealthState;
  last_checked_at?: string;
  mirror_lag_seconds?: number;
  digest_parity?: boolean;
  permissions?: string[];
  signer_identity?: string;
  retention?: string;
  protected_releases?: number;
  error?: string;
  updated_at?: string;
}

export interface RegistryInput {
  name?: string;
  type?: RegistrySummary["type"];
  host?: string;
  namespace?: string;
  secret_reference?: string;
  trust_policy_reference?: string;
  platform_policy?: Record<string, unknown>;
  enabled?: boolean;
}

export type RegistryJobKind = "reconcile" | "repair" | "cleanup";
export type RegistryJobState =
  | "queued"
  | "claimed"
  | "retry"
  | "completed"
  | "dead_letter"
  | "cancelled";
export type RegistryReSignPolicy = "preserve" | "required" | "forbidden";

// Deliberately omits summary, lease token, and idempotency key. Registry job
// summaries are worker-owned JSON and are not an appropriate UI contract.
export interface RegistryJob {
  id: string;
  registry_target_id: string;
  source_registry_target_id?: string;
  release_id?: string;
  kind: RegistryJobKind;
  re_sign_policy: RegistryReSignPolicy;
  state: RegistryJobState;
  actor: string;
  reason: string;
  attempt: number;
  max_attempts: number;
  available_at: string;
  worker_id?: string;
  lease_expires_at?: string;
  heartbeat_at?: string;
  error_class?: string;
  version: number;
  started_at?: string;
  completed_at?: string;
  dead_lettered_at?: string;
  created_at: string;
  updated_at: string;
}

export interface RegistryImageObservation {
  id: string;
  job_id: string;
  image_key: string;
  source_reference?: string;
  destination_reference: string;
  expected_digest: string;
  source_digest?: string;
  destination_digest?: string;
  expected_signature_digest?: string;
  destination_signature_digest?: string;
  expected_provenance_digest?: string;
  destination_provenance_digest?: string;
  expected_sbom_digest?: string;
  destination_sbom_digest?: string;
  state: string;
  checked_at: string;
  created_at: string;
  updated_at: string;
}

export interface RegistryJobDetail {
  job: RegistryJob;
  images: RegistryImageObservation[];
  events_url: string;
  etag?: string;
}

export interface RegistryJobFilters {
  registry_target_id?: string;
  release_id?: string;
  state?: RegistryJobState | "";
  kind?: RegistryJobKind | "";
  cursor?: string;
  limit?: number;
}

export interface RegistryJobInput {
  kind: Exclude<RegistryJobKind, "cleanup">;
  release_id: string;
  source_registry_id?: string;
  re_sign_policy?: RegistryReSignPolicy;
  reason: string;
  max_attempts?: number;
}

export type RegistryQuarantineState =
  | "quarantined"
  | "promoted"
  | "orphaned"
  | "deleting"
  | "deleted"
  | "retained"
  | "delete_failed";

// Worker metadata and error_detail are intentionally excluded. They are
// unstructured backend fields and may contain implementation-only context.
export interface RegistryQuarantineObject {
  id: string;
  registry_target_id: string;
  candidate_id?: string;
  repository: string;
  digest: string;
  object_kind: string;
  state: RegistryQuarantineState;
  protected: boolean;
  retention_class: string;
  retain_until?: string;
  discovered_at: string;
  last_referenced_at?: string;
  deletion_lease_expires_at?: string;
  deletion_verified_at?: string;
  version: number;
  created_at: string;
  updated_at: string;
}

export interface Overview {
  capabilities?: ScannerReleaseCapabilities;
  active_release?: ReleaseSummary;
  stable_release?: ReleaseSummary;
  stable_release_age_seconds?: number;
  desired_release_id?: string;
  cohorts?: CohortSummary[];
  freshness?: {
    status?: string;
    age_seconds?: number;
    current: number;
    updates_available: number;
    incomplete: number;
    failed: number;
    total: number;
    last_checked_at?: string;
  };
  latest_discovery?: DiscoveryRun;
  worker_health?: {
    desired_release_id?: string;
    cohorts?: CohortSummary[];
    ready?: number;
    total?: number;
    drifted?: number;
    active?: number;
    workers?: Array<{
      worker_id: string;
      cohort: string;
      desired_release_id?: string;
      observed_release_id?: string;
      verification_state?: string;
      verification_error?: string;
      last_heartbeat?: string;
    }>;
  };
  registry_health?: {
    healthy?: number;
    degraded?: number;
    failed?: number;
    total?: number;
    configured?: number;
    targets?: RegistrySummary[];
  };
  pending_updates?: Partial<Record<Risk, number>>;
  pending_candidate?: CandidateSummary;
  active_rollout?: RolloutSummary;
  registries?: {
    healthy: number;
    degraded: number;
    failed: number;
    total: number;
  };
  discovery_schedule?: ScheduleSummary;
  candidate_schedule?: ScheduleSummary;
  alerts?: SupplyChainAlert[] | ScannerAlertCounts;
  alert_health?: "healthy" | "warning" | "critical" | string;
  partial_failures?: ResourceFailure[];
  generated_at?: string;
}

export type ReleaseFactoryComponentName =
  | "alert"
  | "scheduler"
  | "discovery"
  | "proposal"
  | "build"
  | "rollout"
  | "notification"
  | "registry"
  | "fixed"
  | "quality"
  | "integration";

export interface ReleaseFactoryComponentHealth {
  component: ReleaseFactoryComponentName;
  enabled: boolean;
  status: string;
  ready: boolean;
  last_activity?: string;
  last_success?: string;
  stuck_work?: Record<string, number>;
  queue_depth?: Record<string, number>;
  run_counts?: Record<string, number>;
  result_counts?: Record<string, number>;
  average_run_duration_ms?: number;
}

export interface ReleaseFactoryHealth {
  status: string;
  ready: boolean;
  database: string;
  uptime_ms: number;
  components: ReleaseFactoryComponentHealth[];
}

export interface SystemHealth {
  status: string;
  uptime_ms: number;
  release_factory: ReleaseFactoryHealth;
}

export type ScannerReleaseMode =
  | "disabled"
  | "read_only"
  | "candidate"
  | "canary"
  | "stable_control";

export interface ScannerReleaseCapabilities {
  mode: ScannerReleaseMode;
  read: boolean;
  candidates: boolean;
  canary: boolean;
  stable_control: boolean;
}

export interface SupplyChainAlert {
  id?: string;
  severity: "info" | "warning" | "critical" | string;
  title: string;
  detail?: string;
  resource_type?: string;
  resource_id?: string;
}

export type ScannerAlertKind =
  | "missed_discovery"
  | "stale_stable_release"
  | "queue_backlog"
  | "lease_churn"
  | "repeated_gate_failure"
  | "mirror_drift"
  | "rollout_failure"
  | "signature_health";

export type ScannerAlertSeverity = "warning" | "critical";
export type ScannerAlertState = "open" | "resolved";

export interface ScannerAlert {
  id: string;
  fingerprint: string;
  kind: ScannerAlertKind;
  severity: ScannerAlertSeverity;
  state: ScannerAlertState;
  scope_type: string;
  scope_id: string;
  summary: string;
  evidence: Record<string, unknown>;
  policy_id?: string;
  policy_revision?: number;
  trigger_count: number;
  generation: number;
  version: number;
  first_triggered_at: string;
  last_triggered_at: string;
  resolved_at?: string;
  created_at: string;
  updated_at: string;
}

export interface ScannerAlertCounts {
  open_warning: number;
  open_critical: number;
  resolved: number;
}

export interface ScannerAlertFilters {
  state?: ScannerAlertState | "all";
  kind?: ScannerAlertKind;
  severity?: ScannerAlertSeverity;
  cursor?: string;
  limit?: number;
}

export interface ResourceFailure {
  resource: string;
  message: string;
  retryable?: boolean;
}

export interface DiscoveryRun {
  id: string;
  trigger: string;
  schedule_period?: string;
  definition_commit: string;
  policy_revision: number;
  state: string;
  available_count: number;
  selected_count: number;
  error_class?: string;
  error_detail?: string;
  actor?: string;
  version?: number;
  started_at?: string;
  completed_at?: string;
  created_at: string;
  updated_at: string;
}

export interface UpdateItem {
  id: string;
  discovery_run_id: string;
  component_type: string;
  component_name: string;
  current_value: string;
  available_value: string;
  source_evidence?: SourceEvidence | string;
  risk_class: Risk;
  compatibility?: Compatibility | string;
  selection_state: string;
  integration_tier?: string;
  source?: string;
  last_checked_at?: string;
  updated_at?: string;
}

export interface SourceEvidence {
  source?: string;
  url?: string;
  checked_at?: string;
  digest_before?: string;
  digest_after?: string;
  error?: string;
  coverage?: string;
  version_status?: string;
}

export interface Compatibility {
  compatible?: boolean;
  reasons?: string[];
  required_gates?: string[];
}

export interface GateEvidence {
  name: string;
  state: string;
  summary?: string;
  evidence_digest?: string;
  evidence_uri?: string;
  started_at?: string;
  completed_at?: string;
  excepted?: boolean;
}

export interface BuildStep {
  id: string;
  step_key: string;
  state: string;
  attempt?: number;
  output_uri?: string;
  output_digest?: string;
  summary?: Record<string, unknown> | string;
  error_class?: string;
  error_detail?: string;
  started_at?: string;
  completed_at?: string;
}

export interface Approval {
  id: string;
  actor: string;
  action: string;
  reason: string;
  exception_scope?: string;
  exception_owner_id?: string;
  compensating_control?: string;
  evidence_digest?: string;
  policy_decision?: string;
  expires_at?: string;
  created_at: string;
}

export interface CandidateExceptionInput {
  gate: string;
  owner_id: string;
  reason: string;
  compensating_control: string;
  evidence_digest: string;
  expires_at: string;
}

export interface ComparisonDelta {
  status?: string;
  summary?: string;
  baseline?: number | string;
  candidate?: number | string;
  delta?: number | string;
  items?: Array<Record<string, unknown>>;
}

export interface CandidateDetail extends CandidateSummary {
  changes?: UpdateItem[];
  gates?: GateEvidence[];
  build_steps?: BuildStep[];
  approvals?: Approval[];
  required_gates?: string[] | string;
  logs?: LogEntry[];
  comparisons?: {
    findings?: ComparisonDelta;
    vulnerabilities?: ComparisonDelta;
    licenses?: ComparisonDelta;
    performance?: ComparisonDelta;
  };
  signature?: VerificationSummary;
  provenance?: VerificationSummary;
  separation_of_duties?: {
    creator?: string;
    current_actor_can_approve?: boolean;
    reason?: string;
    required_approvals?: number;
    valid_approvals?: number;
  };
}

export interface LogEntry {
  id?: string;
  sequence: number;
  timestamp: string;
  level?: string;
  step?: string;
  message: string;
  redacted?: boolean;
}

export interface VerificationSummary {
  state: string;
  identity?: string;
  digest?: string;
  issuer?: string;
  checked_at?: string;
  detail?: string;
  total_count?: number;
  verified_count?: number;
  failed_count?: number;
  pending_count?: number;
  keys?: string[];
  digests?: string[];
}

export interface ReleaseTool {
  id?: string;
  tool_key: string;
  version: string;
  source_reference?: string;
  source_digest?: string;
  checksum?: string;
  parser_compatibility?: string;
}

export interface ReleaseImage {
  id?: string;
  image_key: string;
  image_kind?: "scanner" | "fixer";
  registry_target_id?: string;
  repository: string;
  digest: string;
  platform_digests?: Record<string, string> | string;
  size_bytes?: number;
  signature_status?: string;
  signature_digest?: string;
  signature_artifact_uri?: string;
  signature_artifact_digest?: string;
  signature_media_type?: string;
  signature_artifact_size_bytes?: number;
  signature_certificate_digest?: string;
  signature_identity?: string;
  signature_issuer?: string;
  signature_subject?: string;
  signature_trust_root?: string;
  signature_operation_id?: string;
  provenance_digest?: string;
  sbom_digest?: string;
}

export interface ReleaseArtifact {
  id: string;
  artifact_type: string;
  media_type?: string;
  uri: string;
  digest: string;
  size_bytes?: number;
  protected?: boolean;
}

export type ArtifactDiffOwner = "candidate" | "release";
export type ArtifactDiffKind = "manifest" | "lock";

export interface ArtifactDiff {
  owner_type: ArtifactDiffOwner;
  owner_id: string;
  kind: ArtifactDiffKind;
  format: "unified";
  available: boolean;
  content: string;
  truncated: boolean;
  total_bytes: number;
  returned_bytes: number;
  total_lines: number;
  returned_lines: number;
  digest?: string;
  media_type?: string;
}

export interface ReleaseDetail extends ReleaseSummary {
  tools?: ReleaseTool[];
  images?: ReleaseImage[];
  artifacts?: ReleaseArtifact[];
  approvals?: Approval[];
  deployments?: RolloutSummary[];
  verification?: {
    registry?: VerificationSummary;
    signature?: VerificationSummary;
    provenance?: VerificationSummary;
    mirrors?: VerificationSummary;
  };
}

export interface ReleaseComparison {
  from_release: ReleaseSummary;
  to_release: ReleaseSummary;
  tools?: Array<{
    key: string;
    from?: string;
    to?: string;
    change: string;
  }>;
  images?: Array<{
    key: string;
    from?: string;
    to?: string;
    digest_changed: boolean;
  }>;
  policy_revision_changed?: boolean;
  summary?: string;
}

export interface RolloutEvent {
  id?: string;
  aggregate_type?: string;
  aggregate_id?: string;
  sequence: number;
  event_type: string;
  prior_state?: string;
  new_state?: string;
  actor?: string;
  reason?: string;
  payload?: Record<string, unknown> | string;
  trace_id?: string;
  operation_id?: string;
  parent_operation_id?: string;
  created_at: string;
}

export interface RolloutDetail extends RolloutSummary {
  cohorts: CohortSummary[];
  events?: RolloutEvent[];
  synthetic_health?: SyntheticRolloutHealth;
  real_scan_health?: RealScanRolloutHealth;
  maintenance_window?: {
    open: boolean;
    name?: string;
    next_open_at?: string;
  };
  affected_workers?: number;
  recommendation?: string;
}

export interface SyntheticRolloutHealth {
  corpus_id: string;
  corpus_digest: string;
  current: boolean;
  state: "pending" | "passed" | "failed";
  fixture_total: number;
  fixture_passed: number;
  fixture_failed: number;
  failure_class?: string;
  observed_at: string;
}

export interface RealScanRolloutHealth {
  state: "pending" | "healthy" | "degraded";
  candidate_samples: number;
  stable_samples: number;
  candidate_infrastructure_failures: number;
  stable_infrastructure_failures: number;
  parser_failures: number;
  expected_finding_losses: number;
  candidate_p95_duration_ms: number;
  stable_p95_duration_ms: number;
  workers_total: number;
  workers_ready: number;
  workers_failed: number;
  observed_at: string;
}

export interface PolicySchedule {
  timezone?: string;
  maximum_stable_image_age?: string;
  force_weekly_rebuild?: boolean;
  daily_discovery?: {
    enabled?: boolean;
    frequency?: "daily" | string;
    at?: string;
    jitter?: string;
    catch_up?: string;
  };
  weekly_candidate?: {
    enabled?: boolean;
    frequency?: "weekly" | string;
    weekday?: string;
    at?: string;
    jitter?: string;
    catch_up?: string;
  };
  discovery_cron?: string;
  candidate_cron?: string;
  jitter?: string;
  catch_up_window?: string;
  maintenance_windows?: Array<{
    id?: string;
    name: string;
    cron?: string;
    duration?: string;
  }>;
}

export interface PolicyRules {
  schema_version?: string;
  revision?: number;
  approval_mode?: "manual" | "policy_gated";
  required_approvals?: number;
  separate_creator?: boolean;
  auto_promote_risks?: Risk[];
  auto_promote_changes?: string[];
  required_gates?: string[];
  allow_exceptions?: Record<string, boolean>;
  exception_max_age?: string;
  canary?: {
    size?: number;
    minimum_samples?: number;
    observation?: string;
  };
  rollback?: {
    automatic?: boolean;
    max_infrastructure_failure_rate?: number;
    max_duration_regression?: number;
    max_parser_failures?: number;
  };
  retention?: {
    artifacts?: string;
    logs?: string;
  };
  notifications?: {
    destinations?: string[];
  };
}

export interface ScannerPolicy {
  id: string;
  scope: string;
  revision: number;
  enabled: boolean;
  schedule: PolicySchedule | string;
  rules: PolicyRules | string;
  created_by?: string;
  created_at?: string;
  updated_at?: string;
  etag?: string;
  validation?: {
    valid: boolean;
    errors?: string[];
    warnings?: string[];
    next_execution?: {
      daily_discovery?: string;
      weekly_candidate?: string;
      maintenance_windows?: Array<{
        id?: string;
        name: string;
        at: string;
        duration: string;
      }>;
    };
  };
  diff?: string;
}

export interface PolicyRevision extends ScannerPolicy {
  activated_at?: string;
}

export interface PolicyDryRun {
  candidate_id: string;
  outcome: string;
  auto_promotion?: boolean;
  blocking_reasons?: string[];
  advisories?: string[];
  policy_decision_digest?: string;
}

export interface AuditEvent {
  id: string;
  sequence?: number;
  aggregate_type: string;
  aggregate_id: string;
  trace_id?: string;
  operation_id?: string;
  parent_operation_id?: string;
  event_type: string;
  prior_state?: string;
  new_state?: string;
  actor?: string;
  reason?: string;
  policy_revision?: number;
  payload?: Record<string, unknown> | string;
  created_at: string;
}

export interface AuditFilters {
  aggregate_type?: string;
  event_type?: string;
  actor?: string;
  trace_id?: string;
  operation_id?: string;
  cursor?: string;
}

export type ScannerNotificationState =
  | "pending"
  | "delivering"
  | "retry"
  | "delivered"
  | "dead_letter";

export type ScannerNotificationDestination =
  | "ui"
  | "webhook"
  | "email"
  | "siem";

export interface ScannerNotification {
  id: string;
  event_id: string;
  aggregate_type: string;
  aggregate_id: string;
  event_type: string;
  notification_type: string;
  destination_type: ScannerNotificationDestination;
  destination_ref: string;
  policy_id?: string;
  policy_revision?: number;
  state: ScannerNotificationState;
  payload: string;
  attempt: number;
  max_attempts: number;
  available_at: string;
  worker_id?: string;
  lease_expires_at?: string;
  heartbeat_at?: string;
  delivered_at?: string;
  dead_lettered_at?: string;
  error_class?: string;
  error_detail?: string;
  version: number;
  created_at: string;
  updated_at: string;
}

export interface ScannerNotificationFilters {
  state?: ScannerNotificationState;
  destination_type?: ScannerNotificationDestination;
  notification_type?: string;
  cursor?: string;
  limit?: number;
}

export interface ScannerNotificationDetail {
  notification: ScannerNotification;
  etag?: string;
}

function queryString(
  values: Record<string, string | number | undefined>,
): string {
  const params = new URLSearchParams();
  Object.entries(values).forEach(([key, value]) => {
    if (value !== undefined && value !== "") params.set(key, String(value));
  });
  const encoded = params.toString();
  return encoded ? `?${encoded}` : "";
}

function operationHeaders(version?: number | string): HeadersInit {
  const headers: Record<string, string> = {
    "Idempotency-Key": createIdempotencyKey(),
  };
  if (version !== undefined) headers["If-Match"] = String(version);
  return headers;
}

async function cursorPage<T>(path: string): Promise<Page<T>> {
  const result = await api.get<
    | Page<T>
    | T[]
    | { data?: T[]; meta?: { next_cursor?: string; total?: number } }
  >(path);
  if (Array.isArray(result.data)) {
    return {
      items: result.data,
      next_cursor: result.meta?.next_cursor,
      total: result.meta?.total,
    };
  }
  return normalizePage(result.data);
}

export function createIdempotencyKey(): string {
  const random =
    typeof crypto !== "undefined" && "randomUUID" in crypto
      ? crypto.randomUUID()
      : `${Date.now()}-${Math.random().toString(36).slice(2)}`;
  return `wolf-ui-${random}`;
}

export function isValidScannerTraceId(value: string): boolean {
  const normalized = value.trim();
  return (
    /^[0-9a-f]{32}$/.test(normalized) &&
    normalized !== "00000000000000000000000000000000"
  );
}

export function isValidScannerOperationId(value: string): boolean {
  return /^[A-Za-z0-9][A-Za-z0-9._:/-]{7,127}$/.test(value.trim());
}

export function parseJson<T>(
  value: T | string | null | undefined,
  fallback: T,
): T {
  if (typeof value !== "string") return value ?? fallback;
  try {
    return JSON.parse(value) as T;
  } catch {
    return fallback;
  }
}

export function normalizePage<T>(
  value:
    | Page<T>
    | T[]
    | {
        data?: T[];
        next_cursor?: string;
        total?: number;
        meta?: { next_cursor?: string; total?: number };
      }
    | null,
): Page<T> {
  if (Array.isArray(value)) return { items: value };
  if (!value) return { items: [] };
  if ("items" in value && Array.isArray(value.items)) return value;
  if (!("data" in value)) return { items: [] };
  return {
    items: Array.isArray(value.data) ? value.data : [],
    next_cursor: value.next_cursor ?? value.meta?.next_cursor,
    total: value.total ?? value.meta?.total,
  };
}

export function errorKind(
  error: unknown,
): "unauthorized" | "forbidden" | "unavailable" | "stale" | "unknown" {
  if (error instanceof ApiError) {
    if (error.status === 401) return "unauthorized";
    if (error.status === 403) return "forbidden";
    if (error.status === 404 || error.status === 501 || error.status === 503)
      return "unavailable";
    if (error.status === 409 || error.status === 412) return "stale";
  }
  return "unknown";
}

export const scannerSupplyChainApi = {
  overview: async () =>
    (await api.get<Overview>(`${SCANNER_SUPPLY_CHAIN_PATH}/overview`)).data,

  releaseFactoryHealth: async () =>
    (await api.get<SystemHealth>("/v1/health")).data.release_factory,

  discoveryRuns: async (
    filters: {
      state?: string;
      trigger?: string;
      cursor?: string;
      limit?: number;
    } = {},
  ) => {
    return cursorPage<DiscoveryRun>(
      `${SCANNER_SUPPLY_CHAIN_PATH}/discovery-runs${queryString(filters)}`,
    );
  },

  discoveryRun: async (id: string) => {
    const result = (
      await api.get<
        | (DiscoveryRun & { items?: UpdateItem[] })
        | { run: DiscoveryRun; items?: UpdateItem[] }
      >(`${SCANNER_SUPPLY_CHAIN_PATH}/discovery-runs/${encodeURIComponent(id)}`)
    ).data;
    return "run" in result ? { ...result.run, items: result.items } : result;
  },

  updates: async (
    filters: {
      q?: string;
      risk?: string;
      state?: string;
      source?: string;
      integration_tier?: string;
      cursor?: string;
      limit?: number;
    } = {},
  ) => {
    return cursorPage<UpdateItem>(
      `${SCANNER_SUPPLY_CHAIN_PATH}/updates${queryString(filters)}`,
    );
  },

  createDiscovery: async (
    scope: { type: string; items?: string[] },
    reason: string,
  ) => {
    const receipt = (
      await api.post<OperationReceipt>(
        `${SCANNER_SUPPLY_CHAIN_PATH}/discovery-runs`,
        { scope, reason },
        { headers: operationHeaders() },
      )
    ).data;
    return trackOperation(receipt, "Scanner update discovery");
  },

  createCandidate: async (
    updateItemIds: string[],
    reason: string,
    discoveryRunId?: string,
  ) => {
    const payload: Record<string, unknown> = {
      selected_items: updateItemIds,
      reason,
    };
    if (discoveryRunId) payload.discovery_run_id = discoveryRunId;
    const receipt = (
      await api.post<OperationReceipt>(
        `${SCANNER_SUPPLY_CHAIN_PATH}/candidates`,
        payload,
        { headers: operationHeaders() },
      )
    ).data;
    return trackOperation(receipt, "Scanner release candidate");
  },

  candidates: async (
    filters: {
      state?: string;
      cursor?: string;
      limit?: number;
    } = {},
  ) => {
    return cursorPage<CandidateSummary>(
      `${SCANNER_SUPPLY_CHAIN_PATH}/candidates${queryString(filters)}`,
    );
  },

  candidate: async (id: string): Promise<CandidateDetail> => {
    const result = (
      await api.get<
        | CandidateDetail
        | {
            candidate: CandidateSummary;
            builds?: Array<Record<string, unknown>>;
            steps?: BuildStep[] | Record<string, BuildStep[]>;
            artifacts?: ReleaseArtifact[];
            approvals?: Approval[];
          }
      >(`${SCANNER_SUPPLY_CHAIN_PATH}/candidates/${encodeURIComponent(id)}`)
    ).data;
    const steps =
      "candidate" in result && result.steps && !Array.isArray(result.steps)
        ? Object.values(result.steps).flat()
        : "candidate" in result
          ? result.steps
          : undefined;
    return (
      "candidate" in result
        ? {
            ...result.candidate,
            build_steps: steps,
            artifacts: result.artifacts,
            approvals: result.approvals,
          }
        : result
    ) as CandidateDetail;
  },

  candidateAction: async (
    id: string,
    action: "retry" | "cancel" | "approve" | "reject" | "publish",
    payload: Record<string, unknown>,
    version?: number,
  ) => {
    const result = (
      await api.post<OperationReceipt | CandidateDetail>(
        `${SCANNER_SUPPLY_CHAIN_PATH}/candidates/${encodeURIComponent(id)}/${action}`,
        payload,
        { headers: operationHeaders(version) },
      )
    ).data;
    return trackOperation(result, `Candidate ${action}`);
  },

  createCandidateException: async (
    id: string,
    input: CandidateExceptionInput,
  ) =>
    (
      await api.post<Approval>(
        `${SCANNER_SUPPLY_CHAIN_PATH}/candidates/${encodeURIComponent(id)}/exceptions`,
        input,
        { headers: operationHeaders() },
      )
    ).data,

  artifactDiff: async (
    owner: ArtifactDiffOwner,
    id: string,
    kind: ArtifactDiffKind,
  ) => {
    const collection = owner === "candidate" ? "candidates" : "releases";
    return (
      await api.get<ArtifactDiff>(
        `${SCANNER_SUPPLY_CHAIN_PATH}/${collection}/${encodeURIComponent(id)}/diffs/${kind}`,
      )
    ).data;
  },

  releases: async (
    filters: {
      state?: string;
      cursor?: string;
      limit?: number;
    } = {},
  ) => {
    return cursorPage<ReleaseSummary>(
      `${SCANNER_SUPPLY_CHAIN_PATH}/releases${queryString(filters)}`,
    );
  },

  legacyConfiguration: async (): Promise<LegacyConfigurationSnapshot> => {
    const [config, tools] = await Promise.all([
      api.get<LegacyScannerConfig>("/scanners/config"),
      api.get<LegacyScannerTool[]>("/scanners/tools"),
    ]);
    return {
      config: config.data,
      tools: tools.data ?? [],
    };
  },

  importLegacyConfiguration: async (
    payload: { reason: string; resolved_digests?: Record<string, string> },
    idempotencyKey: string,
  ) =>
    (
      await api.post<LegacyReleaseImportResult>(
        `${SCANNER_SUPPLY_CHAIN_PATH}/legacy-release-imports`,
        payload,
        { headers: { "Idempotency-Key": idempotencyKey } },
      )
    ).data,

  createReleaseRescan: async (
    sourceScanId: string,
    payload: { release_id: string; reason: string },
    idempotencyKey: string,
  ) =>
    (
      await api.post<Scan>(
        `/scans/${encodeURIComponent(sourceScanId)}/release-rescans`,
        payload,
        { headers: { "Idempotency-Key": idempotencyKey } },
      )
    ).data,

  signers: async (includeInactive = true): Promise<Page<SignerProfile>> => {
    const result = await api.get<SignerProfile[] | Page<SignerProfile>>(
      `${SCANNER_SUPPLY_CHAIN_PATH}/signers${queryString({
        include_inactive: includeInactive ? "true" : undefined,
      })}`,
    );
    return Array.isArray(result.data)
      ? { items: result.data }
      : normalizePage(result.data);
  },

  signer: async (id: string): Promise<SignerProfileDetail> => {
    const result = await api.getWithMetadata<SignerProfile>(
      `${SCANNER_SUPPLY_CHAIN_PATH}/signers/${encodeURIComponent(id)}`,
    );
    return {
      signer: result.response.data,
      etag: result.headers.get("ETag") ?? undefined,
    };
  },

  createSigner: async (input: SignerProfileInput, idempotencyKey: string) =>
    (
      await api.post<SignerProfile>(
        `${SCANNER_SUPPLY_CHAIN_PATH}/signers`,
        input,
        { headers: { "Idempotency-Key": idempotencyKey } },
      )
    ).data,

  rotateSigner: async (
    id: string,
    input: SignerProfileInput,
    etag: string,
    idempotencyKey: string,
  ) =>
    (
      await api.post<SignerProfile>(
        `${SCANNER_SUPPLY_CHAIN_PATH}/signers/${encodeURIComponent(id)}/rotate`,
        input,
        {
          headers: {
            "Idempotency-Key": idempotencyKey,
            "If-Match": etag,
          },
        },
      )
    ).data,

  revokeSigner: async (
    id: string,
    reason: string,
    etag: string,
    idempotencyKey: string,
  ) =>
    (
      await api.post<SignerProfile>(
        `${SCANNER_SUPPLY_CHAIN_PATH}/signers/${encodeURIComponent(id)}/revoke`,
        { reason },
        {
          headers: {
            "Idempotency-Key": idempotencyKey,
            "If-Match": etag,
          },
        },
      )
    ).data,

  release: async (id: string) => {
    const result = (
      await api.get<
        | ReleaseDetail
        | {
            release: ReleaseSummary;
            tools?: ReleaseTool[];
            images?: ReleaseImage[];
            artifacts?: ReleaseArtifact[];
          }
      >(`${SCANNER_SUPPLY_CHAIN_PATH}/releases/${encodeURIComponent(id)}`)
    ).data;
    return "release" in result
      ? {
          ...result.release,
          tools: result.tools,
          images: result.images,
          artifacts: result.artifacts,
        }
      : result;
  },

  compareReleases: async (from: string, to: string) =>
    (
      await api.get<ReleaseComparison>(
        `${SCANNER_SUPPLY_CHAIN_PATH}/releases/compare${queryString({ from, to })}`,
      )
    ).data,

  verifyRelease: async (id: string) => {
    const receipt = (
      await api.post<OperationReceipt>(
        `${SCANNER_SUPPLY_CHAIN_PATH}/releases/${encodeURIComponent(id)}/verify`,
        {},
        { headers: operationHeaders() },
      )
    ).data;
    return trackOperation(receipt, "Release verification");
  },

  exportRelease: async (id: string) =>
    api.download(
      `${SCANNER_SUPPLY_CHAIN_PATH}/releases/${encodeURIComponent(id)}/export`,
    ),

  releaseAction: async (
    id: string,
    action: "promote" | "deprecate" | "revoke",
    payload: Record<string, unknown>,
    version?: number,
  ) => {
    const receipt = (
      await api.post<OperationReceipt>(
        `${SCANNER_SUPPLY_CHAIN_PATH}/releases/${encodeURIComponent(id)}/${action}`,
        payload,
        { headers: operationHeaders(version) },
      )
    ).data;
    return trackOperation(receipt, `Release ${action}`);
  },

  rollouts: async (
    filters: {
      state?: string;
      target?: string;
      cursor?: string;
      limit?: number;
    } = {},
  ) => {
    return cursorPage<RolloutSummary>(
      `${SCANNER_SUPPLY_CHAIN_PATH}/rollouts${queryString(filters)}`,
    );
  },

  rollout: async (id: string) => {
    const result = (
      await api.get<
        | RolloutDetail
        | {
            rollout: RolloutSummary;
            cohorts?: CohortSummary[];
            events?: RolloutEvent[];
            health?: CanaryHealth;
            synthetic_health?: SyntheticRolloutHealth;
            real_scan_health?: RealScanRolloutHealth;
            affected_workers?: number;
            recommendation?: string;
            maintenance_window?: RolloutDetail["maintenance_window"];
          }
      >(`${SCANNER_SUPPLY_CHAIN_PATH}/rollouts/${encodeURIComponent(id)}`)
    ).data;
    return "rollout" in result
      ? {
          ...result.rollout,
          cohorts: result.cohorts ?? [],
          events: result.events,
          health: result.health,
          synthetic_health: result.synthetic_health,
          real_scan_health: result.real_scan_health,
          affected_workers: result.affected_workers,
          recommendation: result.recommendation,
          maintenance_window: result.maintenance_window,
        }
      : result;
  },

  rolloutAction: async (
    id: string,
    action: "pause" | "resume" | "rollback",
    reason: string,
    version?: number,
  ) => {
    const result = (
      await api.post<OperationReceipt | RolloutDetail>(
        `${SCANNER_SUPPLY_CHAIN_PATH}/rollouts/${encodeURIComponent(id)}/${action}`,
        { reason },
        { headers: operationHeaders(version) },
      )
    ).data;
    return trackOperation(result, `Rollout ${action}`);
  },

  notifications: async (filters: ScannerNotificationFilters = {}) =>
    cursorPage<ScannerNotification>(
      `${SCANNER_SUPPLY_CHAIN_PATH}/notifications${queryString({
        state: filters.state,
        destination_type: filters.destination_type,
        notification_type: filters.notification_type,
        cursor: filters.cursor,
        limit: filters.limit,
      })}`,
    ),

  notification: async (id: string): Promise<ScannerNotificationDetail> => {
    const result = await api.getWithMetadata<ScannerNotification>(
      `${SCANNER_SUPPLY_CHAIN_PATH}/notifications/${encodeURIComponent(id)}`,
    );
    return {
      notification: result.response.data,
      etag: result.headers.get("ETag") ?? undefined,
    };
  },

  retryNotification: async (id: string, reason: string, etag: string) =>
    (
      await api.post<ScannerNotification>(
        `${SCANNER_SUPPLY_CHAIN_PATH}/notifications/${encodeURIComponent(id)}/retry`,
        { reason },
        {
          headers: {
            "Idempotency-Key": createIdempotencyKey(),
            "If-Match": etag,
          },
        },
      )
    ).data,

  alerts: async (filters: ScannerAlertFilters = {}) =>
    cursorPage<ScannerAlert>(
      `${SCANNER_SUPPLY_CHAIN_PATH}/alerts${queryString({
        state: filters.state,
        kind: filters.kind,
        severity: filters.severity,
        cursor: filters.cursor,
        limit: filters.limit,
      })}`,
    ),

  alert: async (id: string) =>
    (
      await api.get<ScannerAlert>(
        `${SCANNER_SUPPLY_CHAIN_PATH}/alerts/${encodeURIComponent(id)}`,
      )
    ).data,

  policy: async () =>
    (await api.get<ScannerPolicy>(`${SCANNER_SUPPLY_CHAIN_PATH}/policy`)).data,

  updatePolicy: async (
    policy: { schedule: PolicySchedule; rules: PolicyRules },
    revision: number,
  ) =>
    (
      await api.put<ScannerPolicy>(
        `${SCANNER_SUPPLY_CHAIN_PATH}/policy`,
        policy,
        {
          headers: operationHeaders(revision),
        },
      )
    ).data,

  validatePolicy: async (policy: {
    schedule: PolicySchedule;
    rules: PolicyRules;
  }) => {
    try {
      return (
        await api.post<ScannerPolicy["validation"]>(
          `${SCANNER_SUPPLY_CHAIN_PATH}/policy/validate`,
          policy,
        )
      ).data;
    } catch (error) {
      if (!(error instanceof ApiError) || error.status !== 404) throw error;
      const errors: string[] = [];
      const warnings: string[] = [];
      if (!policy.schedule.timezone?.trim())
        errors.push("Timezone is required.");
      if (!policy.schedule.daily_discovery?.at)
        errors.push("Daily discovery time is required.");
      if (!policy.schedule.weekly_candidate?.at)
        errors.push("Weekly candidate time is required.");
      if (!policy.rules.required_gates?.length)
        errors.push("At least one release gate is required.");
      [
        "lock",
        "artifacts",
        "platforms",
        "parser",
        "signature",
        "provenance",
        "source",
        "secret_scan",
      ].forEach((gate) => {
        if (!policy.rules.required_gates?.includes(gate)) {
          errors.push(`Non-bypassable gate ${gate} is required.`);
        }
      });
      if ((policy.rules.required_approvals ?? 0) < 0)
        errors.push("Required approvals cannot be negative.");
      if (
        policy.rules.auto_promote_risks?.some(
          (risk) => risk === "high" || risk === "critical",
        )
      ) {
        errors.push("High and critical risk cannot be automatically promoted.");
      }
      if (!policy.rules.notifications?.destinations?.length) {
        warnings.push("No notification destinations are configured.");
      }
      return { valid: errors.length === 0, errors, warnings };
    }
  },

  policyHistory: async () => {
    try {
      return await cursorPage<PolicyRevision>(
        `${SCANNER_SUPPLY_CHAIN_PATH}/policy/revisions`,
      );
    } catch (error) {
      if (!(error instanceof ApiError) || error.status !== 404) throw error;
      const active = await scannerSupplyChainApi.policy();
      return { items: [active] };
    }
  },

  policyDryRun: async (
    candidateId: string,
    policy: { schedule: PolicySchedule; rules: PolicyRules },
  ) =>
    (
      await api.post<PolicyDryRun>(
        `${SCANNER_SUPPLY_CHAIN_PATH}/policy/dry-run`,
        { candidate_id: candidateId, ...policy },
      )
    ).data,

  restorePolicy: async (revision: number, reason: string) =>
    (
      await api.post<ScannerPolicy>(
        `${SCANNER_SUPPLY_CHAIN_PATH}/policy/revisions/${revision}/restore`,
        { reason },
        { headers: operationHeaders() },
      )
    ).data,

  registries: async () => {
    return cursorPage<RegistrySummary>(
      `${SCANNER_SUPPLY_CHAIN_PATH}/registries?include_disabled=true`,
    );
  },

  createRegistry: async (registry: RegistryInput) =>
    (
      await api.post<RegistrySummary>(
        `${SCANNER_SUPPLY_CHAIN_PATH}/registries`,
        registry,
        { headers: operationHeaders() },
      )
    ).data,

  updateRegistry: async (
    id: string,
    registry: RegistryInput,
    version?: number,
  ) =>
    (
      await api.patch<RegistrySummary>(
        `${SCANNER_SUPPLY_CHAIN_PATH}/registries/${encodeURIComponent(id)}`,
        registry,
        { headers: operationHeaders(version) },
      )
    ).data,

  registryAction: async (
    id: string,
    action: "check" | "reconcile",
    releaseId?: string,
  ) =>
    (
      await api.post<
        OperationReceipt & {
          registry_id?: string;
          reachable?: boolean;
          matched?: boolean;
          checked_at?: string;
          latency_ms?: number;
          error?: string;
        }
      >(
        `${SCANNER_SUPPLY_CHAIN_PATH}/registries/${encodeURIComponent(id)}/${action}`,
        action === "reconcile" ? { release_id: releaseId } : undefined,
      )
    ).data,

  registryJobs: async (filters: RegistryJobFilters = {}) =>
    cursorPage<RegistryJob>(
      `${SCANNER_SUPPLY_CHAIN_PATH}/registry-jobs${queryString({
        registry_target_id: filters.registry_target_id,
        release_id: filters.release_id,
        state: filters.state,
        kind: filters.kind,
        cursor: filters.cursor,
        limit: filters.limit ?? 100,
      })}`,
    ),

  registryJob: async (id: string): Promise<RegistryJobDetail> => {
    const result = await api.getWithMetadata<{
      job: RegistryJob;
      images: RegistryImageObservation[];
      events_url: string;
    }>(`${SCANNER_SUPPLY_CHAIN_PATH}/registry-jobs/${encodeURIComponent(id)}`);
    return {
      ...result.response.data,
      etag: result.headers.get("ETag") ?? undefined,
    };
  },

  createRegistryJob: async (registryId: string, input: RegistryJobInput) => {
    const receipt = (
      await api.post<OperationReceipt>(
        `${SCANNER_SUPPLY_CHAIN_PATH}/registries/${encodeURIComponent(registryId)}/jobs`,
        input,
        { headers: operationHeaders() },
      )
    ).data;
    return trackOperation(receipt, `Registry ${input.kind}`);
  },

  createRegistryCleanupJob: async (
    registryId: string,
    reason: string,
    maxAttempts = 5,
  ) => {
    const receipt = (
      await api.post<OperationReceipt>(
        `${SCANNER_SUPPLY_CHAIN_PATH}/registries/${encodeURIComponent(registryId)}/cleanup-jobs`,
        { reason, max_attempts: maxAttempts },
        { headers: operationHeaders() },
      )
    ).data;
    return trackOperation(receipt, "Registry quarantine cleanup");
  },

  retryRegistryJob: async (
    id: string,
    reason: string,
    etag: string | number,
  ) => {
    const receipt = (
      await api.post<OperationReceipt>(
        `${SCANNER_SUPPLY_CHAIN_PATH}/registry-jobs/${encodeURIComponent(id)}/retry`,
        { reason },
        {
          headers: {
            "Idempotency-Key": createIdempotencyKey(),
            "If-Match": String(etag),
          },
        },
      )
    ).data;
    return trackOperation(receipt, "Registry job retry");
  },

  registryQuarantine: async (
    filters: {
      registry_target_id?: string;
      state?: RegistryQuarantineState | "";
      limit?: number;
    } = {},
  ) =>
    cursorPage<RegistryQuarantineObject>(
      `${SCANNER_SUPPLY_CHAIN_PATH}/registry-quarantine${queryString({
        registry_target_id: filters.registry_target_id,
        state: filters.state,
        limit: filters.limit ?? 100,
      })}`,
    ),

  audit: async (filters: AuditFilters = {}) => {
    try {
      return await cursorPage<AuditEvent>(
        `${SCANNER_SUPPLY_CHAIN_PATH}/audit${queryString({
          ...filters,
          limit: 100,
        })}`,
      );
    } catch (error) {
      if (!(error instanceof ApiError) || error.status !== 404) throw error;
      type RequestAudit = {
        id: string;
        token_id?: string;
        user_id: string;
        action: string;
        method: string;
        path: string;
        resource_id?: string;
        status_code: number;
        event_type?: string;
        created_at: string;
      };
      const result = await api.get<RequestAudit[]>(
        `/audit-log${queryString({
          q: "scanner-supply-chain",
          per_page: 100,
        })}`,
      );
      const events = result.data
        .map<AuditEvent>((entry) => ({
          id: entry.id,
          aggregate_type:
            entry.event_type?.split(".")[1] ??
            entry.path.split("/scanner-supply-chain/")[1]?.split("/")[0] ??
            "scanner-supply-chain",
          aggregate_id: entry.resource_id ?? "",
          event_type: entry.event_type || entry.action,
          actor: entry.token_id
            ? `token:${entry.token_id}`
            : `user:${entry.user_id}`,
          reason: `${entry.method} ${entry.path} → HTTP ${entry.status_code}`,
          created_at: entry.created_at,
        }))
        .filter(
          (event) =>
            (!filters.aggregate_type ||
              event.aggregate_type === filters.aggregate_type) &&
            (!filters.event_type ||
              event.event_type.includes(filters.event_type)) &&
            (!filters.actor || event.actor?.includes(filters.actor)) &&
            !filters.trace_id &&
            !filters.operation_id,
        );
      return { items: events, total: result.meta?.total };
    }
  },
};
