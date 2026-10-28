// Scanners & updates: runtime diagnostics, the release channel, and the update
// policy. Read-only runtime config, per-tool overrides and webhooks sit in a
// collapsed Advanced section. Image pulls live in the Images section.
import { useState, type ReactNode } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { toast } from "sonner";
import { CheckIcon, Loader2Icon, ShieldIcon } from "lucide-react";
import { api } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { ErrorState } from "@/components/ui/empty-state";
import { Section } from "@/components/ui/section";
import { StatusBadge } from "@/components/ui/status-badge";
import { useRuntimeCapabilities } from "@/lib/runtime-capabilities";
import { safeErrorMessage } from "@/lib/safe-display";
import { useSettingsMap } from "@/lib/flags";
import { describeScannerChannel } from "./scanner-utils";
import { useSaveSettings } from "./knobs";
import { BoolToggle, SettingRow, SettingsCard, TextSetting } from "./shared";

interface ScannersConfig {
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
}

interface DoctorResult {
  overall_ok: boolean;
  checks: { label: string; ok: boolean; detail?: string }[];
}

/** Renders children only on a deployment that manages scanner images through Docker. */
export function DockerRuntimeGate({ children }: { children: ReactNode }) {
  const runtime = useRuntimeCapabilities();
  if (runtime.isLoading) return <p role="status" className="text-sm text-muted-foreground">Loading scanner runtime…</p>;
  if (runtime.isError || typeof runtime.data?.docker_image_management !== "boolean") {
    return <ErrorState title="Could not check the scanner runtime" actionLabel="Retry runtime check" onRetry={() => void runtime.refetch()} />;
  }
  return runtime.data.docker_image_management ? <>{children}</> : <K8sManaged />;
}

function K8sManaged() {
  return (
    <SettingsCard
      title="Scanner runtime is managed by Kubernetes"
      description="Image pull, build, and local Docker diagnostics are unavailable in this deployment. Scanner images and policies are managed by the cluster operator."
    />
  );
}

function DoctorCard() {
  const [result, setResult] = useState<DoctorResult | null>(null);
  const run = useMutation({
    mutationFn: async () => (await api.post<DoctorResult>("/scanners/doctor")).data,
    onSuccess: (d) => {
      setResult(d);
      if (d?.overall_ok) toast.success("All scanner checks passed");
      else toast.error("Scanner checks failed — see report below");
    },
    onError: (e) => toast.error(safeErrorMessage(e, "Scanner diagnostics could not run.")),
  });
  return (
    <SettingsCard
      title="Diagnostics"
      description="Checks that Docker is reachable, the scanner image is present and caches are writable."
      actions={
        <Button type="button" size="sm" variant="outline" disabled={run.isPending} onClick={() => run.mutate()}>
          {run.isPending ? <Loader2Icon className="animate-spin" /> : <ShieldIcon />}
          {run.isPending ? "Running…" : "Run Doctor"}
        </Button>
      }
    >
      {result ? (
        <div className="space-y-1 rounded-md border border-border bg-muted/20 p-3 text-sm" role={result.overall_ok ? "status" : "alert"} aria-live="polite">
          <div className="mb-1 flex items-center gap-2">
            {result.overall_ok ? <CheckIcon className="size-4 text-status-success" /> : <span className="text-destructive-text">●</span>}
            <span className="font-medium">{result.overall_ok ? "All checks passed" : "Some checks failed"}</span>
          </div>
          <ul className="space-y-0.5 font-mono text-xs">
            {result.checks.map((c, i) => (
              <li key={i} className="flex items-start gap-2">
                <span className={c.ok ? "text-status-success" : "text-destructive-text"}>{c.ok ? "✓" : "✗"}</span>
                <span className="flex-1">
                  {c.label}
                  {c.detail && <span className="text-muted-foreground"> — {c.ok ? "Check completed." : "Check failed. Review server logs."}</span>}
                </span>
              </li>
            ))}
          </ul>
        </div>
      ) : (
        <p className="text-sm text-muted-foreground">Not run in this session.</p>
      )}
    </SettingsCard>
  );
}

function UpdatesCard() {
  const q = useSettingsMap();
  const save = useSaveSettings();
  if (q.isLoading) return <p role="status" className="text-sm text-muted-foreground">Loading…</p>;
  if (q.isError) return <ErrorState title="Could not load settings" onRetry={() => q.refetch()} />;
  const notifyOnly = q.data?.scanner_notify_only !== "false";
  return (
    <SettingsCard
      title="Scanner updates"
      description={<>Factory discovery, candidates, and releases stay CLI (<code>wolf scanner-release</code>); this toggle is the Community default.</>}
    >
      <SettingRow label="Notify only" help="Notify only (do not auto-promote scanner releases)">
        <BoolToggle label="Notify only" value={notifyOnly} disabled={save.isPending}
          onChange={(value) => save.mutateAsync({ scanner_notify_only: value ? "true" : "false" })} />
      </SettingRow>
    </SettingsCard>
  );
}

function WebhookFields() {
  const q = useSettingsMap();
  const save = useSaveSettings();
  const test = useMutation({
    mutationFn: () => api.post("/webhooks/outbound/test"),
    onSuccess: () => toast.success("Test delivery sent"),
    onError: (e) => toast.error(safeErrorMessage(e, "Test delivery failed")),
  });
  const settings = q.data ?? {};
  return (
    <div className="space-y-4">
      <h3 className="text-sm font-medium">Webhooks</h3>
      {[
        ["github_webhook_secret", "GitHub webhook secret", "password"],
        ["outbound_webhook_url", "Outbound webhook URL", "url"],
        ["outbound_webhook_secret", "Outbound webhook secret", "password"],
      ].map(([key, label, type]) => (
        <TextSetting key={key} label={label} type={type} value={settings[key] ?? ""} disabled={save.isPending}
          onCommit={(value) => save.mutate({ [key]: value })} />
      ))}
      <Button type="button" size="sm" variant="outline" disabled={test.isPending} onClick={() => test.mutate()}>
        {test.isPending ? "Sending…" : "Test delivery"}
      </Button>
    </div>
  );
}

function RuntimeCard({ cfg }: { cfg: ScannersConfig }) {
  const channel = describeScannerChannel(cfg.image);
  return (
    <SettingsCard title="Scanner runtime" description="The image scans run in and the release channel it follows.">
      <dl className="space-y-3 text-sm">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <dt className="text-muted-foreground">Default image</dt>
          <dd className="min-w-0 break-all font-mono text-xs">{cfg.image}</dd>
        </div>
        <div className="flex flex-wrap items-center justify-between gap-2">
          <dt className="text-muted-foreground">Release channel</dt>
          <dd className="inline-flex flex-wrap items-center gap-2">
            <StatusBadge status={channel.tone} label={channel.label} size="sm" showDot={false} />
            <span className="text-xs text-muted-foreground">{channel.detail}</span>
          </dd>
        </div>
      </dl>
    </SettingsCard>
  );
}

function RuntimeLimits({ cfg }: { cfg: ScannersConfig }) {
  const rows: [string, ReactNode][] = [
    ["Pull policy", cfg.pull_policy],
    ["Network", cfg.network],
    ["Memory", cfg.memory],
    ["CPUs", cfg.cpus],
    ["UID:GID", `${cfg.uid}:${cfg.gid}`],
    ["Vuln-DB volume", <code key="v" className="text-xs">{cfg.db_volume || "—"}</code>],
    ["Host repos root", <code key="h" className="text-xs">{cfg.host_repos_root || "—"}</code>],
  ];
  const overrides = Object.entries(cfg.image_overrides ?? {});
  return (
    <>
      <div>
        <p className="mb-3 text-xs text-muted-foreground">
          Config is read-only — edit via <code>wolf.yaml</code> / <code>WOLF_SCANNERS_*</code> env, then restart wolf.
        </p>
        <dl className="grid gap-x-6 gap-y-2 text-sm md:grid-cols-2">
          {rows.map(([k, v]) => (
            <div key={k} className="flex items-center justify-between gap-3">
              <dt className="text-muted-foreground">{k}</dt>
              <dd className="min-w-0 break-all text-right">{v}</dd>
            </div>
          ))}
        </dl>
      </div>
      {overrides.length > 0 && (
        <div>
          <h3 className="mb-2 text-sm font-medium">Per-tool image overrides</h3>
          <ul className="space-y-1">
            {overrides.map(([tool, image]) => (
              <li key={tool} className="flex items-center gap-3 font-mono text-xs">
                <span className="w-24 text-muted-foreground">{tool}</span>
                <code className="break-all">{image}</code>
              </li>
            ))}
          </ul>
        </div>
      )}
    </>
  );
}

export function ScannersSection({ advanced }: { advanced?: boolean }) {
  const runtime = useRuntimeCapabilities();
  const docker = runtime.data?.docker_image_management === true;
  const cfgQ = useQuery({
    queryKey: ["scanners-config"],
    queryFn: async () => (await api.get<ScannersConfig>("/scanners/config")).data,
    enabled: docker,
  });
  const cfg = cfgQ.data;
  return (
    <div className="space-y-4">
      {runtime.isLoading || (docker && cfgQ.isLoading) ? (
        <p role="status" className="text-sm text-muted-foreground">Loading scanner runtime…</p>
      ) : runtime.isError || typeof runtime.data?.docker_image_management !== "boolean" ? (
        <ErrorState title="Could not check the scanner runtime" actionLabel="Retry runtime check" onRetry={() => void runtime.refetch()} />
      ) : !docker ? (
        <K8sManaged />
      ) : cfgQ.isError || !cfg ? (
        <ErrorState title="Failed to load scanner config" description="Is the container backend initialized?" onRetry={() => void cfgQ.refetch()} />
      ) : (
        <>
          <RuntimeCard cfg={cfg} />
          <DoctorCard />
        </>
      )}
      <UpdatesCard />
      <Section title="Advanced" summary="Runtime limits, per-tool images, webhooks" defaultOpen={advanced} id="scanner-advanced">
        <div className="space-y-6 pt-1">
          {cfg && docker ? <RuntimeLimits cfg={cfg} /> : null}
          <WebhookFields />
        </div>
      </Section>
    </div>
  );
}
