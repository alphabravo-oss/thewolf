import type { OperationReceipt } from "./scanner-supply-chain";
import { safeDisplayText } from "./safe-display";

export interface RememberedOperationReceipt extends OperationReceipt {
  label: string;
  recorded_at: string;
}

const STORAGE_KEY = "wolf.scanner-operation-receipts.v1";
const CHANGE_EVENT = "wolf:scanner-operation-receipts-changed";
const MAX_RECEIPTS = 20;
const SAFE_ID = /^[A-Za-z0-9][A-Za-z0-9._:/-]{0,127}$/;
const SAFE_STATUS_PATH =
  /^\/v1\/(?:scanner-supply-chain|scanners\/custom-builds)\/[A-Za-z0-9._:/-]{1,480}$/;
let cachedSerialized = "";
let cachedReceipts: RememberedOperationReceipt[] = [];

export function rememberOperationReceipt(
  receipt: OperationReceipt,
  label: string,
): OperationReceipt {
  if (typeof window === "undefined" || !SAFE_ID.test(receipt?.id ?? "")) {
    return receipt;
  }
  const statusURL = safeAPIPath(receipt.status_url);
  if (!statusURL) return receipt;
  const remembered: RememberedOperationReceipt = {
    id: receipt.id,
    state: safeState(receipt.state),
    // Command receipts are public API contracts and therefore include the
    // external /api prefix. The browser client already supplies that prefix.
    status_url: statusURL,
    events_url: safeEventsURL(receipt.events_url),
    label: safeLabel(label),
    recorded_at: new Date().toISOString(),
  };
  const current = readOperationReceipts().filter(
    (entry) => entry.id !== remembered.id,
  );
  writeOperationReceipts([remembered, ...current].slice(0, MAX_RECEIPTS));
  return receipt;
}

export function readOperationReceipts(): RememberedOperationReceipt[] {
  if (typeof window === "undefined") return [];
  try {
    const serialized = window.localStorage.getItem(STORAGE_KEY) ?? "[]";
    if (serialized === cachedSerialized) return cachedReceipts;
    const parsed = JSON.parse(serialized);
    cachedSerialized = serialized;
    cachedReceipts = Array.isArray(parsed)
      ? parsed.filter(isRememberedReceipt).slice(0, MAX_RECEIPTS)
      : [];
    return cachedReceipts;
  } catch {
    return cachedReceipts;
  }
}

export function updateOperationReceiptState(id: string, state: string): void {
  if (!SAFE_ID.test(id) || typeof window === "undefined") return;
  const nextState = safeState(state);
  const current = readOperationReceipts();
  if (!current.some((entry) => entry.id === id && entry.state !== nextState)) {
    return;
  }
  writeOperationReceipts(
    current.map((entry) =>
      entry.id === id ? { ...entry, state: nextState } : entry,
    ),
  );
}

export function dismissOperationReceipt(id: string): void {
  if (typeof window === "undefined") return;
  writeOperationReceipts(
    readOperationReceipts().filter((entry) => entry.id !== id),
  );
}

export function subscribeOperationReceipts(callback: () => void): () => void {
  if (typeof window === "undefined") return () => undefined;
  window.addEventListener(CHANGE_EVENT, callback);
  window.addEventListener("storage", callback);
  return () => {
    window.removeEventListener(CHANGE_EVENT, callback);
    window.removeEventListener("storage", callback);
  };
}

function writeOperationReceipts(receipts: RememberedOperationReceipt[]): void {
  try {
    window.localStorage.setItem(STORAGE_KEY, JSON.stringify(receipts));
    window.dispatchEvent(new Event(CHANGE_EVENT));
  } catch {
    // Storage can be unavailable in hardened/private browser contexts. The
    // command itself remains authoritative and must not fail with the UI cache.
  }
}

function isSafeReceipt(receipt: OperationReceipt): boolean {
  return (
    typeof receipt?.id === "string" &&
    SAFE_ID.test(receipt.id) &&
    safeAPIPath(receipt.status_url) === receipt.status_url
  );
}

function isRememberedReceipt(
  value: unknown,
): value is RememberedOperationReceipt {
  if (!value || typeof value !== "object") return false;
  const receipt = value as RememberedOperationReceipt;
  return (
    isSafeReceipt(receipt) &&
    typeof receipt.label === "string" &&
    receipt.label.length <= 96 &&
    typeof receipt.recorded_at === "string" &&
    receipt.recorded_at.length <= 64
  );
}

function safeState(value: string): string {
  return typeof value === "string" && /^[a-z][a-z0-9_]{0,31}$/.test(value)
    ? value
    : "queued";
}

function safeLabel(value: string): string {
  const normalized = safeDisplayText(value, 96)
    .replace(/[^A-Za-z0-9 ._/[\]-]/g, "")
    .trim();
  return normalized.slice(0, 96) || "Scanner operation";
}

function safeEventsURL(value?: string): string | undefined {
  return safeAPIPath(value);
}

function safeAPIPath(value?: string): string | undefined {
  if (typeof value !== "string" || value.length > 512) return undefined;
  const path = value.startsWith("/api/v1/") ? value.slice(4) : value;
  if (
    !SAFE_STATUS_PATH.test(path) ||
    path.includes("..") ||
    path.includes("//") ||
    path.includes("?") ||
    path.includes("#")
  ) {
    return undefined;
  }
  return path;
}
