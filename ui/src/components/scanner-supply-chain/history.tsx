import { memo, useRef, useState } from "react";
import { useVirtualizer } from "@tanstack/react-virtual";
import { CopyIcon, DownloadIcon, LockKeyholeIcon } from "lucide-react";
import { Button } from "@/components/ui/button";
import { CodeValue, StatusBadge, Timestamp, humanize } from "./primitives";
import type {
  LogEntry,
  RolloutEvent,
  AuditEvent,
} from "@/lib/scanner-supply-chain";
import { cn } from "@/lib/utils";
import { safeDisplayText, safeEvidenceHref } from "@/lib/safe-display";

export const EventTimeline = memo(function EventTimeline({
  events,
}: {
  events: Array<RolloutEvent | AuditEvent>;
}) {
  const [copyStatus, setCopyStatus] = useState("");

  async function copyCorrelation(label: string, value: string) {
    try {
      if (!navigator.clipboard?.writeText) {
        throw new Error("Clipboard access is unavailable");
      }
      await navigator.clipboard.writeText(value);
      setCopyStatus(`${label} copied to the clipboard.`);
    } catch {
      setCopyStatus(`Could not copy ${label.toLowerCase()}.`);
    }
  }

  return (
    <>
      <p className="sr-only" aria-live="polite" aria-atomic="true">
        {copyStatus}
      </p>
      <ol className="space-y-0">
        {events.map((event, index) => (
          <li
            key={event.id ?? `${event.created_at}-${index}`}
            className="relative grid grid-cols-[1.25rem_1fr] gap-3 pb-5 [content-visibility:auto] [contain-intrinsic-size:0_76px]"
          >
            <div className="flex flex-col items-center">
              <span className="mt-1 size-2.5 rounded-full border-2 border-primary bg-background" />
              {index < events.length - 1 ? (
                <span className="mt-1 w-px flex-1 bg-border" />
              ) : null}
            </div>
            <div className="min-w-0">
              <div className="flex flex-wrap items-center gap-2">
                <span className="text-sm font-medium">
                  {eventTitle(safeDisplayText(event.event_type, 128))}
                </span>
                {"new_state" in event && event.new_state ? (
                  <StatusBadge state={safeDisplayText(event.new_state, 64)} />
                ) : null}
              </div>
              {event.reason ? (
                <p className="mt-1 whitespace-pre-wrap break-words text-sm text-muted-foreground">
                  {safeDisplayText(event.reason, 2_048)}
                </p>
              ) : null}
              <div className="mt-1 flex flex-wrap gap-x-3 text-xs text-muted-foreground">
                <Timestamp value={event.created_at} />
                {event.actor ? (
                  <span>by {safeDisplayText(event.actor, 256)}</span>
                ) : null}
                {"aggregate_id" in event ? (
                  <CodeValue>
                    {safeDisplayText(event.aggregate_id, 256)}
                  </CodeValue>
                ) : null}
              </div>
              {"trace_id" in event &&
              (event.trace_id ||
                event.operation_id ||
                event.parent_operation_id) ? (
                <div
                  className="mt-2 flex flex-wrap gap-2"
                  aria-label={`Correlation for ${eventTitle(event.event_type)}`}
                >
                  {event.trace_id ? (
                    <CorrelationCopy
                      label="Trace ID"
                      value={safeDisplayText(event.trace_id, 128)}
                      onCopy={copyCorrelation}
                    />
                  ) : null}
                  {event.operation_id ? (
                    <CorrelationCopy
                      label="Operation ID"
                      value={safeDisplayText(event.operation_id, 128)}
                      onCopy={copyCorrelation}
                    />
                  ) : null}
                  {event.parent_operation_id ? (
                    <CorrelationCopy
                      label="Parent operation ID"
                      value={safeDisplayText(event.parent_operation_id, 128)}
                      onCopy={copyCorrelation}
                    />
                  ) : null}
                </div>
              ) : null}
            </div>
          </li>
        ))}
      </ol>
    </>
  );
});

function eventTitle(value: string): string {
  return humanize(value.replaceAll(".", "_"));
}

function CorrelationCopy({
  label,
  value,
  onCopy,
}: {
  label: string;
  value: string;
  onCopy: (label: string, value: string) => void;
}) {
  return (
    <span className="inline-flex min-w-0 items-center gap-1 rounded-md border border-border/60 bg-muted/10 px-2 py-1 text-xs">
      <span className="text-muted-foreground">{label}</span>
      <CodeValue title={value}>{value}</CodeValue>
      <button
        type="button"
        className="rounded-sm p-1 text-muted-foreground hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
        aria-label={`Copy ${label}`}
        onClick={() => onCopy(label, value)}
      >
        <CopyIcon className="size-3" aria-hidden="true" />
      </button>
    </span>
  );
}

export function StructuredLogViewer({
  entries,
  downloadUrl,
}: {
  entries: LogEntry[];
  downloadUrl?: string;
}) {
  const parentRef = useRef<HTMLDivElement>(null);
  const safeDownloadUrl = safeEvidenceHref(downloadUrl);
  const virtualizer = useVirtualizer({
    count: entries.length,
    getScrollElement: () => parentRef.current,
    estimateSize: () => 30,
    overscan: 12,
  });

  return (
    <section className="overflow-hidden rounded-lg border border-border bg-background/70">
      <div className="flex items-center justify-between border-b border-border px-3 py-2">
        <div className="flex items-center gap-2 text-xs text-muted-foreground">
          <LockKeyholeIcon className="size-3.5" aria-hidden="true" />
          Redacted structured output
        </div>
        {safeDownloadUrl ? (
          <Button asChild variant="ghost" size="sm">
            <a href={safeDownloadUrl} download>
              <DownloadIcon aria-hidden="true" />
              Download
            </a>
          </Button>
        ) : null}
      </div>
      {entries.length === 0 ? (
        <p className="p-6 text-center text-sm text-muted-foreground">
          No persisted log entries are available.
        </p>
      ) : (
        <div
          ref={parentRef}
          className="h-80 overflow-auto font-mono text-xs"
          role="log"
          aria-label="Candidate build log"
          aria-live="polite"
        >
          <div
            className="relative w-full"
            style={{ height: `${virtualizer.getTotalSize()}px` }}
          >
            {virtualizer.getVirtualItems().map((item) => {
              const entry = entries[item.index];
              return (
                <div
                  key={entry.id ?? entry.sequence}
                  className="absolute left-0 top-0 grid w-full grid-cols-[5.5rem_7rem_1fr] gap-2 border-b border-border/30 px-3 py-1.5"
                  style={{ transform: `translateY(${item.start}px)` }}
                >
                  <span className="text-faint">
                    {entry.timestamp.slice(11, 19)}
                  </span>
                  <span
                    className={cn(
                      "truncate uppercase",
                      entry.level === "error"
                        ? "text-red-300"
                        : entry.level === "warning"
                          ? "text-amber-300"
                          : "text-muted-foreground",
                    )}
                  >
                    {safeDisplayText(entry.step ?? entry.level ?? "event", 128)}
                  </span>
                  <span className="break-all whitespace-pre-wrap">
                    {safeDisplayText(entry.message, 8_192)}
                  </span>
                </div>
              );
            })}
          </div>
        </div>
      )}
    </section>
  );
}
