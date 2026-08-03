import { useEffect, useId, useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  CheckCircle2Icon,
  GitCompareArrowsIcon,
  Loader2Icon,
  RotateCcwIcon,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  createIdempotencyKey,
  scannerSupplyChainApi,
  type ReleaseSummary,
} from "@/lib/scanner-supply-chain";
import type { Scan } from "@/lib/types";

const RUNNABLE_RELEASE_STATES = new Set(["published", "stable"]);

export function ScanReleaseControls({
  scan,
  authorized,
}: {
  scan: Scan;
  authorized: boolean;
}) {
  const [open, setOpen] = useState(false);
  const overview = useQuery({
    queryKey: ["scanner-supply-chain", "overview"],
    queryFn: scannerSupplyChainApi.overview,
    enabled: authorized,
    staleTime: 20_000,
  });
  const capabilityUnavailableReason = overview.isPending
    ? "Checking scanner release-management capabilities."
    : overview.isError
      ? "Release-management capabilities are unavailable."
      : !overview.data?.capabilities?.candidates
        ? "Requires candidate mode or higher."
        : undefined;

  return (
    <section
      aria-labelledby="scan-release-provenance-heading"
      className="glass-card p-5"
    >
      <div className="flex flex-col gap-4 lg:flex-row lg:items-start lg:justify-between">
        <div className="min-w-0">
          <h2
            id="scan-release-provenance-heading"
            className="text-base font-semibold"
          >
            Scanner release provenance
          </h2>
          <p className="mt-1 text-xs text-muted-foreground">
            This assignment is pinned for the lifetime of this scan, including
            ordinary worker recovery and retry.
          </p>
        </div>
        {authorized ? (
          <Button
            type="button"
            variant="outline"
            onClick={() => setOpen(true)}
            disabled={Boolean(capabilityUnavailableReason)}
            title={capabilityUnavailableReason}
          >
            <GitCompareArrowsIcon aria-hidden="true" />
            Re-scan under different release
          </Button>
        ) : null}
      </div>

      <dl className="mt-4 grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
        <ProvenanceValue label="Release ID">
          {scan.scanner_release_id ?? "Legacy / not recorded"}
        </ProvenanceValue>
        <ProvenanceValue label="Manifest digest">
          {scan.release_manifest_digest ?? "Not recorded"}
        </ProvenanceValue>
        <ProvenanceValue label="Lineage">
          {scan.rescan_of_scan_id ? (
            <a
              href={`/scans/${encodeURIComponent(scan.rescan_of_scan_id)}`}
              className="text-primary underline-offset-4 hover:underline"
            >
              Re-scan of {scan.rescan_of_scan_id}
            </a>
          ) : (
            "Original scan"
          )}
        </ProvenanceValue>
        <ProvenanceValue label="Release selection reason">
          {scan.release_selection_reason ?? "Initial assignment"}
        </ProvenanceValue>
      </dl>

      {authorized && capabilityUnavailableReason ? (
        <p className="mt-3 text-xs text-amber-300">
          New-release re-scan unavailable: {capabilityUnavailableReason}
        </p>
      ) : null}

      {open ? (
        <ReleaseRescanDialog
          sourceScan={scan}
          onClose={() => setOpen(false)}
        />
      ) : null}
    </section>
  );
}

function ProvenanceValue({
  label,
  children,
}: {
  label: string;
  children: React.ReactNode;
}) {
  return (
    <div className="min-w-0">
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd className="mt-1 break-all font-mono text-xs">{children}</dd>
    </div>
  );
}

function ReleaseRescanDialog({
  sourceScan,
  onClose,
}: {
  sourceScan: Scan;
  onClose: () => void;
}) {
  const reasonId = useId();
  const releaseId = useId();
  const confirmationId = useId();
  const queryClient = useQueryClient();
  const [selectedReleaseId, setSelectedReleaseId] = useState("");
  const [reason, setReason] = useState("");
  const [confirmation, setConfirmation] = useState("");
  const [idempotencyKey] = useState(() => createIdempotencyKey());
  const releases = useQuery({
    queryKey: ["scanner-supply-chain", "releases", "rescan-options"],
    queryFn: () => scannerSupplyChainApi.releases({ limit: 100 }),
    staleTime: 60_000,
  });
  const options = useMemo(
    () =>
      (releases.data?.items ?? []).filter(
        (release) =>
          release.id !== sourceScan.scanner_release_id &&
          !release.legacy &&
          !release.revoked_at &&
          !release.deprecated_at &&
          RUNNABLE_RELEASE_STATES.has(release.state) &&
          Boolean(release.manifest_digest),
      ),
    [releases.data?.items, sourceScan.scanner_release_id],
  );
  const selectedRelease = useMemo(
    () => options.find((release) => release.id === selectedReleaseId),
    [options, selectedReleaseId],
  );

  useEffect(() => {
    if (
      options.length > 0 &&
      !options.some((release) => release.id === selectedReleaseId)
    ) {
      setSelectedReleaseId(options[0].id);
      setConfirmation("");
    }
  }, [options, selectedReleaseId]);

  const createRescan = useMutation({
    mutationFn: () => {
      if (!selectedRelease) throw new Error("Select a runnable release.");
      return scannerSupplyChainApi.createReleaseRescan(
        sourceScan.id,
        {
          release_id: selectedRelease.id,
          reason: reason.trim(),
        },
        idempotencyKey,
      );
    },
    onSuccess: (created) => {
      queryClient.setQueryData(["scan", created.id], created);
      queryClient.invalidateQueries({ queryKey: ["scans"] });
    },
  });

  const canSubmit =
    Boolean(selectedRelease) &&
    reason.trim().length >= 3 &&
    confirmation.trim() === selectedRelease?.id;

  return (
    <Dialog
      open
      onOpenChange={(nextOpen) => {
        if (!nextOpen && !createRescan.isPending) onClose();
      }}
    >
      <DialogContent className="max-h-[90vh] max-w-2xl overflow-y-auto">
        {createRescan.data ? (
          <ReleaseRescanSuccess
            sourceScan={sourceScan}
            createdScan={createRescan.data}
            onClose={onClose}
          />
        ) : (
          <>
            <DialogHeader>
              <DialogTitle>Create a distinct release re-scan?</DialogTitle>
              <DialogDescription>
                This creates a new scan ID and preserves the source scan
                unchanged.
              </DialogDescription>
            </DialogHeader>

            <div className="rounded-lg border border-amber-500/50 bg-amber-500/10 p-4 text-sm">
              <p className="font-semibold text-amber-200">
                This is not Retry.
              </p>
              <p className="mt-1 text-amber-100/90">
                Ordinary retry continues scan{" "}
                <span className="font-mono">{sourceScan.id}</span> under its
                pinned release and manifest digest. This action creates a
                separate lineage-bearing scan under a different immutable
                release so results can be compared without rewriting history.
              </p>
            </div>

            {releases.isPending ? (
              <div
                role="status"
                className="flex items-center gap-2 text-sm text-muted-foreground"
              >
                <Loader2Icon className="size-4 animate-spin" aria-hidden="true" />
                Loading runnable immutable releases…
              </div>
            ) : releases.isError ? (
              <div
                role="alert"
                className="rounded-md border border-destructive/40 bg-destructive/10 p-3 text-sm"
              >
                <p>
                  {releases.error instanceof Error
                    ? releases.error.message
                    : "Runnable releases could not be loaded."}
                </p>
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  className="mt-2"
                  onClick={() => releases.refetch()}
                >
                  <RotateCcwIcon aria-hidden="true" /> Retry release lookup
                </Button>
              </div>
            ) : options.length === 0 ? (
              <div
                role="status"
                className="rounded-md border border-border bg-muted/20 p-3 text-sm text-muted-foreground"
              >
                No different runnable release is available. Legacy,
                deprecated, revoked, and digest-less releases are excluded.
              </div>
            ) : (
              <>
                <div className="space-y-1.5">
                  <Label htmlFor={releaseId}>Immutable scanner release</Label>
                  <select
                    id={releaseId}
                    value={selectedReleaseId}
                    onChange={(event) => {
                      setSelectedReleaseId(event.target.value);
                      setConfirmation("");
                      createRescan.reset();
                    }}
                    className="h-10 w-full rounded-md border border-input bg-background px-3 text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                  >
                    {options.map((release) => (
                      <option key={release.id} value={release.id}>
                        {release.name ?? release.id} · {release.state}
                      </option>
                    ))}
                  </select>
                </div>

                <ReleaseComparison
                  sourceScan={sourceScan}
                  selectedRelease={selectedRelease}
                />

                <div className="space-y-1.5">
                  <Label htmlFor={reasonId}>Reason</Label>
                  <textarea
                    id={reasonId}
                    value={reason}
                    onChange={(event) => setReason(event.target.value)}
                    placeholder="Explain why results must be compared under this release"
                    rows={3}
                    className="w-full rounded-md border border-input bg-background px-3 py-2 text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                  />
                  <p className="text-xs text-muted-foreground">
                    The reason is stored on the new scan as release-selection
                    provenance.
                  </p>
                </div>

                <div className="space-y-1.5">
                  <Label htmlFor={confirmationId}>
                    Type{" "}
                    <span className="break-all font-mono">
                      {selectedRelease?.id}
                    </span>{" "}
                    to confirm
                  </Label>
                  <Input
                    id={confirmationId}
                    value={confirmation}
                    onChange={(event) => setConfirmation(event.target.value)}
                    autoComplete="off"
                  />
                </div>
              </>
            )}

            {createRescan.error ? (
              <div
                role="alert"
                className="rounded-md border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive"
              >
                {createRescan.error instanceof Error
                  ? createRescan.error.message
                  : "The release re-scan could not be created."}
              </div>
            ) : null}

            <DialogFooter>
              <Button
                type="button"
                variant="outline"
                onClick={onClose}
                disabled={createRescan.isPending}
              >
                Cancel
              </Button>
              <Button
                type="button"
                onClick={() => createRescan.mutate()}
                disabled={!canSubmit || createRescan.isPending}
              >
                {createRescan.isPending ? (
                  <Loader2Icon className="animate-spin" aria-hidden="true" />
                ) : (
                  <GitCompareArrowsIcon aria-hidden="true" />
                )}
                Create distinct re-scan
              </Button>
            </DialogFooter>
          </>
        )}
      </DialogContent>
    </Dialog>
  );
}

function ReleaseComparison({
  sourceScan,
  selectedRelease,
}: {
  sourceScan: Scan;
  selectedRelease?: ReleaseSummary;
}) {
  return (
    <div className="grid gap-3 sm:grid-cols-2">
      <section className="rounded-md border border-border/70 bg-card p-3">
        <p className="text-xs font-medium uppercase tracking-wide text-muted-foreground">
          Source remains unchanged
        </p>
        <p className="mt-2 break-all font-mono text-xs">
          {sourceScan.scanner_release_id ?? "Legacy / not recorded"}
        </p>
        <p className="mt-1 break-all font-mono text-xs text-muted-foreground">
          {sourceScan.release_manifest_digest ?? "Digest not recorded"}
        </p>
      </section>
      <section className="rounded-md border border-primary/40 bg-primary/5 p-3">
        <p className="text-xs font-medium uppercase tracking-wide text-primary">
          New scan assignment
        </p>
        <p className="mt-2 break-all font-mono text-xs">
          {selectedRelease?.id ?? "Select a release"}
        </p>
        <p className="mt-1 break-all font-mono text-xs text-muted-foreground">
          {selectedRelease?.manifest_digest ?? "Manifest unavailable"}
        </p>
      </section>
    </div>
  );
}

function ReleaseRescanSuccess({
  sourceScan,
  createdScan,
  onClose,
}: {
  sourceScan: Scan;
  createdScan: Scan;
  onClose: () => void;
}) {
  return (
    <>
      <DialogHeader>
        <DialogTitle className="flex items-center gap-2">
          <CheckCircle2Icon className="size-5 text-emerald-400" aria-hidden="true" />
          Distinct re-scan created
        </DialogTitle>
        <DialogDescription>
          The source scan remains unchanged. The new scan is queued under the
          selected immutable release.
        </DialogDescription>
      </DialogHeader>
      <dl className="grid gap-3 rounded-lg border border-emerald-500/30 bg-emerald-500/5 p-4 sm:grid-cols-2">
        <ProvenanceValue label="New scan ID">{createdScan.id}</ProvenanceValue>
        <ProvenanceValue label="Re-scan of">
          {createdScan.rescan_of_scan_id ?? sourceScan.id}
        </ProvenanceValue>
        <ProvenanceValue label="Release ID">
          {createdScan.scanner_release_id ?? "Not returned"}
        </ProvenanceValue>
        <ProvenanceValue label="Manifest digest">
          {createdScan.release_manifest_digest ?? "Not returned"}
        </ProvenanceValue>
      </dl>
      <DialogFooter>
        <Button type="button" variant="outline" onClick={onClose}>
          Close
        </Button>
        <Button asChild>
          <a href={`/scans/${encodeURIComponent(createdScan.id)}`}>
            Open new scan
          </a>
        </Button>
      </DialogFooter>
    </>
  );
}
