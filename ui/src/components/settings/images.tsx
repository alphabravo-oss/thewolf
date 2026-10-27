// Scanner images: per-image digests, update detection and pulls. The Wolf-built
// images come off our release factory and follow the configured channel; the
// upstream ones are third-party tools pinned in scanner-lock.yaml. They are two
// supply chains, so two cards, so "is our release current?" is its own answer.
import { useEffect, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { DownloadIcon, Loader2Icon, RefreshCwIcon } from "lucide-react";
import { api } from "@/lib/api";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { Button } from "@/components/ui/button";
import { StatusBadge } from "@/components/ui/status-badge";
import { safeErrorMessage } from "@/lib/safe-display";
import { DockerRuntimeGate } from "./scanners";
import { isWolfBuiltImage } from "./scanner-utils";
import { RowActions, SettingsCard, SettingsTable } from "./shared";

interface ImageStatus {
  image: string;
  local_present?: boolean;
  local_digest?: string;
  remote_digest?: string;
  updates_available: boolean;
  local_error?: string;
  remote_error?: string;
}

const needsPull = (img: ImageStatus) =>
  img.updates_available || (!(img.local_present ?? Boolean(img.local_digest)) && !img.local_error);

function imageState(img: ImageStatus) {
  if (img.local_error || img.remote_error) return { status: "error", label: "Probe failed" };
  if (img.updates_available) return { status: "stale", label: "Update available" };
  if (needsPull(img)) return { status: "pending", label: "Not pulled" };
  return { status: "active", label: "Current" };
}

// Truncated sha256 (8 chars after the prefix) is enough to recognise and fits the cell.
function Digest({ value, err, label }: { value?: string; err?: string; label: "local" | "remote" }) {
  if (err) return <span className="text-xs text-destructive-text" title={`${label} registry probe failed. Review server logs.`}>error</span>;
  if (!value) return <span className="text-xs text-muted-foreground">{label === "local" ? "not pulled" : "—"}</span>;
  const short = value.startsWith("sha256:") ? `sha256:${value.slice(7, 15)}` : value.slice(0, 16);
  return <span className="font-mono text-xs text-muted-foreground" title={value}>{short}…</span>;
}

function ImagesTable({ title, description, items, probe, onPull, bulk, onRefresh, refreshing, emptyTitle }: {
  title: string;
  description: string;
  items: ImageStatus[];
  probe: { isLoading: boolean; isError: boolean; refetch: () => unknown };
  onPull: (image: string) => void;
  /** Only the Wolf card gets a bulk action, scoped to its outdated images. */
  bulk?: { run: () => void; pending: boolean };
  onRefresh: () => void;
  refreshing: boolean;
  emptyTitle: string;
}) {
  const stale = items.filter((i) => i.updates_available);
  const [confirm, setConfirm] = useState(false);
  return (
    <SettingsCard
      title={title}
      label={title}
      description={`${description}${stale.length ? ` ${stale.length} of ${items.length} outdated.` : ""}`}
      actions={
        <>
          {bulk && stale.length > 0 && (
            <Button type="button" size="sm" disabled={bulk.pending} onClick={() => setConfirm(true)}>
              {bulk.pending ? <Loader2Icon className="animate-spin" /> : <DownloadIcon />}
              Update Wolf images
            </Button>
          )}
          <Button type="button" size="sm" variant="outline" disabled={refreshing} onClick={onRefresh} title="Re-probe local and remote digests">
            {refreshing ? <Loader2Icon className="animate-spin" /> : <RefreshCwIcon />}
            Check for updates
          </Button>
        </>
      }
      flush
    >
      <SettingsTable
        query={{ ...probe, data: items }}
        keyExtractor={(i) => i.image}
        persistKey={`wolf-images-${title.split(" ")[0].toLowerCase()}`}
        searchPlaceholder="Search images..."
        empty={{ title: emptyTitle }}
        columns={[
          { key: "image", header: "Image", sortAccessor: (i) => i.image, accessor: (i) => <span className="break-all font-mono text-xs">{i.image}</span> },
          {
            key: "local", header: "Local", sortable: false,
            accessor: (i) => (i.local_present && !i.local_digest ? <span className="text-xs text-muted-foreground">Present (local build)</span> : <Digest label="local" value={i.local_digest} err={i.local_error} />),
          },
          { key: "remote", header: "Remote", sortable: false, accessor: (i) => <Digest label="remote" value={i.remote_digest} err={i.remote_error} /> },
          { key: "state", header: "State", sortAccessor: (i) => imageState(i).label, accessor: (i) => { const s = imageState(i); return <StatusBadge status={s.status} label={s.label} size="sm" />; } },
          {
            key: "actions", header: "", sortable: false,
            accessor: (i) => <RowActions label={i.image} actions={[needsPull(i) && { label: i.updates_available ? "Update image" : "Pull image", onSelect: () => onPull(i.image) }]} />,
          },
        ]}
      />
      <ConfirmDialog
        open={confirm}
        onOpenChange={setConfirm}
        variant="default"
        title="Update Wolf scanner images?"
        description={`Pulls ${stale.length} outdated image${stale.length === 1 ? "" : "s"} to their current digest. New scans use them once the pull finishes.`}
        confirmLabel="Update images"
        pending={bulk?.pending}
        onConfirm={() => {
          setConfirm(false);
          bulk?.run();
        }}
      />
    </SettingsCard>
  );
}

function ImagesPanel() {
  const qc = useQueryClient();
  const notified = useRef("");
  const q = useQuery({
    queryKey: ["scanner-images"],
    queryFn: async () => (await api.get<ImageStatus[]>("/scanners/images")).data ?? [],
    // Registry probes shell out per image; keep the cadence slow enough to be
    // cheap while still surfacing scanner channel changes without a restart.
    refetchInterval: 6 * 60 * 60 * 1000,
    refetchOnWindowFocus: false,
  });
  const refresh = () => {
    qc.invalidateQueries({ queryKey: ["scanner-images"] });
    qc.invalidateQueries({ queryKey: ["scanners", "images"] });
    qc.invalidateQueries({ queryKey: ["scanners-config"] });
  };
  const pullOne = useMutation({
    mutationFn: (image: string) => api.post("/scanners/images/pull", { image }),
    onSuccess: (_, image) => {
      toast.success(`Pulled ${image}`);
      refresh();
    },
    onError: (e) => toast.error(safeErrorMessage(e, "The scanner image could not be pulled.")),
  });
  const images = q.data ?? [];
  const wolfBuilt = images.filter((img) => isWolfBuiltImage(img.image));
  const upstream = images.filter((img) => !isWolfBuiltImage(img.image));
  const pullAll = useMutation({
    mutationFn: async () => {
      const errors: string[] = [];
      let pulled = 0;
      for (const image of wolfBuilt.filter((item) => item.updates_available)) {
        try {
          await api.post("/scanners/images/pull", { image: image.image });
          pulled++;
        } catch (e) {
          errors.push(safeErrorMessage(e));
        }
      }
      return { pulled, failed: errors.length };
    },
    onSuccess: ({ pulled, failed }) => {
      if (failed > 0) toast.error(`${failed} scanner image update${failed === 1 ? "" : "s"} failed`);
      else toast.success(`Pulled ${pulled} scanner image${pulled === 1 ? "" : "s"}`);
      refresh();
    },
    onError: (e) => toast.error(safeErrorMessage(e, "Scanner images could not be pulled.")),
  });

  const updateDigest = images.filter((i) => i.updates_available).map((i) => `${i.image}:${i.remote_digest ?? ""}`).join("|");
  const updateCount = images.filter((i) => i.updates_available).length;
  useEffect(() => {
    if (!updateDigest || notified.current === updateDigest) return;
    notified.current = updateDigest;
    toast.info(`${updateCount} scanner image update${updateCount === 1 ? "" : "s"} available`);
  }, [updateDigest, updateCount]);

  const busy = pullOne.isPending || pullAll.isPending;
  const common = { probe: q, onRefresh: () => q.refetch(), refreshing: q.isFetching, onPull: (image: string) => !busy && pullOne.mutate(image) };
  return (
    <div className="space-y-4">
      <ImagesTable
        {...common}
        title="Wolf scanner images"
        description="Built and released by the Wolf scanner factory. These move with the configured release channel."
        items={wolfBuilt}
        bulk={{ run: () => pullAll.mutate(), pending: busy }}
        emptyTitle="No Wolf-built scanner images configured"
      />
      <ImagesTable
        {...common}
        title="Upstream tool images"
        description="Third-party scanners pinned to an exact version in scanner-lock.yaml. They change only when that lock is bumped."
        items={upstream}
        emptyTitle="No upstream tool images configured"
      />
    </div>
  );
}

export function ImagesSection() {
  return (
    <DockerRuntimeGate>
      <ImagesPanel />
    </DockerRuntimeGate>
  );
}
