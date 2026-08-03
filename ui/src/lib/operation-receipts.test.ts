import { beforeEach, describe, expect, it } from "vitest";
import {
  dismissOperationReceipt,
  readOperationReceipts,
  rememberOperationReceipt,
  updateOperationReceiptState,
} from "./operation-receipts";

describe("durable scanner operation receipts", () => {
  beforeEach(() => {
    const values = new Map<string, string>();
    Object.defineProperty(window, "localStorage", {
      configurable: true,
      value: {
        clear: () => values.clear(),
        getItem: (key: string) => values.get(key) ?? null,
        removeItem: (key: string) => values.delete(key),
        setItem: (key: string, value: string) => values.set(key, value),
      },
    });
  });

  it("persists only allowlisted status receipts and survives a fresh read", () => {
    rememberOperationReceipt(
      {
        id: "candidate-123",
        state: "queued",
        status_url: "/api/v1/scanner-supply-chain/candidates/candidate-123",
        events_url:
          "/api/v1/scanner-supply-chain/candidates/candidate-123/events",
      },
      "Candidate create <script> token=dckr_pat_never_render",
    );
    rememberOperationReceipt(
      {
        id: "unsafe",
        state: "queued",
        status_url: "https://attacker.example/collect",
      },
      "Must not persist",
    );
    rememberOperationReceipt(
      {
        id: "unsafe-query",
        state: "queued",
        status_url:
          "/api/v1/scanner-supply-chain/candidates/123?token=never-persist",
      },
      "Must not persist query strings",
    );

    expect(readOperationReceipts()).toEqual([
      expect.objectContaining({
        id: "candidate-123",
        state: "queued",
        status_url: "/v1/scanner-supply-chain/candidates/candidate-123",
        label: "Candidate create script token[REDACTED]",
      }),
    ]);
    expect(
      window.localStorage.getItem("wolf.scanner-operation-receipts.v1"),
    ).not.toContain("attacker.example");
    expect(
      window.localStorage.getItem("wolf.scanner-operation-receipts.v1"),
    ).not.toContain("never-persist");
    expect(
      window.localStorage.getItem("wolf.scanner-operation-receipts.v1"),
    ).not.toContain("dckr_pat_never_render");
  });

  it("updates terminal status and supports explicit dismissal", () => {
    rememberOperationReceipt(
      {
        id: "rollout-1",
        state: "queued",
        status_url: "/v1/scanner-supply-chain/rollouts/rollout-1",
      },
      "Rollout promote",
    );
    updateOperationReceiptState("rollout-1", "completed");
    expect(readOperationReceipts()[0].state).toBe("completed");
    dismissOperationReceipt("rollout-1");
    expect(readOperationReceipts()).toEqual([]);
  });
});
