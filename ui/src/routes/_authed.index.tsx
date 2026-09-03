// ui/src/routes/_authed.index.tsx
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router";
import { useMutation, useQuery } from "@tanstack/react-query";
import { DownloadIcon, Loader2Icon, PlusIcon, ShieldIcon } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { PostureCards } from "@/components/fleet/posture-cards";
import { SeverityTrend } from "@/components/fleet/severity-trend";
import { TopComponents } from "@/components/fleet/top-components";
import { NeedsAttention } from "@/components/fleet/needs-attention";
import { InventoryBreakdown } from "@/components/fleet/inventory-breakdown";
import { RecentActivity } from "@/components/fleet/recent-activity";
import { PageHeader, PageShell } from "@/components/ui/page";
import { api, isNotFound } from "@/lib/api";
import { safeErrorMessage } from "@/lib/safe-display";
import type { Repo, SetupStatus } from "@/lib/types";

export const Route = createFileRoute("/_authed/")({ component: FleetDashboard });

function FleetDashboard() {
  const setupQ = useQuery({
    queryKey: ["setup", "status"],
    queryFn: async () => {
      try {
        return (await api.get<SetupStatus>("/setup/status")).data ?? null;
      } catch (e) {
        if (isNotFound(e)) return null;
        throw e;
      }
    },
  });
  const reposQ = useQuery({
    queryKey: ["repos", { limit: 1 }],
    queryFn: async () => {
      const r = await api.get<Repo[]>("/repos?limit=1");
      return r.data ?? [];
    },
  });
  const repoCount = setupQ.data?.repo_count ?? reposQ.data?.length;
  const showOnboarding = repoCount === 0;

  return (
    <PageShell>
      <div className="reveal reveal-1">
        <PageHeader
          title="Home"
          description="Open findings, posture, and inventory across every repository wolf manages."
        />
      </div>
      {showOnboarding ? (
        <OnboardingCard />
      ) : (
        <>
          <div className="reveal reveal-2">
            <PostureCards />
          </div>
          <div className="reveal reveal-3">
            <SeverityTrend />
          </div>
          <div className="grid grid-cols-1 lg:grid-cols-2 gap-4 reveal reveal-4">
            <TopComponents />
            <NeedsAttention />
          </div>
          <div className="grid grid-cols-1 lg:grid-cols-2 gap-4 reveal reveal-5">
            <InventoryBreakdown />
            <RecentActivity />
          </div>
        </>
      )}
    </PageShell>
  );
}

interface DoctorCheck {
  label: string;
  ok: boolean;
  detail?: string;
}

interface DoctorResult {
  overall_ok: boolean;
  checks: DoctorCheck[];
}

interface PullResult {
  pulled: string[];
  errors?: { image: string; error: string }[];
}

function OnboardingCard() {
  const navigate = useNavigate();
  const [doctor, setDoctor] = useState<DoctorResult | null>(null);

  const doctorMut = useMutation({
    mutationFn: async () =>
      (await api.post<DoctorResult>("/scanners/doctor")).data,
    onSuccess: (d) => setDoctor(d),
    onError: (e) =>
      toast.error(safeErrorMessage(e, "Scanner diagnostics could not run.")),
  });

  const pullMut = useMutation({
    mutationFn: async () => (await api.post<PullResult>("/scanners/pull")).data,
    onSuccess: (p) => {
      if (p && (!p.errors || p.errors.length === 0)) {
        toast.success(`Pulled ${p.pulled?.length ?? 0} image(s)`);
      } else {
        toast.error(
          `Pulled ${p?.pulled?.length ?? 0}, ${p?.errors?.length ?? 0} failed`,
        );
      }
    },
    onError: (e) =>
      toast.error(safeErrorMessage(e, "Scanner images could not be pulled.")),
  });

  const sampleMut = useMutation({
    mutationFn: async () => (await api.post<Repo>("/setup/sample-repo")).data,
    onSuccess: (repo) => {
      if (!repo?.id) {
        toast.error("Sample repository was created but no id was returned");
        return;
      }
      toast.success(`Added ${repo.name || "sample repository"}`);
      void navigate({ to: "/repos/$repoId", params: { repoId: repo.id } });
    },
    onError: (e) =>
      toast.error(safeErrorMessage(e, "Could not add the sample repository.")),
  });

  return (
    <section className="glass-card p-5 space-y-4 reveal reveal-2">
      <div>
        <h2 className="text-sm font-medium">Get started</h2>
        <p className="mt-1 text-sm text-muted-foreground">
          Run doctor, pull scanner images, then add a sample or real repository.
        </p>
      </div>
      <div className="flex flex-wrap gap-2">
        <button
          type="button"
          onClick={() => doctorMut.mutate()}
          disabled={doctorMut.isPending}
          className="inline-flex items-center gap-1.5 h-9 px-3 rounded-md border border-border text-sm hover:bg-muted/30 disabled:opacity-50"
        >
          {doctorMut.isPending ? (
            <Loader2Icon className="size-4 animate-spin" />
          ) : (
            <ShieldIcon className="size-4" />
          )}
          {doctorMut.isPending ? "Running…" : "Run doctor"}
        </button>
        <button
          type="button"
          onClick={() => pullMut.mutate()}
          disabled={pullMut.isPending}
          className="inline-flex items-center gap-1.5 h-9 px-3 rounded-md border border-border text-sm hover:bg-muted/30 disabled:opacity-50"
        >
          {pullMut.isPending ? (
            <Loader2Icon className="size-4 animate-spin" />
          ) : (
            <DownloadIcon className="size-4" />
          )}
          {pullMut.isPending ? "Pulling…" : "Pull scanner images"}
        </button>
        <button
          type="button"
          onClick={() => sampleMut.mutate()}
          disabled={sampleMut.isPending}
          className="inline-flex items-center gap-1.5 h-9 px-3 rounded-md bg-primary text-primary-foreground text-sm font-medium disabled:opacity-50"
        >
          {sampleMut.isPending ? (
            <Loader2Icon className="size-4 animate-spin" />
          ) : (
            <PlusIcon className="size-4" />
          )}
          {sampleMut.isPending ? "Adding…" : "Add sample repository"}
        </button>
        <Link
          to="/repos"
          className="inline-flex items-center gap-1.5 h-9 px-3 rounded-md border border-border text-sm hover:bg-muted/30"
        >
          Add a real repo
        </Link>
      </div>
      {doctor && (
        <div
          className="rounded-md border border-border bg-muted/20 p-3 text-sm space-y-1"
          role={doctor.overall_ok ? "status" : "alert"}
        >
          <div className="font-medium">
            {doctor.overall_ok ? "All checks passed" : "Some checks failed"}
          </div>
          <ul className="text-xs space-y-0.5 font-mono">
            {(doctor.overall_ok
              ? (doctor.checks ?? [])
              : (doctor.checks ?? []).filter((c) => !c.ok)
            ).map((c, i) => (
              <li key={i}>
                {c.ok ? "ok" : "fail"} · {c.label}
                {c.detail ? ` — ${c.detail}` : ""}
              </li>
            ))}
          </ul>
        </div>
      )}
    </section>
  );
}
