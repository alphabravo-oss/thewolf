import { EmptyState, ErrorState, PermissionState } from "@/components/ui/empty-state";
// Finding detail. Surface tool name, severity, location, code snippet,
// AI fix suggestion, status controls (open / wont_fix / false_positive).
import { createFileRoute, Link } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowLeftIcon } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { api, ApiError, isNotFound } from "@/lib/api";
import type { Finding, FindingStatus } from "@/lib/types";
import { CardSkeleton } from "@/components/skeleton";
import { SeverityBadge } from "@/components/severity-badge";
import { Badge } from "@/components/ui/badge";
import { findingTitle } from "@/lib/finding-title";
import { useAutofixEnabled } from "@/lib/fixes";
import { FixFindingButton } from "@/components/fixes/fix-finding-button";
import { Section } from "@/components/ui/section";
import { ConfirmDialog } from "@/components/confirm-dialog";

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
  const { enabled: autofixEnabled } = useAutofixEnabled();
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

  // Suppress is two steps: preview how many findings it would hide, ask in a
  // ConfirmDialog, then save.
  const [suppressPreview, setSuppressPreview] = useState<{ finding: Finding; count: number; hidden: number } | null>(null);
  const previewSuppress = useMutation({
    mutationFn: async (finding: Finding) => {
      const preview = await api.post<{ count?: number; hidden_count?: number }>(
        "/suppressions/preview",
        { finding_id: finding.id, reason: "user" },
      );
      const count = preview.data?.count ?? 0;
      return { finding, count, hidden: preview.data?.hidden_count ?? count };
    },
    onSuccess: setSuppressPreview,
    onError: (e) =>
      toast.error(e instanceof Error ? e.message : "Suppress failed"),
  });
  const suppress = useMutation({
    mutationFn: (finding: Finding) =>
      api.post("/suppressions", {
        finding_id: finding.id,
        repo_id: finding.repo_id,
        reason: "user",
      }),
    onSuccess: () => {
      setSuppressPreview(null);
      qc.invalidateQueries({ queryKey: ["finding", findingId] });
      qc.invalidateQueries({ queryKey: ["findings"] });
      toast.success("Suppression saved");
    },
    onError: (e) =>
      toast.error(e instanceof Error ? e.message : "Suppress failed"),
  });

  if (q.isLoading) {
    return (
      <div className="page stack page--mid">
        <CardSkeleton />
        <CardSkeleton />
      </div>
    );
  }
  if (q.error instanceof ApiError && q.error.status === 403) return <PermissionState description="You do not have access to this finding." />;
  if (isNotFound(q.error) || (!q.isLoading && !q.isError && !q.data)) return <EmptyState title="Finding not found" description="This finding may have been removed." cta={{ label: "Findings", to: "/findings" }} />;
  if (q.isError || !q.data) {
    return <ErrorState title="Finding unavailable" description="The finding could not be loaded. Check your access or retry." onRetry={() => void q.refetch()} />;
  }
  const f = q.data;
  const evidence = f.evidence?.length ? f.evidence : [f];
  const tools = f.corroborated_by?.length ? f.corroborated_by : [...new Set(evidence.map((row) => row.tool_name))];
  const severitiesDiffer = new Set(evidence.map((row) => row.severity)).size > 1;
  const descriptions = new Set<string>();
  const reports = evidence.map((row) => {
    const description = row.description?.trim();
    const showDescription = description && !descriptions.has(description);
    if (description) descriptions.add(description);
    return { ...row, description: showDescription ? description : "" };
  });
  const code = [f, ...evidence].find((row) => row.file_path === f.file_path && row.line_start === f.line_start && row.code_snippet?.trim() && !/^requires login[.!]?$/i.test(row.code_snippet.trim()));
  const language = fileLanguage(f.file_path);
  const guidance = f.fix_strategy?.body.replace(/(?:\*\*(?:Before|After):\*\*\s*)?```([^\n]*)\n[\s\S]*?```/g, (block, tag: string) => language && fileLanguage(`example.${tag.trim().toLowerCase()}`) === language ? block : "").trim();

  return (
    <div className="page stack page--mid">
      <div className="grid grid-cols-[auto_minmax(0,1fr)] items-start gap-3">
        <Link
          to="/findings"
          className="size-9 grid place-items-center rounded-md hover:bg-muted/50"
          aria-label="Back"
        >
          <ArrowLeftIcon className="size-4" />
        </Link>
        <div className="flex-1 min-w-0">
          <h1 className="text-xl font-semibold leading-snug text-balance sm:text-2xl">{findingTitle(f)}</h1>
          <div className="mt-3 flex flex-wrap items-center gap-x-4 gap-y-2 text-xs text-muted-foreground">
            {f.file_path && <a href="#source-code" className="break-all font-mono underline-offset-4 hover:underline focus-visible:underline">{f.file_path}{f.line_start ? `:${f.line_start}` : ""}</a>}
            <span>{tools.join(" + ")}</span>
            {f.introduced_in_scan_id ? <Link to="/scans/$scanId" params={{ scanId: f.introduced_in_scan_id }} className="hover:underline" title={f.introduced_in_scan_id === f.scan_id ? fmtTime(f.created_at) : "Open the first observed scan"}>First seen {f.introduced_in_scan_id === f.scan_id ? fmtDate(f.created_at) : "in scan"}</Link> : <span title={fmtTime(f.created_at)}>Recorded {fmtDate(f.created_at)}</span>}
            {f.baseline_state && f.baseline_state !== "new" && <span className="capitalize">{f.baseline_state.replaceAll("_", " ")} baseline</span>}
            {!f.baseline_state && <span>Not compared to baseline</span>}
          </div>
          <div className="mt-4 flex flex-wrap items-center gap-2">
            <SeverityBadge severity={f.severity} />
            {f.confidence && <Badge variant="outline" className="font-normal capitalize">{f.confidence} confidence</Badge>}
            {f.baseline_state === "new" && <Badge variant="secondary">New</Badge>}
            {Number.isFinite(f.composite_score) && f.composite_score > 0 && <span className="ml-1 text-xs text-muted-foreground" title="Composite risk score">Score {Math.round(f.composite_score)}</span>}
          </div>
        </div>
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
        {autofixEnabled && <div className="ml-auto"><FixFindingButton findingId={f.id} repoId={f.repo_id} scanId={f.scan_id} /></div>}
        {f.suppressed ? (
          <span className="text-xs text-muted-foreground ml-auto">
            Suppressed
            {f.suppressed_reason ? `: ${f.suppressed_reason}` : ""}
          </span>
        ) : (
          <button
            type="button"
            disabled={previewSuppress.isPending}
            onClick={() => previewSuppress.mutate(f)}
            className="ml-auto h-8 px-3 rounded-md border border-border text-xs hover:bg-muted/40 disabled:opacity-50"
          >
            {previewSuppress.isPending ? "Suppressing…" : "Suppress"}
          </button>
        )}
      </section>

      {f.description?.trim() && !descriptions.has(f.description.trim()) && <section className="glass-card p-5"><h2 className="mb-2 text-sm font-medium">Description</h2><p className="whitespace-pre-line text-sm leading-relaxed text-muted-foreground">{f.description}</p></section>}

      <section className="glass-card space-y-4 p-5" aria-label="Scanner evidence">
        <div className="flex flex-wrap items-center justify-between gap-2"><h2 className="text-sm font-medium">Scanner evidence</h2><span className="text-xs text-muted-foreground">{tools.length} scanner{tools.length === 1 ? "" : "s"}</span></div>
        {severitiesDiffer && <p className="text-xs leading-relaxed text-muted-foreground">Scanners assigned different severities. Their original assessments are preserved below.</p>}
        <ul className="divide-y divide-border">{reports.map((row) => <li key={row.id} className="space-y-2 py-4 first:pt-0 last:pb-0"><div className="flex flex-wrap items-center gap-2"><span className="text-sm font-medium">{row.tool_name}</span><SeverityBadge severity={row.severity} size="sm" /></div>{(row.advisory_id || row.rule_id) && <p className="break-all font-mono text-xs text-muted-foreground">{[row.advisory_id, row.rule_id].filter((id, index, ids) => id && ids.indexOf(id) === index).join(" · ")}</p>}{(row.file_path !== f.file_path || row.line_start !== f.line_start) && <p className="break-all font-mono text-xs text-muted-foreground">{row.file_path}{row.line_start ? `:${row.line_start}` : ""}</p>}{row.description && <p className="whitespace-pre-line text-sm leading-relaxed text-muted-foreground">{row.description}</p>}</li>)}</ul>
        {f.confidence && <Section variant="plain" className="text-xs text-muted-foreground" title="How confidence is determined"><p className="mt-2 leading-relaxed">Confidence reflects recorded cross-tool agreement: one scanner is low, two medium, and three or more high. Severity describes impact; confidence describes agreement.</p></Section>}
      </section>

      {f.file_path && <section id="source-code" className="glass-card scroll-mt-20 overflow-hidden" tabIndex={-1}>
        <div className="flex flex-wrap items-center justify-between gap-2 border-b border-border px-5 py-3"><h2 className="text-sm font-medium">Code</h2><span className="break-all font-mono text-xs text-muted-foreground">{f.file_path}{f.line_start ? `:${f.line_start}` : ""}</span></div>
        {code ? <><pre className="overflow-x-auto bg-muted/30 p-5 font-mono text-xs leading-6">{code.code_snippet}</pre><p className="px-5 py-2 text-xs text-muted-foreground">Recorded by {code.tool_name}</p></> : <p className="p-5 text-sm text-muted-foreground">{f.code_snippet ? "Source preview unavailable. The scanner did not return source code for this location." : "No source snippet was recorded for this finding."}</p>}
      </section>}

      {f.fix_strategy && guidance && <section className="glass-card p-5">
        <h2 className="mb-2 text-sm font-medium">{f.fix_strategy.title}</h2>
        <pre className="whitespace-pre-wrap font-sans text-sm leading-relaxed text-muted-foreground">{guidance}</pre>
      </section>}

      {f.ai_fix_suggestion && <section className="glass-card p-5"><h2 className="mb-2 text-sm font-medium">AI fix suggestion</h2><pre className="log-viewer whitespace-pre-wrap text-xs">{f.ai_fix_suggestion}</pre></section>}

      <footer className="flex flex-wrap items-center gap-4 text-xs text-muted-foreground">
        {f.cwe_id && <span className="font-mono">{f.cwe_id}</span>}
        {f.scan_id && <Link to="/scans/$scanId" params={{ scanId: f.scan_id }} className="hover:underline">View scan</Link>}
        {f.repo_id && <Link to="/repos/$repoId" params={{ repoId: f.repo_id }} className="hover:underline">View repository</Link>}
      </footer>
      <ConfirmDialog
        open={!!suppressPreview}
        onOpenChange={(open) => !open && setSuppressPreview(null)}
        title="Save this suppression?"
        description={suppressPreview && `It matches ${suppressPreview.count} current finding${suppressPreview.count === 1 ? "" : "s"}; ${suppressPreview.hidden} will be hidden in the latest scan.`}
        confirmLabel="Save suppression"
        variant="default"
        pending={suppress.isPending}
        onConfirm={() => suppressPreview && suppress.mutate(suppressPreview.finding)}
      />
    </div>
  );
}

function fmtTime(iso?: string) {
  if (!iso) return "—";
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString();
}

function fmtDate(iso?: string) {
  if (!iso) return "Unknown";
  const date = new Date(iso);
  return Number.isNaN(date.getTime()) ? "Unknown" : date.toLocaleDateString(undefined, { month: "short", day: "numeric", year: "numeric" });
}

function fileLanguage(path: string): string | undefined {
  const extension = path.split(".").at(-1)?.toLowerCase();
  const languages: Record<string, string> = { go: "go", golang: "go", py: "python", python: "python", js: "javascript", jsx: "javascript", javascript: "javascript", ts: "typescript", tsx: "typescript", typescript: "typescript", java: "java", rb: "ruby", ruby: "ruby", rs: "rust", rust: "rust", php: "php", cs: "csharp", csharp: "csharp", c: "c", cpp: "cpp", sh: "bash", bash: "bash", yaml: "yaml", yml: "yaml", json: "json", dockerfile: "dockerfile" };
  return languages[extension || ""] || (path.split("/").at(-1)?.toLowerCase() === "dockerfile" ? "dockerfile" : undefined);
}
