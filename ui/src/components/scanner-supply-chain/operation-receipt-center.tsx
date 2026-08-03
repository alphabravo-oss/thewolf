import { useSyncExternalStore } from "react";
import { useQuery } from "@tanstack/react-query";
import { XIcon } from "lucide-react";
import { api } from "@/lib/api";
import {
  dismissOperationReceipt,
  readOperationReceipts,
  subscribeOperationReceipts,
  updateOperationReceiptState,
  type RememberedOperationReceipt,
} from "@/lib/operation-receipts";
import { safeDisplayText } from "@/lib/safe-display";
import { StatusBadge, Timestamp } from "./primitives";

const serverSnapshot: RememberedOperationReceipt[] = [];

export function OperationReceiptCenter() {
  const receipts = useSyncExternalStore(
    subscribeOperationReceipts,
    readOperationReceipts,
    () => serverSnapshot,
  );
  if (receipts.length === 0) return null;
  return (
    <section
      aria-labelledby="scanner-operation-receipts-heading"
      className="overflow-hidden rounded-lg border border-border/70 bg-card"
    >
      <div className="border-b border-border/60 px-4 py-3">
        <h2
          id="scanner-operation-receipts-heading"
          className="text-sm font-semibold"
        >
          Recent durable operations
        </h2>
        <p className="text-xs text-muted-foreground">
          Status receipts remain available in this browser after navigation and
          reload.
        </p>
      </div>
      <ul className="divide-y divide-border/50">
        {receipts.slice(0, 8).map((receipt) => (
          <OperationReceiptRow key={receipt.id} receipt={receipt} />
        ))}
      </ul>
    </section>
  );
}

function OperationReceiptRow({
  receipt,
}: {
  receipt: RememberedOperationReceipt;
}) {
  const status = useQuery({
    queryKey: ["scanner-operation-receipt", receipt.id, receipt.status_url],
    queryFn: async () => {
      const value = (
        await api.get<Record<string, unknown>>(receipt.status_url!)
      ).data;
      const state = operationState(value) ?? receipt.state;
      updateOperationReceiptState(receipt.id, state);
      return state;
    },
    refetchInterval: (query) =>
      isTerminalOperationState(query.state.data ?? receipt.state)
        ? false
        : 5_000,
    retry: 1,
  });
  const state = status.data ?? receipt.state;
  return (
    <li className="flex flex-wrap items-center gap-3 px-4 py-3 text-sm">
      <div className="min-w-0 flex-1">
        <p className="truncate font-medium">
          {safeDisplayText(receipt.label, 96)}
        </p>
        <p className="mt-0.5 truncate font-mono text-xs text-muted-foreground">
          {safeDisplayText(receipt.id, 128)} ·{" "}
          <Timestamp value={receipt.recorded_at} />
        </p>
      </div>
      {status.isError ? (
        <span className="text-xs text-amber-300">
          Status refresh unavailable
        </span>
      ) : null}
      <StatusBadge state={state} />
      <button
        type="button"
        className="rounded p-1 text-muted-foreground hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
        aria-label={`Dismiss ${safeDisplayText(receipt.label, 96)}`}
        onClick={() => dismissOperationReceipt(receipt.id)}
      >
        <XIcon className="size-4" aria-hidden="true" />
      </button>
    </li>
  );
}

function operationState(value: Record<string, unknown>): string | undefined {
  if (typeof value.state === "string") return value.state;
  for (const key of [
    "candidate",
    "release",
    "rollout",
    "job",
    "run",
    "build",
  ]) {
    const nested = value[key];
    if (
      nested &&
      typeof nested === "object" &&
      typeof (nested as Record<string, unknown>).state === "string"
    ) {
      return (nested as Record<string, unknown>).state as string;
    }
  }
  return undefined;
}

function isTerminalOperationState(state: string): boolean {
  return [
    "approved",
    "blocked",
    "candidate_channel",
    "canary",
    "completed",
    "deprecated",
    "published",
    "partial",
    "paused",
    "stable",
    "failed",
    "cancelled",
    "rejected",
    "revoked",
    "dead_letter",
    "rolled_back",
    "verified",
  ].includes(state);
}
