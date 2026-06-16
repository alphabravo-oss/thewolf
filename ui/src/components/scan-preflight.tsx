// useScanWithPreflight wraps the "start a scan" flow with an image preflight:
// before creating the scan it asks the server which selected scanners are
// missing their container image (POST /scans/preflight). If any are missing it
// prompts the user to pull them first (the runner uses --pull never, so a
// missing image would otherwise fail mid-scan), then starts the scan. The hook
// returns a `launch()` to call from a button and a `dialog` element to render.
import { useState } from "react";
import { useNavigate } from "@tanstack/react-router";
import { useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { api } from "@/lib/api";
import type { Scan } from "@/lib/types";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogFooter,
  DialogTitle,
  DialogDescription,
} from "@/components/ui/dialog";

export type ScanParams = {
  repo_id: string;
  branch?: string;
  tools?: string[];
  all_scanners?: boolean;
};

type MissingImage = { tool: string; image: string };
type Phase = "idle" | "checking" | "prompt" | "pulling";

export function useScanWithPreflight() {
  const navigate = useNavigate();
  const qc = useQueryClient();
  const [phase, setPhase] = useState<Phase>("idle");
  const [pending, setPending] = useState<ScanParams | null>(null);
  const [missing, setMissing] = useState<MissingImage[]>([]);
  const [progress, setProgress] = useState({ done: 0, total: 0, current: "" });

  function reset() {
    setPhase("idle");
    setPending(null);
    setMissing([]);
    setProgress({ done: 0, total: 0, current: "" });
  }

  async function doScan(params: ScanParams) {
    try {
      const r = await api.post<Scan>("/scans", params);
      toast.success("Scan started");
      qc.invalidateQueries({ queryKey: ["scans"] });
      navigate({ to: "/scans/$scanId", params: { scanId: r.data.id } });
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "Failed to start scan");
    } finally {
      reset();
    }
  }

  async function launch(params: ScanParams) {
    setPhase("checking");
    try {
      const r = await api.post<{ missing: MissingImage[] }>("/scans/preflight", params);
      const miss = r.data.missing ?? [];
      if (miss.length === 0) {
        await doScan(params);
        return;
      }
      setPending(params);
      setMissing(miss);
      setPhase("prompt");
    } catch {
      // Preflight is best-effort: if it errors, don't block — just scan.
      await doScan(params);
    }
  }

  async function pullAndScan() {
    if (!pending) return;
    setPhase("pulling");
    const images = Array.from(new Set(missing.map((m) => m.image)));
    let done = 0;
    for (const image of images) {
      setProgress({ done, total: images.length, current: image });
      try {
        await api.post("/scanners/images/pull", { image });
      } catch {
        toast.error(`Failed to pull ${image}`);
      }
      done += 1;
      setProgress({ done, total: images.length, current: image });
    }
    qc.invalidateQueries({ queryKey: ["scanner-images"] });
    await doScan(pending);
  }

  const plural = missing.length === 1 ? "" : "s";
  const dialog = (
    <Dialog
      open={phase === "prompt" || phase === "pulling"}
      onOpenChange={(open) => {
        if (!open && phase === "prompt") reset();
      }}
    >
      <DialogContent>
        <DialogHeader>
          <DialogTitle>
            {phase === "pulling"
              ? "Pulling scanner images"
              : `${missing.length} scanner${plural} need${plural ? "" : "s"} an image`}
          </DialogTitle>
          <DialogDescription>
            {phase === "pulling"
              ? `Pulling ${Math.min(progress.done + 1, progress.total)} of ${progress.total}: ${progress.current}`
              : `These selected scanners aren't pulled on this machine yet. Pull them now, or scan without them (they'll be skipped, not failed).`}
          </DialogDescription>
        </DialogHeader>

        {phase === "prompt" && (
          <ul className="max-h-48 overflow-auto rounded-md border border-border divide-y divide-border text-sm">
            {missing.map((m) => (
              <li key={m.tool} className="flex items-center justify-between gap-3 px-3 py-1.5">
                <span className="font-medium">{m.tool}</span>
                <span className="font-mono text-xs text-muted-foreground truncate">{m.image}</span>
              </li>
            ))}
          </ul>
        )}

        {phase === "pulling" && (
          <div className="h-1.5 w-full overflow-hidden rounded-full bg-muted">
            <div
              className="h-full bg-primary transition-all"
              style={{ width: `${progress.total ? (progress.done / progress.total) * 100 : 0}%` }}
            />
          </div>
        )}

        <DialogFooter>
          {phase === "prompt" ? (
            <>
              <button
                type="button"
                onClick={reset}
                className="h-9 px-3 rounded-md border border-border text-sm hover:bg-muted/40"
              >
                Cancel
              </button>
              <button
                type="button"
                onClick={() => pending && doScan(pending)}
                className="h-9 px-3 rounded-md border border-border text-sm hover:bg-muted/40"
              >
                Scan without them
              </button>
              <button
                type="button"
                onClick={pullAndScan}
                className="h-9 px-3 rounded-md bg-primary text-primary-foreground text-sm font-medium hover:opacity-90"
              >
                Pull &amp; scan
              </button>
            </>
          ) : (
            <span className="text-xs text-muted-foreground">
              Large images can take a few minutes. The scan starts automatically when pulls finish.
            </span>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );

  return { launch, dialog, busy: phase !== "idle" };
}
