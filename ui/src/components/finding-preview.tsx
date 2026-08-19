// Slide-in side panel that previews a finding without leaving the list.
// Used by the findings table's Enter shortcut + row click. Contains
// description, code snippet with log-viewer styling, AI fix suggestion
// (when present), and status-mutation buttons.
import { useEffect } from "react";
import { XIcon, ExternalLinkIcon } from "lucide-react";
import { Link } from "@tanstack/react-router";
import type { Finding, FindingStatus } from "@/lib/types";
import { SeverityBadge } from "./severity-badge";
import { cn } from "@/lib/utils";
import { api } from "@/lib/api";
import { useQueryClient, useMutation } from "@tanstack/react-query";
import { toast } from "sonner";

interface FindingPreviewProps {
  finding: Finding | null;
  onClose: () => void;
}

export function FindingPreview({ finding, onClose }: FindingPreviewProps) {
  // Close on Escape, regardless of focus.
  useEffect(() => {
    if (!finding) return;
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") {
        e.preventDefault();
        onClose();
      }
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [finding, onClose]);

  const qc = useQueryClient();
  const updateStatus = useMutation({
    mutationFn: async ({
      id,
      status,
    }: {
      id: string;
      status: FindingStatus;
    }) => {
      await api.put(`/findings/${id}`, { status });
    },
    onSuccess: (_d, vars) => {
      qc.invalidateQueries({ queryKey: ["findings"] });
      qc.invalidateQueries({ queryKey: ["finding", vars.id] });
      toast.success(`Marked ${vars.status.replace("_", " ")}`);
    },
    onError: (e) => toast.error(e instanceof Error ? e.message : "Update failed"),
  });

  if (!finding) return null;

  return (
    <aside
      className={cn(
        "fixed top-0 right-0 z-40 h-screen w-full sm:w-[28rem] md:w-[36rem]",
        "glass-card border-l rounded-none bg-popover/95 backdrop-blur-2xl",
        "animate-slide-in-right shadow-2xl",
        "flex flex-col",
      )}
      role="dialog"
      aria-label="Finding preview"
    >
      <header className="px-5 py-4 border-b border-border/50 flex items-start gap-3">
        <SeverityBadge severity={finding.severity} />
        <div className="flex-1 min-w-0">
          <div className="text-xs uppercase tracking-wide text-muted-foreground">
            {finding.tool_name} · {finding.category}
          </div>
          <h2 className="text-base font-semibold leading-tight mt-0.5">
            {finding.title}
          </h2>
          <div className="text-2xs text-muted-foreground font-mono mt-0.5 truncate">
            {finding.file_path}
            {finding.line_start ? `:${finding.line_start}` : ""}
          </div>
        </div>
        <Link
          to="/findings/$findingId"
          params={{ findingId: finding.id }}
          className="size-7 grid place-items-center rounded-md hover:bg-muted/50"
          title="Open full page"
        >
          <ExternalLinkIcon className="size-4 text-muted-foreground" />
        </Link>
        <button
          type="button"
          onClick={onClose}
          className="size-7 grid place-items-center rounded-md hover:bg-muted/50"
          aria-label="Close"
        >
          <XIcon className="size-4" />
        </button>
      </header>

      <div className="flex-1 overflow-y-auto px-5 py-4 space-y-5 text-sm">
        {finding.description && (
          <section>
            <h3 className="text-2xs uppercase tracking-wide text-muted-foreground mb-1.5">
              Description
            </h3>
            <p className="whitespace-pre-line text-foreground/90">
              {finding.description}
            </p>
          </section>
        )}

        {finding.code_snippet && (
          <section>
            <h3 className="text-2xs uppercase tracking-wide text-muted-foreground mb-1.5">
              Code
            </h3>
            <pre className="log-viewer text-2xs whitespace-pre-wrap">
              {finding.code_snippet}
            </pre>
          </section>
        )}

        {finding.ai_fix_suggestion && (
          <section>
            <h3 className="text-2xs uppercase tracking-wide text-muted-foreground mb-1.5">
              AI fix suggestion
            </h3>
            <pre className="log-viewer text-2xs whitespace-pre-wrap">
              {finding.ai_fix_suggestion}
            </pre>
          </section>
        )}

        {(finding.cwe_id || finding.rule_id) && (
          <section className="text-2xs text-muted-foreground space-x-3">
            {finding.rule_id && <span>Rule: {finding.rule_id}</span>}
            {finding.cwe_id && <span>{finding.cwe_id}</span>}
          </section>
        )}
      </div>

      <footer className="px-5 py-3 border-t border-border/50 flex flex-wrap gap-2">
        <StatusBtn
          label="Open"
          active={finding.status === "open"}
          onClick={() =>
            updateStatus.mutate({ id: finding.id, status: "open" })
          }
        />
        <StatusBtn
          label="Fixed"
          tone="success"
          active={finding.status === "fixed"}
          onClick={() =>
            updateStatus.mutate({ id: finding.id, status: "fixed" })
          }
        />
        <StatusBtn
          label="Won't fix"
          tone="muted"
          active={finding.status === "wont_fix"}
          onClick={() =>
            updateStatus.mutate({ id: finding.id, status: "wont_fix" })
          }
        />
        <StatusBtn
          label="False positive"
          tone="muted"
          active={finding.status === "false_positive"}
          onClick={() =>
            updateStatus.mutate({ id: finding.id, status: "false_positive" })
          }
        />
      </footer>
    </aside>
  );
}

function StatusBtn({
  label,
  active,
  tone,
  onClick,
}: {
  label: string;
  active?: boolean;
  tone?: "success" | "muted";
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(
        "px-2.5 h-8 rounded-md text-xs font-medium ring-1 transition",
        active
          ? tone === "success"
            ? "bg-status-success/15 text-status-success ring-status-success/30"
            : tone === "muted"
              ? "bg-status-neutral/15 text-status-neutral ring-status-neutral/30"
              : "bg-status-info/15 text-status-info ring-status-info/30"
          : "bg-transparent text-muted-foreground ring-border hover:bg-muted/40",
      )}
    >
      {label}
    </button>
  );
}
