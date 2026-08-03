import { memo, useId } from "react";
import { useQueries } from "@tanstack/react-query";
import { RefreshCcwIcon } from "lucide-react";
import { Button } from "@/components/ui/button";
import { PanelHeading } from "./primitives";
import {
  scannerSupplyChainApi,
  type ArtifactDiff,
  type ArtifactDiffKind,
  type ArtifactDiffOwner,
} from "@/lib/scanner-supply-chain";
import { safeErrorMessage } from "@/lib/safe-display";

const diffKinds: ArtifactDiffKind[] = ["manifest", "lock"];
const numberFormatter = new Intl.NumberFormat();

export const ArtifactDiffViewer = memo(function ArtifactDiffViewer({
  ownerType,
  ownerId,
}: {
  ownerType: ArtifactDiffOwner;
  ownerId: string;
}) {
  const results = useQueries({
    queries: diffKinds.map((kind) => ({
      queryKey: [
        "scanner-supply-chain",
        ownerType,
        ownerId,
        "artifact-diff",
        kind,
      ],
      queryFn: () =>
        scannerSupplyChainApi.artifactDiff(ownerType, ownerId, kind),
      staleTime: ownerType === "release" ? Number.POSITIVE_INFINITY : 30_000,
    })),
  });

  return (
    <div className="grid min-w-0 gap-4 xl:grid-cols-2">
      {diffKinds.map((kind, index) => (
        <DiffPanel
          key={kind}
          kind={kind}
          diff={results[index].data}
          loading={results[index].isPending}
          error={results[index].error}
          onRetry={() => results[index].refetch()}
        />
      ))}
    </div>
  );
});

function DiffPanel({
  kind,
  diff,
  loading,
  error,
  onRetry,
}: {
  kind: ArtifactDiffKind;
  diff?: ArtifactDiff;
  loading: boolean;
  error: Error | null;
  onRetry: () => void;
}) {
  const descriptionId = useId();
  const title = kind === "manifest" ? "Manifest Diff" : "Release Lock Diff";

  return (
    <section className="min-w-0 overflow-hidden rounded-lg border border-border/70 bg-card">
      <PanelHeading
        title={title}
        description="Authenticated, integrity-checked text from durable release evidence."
      />
      {loading ? (
        <div
          className="space-y-3 p-4"
          role="status"
          aria-live="polite"
          aria-label={`Loading ${title.toLowerCase()}`}
        >
          <p className="text-sm text-muted-foreground">
            Loading {title.toLowerCase()}…
          </p>
          <div
            className="h-28 animate-pulse rounded bg-muted/30 motion-reduce:animate-none"
            aria-hidden="true"
          />
        </div>
      ) : error ? (
        <div className="space-y-3 p-4" role="alert">
          <div>
            <p className="text-sm font-medium">Could Not Load {title}</p>
            <p className="mt-1 text-sm text-muted-foreground">
              {safeErrorMessage(
                error,
                "The diff service did not return evidence.",
              )}{" "}
              Retry the authenticated request.
            </p>
          </div>
          <Button type="button" variant="outline" size="sm" onClick={onRetry}>
            <RefreshCcwIcon aria-hidden="true" /> Retry {title}
          </Button>
        </div>
      ) : !diff?.available ? (
        <div className="p-8 text-center" role="status" aria-live="polite">
          <p className="text-sm font-medium">No {title} Available</p>
          <p className="mt-1 text-sm text-muted-foreground">
            This evidence has not been persisted for the current resource.
          </p>
        </div>
      ) : (
        <>
          <div
            id={descriptionId}
            className="flex flex-wrap items-center gap-x-3 gap-y-1 border-y border-border/50 bg-muted/10 px-4 py-2 text-xs text-muted-foreground"
          >
            <span>
              {numberFormatter.format(diff.returned_lines)} of{" "}
              {numberFormatter.format(diff.total_lines)} lines
            </span>
            <span>
              {numberFormatter.format(diff.returned_bytes)} of{" "}
              {numberFormatter.format(diff.total_bytes)} bytes
            </span>
            {diff.digest ? (
              <span
                className="min-w-0 break-all font-mono"
                translate="no"
                title={diff.digest}
              >
                {diff.digest}
              </span>
            ) : null}
          </div>
          {diff.truncated ? (
            <p
              className="border-b border-amber-500/30 bg-amber-500/10 px-4 py-2 text-xs text-amber-200"
              role="status"
              aria-live="polite"
            >
              Large diff truncated: showing the first{" "}
              {numberFormatter.format(diff.returned_bytes)} of{" "}
              {numberFormatter.format(diff.total_bytes)} bytes.
            </p>
          ) : null}
          <pre
            className="max-h-[32rem] min-h-40 max-w-full overflow-auto overscroll-contain p-4 font-mono text-xs leading-5 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring"
            tabIndex={0}
            role="region"
            aria-label={`${title} content`}
            aria-describedby={descriptionId}
          >
            <code className="whitespace-pre" translate="no">
              {diff.content}
            </code>
          </pre>
        </>
      )}
    </section>
  );
}
