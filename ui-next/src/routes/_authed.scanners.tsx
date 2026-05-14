// Scanner backend admin. Carries the same /api/scanners/{config,doctor,pull}
// endpoints the Next.js version used.
import { createFileRoute } from "@tanstack/react-router";
import { useQuery, useMutation } from "@tanstack/react-query";
import { useState } from "react";
import {
  ContainerIcon,
  CheckCircle2Icon,
  XCircleIcon,
  AlertCircleIcon,
  RefreshCwIcon,
  DownloadIcon,
} from "lucide-react";
import { api } from "@/lib/api";
import { CardSkeleton } from "@/components/skeleton";

export const Route = createFileRoute("/_authed/scanners")({
  component: ScannersPage,
});

type ScannerConfig = {
  image: string;
  image_overrides: Record<string, string> | null;
  pull_policy: string;
  network: string;
  memory: string;
  cpus: string;
  db_volume: string;
  host_repos_root: string;
  in_container_repos_root: string;
  uid: number;
  gid: number;
};

type DoctorCheck = { label: string; ok: boolean; detail?: string };
type DoctorResult = { overall_ok: boolean; checks: DoctorCheck[] };
type PullResult = {
  pulled: string[];
  errors?: { image: string; error: string }[];
};

function ScannersPage() {
  const [doctor, setDoctor] = useState<DoctorResult | null>(null);
  const [pull, setPull] = useState<PullResult | null>(null);

  const cfgQ = useQuery({
    queryKey: ["scanners", "config"],
    queryFn: async () => {
      const r = await api.get<ScannerConfig>("/scanners/config");
      return r.data;
    },
  });

  const doctorMut = useMutation({
    mutationFn: async () => {
      const r = await api.post<DoctorResult>("/scanners/doctor");
      return r.data;
    },
    onSuccess: setDoctor,
  });

  const pullMut = useMutation({
    mutationFn: async () => {
      const r = await api.post<PullResult>("/scanners/pull");
      return r.data;
    },
    onSuccess: setPull,
  });

  if (cfgQ.isLoading) {
    return (
      <div className="p-6 space-y-3 max-w-5xl">
        <CardSkeleton />
        <CardSkeleton />
      </div>
    );
  }
  if (!cfgQ.data) {
    return (
      <div className="p-6 max-w-5xl text-sm text-muted-foreground">
        Could not load scanner config. Is wolf-slim running with the docker
        socket mounted? Try <code>wolf doctor</code> from the CLI.
      </div>
    );
  }
  const cfg = cfgQ.data;
  const overrides = Object.entries(cfg.image_overrides ?? {});
  const distinctImages = Array.from(
    new Set([cfg.image, ...overrides.map(([, v]) => v)]),
  );

  return (
    <div className="p-6 space-y-6 max-w-5xl">
      <div className="flex items-start justify-between gap-4">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight flex items-center gap-2">
            <ContainerIcon className="size-6" />
            Scanner backend
          </h1>
          <p className="text-sm text-muted-foreground mt-1 max-w-2xl">
            Wolf runs every scanner inside short-lived containers. Tools below
            without an override use the default <code>wolf-scanners</code> image.
            Upstream-routed tools (trivy, semgrep, etc.) live in their
            maintainer's official images.
          </p>
        </div>
        <div className="flex gap-2">
          <button
            type="button"
            onClick={() => doctorMut.mutate()}
            disabled={doctorMut.isPending}
            className="inline-flex items-center gap-2 px-3 h-9 rounded-md bg-muted text-foreground text-sm hover:bg-muted/70 disabled:opacity-60"
          >
            <RefreshCwIcon className="size-4" />
            {doctorMut.isPending ? "Running…" : "Doctor"}
          </button>
          <button
            type="button"
            onClick={() => pullMut.mutate()}
            disabled={pullMut.isPending}
            className="inline-flex items-center gap-2 px-3 h-9 rounded-md bg-primary text-primary-foreground text-sm hover:opacity-90 disabled:opacity-60"
          >
            <DownloadIcon className="size-4" />
            {pullMut.isPending ? "Pulling…" : "Pull all images"}
          </button>
        </div>
      </div>

      <section className="glass-card p-5">
        <h2 className="text-sm font-medium mb-3">Configuration</h2>
        <dl className="grid grid-cols-1 sm:grid-cols-2 gap-x-6 gap-y-3 text-sm">
          <KV label="Default image" v={<code>{cfg.image}</code>} />
          <KV label="Pull policy" v={cfg.pull_policy} />
          <KV label="Network" v={cfg.network} />
          <KV label="Memory cap" v={cfg.memory || "(unlimited)"} />
          <KV label="CPU cap" v={cfg.cpus || "(unlimited)"} />
          <KV label="DB volume" v={cfg.db_volume || "(none)"} />
          <KV label="UID:GID" v={`${cfg.uid}:${cfg.gid}`} />
          <KV
            label="Repo translation"
            v={
              cfg.host_repos_root
                ? `${cfg.in_container_repos_root} → ${cfg.host_repos_root}`
                : "(dev mode)"
            }
          />
        </dl>
      </section>

      <section className="glass-card p-5">
        <h2 className="text-sm font-medium mb-3">Per-tool image overrides</h2>
        {overrides.length === 0 ? (
          <p className="text-sm text-muted-foreground">
            No overrides configured — every tool routes to{" "}
            <code>{cfg.image}</code> by default.
          </p>
        ) : (
          <table className="w-full text-sm">
            <thead className="text-xs uppercase tracking-wide text-muted-foreground">
              <tr>
                <th className="text-left pb-2 font-medium">Tool</th>
                <th className="text-left pb-2 font-medium">Image</th>
              </tr>
            </thead>
            <tbody>
              {overrides.map(([tool, image]) => (
                <tr key={tool} className="border-t border-border/30">
                  <td className="py-2 font-mono">{tool}</td>
                  <td className="py-2 font-mono text-xs">{image}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </section>

      <section className="glass-card p-5">
        <h2 className="text-sm font-medium mb-3">
          Images wolf will use ({distinctImages.length})
        </h2>
        <ul className="space-y-1 font-mono text-xs">
          {distinctImages.map((img) => (
            <li key={img}>{img}</li>
          ))}
        </ul>
      </section>

      {doctor && (
        <section className="glass-card p-5">
          <h2 className="text-sm font-medium mb-3">
            Doctor report — {doctor.overall_ok ? "Healthy" : "Issues"}
          </h2>
          <ul className="space-y-2 text-sm">
            {doctor.checks.map((c) => (
              <li key={c.label} className="flex items-start gap-2">
                {c.ok ? (
                  <CheckCircle2Icon className="size-4 text-emerald-400 mt-0.5 shrink-0" />
                ) : (
                  <XCircleIcon className="size-4 text-red-400 mt-0.5 shrink-0" />
                )}
                <div className="flex-1">
                  <div className="font-medium">{c.label}</div>
                  {c.detail && (
                    <div className="text-muted-foreground text-xs mt-0.5">
                      {c.detail}
                    </div>
                  )}
                </div>
              </li>
            ))}
          </ul>
        </section>
      )}

      {pull && (
        <section className="glass-card p-5">
          <h2 className="text-sm font-medium mb-3">Pull results</h2>
          {pull.pulled.length > 0 && (
            <>
              <p className="text-xs font-medium mb-1">Ready locally:</p>
              <ul className="text-xs font-mono space-y-1 mb-3">
                {pull.pulled.map((i) => (
                  <li key={i} className="flex items-center gap-2">
                    <CheckCircle2Icon className="size-3 text-emerald-400" />
                    {i}
                  </li>
                ))}
              </ul>
            </>
          )}
          {pull.errors && pull.errors.length > 0 && (
            <>
              <p className="text-xs font-medium mb-1 text-red-400">Errors:</p>
              <ul className="text-xs space-y-1">
                {pull.errors.map((e) => (
                  <li key={e.image} className="flex items-start gap-2">
                    <AlertCircleIcon className="size-3 text-red-400 mt-0.5" />
                    <span className="font-mono">{e.image}</span>:{" "}
                    <span className="text-muted-foreground">{e.error}</span>
                  </li>
                ))}
              </ul>
            </>
          )}
        </section>
      )}
    </div>
  );
}

function KV({ label, v }: { label: string; v: React.ReactNode }) {
  return (
    <div className="flex flex-col">
      <dt className="text-2xs text-muted-foreground uppercase tracking-wide">
        {label}
      </dt>
      <dd className="mt-0.5">{v}</dd>
    </div>
  );
}
