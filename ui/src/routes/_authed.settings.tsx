// Settings page — General / Secrets / Users / Scanners.
//
// Tab state lives in the URL (?tab=…) so deep links work and refresh
// preserves which section the user was on. Scan presets were deliberately
// omitted — wolf auto-detects per-repo language/framework, no manual
// preset list is needed.
import { createFileRoute, useNavigate, redirect } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  CheckIcon,
  ChevronDownIcon,
  ChevronLeftIcon,
  ChevronRightIcon,
  ChevronUpIcon,
  DownloadIcon,
  KeyIcon,
  KeyRoundIcon,
  Loader2Icon,
  LockIcon,
  PlusIcon,
  SearchIcon,
  RefreshCwIcon,
  ScrollTextIcon,
  ServerIcon,
  SettingsIcon,
  ShieldIcon,
  Trash2Icon,
  WrenchIcon,
  UsersIcon,
} from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { toast } from "sonner";
import { api } from "@/lib/api";
import { GitHubTokenHelp } from "@/components/github-token-help";
import { StatusBadge } from "@/components/ui/status-badge";
import { useMe } from "@/lib/me";
import type {
  AdminSecret,
  ApiToken,
  ApiTokenCreated,
  RemoteNode,
} from "@/lib/types";
import { FixerSettings } from "@/components/fixes/fixer-settings";
import { useRuntimeCapabilities } from "@/lib/runtime-capabilities";
import { safeErrorMessage } from "@/lib/safe-display";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { PageHeader } from "@/components/ui/page";

// Legacy personal ?tab= values that have NO admin tab of the same name now
// redirect to /account. (apikeys/secrets/nodes are kept here as ADMIN tabs —
// the global oversight view — so they are intentionally NOT redirected.)
const LEGACY_PERSONAL: Record<string, "profile" | "security"> = {
  account: "profile",
  security: "security",
};

export const Route = createFileRoute("/_authed/settings")({
  // Accept any string here; beforeLoad does the routing/redirects.
  validateSearch: (s: Record<string, unknown>) => ({
    tab: typeof s.tab === "string" ? s.tab : "general",
  }),
  beforeLoad: async ({ search }) => {
    // Personal surfaces moved to /account.
    const legacy = LEGACY_PERSONAL[(search as { tab?: string }).tab ?? ""];
    if (legacy) {
      throw redirect({ to: "/account", search: { section: legacy } });
    }
    // Settings is admin-only; bounce everyone else to their account.
    const me = await api
      .get<{ role: string }>("/auth/me")
      .then((r) => r.data)
      .catch(() => null);
    if (me?.role !== "admin") {
      throw redirect({ to: "/account", search: { section: "profile" } });
    }
  },
  component: SettingsPage,
});

type TabKey =
  | "general"
  | "fixer"
  | "users"
  | "apikeys"
  | "secrets"
  | "nodes"
  | "scanners"
  | "audit";

// Settings is the admin surface: system config + a global, cross-user
// oversight view of API keys / secrets / nodes, plus the audit log. Personal
// (per-user) management lives under /account.
const TABS: { key: TabKey; label: string; Icon: typeof SettingsIcon }[] = [
  { key: "general", label: "General", Icon: SettingsIcon },
  { key: "fixer", label: "Fixer", Icon: WrenchIcon },
  { key: "users", label: "Users", Icon: UsersIcon },
  { key: "apikeys", label: "API Keys", Icon: KeyRoundIcon },
  { key: "secrets", label: "Secrets", Icon: KeyIcon },
  { key: "nodes", label: "Nodes", Icon: ServerIcon },
  { key: "scanners", label: "Scanners", Icon: ShieldIcon },
  { key: "audit", label: "Audit", Icon: ScrollTextIcon },
];

function SettingsPage() {
  const { tab } = Route.useSearch();
  const navigate = useNavigate();
  const activeTabRef = useRef<HTMLButtonElement>(null);
  const activeTab: TabKey = TABS.some((t) => t.key === tab)
    ? (tab as TabKey)
    : "general";

  useEffect(() => {
    activeTabRef.current?.scrollIntoView({
      block: "nearest",
      inline: "nearest",
    });
  }, [activeTab]);

  return (
    <div className="page stack page--narrow min-w-0">
      {/* PageHeader owns the title/description pair. The previous hand-rolled
          version pulled the description up with `-mt-2` to close the gap that
          `.stack` opens between siblings — but a utility replaces that margin
          rather than trimming it, so the net was -8px and the description
          overlapped the heading. */}
      <PageHeader
        title="Settings"
        description={
          <>
            Administration &amp; global oversight. Manage your own profile,
            keys, and secrets under{" "}
            <button
              type="button"
              onClick={() =>
                navigate({ to: "/account", search: { section: "profile" } })
              }
              className="text-foreground hover:underline"
            >
              Account
            </button>
            .
          </>
        }
      />
      <nav
        aria-label="Administration settings"
        className="max-w-full overflow-x-auto overscroll-x-contain border-b border-border"
      >
        <div className="flex min-w-max gap-1">
          {TABS.map(({ key, label, Icon }) => {
            const active = activeTab === key;
            return (
              <button
                key={key}
                ref={active ? activeTabRef : undefined}
                type="button"
                aria-current={active ? "page" : undefined}
                onClick={() =>
                  navigate({ to: "/settings", search: { tab: key } })
                }
                className={
                  "-mb-px inline-flex h-9 items-center gap-1.5 border-b-2 px-3 text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring " +
                  (active
                    ? "border-primary text-foreground"
                    : "border-transparent text-muted-foreground hover:text-foreground")
                }
              >
                <Icon className="size-4" aria-hidden="true" /> {label}
              </button>
            );
          })}
        </div>
      </nav>

      <section aria-labelledby="active-settings-section" className="min-w-0">
        <h2 id="active-settings-section" className="sr-only">
          {TABS.find((item) => item.key === activeTab)?.label} settings
        </h2>
        {activeTab === "general" && <GeneralTab />}
        {activeTab === "fixer" && <FixerSettings />}
        {activeTab === "users" && <UsersTab />}
        {activeTab === "apikeys" && <AdminApiKeysTab />}
        {activeTab === "secrets" && <AdminSecretsTab />}
        {activeTab === "nodes" && <AdminNodesTab />}
        {activeTab === "scanners" && <ScannersTab />}
        {activeTab === "audit" && <AuditTab />}
      </section>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Admin oversight — global, cross-user views (read + revoke/delete). Secret
// values are never exposed; tokens are hash-only. Owner is resolved from the
// users list (no backend join).
// ---------------------------------------------------------------------------

// useOwnerLookup returns a fn mapping a user_id to that user's email.
function useOwnerLookup() {
  const q = useQuery({
    queryKey: ["users"],
    queryFn: async () =>
      (await api.get<{ id: string; email: string }[]>("/users")).data ?? [],
  });
  const map = new Map((q.data ?? []).map((u) => [u.id, u.email]));
  return (id?: string) => (id ? (map.get(id) ?? `${id.slice(0, 8)}…`) : "—");
}

function AdminCard({ children }: { children: React.ReactNode }) {
  return <section className="glass-card overflow-x-auto">{children}</section>;
}

function AdminApiKeysTab() {
  const qc = useQueryClient();
  const ownerOf = useOwnerLookup();
  const q = useQuery({
    queryKey: ["admin", "tokens"],
    queryFn: async () =>
      (await api.get<ApiToken[]>("/admin/tokens")).data ?? [],
  });
  const [pending, setPending] = useState<{ id: string; label: string } | null>(
    null,
  );
  const revoke = useMutation({
    mutationFn: (id: string) => api.delete(`/auth/tokens/${id}`),
    onSuccess: () => {
      toast.success("Key revoked");
      qc.invalidateQueries({ queryKey: ["admin", "tokens"] });
      setPending(null);
    },
    onError: (e) =>
      toast.error(e instanceof Error ? e.message : "Revoke failed"),
  });
  return (
    <div className="space-y-2">
      <p className="text-xs text-muted-foreground">
        Every user's API keys. Keys are hash-only — names, scopes, and usage are
        visible; the secret itself is never recoverable.
      </p>
      <AdminCard>
        {q.isLoading ? (
          <div className="p-5 text-sm text-muted-foreground">Loading…</div>
        ) : !q.data || q.data.length === 0 ? (
          <div className="p-5 text-sm text-muted-foreground">No API keys.</div>
        ) : (
          <table className="data-table">
            <thead>
              <tr>
                <th className="text-left px-4 py-2">Owner</th>
                <th className="text-left px-4 py-2">Name</th>
                <th className="text-left px-4 py-2">Prefix</th>
                <th className="text-left px-4 py-2">Scopes</th>
                <th className="text-left px-4 py-2">Expires</th>
                <th className="text-right px-4 py-2 w-20"></th>
              </tr>
            </thead>
            <tbody>
              {q.data.map((t) => (
                <tr key={t.id} className="border-t border-border align-top">
                  <td className="px-4 py-2">{ownerOf(t.user_id)}</td>
                  <td className="px-4 py-2">
                    {t.name}
                    {t.revoked_at && (
                      <span className="ml-1 text-[10px] uppercase text-muted-foreground">
                        revoked
                      </span>
                    )}
                  </td>
                  <td className="px-4 py-2 font-mono text-xs text-muted-foreground">
                    {t.token_prefix}…
                  </td>
                  <td className="px-4 py-2">
                    <div className="flex flex-wrap gap-1">
                      {t.scopes.map((s) => (
                        <span
                          key={s}
                          className="text-[10px] font-mono rounded bg-muted/40 border border-border px-1.5 py-0.5"
                        >
                          {s}
                        </span>
                      ))}
                    </div>
                  </td>
                  <td className="px-4 py-2 text-xs text-muted-foreground">
                    {t.expires_at
                      ? new Date(t.expires_at).toLocaleDateString()
                      : "never"}
                  </td>
                  <td className="px-4 py-2 text-right">
                    {!t.revoked_at && (
                      <button
                        type="button"
                        onClick={() =>
                          setPending({
                            id: t.id,
                            label: `${ownerOf(t.user_id)}'s key "${t.name}"`,
                          })
                        }
                        className="inline-flex items-center gap-1 h-7 px-2 rounded-md border border-status-error/40 text-status-error hover:bg-status-error/10 text-xs"
                      >
                        <Trash2Icon className="size-3.5" /> Revoke
                      </button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </AdminCard>
      <ConfirmDialog
        open={!!pending}
        onOpenChange={(o) => !o && setPending(null)}
        title={`Revoke ${pending?.label ?? "API key"}?`}
        description="The key stops working immediately. This cannot be undone."
        confirmLabel="Revoke"
        pending={revoke.isPending}
        onConfirm={() => pending && revoke.mutate(pending.id)}
      />
    </div>
  );
}

function AdminSecretsTab() {
  const qc = useQueryClient();
  const ownerOf = useOwnerLookup();
  const q = useQuery({
    queryKey: ["admin", "secrets"],
    queryFn: async () =>
      (await api.get<AdminSecret[]>("/admin/secrets")).data ?? [],
  });
  const [pending, setPending] = useState<{ id: string; label: string } | null>(
    null,
  );
  const del = useMutation({
    mutationFn: (id: string) => api.delete(`/config/secrets/${id}`),
    onSuccess: () => {
      toast.success("Secret deleted");
      qc.invalidateQueries({ queryKey: ["admin", "secrets"] });
      setPending(null);
    },
    onError: (e) =>
      toast.error(e instanceof Error ? e.message : "Delete failed"),
  });
  return (
    <div className="space-y-2">
      <p className="text-xs text-muted-foreground">
        Every user's secrets. Values are <strong>masked</strong> — you can see a
        secret exists and delete it, but never read another user's value.
      </p>
      <AdminCard>
        {q.isLoading ? (
          <div className="p-5 text-sm text-muted-foreground">Loading…</div>
        ) : !q.data || q.data.length === 0 ? (
          <div className="p-5 text-sm text-muted-foreground">No secrets.</div>
        ) : (
          <table className="data-table">
            <thead>
              <tr>
                <th className="text-left px-4 py-2">Owner</th>
                <th className="text-left px-4 py-2">Type</th>
                <th className="text-left px-4 py-2">Name</th>
                <th className="text-left px-4 py-2">Value</th>
                <th className="text-right px-4 py-2 w-20"></th>
              </tr>
            </thead>
            <tbody>
              {q.data.map((s) => (
                <tr key={s.id} className="border-t border-border">
                  <td className="px-4 py-2">{ownerOf(s.user_id)}</td>
                  <td className="px-4 py-2 font-mono text-xs">{s.key_type}</td>
                  <td className="px-4 py-2">{s.key_name}</td>
                  <td className="px-4 py-2 font-mono text-xs text-muted-foreground">
                    {s.value}
                  </td>
                  <td className="px-4 py-2 text-right">
                    <button
                      type="button"
                      onClick={() =>
                        setPending({
                          id: s.id,
                          label: `${ownerOf(s.user_id)}'s secret "${s.key_name}"`,
                        })
                      }
                      className="inline-flex items-center gap-1 h-7 px-2 rounded-md border border-status-error/40 text-status-error hover:bg-status-error/10 text-xs"
                    >
                      <Trash2Icon className="size-3.5" /> Delete
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </AdminCard>
      <ConfirmDialog
        open={!!pending}
        onOpenChange={(o) => !o && setPending(null)}
        title={`Delete ${pending?.label ?? "secret"}?`}
        description="This cannot be undone."
        confirmLabel="Delete"
        pending={del.isPending}
        onConfirm={() => pending && del.mutate(pending.id)}
      />
    </div>
  );
}

function AdminNodesTab() {
  const qc = useQueryClient();
  const ownerOf = useOwnerLookup();
  const q = useQuery({
    queryKey: ["admin", "nodes"],
    queryFn: async () => (await api.get<RemoteNode[]>("/nodes")).data ?? [],
  });
  const [pending, setPending] = useState<{ id: string; label: string } | null>(
    null,
  );
  const del = useMutation({
    mutationFn: (id: string) => api.delete(`/nodes/${id}`),
    onSuccess: () => {
      toast.success("Node deleted");
      qc.invalidateQueries({ queryKey: ["admin", "nodes"] });
      setPending(null);
    },
    onError: (e) =>
      toast.error(e instanceof Error ? e.message : "Delete failed"),
  });
  return (
    <div className="space-y-2">
      <p className="text-xs text-muted-foreground">Every user's SSH nodes.</p>
      <AdminCard>
        {q.isLoading ? (
          <div className="p-5 text-sm text-muted-foreground">Loading…</div>
        ) : !q.data || q.data.length === 0 ? (
          <div className="p-5 text-sm text-muted-foreground">No nodes.</div>
        ) : (
          <table className="data-table">
            <thead>
              <tr>
                <th className="text-left px-4 py-2">Owner</th>
                <th className="text-left px-4 py-2">Name</th>
                <th className="text-left px-4 py-2">Host</th>
                <th className="text-left px-4 py-2">Enabled</th>
                <th className="text-right px-4 py-2 w-20"></th>
              </tr>
            </thead>
            <tbody>
              {q.data.map((n) => (
                <tr key={n.id} className="border-t border-border">
                  <td className="px-4 py-2">{ownerOf(n.user_id)}</td>
                  <td className="px-4 py-2">{n.name}</td>
                  <td className="px-4 py-2 font-mono text-xs text-muted-foreground">
                    {n.username}@{n.host}:{n.port}
                  </td>
                  <td className="px-4 py-2 text-xs">
                    {n.enabled ? "yes" : "no"}
                  </td>
                  <td className="px-4 py-2 text-right">
                    <button
                      type="button"
                      onClick={() =>
                        setPending({
                          id: n.id,
                          label: `${ownerOf(n.user_id)}'s node "${n.name}"`,
                        })
                      }
                      className="inline-flex items-center gap-1 h-7 px-2 rounded-md border border-status-error/40 text-status-error hover:bg-status-error/10 text-xs"
                    >
                      <Trash2Icon className="size-3.5" /> Delete
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </AdminCard>
      <ConfirmDialog
        open={!!pending}
        onOpenChange={(o) => !o && setPending(null)}
        title={`Delete ${pending?.label ?? "node"}?`}
        description="This cannot be undone."
        confirmLabel="Delete"
        pending={del.isPending}
        onConfirm={() => pending && del.mutate(pending.id)}
      />
    </div>
  );
}

interface AuditEntry {
  id: string;
  user_id: string;
  method: string;
  path: string;
  action: string;
  status_code: number;
  created_at: string;
  event_type?: string;
  category?: string;
  severity?: string;
  ip?: string;
}

const AUDIT_METHODS = ["", "POST", "PUT", "DELETE"];
const AUDIT_CATEGORIES = [
  "",
  "authentication",
  "authorization",
  "configuration",
  "secrets",
  "data",
  "system",
];
const AUDIT_SEVERITIES = ["", "info", "warning", "critical"];
const AUDIT_PER_PAGE = 25;

function SeverityBadge({ severity }: { severity?: string }) {
  if (!severity) return <span className="text-muted-foreground/50">—</span>;
  const cls =
    severity === "critical"
      ? "bg-status-error/15 text-status-error border-status-error/30"
      : severity === "warning"
        ? "bg-status-warning/15 text-status-warning border-status-warning/30"
        : "bg-muted/40 text-muted-foreground border-border";
  return (
    <span
      className={
        "rounded px-1.5 py-0.5 text-[10px] font-medium uppercase tracking-wide border " +
        cls
      }
    >
      {severity}
    </span>
  );
}

function AuditTab() {
  const ownerOf = useOwnerLookup();
  const [search, setSearch] = useState("");
  const [method, setMethod] = useState("");
  const [category, setCategory] = useState("");
  const [severity, setSeverity] = useState("");
  const [sort, setSort] = useState<"time" | "status">("time");
  const [order, setOrder] = useState<"asc" | "desc">("desc");
  const [page, setPage] = useState(1);

  const q = useQuery({
    queryKey: [
      "audit-log",
      search,
      method,
      category,
      severity,
      sort,
      order,
      page,
    ],
    queryFn: async () => {
      const params = new URLSearchParams({
        page: String(page),
        per_page: String(AUDIT_PER_PAGE),
        sort,
        order,
      });
      if (search.trim()) params.set("q", search.trim());
      if (method) params.set("method", method);
      if (category) params.set("category", category);
      if (severity) params.set("severity", severity);
      const res = await api.get<AuditEntry[]>(
        `/audit-log?${params.toString()}`,
      );
      return { entries: res.data ?? [], total: res.meta?.total ?? 0 };
    },
    placeholderData: (prev) => prev, // keep the current page visible while the next loads
  });

  const total = q.data?.total ?? 0;
  const totalPages = Math.max(1, Math.ceil(total / AUDIT_PER_PAGE));
  const entries = q.data?.entries ?? [];

  // Changing a filter resets to page 1.
  const onFilter = (fn: () => void) => {
    fn();
    setPage(1);
  };
  const toggleSort = (col: "time" | "status") =>
    onFilter(() => {
      if (sort === col) setOrder((o) => (o === "desc" ? "asc" : "desc"));
      else {
        setSort(col);
        setOrder("desc");
      }
    });
  const sortMark = (col: "time" | "status") =>
    sort === col ? (
      order === "desc" ? (
        <ChevronDownIcon className="inline size-3" />
      ) : (
        <ChevronUpIcon className="inline size-3" />
      )
    ) : null;

  return (
    <div className="space-y-3">
      <p className="text-xs text-muted-foreground">
        Every mutating request — who did what, where, and the response status.
      </p>

      <div className="flex flex-wrap items-center gap-2">
        <div className="relative flex-1 min-w-[12rem]">
          <SearchIcon className="absolute left-2.5 top-1/2 -translate-y-1/2 size-4 text-muted-foreground" />
          <input
            value={search}
            onChange={(e) => onFilter(() => setSearch(e.target.value))}
            placeholder="Search path, action, or method…"
            className="w-full h-9 pl-8 pr-3 rounded-md bg-muted/40 border border-border text-sm"
          />
        </div>
        <select
          value={category}
          onChange={(e) => onFilter(() => setCategory(e.target.value))}
          className="h-9 px-2 rounded-md bg-muted/40 border border-border text-sm capitalize"
        >
          {AUDIT_CATEGORIES.map((c) => (
            <option key={c} value={c}>
              {c || "All categories"}
            </option>
          ))}
        </select>
        <select
          value={severity}
          onChange={(e) => onFilter(() => setSeverity(e.target.value))}
          className="h-9 px-2 rounded-md bg-muted/40 border border-border text-sm capitalize"
        >
          {AUDIT_SEVERITIES.map((sv) => (
            <option key={sv} value={sv}>
              {sv || "All severities"}
            </option>
          ))}
        </select>
        <select
          value={method}
          onChange={(e) => onFilter(() => setMethod(e.target.value))}
          className="h-9 px-2 rounded-md bg-muted/40 border border-border text-sm"
        >
          {AUDIT_METHODS.map((m) => (
            <option key={m} value={m}>
              {m || "All methods"}
            </option>
          ))}
        </select>
      </div>

      <AdminCard>
        {q.isLoading && !q.data ? (
          <div className="p-5 text-sm text-muted-foreground">Loading…</div>
        ) : entries.length === 0 ? (
          <div className="p-5 text-sm text-muted-foreground">
            No matching audit entries.
          </div>
        ) : (
          <table className="data-table">
            <thead>
              <tr>
                <th className="text-left px-4 py-2">
                  <button
                    type="button"
                    onClick={() => toggleSort("time")}
                    className="hover:text-foreground"
                  >
                    When {sortMark("time")}
                  </button>
                </th>
                <th className="text-left px-4 py-2">Severity</th>
                <th className="text-left px-4 py-2">Event</th>
                <th className="text-left px-4 py-2">Category</th>
                <th className="text-left px-4 py-2">User</th>
                <th className="text-left px-4 py-2">Source</th>
                <th className="text-left px-4 py-2">Request</th>
                <th className="text-right px-4 py-2">
                  <button
                    type="button"
                    onClick={() => toggleSort("status")}
                    className="hover:text-foreground"
                  >
                    Status {sortMark("status")}
                  </button>
                </th>
              </tr>
            </thead>
            <tbody>
              {entries.map((e) => (
                <tr key={e.id} className="border-t border-border align-top">
                  <td className="px-4 py-2 text-xs text-muted-foreground whitespace-nowrap">
                    {new Date(e.created_at).toLocaleString()}
                  </td>
                  <td className="px-4 py-2">
                    <SeverityBadge severity={e.severity} />
                  </td>
                  <td className="px-4 py-2 font-mono text-xs">
                    {e.event_type || e.action}
                  </td>
                  <td className="px-4 py-2 text-xs text-muted-foreground capitalize">
                    {e.category || "—"}
                  </td>
                  <td className="px-4 py-2 text-xs">{ownerOf(e.user_id)}</td>
                  <td className="px-4 py-2 font-mono text-xs text-muted-foreground">
                    {e.ip || "—"}
                  </td>
                  <td className="px-4 py-2 font-mono text-xs text-muted-foreground">
                    {e.method} {e.path}
                  </td>
                  <td
                    className={
                      "px-4 py-2 text-right text-xs tabular-nums " +
                      (e.status_code >= 400
                        ? "text-status-error"
                        : "text-muted-foreground")
                    }
                  >
                    {e.status_code}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </AdminCard>

      <div className="flex items-center justify-between text-xs text-muted-foreground">
        <span>
          {total.toLocaleString()} {total === 1 ? "entry" : "entries"}
        </span>
        <div className="flex items-center gap-2">
          <button
            type="button"
            onClick={() => setPage((p) => Math.max(1, p - 1))}
            disabled={page <= 1}
            className="inline-flex items-center gap-1 h-7 px-2 rounded-md border border-border hover:bg-muted/40 disabled:opacity-40"
          >
            <ChevronLeftIcon className="size-3.5" /> Prev
          </button>
          <span className="tabular-nums">
            Page {page} / {totalPages}
          </span>
          <button
            type="button"
            onClick={() => setPage((p) => Math.min(totalPages, p + 1))}
            disabled={page >= totalPages}
            className="inline-flex items-center gap-1 h-7 px-2 rounded-md border border-border hover:bg-muted/40 disabled:opacity-40"
          >
            Next <ChevronRightIcon className="size-3.5" />
          </button>
        </div>
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Account — per-user profile (display name, email, password) + links to the
// other personal surfaces (API keys, two-factor).
// ---------------------------------------------------------------------------

export function AccountTab() {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const me = useMe();

  const [displayName, setDisplayName] = useState("");
  const [email, setEmail] = useState("");
  const [currentPw, setCurrentPw] = useState("");
  // Seed the form from the loaded user (and re-sync after a saved change).
  useEffect(() => {
    if (me.data) {
      setDisplayName(me.data.display_name ?? "");
      setEmail(me.data.email);
    }
  }, [me.data]);
  const emailChanged =
    !!me.data && email.trim().toLowerCase() !== me.data.email;

  const [curPw, setCurPw] = useState("");
  const [newPw, setNewPw] = useState("");
  const [confirmPw, setConfirmPw] = useState("");

  const profile = useMutation({
    mutationFn: () =>
      api.put("/auth/profile", {
        display_name: displayName,
        email,
        current_password: currentPw,
      }),
    onSuccess: () => {
      toast.success("Profile saved");
      setCurrentPw("");
      qc.invalidateQueries({ queryKey: ["me"] });
    },
    onError: (e) => toast.error(e instanceof Error ? e.message : "Save failed"),
  });
  const password = useMutation({
    mutationFn: () =>
      api.put("/auth/password", {
        current_password: curPw,
        new_password: newPw,
      }),
    onSuccess: () => {
      toast.success("Password updated");
      setCurPw("");
      setNewPw("");
      setConfirmPw("");
    },
    onError: (e) =>
      toast.error(e instanceof Error ? e.message : "Update failed"),
  });

  if (me.isLoading)
    return <p className="text-sm text-muted-foreground">Loading…</p>;
  const pwMismatch = confirmPw.length > 0 && confirmPw !== newPw;

  return (
    <section className="space-y-4 max-w-xl">
      {/* Profile */}
      <div className="glass-card p-5 space-y-4">
        <h3 className="text-sm font-medium">Profile</h3>
        <label className="block space-y-1">
          <span className="text-xs text-muted-foreground">Display name</span>
          <input
            value={displayName}
            onChange={(e) => setDisplayName(e.target.value)}
            placeholder="Shown in the UI instead of your email"
            className="w-full h-9 px-3 rounded-md bg-muted/40 border border-border text-sm"
          />
        </label>
        <label className="block space-y-1">
          <span className="text-xs text-muted-foreground">Email</span>
          <input
            type="email"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            className="w-full h-9 px-3 rounded-md bg-muted/40 border border-border text-sm"
          />
        </label>
        {emailChanged && (
          <label className="block space-y-1">
            <span className="text-xs text-muted-foreground">
              Current password{" "}
              <span className="text-status-warning">(required to change email)</span>
            </span>
            <input
              type="password"
              value={currentPw}
              onChange={(e) => setCurrentPw(e.target.value)}
              autoComplete="current-password"
              className="w-full h-9 px-3 rounded-md bg-muted/40 border border-border text-sm"
            />
          </label>
        )}
        <button
          type="button"
          onClick={() => profile.mutate()}
          disabled={
            profile.isPending ||
            email.trim() === "" ||
            (emailChanged && currentPw.length === 0)
          }
          className="inline-flex items-center gap-1.5 h-9 px-4 rounded-md bg-primary text-primary-foreground text-sm font-medium disabled:opacity-50"
        >
          <CheckIcon className="size-4" />
          {profile.isPending ? "Saving…" : "Save profile"}
        </button>
      </div>

      {/* Password */}
      <div className="glass-card p-5 space-y-4">
        <h3 className="text-sm font-medium">Password</h3>
        <div className="grid sm:grid-cols-3 gap-3">
          <label className="block space-y-1">
            <span className="text-xs text-muted-foreground">Current</span>
            <input
              type="password"
              value={curPw}
              onChange={(e) => setCurPw(e.target.value)}
              autoComplete="current-password"
              className="w-full h-9 px-3 rounded-md bg-muted/40 border border-border text-sm"
            />
          </label>
          <label className="block space-y-1">
            <span className="text-xs text-muted-foreground">New</span>
            <input
              type="password"
              value={newPw}
              onChange={(e) => setNewPw(e.target.value)}
              autoComplete="new-password"
              minLength={12}
              className="w-full h-9 px-3 rounded-md bg-muted/40 border border-border text-sm"
            />
          </label>
          <label className="block space-y-1">
            <span className="text-xs text-muted-foreground">Confirm</span>
            <input
              type="password"
              value={confirmPw}
              onChange={(e) => setConfirmPw(e.target.value)}
              autoComplete="new-password"
              aria-invalid={pwMismatch}
              className={
                "w-full h-9 px-3 rounded-md bg-muted/40 border text-sm " +
                (pwMismatch ? "border-status-error" : "border-border")
              }
            />
          </label>
        </div>
        <p className="text-xs text-muted-foreground">At least 12 characters.</p>
        <button
          type="button"
          onClick={() => password.mutate()}
          disabled={
            password.isPending ||
            curPw.length === 0 ||
            newPw.length < 12 ||
            newPw !== confirmPw
          }
          className="inline-flex items-center gap-1.5 h-9 px-4 rounded-md bg-primary text-primary-foreground text-sm font-medium disabled:opacity-50"
        >
          <CheckIcon className="size-4" />
          {password.isPending ? "Updating…" : "Update password"}
        </button>
      </div>

      {/* Links to the other personal surfaces. */}
      <div className="glass-card p-5 space-y-2">
        <h3 className="text-sm font-medium">More</h3>
        <div className="flex flex-wrap gap-2">
          <button
            type="button"
            onClick={() =>
              navigate({ to: "/settings", search: { tab: "apikeys" } })
            }
            className="inline-flex items-center gap-1.5 h-9 px-3 rounded-md border border-border text-sm hover:bg-muted/40"
          >
            <KeyRoundIcon className="size-4" /> API Keys
          </button>
          <button
            type="button"
            onClick={() =>
              navigate({ to: "/settings", search: { tab: "security" } })
            }
            className="inline-flex items-center gap-1.5 h-9 px-3 rounded-md border border-border text-sm hover:bg-muted/40"
          >
            <LockIcon className="size-4" /> Two-factor auth
          </button>
        </div>
      </div>
    </section>
  );
}

// ---------------------------------------------------------------------------
// General — global toggles backed by GET/PUT /api/settings
// ---------------------------------------------------------------------------

interface SettingRow {
  key: string;
  value: string;
}

// Knobs we expose. Anything else in the settings KV is left untouched.
const GENERAL_KNOBS = [
  {
    key: "ai_enabled",
    label: "AI features",
    alpha: true,
    help: "Master switch for AI-assisted finding enrichment and fix suggestions. When off, scans complete normally but no AI prompts are issued.",
    type: "bool" as const,
  },
  {
    key: "autofix_enabled",
    label: "Autonomous fixing",
    alpha: true,
    help: "Turns on Fixes and Agents. Queue work from a scan or finding. Login lives in Settings → Fixer (OAuth on the fixer worker) or store Anthropic/OpenAI keys in Account → Secrets. Default off.",
    type: "bool" as const,
  },
  {
    key: "registration_enabled",
    label: "Self-service registration",
    help: "When off, new accounts can only be created from the Users tab. The first account can always bootstrap the system.",
    type: "bool" as const,
  },
  {
    key: "mfa_required",
    label: "Require two-factor auth",
    help: "When on, every user must enroll an authenticator app before they can use Wolf. Existing sessions are confined to the Security tab until they enroll, and MFA cannot be self-disabled.",
    type: "bool" as const,
  },
  {
    key: "scan_concurrency",
    label: "Scan concurrency",
    help: "Maximum number of scanner containers run in parallel per scan. Lower this if your host is under-provisioned for memory or CPU.",
    type: "int" as const,
    min: 1,
    max: 32,
  },
  {
    key: "remote_scan_dirty_policy",
    label: "Remote dirty worktrees",
    help: "Default fail blocks SSH scans when uncommitted remote changes would be omitted from git archive. Allow records the dirty state but scans committed content only.",
    type: "choice" as const,
    options: ["fail", "allow"],
  },
];

function GeneralTab() {
  const qc = useQueryClient();
  const q = useQuery({
    queryKey: ["settings"],
    queryFn: async () => {
      const r = await api.get<SettingRow[] | Record<string, string>>(
        "/settings",
      );
      // The endpoint returns either an array of {key,value} or a map.
      // Normalize to a map for the form below.
      const out: Record<string, string> = {};
      if (Array.isArray(r.data)) {
        for (const row of r.data) out[row.key] = row.value;
      } else if (r.data && typeof r.data === "object") {
        for (const [k, v] of Object.entries(r.data)) out[k] = String(v);
      }
      return out;
    },
  });
  const m = useMutation({
    mutationFn: (updates: Record<string, string>) =>
      api.put("/settings", updates),
    onSuccess: () => {
      toast.success("Settings saved");
      qc.invalidateQueries({ queryKey: ["settings"] });
    },
    onError: (e) => toast.error(e instanceof Error ? e.message : "Save failed"),
  });

  if (q.isLoading)
    return <p className="text-sm text-muted-foreground">Loading…</p>;
  const settings = q.data ?? {};

  return (
    <section className="glass-card p-5 space-y-5">
      {GENERAL_KNOBS.map((knob) => {
        const current = settings[knob.key] ?? "";
        return (
          <div
            key={knob.key}
            className="grid md:grid-cols-[1fr_240px] gap-4 items-start"
          >
            <div>
              <label className="text-sm font-medium inline-flex items-center gap-1.5">
                {knob.label}
                {"alpha" in knob && knob.alpha && (
                  <span className="rounded px-1.5 py-0.5 text-3xs font-semibold uppercase tracking-wide bg-status-warning/15 text-status-warning border border-status-warning/30">
                    Alpha
                  </span>
                )}
              </label>
              <p className="text-xs text-muted-foreground mt-0.5">
                {knob.help}
              </p>
            </div>
            {knob.type === "bool" ? (
              <BoolToggle
                value={current === "true"}
                onChange={(v) => m.mutate({ [knob.key]: v ? "true" : "false" })}
                disabled={m.isPending}
              />
            ) : knob.type === "choice" ? (
              <select
                value={current || knob.options[0]}
                onChange={(e) => m.mutate({ [knob.key]: e.target.value })}
                disabled={m.isPending}
                className="w-full h-9 px-2 rounded-md bg-muted/40 border border-border text-sm"
              >
                {knob.options.map((opt) => (
                  <option key={opt} value={opt}>
                    {opt}
                  </option>
                ))}
              </select>
            ) : (
              <IntInput
                value={current}
                min={knob.min}
                max={knob.max}
                onCommit={(v) => m.mutate({ [knob.key]: v })}
                disabled={m.isPending}
              />
            )}
          </div>
        );
      })}
    </section>
  );
}

function BoolToggle({
  value,
  onChange,
  disabled,
}: {
  value: boolean;
  onChange: (v: boolean) => void;
  disabled?: boolean;
}) {
  return (
    <button
      type="button"
      onClick={() => onChange(!value)}
      disabled={disabled}
      className={
        "inline-flex items-center gap-2 px-3 h-9 rounded-md text-sm border " +
        (value
          ? "bg-status-success/10 border-status-success/40 text-status-success"
          : "bg-muted/40 border-border text-muted-foreground hover:text-foreground") +
        " disabled:opacity-50"
      }
    >
      <span
        className={
          "size-2 rounded-full " +
          (value ? "bg-status-success" : "bg-muted-foreground/50")
        }
      />
      {value ? "Enabled" : "Disabled"}
    </button>
  );
}

function IntInput({
  value,
  min,
  max,
  onCommit,
  disabled,
}: {
  value: string;
  min?: number;
  max?: number;
  onCommit: (v: string) => void;
  disabled?: boolean;
}) {
  const [draft, setDraft] = useState(value);
  // Keep draft in sync with server-confirmed value when it changes externally.
  if (value !== draft && document.activeElement?.tagName !== "INPUT") {
    setDraft(value);
  }
  return (
    <div className="flex items-center gap-2">
      <input
        type="number"
        value={draft}
        min={min}
        max={max}
        onChange={(e) => setDraft(e.target.value)}
        className="w-24 h-9 px-2 rounded-md bg-muted/40 border border-border text-sm tabular-nums"
        disabled={disabled}
      />
      <button
        type="button"
        onClick={() => onCommit(draft)}
        disabled={disabled || draft === value}
        className="inline-flex items-center gap-1 h-9 px-3 rounded-md bg-primary text-primary-foreground text-sm disabled:opacity-30"
      >
        <CheckIcon className="size-3.5" />
        Save
      </button>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Security — per-user two-factor authentication (TOTP).
// ---------------------------------------------------------------------------

interface MfaStatus {
  enabled: boolean;
  required: boolean;
}
interface MfaSetup {
  otpauth_uri: string;
  secret: string;
  qr: string; // PNG data URI
}

export function SecurityTab() {
  const qc = useQueryClient();
  const status = useQuery({
    queryKey: ["mfa-status"],
    queryFn: async () => (await api.get<MfaStatus>("/auth/mfa/status")).data,
  });

  // Local enrollment state machine: idle -> enroll (scan QR) -> codes (show
  // recovery codes once).
  const [setup, setSetup] = useState<MfaSetup | null>(null);
  const [code, setCode] = useState("");
  const [recovery, setRecovery] = useState<string[] | null>(null);

  const begin = useMutation({
    mutationFn: async () => (await api.post<MfaSetup>("/auth/mfa/setup")).data,
    onSuccess: (d) => {
      setSetup(d);
      setRecovery(null);
      setCode("");
    },
    onError: (e) =>
      toast.error(e instanceof Error ? e.message : "Could not start setup"),
  });
  const activate = useMutation({
    mutationFn: async () =>
      (
        await api.post<{ recovery_codes: string[] }>("/auth/mfa/activate", {
          code,
        })
      ).data,
    onSuccess: (d) => {
      setRecovery(d?.recovery_codes ?? []);
      setSetup(null);
      setCode("");
      qc.invalidateQueries({ queryKey: ["mfa-status"] });
      toast.success("Two-factor authentication enabled");
    },
    onError: (e) =>
      toast.error(e instanceof Error ? e.message : "That code is not valid"),
  });
  const disable = useMutation({
    mutationFn: async () => api.post("/auth/mfa/disable", { code }),
    onSuccess: () => {
      setCode("");
      qc.invalidateQueries({ queryKey: ["mfa-status"] });
      toast.success("Two-factor authentication disabled");
    },
    onError: (e) =>
      toast.error(e instanceof Error ? e.message : "That code is not valid"),
  });

  if (status.isLoading)
    return <p className="text-sm text-muted-foreground">Loading…</p>;
  const enabled = status.data?.enabled ?? false;
  const required = status.data?.required ?? false;

  return (
    <section className="glass-card p-5 space-y-5 max-w-xl">
      <div className="flex items-start gap-3">
        <LockIcon className="size-5 mt-0.5 text-muted-foreground" />
        <div>
          <h2 className="text-sm font-medium">Two-factor authentication</h2>
          <p className="text-xs text-muted-foreground mt-0.5">
            Protect your account with a time-based code from an authenticator
            app (Google Authenticator, 1Password, Authy, …) in addition to your
            password.
          </p>
        </div>
        <span
          className={
            "ml-auto shrink-0 rounded px-2 py-0.5 text-xs font-medium border " +
            (enabled
              ? "bg-status-success/10 border-status-success/40 text-status-success"
              : "bg-muted/40 border-border text-muted-foreground")
          }
        >
          {enabled ? "On" : "Off"}
        </span>
      </div>

      {/* One-time recovery codes, shown right after activation. */}
      {recovery && (
        <div className="rounded-md border border-status-warning/30 bg-status-warning/5 p-4 space-y-2">
          <p className="text-sm font-medium text-status-warning">
            Save your recovery codes
          </p>
          <p className="text-xs text-muted-foreground">
            Each code works once if you lose your device. Store them somewhere
            safe — they won't be shown again.
          </p>
          <div className="grid grid-cols-2 gap-1.5 font-mono text-sm">
            {recovery.map((c) => (
              <span
                key={c}
                className="rounded bg-muted/40 px-2 py-1 tracking-wider"
              >
                {c}
              </span>
            ))}
          </div>
          <button
            type="button"
            onClick={() => {
              void navigator.clipboard?.writeText(recovery.join("\n"));
              toast.success("Recovery codes copied");
            }}
            className="text-xs text-foreground hover:underline"
          >
            Copy all
          </button>
        </div>
      )}

      {/* Enrollment in progress: QR + verify. */}
      {setup && (
        <div className="space-y-3">
          <p className="text-sm">
            1. Scan this with your authenticator app, then enter the 6-digit
            code to confirm.
          </p>
          <div className="flex items-start gap-4">
            {/* Solid white box with a generous quiet zone — a QR needs a white
                border on all sides to scan, and the dark theme otherwise
                crowds the code's edges. The white background lives on the
                wrapper (not the img) so it's opaque even if the PNG isn't. */}
            <div className="shrink-0 rounded-md bg-white p-4">
              <img
                src={setup.qr}
                alt="TOTP QR code"
                className="block size-48"
                width={192}
                height={192}
              />
            </div>
            <div className="space-y-1 text-xs text-muted-foreground">
              <p>Can't scan? Enter this secret manually:</p>
              <code className="block rounded bg-muted/40 px-2 py-1 font-mono break-all">
                {setup.secret}
              </code>
            </div>
          </div>
          <div className="flex items-center gap-2">
            <input
              inputMode="numeric"
              autoComplete="one-time-code"
              maxLength={6}
              placeholder="123456"
              value={code}
              onChange={(e) => setCode(e.target.value.replace(/\D/g, ""))}
              className="w-32 h-9 px-3 rounded-md bg-muted/40 border border-border text-sm font-mono tracking-widest"
            />
            <button
              type="button"
              onClick={() => activate.mutate()}
              disabled={activate.isPending || code.length < 6}
              className="h-9 px-4 rounded-md bg-primary text-primary-foreground text-sm font-medium disabled:opacity-50"
            >
              {activate.isPending ? "Verifying…" : "Activate"}
            </button>
            <button
              type="button"
              onClick={() => {
                setSetup(null);
                setCode("");
              }}
              className="h-9 px-3 rounded-md text-sm text-muted-foreground hover:text-foreground"
            >
              Cancel
            </button>
          </div>
        </div>
      )}

      {/* Idle states: offer enable, or disable when already on. */}
      {!setup && !enabled && (
        <button
          type="button"
          onClick={() => begin.mutate()}
          disabled={begin.isPending}
          className="h-9 px-4 rounded-md bg-primary text-primary-foreground text-sm font-medium disabled:opacity-50"
        >
          {begin.isPending ? "Starting…" : "Set up two-factor authentication"}
        </button>
      )}

      {!setup && enabled && (
        <div className="space-y-2">
          {required ? (
            <p className="text-xs text-muted-foreground">
              Your administrator requires two-factor authentication, so it can't
              be turned off.
            </p>
          ) : (
            <div className="flex items-center gap-2">
              <input
                inputMode="numeric"
                autoComplete="one-time-code"
                maxLength={9}
                placeholder="code to disable"
                value={code}
                onChange={(e) => setCode(e.target.value.trim())}
                className="w-44 h-9 px-3 rounded-md bg-muted/40 border border-border text-sm font-mono"
              />
              <button
                type="button"
                onClick={() => disable.mutate()}
                disabled={disable.isPending || code.length < 6}
                className="h-9 px-4 rounded-md border border-status-error/40 text-status-error hover:bg-status-error/10 text-sm disabled:opacity-50"
              >
                {disable.isPending ? "Disabling…" : "Disable"}
              </button>
            </div>
          )}
        </div>
      )}
    </section>
  );
}

// ---------------------------------------------------------------------------
// API Keys — per-user, scoped tokens for the CLI / CI / agents. The secret is
// shown exactly once at creation. These bypass MFA by design (they are minted
// from an already-authenticated session and are individually revocable).
// ---------------------------------------------------------------------------

const API_SCOPES = [
  "read:repos",
  "write:repos",
  "read:scans",
  "write:scans",
  "read:findings",
  "write:findings",
  "read:fixes",
  "write:fixes",

  "read:config",
  "write:config",
  "admin",
] as const;

// Role presets map to the scope aliases the backend (apikey.ParseScopes) knows.
const ROLE_PRESETS = [
  {
    key: "read-only",
    label: "Read-only",
    help: "Read every resource; no writes.",
  },
  {
    key: "read-write",
    label: "Read & write",
    help: "Read and write everything except admin.",
  },
  {
    key: "admin",
    label: "Admin (full)",
    help: "Full access, including settings and users.",
  },
  { key: "custom", label: "Custom", help: "Pick exact scopes." },
] as const;

const EXPIRY_OPTIONS = [
  { days: 30, label: "30 days" },
  { days: 90, label: "90 days" },
  { days: 365, label: "1 year" },
  { days: 0, label: "Never" },
];

export function ApiKeysTab() {
  const qc = useQueryClient();
  const q = useQuery({
    queryKey: ["api-tokens"],
    queryFn: async () => (await api.get<ApiToken[]>("/auth/tokens")).data ?? [],
  });

  const [name, setName] = useState("");
  const [role, setRole] =
    useState<(typeof ROLE_PRESETS)[number]["key"]>("read-only");
  const [customScopes, setCustomScopes] = useState<string[]>(["read:scans"]);
  const [expiryDays, setExpiryDays] = useState(90);
  const [created, setCreated] = useState<ApiTokenCreated | null>(null);

  const create = useMutation({
    mutationFn: async () => {
      const scopes = role === "custom" ? customScopes : [role];
      return (
        await api.post<ApiTokenCreated>("/auth/tokens", {
          name,
          scopes,
          expires_in_days: expiryDays,
        })
      ).data;
    },
    onSuccess: (d) => {
      setCreated(d ?? null);
      setName("");
      qc.invalidateQueries({ queryKey: ["api-tokens"] });
    },
    onError: (e) =>
      toast.error(e instanceof Error ? e.message : "Could not create key"),
  });
  const [pending, setPending] = useState<{ id: string; label: string } | null>(
    null,
  );
  const revoke = useMutation({
    mutationFn: (id: string) => api.delete(`/auth/tokens/${id}`),
    onSuccess: () => {
      toast.success("Key revoked");
      qc.invalidateQueries({ queryKey: ["api-tokens"] });
      setPending(null);
    },
    onError: (e) =>
      toast.error(e instanceof Error ? e.message : "Revoke failed"),
  });

  const toggleScope = (s: string) =>
    setCustomScopes((prev) =>
      prev.includes(s) ? prev.filter((x) => x !== s) : [...prev, s],
    );

  const canCreate =
    name.trim().length > 0 &&
    (role !== "custom" || customScopes.length > 0) &&
    !create.isPending;

  const origin =
    typeof window !== "undefined"
      ? window.location.origin
      : "https://wolf.example.com";

  return (
    <section className="space-y-4 max-w-2xl">
      <p className="text-sm text-muted-foreground">
        API keys are scoped, revocable credentials for the{" "}
        <code className="text-foreground">wolf</code> CLI, CI pipelines, and
        agents. They <strong>bypass two-factor auth</strong> by design, so treat
        them like passwords. Browse the full API at{" "}
        <a
          href="/api/v1/docs"
          target="_blank"
          rel="noreferrer"
          className="text-foreground hover:underline"
        >
          /api/v1/docs
        </a>
        .
      </p>

      {/* One-time secret reveal. */}
      {created && (
        <div className="rounded-md border border-status-success/30 bg-status-success/5 p-4 space-y-3">
          <p className="text-sm font-medium text-status-success">
            Key created — copy it now, it won't be shown again
          </p>
          <div className="flex items-center gap-2">
            <code className="flex-1 path break-all rounded bg-muted/50 px-2 py-1.5 text-sm">
              {created.token}
            </code>
            <button
              type="button"
              onClick={() => {
                void navigator.clipboard?.writeText(created.token);
                toast.success("Key copied");
              }}
              className="h-8 px-3 rounded-md bg-primary text-primary-foreground text-xs font-medium"
            >
              Copy
            </button>
          </div>
          <div className="space-y-1">
            <p className="text-xs text-muted-foreground">
              Use it with the CLI:
            </p>
            <pre className="path text-xs rounded bg-muted/50 p-2 overflow-x-auto">
              {`export WOLF_SERVER=${origin}\nexport WOLF_TOKEN=${created.token}\nwolf scans list`}
            </pre>
          </div>
          <button
            type="button"
            onClick={() => setCreated(null)}
            className="text-xs text-muted-foreground hover:text-foreground"
          >
            Done
          </button>
        </div>
      )}

      {/* Create form. */}
      <div className="glass-card p-5 space-y-4">
        <h3 className="text-sm font-medium">Create a key</h3>
        <div className="grid sm:grid-cols-2 gap-3">
          <label className="block space-y-1">
            <span className="text-xs text-muted-foreground">Name</span>
            <input
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="ci-pipeline"
              className="w-full h-9 px-3 rounded-md bg-muted/40 border border-border text-sm"
            />
          </label>
          <label className="block space-y-1">
            <span className="text-xs text-muted-foreground">Expires</span>
            <select
              value={expiryDays}
              onChange={(e) => setExpiryDays(Number(e.target.value))}
              className="w-full h-9 px-2 rounded-md bg-muted/40 border border-border text-sm"
            >
              {EXPIRY_OPTIONS.map((o) => (
                <option key={o.days} value={o.days}>
                  {o.label}
                </option>
              ))}
            </select>
          </label>
        </div>

        <div className="space-y-1.5">
          <span className="text-xs text-muted-foreground">Access</span>
          <div className="grid sm:grid-cols-2 gap-1.5">
            {ROLE_PRESETS.map((p) => (
              <button
                key={p.key}
                type="button"
                onClick={() => setRole(p.key)}
                className={
                  "text-left rounded-md border px-3 py-2 text-sm " +
                  (role === p.key
                    ? "border-primary bg-primary/10"
                    : "border-border hover:border-border")
                }
              >
                <div className="font-medium">{p.label}</div>
                <div className="text-xs text-muted-foreground">{p.help}</div>
              </button>
            ))}
          </div>
        </div>

        {role === "custom" && (
          <div className="grid grid-cols-2 sm:grid-cols-3 gap-1.5">
            {API_SCOPES.map((s) => (
              <label
                key={s}
                className="inline-flex items-center gap-1.5 text-xs cursor-pointer select-none"
              >
                <input
                  type="checkbox"
                  checked={customScopes.includes(s)}
                  onChange={() => toggleScope(s)}
                  className="size-3.5 accent-primary"
                />
                <span
                  className={
                    s === "admin" ? "text-status-warning font-mono" : "font-mono"
                  }
                >
                  {s}
                </span>
              </label>
            ))}
          </div>
        )}

        <button
          type="button"
          onClick={() => create.mutate()}
          disabled={!canCreate}
          className="inline-flex items-center gap-1.5 h-9 px-4 rounded-md bg-primary text-primary-foreground text-sm font-medium disabled:opacity-50"
        >
          <PlusIcon className="size-4" />
          {create.isPending ? "Creating…" : "Create key"}
        </button>
      </div>

      {/* Existing keys. */}
      <div className="glass-card overflow-hidden">
        {q.isLoading ? (
          <div className="p-5 text-sm text-muted-foreground">Loading…</div>
        ) : !q.data || q.data.length === 0 ? (
          <div className="p-5 text-sm text-muted-foreground">
            No API keys yet.
          </div>
        ) : (
          <table className="data-table">
            <thead>
              <tr>
                <th className="text-left px-4 py-2">Name</th>
                <th className="text-left px-4 py-2">Prefix</th>
                <th className="text-left px-4 py-2">Scopes</th>
                <th className="text-left px-4 py-2">Expires</th>
                <th className="text-right px-4 py-2 w-20"></th>
              </tr>
            </thead>
            <tbody>
              {q.data.map((t) => {
                const revoked = !!t.revoked_at;
                const expired =
                  !!t.expires_at && new Date(t.expires_at) < new Date();
                return (
                  <tr
                    key={t.id}
                    className="border-t border-border align-top"
                  >
                    <td className="px-4 py-2">
                      <div className="font-medium">{t.name}</div>
                      {(revoked || expired) && (
                        <span className="text-[10px] uppercase tracking-wide text-muted-foreground">
                          {revoked ? "revoked" : "expired"}
                        </span>
                      )}
                    </td>
                    <td className="px-4 py-2 font-mono text-xs text-muted-foreground">
                      {t.token_prefix}…
                    </td>
                    <td className="px-4 py-2">
                      <div className="flex flex-wrap gap-1">
                        {t.scopes.map((s) => (
                          <span
                            key={s}
                            className="text-[10px] font-mono rounded bg-muted/40 border border-border px-1.5 py-0.5"
                          >
                            {s}
                          </span>
                        ))}
                      </div>
                    </td>
                    <td className="px-4 py-2 text-xs text-muted-foreground">
                      {t.expires_at
                        ? new Date(t.expires_at).toLocaleDateString()
                        : "never"}
                    </td>
                    <td className="px-4 py-2 text-right">
                      {!revoked && (
                        <button
                          type="button"
                          onClick={() =>
                            setPending({ id: t.id, label: t.name })
                          }
                          className="inline-flex items-center gap-1 h-7 px-2 rounded-md border border-status-error/40 text-status-error hover:bg-status-error/10 text-xs"
                        >
                          <Trash2Icon className="size-3.5" /> Revoke
                        </button>
                      )}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        )}
      </div>
      <ConfirmDialog
        open={!!pending}
        onOpenChange={(o) => !o && setPending(null)}
        title={`Revoke API key “${pending?.label ?? ""}”?`}
        description="The key stops working immediately. This cannot be undone."
        confirmLabel="Revoke"
        pending={revoke.isPending}
        onConfirm={() => pending && revoke.mutate(pending.id)}
      />
    </section>
  );
}

// ---------------------------------------------------------------------------
// Secrets — API keys and git tokens. Values are encrypted server-side; the
// list endpoint returns them masked (last 4 chars only).
// ---------------------------------------------------------------------------

interface MaskedSecret {
  id: string;
  key_type: string;
  key_name: string;
  value: string; // masked
  metadata?: { login?: string; scopes?: string[]; validated?: boolean };
  created_at: string;
}

const KEY_TYPES = [
  { value: "github_token", label: "GitHub token" },
  { value: "gitlab_token", label: "GitLab token" },
  { value: "ssh_private_key", label: "SSH private key" },
  { value: "ssh_password", label: "SSH password" },
  { value: "anthropic_key", label: "Anthropic API key" },
  { value: "openai_key", label: "OpenAI API key" },
  { value: "xai_key", label: "xAI / Grok API key" },
  { value: "custom", label: "Custom" },
];

export function SecretsTab() {
  const qc = useQueryClient();
  const q = useQuery({
    queryKey: ["secrets"],
    queryFn: async () => {
      const r = await api.get<MaskedSecret[]>("/config/secrets");
      return r.data ?? [];
    },
  });
  const [pending, setPending] = useState<{ id: string; label: string } | null>(
    null,
  );
  const del = useMutation({
    mutationFn: (id: string) => api.delete(`/config/secrets/${id}`),
    onSuccess: () => {
      toast.success("Secret deleted");
      qc.invalidateQueries({ queryKey: ["secrets"] });
      setPending(null);
    },
    onError: (e) =>
      toast.error(e instanceof Error ? e.message : "Delete failed"),
  });
  const create = useMutation({
    mutationFn: (body: { key_type: string; key_name: string; value: string }) =>
      api.post("/config/secrets", body),
    onSuccess: () => {
      toast.success("Secret added");
      qc.invalidateQueries({ queryKey: ["secrets"] });
    },
    onError: (e) =>
      toast.error(e instanceof Error ? e.message : "Create failed"),
  });

  return (
    <section className="space-y-4">
      <p className="text-sm text-muted-foreground">
        Git tokens, SSH material, and model keys (Anthropic / OpenAI) for the
        fixer. Values are encrypted; only a mask is shown after save.
      </p>
      <NewSecretForm
        onSubmit={(b) => create.mutate(b)}
        disabled={create.isPending}
      />
      <div className="glass-card overflow-hidden">
        {q.isLoading ? (
          <div className="p-5 text-sm text-muted-foreground">Loading…</div>
        ) : !q.data || q.data.length === 0 ? (
          <div className="p-5 text-sm text-muted-foreground">
            No secrets stored.
          </div>
        ) : (
          <table className="data-table">
            <thead>
              <tr>
                <th className="text-left px-4 py-2">Type</th>
                <th className="text-left px-4 py-2">Name</th>
                <th className="text-left px-4 py-2">Value</th>
                <th className="text-right px-4 py-2 w-12"></th>
              </tr>
            </thead>
            <tbody>
              {q.data.map((s) => (
                <tr key={s.id} className="border-t border-border">
                  <td className="px-4 py-2 font-mono text-xs">{s.key_type}</td>
                  <td className="px-4 py-2">
                    {s.key_name}
                    {s.metadata?.login ? (
                      <div className="text-[11px] text-muted-foreground">
                        {s.metadata.login}
                        {s.metadata.scopes?.length
                          ? ` · ${s.metadata.scopes.join(", ")}`
                          : ""}
                      </div>
                    ) : null}
                  </td>
                  <td className="px-4 py-2 font-mono text-xs text-muted-foreground">
                    {s.value}
                  </td>
                  <td className="px-4 py-2 text-right">
                    <button
                      type="button"
                      onClick={() =>
                        setPending({ id: s.id, label: s.key_name })
                      }
                      disabled={del.isPending}
                      className="text-muted-foreground hover:text-destructive disabled:opacity-50"
                      aria-label="Delete"
                    >
                      <Trash2Icon className="size-4" />
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
      <ConfirmDialog
        open={!!pending}
        onOpenChange={(o) => !o && setPending(null)}
        title={`Delete secret “${pending?.label ?? ""}”?`}
        description="This cannot be undone."
        confirmLabel="Delete"
        pending={del.isPending}
        onConfirm={() => pending && del.mutate(pending.id)}
      />
    </section>
  );
}

function NewSecretForm({
  onSubmit,
  disabled,
}: {
  onSubmit: (b: { key_type: string; key_name: string; value: string }) => void;
  disabled?: boolean;
}) {
  const [keyType, setKeyType] = useState("github_token");
  const [keyName, setKeyName] = useState("");
  const [value, setValue] = useState("");
  return (
    <form
      className="glass-card p-4 grid md:grid-cols-[200px_1fr_1fr_auto] gap-2 items-end"
      onSubmit={(e) => {
        e.preventDefault();
        if (!keyName.trim() || !value) return;
        onSubmit({ key_type: keyType, key_name: keyName.trim(), value });
        setKeyName("");
        setValue("");
      }}
    >
      <Field label="Type">
        <select
          value={keyType}
          onChange={(e) => setKeyType(e.target.value)}
          className="w-full h-9 px-2 rounded-md bg-muted/40 border border-border text-sm"
        >
          {KEY_TYPES.map((k) => (
            <option key={k.value} value={k.value}>
              {k.label}
            </option>
          ))}
        </select>
      </Field>
      <Field label="Name">
        <input
          required
          value={keyName}
          onChange={(e) => setKeyName(e.target.value)}
          placeholder="e.g. github-personal"
          className="w-full h-9 px-2 rounded-md bg-muted/40 border border-border text-sm"
        />
      </Field>
      <Field label="Value">
        <input
          required
          type="password"
          value={value}
          onChange={(e) => setValue(e.target.value)}
          placeholder="paste secret"
          className="w-full h-9 px-2 rounded-md bg-muted/40 border border-border text-sm font-mono"
        />
      </Field>
      <button
        type="submit"
        disabled={disabled || !keyName.trim() || !value}
        className="inline-flex items-center gap-1 h-9 px-3 rounded-md bg-primary text-primary-foreground text-sm font-medium disabled:opacity-50"
      >
        <PlusIcon className="size-4" /> Add
      </button>
      {keyType === "github_token" && (
        <div className="md:col-span-4 mt-1">
          <GitHubTokenHelp />
        </div>
      )}
    </form>
  );
}

function Field({
  label,
  children,
}: {
  label: string;
  children: React.ReactNode;
}) {
  return (
    <label className="block">
      <span className="block text-xs text-muted-foreground mb-1">{label}</span>
      {children}
    </label>
  );
}

// ---------------------------------------------------------------------------
// Nodes — remote Linux hosts scanned over SSH. Credentials are encrypted
// secrets; node records only reference the selected secret.
// ---------------------------------------------------------------------------

export function NodesTab() {
  const qc = useQueryClient();
  const nodes = useQuery({
    queryKey: ["remote-nodes"],
    queryFn: async () => {
      const r = await api.get<RemoteNode[]>("/nodes");
      return r.data ?? [];
    },
  });
  const secrets = useQuery({
    queryKey: ["secrets"],
    queryFn: async () => {
      const r = await api.get<MaskedSecret[]>("/config/secrets");
      return r.data ?? [];
    },
  });
  const create = useMutation({
    mutationFn: (body: Record<string, unknown>) => api.post("/nodes", body),
    onSuccess: () => {
      toast.success("Node added");
      qc.invalidateQueries({ queryKey: ["remote-nodes"] });
    },
    onError: (e) =>
      toast.error(e instanceof Error ? e.message : "Create failed"),
  });
  const update = useMutation({
    mutationFn: ({ id, body }: { id: string; body: Record<string, unknown> }) =>
      api.put(`/nodes/${id}`, body),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["remote-nodes"] });
    },
    onError: (e) =>
      toast.error(e instanceof Error ? e.message : "Update failed"),
  });
  const check = useMutation({
    mutationFn: (id: string) => api.post(`/nodes/${id}/check`),
    onSuccess: () => {
      toast.success("Node check passed");
      qc.invalidateQueries({ queryKey: ["remote-nodes"] });
    },
    onError: (e) => {
      toast.error(e instanceof Error ? e.message : "Node check failed");
      qc.invalidateQueries({ queryKey: ["remote-nodes"] });
    },
  });
  const [pending, setPending] = useState<{ id: string; label: string } | null>(
    null,
  );
  const del = useMutation({
    mutationFn: (id: string) => api.delete(`/nodes/${id}`),
    onSuccess: () => {
      toast.success("Node deleted");
      qc.invalidateQueries({ queryKey: ["remote-nodes"] });
      setPending(null);
    },
    onError: (e) =>
      toast.error(e instanceof Error ? e.message : "Delete failed"),
  });

  return (
    <section className="space-y-4">
      <NewNodeForm
        secrets={secrets.data ?? []}
        disabled={create.isPending}
        onSubmit={(body) => create.mutate(body)}
      />
      <div className="glass-card overflow-hidden">
        {nodes.isLoading ? (
          <div className="p-5 text-sm text-muted-foreground">Loading…</div>
        ) : !nodes.data || nodes.data.length === 0 ? (
          <div className="p-5 text-sm text-muted-foreground">
            No remote nodes configured.
          </div>
        ) : (
          <table className="data-table">
            <thead>
              <tr>
                <th className="text-left px-4 py-2">Name</th>
                <th className="text-left px-4 py-2">Host</th>
                <th className="text-left px-4 py-2">Auth</th>
                <th className="text-left px-4 py-2">Status</th>
                <th className="text-right px-4 py-2 w-28"></th>
              </tr>
            </thead>
            <tbody>
              {nodes.data.map((n) => (
                <tr key={n.id} className="border-t border-border">
                  <td className="px-4 py-2">
                    <div className="font-medium">{n.name}</div>
                    {n.base_path && (
                      <div className="text-xs text-muted-foreground font-mono">
                        {n.base_path}
                      </div>
                    )}
                  </td>
                  <td className="px-4 py-2 font-mono text-xs">
                    {n.username}@{n.host}:{n.port || 22}
                  </td>
                  <td className="px-4 py-2 text-xs">{n.auth_type}</td>
                  <td className="px-4 py-2 text-xs">
                    <span
                      className={
                        n.enabled ? "text-status-success" : "text-muted-foreground"
                      }
                    >
                      {n.enabled ? "enabled" : "disabled"}
                    </span>
                    {n.last_check_status && (
                      <span className="text-muted-foreground">
                        {" "}
                        · {n.last_check_status}
                      </span>
                    )}
                    {n.last_check_error && (
                      <div
                        className="text-destructive truncate max-w-[240px]"
                        title={n.last_check_error}
                      >
                        {n.last_check_error}
                      </div>
                    )}
                  </td>
                  <td className="px-4 py-2">
                    <div className="flex justify-end gap-2">
                      <button
                        type="button"
                        onClick={() => check.mutate(n.id)}
                        disabled={check.isPending}
                        className="size-8 grid place-items-center rounded-md hover:bg-muted/50 disabled:opacity-50"
                        aria-label="Check node"
                        title="Check node"
                      >
                        {check.isPending ? (
                          <Loader2Icon className="size-4 animate-spin" />
                        ) : (
                          <CheckIcon className="size-4" />
                        )}
                      </button>
                      <button
                        type="button"
                        onClick={() =>
                          update.mutate({
                            id: n.id,
                            body: { enabled: !n.enabled },
                          })
                        }
                        disabled={update.isPending}
                        className="h-8 px-2 rounded-md border border-border text-xs hover:bg-muted/40 disabled:opacity-50"
                      >
                        {n.enabled ? "Disable" : "Enable"}
                      </button>
                      <button
                        type="button"
                        onClick={() => setPending({ id: n.id, label: n.name })}
                        disabled={del.isPending}
                        className="size-8 grid place-items-center rounded-md hover:bg-destructive/10 text-destructive disabled:opacity-50"
                        aria-label="Delete node"
                      >
                        <Trash2Icon className="size-4" />
                      </button>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
      <ConfirmDialog
        open={!!pending}
        onOpenChange={(o) => !o && setPending(null)}
        title={`Delete node “${pending?.label ?? ""}”?`}
        description="Repos using this node must be removed first."
        confirmLabel="Delete"
        pending={del.isPending}
        onConfirm={() => pending && del.mutate(pending.id)}
      />
    </section>
  );
}

function NewNodeForm({
  secrets,
  disabled,
  onSubmit,
}: {
  secrets: MaskedSecret[];
  disabled?: boolean;
  onSubmit: (body: Record<string, unknown>) => void;
}) {
  const [name, setName] = useState("");
  const [host, setHost] = useState("");
  const [port, setPort] = useState("22");
  const [username, setUsername] = useState("");
  const [authType, setAuthType] = useState<"private_key" | "password">(
    "private_key",
  );
  const [secretId, setSecretId] = useState("");
  const [knownHosts, setKnownHosts] = useState("");
  const [basePath, setBasePath] = useState("");
  const allowedSecrets = secrets.filter((s) =>
    authType === "private_key"
      ? s.key_type === "ssh_private_key"
      : s.key_type === "ssh_password",
  );

  return (
    <form
      className="glass-card p-4 space-y-3"
      onSubmit={(e) => {
        e.preventDefault();
        if (!name.trim() || !host.trim() || !username.trim()) return;
        onSubmit({
          name: name.trim(),
          host: host.trim(),
          port: Number.parseInt(port, 10) || 22,
          username: username.trim(),
          auth_type: authType,
          credential_secret_id: secretId || undefined,
          known_hosts: knownHosts.trim(),
          base_path: basePath.trim(),
          enabled: true,
        });
        setName("");
        setHost("");
        setUsername("");
        setSecretId("");
        setKnownHosts("");
        setBasePath("");
      }}
    >
      <div className="grid md:grid-cols-[1fr_1fr_90px_1fr] gap-2">
        <Field label="Name">
          <input
            required
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="dev-box"
            className="w-full h-9 px-2 rounded-md bg-muted/40 border border-border text-sm"
          />
        </Field>
        <Field label="Host">
          <input
            required
            value={host}
            onChange={(e) => setHost(e.target.value)}
            placeholder="dev.example.com"
            className="w-full h-9 px-2 rounded-md bg-muted/40 border border-border text-sm font-mono"
          />
        </Field>
        <Field label="Port">
          <input
            required
            value={port}
            onChange={(e) => setPort(e.target.value)}
            inputMode="numeric"
            className="w-full h-9 px-2 rounded-md bg-muted/40 border border-border text-sm"
          />
        </Field>
        <Field label="Username">
          <input
            required
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            placeholder="alice"
            className="w-full h-9 px-2 rounded-md bg-muted/40 border border-border text-sm"
          />
        </Field>
      </div>
      <div className="grid md:grid-cols-[180px_1fr_1fr_auto] gap-2 items-end">
        <Field label="Auth">
          <select
            value={authType}
            onChange={(e) => {
              setAuthType(e.target.value as "private_key" | "password");
              setSecretId("");
            }}
            className="w-full h-9 px-2 rounded-md bg-muted/40 border border-border text-sm"
          >
            <option value="private_key">Private key</option>
            <option value="password">Password</option>
          </select>
        </Field>
        <Field label="Credential secret">
          <select
            value={secretId}
            onChange={(e) => setSecretId(e.target.value)}
            className="w-full h-9 px-2 rounded-md bg-muted/40 border border-border text-sm"
          >
            <option value="">Select secret…</option>
            {allowedSecrets.map((s) => (
              <option key={s.id} value={s.id}>
                {s.key_name}
              </option>
            ))}
          </select>
        </Field>
        <Field label="Base path">
          <input
            value={basePath}
            onChange={(e) => setBasePath(e.target.value)}
            placeholder="/home/alice/code"
            className="w-full h-9 px-2 rounded-md bg-muted/40 border border-border text-sm font-mono"
          />
        </Field>
        <button
          type="submit"
          disabled={
            disabled ||
            !secretId ||
            !name.trim() ||
            !host.trim() ||
            !username.trim()
          }
          className="inline-flex items-center gap-1 h-9 px-3 rounded-md bg-primary text-primary-foreground text-sm font-medium disabled:opacity-50"
        >
          <PlusIcon className="size-4" /> Add
        </button>
      </div>
      <Field label="Known hosts">
        <textarea
          value={knownHosts}
          onChange={(e) => setKnownHosts(e.target.value)}
          placeholder="dev.example.com ssh-ed25519 AAAA…"
          className="w-full min-h-20 px-2 py-2 rounded-md bg-muted/40 border border-border text-sm font-mono"
        />
      </Field>
    </form>
  );
}

// ---------------------------------------------------------------------------
// Users — list, create (admin flavor), delete. Self-delete blocked
// server-side; the UI also hides the delete button on the current user's
// row as a safety belt.
// ---------------------------------------------------------------------------

interface UserSummary {
  id: string;
  email: string;
  role: string;
  created_at: string;
  updated_at: string;
}

function UsersTab() {
  const qc = useQueryClient();
  const meQ = useQuery({
    queryKey: ["me"],
    queryFn: async () => (await api.get<{ id: string }>("/auth/me")).data,
  });
  const q = useQuery({
    queryKey: ["users"],
    queryFn: async () => (await api.get<UserSummary[]>("/users")).data ?? [],
  });
  const create = useMutation({
    mutationFn: (body: { email: string; password: string; role: string }) =>
      api.post("/users", body),
    onSuccess: () => {
      toast.success("User created");
      qc.invalidateQueries({ queryKey: ["users"] });
    },
    onError: (e) =>
      toast.error(e instanceof Error ? e.message : "Create failed"),
  });
  const setRole = useMutation({
    mutationFn: ({ id, role }: { id: string; role: string }) =>
      api.put(`/users/${id}/role`, { role }),
    onSuccess: () => {
      toast.success("Role updated");
      qc.invalidateQueries({ queryKey: ["users"] });
    },
    onError: (e) =>
      toast.error(e instanceof Error ? e.message : "Role change failed"),
  });
  const [pending, setPending] = useState<{ id: string; label: string } | null>(
    null,
  );
  const del = useMutation({
    mutationFn: (id: string) => api.delete(`/users/${id}`),
    onSuccess: () => {
      toast.success("User deleted");
      qc.invalidateQueries({ queryKey: ["users"] });
      setPending(null);
    },
    onError: (e) =>
      toast.error(e instanceof Error ? e.message : "Delete failed"),
  });
  const meId = meQ.data?.id;
  const adminCount = (q.data ?? []).filter((u) => u.role === "admin").length;
  return (
    <section className="space-y-4">
      <NewUserForm
        onSubmit={(b) => create.mutate(b)}
        disabled={create.isPending}
      />
      <div className="glass-card overflow-hidden">
        {q.isLoading ? (
          <div className="p-5 text-sm text-muted-foreground">Loading…</div>
        ) : (
          <table className="data-table">
            <thead>
              <tr>
                <th className="text-left px-4 py-2">Email</th>
                <th className="text-left px-4 py-2">Role</th>
                <th className="text-left px-4 py-2">Created</th>
                <th className="text-right px-4 py-2 w-12"></th>
              </tr>
            </thead>
            <tbody>
              {(q.data ?? []).map((u) => {
                const isMe = u.id === meId;
                const isAdmin = u.role === "admin";
                // Don't let the last admin be demoted (would lock everyone out
                // of settings). You also can't change your own role.
                const lockDemote = isMe || (isAdmin && adminCount <= 1);
                return (
                  <tr key={u.id} className="border-t border-border">
                    <td className="px-4 py-2">
                      {u.email}
                      {isMe && (
                        <span className="ml-2 text-[10px] uppercase tracking-wide text-muted-foreground">
                          you
                        </span>
                      )}
                    </td>
                    <td className="px-4 py-2">
                      {isMe ? (
                        <RoleBadge role={u.role} />
                      ) : (
                        <select
                          value={isAdmin ? "admin" : "user"}
                          disabled={setRole.isPending || lockDemote}
                          onChange={(e) =>
                            setRole.mutate({ id: u.id, role: e.target.value })
                          }
                          className="h-7 px-1.5 rounded-md bg-muted/40 border border-border text-xs disabled:opacity-60"
                          title={
                            lockDemote
                              ? "There must be at least one admin"
                              : "Change role"
                          }
                        >
                          <option value="user">User</option>
                          <option value="admin">Admin</option>
                        </select>
                      )}
                    </td>
                    <td className="px-4 py-2 text-xs text-muted-foreground">
                      {new Date(u.created_at).toLocaleDateString()}
                    </td>
                    <td className="px-4 py-2 text-right">
                      {!isMe && (
                        <button
                          type="button"
                          onClick={() =>
                            setPending({ id: u.id, label: u.email })
                          }
                          disabled={del.isPending}
                          className="text-muted-foreground hover:text-destructive disabled:opacity-50"
                          aria-label="Delete"
                        >
                          <Trash2Icon className="size-4" />
                        </button>
                      )}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        )}
      </div>
      <ConfirmDialog
        open={!!pending}
        onOpenChange={(o) => !o && setPending(null)}
        title={`Delete user “${pending?.label ?? ""}”?`}
        description="This cannot be undone."
        confirmLabel="Delete"
        pending={del.isPending}
        onConfirm={() => pending && del.mutate(pending.id)}
      />
    </section>
  );
}

function RoleBadge({ role }: { role: string }) {
  const admin = role === "admin";
  return (
    <span
      className={
        "rounded px-1.5 py-0.5 text-3xs font-semibold uppercase tracking-wide border " +
        (admin
          ? "bg-primary/15 text-primary border-primary/30"
          : "bg-muted/40 text-muted-foreground border-border")
      }
    >
      {admin ? "Admin" : "User"}
    </span>
  );
}

function NewUserForm({
  onSubmit,
  disabled,
}: {
  onSubmit: (b: { email: string; password: string; role: string }) => void;
  disabled?: boolean;
}) {
  const [open, setOpen] = useState(false);
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [role, setRole] = useState("user");

  function reset() {
    setEmail("");
    setPassword("");
    setRole("user");
    setOpen(false);
  }

  // Collapsed: just a button, so the table reads cleanly.
  if (!open) {
    return (
      <div>
        <button
          type="button"
          onClick={() => setOpen(true)}
          className="inline-flex items-center gap-1.5 h-9 px-3 rounded-md border border-border text-sm hover:bg-muted/30"
        >
          <PlusIcon className="size-4" /> Add user
        </button>
      </div>
    );
  }

  return (
    <form
      className="glass-card p-4 space-y-3"
      onSubmit={(e) => {
        e.preventDefault();
        if (!email.trim() || password.length < 12) return;
        onSubmit({
          email: email.trim().toLowerCase(),
          password,
          role,
        });
        reset();
      }}
    >
      <div className="grid sm:grid-cols-3 gap-3">
        <Field label="Email">
          <input
            required
            type="email"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            placeholder="user@example.com"
            className="w-full h-9 px-2 rounded-md bg-muted/40 border border-border text-sm"
          />
        </Field>
        <Field label="Password">
          <input
            required
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            minLength={12}
            placeholder="At least 12 characters"
            className="w-full h-9 px-2 rounded-md bg-muted/40 border border-border text-sm"
          />
        </Field>
        <Field label="Role">
          <select
            value={role}
            onChange={(e) => setRole(e.target.value)}
            className="w-full h-9 px-2 rounded-md bg-muted/40 border border-border text-sm"
          >
            <option value="user">User</option>
            <option value="admin">Admin</option>
          </select>
        </Field>
      </div>
      <div className="flex items-center gap-2">
        <button
          type="submit"
          disabled={disabled || !email.trim() || password.length < 12}
          className="inline-flex items-center gap-1 h-9 px-3 rounded-md bg-primary text-primary-foreground text-sm font-medium disabled:opacity-50"
        >
          <PlusIcon className="size-4" /> Create user
        </button>
        <button
          type="button"
          onClick={reset}
          className="h-9 px-3 rounded-md border border-border text-sm hover:bg-muted/30"
        >
          Cancel
        </button>
      </div>
    </form>
  );
}

// ---------------------------------------------------------------------------
// Scanners — live container config + operator actions (Doctor / Pull).
// Editing the scanner backend itself happens via wolf.yaml + env + restart
// per the API comment on /api/scanners/config.
// ---------------------------------------------------------------------------

/**
 * Classify the tag on the resolved scanner image into the release channel it
 * represents, so an operator can tell at a glance whether this deployment
 * tracks a moving channel or is pinned to an exact set.
 *
 * The channels are produced by the scanner release factory
 * (.github/workflows/scanners-image.yml); `stable`/`latest` move only on an
 * approved or scheduled release, `candidate` on every gated build, and
 * `scanner-set-YYYY.WW.N` never moves.
 */
export function describeScannerChannel(image: string): {
  label: string;
  detail: string;
  tone: string;
} {
  // Strip the registry host before looking for the tag separator, or the
  // colon in "host:port" would be mistaken for one.
  const lastSlash = image.lastIndexOf("/");
  const namePart = lastSlash === -1 ? image : image.slice(lastSlash + 1);
  const atDigest = namePart.indexOf("@");
  if (atDigest !== -1) {
    return {
      label: "Pinned digest",
      detail: "Immutable — never moves.",
      tone: "completed",
    };
  }
  const colon = namePart.lastIndexOf(":");
  const tag = colon === -1 ? "" : namePart.slice(colon + 1);

  switch (tag) {
    case "":
      return {
        label: "Unpinned",
        detail: "No tag resolved; the registry default applies.",
        tone: "warning",
      };
    case "stable":
    case "latest":
      return {
        label: tag,
        detail: "Approved release set. Updates on each scanner release.",
        tone: "completed",
      };
    case "candidate":
      return {
        label: "candidate",
        detail: "Newest gated build, not yet promoted to stable.",
        tone: "running",
      };
    default:
      if (/^scanner-set-\d{4}\.\d{2}\.\d+$/.test(tag)) {
        return {
          label: tag,
          detail: "Pinned to an exact release set — never moves.",
          tone: "completed",
        };
      }
      return {
        label: tag,
        detail: "Custom or locally built tag.",
        tone: "info",
      };
  }
}

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

interface DoctorCheck {
  label: string;
  ok: boolean;
  detail?: string;
}

interface DoctorResult {
  overall_ok: boolean;
  checks: DoctorCheck[];
}

interface PullResult {
  pulled: string[];
  errors?: { image: string; error: string }[];
}

interface ImageStatus {
  image: string;
  local_digest?: string;
  remote_digest?: string;
  updates_available: boolean;
  local_error?: string;
  remote_error?: string;
}

function ScannersTab() {
  const qc = useQueryClient();
  const runtimeQ = useRuntimeCapabilities();
  const dockerImageManagement = runtimeQ.data?.docker_image_management ?? true;
  const cfgQ = useQuery({
    queryKey: ["scanners-config"],
    queryFn: async () =>
      (await api.get<ScannersConfig>("/scanners/config")).data,
    enabled: dockerImageManagement,
  });
  const [doctor, setDoctor] = useState<DoctorResult | null>(null);
  const [pull, setPull] = useState<PullResult | null>(null);

  const doctorMut = useMutation({
    mutationFn: async () =>
      (await api.post<DoctorResult>("/scanners/doctor")).data,
    onSuccess: (d) => {
      setDoctor(d);
      if (d?.overall_ok) toast.success("All scanner checks passed");
      else toast.error("Scanner checks failed — see report below");
    },
    onError: (e) =>
      toast.error(safeErrorMessage(e, "Scanner diagnostics could not run.")),
  });

  const pullMut = useMutation({
    mutationFn: async () => (await api.post<PullResult>("/scanners/pull")).data,
    onSuccess: (p) => {
      setPull(p);
      if (p && (!p.errors || p.errors.length === 0)) {
        toast.success(`Pulled ${p.pulled?.length ?? 0} image(s)`);
      } else {
        toast.error(
          `Pulled ${p?.pulled?.length ?? 0}, ${p?.errors?.length ?? 0} failed`,
        );
      }
      qc.invalidateQueries({ queryKey: ["scanners-config"] });
      qc.invalidateQueries({ queryKey: ["scanner-images"] });
      qc.invalidateQueries({ queryKey: ["scanners", "images"] });
    },
    onError: (e) =>
      toast.error(safeErrorMessage(e, "Scanner images could not be pulled.")),
  });

  if (runtimeQ.isLoading || (dockerImageManagement && cfgQ.isLoading)) {
    return (
      <p className="text-sm text-muted-foreground" role="status">
        Loading scanner runtime…
      </p>
    );
  }
  if (runtimeQ.data && !runtimeQ.data.docker_image_management) {
    return (
      <section className="space-y-4">
        <div className="glass-card p-5">
          <h3 className="text-sm font-medium">
            Scanner runtime is managed by Kubernetes
          </h3>
          <p className="mt-1 text-xs text-muted-foreground">
            Image pull, build, and local Docker diagnostics are unavailable in
            this deployment. Scanner images and policies are managed by the
            cluster operator.
          </p>
        </div>
      </section>
    );
  }
  if (cfgQ.isError || !cfgQ.data) {
    return (
      <p className="text-sm text-destructive" role="alert">
        Failed to load scanner config — is the container backend initialized?
      </p>
    );
  }

  const cfg = cfgQ.data;
  const channel = describeScannerChannel(cfg.image);
  const rows: Array<[string, React.ReactNode]> = [
    ["Default image", <code className="text-xs">{cfg.image}</code>],
    [
      "Release channel",
      <span className="inline-flex items-center gap-2">
        <StatusBadge
          status={channel.tone}
          label={channel.label}
          size="sm"
          showDot={false}
        />
        <span className="text-xs text-muted-foreground">{channel.detail}</span>
      </span>,
    ],
    ["Pull policy", cfg.pull_policy],
    ["Network", cfg.network],
    ["Memory", cfg.memory],
    ["CPUs", cfg.cpus],
    ["UID:GID", `${cfg.uid}:${cfg.gid}`],
    ["Vuln-DB volume", <code className="text-xs">{cfg.db_volume || "—"}</code>],
    [
      "Host repos root",
      <code className="text-xs">{cfg.host_repos_root || "—"}</code>,
    ],
  ];

  // Heuristic for "set up needed": pull policy expects local image
  // (Never / IfNotPresent) and the most recent pull / doctor surfaces
  // an error. We can't probe "is the image present" from the client
  // without a dedicated endpoint, so this is best-effort UX guidance.
  const wolfBuiltMissing =
    pull?.errors?.some((e) => e.image === cfg.image) ?? false;

  return (
    <section className="space-y-4">
      {/* Operator actions row. Doctor + Set up are the two day-1 buttons. */}
      <div className="glass-card p-5">
        <div className="flex flex-wrap items-center gap-2">
          <button
            type="button"
            onClick={() => pullMut.mutate()}
            disabled={pullMut.isPending}
            className="inline-flex items-center gap-1.5 h-9 px-3 rounded-md bg-primary text-primary-foreground text-sm font-medium disabled:opacity-50"
            title="Pre-pull every image in the configured set so the first scan doesn't pay the pull latency"
          >
            {pullMut.isPending ? (
              <Loader2Icon className="size-4 animate-spin" />
            ) : (
              <DownloadIcon className="size-4" />
            )}
            {pullMut.isPending ? "Pulling…" : "Set up scanners (pull images)"}
          </button>
          <button
            type="button"
            onClick={() => doctorMut.mutate()}
            disabled={doctorMut.isPending}
            className="inline-flex items-center gap-1.5 h-9 px-3 rounded-md border border-border text-sm hover:bg-muted/30 disabled:opacity-50"
            title="Run scanner-backend diagnostics (docker reachable, image present, cache writable, etc.)"
          >
            {doctorMut.isPending ? (
              <Loader2Icon className="size-4 animate-spin" />
            ) : (
              <ShieldIcon className="size-4" />
            )}
            {doctorMut.isPending ? "Running…" : "Run Doctor"}
          </button>
        </div>

        {/* First-run hint: pull is the only setup step. The default
            wolf-scanners* images are published to GHCR
            (alphabravo-oss/*); first scan triggers an auto-pull
            per the IfNotPresent policy, but pre-pulling saves the
            wait on the first real scan. If pull errors out for the
            default image, surface the registry hint as a banner. */}
        {wolfBuiltMissing && (
          <div className="mt-3 rounded-md border border-status-warning/40 bg-status-warning/10 px-3 py-2 text-xs text-status-warning">
            <strong>Couldn't pull {cfg.image}.</strong> Check your network /
            registry credentials. The default registry is{" "}
            <code>ghcr.io/alphabravo-oss</code>; override via{" "}
            <code>WOLF_SCANNERS_IMAGE</code> to point at a mirror or a
            locally-built image. If the tag itself is missing, set{" "}
            <code>WOLF_SCANNERS_TAG</code> to another channel —{" "}
            <code>stable</code> and <code>latest</code> track the approved
            release set, <code>candidate</code> the newest gated build, and{" "}
            <code>scanner-set-YYYY.WW.N</code> pins an exact set.
          </div>
        )}

        {/* Doctor result panel. */}
        {doctor && (
          <div
            className="mt-4 rounded-md border border-border bg-muted/20 p-3 text-sm space-y-1"
            role={doctor.overall_ok ? "status" : "alert"}
            aria-live="polite"
          >
            <div className="flex items-center gap-2 mb-1">
              {doctor.overall_ok ? (
                <CheckIcon className="size-4 text-status-success" />
              ) : (
                <span className="text-destructive">●</span>
              )}
              <span className="font-medium">
                {doctor.overall_ok ? "All checks passed" : "Some checks failed"}
              </span>
            </div>
            <ul className="text-xs space-y-0.5 font-mono">
              {doctor.checks.map((c, i) => (
                <li key={i} className="flex items-start gap-2">
                  <span
                    className={c.ok ? "text-status-success" : "text-destructive"}
                  >
                    {c.ok ? "✓" : "✗"}
                  </span>
                  <span className="flex-1">
                    {c.label}
                    {c.detail && (
                      <span className="text-muted-foreground">
                        {" "}
                        —{" "}
                        {c.ok
                          ? "Check completed."
                          : "Check failed. Review server logs."}
                      </span>
                    )}
                  </span>
                </li>
              ))}
            </ul>
          </div>
        )}

        {/* Pull result panel. */}
        {pull && (
          <div
            className="mt-4 rounded-md border border-border bg-muted/20 p-3 text-sm space-y-1"
            role={pull.errors?.length ? "alert" : "status"}
            aria-live="polite"
          >
            <div className="font-medium mb-1">
              Pulled {pull.pulled?.length ?? 0} image
              {(pull.pulled?.length ?? 0) === 1 ? "" : "s"}
              {pull.errors && pull.errors.length > 0 && (
                <span className="text-destructive ml-2">
                  · {pull.errors.length} failed
                </span>
              )}
            </div>
            {pull.errors && pull.errors.length > 0 && (
              <ul className="text-xs space-y-0.5 font-mono text-destructive">
                {pull.errors.map((e, i) => (
                  <li key={i}>
                    {e.image}: Pull failed. Review registry access and server
                    logs.
                  </li>
                ))}
              </ul>
            )}
          </div>
        )}
      </div>

      <ImagesPanel />

      <div className="glass-card p-5">
        <p className="text-xs text-muted-foreground mb-3">
          Config is read-only — edit via <code>wolf.yaml</code> /{" "}
          <code>WOLF_SCANNERS_*</code> env, then restart wolf.
        </p>
        <dl className="grid md:grid-cols-2 gap-x-6 gap-y-2 text-sm">
          {rows.map(([k, v]) => (
            <div key={k} className="flex items-center justify-between gap-3">
              <dt className="text-muted-foreground">{k}</dt>
              <dd className="text-right">{v}</dd>
            </div>
          ))}
        </dl>
      </div>
      {cfg.image_overrides && Object.keys(cfg.image_overrides).length > 0 && (
        <div className="glass-card p-5">
          <h3 className="text-sm font-medium mb-2">Per-tool image overrides</h3>
          <ul className="text-sm space-y-1">
            {Object.entries(cfg.image_overrides).map(([tool, image]) => (
              <li
                key={tool}
                className="flex items-center gap-3 font-mono text-xs"
              >
                <span className="text-muted-foreground w-24">{tool}</span>
                <code>{image}</code>
              </li>
            ))}
          </ul>
        </div>
      )}
    </section>
  );
}

// ImagesPanel — per-image digests + update detection + per-image pull.
//
// The list of images is whatever wolf is currently configured to use
// (default + per-tool overrides + upstream-pinned scanner images). For
// each, we show the LOCAL pull-time digest and the REMOTE current
// digest. When they differ, an "Update" button appears that pulls
// just that image. "Check for updates" re-runs the probe.

/**
 * Is this one of the images Wolf builds and releases itself?
 *
 * Matches on the repository path rather than the registry host, so a mirror
 * or a private re-host of the same images still classifies correctly — an
 * operator pointing WOLF_SCANNERS_IMAGE at their own registry should still
 * see those under "built by Wolf", not lumped in with third-party tools.
 */
export function isWolfBuiltImage(image: string): boolean {
  const path = image.split("@")[0].split(":")[0];
  const name = path.split("/").pop() ?? "";
  return name.startsWith("wolf-scanners") || name.startsWith("wolf-fixer");
}

function ImagesPanel() {
  const qc = useQueryClient();
  const notifiedUpdateDigest = useRef("");
  const q = useQuery({
    queryKey: ["scanner-images"],
    queryFn: async () =>
      (await api.get<ImageStatus[]>("/scanners/images")).data ?? [],
    // Registry probes shell out per image; keep the cadence slow enough to be
    // cheap while still surfacing scanner channel changes without a restart.
    refetchInterval: 6 * 60 * 60 * 1000,
    refetchOnWindowFocus: false,
  });
  const pullAll = useMutation({
    mutationFn: async () => (await api.post<PullResult>("/scanners/pull")).data,
    onSuccess: (result) => {
      const failed = result.errors?.length ?? 0;
      if (failed > 0) {
        toast.error(`${failed} scanner image update${failed === 1 ? "" : "s"} failed`);
      } else {
        toast.success(
          `Pulled ${result.pulled.length} scanner image${result.pulled.length === 1 ? "" : "s"}`,
        );
      }
      qc.invalidateQueries({ queryKey: ["scanner-images"] });
      qc.invalidateQueries({ queryKey: ["scanners", "images"] });
      qc.invalidateQueries({ queryKey: ["scanners-config"] });
    },
    onError: (e) =>
      toast.error(safeErrorMessage(e, "Scanner images could not be pulled.")),
  });
  const pullOne = useMutation({
    mutationFn: (image: string) =>
      api.post<{ image: string; local_digest: string }>(
        "/scanners/images/pull",
        { image },
      ),
    onSuccess: (_, image) => {
      toast.success(`Pulled ${image}`);
      qc.invalidateQueries({ queryKey: ["scanner-images"] });
      qc.invalidateQueries({ queryKey: ["scanners", "images"] });
    },
    onError: (e) =>
      toast.error(
        safeErrorMessage(e, "The scanner image could not be pulled."),
      ),
  });
  const images = q.data ?? [];
  const updateItems = images.filter((img) => img.updates_available);
  // Two different things share this list, and they behave differently: the
  // wolf-built images come off our own release factory and move with the
  // configured channel, while the upstream ones are third-party tools pinned
  // by exact version in scanner-lock.yaml and only change when that lock is
  // bumped. Splitting them keeps "is our release current?" separable from
  // "are the pinned third-party tools present?".
  const wolfBuilt = images.filter((img) => isWolfBuiltImage(img.image));
  const upstream = images.filter((img) => !isWolfBuiltImage(img.image));
  const updateDigest = updateItems
    .map((img) => `${img.image}:${img.remote_digest ?? ""}`)
    .join("|");

  useEffect(() => {
    if (!updateDigest || notifiedUpdateDigest.current === updateDigest) return;
    notifiedUpdateDigest.current = updateDigest;
    toast.info(
      `${updateItems.length} scanner image update${updateItems.length === 1 ? "" : "s"} available`,
    );
  }, [updateDigest, updateItems.length]);

  const probeState = { loading: q.isLoading, error: q.isError };

  return (
    // Two separate cards rather than one card with two sections: these are
    // different supply chains, not two views of the same list. The Wolf images
    // are ours to rebuild and re-release; the upstream ones we only pin and
    // pull. Keeping them in one card invited reading a single "N updates
    // available" number that spanned both.
    <div className="space-y-4">
      <ImagesCard
        title="Wolf scanner images"
        description="Built and released by the Wolf scanner factory. These move with the configured release channel."
        items={wolfBuilt}
        probe={probeState}
        pullOne={pullOne}
        pullAll={pullAll}
        onRefresh={() => q.refetch()}
        refreshing={q.isFetching}
        emptyLabel="No Wolf-built scanner images configured."
      />
      <ImagesCard
        title="Upstream tool images"
        description="Third-party scanners pinned to an exact version in scanner-lock.yaml. They change only when that lock is bumped."
        items={upstream}
        probe={probeState}
        pullOne={pullOne}
        onRefresh={() => q.refetch()}
        refreshing={q.isFetching}
        emptyLabel="No upstream tool images configured."
      />
    </div>
  );
}

/**
 * One card of scanner images: its own heading, its own outdated count, and its
 * own actions. Each card owns a single supply chain so an operator can read
 * "are my Wolf images current?" without that answer being averaged together
 * with 25 pinned third-party tools.
 */
function ImagesCard({
  title,
  description,
  items,
  probe,
  pullOne,
  pullAll,
  onRefresh,
  refreshing,
  emptyLabel,
}: {
  title: string;
  description: string;
  items: ImageStatus[];
  probe: { loading: boolean; error: boolean };
  pullOne: {
    mutate: (image: string) => void;
    isPending: boolean;
    variables?: string;
  };
  /** Only the Wolf card gets a bulk action; `POST /scanners/pull` pulls the
   *  configured channel, which is exactly the Wolf-built set. */
  pullAll?: { mutate: () => void; isPending: boolean };
  onRefresh: () => void;
  refreshing: boolean;
  emptyLabel: string;
}) {
  const stale = items.filter((i) => i.updates_available);

  return (
    <div className="glass-card p-5">
      <div className="mb-3 flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0">
          <h3 className="text-sm font-medium">{title}</h3>
          <p className="mt-0.5 max-w-2xl text-2xs text-muted-foreground">
            {description}
          </p>
        </div>
        <div className="flex flex-wrap items-center justify-end gap-2">
          {pullAll && stale.length > 0 && (
            <button
              type="button"
              onClick={() => pullAll.mutate()}
              disabled={pullAll.isPending}
              className="inline-flex items-center gap-1 h-8 px-2.5 rounded-md bg-primary text-primary-foreground text-xs font-medium disabled:opacity-50"
              title="Pull every configured scanner image from the current registry channel"
            >
              {pullAll.isPending ? (
                <Loader2Icon className="size-3.5 animate-spin" />
              ) : (
                <DownloadIcon className="size-3.5" />
              )}
              Update all
            </button>
          )}
          <button
            type="button"
            onClick={onRefresh}
            disabled={refreshing}
            className="inline-flex items-center gap-1 h-8 px-2.5 rounded-md border border-border text-xs hover:bg-accent disabled:opacity-50"
            title="Re-probe local and remote digests"
          >
            {refreshing ? (
              <Loader2Icon className="size-3.5 animate-spin" />
            ) : (
              <RefreshCwIcon className="size-3.5" />
            )}
            Check for updates
          </button>
        </div>
      </div>

      {stale.length > 0 && (
        <div className="mb-3 rounded-md border border-status-warning/30 bg-status-warning/10 px-3 py-2 text-xs text-status-warning">
          <span className="font-medium">
            {stale.length} of {items.length} image
            {items.length === 1 ? "" : "s"} outdated.
          </span>{" "}
          Pull to move to the current digest.
        </div>
      )}

      {probe.loading ? (
        <p className="text-xs text-muted-foreground" role="status">
          Probing local + remote digests…
        </p>
      ) : probe.error ? (
        <p className="text-xs text-destructive" role="alert">
          Failed to probe scanner image digests.
        </p>
      ) : items.length === 0 ? (
        <p className="text-xs text-muted-foreground">{emptyLabel}</p>
      ) : (
        <ul className="space-y-2 text-sm">
          {items.map((img) => (
            <li
              key={img.image}
              className="flex flex-wrap items-center gap-3 border-b border-border pb-2 last:border-0 last:pb-0"
            >
              <div className="font-mono text-xs flex-1 min-w-0 break-all">
                {img.image}
              </div>
              <DigestPill
                label="local"
                value={img.local_digest}
                err={img.local_error}
              />
              <DigestPill
                label="remote"
                value={img.remote_digest}
                err={img.remote_error}
              />
              {img.updates_available && (
                <span className="text-[10px] uppercase tracking-wide font-medium text-status-warning bg-status-warning/10 border border-status-warning/30 rounded px-1.5 py-0.5">
                  update available
                </span>
              )}
              {(img.updates_available ||
                (!img.local_digest && !img.local_error)) && (
                <button
                  type="button"
                  onClick={() => pullOne.mutate(img.image)}
                  disabled={pullOne.isPending}
                  className="inline-flex items-center gap-1 h-7 px-2.5 rounded-md bg-primary text-primary-foreground text-xs font-medium disabled:opacity-50"
                >
                  {pullOne.isPending && pullOne.variables === img.image ? (
                    <Loader2Icon className="size-3 animate-spin" />
                  ) : (
                    <DownloadIcon className="size-3" />
                  )}
                  {img.updates_available ? "Update" : "Pull"}
                </button>
              )}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

// DigestPill renders a labelled SHA-256 (truncated) or an error tag
// when the probe failed.
function DigestPill({
  label,
  value,
  err,
}: {
  label: string;
  value?: string;
  err?: string;
}) {
  if (err) {
    return (
      <span
        className="text-[10px] uppercase tracking-wide text-status-error bg-status-error/10 border border-status-error/30 rounded px-1.5 py-0.5"
        title={`${label} registry probe failed. Review server logs.`}
      >
        {label}: error
      </span>
    );
  }
  if (!value) {
    return (
      <span
        className="text-[10px] uppercase tracking-wide text-muted-foreground bg-muted/20 border border-border rounded px-1.5 py-0.5"
        title={
          label === "local"
            ? "image not pulled yet — use 'Set up scanners' or the per-image Update"
            : "no manifest available"
        }
      >
        {label}: {label === "local" ? "not pulled" : "—"}
      </span>
    );
  }
  // Display sha256:abcdef… (8 chars after the prefix is plenty to
  // recognize and short enough to fit the row).
  const short = value.startsWith("sha256:")
    ? "sha256:" + value.slice(7, 15)
    : value.slice(0, 16);
  return (
    <span
      className="text-[10px] font-mono uppercase tracking-tight text-muted-foreground bg-muted/30 border border-border rounded px-1.5 py-0.5"
      title={value}
    >
      {label}: {short}…
    </span>
  );
}
