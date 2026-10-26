// useScanWithPreflight wraps the "start a scan" flow with an image preflight:
// before creating the scan it asks the server which selected scanners are
// missing their container image (POST /scans/preflight). If any are missing it
// prompts the user to pull them first (the runner uses --pull never, so a
// missing image would otherwise fail mid-scan), then starts the scan. The hook
// returns a `launch()` to call from a button and a `dialog` element to render.
import { useRef, useState } from "react";
import { useNavigate } from "@tanstack/react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { api } from "@/lib/api";
import { safeErrorMessage } from "@/lib/safe-display";
import { useIsAdmin } from "@/lib/me";
import type { Repo, Scan } from "@/lib/types";
import { runtimeCapabilitiesQuery } from "@/lib/runtime-capabilities";
import { Button } from "@/components/ui/button";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
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
  collection_id?: string;
  branch?: string;
  tools?: string[];
  all_scanners?: boolean;
  profile?: string;
};

type MissingImage = { tool: string; image: string };
type Phase = "idle" | "configuring" | "checking" | "check_failed" | "prompt" | "pulling" | "pull_failed" | "submitting" | "submit_failed";

export function useScanWithPreflight() {
  const navigate = useNavigate();
  const qc = useQueryClient();
  const isAdmin = useIsAdmin();
  const phaseRef = useRef<Phase>("idle");
  const requestKey = useRef("");
  const [failure, setFailure] = useState("");
  const [phase, setPhase] = useState<Phase>("idle");
  const [pending, setPending] = useState<ScanParams | null>(null);
  const [toolCount, setToolCount] = useState<number | null>(null);
  const [missing, setMissing] = useState<MissingImage[]>([]);
  const [failedImages, setFailedImages] = useState<string[]>([]);
  const [progress, setProgress] = useState({ done: 0, total: 0, current: "" });

  function transition(next: Phase) {
    phaseRef.current = next;
    setPhase(next);
  }

  function reset() {
    transition("idle");
    requestKey.current = "";
    setPending(null);
    setMissing([]);
    setFailedImages([]);
    setFailure("");
    setProgress({ done: 0, total: 0, current: "" });
  }

  async function doScan(params: ScanParams) {
    if (phaseRef.current === "submitting") return;
    transition("submitting");
    setFailure("");
    try {
      const r = await api.post<Scan>("/scans", params, {
        headers: { "Idempotency-Key": requestKey.current },
      });
      if (!r.data?.id) throw new Error("The server did not return a scan ID. Retry to recover this request.");
      toast.success(r.data.status === "pending" ? "Scan queued" : "Scan started");
      void qc.invalidateQueries({ queryKey: ["scans"] });
      void qc.invalidateQueries({ queryKey: ["fleet"] });
      void qc.invalidateQueries({ queryKey: ["setup"] });
      reset();
      void navigate({ to: "/scans/$scanId", params: { scanId: r.data.id } });
    } catch (e) {
      // Retain this exact intent/key: a lost response may hide a created scan.
      setFailure(safeErrorMessage(e, "Failed to start scan"));
      transition("submit_failed");
    }
  }

  async function checkAndScan(params: ScanParams) {
    transition("checking");
    setFailure("");
    try {
      const capabilities = await qc.fetchQuery({ ...runtimeCapabilitiesQuery, staleTime: 0 });
      if (typeof capabilities?.docker_image_management !== "boolean") {
        throw new Error("Scanner readiness is unavailable. Try again after checking service health.");
      }
      if (capabilities.queue_execution && capabilities.worker_readiness?.status === "unavailable") {
        toast.warning("No active scan worker is available. Your scan will be queued until an administrator starts one.");
      }
      if (!capabilities.docker_image_management) {
        await doScan(params);
        return;
      }
      const r = await api.post<{ missing: MissingImage[]; tool_count?: number }>("/scans/preflight", params);
      if (!Array.isArray(r.data?.missing) || r.data.missing.some((m) => !m || typeof m.tool !== "string" || !m.tool || typeof m.image !== "string" || !m.image)) {
        throw new Error("The server returned an invalid scanner readiness result.");
      }
      if (r.data.missing.length === 0) {
        await doScan(params);
        return;
      }
      setMissing(r.data.missing);
      setToolCount(r.data.tool_count ?? null);
      setFailedImages([]);
      transition("prompt");
    } catch (e) {
      setFailure(safeErrorMessage(e, "Could not check scanner readiness"));
      transition("check_failed");
    }
  }

  function configure(params: ScanParams) {
    if (phaseRef.current !== "idle") return;
    setPending(structuredClone(params));
    transition("configuring");
  }

  async function launch(params: ScanParams) {
    if (phaseRef.current !== "idle") return;
    // A synchronous guard covers clicks before React renders the busy state.
    transition("checking");
    const intent = structuredClone(params);
    // getRandomValues also works on HTTP intranets, where randomUUID is absent.
    requestKey.current = Array.from(crypto.getRandomValues(new Uint8Array(16)), (byte) => byte.toString(16).padStart(2, "0")).join("");
    setPending(intent);
    await checkAndScan(intent);
  }

  async function pullAndScan() {
    if (!pending || !isAdmin || !["prompt", "pull_failed"].includes(phaseRef.current)) return;
    transition("pulling");
    setFailure("");
    const images = failedImages.length > 0
      ? failedImages
      : Array.from(new Set(missing.map((m) => m.image)));
    setFailedImages([]);
    let done = 0;
    const failures: string[] = [];
    for (const image of images) {
      setProgress({ done, total: images.length, current: image });
      try {
        await api.post("/scanners/images/pull", { image });
      } catch (e) {
        failures.push(image);
        setFailure(safeErrorMessage(e, "Scanner download failed"));
      }
      done += 1;
      setProgress({ done, total: images.length, current: image });
    }
    void qc.invalidateQueries({ queryKey: ["scanner-images"] });
    void qc.invalidateQueries({ queryKey: ["scanners", "images"] });
    if (failures.length > 0) {
      setFailedImages(failures);
      transition("pull_failed");
      return;
    }
    // A pull receipt alone is not readiness; the required image set can change.
    await checkAndScan(pending);
  }

  const remainingMissing = phase === "pull_failed" ? missing.filter((m) => failedImages.includes(m.image)) : missing;
  const imageCount = new Set(remainingMissing.map((m) => m.image)).size;
  const plural = remainingMissing.length === 1 ? "" : "s";
  const dialog = (
    <Dialog
      open={pending !== null && phase !== "idle"}
      onOpenChange={(open) => {
        if (!open && ["configuring", "prompt", "pull_failed", "check_failed", "submit_failed"].includes(phaseRef.current)) reset();
      }}
    >
      <DialogContent className="max-h-[90dvh] overflow-y-auto">
        {phase === "configuring" && pending ? <ScanConfiguration params={pending} onStart={(params) => { if (phaseRef.current !== "configuring") return; reset(); void launch(params); }} /> : <>
        <DialogHeader>
          <DialogTitle>
            {phase === "checking" ? "Checking scanner readiness"
              : phase === "check_failed" ? "Scanner readiness could not be checked"
              : phase === "submitting" ? "Starting scan"
              : phase === "submit_failed" ? "Scan submission could not be confirmed"
              : phase === "pulling"
              ? "Pulling scanner images"
              : phase === "pull_failed"
                ? "Some scanner images could not be pulled"
                : `${remainingMissing.length} scanner${plural} need${plural ? "" : "s"} preparation`}
          </DialogTitle>
          <DialogDescription>
            {phase === "checking" ? "Checking which images your selected scan needs."
              : phase === "check_failed" ? "No scan has started. Retry the readiness check."
              : phase === "submitting" ? "Submitting your selected repository, branch, and scan profile."
              : phase === "submit_failed" ? "Retry uses the same request so a lost response does not create a second scan."
              : phase === "pulling"
              ? `Pulling ${Math.min(progress.done + 1, progress.total)} of ${progress.total}: ${progress.current}`
              : phase === "pull_failed"
                ? `The scan has not started. Retry the ${failedImages.length} failed image pull${failedImages.length === 1 ? "" : "s"}, or explicitly continue without those scanners.`
                : isAdmin
                  ? "Download the required images, or explicitly start with reduced coverage. Missing scanners will not run."
                  : "An administrator must download these images. You can cancel or explicitly start with reduced coverage."}
          </DialogDescription>
        </DialogHeader>

        {failure && <p role="alert" className="text-sm text-destructive">{failure}</p>}
        {(phase === "prompt" || phase === "pull_failed") && (
          <div className="space-y-3">
          <p className="text-sm">Required for this scan: {remainingMissing.length} scanner{plural} {plural ? "need" : "needs"} {imageCount} image{imageCount === 1 ? "" : "s"}.{toolCount !== null && ` ${Math.max(0, toolCount - remainingMissing.length)} of ${toolCount} selected scanners do not need a download.`}</p>
          <p className="text-xs text-muted-foreground">Optional scanners outside your selected profile are not downloaded. Scanning without these images leaves the checks listed below uncovered; a clean result cannot establish complete coverage.</p>
          <ul className="max-h-48 overflow-auto rounded-md border border-border divide-y divide-border text-sm">
            {remainingMissing.map((m) => (
                <li
                  key={m.tool}
                  className="flex min-w-0 items-center justify-between gap-3 px-3 py-1.5"
                >
                  <span className="min-w-0 truncate font-medium">{m.tool}</span>
                  <span className="min-w-0 truncate font-mono text-xs text-muted-foreground">
                    {m.image}
                  </span>
                </li>
              ))}
          </ul>
          </div>
        )}

        {phase === "pulling" && (
          <div
            role="progressbar"
            aria-label="Scanner image pull progress"
            aria-valuemin={0}
            aria-valuemax={progress.total}
            aria-valuenow={progress.done}
            aria-valuetext={`${progress.done} of ${progress.total} download attempts completed`}
            className="h-1.5 w-full overflow-hidden rounded-full bg-muted"
          >
            <div
              className="h-full bg-primary transition-[width] motion-reduce:transition-none"
              style={{
                width: `${progress.total ? (progress.done / progress.total) * 100 : 0}%`,
              }}
            />
          </div>
        )}

        <DialogFooter className="gap-2 sm:flex-wrap sm:space-x-0">
          {phase === "check_failed" || phase === "submit_failed" ? (
            <>
              <button type="button" onClick={reset} className="h-9 rounded-md border border-border px-3 text-sm">Close</button>
              <button type="button" onClick={() => {
                if (!pending) return;
                if (phaseRef.current === "check_failed") void checkAndScan(pending);
                else if (phaseRef.current === "submit_failed") void doScan(pending);
              }} className="h-9 rounded-md bg-primary px-3 text-sm text-primary-foreground">
                {phase === "check_failed" ? "Retry readiness check" : "Retry scan submission"}
              </button>
            </>
          ) : phase === "prompt" || phase === "pull_failed" ? (
            <>
              <button
                type="button"
                onClick={reset}
                className="h-9 rounded-md border border-border px-3 text-sm hover:bg-muted/40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
              >
                Cancel
              </button>
              <button
                type="button"
                onClick={() => pending && ["prompt", "pull_failed"].includes(phaseRef.current) && doScan(pending)}
                className="h-9 rounded-md border border-border px-3 text-sm hover:bg-muted/40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
              >
                Scan without them
              </button>
              {isAdmin && <button
                type="button"
                onClick={pullAndScan}
                className="h-9 rounded-md bg-primary px-3 text-sm font-medium text-primary-foreground hover:opacity-90 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
              >
                {phase === "pull_failed" ? `Retry ${failedImages.length} downloads and scan` : `Download ${imageCount} image${imageCount === 1 ? "" : "s"} and scan`}
              </button>}
            </>
          ) : (
            <span
              className="text-xs text-muted-foreground"
              role="status"
              aria-live="polite"
            >
              {phase === "pulling" ? "Large images can take a few minutes. The scan starts automatically when readiness is confirmed." : "Please wait…"}
            </span>
          )}
        </DialogFooter>
        </>}
      </DialogContent>
    </Dialog>
  );

  return { launch, configure, dialog, busy: phase !== "idle" };
}

// Shared configuration for Home and other scan entry points. launch() still
// accepts an already-configured intent without adding another confirmation.
export function ScanProfileSelect({ value, onChange }: { value: string; onChange: (value: string) => void }) {
  const profiles = useQuery({
    queryKey: ["scan-profiles"],
    queryFn: async () => (await api.get<{ id: string; label: string; summary: string }[]>("/scan-profiles")).data,
    staleTime: 300_000,
  });
  const choices = (profiles.data ?? []).filter((p) => p.id !== "targeted" && p.id !== "pr");
  return <div className="space-y-2">
    <label htmlFor="scan-profile" className="text-sm font-medium">Scan profile</label>
    <Select value={value} onValueChange={onChange} disabled={profiles.isPending || profiles.isError}>
      <SelectTrigger id="scan-profile"><SelectValue placeholder={profiles.isPending ? "Loading profiles…" : "Choose profile"} /></SelectTrigger>
      <SelectContent>{choices.map((p) => <SelectItem key={p.id} value={p.id}>{p.id === "fast" ? "Fast" : p.label}</SelectItem>)}</SelectContent>
    </Select>
    {profiles.isError ? <p role="alert" className="text-xs text-destructive">Profiles unavailable. <button type="button" className="underline" onClick={() => void profiles.refetch()}>Retry profiles</button></p>
      : <ul className="space-y-1 text-xs text-muted-foreground">{choices.map((p) => <li key={p.id}><span className={p.id === value ? "font-medium text-foreground" : "font-medium"}>{p.id === "fast" ? "Fast" : p.label}:</span> {p.summary}</li>)}</ul>}
  </div>;
}

function ScanConfiguration({ params, onStart }: { params: ScanParams; onStart: (params: ScanParams) => void }) {
  const [profile, setProfile] = useState(params.profile || "standard");
  const [branch, setBranch] = useState(params.branch || "");
  const repo = useQuery({ queryKey: ["repo", params.repo_id], queryFn: async () => (await api.get<Repo>(`/repos/${params.repo_id}`)).data });
  const branches = useQuery({
    queryKey: ["repo-branches", params.repo_id],
    queryFn: async () => (await api.get<{ branches: string[]; current_branch: string; default_branch: string }>(`/repos/${params.repo_id}/branches`)).data,
    retry: false,
  });
  // Only the known missing-metadata case is a working tree. Permissions,
  // credentials, and remote failures remain actionable errors.
  const workingTree = repo.data?.source_type === "local" && ((branches.isSuccess && !branches.data?.branches?.length) || (branches.isError && /\.git[^\n]*(no such file|does not exist)/i.test(branches.error.message)));
  const selectedBranch = workingTree ? "" : branch || branches.data?.current_branch || branches.data?.default_branch || repo.data?.default_branch || "";
  const branchChoices = Array.from(new Set([selectedBranch, ...(branches.data?.branches ?? [])])).filter(Boolean);
  return <>
    <DialogHeader><DialogTitle>Scan {repo.data?.name || "repository"}</DialogTitle><DialogDescription>Standard is a good first scan. Wolf checks the selected scanners and prepares any missing images before starting.</DialogDescription></DialogHeader>
    {repo.isError && <p role="alert" className="text-sm text-destructive">Repository unavailable. <button className="underline" onClick={() => void repo.refetch()}>Retry repository</button></p>}
    <div className="space-y-2">
      <label htmlFor="scan-branch" className="text-sm font-medium">Branch</label>
      {workingTree ? <p className="text-sm text-muted-foreground">Working tree · this folder has no Git branches.</p> : <Select value={selectedBranch} onValueChange={setBranch} disabled={branches.isPending}>
        <SelectTrigger id="scan-branch"><SelectValue placeholder={branches.isPending ? "Loading branches…" : "Choose branch"} /></SelectTrigger>
        <SelectContent>{branchChoices.map((b) => <SelectItem key={b} value={b}>{b}</SelectItem>)}</SelectContent>
      </Select>}
      {branches.isError && !workingTree && <p role="alert" className="text-xs text-destructive">Branches could not be loaded. <button className="underline" onClick={() => void branches.refetch()}>Retry branches</button></p>}
    </div>
    <ScanProfileSelect value={profile} onChange={setProfile} />
    <DialogFooter><Button disabled={repo.isPending || repo.isError || branches.isPending || (branches.isError && !workingTree)} onClick={() => onStart({ ...params, profile, branch: selectedBranch || undefined })}>Start scan</Button></DialogFooter>
  </>;
}
