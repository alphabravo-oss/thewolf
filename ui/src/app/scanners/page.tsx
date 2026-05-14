"use client";

/**
 * Scanners admin page.
 *
 * Surfaces the container-backend status that wolf-slim is configured with,
 * and exposes two operator actions:
 *
 *   - Doctor:    GET  /api/scanners/doctor  → structured health report
 *   - Pull all:  POST /api/scanners/pull    → ensure every image is present
 *
 * The backend handlers are thin wrappers around scanners.Doctor and
 * scanners.Pull in internal/setup/scanners/.
 */

import { useState } from "react";
import { useQuery, useMutation } from "@tanstack/react-query";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { LoadingSpinner } from "@/components/loading-spinner";
import api from "@/lib/api";
import {
  CheckCircle2Icon,
  XCircleIcon,
  AlertCircleIcon,
  RefreshCwIcon,
  DownloadIcon,
  ContainerIcon,
} from "lucide-react";

type ScannerConfig = {
  image: string;
  image_overrides: Record<string, string>;
  pull_policy: "IfNotPresent" | "Always" | "Never";
  network: string;
  memory: string;
  cpus: string;
  db_volume: string;
  host_repos_root: string;
  in_container_repos_root: string;
  uid: number;
  gid: number;
};

type DoctorCheck = {
  label: string;
  ok: boolean;
  detail?: string;
};

type DoctorResult = {
  overall_ok: boolean;
  checks: DoctorCheck[];
};

type PullResult = {
  pulled: string[];
  errors?: { image: string; error: string }[];
};

export default function ScannersPage() {
  const [doctorOutput, setDoctorOutput] = useState<DoctorResult | null>(null);
  const [pullOutput, setPullOutput] = useState<PullResult | null>(null);

  const cfgQuery = useQuery<ScannerConfig>({
    queryKey: ["scanners", "config"],
    queryFn: async () => {
      const r = await api.get<ScannerConfig>("/scanners/config");
      return r.data;
    },
  });

  const doctorMut = useMutation({
    mutationFn: async () => {
      const r = await api.post<DoctorResult>("/scanners/doctor", {});
      return r.data;
    },
    onSuccess: (data) => setDoctorOutput(data),
  });

  const pullMut = useMutation({
    mutationFn: async () => {
      const r = await api.post<PullResult>("/scanners/pull", {});
      return r.data;
    },
    onSuccess: (data) => setPullOutput(data),
  });

  if (cfgQuery.isLoading) {
    return (
      <div className="p-6">
        <LoadingSpinner />
      </div>
    );
  }

  const cfg = cfgQuery.data;
  if (!cfg) {
    return (
      <div className="p-6 text-sm text-muted-foreground">
        Could not load scanner config. Is wolf-slim running with the docker
        socket mounted? Try <code>wolf doctor</code>.
      </div>
    );
  }

  const overrideEntries = Object.entries(cfg.image_overrides || {});
  const distinctImages = Array.from(
    new Set([cfg.image, ...overrideEntries.map(([, v]) => v)]),
  );

  return (
    <div className="p-6 space-y-6 max-w-5xl">
      <div className="flex items-start justify-between gap-4">
        <div>
          <h1 className="text-2xl font-semibold flex items-center gap-2">
            <ContainerIcon className="size-6" />
            Scanner backend
          </h1>
          <p className="text-sm text-muted-foreground mt-1 max-w-xl">
            Wolf runs every scanner inside short-lived containers from the{" "}
            <code>wolf-scanners</code> image family. Tools listed in the
            override map below come from separate images that get pulled
            lazily.
          </p>
        </div>
        <div className="flex gap-2">
          <Button
            onClick={() => doctorMut.mutate()}
            disabled={doctorMut.isPending}
            variant="secondary"
          >
            <RefreshCwIcon className="size-4 mr-2" />
            {doctorMut.isPending ? "Running…" : "Doctor"}
          </Button>
          <Button
            onClick={() => pullMut.mutate()}
            disabled={pullMut.isPending}
          >
            <DownloadIcon className="size-4 mr-2" />
            {pullMut.isPending ? "Pulling…" : "Pull all images"}
          </Button>
        </div>
      </div>

      {/* Live config */}
      <Card>
        <CardHeader>
          <CardTitle>Configuration</CardTitle>
          <CardDescription>
            From <code>scan.container</code> in wolf.yaml and{" "}
            <code>WOLF_SCANNERS_*</code> env. Read-only here; edit the config
            file and restart wolf-slim to change.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <dl className="grid grid-cols-1 sm:grid-cols-2 gap-x-6 gap-y-3 text-sm">
            <KV label="Default image" value={<code>{cfg.image}</code>} />
            <KV label="Pull policy" value={<Badge>{cfg.pull_policy}</Badge>} />
            <KV label="Network" value={cfg.network} />
            <KV label="Memory cap" value={cfg.memory || "(unlimited)"} />
            <KV label="CPU cap" value={cfg.cpus || "(unlimited)"} />
            <KV label="DB volume" value={cfg.db_volume || "(none)"} />
            <KV
              label="UID:GID"
              value={`${cfg.uid}:${cfg.gid}`}
            />
            <KV
              label="Repo translation"
              value={
                cfg.host_repos_root
                  ? `${cfg.in_container_repos_root} → ${cfg.host_repos_root}`
                  : "(none — dev mode)"
              }
            />
          </dl>
        </CardContent>
      </Card>

      {/* Per-tool overrides */}
      <Card>
        <CardHeader>
          <CardTitle>Per-tool image overrides</CardTitle>
          <CardDescription>
            Tools not listed below run in the default image.
          </CardDescription>
        </CardHeader>
        <CardContent>
          {overrideEntries.length === 0 ? (
            <p className="text-sm text-muted-foreground">
              No overrides configured — all tools run in{" "}
              <code>{cfg.image}</code>.
            </p>
          ) : (
            <table className="w-full text-sm">
              <thead>
                <tr className="text-left text-muted-foreground border-b">
                  <th className="pb-2 font-medium">Tool</th>
                  <th className="pb-2 font-medium">Image</th>
                </tr>
              </thead>
              <tbody>
                {overrideEntries.map(([tool, image]) => (
                  <tr key={tool} className="border-b last:border-0">
                    <td className="py-2 font-mono">{tool}</td>
                    <td className="py-2 font-mono text-xs">{image}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </CardContent>
      </Card>

      {/* Image inventory */}
      <Card>
        <CardHeader>
          <CardTitle>Images wolf will use</CardTitle>
          <CardDescription>
            {distinctImages.length} distinct image
            {distinctImages.length === 1 ? "" : "s"}. Click "Pull all images"
            above to pre-fetch.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <ul className="space-y-1">
            {distinctImages.map((img) => (
              <li key={img} className="text-xs font-mono">
                {img}
              </li>
            ))}
          </ul>
        </CardContent>
      </Card>

      {/* Doctor output */}
      {doctorOutput && (
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              Doctor report
              {doctorOutput.overall_ok ? (
                <Badge variant="default" className="bg-green-600">
                  Healthy
                </Badge>
              ) : (
                <Badge variant="destructive">Issues</Badge>
              )}
            </CardTitle>
          </CardHeader>
          <CardContent>
            <ul className="space-y-2 text-sm">
              {doctorOutput.checks.map((c) => (
                <li
                  key={c.label}
                  className="flex items-start gap-2"
                >
                  {c.ok ? (
                    <CheckCircle2Icon className="size-4 text-green-600 mt-0.5 shrink-0" />
                  ) : (
                    <XCircleIcon className="size-4 text-red-600 mt-0.5 shrink-0" />
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
          </CardContent>
        </Card>
      )}

      {/* Pull output */}
      {pullOutput && (
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              Pull results
              {pullOutput.errors && pullOutput.errors.length > 0 ? (
                <Badge variant="destructive">
                  {pullOutput.errors.length} failed
                </Badge>
              ) : (
                <Badge className="bg-green-600">All present</Badge>
              )}
            </CardTitle>
          </CardHeader>
          <CardContent>
            {pullOutput.pulled.length > 0 && (
              <>
                <p className="text-sm font-medium mb-2">Ready locally:</p>
                <ul className="text-xs font-mono space-y-1 mb-4">
                  {pullOutput.pulled.map((img) => (
                    <li key={img} className="flex items-center gap-2">
                      <CheckCircle2Icon className="size-3 text-green-600" />
                      {img}
                    </li>
                  ))}
                </ul>
              </>
            )}
            {pullOutput.errors && pullOutput.errors.length > 0 && (
              <>
                <p className="text-sm font-medium mb-2 text-red-600">
                  Errors:
                </p>
                <ul className="text-xs space-y-1">
                  {pullOutput.errors.map((e) => (
                    <li key={e.image} className="flex items-start gap-2">
                      <AlertCircleIcon className="size-3 text-red-600 mt-0.5" />
                      <div>
                        <span className="font-mono">{e.image}</span>:{" "}
                        <span className="text-muted-foreground">
                          {e.error}
                        </span>
                      </div>
                    </li>
                  ))}
                </ul>
              </>
            )}
          </CardContent>
        </Card>
      )}
    </div>
  );
}

function KV({ label, value }: { label: string; value: React.ReactNode }) {
  return (
    <div className="flex flex-col">
      <dt className="text-xs text-muted-foreground uppercase tracking-wide">
        {label}
      </dt>
      <dd className="mt-0.5">{value}</dd>
    </div>
  );
}
