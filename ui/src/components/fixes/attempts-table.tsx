// AttemptsTable — the per-finding audit trail for a fix job. Each row is one
// engine attempt at one finding and how the verification gate judged it: which
// engine ran, whether the change built, whether the targeted rescan cleared
// the finding, how many new findings it introduced, and the final outcome
// (kept | rolled_back | unfixable). Reliability principle in UI form — we show
// the verify outcomes, never the engine's self-report.
import { ListChecksIcon } from "lucide-react";
import type { FixAttempt, FixAttemptOutcome } from "@/lib/fixes";

export function AttemptsTable({ attempts }: { attempts: FixAttempt[] }) {
  return (
    <div className="glass-card overflow-hidden">
      <div className="flex items-center gap-2 px-5 py-3 text-sm font-medium border-b border-border/30">
        <ListChecksIcon className="size-4 text-muted-foreground" />
        Attempts
        <span className="text-xs text-muted-foreground">({attempts.length})</span>
      </div>
      {attempts.length === 0 ? (
        <p className="px-5 py-4 text-sm text-muted-foreground">
          No attempts recorded yet.
        </p>
      ) : (
        <table className="w-full text-sm">
          <thead className="text-xs uppercase tracking-wide text-muted-foreground bg-muted/20">
            <tr>
              <th className="text-left px-4 py-2">Finding</th>
              <th className="text-right px-4 py-2">#</th>
              <th className="text-left px-4 py-2">Engine</th>
              <th className="text-center px-4 py-2">Built</th>
              <th className="text-center px-4 py-2">Cleared</th>
              <th className="text-right px-4 py-2">New</th>
              <th className="text-left px-4 py-2">Outcome</th>
            </tr>
          </thead>
          <tbody>
            {attempts.map((a) => (
              <tr key={a.id} className="border-t border-border/20">
                <td className="px-4 py-2 font-mono text-xs text-muted-foreground">
                  {a.finding_id.slice(0, 8)}
                </td>
                <td className="px-4 py-2 text-right tabular-nums">{a.attempt_no}</td>
                <td className="px-4 py-2 font-mono text-xs">
                  {a.engine_used}
                  {a.model ? ` · ${a.model}` : ""}
                </td>
                <td className="px-4 py-2 text-center">
                  <BoolDot ok={a.built} />
                </td>
                <td className="px-4 py-2 text-center">
                  <BoolDot ok={a.finding_cleared} />
                </td>
                <td className="px-4 py-2 text-right tabular-nums">
                  {a.new_findings > 0 ? (
                    <span className="text-amber-300">{a.new_findings}</span>
                  ) : (
                    a.new_findings
                  )}
                </td>
                <td className="px-4 py-2">
                  <OutcomeBadge outcome={a.outcome} />
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}

function BoolDot({ ok }: { ok: boolean }) {
  return (
    <span
      className={
        "inline-block size-2 rounded-full " +
        (ok ? "bg-emerald-400" : "bg-rose-400/70")
      }
      title={ok ? "yes" : "no"}
    />
  );
}

function OutcomeBadge({ outcome }: { outcome: FixAttemptOutcome }) {
  const cls =
    outcome === "kept"
      ? "text-emerald-300 bg-emerald-500/10 border-emerald-500/30"
      : outcome === "rolled_back"
        ? "text-amber-300 bg-amber-500/10 border-amber-500/30"
        : "text-rose-300 bg-rose-500/10 border-rose-500/30";
  return (
    <span
      className={`text-[10px] uppercase tracking-wide border rounded px-1.5 py-0.5 ${cls}`}
    >
      {outcome.replace("_", " ")}
    </span>
  );
}
