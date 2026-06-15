// DiffViewer — renders the proposed unified diff a fix job assembled on its
// branch (the v1 deliverable). Loads it lazily from GET /fixes/{id}/diff
// (text/plain) and colourises hunk/added/removed lines with Nocturne classes.
// No bespoke page CSS — just the .glass-card / .mono frame.
import { useQuery } from "@tanstack/react-query";
import { FileDiffIcon } from "lucide-react";
import { fetchFixDiff } from "@/lib/fixes";

export function DiffViewer({ fixId, enabled = true }: { fixId: string; enabled?: boolean }) {
  const q = useQuery({
    queryKey: ["fix-diff", fixId],
    enabled,
    queryFn: () => fetchFixDiff(fixId),
  });

  return (
    <div className="glass-card p-5 space-y-3">
      <div className="flex items-center gap-2 text-sm font-medium">
        <FileDiffIcon className="size-4 text-muted-foreground" />
        Proposed diff
      </div>

      {q.isLoading ? (
        <p className="text-xs text-muted-foreground">Loading diff…</p>
      ) : q.isError ? (
        <p className="text-xs text-destructive">Failed to load the proposed diff.</p>
      ) : !q.data ? (
        <p className="text-xs text-muted-foreground">
          No diff available yet — it appears once the worker assembles the fix
          branch.
        </p>
      ) : (
        <pre className="mono text-[11px] leading-relaxed max-h-[28rem] overflow-auto rounded-md border border-border/40 bg-black/40 p-3 whitespace-pre">
          {q.data.split("\n").map((line, i) => (
            <div key={i} className={diffLineClass(line)}>
              {line || " "}
            </div>
          ))}
        </pre>
      )}
    </div>
  );
}

// diffLineClass picks a Nocturne-friendly colour per unified-diff line kind.
function diffLineClass(line: string): string {
  if (line.startsWith("+++") || line.startsWith("---")) return "text-muted-foreground";
  if (line.startsWith("@@")) return "text-sky-300";
  if (line.startsWith("+")) return "text-emerald-300";
  if (line.startsWith("-")) return "text-rose-300";
  if (line.startsWith("diff ") || line.startsWith("index ")) return "text-muted-foreground/70";
  return "text-foreground/70";
}
