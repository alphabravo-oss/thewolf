import { createFileRoute, Link, useNavigate } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Loader2Icon, PlayIcon, ShieldCheckIcon } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { DashboardEvidence } from "@/components/fleet/dashboard-evidence";
import { AddRepoForm } from "@/components/add-repo-form";
import { useScanWithPreflight } from "@/components/scan-preflight";
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription } from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { PermissionState } from "@/components/ui/empty-state";
import { Skeleton } from "@/components/ui/skeleton";
import { PageHeader, PageShell } from "@/components/ui/page";
import { ApiError, api } from "@/lib/api";
import { useFleetPosture } from "@/lib/fleet";
import { useIsAdmin } from "@/lib/me";
import { useRuntimeCapabilities } from "@/lib/runtime-capabilities";
import { safeErrorMessage } from "@/lib/safe-display";
import type { Repo } from "@/lib/types";
import { Section } from "@/components/ui/section";

export const Route = createFileRoute("/_authed/")({ component: FleetDashboard });

function FleetDashboard() {
  const fleet = useFleetPosture();
  const scan = useScanWithPreflight();
  const navigate = useNavigate();
  const qc = useQueryClient();
  const [adding, setAdding] = useState(false);
  const sample = useMutation({
    mutationFn: async () => (await api.post<Repo>("/setup/sample-repo")).data,
    onSuccess: (repo) => {
      if (!repo?.id) {
        toast.error("The sample could not be opened. Try again.");
        return;
      }
      scan.configure({ repo_id: repo.id, profile: "standard" });
      void qc.invalidateQueries({ queryKey: ["repos"] });
      void qc.invalidateQueries({ queryKey: ["fleet"] });
      void qc.invalidateQueries({ queryKey: ["setup"] });
    },
    onError: (e) => toast.error(safeErrorMessage(e, "Could not add the sample repository.")),
  });

  return <PageShell>
    {scan.dialog}
    <PageHeader title="Home" description="Security risk, scan coverage, and what needs your attention." />
    {fleet.isLoading ? <div role="status" aria-label="Loading Home"><Skeleton className="h-64" /></div>
      : fleet.error instanceof ApiError && fleet.error.status === 403 ? <PermissionState description="You do not have access to fleet evidence." />
      : fleet.isError || !fleet.data ? <div role="alert" className="rounded-lg border border-border p-6 space-y-3"><p>Dashboard evidence could not be loaded. Coverage and risk are unknown.</p><Button variant="outline" onClick={() => void fleet.refetch()}>Retry dashboard</Button></div>
      : fleet.data.repo_count === 0 ? <section className="rounded-xl border border-border bg-card px-6 py-8 sm:p-10 space-y-6">
        <div className="max-w-xl space-y-3">
          <ShieldCheckIcon className="size-8 text-primary" aria-hidden />
          <h2 className="text-2xl font-semibold tracking-tight">Your first security scan starts here</h2>
          <p className="text-sm leading-relaxed text-muted-foreground">Try a small sample with security issues to explore. Wolf checks scanner readiness, walks you through any downloads, and shows what to fix.</p>
        </div>
        <div className="flex flex-wrap gap-3">
          <Button onClick={() => sample.mutate()} disabled={sample.isPending || scan.busy}>{sample.isPending ? <Loader2Icon className="animate-spin" /> : <PlayIcon />}{sample.isPending ? "Preparing sample…" : "Scan the sample repository"}</Button>
          <Button variant="outline" onClick={() => setAdding(true)}>Add your repository</Button>
        </div>
        <p className="text-xs text-muted-foreground">No source credentials needed for the sample. Bring your own local path, GitHub repository, Git URL, or SSH source.</p>
        <ScannerReadiness />
      </section> : <>
        <div className="flex flex-wrap items-center justify-between gap-3"><ScannerReadiness /><Button variant="outline" onClick={() => setAdding(true)}>Add your repository</Button></div>
        <DashboardEvidence />
      </>}
    <Dialog open={adding} onOpenChange={setAdding}><DialogContent className="max-h-[90dvh] overflow-y-auto [&>button]:hidden"><DialogHeader className="sr-only"><DialogTitle>Add your repository</DialogTitle><DialogDescription>Choose a local path, GitHub repository, remote Git URL, or SSH node.</DialogDescription></DialogHeader>{adding && <AddRepoForm onDone={(repoId) => {
      setAdding(false);
      void qc.invalidateQueries({ queryKey: ["fleet"] });
      if (repoId) void navigate({ to: "/repos/$repoId", params: { repoId } });
    }} />}</DialogContent></Dialog>
  </PageShell>;
}

type DoctorResult = { overall_ok: boolean; checks: { label: string; ok: boolean; detail?: string }[] };

function ScannerReadiness() {
  const admin = useIsAdmin();
  const runtime = useRuntimeCapabilities();
  const doctor = useQuery({
    queryKey: ["scanners", "doctor", "home"],
    queryFn: async () => (await api.post<DoctorResult>("/scanners/doctor")).data,
    enabled: admin && runtime.data?.docker_image_management === true,
    staleTime: 60_000,
    retry: false,
  });
  if (!admin) return <p className="text-xs text-muted-foreground">Scanner readiness is checked before every scan. Ask an administrator if preparation is needed. <Link to="/account" search={{ section: "secrets" }} className="underline underline-offset-4">GitHub credentials</Link></p>;
  const failed = doctor.data?.checks?.filter((c) => !c.ok) ?? [];
  const dockerUnavailable = failed.some((c) => /^(docker reachable|Container config)/.test(`${c.label} ${c.detail || ""}`));
  const needsImages = failed.some((c) => /^scanners image present/.test(`${c.label} ${c.detail || ""}`));
  const status = runtime.isError || doctor.isError ? "Readiness unavailable"
    : runtime.isPending ? "Checking scanner readiness…"
    : !runtime.data?.docker_image_management ? runtime.data?.worker_readiness?.status === "unavailable" ? "No scan worker available" : "Scans run on workers"
    : doctor.isPending ? "Checking scanner readiness…"
    : dockerUnavailable ? "Docker unavailable"
    : needsImages ? "Scanner images needed"
    : doctor.data?.overall_ok ? "Ready to scan"
    : "Scanner setup needs attention";
  return <div className="space-y-2 text-xs text-muted-foreground">
    <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
      <span role="status" className="font-medium text-foreground">{status}</span>
      <Link to="/settings" search={{ tab: "scanners" }} className="underline underline-offset-4">{dockerUnavailable ? "Check Docker connection" : needsImages ? "Prepare scanner images" : "Scanner preparation"}</Link>
      <Link to="/account" search={{ section: "secrets" }} className="underline underline-offset-4">GitHub credentials</Link>
      {runtime.data?.docker_image_management && <button type="button" disabled={doctor.isFetching} onClick={() => void doctor.refetch()} className="underline underline-offset-4 disabled:opacity-50">{doctor.isFetching ? "Running doctor…" : "Run doctor"}</button>}
    </div>
    {failed.length > 0 && <Section variant="plain" triggerClassName="text-foreground" title="Diagnostic details"><ul className="mt-2 space-y-1">{failed.map((c) => <li key={c.label}>{c.label} {c.detail}</li>)}</ul></Section>}
  </div>;
}
