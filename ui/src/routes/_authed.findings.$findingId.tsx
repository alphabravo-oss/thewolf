// Finding detail. Surface tool name, severity, location, code snippet,
// AI fix suggestion, status controls (open / fixed / wont_fix / false_positive).
import { createFileRoute, Link } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { ArrowLeftIcon } from "lucide-react";
import { api } from "@/lib/api";
import type { Finding } from "@/lib/types";
import { CardSkeleton } from "@/components/skeleton";
import { SeverityBadge } from "@/components/severity-badge";

export const Route = createFileRoute("/_authed/findings/$findingId")({
  component: FindingDetailPage,
});

function FindingDetailPage() {
  const { findingId } = Route.useParams();
  const q = useQuery({
    queryKey: ["finding", findingId],
    queryFn: async () => {
      const r = await api.get<Finding>(`/findings/${findingId}`);
      return r.data;
    },
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
            <span className="text-2xs text-muted-foreground">·</span>
            <span className="font-mono text-2xs text-muted-foreground">
              {f.tool_name}
            </span>
          </div>
          <h1 className="text-xl font-semibold">{f.title}</h1>
          <p className="text-xs text-muted-foreground mt-0.5 font-mono">
            {f.file_path}
            {f.line_start ? `:${f.line_start}` : ""}
          </p>
        </div>
      </div>

      {f.description && (
        <section className="glass-card p-5">
          <h2 className="text-sm font-medium mb-2">Description</h2>
          <p className="text-sm text-muted-foreground whitespace-pre-line">
            {f.description}
          </p>
        </section>
      )}

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
