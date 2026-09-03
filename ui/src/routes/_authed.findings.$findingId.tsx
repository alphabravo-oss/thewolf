// Finding detail. Surface tool name, severity, location, code snippet,
// AI fix suggestion, status controls (open / wont_fix / false_positive).
import { createFileRoute, Link } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowLeftIcon } from "lucide-react";
import { toast } from "sonner";
import { api } from "@/lib/api";
import type { Finding, FindingStatus } from "@/lib/types";
import { CardSkeleton } from "@/components/skeleton";
import { SeverityBadge } from "@/components/severity-badge";
import { FixFindingButton } from "@/components/fixes/fix-finding-button";

export const Route = createFileRoute("/_authed/findings/$findingId")({
  component: FindingDetailPage,
});

const STATUSES: { value: FindingStatus; label: string }[] = [
  { value: "open", label: "Open" },
  { value: "wont_fix", label: "Won't fix" },
  { value: "false_positive", label: "False positive" },
];

function FindingDetailPage() {
  const { findingId } = Route.useParams();
  const qc = useQueryClient();
  const q = useQuery({
    queryKey: ["finding", findingId],
    queryFn: async () => {
      const r = await api.get<Finding>(`/findings/${findingId}`);
      return r.data;
    },
  });

  const setStatus = useMutation({
    mutationFn: (status: FindingStatus) =>
      api.put(`/findings/${findingId}/status`, { status }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["finding", findingId] });
      qc.invalidateQueries({ queryKey: ["findings"] });
      toast.success("Status updated");
    },
    onError: (e) =>
      toast.error(e instanceof Error ? e.message : "Update failed"),
  });

  const suppress = useMutation({
    mutationFn: async (finding: Finding) => {
      const preview = await api.post<{ count?: number }>(
        "/suppressions/preview",
        { finding_id: finding.id, reason: "user" },
      );
      const count = preview.data?.count ?? 0;
      if (
        !window.confirm(
          `Suppress this finding? This matches ${count} finding${count === 1 ? "" : "s"}.`,
        )
      ) {
        return false;
      }
      await api.post("/suppressions", {
        finding_id: finding.id,
        repo_id: finding.repo_id,
        reason: "user",
      });
      return true;
    },
    onSuccess: (ok) => {
      if (!ok) return;
      qc.invalidateQueries({ queryKey: ["finding", findingId] });
      qc.invalidateQueries({ queryKey: ["findings"] });
      toast.success("Finding suppressed");
    },
    onError: (e) =>
      toast.error(e instanceof Error ? e.message : "Suppress failed"),
  });

  if (q.isLoading || !q.data) {
    return (
      <div className="page stack page--mid">
        <CardSkeleton />
        <CardSkeleton />
      </div>
    );
  }
  const f = q.data;

  return (
    <div className="page stack page--mid">
      <div className="flex items-start gap-3">
        <Link
          to="/findings"
          className="size-9 grid place-items-center rounded-md hover:bg-muted/50"
          aria-label="Back"
        >
          <ArrowLeftIcon className="size-4" />
        </Link>
        <div className="flex-1 min-w-0">
          <div className="flex items-center gap-2 flex-wrap mb-1">
            <SeverityBadge severity={f.severity} />
            <span className="text-2xs uppercase tracking-wide text-muted-foreground">
              {f.category}
            </span>
            {f.fine_category ? (
              <span className="text-2xs text-muted-foreground">
                · {f.fine_category}
              </span>
            ) : null}
            <span className="text-2xs text-muted-foreground">·</span>
            <span className="font-mono text-2xs text-muted-foreground">
              {f.tool_name}
            </span>
            {typeof f.composite_score === "number" ? (
              <span className="text-2xs text-muted-foreground">
                · score {f.composite_score}
              </span>
            ) : null}
            {f.confidence ? (
              <span className="text-2xs text-muted-foreground">
                · {f.confidence}
              </span>
            ) : null}
            {f.baseline_state ? (
              <span className="text-2xs text-muted-foreground">
                · {f.baseline_state}
              </span>
            ) : null}
          </div>
          <h1 className="text-xl font-semibold">{f.title}</h1>
          <p className="text-xs text-muted-foreground mt-0.5 font-mono">
            {f.file_path}
            {f.line_start ? `:${f.line_start}` : ""}
          </p>
          {f.corroborated_by && f.corroborated_by.length > 0 ? (
            <p className="text-xs text-muted-foreground mt-1">
              Flagged by {f.corroborated_by.join(", ")}
            </p>
          ) : null}
          <p className="text-xs text-muted-foreground mt-1">
            Created {fmtTime(f.created_at)}
            {f.introduced_in_scan_id ? (
              <>
                {" · introduced in "}
                <Link
                  to="/scans/$scanId"
                  params={{ scanId: f.introduced_in_scan_id }}
                  className="font-mono hover:underline"
                >
                  {f.introduced_in_scan_id.slice(0, 8)}
                </Link>
              </>
            ) : null}
            {f.updated_at ? <> · updated {fmtTime(f.updated_at)}</> : null}
          </p>
          <div className="flex flex-wrap gap-3 mt-1 text-xs">
            {f.scan_id ? (
              <Link
                to="/scans/$scanId"
                params={{ scanId: f.scan_id }}
                className="hover:underline"
              >
                Open in scan
              </Link>
            ) : null}
            {f.repo_id ? (
              <Link
                to="/repos/$repoId"
                params={{ repoId: f.repo_id }}
                className="hover:underline"
              >
                Open in repo
              </Link>
            ) : null}
          </div>
        </div>
        <FixFindingButton
          findingId={f.id}
          repoId={f.repo_id}
          scanId={f.scan_id}
        />
      </div>

      <section className="glass-card p-5 flex flex-wrap items-center gap-2">
        <span className="text-xs text-muted-foreground mr-1">Status</span>
        {STATUSES.map((s) => (
          <button
            key={s.value}
            type="button"
            disabled={setStatus.isPending}
            onClick={() => setStatus.mutate(s.value)}
            className={
              "px-2.5 h-8 rounded-md text-xs font-medium ring-1 transition disabled:opacity-50 " +
              (f.status === s.value
                ? "bg-status-info/15 text-status-info ring-status-info/30"
                : "bg-transparent text-muted-foreground ring-border hover:bg-muted/40")
            }
          >
            {s.label}
          </button>
        ))}
        {f.suppressed ? (
          <span className="text-xs text-muted-foreground ml-auto">
            Suppressed
            {f.suppressed_reason ? `: ${f.suppressed_reason}` : ""}
          </span>
        ) : (
          <button
            type="button"
            disabled={suppress.isPending}
            onClick={() => suppress.mutate(f)}
            className="ml-auto h-8 px-3 rounded-md border border-border text-xs hover:bg-muted/40 disabled:opacity-50"
          >
            {suppress.isPending ? "Suppressing…" : "Suppress"}
          </button>
        )}
      </section>

      {f.description && (
        <section className="glass-card p-5">
          <h2 className="text-sm font-medium mb-2">Description</h2>
          <p className="text-sm text-muted-foreground whitespace-pre-line">
            {f.description}
          </p>
        </section>
      )}

      {f.fix_strategy ? (
        <section className="glass-card p-5">
          <h2 className="text-sm font-medium mb-2">{f.fix_strategy.title}</h2>
          <pre className="text-sm text-muted-foreground whitespace-pre-wrap">
            {f.fix_strategy.body}
          </pre>
        </section>
      ) : f.fix_strategy_id ? (
        <section className="text-xs text-muted-foreground">
          Fix strategy: <span className="font-mono">{f.fix_strategy_id}</span>
        </section>
      ) : null}

      {f.code_snippet && (
        <section>
          <h2 className="text-sm font-medium mb-2">Code</h2>
          <pre className="log-viewer">{f.code_snippet}</pre>
        </section>
      )}

      {f.ai_fix_suggestion && (
        <section className="glass-card p-5">
          <h2 className="text-sm font-medium mb-2">AI fix suggestion</h2>
          <pre className="log-viewer text-2xs whitespace-pre-wrap">
            {f.ai_fix_suggestion}
          </pre>
        </section>
      )}

      {(f.cwe_id || f.rule_id) && (
        <section className="text-xs text-muted-foreground space-x-3">
          {f.rule_id && <span>Rule: {f.rule_id}</span>}
          {f.cwe_id && <span>{f.cwe_id}</span>}
        </section>
      )}
    </div>
  );
}

function fmtTime(iso?: string) {
  if (!iso) return "—";
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString();
}
