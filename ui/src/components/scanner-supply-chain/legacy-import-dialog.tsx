import { useId, useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  ArchiveIcon,
  CheckCircle2Icon,
  Loader2Icon,
  RotateCcwIcon,
  ShieldAlertIcon,
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
  type LegacyConfigurationSnapshot,
  type LegacyReleaseImportResult,
} from "@/lib/scanner-supply-chain";
import { safeErrorMessage } from "@/lib/safe-display";

const CONFIRMATION_TEXT = "IMPORT LEGACY SNAPSHOT";
const SHA256_DIGEST = /^sha256:[a-f0-9]{64}$/;
const EXPECTED_LIMITATIONS = [
  "Image signatures were not verified by the managed release pipeline.",
  "SBOM and build provenance are unavailable.",
  "The snapshot is historical evidence and is not rollout eligible.",
];

export interface LegacyConfiguredImagePreview {
  key: string;
  reference: string;
  source: "default" | "override" | "upstream";
  embeddedDigest?: string;
  invalidEmbeddedDigest?: boolean;
}

export function deriveLegacyConfiguredImages(
  snapshot: LegacyConfigurationSnapshot,
): LegacyConfiguredImagePreview[] {
  const images = new Map<string, LegacyConfiguredImagePreview>();
  const add = (
    key: string,
    reference: string | undefined,
    source: LegacyConfiguredImagePreview["source"],
  ) => {
    const normalized = reference?.trim();
    if (!normalized || images.has(key)) return;
    const at = normalized.lastIndexOf("@");
    const suffix = at >= 0 ? normalized.slice(at + 1).trim() : undefined;
    images.set(key, {
      key,
      reference: normalized,
      source,
      embeddedDigest: suffix && SHA256_DIGEST.test(suffix) ? suffix : undefined,
      invalidEmbeddedDigest: at >= 0 && !SHA256_DIGEST.test(suffix ?? ""),
    });
  };

  add("default", snapshot.config.image, "default");
  for (const [tool, reference] of Object.entries(
    snapshot.config.image_overrides ?? {},
  ).sort(([left], [right]) => left.localeCompare(right))) {
    add(`wolf-${tool}`, reference, "override");
  }
  for (const tool of snapshot.tools
    .filter((item) => item.integration_tier === "upstream")
    .toSorted((left, right) => left.name.localeCompare(right.name))) {
    add(`upstream-${tool.name}`, tool.configured_image, "upstream");
  }
  return [...images.values()].sort((left, right) =>
    left.key.localeCompare(right.key),
  );
}

export function LegacyImportDialog({
  open,
  onOpenChange,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const reasonId = useId();
  const confirmationId = useId();
  const queryClient = useQueryClient();
  const [reason, setReason] = useState("");
  const [confirmation, setConfirmation] = useState("");
  const [resolvedDigests, setResolvedDigests] = useState<
    Record<string, string>
  >({});
  const [idempotencyKey] = useState(() => createIdempotencyKey());
  const configuration = useQuery({
    queryKey: ["scanner-supply-chain", "legacy-configuration"],
    queryFn: scannerSupplyChainApi.legacyConfiguration,
    enabled: open,
    staleTime: 10_000,
  });
  const images = useMemo(
    () =>
      configuration.data
        ? deriveLegacyConfiguredImages(configuration.data)
        : [],
    [configuration.data],
  );
  const taggedImages = useMemo(
    () =>
      images.filter(
        (image) => !image.embeddedDigest && !image.invalidEmbeddedDigest,
      ),
    [images],
  );
  const invalidReferences = useMemo(
    () => images.filter((image) => image.invalidEmbeddedDigest),
    [images],
  );

  const importSnapshot = useMutation({
    mutationFn: () => {
      const supplied = Object.fromEntries(
        taggedImages.map((image) => [
          image.key,
          (resolvedDigests[image.key] ?? "").trim(),
        ]),
      );
      return scannerSupplyChainApi.importLegacyConfiguration(
        {
          reason: reason.trim(),
          ...(Object.keys(supplied).length > 0
            ? { resolved_digests: supplied }
            : {}),
        },
        idempotencyKey,
      );
    },
    onSuccess: (result) => {
      queryClient.invalidateQueries({
        queryKey: ["scanner-supply-chain", "releases"],
      });
      queryClient.setQueryData(
        ["scanner-supply-chain", "release", result.release.id],
        {
          ...result.release,
          images: result.images,
        },
      );
    },
  });

  const digestsValid = taggedImages.every((image) =>
    SHA256_DIGEST.test((resolvedDigests[image.key] ?? "").trim()),
  );
  const canSubmit =
    images.length > 0 &&
    invalidReferences.length === 0 &&
    digestsValid &&
    reason.trim().length >= 3 &&
    confirmation.trim() === CONFIRMATION_TEXT;

  return (
    <Dialog
      open={open}
      onOpenChange={
        importSnapshot.isPending
          ? undefined
          : (nextOpen) => onOpenChange(nextOpen)
      }
    >
      <DialogContent className="max-h-[92vh] max-w-3xl overflow-y-auto">
        {importSnapshot.data ? (
          <LegacyImportSuccess
            result={importSnapshot.data}
            onClose={() => onOpenChange(false)}
          />
        ) : (
          <>
            <DialogHeader>
              <DialogTitle>Import legacy configuration snapshot</DialogTitle>
              <DialogDescription>
                Archive the scanner image references currently configured on
                this deployment as immutable, unverified historical evidence.
              </DialogDescription>
            </DialogHeader>

            <div className="rounded-lg border border-amber-500/50 bg-amber-500/10 p-4">
              <div className="flex items-start gap-3">
                <ShieldAlertIcon
                  className="mt-0.5 size-5 shrink-0 text-amber-300"
                  aria-hidden="true"
                />
                <div>
                  <p className="font-semibold text-amber-200">
                    Evidence-only import with permanent limitations
                  </p>
                  <ul className="mt-2 list-disc space-y-1 pl-5 text-sm text-amber-100/90">
                    <li>
                      The release is marked legacy, imported, protected, and
                      not rollback eligible.
                    </li>
                    <li>
                      It cannot be selected for rollout or a managed release
                      re-scan.
                    </li>
                    <li>
                      It does not change the desired release, worker
                      assignments, or queued and running scans.
                    </li>
                    <li>
                      Wolf records references and supplied digests; it does not
                      pull, retag, sign, or rebuild these images.
                    </li>
                  </ul>
                </div>
              </div>
            </div>

            {configuration.isPending ? (
              <div
                role="status"
                className="flex items-center gap-2 rounded-md border border-border p-4 text-sm text-muted-foreground"
              >
                <Loader2Icon className="size-4 animate-spin" aria-hidden="true" />
                Loading the deployment’s current scanner configuration…
              </div>
            ) : configuration.isError ? (
              <div
                role="alert"
                className="rounded-md border border-destructive/40 bg-destructive/10 p-4 text-sm"
              >
                <p>
                  {safeErrorMessage(
                    configuration.error,
                    "The configured scanner references could not be loaded.",
                  )}
                </p>
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  className="mt-3"
                  onClick={() => configuration.refetch()}
                >
                  <RotateCcwIcon aria-hidden="true" />
                  Retry configuration preview
                </Button>
              </div>
            ) : images.length === 0 ? (
              <div
                role="status"
                className="rounded-md border border-border p-4 text-sm text-muted-foreground"
              >
                No configured scanner image references are available to
                archive.
              </div>
            ) : (
              <>
                <section
                  aria-labelledby="legacy-configuration-preview"
                  className="overflow-hidden rounded-lg border border-border/70"
                >
                  <div className="border-b border-border/70 bg-muted/20 px-4 py-3">
                    <h3
                      id="legacy-configuration-preview"
                      className="text-sm font-medium"
                    >
                      Configuration preview
                    </h3>
                    <p className="mt-0.5 text-xs text-muted-foreground">
                      {images.length} configured reference
                      {images.length === 1 ? "" : "s"} will be snapshotted.
                      Tagged references require an operator-supplied immutable
                      digest.
                    </p>
                  </div>
                  <div
                    className="overflow-x-auto focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring"
                    role="region"
                    tabIndex={0}
                    aria-label="Legacy scanner configuration preview"
                  >
                    <table className="w-full min-w-[44rem] text-sm">
                      <thead className="bg-muted/10 text-left text-xs text-muted-foreground">
                        <tr>
                          <th className="px-4 py-2 font-medium">Image key</th>
                          <th className="px-4 py-2 font-medium">Configured reference</th>
                          <th className="px-4 py-2 font-medium">Immutable digest</th>
                        </tr>
                      </thead>
                      <tbody className="divide-y divide-border/50">
                        {images.map((image) => (
                          <tr key={image.key}>
                            <td className="px-4 py-3 align-top">
                              <p className="font-mono text-xs">{image.key}</p>
                              <p className="mt-1 text-[11px] text-muted-foreground">
                                {image.source}
                              </p>
                            </td>
                            <td className="max-w-80 break-all px-4 py-3 align-top font-mono text-xs">
                              {image.reference}
                            </td>
                            <td className="min-w-80 px-4 py-3 align-top">
                              {image.embeddedDigest ? (
                                <p className="break-all font-mono text-xs text-emerald-300">
                                  {image.embeddedDigest}
                                </p>
                              ) : image.invalidEmbeddedDigest ? (
                                <p className="text-xs text-destructive-text">
                                  The reference contains an invalid digest
                                  suffix. Correct deployment configuration
                                  before importing.
                                </p>
                              ) : (
                                <div className="space-y-1">
                                  <Label
                                    className="sr-only"
                                    htmlFor={`legacy-digest-${image.key}`}
                                  >
                                    Digest for {image.key}
                                  </Label>
                                  <Input
                                    id={`legacy-digest-${image.key}`}
                                    value={resolvedDigests[image.key] ?? ""}
                                    onChange={(event) =>
                                      setResolvedDigests((current) => ({
                                        ...current,
                                        [image.key]: event.target.value,
                                      }))
                                    }
                                    placeholder="sha256:64 lowercase hex characters"
                                    autoComplete="off"
                                    spellCheck={false}
                                    className="font-mono text-xs"
                                    aria-invalid={
                                      Boolean(resolvedDigests[image.key]) &&
                                      !SHA256_DIGEST.test(
                                        resolvedDigests[image.key].trim(),
                                      )
                                    }
                                  />
                                </div>
                              )}
                            </td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                </section>

                <section className="rounded-lg border border-border/70 bg-muted/10 p-4">
                  <h3 className="text-sm font-medium">
                    Provenance limitations recorded with the result
                  </h3>
                  <ul className="mt-2 list-disc space-y-1 pl-5 text-xs text-muted-foreground">
                    {EXPECTED_LIMITATIONS.map((limitation) => (
                      <li key={limitation}>{limitation}</li>
                    ))}
                  </ul>
                </section>

                <div className="space-y-1.5">
                  <Label htmlFor={reasonId}>Audit reason</Label>
                  <textarea
                    id={reasonId}
                    value={reason}
                    onChange={(event) => setReason(event.target.value)}
                    placeholder="Explain why this deployment state must be preserved"
                    rows={3}
                    className="w-full rounded-md border border-input bg-background px-3 py-2 text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                  />
                </div>

                <div className="space-y-1.5">
                  <Label htmlFor={confirmationId}>
                    Type{" "}
                    <span className="font-mono">{CONFIRMATION_TEXT}</span> to
                    confirm
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

            {importSnapshot.error ? (
              <div
                role="alert"
                className="rounded-md border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive-text"
              >
                {safeErrorMessage(
                  importSnapshot.error,
                  "The legacy configuration snapshot could not be imported.",
                )}
                <p className="mt-1 text-xs">
                  Correct the issue and submit again; this dialog reuses the
                  same idempotency key.
                </p>
              </div>
            ) : null}

            <DialogFooter>
              <Button
                type="button"
                variant="outline"
                onClick={() => onOpenChange(false)}
                disabled={importSnapshot.isPending}
              >
                Cancel
              </Button>
              <Button
                type="button"
                onClick={() => importSnapshot.mutate()}
                disabled={!canSubmit || importSnapshot.isPending}
              >
                {importSnapshot.isPending ? (
                  <Loader2Icon className="animate-spin" aria-hidden="true" />
                ) : (
                  <ArchiveIcon aria-hidden="true" />
                )}
                Import evidence snapshot
              </Button>
            </DialogFooter>
          </>
        )}
      </DialogContent>
    </Dialog>
  );
}

function LegacyImportSuccess({
  result,
  onClose,
}: {
  result: LegacyReleaseImportResult;
  onClose: () => void;
}) {
  const release = result.release;
  return (
    <>
      <DialogHeader>
        <DialogTitle className="flex items-center gap-2">
          <CheckCircle2Icon className="size-5 text-emerald-400" aria-hidden="true" />
          {result.created
            ? "Legacy snapshot imported"
            : "Existing legacy snapshot returned"}
        </DialogTitle>
        <DialogDescription>
          The operation was idempotent and runtime assignments remain
          unchanged.
        </DialogDescription>
      </DialogHeader>

      <div className="rounded-lg border border-emerald-500/30 bg-emerald-500/5 p-4">
        <p className="font-medium">{release.name ?? release.id}</p>
        <p className="mt-1 break-all font-mono text-xs text-muted-foreground">
          {release.id}
        </p>
        <p className="mt-1 break-all font-mono text-xs text-muted-foreground">
          {release.manifest_digest}
        </p>
        <div className="mt-3 flex flex-wrap gap-2 text-xs">
          <ResultFlag enabled={Boolean(release.legacy)} label="Legacy" />
          <ResultFlag enabled={Boolean(release.imported)} label="Imported" />
          <ResultFlag enabled={Boolean(release.protected)} label="Protected" />
          <ResultFlag
            enabled={!release.rollback_eligible}
            label="Not rollback eligible"
          />
          <ResultFlag
            enabled={!result.runtime_assignments_changed}
            label="Runtime unchanged"
          />
        </div>
      </div>

      <section className="rounded-lg border border-border/70 p-4">
        <h3 className="text-sm font-medium">Recorded provenance limitations</h3>
        <ul className="mt-2 list-disc space-y-1 pl-5 text-xs text-muted-foreground">
          {result.provenance_limitations.map((limitation) => (
            <li key={limitation}>{limitation}</li>
          ))}
        </ul>
      </section>

      {result.runtime_assignments_changed ? (
        <div
          role="alert"
          className="rounded-md border border-destructive/50 bg-destructive/10 p-3 text-sm text-destructive-text"
        >
          The server reported an unexpected runtime assignment change. Stop and
          investigate before continuing.
        </div>
      ) : (
        <p className="text-sm text-muted-foreground">
          Desired release, worker assignments, and queued or running scans were
          not changed.
        </p>
      )}

      <DialogFooter>
        <Button type="button" variant="outline" onClick={onClose}>
          Close
        </Button>
        <Button asChild>
          <a
            href={`/scanners?tab=releases&release=${encodeURIComponent(release.id)}`}
          >
            Open imported release
          </a>
        </Button>
      </DialogFooter>
    </>
  );
}

function ResultFlag({ enabled, label }: { enabled: boolean; label: string }) {
  return (
    <span
      className={
        enabled
          ? "rounded-full border border-emerald-500/40 bg-emerald-500/10 px-2 py-1 text-emerald-200"
          : "rounded-full border border-destructive/40 bg-destructive/10 px-2 py-1 text-destructive-text"
      }
    >
      {enabled ? "✓" : "!"} {label}
    </span>
  );
}
