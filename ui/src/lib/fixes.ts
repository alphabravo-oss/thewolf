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

const BASE_URL = import.meta.env.VITE_API_URL ?? "/api";

// ---------------------------------------------------------------------------
// Types — mirror internal/models/fix_job.go (FixJob / FixAttempt).
// ---------------------------------------------------------------------------

export type FixJobStatus =
  | "queued"
  | "claimed"
  | "running"
  | "succeeded"
  | "failed"
  | "cancelled";

export type FixAttemptOutcome = "kept" | "rolled_back" | "unfixable";

export interface FixJob {
  id: string;
  user_id: string;
  type: string;
  repo_id: string;
  scan_id?: string;
  finding_ids: string[];
  target_branch: string;
  engine: string;
  mode: string;
  severity_floor: string;
  max_attempts: number;
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
  created_at: string;
}

// GET /fixes/{id} returns the job spread with its attempts attached.
export type FixJobDetail = FixJob & { attempts: FixAttempt[] };

// GET /repos/{id}/fixable — the writability preflight verdict.
export interface RepoFixable {
  writable: boolean;
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
      return status === "succeeded" || status === "failed" || status === "cancelled"
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

// fetchFixDiff reads the proposed unified diff (text/plain). Returns "" when
// the worker hasn't assembled one yet (the endpoint 404s) so the viewer can
// show a "no diff yet" state without throwing.
export async function fetchFixDiff(id: string): Promise<string> {
  const res = await fetch(`${BASE_URL}/fixes/${id}/diff`, {
    credentials: "include",
    headers: { Accept: "text/plain" },
  });
  if (res.status === 404) return "";
  if (!res.ok) throw new Error(`failed to load diff (${res.status})`);
  return res.text();
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
  return status === "succeeded" || status === "failed" || status === "cancelled";
}
