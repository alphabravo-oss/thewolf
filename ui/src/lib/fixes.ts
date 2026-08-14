// Typed client + hooks for the autonomous fix engine (v1: dry-run, per-finding,
// verified, branch-only). Everything here is dark unless the autofix_enabled
// setting is on — useAutofixEnabled() reads it from GET /settings and the UI
// surface hides/disables itself when off.
//
// Endpoints (all gated server-side; the execute path returns 403
// autofix_disabled when the flag is off):
//   - POST   /fixes              enqueue a fix job
//   - GET    /fixes              list jobs (optional ?repo_id=)
//   - GET    /fixes/{id}         job + per-finding attempts (the audit trail)
//   - GET    /fixes/{id}/diff    the proposed unified diff (text/plain)
//   - GET    /fixes/{id}/stream  SSE log relay (consumed via useSSE)
//   - DELETE /fixes/{id}         cancel a queued/running job
//   - GET    /repos/{id}/fixable writability verdict {writable, reason}
import { useQuery } from "@tanstack/react-query";
import { api } from "./api";

const BASE_URL = import.meta.env.VITE_API_URL ?? "/api/v1";

// ---------------------------------------------------------------------------
// Types — mirror internal/models/fix_job.go (FixJob / FixAttempt).
// ---------------------------------------------------------------------------

export type FixJobStatus =
  | "queued"
  | "claimed"
  | "running"
  | "awaiting_review"
  | "awaiting_push"
  | "push_failed"
  | "succeeded"
  | "failed"
  | "cancelled"
  | "superseded";

export type FixAttemptOutcome = "kept" | "rolled_back" | "unfixable" | "muted";

export interface FixJob {
  id: string;
  user_id: string;
  type: string;
  repo_id: string;
  scan_id?: string;
  remediation_id?: string;
  finding_ids: string[];
  target_branch: string;
  engine: string;
  mode: string;
  severity_floor: string;
  max_attempts: number;
  max_loops: number;
  current_loop: number;
  planned_runs?: number;
  run_index?: number;
  human_in_the_loop: boolean;
  model?: string;
  effort?: string;
  variant?: string;
  pushed: boolean;
  push_sha?: string;
  pause_reason?: string;
  resume_action?: string;
  status: FixJobStatus;
  claimed_by?: string;
  result_branch?: string;
  diff_artifact_id?: string;
  summary: string;
  error?: string;
  claimed_at?: string;
  started_at?: string;
  finished_at?: string;
  heartbeat_at?: string;
  created_at: string;
  updated_at: string;
  queued_behind?: QueuedBehind;
}

export interface QueuedBehind {
  id: string;
  kind: "job" | "console";
  repo_id?: string;
  started_at?: string;
}

export interface FixAttempt {
  id: string;
  job_id: string;
  finding_id: string;
  attempt_no: number;
  engine_used: string;
  model?: string;
  built: boolean;
  finding_cleared: boolean;
  new_findings: number;
  outcome: FixAttemptOutcome;
  files_changed: string;
  diff_excerpt?: string;
  duration_ms: number;
  cost_usd: number;
  input_tokens: number;
  output_tokens: number;
  created_at: string;
  tool_name?: string;
  title?: string;
  file_path?: string;
  line_start?: number;
  severity?: string;
  rule_id?: string;
}

export interface ScannerToolStat {
  tool: string;
  total: number;
  kept: number;
  open: number;
  unfixable: number;
  muted: number;
  rolled: number;
  after?: number;
}

export interface FixCommit {
  sha: string;
  subject: string;
  when?: string;
}

// GET /fixes/{id} returns the job spread with its attempts attached.
export type FixJobDetail = FixJob & {
  attempts: FixAttempt[];
  tool_stats?: ScannerToolStat[];
};

// GET /repos/{id}/fixable — the writability preflight verdict.
export interface RepoFixable {
  writable: boolean;
  can_push: boolean;
  reason: string;
}

// Body of POST /fixes. v1 mode defaults to dry_run server-side.
export interface CreateFixRequest {
  repo_id: string;
  scan_id?: string;
  finding_ids?: string[];
  target_branch?: string;
  engine?: string;
  mode?: string;
  severity_floor?: string;
  max_attempts?: number;
  max_loops?: number;
  human_in_the_loop?: boolean;
  model?: string;
  effort?: string;
  variant?: string;
  planned_runs?: number;
}

// ---------------------------------------------------------------------------
// Settings — the master flag. The execute path, the worker, and this UI
// surface are all dark until autofix_enabled is true.
// ---------------------------------------------------------------------------

interface SettingRow {
  key: string;
  value: string;
}

// useAutofixEnabled reads autofix_enabled from GET /settings (array of
// {key,value} or a map). Defaults to false on error/absent. Components gate
// their fix surface on `enabled`; `isLoading` lets them avoid a flash.
export function useAutofixEnabled() {
  const q = useQuery({
    queryKey: ["settings"],
    queryFn: async () => {
      const r = await api.get<SettingRow[] | Record<string, string>>("/settings");
      const out: Record<string, string> = {};
      if (Array.isArray(r.data)) {
        for (const row of r.data) out[row.key] = row.value;
      } else if (r.data && typeof r.data === "object") {
        for (const [k, v] of Object.entries(r.data)) out[k] = String(v);
      }
      return out;
    },
  });
  return { enabled: q.data?.autofix_enabled === "true", isLoading: q.isLoading };
}

// ---------------------------------------------------------------------------
// Fixable preflight — used to show a fixable indicator on a repo and disable
// the "Fix this finding" action (with the reason) when wolf can't write here.
// ---------------------------------------------------------------------------

export function useRepoFixable(repoId: string | undefined, enabled = true) {
  return useQuery({
    queryKey: ["repo-fixable", repoId],
    enabled: !!repoId && enabled,
    queryFn: async () => {
      const r = await api.get<RepoFixable>(`/repos/${repoId}/fixable`);
      return r.data;
    },
  });
}

// ---------------------------------------------------------------------------
// Job list + detail.
// ---------------------------------------------------------------------------

export function useFixJobs(enabled = true) {
  return useQuery({
    queryKey: ["fix-jobs"],
    enabled,
    refetchInterval: 10_000,
    queryFn: async () => {
      const r = await api.get<FixJob[]>("/fixes?limit=200");
      return r.data ?? [];
    },
  });
}

export function useFixJob(id: string, enabled = true) {
  return useQuery({
    queryKey: ["fix-job", id],
    enabled,
    refetchInterval: (q) => {
      const status = (q.state.data as FixJobDetail | undefined)?.status;
      // Stop polling once the job reaches a terminal state.
      return status === "succeeded" ||
        status === "failed" ||
        status === "cancelled" ||
        status === "awaiting_review" ||
        status === "awaiting_push" ||
        status === "push_failed"
        ? false
        : 4_000;
    },
    queryFn: async () => {
      const r = await api.get<FixJobDetail>(`/fixes/${id}`);
      return r.data;
    },
  });
}

// enqueueFix POSTs a new fix job. Throws ApiError on the 403 autofix_disabled
// gate (flag off) so the caller can surface it.
export async function enqueueFix(body: CreateFixRequest): Promise<FixJob> {
  const r = await api.post<FixJob>("/fixes", body);
  return r.data as FixJob;
}

export async function cancelFix(id: string): Promise<void> {
  await api.delete(`/fixes/${id}`);
}

export async function resumeFix(
  id: string,
  action: "continue" | "push",
): Promise<FixJob> {
  const r = await api.post<FixJob>(`/fixes/${id}/resume`, { action });
  return r.data as FixJob;
}

export async function acceptRemediation(id: string): Promise<void> {
  await api.post(`/remediations/${id}/accept`, {});
}

export interface ScanLineage {
  origin: { id: string; finding_count?: number; branch?: string };
  children: {
    id: string;
    finding_count?: number;
    branch?: string;
    commit_sha?: string;
    origin_scan_id?: string;
    previous_scan_id?: string;
    prepared_workspace?: string;
    status?: string;
  }[];
  remediation?: {
    id: string;
    state: string;
    branch: string;
    workspace_path?: string;
    published_sha?: string;
  } | null;
  agents: {
    id: string;
    status: string;
    current_loop?: number;
    planned_runs?: number;
    run_index?: number;
    result_branch?: string;
    pushed?: boolean;
  }[];
  runs?: LineageRun[];
}

export interface LineageRun {
  job_id: string;
  status: string;
  run_index: number;
  planned_runs: number;
  created_at?: string;
  finished_at?: string;
  input_scan_id?: string;
  input_findings: number;
  child_scan_id?: string;
  child_status?: string;
  output_findings?: number | null;
  delta?: number | null;
  kept: number;
  muted: number;
  unfixable: number;
  remaining: number;
  pushed?: boolean;
  push_sha?: string;
  result_branch?: string;
  pause_reason?: string;
  error?: string;
}

export function useScanLineage(scanId: string | undefined) {
  return useQuery({
    queryKey: ["scan-lineage", scanId],
    enabled: !!scanId,
    refetchInterval: (q) => {
      const runs = q.state.data?.runs ?? [];
      const live = runs.some(
        (r) =>
          r.status === "queued" ||
          r.status === "claimed" ||
          r.status === "running" ||
          (r.child_status &&
            r.child_status !== "completed" &&
            r.child_status !== "failed"),
      );
      return live ? 4_000 : 10_000;
    },
    queryFn: async () => {
      const r = await api.get<ScanLineage>(`/scans/${scanId}/lineage`);
      return r.data;
    },
  });
}

export interface FixEngineStatus {
  name: string;
  command?: string;
  available: boolean;
  installed?: boolean;
  installable?: boolean;
  auth: string;
  detail?: string;
  account?: string;
  login?: string[];
  session_paths?: string[];
  persisted?: boolean;
  usage?: string;
  models?: FixCatalogModel[];
}

export interface FixCatalogModel {
  id: string;
  label: string;
  context_k?: number;
  plan?: string;
  default?: boolean;
  speed?: string;
  provider?: string;
  efforts?: FixCatalogEffort[];
}

export interface FixCatalogEffort {
  id: string;
  label: string;
  hint?: string;
}

export interface FixerPromptTemplate {
  key: string;
  value: string;
  default: string;
}

export interface FixCatalogEngine {
  name: string;
  command?: string;
  label: string;
  auth: string;
  login?: string[];
  session_paths?: string[];
  models: FixCatalogModel[];
  efforts?: FixCatalogEffort[];
}

export function useFixEngines(enabled = true) {
  return useQuery({
    queryKey: ["fix-engines"],
    enabled,
    refetchInterval: 15_000,
    queryFn: async () => {
      const r = await api.get<{
        worker: FixEngineStatus[];
        catalog: FixCatalogEngine[];
        defaults: Record<string, string>;
        api_keys: { anthropic: boolean; openai: boolean; xai?: boolean };
        oauth_hint: string;
        home?: string;
        console_shell?: boolean;
        prompts?: {
          initial: FixerPromptTemplate;
          followup: FixerPromptTemplate;
          placeholder: string;
        };
      }>("/fixes/engines");
      return r.data;
    },
  });
}

// fetchFixDiff reads the proposed unified diff (text/plain). Returns "" when
// the worker hasn't assembled one yet (the endpoint 404s) so the viewer can
// show a "no diff yet" state without throwing.
export async function fetchFixDiff(id: string, files?: string[]): Promise<string> {
  const qs = new URLSearchParams();
  for (const f of files ?? []) {
    if (f) qs.append("file", f);
  }
  const suffix = qs.size ? `?${qs.toString()}` : "";
  const res = await fetch(`${BASE_URL}/fixes/${id}/diff${suffix}`, {
    credentials: "include",
    headers: { Accept: "text/plain" },
  });
  if (res.status === 404) return "";
  if (!res.ok) throw new Error(`failed to load diff (${res.status})`);
  return res.text();
}

export async function fetchFixCommits(id: string): Promise<FixCommit[]> {
  const r = await api.get<FixCommit[]>(`/fixes/${id}/commits`);
  return r.data ?? [];
}

// The shape of the SSE frames StreamFix relays. Every frame is an unnamed
// `data:` event carrying a `type` discriminator (fix_log | fix_status |
// fix_completed) — consumed via the shared useSSE hook.
export interface FixStreamEvent {
  type: "fix_log" | "fix_status" | "fix_completed";
  line?: string;
  fix_id?: string;
  status?: FixJobStatus;
  result_branch?: string;
  diff_artifact_id?: string;
}

// A finished job is read-only; helper for gating cancel + poll behavior.
export function isFixTerminal(status: FixJobStatus | undefined): boolean {
  return (
    status === "succeeded" ||
    status === "failed" ||
    status === "cancelled" ||
    status === "superseded"
  );
}

export function isFixPaused(status: FixJobStatus | undefined): boolean {
  return (
    status === "awaiting_review" ||
    status === "awaiting_push" ||
    status === "push_failed"
  );
}

function githubRepo(sourcePath: string | undefined): { owner: string; repo: string } | null {
  if (!sourcePath) return null;
  const fromHost = sourcePath.match(/github\.com[/:]([^/]+)\/([^/.]+)/i);
  if (fromHost) return { owner: fromHost[1], repo: fromHost[2] };
  const slim = sourcePath.match(/^([^/\s]+)\/([^/\s]+)$/);
  if (slim) return { owner: slim[1], repo: slim[2] };
  return null;
}

export function githubBranchUrl(
  sourcePath: string | undefined,
  branch: string | undefined,
): string | null {
  const repo = githubRepo(sourcePath);
  if (!repo || !branch) return null;
  return `https://github.com/${repo.owner}/${repo.repo}/tree/${encodeURIComponent(branch)}`;
}

export function githubCommitUrl(
  sourcePath: string | undefined,
  sha: string | undefined,
): string | null {
  const repo = githubRepo(sourcePath);
  if (!repo || !sha) return null;
  return `https://github.com/${repo.owner}/${repo.repo}/commit/${sha}`;
}

export function pushFailureHint(error: string | undefined): string {
  const raw = (error || "").trim();
  if (!raw) return "";
  if (/workflow.*scope/i.test(raw) || /without `workflow` scope/i.test(raw)) {
    return "GitHub rejected the push: this branch updates a GitHub Actions workflow, and the stored token needs the workflow scope. Add that scope on the PAT, save it in Wolf, then retry the push.";
  }
  const rejected = raw.match(/remote rejected[^\n]*/i);
  if (rejected) return `GitHub rejected the push: ${rejected[0]}`;
  if (/git push/i.test(raw)) return raw.replace(/^[\s\S]*git push[^:]*:\s*/i, "").trim() || raw;
  return raw;
}

export function useRepoName(repoId: string | undefined) {
  return useQuery({
    queryKey: ["repo", repoId],
    enabled: !!repoId,
    staleTime: 60_000,
    queryFn: async () => {
      const r = await api.get<{ name?: string; source_path?: string }>(
        `/repos/${repoId}`,
      );
      return r.data;
    },
  });
}

export function useScanSummary(scanId: string | undefined) {
  return useQuery({
    queryKey: ["scan-summary", scanId],
    enabled: !!scanId,
    staleTime: 60_000,
    queryFn: async () => {
      const r = await api.get<{
        id: string;
        branch?: string;
        finding_count?: number;
        commit_sha?: string;
        created_at?: string;
        source_path?: string;
        repo?: { name?: string };
      }>(`/scans/${scanId}`);
      return r.data;
    },
  });
}

export function decisionInfo(
  outcome: FixAttemptOutcome,
  excerpt: string,
): { label: string; hint: string; tone: "ok" | "warn" | "mute" } {
  const ex = excerpt.trim();
  if (ex.startsWith("MUTE:") || outcome === "muted") {
    return {
      label: "muted",
      hint: "Accepted as scanner noise. Wolf wrote an in-repo ignore and a durable suppression so the next scan stays clean.",
      tone: "mute",
    };
  }
  if (ex.startsWith("SKIP:")) {
    return {
      label: "skipped",
      hint: "The agent (or Wolf) judged this not worth a production change.",
      tone: "mute",
    };
  }
  if (ex.includes("no decisions") || ex.includes("no file changes")) {
    return {
      label: "no work",
      hint: "The engine did not edit this finding. Nothing was committed or undone.",
      tone: "warn",
    };
  }
  if (outcome === "kept") {
    return {
      label: "fixed",
      hint: "A change landed on the fix branch and the verify gate kept it.",
      tone: "ok",
    };
  }
  if (outcome === "rolled_back") {
    return {
      label: "rolled back",
      hint: "The agent edited files, then verify failed (build/rescan), so Wolf restored those files.",
      tone: "warn",
    };
  }
  return {
    label: "skipped",
    hint: "Marked unfixable after skip or a failed engine chain.",
    tone: "mute",
  };
}

export function useFindingsMap(ids: string[]) {
  const unique = [...new Set(ids.filter(Boolean))];
  return useQuery({
    queryKey: ["findings-map", unique.slice().sort().join(",")],
    enabled: unique.length > 0,
    staleTime: 60_000,
    queryFn: async () => {
      const out: Record<
        string,
        { id: string; title: string; file_path: string; line_start: number; severity: string; tool_name: string }
      > = {};
      const chunk = 12;
      for (let i = 0; i < unique.length; i += chunk) {
        await Promise.all(
          unique.slice(i, i + chunk).map(async (id) => {
            try {
              const r = await api.get<{
                id: string;
                title: string;
                file_path: string;
                line_start: number;
                severity: string;
                tool_name: string;
              }>(`/findings/${id}`);
              if (r.data) out[id] = r.data;
            } catch {
              /* missing finding is not fatal */
            }
          }),
        );
      }
      return out;
    },
  });
}

export function findingLabel(f: {
  title?: string;
  file_path?: string;
  line_start?: number;
  severity?: string;
  tool_name?: string;
  id?: string;
}): string {
  const file =
    f.file_path && f.line_start
      ? `${f.file_path}:${f.line_start}`
      : f.file_path || "";
  const title = f.title || f.id || "finding";
  const bits = [f.severity && `[${f.severity}]`, f.tool_name, file, title && `— ${title}`]
    .filter(Boolean)
    .join(" ");
  return bits || title;
}

export type FixerConsoleStatus =
  | "queued"
  | "claimed"
  | "running"
  | "exited"
  | "cancelled";

export interface FixerConsole {
  id: string;
  user_id: string;
  kind: "login" | "shell" | "install";
  engine: string;
  status: FixerConsoleStatus;
  claimed_by?: string;
  last_url?: string;
  error?: string;
  created_at: string;
  updated_at: string;
}

export interface FixerConsoleStreamEvent {
  type: "console_data" | "console_log" | "console_status" | "console_completed";
  data?: string;
  line?: string;
  id?: string;
  status?: FixerConsoleStatus;
  kind?: string;
  engine?: string;
  last_url?: string;
  error?: string;
}

export async function startFixerConsole(body: {
  kind: "login" | "shell" | "install";
  engine?: string;
}): Promise<FixerConsole> {
  const r = await api.post<FixerConsole>("/fixes/consoles", body);
  return r.data as FixerConsole;
}

export async function getFixerConsole(id: string): Promise<FixerConsole> {
  const r = await api.get<FixerConsole>(`/fixes/consoles/${id}`);
  return r.data as FixerConsole;
}

export async function sendFixerConsoleInput(id: string, data: string): Promise<void> {
  await api.post(`/fixes/consoles/${id}/input`, { data });
}

export async function cancelFixerConsole(id: string): Promise<void> {
  await api.delete(`/fixes/consoles/${id}`);
}

export function isFixerConsoleActive(status: FixerConsoleStatus | undefined): boolean {
  return status === "queued" || status === "claimed" || status === "running";
}
