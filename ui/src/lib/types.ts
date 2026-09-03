// Enums matching Go models

export type Severity = "critical" | "high" | "medium" | "low" | "info";

export type Category =
  | "sast"
  | "sca"
  | "secrets"
  | "container"
  | "quality"
  | "docs"
  | "license"
  | "sbom"
  | "infra"
  | "dast";

export type FindingStatus = "open" | "fixed" | "wont_fix" | "false_positive";

export type ScanStatus =
  | "pending"
  | "running"
  | "completed"
  | "failed"
  | "cancelled";

export type FixStatus =
  | "pending"
  | "running"
  | "completed"
  | "failed"
  | "cancelled";

export type FixItemStatus =
  | "pending"
  | "in_progress"
  | "fixed"
  | "failed"
  | "skipped";

export type AgentStatus =
  | "running"
  | "paused"
  | "completed"
  | "stopped"
  | "failed";

export type RescanStrategy = "full" | "targeted" | "smart";

export type SourceType = "local" | "github" | "gitlab" | "git" | "ssh";

export type SecretKeyType =
  | "github_token"
  | "gitlab_token"
  | "ssh_private_key"
  | "ssh_password"
  | "anthropic_key"
  | "openai_key"
  | "xai_key"
  | "custom";

export type ArtifactType = "sarif" | "json" | "markdown" | "log" | "coverage";

export type ValidationResult = "pass" | "fail";

// Core entities

export interface User {
  id: string;
  email: string;
  created_at: string;
  updated_at: string;
}

export interface Repo {
  id: string;
  user_id: string;
  name: string;
  source_type: SourceType;
  source_path: string;
  remote_node_id?: string;
  remote_path?: string;
  last_commit_sha?: string;
  last_dirty_state?: string;
  default_branch: string;
  detected_languages: string;
  detected_frameworks: string;
  detected_at: string | null;
  created_at: string;
  updated_at: string;
}

export interface RemoteNode {
  id: string;
  user_id: string;
  name: string;
  host: string;
  port: number;
  username: string;
  auth_type: "private_key" | "password";
  credential_secret_id?: string;
  known_hosts?: string;
  base_path?: string;
  enabled: boolean;
  last_check_status?: string;
  last_check_error?: string;
  last_checked_at?: string;
  created_at: string;
  updated_at: string;
}

export interface ScanConfig {
  disabled_tools?: string[];
  branch_overrides?: Record<string, string>; // repo_id → branch to scan
  ai_enabled?: boolean;
  ai_engine?: string;
  ai_model?: string;
  concurrency?: number;
}

export interface ToolInfo {
  name: string;
  description?: string;
  category: string;
  languages: string[];
  available: boolean;
  installable?: boolean;
  install_hint?: string;
  enabled: boolean;
  recommended: boolean;
}

export interface RepoDetection {
  repo_id: string;
  repo_name: string;
  languages: Record<string, number>;
  frameworks: string[];
  total_files: number;
  source_files: number;
  test_files: number;
  branches: string[];
  default_branch: string;
  current_branch: string;
  branch_error?: string;
}

export interface CollectionToolsResponse {
  tools: ToolInfo[];
  repo_summary: RepoDetection[];
}

export interface Collection {
  id: string;
  user_id: string;
  name: string;
  description: string;
  repos?: Repo[];
  repo_count?: number;
  latest_scan?: Scan;
  finding_counts?: FindingCounts;
  trend?: number[];
  scan_config?: ScanConfig | string;
  created_at: string;
  updated_at: string;
}

export interface FindingCounts {
  critical: number;
  high: number;
  medium: number;
  low: number;
  info: number;
  total: number;
}

export interface Secret {
  id: string;
  user_id: string;
  key_type: SecretKeyType;
  key_name: string;
  masked_value: string;
  created_at: string;
  updated_at: string;
}

export interface RepoMap {
  id: string;
  repo_id: string;
  branch: string;
  structural_data: Record<string, unknown>;
  semantic_data: Record<string, unknown>;
  file_hashes: Record<string, string>;
  created_at: string;
  updated_at: string;
}

export interface Scan {
  id: string;
  user_id: string;
  repo_id: string;
  collection_id?: string;
  loop_id?: string;
  iteration?: number;
  branch: string;
  source_type?: SourceType;
  remote_node_id?: string;
  source_path?: string;
  commit_sha?: string;
  dirty_state?: string;
  prepared_workspace?: string;
  scanner_release_id?: string;
  release_manifest_digest?: string;
  rescan_of_scan_id?: string;
  release_selection_reason?: string;
  origin_scan_id?: string;
  previous_scan_id?: string;
  remediation_id?: string;
  fix_job_id?: string;
  status: ScanStatus;
  failure_code?: string;
  failure_message?: string;
  attempt?: number;
  phase?: string;
  // The API stores these as JSON-encoded strings, NOT arrays. Use
  // parseToolList() to get a real string[] for length / iteration.
  tools_selected: string;
  tools_completed: string;
  tools_running: string;
  tools_failed: string;
  finding_count: number;
  coverage_summary?: Record<string, unknown>;
  ai_enabled?: boolean;
  ai_summary?: string;
  profile?: string;
  started_at?: string;
  completed_at?: string;
  created_at: string;
  updated_at: string;
  repo?: Repo;
}

export interface VulnerabilityEvidence {
  id: string;
  vulnerability_id: string;
  finding_id: string;
  tool_name: string;
  title: string;
  file_path: string;
  line_start: number;
  rule_id?: string;
  reason: string;
  created_at: string;
}

export interface Vulnerability {
  id: string;
  repo_id: string;
  scan_id: string;
  canonical_key: string;
  title: string;
  severity: Severity;
  category: Category;
  fine_category?: string;
  confidence?: string;
  baseline_state?: string;
  composite_score: number;
  evidence_count: number;
  finding_ids?: string[];
  corroborated_by?: string[];
  suppressed?: boolean;
  merge_reason?: string;
  evidence?: VulnerabilityEvidence[];
  created_at: string;
  updated_at: string;
}

export interface Finding {
  id: string;
  scan_id: string;
  repo_id: string;
  fingerprint: string;
  tool_name: string;
  category: Category;
  severity: Severity;
  tool_severity_score: number;
  location_weight: number;
  ai_context_score: number;
  composite_score: number;
  title: string;
  description: string;
  file_path: string;
  line_start: number;
  line_end: number;
  code_snippet: string;
  ai_fix_suggestion?: string;
  status: FindingStatus;
  cwe_id?: string;
  rule_id?: string;
  fine_category?: string;
  confidence?: string;
  corroborated_by?: string[];
  baseline_state?: string;
  fix_strategy_id?: string;
  fix_strategy?: { id: string; title: string; body: string };
  suppressed?: boolean;
  suppressed_reason?: string;
  introduced_in_scan_id?: string;
  sarif_data?: Record<string, unknown>;
  module_name?: string;
  function_name?: string;
  symbol_kind?: string;
  file_purpose?: string;
  dependents_json?: string;
  created_at: string;
  updated_at: string;
  repo?: Repo;
  scan?: Scan;
}

export interface Fix {
  id: string;
  user_id: string;
  scan_id: string;
  loop_id?: string;
  status: FixStatus;
  severity_filter: Severity[];
  branch_name: string;
  worktree_path: string;
  findings_attempted: number;
  findings_fixed: number;
  findings_failed: number;
  pr_urls: string[];
  items?: FixItem[];
  started_at?: string;
  completed_at?: string;
  created_at: string;
  updated_at: string;
  scan?: Scan;
}

export interface FixItem {
  id: string;
  fix_id: string;
  finding_id: string;
  status: FixItemStatus;
  files_changed: string[];
  diff: string;
  validation_result?: ValidationResult;
  validation_output?: string;
  error_message?: string;
  finding?: Finding;
  created_at: string;
  updated_at: string;
}

export interface Agent {
  id: string;
  user_id: string;
  repo_id: string;
  collection_id?: string;
  status: AgentStatus;
  max_iterations: number;
  current_iteration: number;
  severity_filter: Severity[];
  rescan_strategy: RescanStrategy;
  total_findings_initial: number;
  total_findings_fixed: number;
  total_findings_new: number;
  total_findings_remaining: number;
  guardrail_warnings: string[];
  iterations?: AgentRound[];
  started_at?: string;
  completed_at?: string;
  created_at: string;
  updated_at: string;
  repo?: Repo;
}

export interface AgentRound {
  iteration: number;
  scan_id: string;
  fix_id?: string;
  findings_before: number;
  findings_after: number;
  findings_fixed: number;
  findings_new: number;
}

export interface ScanArtifact {
  id: string;
  scan_id: string;
  artifact_type: ArtifactType;
  file_path: string;
  file_size: number;
  created_at: string;
  updated_at: string;
}

// Trend types (from GET /api/findings/trends)

export interface TrendSeverityCounts {
  critical: number;
  high: number;
  medium: number;
  low: number;
  info: number;
  total: number;
}

export interface TrendEntry {
  date: string;
  counts: TrendSeverityCounts;
}

// API response types

export interface ApiResponse<T> {
  data: T;
  meta?: ApiMeta;
  error?: ApiError;
}

export interface ApiMeta {
  next_cursor?: string;
  page?: number;
  per_page?: number;
  total?: number;
  total_pages?: number;
  // Count of records hidden by server-side suppression rules. Present on
  // endpoints that apply suppression (e.g. /scans/{id}/findings).
  suppressed?: number;
}

export interface ApiError {
  code: string;
  message: string;
  details?: Record<string, unknown>;
}

export interface LoginRequest {
  email: string;
  password: string;
}

export interface RegisterRequest {
  email: string;
  password: string;
}

// An API token (CLI / CI / agent credential). The plaintext `token` is only
// present on the create response — never on list.
export interface ApiToken {
  id: string;
  name: string;
  token_prefix: string;
  scopes: string[];
  created_at: string;
  last_used_at?: string;
  expires_at?: string;
  revoked_at?: string;
  user_id?: string; // present in the admin oversight view
}

// A secret in the admin oversight view: masked value + owner's user_id.
export interface AdminSecret {
  id: string;
  user_id: string;
  key_type: string;
  key_name: string;
  value: string; // masked
  created_at: string;
}

export interface ApiTokenCreated extends ApiToken {
  token: string; // shown exactly once
}

export interface AuthResponse {
  user?: User;
  access_token?: string;
  refresh_token?: string;
  // Present instead of a session when the account has TOTP enabled: the caller
  // must complete POST /auth/mfa/login with mfa_token + a code.
  mfa_required?: boolean;
  mfa_token?: string;
  // Present on a normal login when the org mandates MFA but the user hasn't
  // enrolled yet — the session is confined to the Security tab until they do.
  enrollment_required?: boolean;
}

// Coverage types

export interface LanguageCoverage {
  language: string;
  source_files: number;
  test_files: number;
  files_with_tests: number;
  coverage_percent: number;
  uncovered_files?: string[];
}

export interface CoverageReport {
  total_source_files: number;
  files_with_tests: number;
  files_without_tests: number;
  test_files: number;
  coverage_percent: number;
  uncovered_files: string[];
  by_language: Record<string, LanguageCoverage>;
}

// Scan comparison types

export interface ComparisonSummary {
  scan1_total: number;
  scan2_total: number;
  new_count: number;
  fixed_count: number;
  unchanged_count: number;
  changed_count: number;
  delta_percent: number;
}

export interface ChangedFinding {
  before: Finding;
  after: Finding;
}

export interface ComparisonResult {
  scan1: Scan;
  scan2: Scan;
  new_findings: Finding[];
  fixed_findings: Finding[];
  unchanged_count: number;
  changed_findings: ChangedFinding[];
  summary: ComparisonSummary;
}

// AI log entry (from GET /api/scans/{id}/ai-logs)

export interface AILog {
  id: string;
  scan_id: string;
  provider: string;
  model: string;
  phase: string;
  tool_name: string;
  prompt: string;
  response: string;
  error: string;
  prompt_tokens: number;
  response_tokens: number;
  duration_ms: number;
  cost_usd: number;
  created_at: string;
}

// Tool summary (from GET /api/scans/{id}/tool-summaries)
export interface ToolSummary {
  id: string;
  scan_id: string;
  tool_name: string;
  summary_text: string;
  finding_count: number;
  severity_counts: string; // JSON string of Record<string, number>
  critical_issues: string; // JSON string
  created_at: string;
}

// Scan recommendation (from GET /api/scans/{id}/recommendations)
export interface ScanRecommendation {
  id: string;
  scan_id: string;
  priority: number;
  category: string;
  title: string;
  description: string;
  affected_tools: string; // JSON string of string[]
  effort_estimate: string;
  created_at: string;
}

// Scan tool status (from GET /api/scans/{id}/tools)

export interface ScanToolStatus {
  name: string;
  status: string;
  finding_count: number;
  has_output: boolean;
}

// SSE event types

export interface SSEEvent {
  type: string;
  data: Record<string, unknown>;
}

export interface ScanProgressEvent {
  type: "scan_progress";
  scan_id: string;
  tool_name: string;
  status: "running" | "completed" | "failed";
  finding_count: number;
  total_findings?: number;
  elapsed_ms: number;
  progress_pct: number;
}

export interface ToolLogEvent {
  type: "tool_output";
  scan_id: string;
  tool_name: string;
  line: string;
}

export interface FixProgressEvent {
  type: "fix_progress";
  fix_id: string;
  finding_id: string;
  status: FixItemStatus;
  current_index: number;
  total: number;
}

// AI Prompt Template
export interface AIPromptTemplate {
  id: string;
  scope: "global" | "collection";
  scope_id: string;
  prompt_type: "tool_assess" | "executive_summary";
  section: "system_context" | "scoring_criteria" | "output_instructions";
  content: string;
  updated_at: string;
}

// AI Provider
export interface AIProvider {
  name: string;
  available: boolean;
  models: string[];
}

// Collection Metrics (from GET /api/collections/{id}/metrics)

export interface CollectionMetrics {
  snapshot: CollectionSnapshot;
  trends: TrendEntry[];
  resolution_rate: ResolutionRate;
  branches: string[];
}

export interface CollectionSnapshot {
  total_findings: number;
  by_severity: Record<Severity, number>;
  by_status: Record<string, number>;
  repos_scanned: number;
  branches_scanned: number;
  latest_scans: LatestScanSummary[];
}

export interface LatestScanSummary {
  repo_id: string;
  repo_name: string;
  branch: string;
  scan_id: string;
  finding_count: number;
  completed_at: string;
  by_severity: Record<Severity, number>;
}

export interface ResolutionRate {
  total_unique_fingerprints: number;
  resolved: number;
  open: number;
  triaged: number;
  suppressed: number;
  rate: number;
}

// Settings map
export type AppSettings = Record<string, string>;

export interface ScanSchedule {
  id: string;
  repo_id?: string;
  collection_id?: string;
  interval_minutes: number;
  branch?: string;
  profile?: string;
  quiet_start?: string;
  quiet_end?: string;
  enabled: boolean;
  last_run_at?: string;
  last_sha?: string;
}

export interface SetupStatus {
  repo_count: number;
  collection_count: number;
  has_completed_scan: boolean;
  overall_ok?: boolean;
}

export interface InboxNotification {
  type: string;
  title: string;
  href?: string;
  at?: string;
}

export interface DiskUsage {
  artifacts_bytes: number;
  workspaces_bytes: number;
  db_bytes: number;
}

// parseToolList unwraps the API's JSON-encoded tool list strings into a
// real string[]. Returns [] for empty / null / "null" / malformed inputs.
// Use everywhere the UI reads scan.tools_* to avoid the "503 tools" bug
// where .length on the raw string returns the character count.
export function parseToolList(s: string | null | undefined): string[] {
  if (!s || s === "null") return [];
  try {
    const parsed = JSON.parse(s);
    return Array.isArray(parsed) ? parsed.map(String) : [];
  } catch {
    return [];
  }
}
