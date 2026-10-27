// Administration settings; personal controls live under /account.
//
// Tab state lives in the URL (?tab=…) so deep links work and refresh
// preserves which section the user was on. Scan presets were deliberately
// omitted — wolf auto-detects per-repo language/framework, no manual
// preset list is needed.
import { createFileRoute, useNavigate, redirect } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  BadgeCheckIcon,
  CheckIcon,
  DownloadIcon,
  HardDriveIcon,
  KeyIcon,
  KeyRoundIcon,
  Loader2Icon,
  LockIcon,
  PlusIcon,
  RefreshCwIcon,
  ServerIcon,
  SettingsIcon,
  ShieldIcon,
  Trash2Icon,
  WrenchIcon,
  UsersIcon,
} from "lucide-react";
import { useEffect, useId, useRef, useState } from "react";
import { toast } from "sonner";
import { api, isNotFound } from "@/lib/api";
import { GitHubTokenHelp } from "@/components/github-token-help";
import { GitLabTokenHelp } from "@/components/gitlab-token-help";
import { StatusBadge } from "@/components/ui/status-badge";
import { ErrorState } from "@/components/ui/empty-state";
import { useIsAdmin, useMe } from "@/lib/me";
import type {
  AdminSecret,
  ApiToken,
  ApiTokenCreated,
  DiskUsage,
  RemoteNode,
} from "@/lib/types";
import { formatBytes } from "@/lib/utils";
import { useSettingsMap } from "@/lib/flags";
import { FixerSettings } from "@/components/fixes/fixer-settings";
import { useRuntimeCapabilities } from "@/lib/runtime-capabilities";
import { safeErrorMessage } from "@/lib/safe-display";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { PageHeader } from "@/components/ui/page";
import { LicenseTab } from "@/components/license-settings";
import { entitled, REMEDIATION, useEdition } from "@/lib/edition";
import { Button } from "@/components/ui/button";
import { Switch } from "@/components/ui/switch";
import { Checkbox } from "@/components/ui/checkbox";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { DataTable, type Column } from "@/components/ui/data-table";

// Legacy personal ?tab= values that have NO admin tab of the same name now
// redirect to /account. (apikeys/secrets/nodes are kept here as ADMIN tabs —
// the global oversight view — so they are intentionally NOT redirected.)
const LEGACY_PERSONAL: Record<string, "profile" | "security"> = {
  account: "profile",
  security: "security",
};

export const Route = createFileRoute("/_authed/settings")({
  // Accept any string here; beforeLoad does the routing/redirects.
  validateSearch: (s: Record<string, unknown>): { tab: string; advanced?: boolean } => ({
    tab: typeof s.tab === "string" ? s.tab : "general",
    ...(s.advanced === true || s.advanced === "1" || s.advanced === "true"
      ? { advanced: true }
      : {}),
  }),
  beforeLoad: async ({ search }) => {
    // Personal surfaces moved to /account.
    const tab = (search as { tab?: string }).tab ?? "";
    if (tab === "audit") {
      throw redirect({ to: "/audit" });
    }
    const legacy = LEGACY_PERSONAL[tab];
    if (legacy) {
      throw redirect({ to: "/account", search: { section: legacy } });
    }
    // Settings is admin-only; bounce everyone else to their account.
    const me = await api
      .get<{ role: string }>("/auth/me")
      .then((r) => r.data)
      .catch(() => null);
    if (me?.role !== "admin") {
      const section = tab === "apikeys" || tab === "secrets" || tab === "nodes" ? tab : "profile";
      throw redirect({ to: "/account", search: { section } });
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
  | "scanner-updates"
  | "license";

// Settings is the admin surface: system config + a global, cross-user
// oversight view of API keys / secrets / nodes. Audit lives at /audit. Personal
// (per-user) management lives under /account.
const TABS: { key: TabKey; label: string; Icon: typeof SettingsIcon }[] = [
  { key: "general", label: "General", Icon: SettingsIcon },
  { key: "users", label: "Users", Icon: UsersIcon },
  { key: "scanners", label: "Scanners", Icon: ShieldIcon },
  { key: "license", label: "License", Icon: BadgeCheckIcon },
  { key: "fixer", label: "Fixer", Icon: WrenchIcon },
  { key: "apikeys", label: "All API keys", Icon: KeyRoundIcon },
  { key: "secrets", label: "All secrets", Icon: KeyIcon },
  { key: "nodes", label: "All nodes", Icon: ServerIcon },
  { key: "scanner-updates", label: "Advanced", Icon: RefreshCwIcon },
];

function SettingsPage() {
  const { tab } = Route.useSearch();
  const navigate = useNavigate();
  const activeTab = TABS.some((t) => t.key === tab) ? tab as TabKey : "general";
  const changeTab = (tab: string) => navigate({ to: "/settings", search: { tab } });

  return (
    <div className="page stack min-w-0">
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
              className="text-foreground underline underline-offset-2"
            >
              Account
            </button>
            .
          </>
        }
      />
      <div className="grid gap-6 lg:grid-cols-[180px_minmax(0,1fr)]">
        <div className="lg:hidden">
          <Select value={activeTab} onValueChange={changeTab}>
            <SelectTrigger aria-label="Administration section"><SelectValue /></SelectTrigger>
            <SelectContent>{TABS.map(({ key, label }) => <SelectItem key={key} value={key}>{label}</SelectItem>)}</SelectContent>
          </Select>
        </div>
        <nav aria-label="Administration settings" className="hidden lg:block space-y-1">
          {TABS.map(({ key, label, Icon }) => (
            <button key={key} type="button" aria-current={activeTab === key ? "page" : undefined}
              onClick={() => changeTab(key)}
              className={`flex w-full items-center gap-2 rounded-md px-3 py-2 text-sm text-left ${activeTab === key ? "bg-accent text-foreground font-medium" : "text-muted-foreground hover:bg-accent/50 hover:text-foreground"}`}>
              <Icon className="size-4" aria-hidden="true" />{label}
            </button>
          ))}
        </nav>

      <section aria-labelledby="active-settings-section" className="min-w-0 max-w-4xl">
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
        {activeTab === "scanner-updates" && <ScannerUpdatesTab />}
        {activeTab === "license" && <LicenseTab />}
      </section>
      </div>
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

function adminApiKeyColumns(
  ownerOf: (id?: string) => string,
  setPending: (p: { id: string; label: string }) => void,
): Column<ApiToken>[] {
  return [
    { key: "owner", header: "Owner", sortAccessor: (t) => ownerOf(t.user_id), accessor: (t) => ownerOf(t.user_id) },
    {
      key: "name",
      header: "Name",
      sortAccessor: (t) => t.name,
      accessor: (t) => (
        <>
          {t.name}
          {t.revoked_at && (
            <span className="ml-1 text-[10px] uppercase text-muted-foreground">revoked</span>
          )}
        </>
      ),
    },
    { key: "prefix", header: "Prefix", sortAccessor: (t) => t.token_prefix, accessor: (t) => <span className="font-mono text-xs text-muted-foreground">{t.token_prefix}…</span> },
    { key: "scopes", header: "Scopes", sortAccessor: (t) => t.scopes.join(" "), accessor: (t) => t.scopes.join(", ") },
    {
      key: "expires",
      header: "Expires",
      sortAccessor: (t) => t.expires_at ?? "",
      accessor: (t) => (t.expires_at ? new Date(t.expires_at).toLocaleDateString() : "never"),
    },
    {
      key: "actions",
      header: "",
      sortable: false,
      accessor: (t) =>
        t.revoked_at ? null : (
          <button
            type="button"
            onClick={() => setPending({ id: t.id, label: `${ownerOf(t.user_id)}'s key "${t.name}"` })}
            className="inline-flex items-center gap-1 h-7 px-2 rounded-md border border-status-error/40 text-status-error hover:bg-status-error/10 text-xs"
          >
            <Trash2Icon className="size-3.5" /> Revoke
          </button>
        ),
    },
  ];
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
        <DataTable
          data={q.data ?? []}
          columns={adminApiKeyColumns(ownerOf, setPending)}
          keyExtractor={(t) => t.id}
          persistKey="wolf-admin-apikeys"
          density="compact"
          loading={q.isLoading}
          isError={q.isError}
          onRetry={() => void q.refetch()}
          emptyMessage="No API keys."
          searchPlaceholder="Search keys..."
        />
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
        <DataTable
          data={q.data ?? []}
          columns={[
            { key: "owner", header: "Owner", sortAccessor: (s) => ownerOf(s.user_id), accessor: (s) => ownerOf(s.user_id) },
            { key: "type", header: "Type", sortAccessor: (s) => s.key_type, accessor: (s) => s.key_type },
            { key: "name", header: "Name", sortAccessor: (s) => s.key_name, accessor: (s) => s.key_name },
            { key: "value", header: "Value", sortable: false, accessor: () => "••••" },
            {
              key: "actions",
              header: "",
              sortable: false,
              accessor: (s) => (
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
              ),
            },
          ]}
          keyExtractor={(s) => s.id}
          persistKey="wolf-admin-secrets"
          density="compact"
          loading={q.isLoading}
          isError={q.isError}
          onRetry={() => void q.refetch()}
          emptyMessage="No secrets."
          searchPlaceholder="Search secrets..."
        />
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
        <DataTable
          data={q.data ?? []}
          columns={[
            { key: "owner", header: "Owner", sortAccessor: (n) => ownerOf(n.user_id), accessor: (n) => ownerOf(n.user_id) },
            { key: "name", header: "Name", sortAccessor: (n) => n.name, accessor: (n) => n.name },
            { key: "host", header: "Host", sortAccessor: (n) => n.host, accessor: (n) => `${n.username}@${n.host}:${n.port}` },
            { key: "enabled", header: "Enabled", sortAccessor: (n) => (n.enabled ? "yes" : "no"), accessor: (n) => (n.enabled ? "yes" : "no") },
            {
              key: "actions",
              header: "",
              sortable: false,
              accessor: (n) => (
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
              ),
            },
          ]}
          keyExtractor={(n) => n.id}
          persistKey="wolf-admin-nodes"
          density="compact"
          loading={q.isLoading}
          isError={q.isError}
          onRetry={() => void q.refetch()}
          emptyMessage="No nodes."
          searchPlaceholder="Search nodes..."
        />
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


// ---------------------------------------------------------------------------
// Account — per-user profile (display name, email, password) + links to the
// other personal surfaces (API keys, two-factor).
// ---------------------------------------------------------------------------

export function AccountTab() {
  const qc = useQueryClient();
  const me = useMe();

  const [email, setEmail] = useState("");
  const [currentPw, setCurrentPw] = useState("");
  // Seed the form from the loaded user (and re-sync after a saved change).
  useEffect(() => {
    if (me.data?.email) setEmail(me.data.email);
  }, [me.data?.email]);
  const emailChanged =
    !!me.data && email.trim().toLowerCase() !== me.data.email;

  const [curPw, setCurPw] = useState("");
  const [newPw, setNewPw] = useState("");
  const [confirmPw, setConfirmPw] = useState("");

  const profile = useMutation({
    mutationFn: (updates: { display_name?: string; email?: string; current_password?: string }) =>
      api.put("/auth/profile", updates),
    onSuccess: () => {
      toast.success("Profile saved");
      setCurrentPw("");
      return qc.invalidateQueries({ queryKey: ["me"] });
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
        <TextSetting label="Display name" value={me.data?.display_name ?? ""} disabled={profile.isPending}
          onCommit={(display_name) => profile.mutate({ display_name })} />
        <form className="space-y-3" onSubmit={(e) => { e.preventDefault(); if (emailChanged && currentPw && !profile.isPending) profile.mutate({ email, current_password: currentPw }); }}>
        <label className="block space-y-1">
          <span className="text-xs text-muted-foreground">Email</span>
          <input
            type="email"
            value={email}
            disabled={profile.isPending}
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
              disabled={profile.isPending}
              onChange={(e) => setCurrentPw(e.target.value)}
              autoComplete="current-password"
              className="w-full h-9 px-3 rounded-md bg-muted/40 border border-border text-sm"
            />
          </label>
        )}
        {emailChanged && <Button type="submit" size="sm" disabled={profile.isPending || !email.trim() || !currentPw}>
          {profile.isPending ? "Saving…" : "Save"}
        </Button>}
        </form>
      </div>

      {/* Password */}
      <form className="glass-card p-5 space-y-4" onSubmit={(e) => { e.preventDefault(); if (!password.isPending && curPw && newPw.length >= 12 && newPw === confirmPw) password.mutate(); }}>
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
        {(curPw || newPw || confirmPw) && <Button type="submit" size="sm"
          disabled={password.isPending || !curPw || newPw.length < 12 || newPw !== confirmPw}>
          {password.isPending ? "Saving…" : "Save"}
        </Button>}
      </form>


    </section>
  );
}

// ---------------------------------------------------------------------------
// General — global toggles backed by GET/PUT /api/settings
// ---------------------------------------------------------------------------

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
    entitlement: REMEDIATION,
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
    help: "0 = automatic sizing from the scanner host. Set a number to cap tools per scan; the shared CPU and memory budget still applies.",
    type: "int" as const,
    min: 0,
    max: 32,
  },
  {
    key: "heavy_scanner_concurrency",
    label: "Heavy tool concurrency",
    help: "0 = automatic. Caps demanding tools such as Semgrep, Bearer, and Grype while leaving room for lighter checks.",
    type: "int" as const,
    min: 0,
    max: 32,
  },
  {
    key: "network_scanner_concurrency",
    label: "Network tool concurrency",
    help: "0 = automatic. Caps tools downloading rules, dependencies, or vulnerability databases at the same time.",
    type: "int" as const,
    min: 0,
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
  const q = useSettingsMap();
  const editionQ = useEdition();
  const m = useMutation({
    mutationFn: (updates: Record<string, string>) =>
      api.put("/settings", updates),
    onSuccess: async (_, updates) => {
      qc.setQueryData<Record<string, string>>(["settings"], (previous) => ({ ...previous, ...updates }));
      toast.success("Settings saved");
      await Promise.all([
        qc.invalidateQueries({ queryKey: ["settings"] }),
        qc.invalidateQueries({ queryKey: ["scanner-scheduling"] }),
      ]);
    },
    onError: (e) => toast.error(e instanceof Error ? e.message : "Save failed"),
  });

  if (q.isLoading)
    return <p role="status" className="text-sm text-muted-foreground">Loading…</p>;
  if (q.isError) return <ErrorState title="Could not load settings" onRetry={() => q.refetch()} />;
  const settings = q.data ?? {};
  const product = editionQ.data?.product ?? "Wolf Community";
  const editionName = editionQ.data?.edition ?? "community";

  return (
    <div className="space-y-4">
    <section className="glass-card p-5 space-y-2">
      <h3 className="text-sm font-medium">Edition</h3>
      <p className="text-xs text-muted-foreground">
        {product} — {editionName}.{" "}
        {editionQ.data?.licensed
          ? "A commercial license is active."
          : "No commercial license is installed in this binary."}{" "}
        See the License section for entitlements and install.
      </p>
    </section>
    <section className="glass-card p-5 space-y-5">
      {GENERAL_KNOBS.map((knob) => {
        const current = settings[knob.key] ?? (knob.type === "int" ? "0" : "");
        const capability = "entitlement" in knob ? knob.entitlement : undefined;
        const locked = capability !== undefined && !entitled(editionQ.data, capability);
        return (
          <div
            key={knob.key}
            className="grid md:grid-cols-[1fr_240px] gap-4 items-start"
          >
            <div>
              <label className="text-sm font-medium inline-flex items-center gap-1.5">
                {knob.label}
                {locked && (
                  <span className="rounded px-1.5 py-0.5 text-3xs font-semibold uppercase tracking-wide bg-primary/10 text-primary border border-primary/30">
                    Enterprise
                  </span>
                )}
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
                label={knob.label}
                value={!locked && current === "true"}
                onChange={(v) => m.mutateAsync({ [knob.key]: v ? "true" : "false" })}
                disabled={m.isPending || locked}
              />
            ) : knob.type === "choice" ? (
              <ChoiceSetting label={knob.label} value={current || knob.options[0]} options={knob.options}
                onCommit={(value) => m.mutate({ [knob.key]: value })} disabled={m.isPending} />
            ) : (
              <IntInput
                label={knob.label}
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
    <SchedulingCard />
    <DatabaseCard />
    <DiskCard />
    </div>
  );
}

function SchedulingCard() {
  const q = useQuery({
    queryKey: ["scanner-scheduling"],
    queryFn: async () => (await api.get<{
      concurrency: number; heavy_concurrency: number; network_concurrency: number;
      host_cpus: number; host_memory_bytes: number; cpu_budget: number;
      memory_budget_bytes: number; workers: number; source: string; warning?: string;
    }>("/settings/scheduling")).data,
    staleTime: 60_000,
  });
  const plan = q.data;
  return (
    <section className="glass-card p-5 space-y-2" aria-label="Scanner scheduling">
      <h3 className="text-sm font-medium">Scanner scheduling</h3>
      {q.isPending ? <p className="text-sm text-muted-foreground">Detecting scanner host…</p> :
       q.isError || !plan ? <p className="text-sm text-destructive">Could not load scheduling capacity.</p> : <>
        <p className="text-sm">Up to {plan.concurrency} tools per scan · {plan.heavy_concurrency} heavy · {plan.network_concurrency} network</p>
        {plan.host_cpus > 0 && <p className="text-xs text-muted-foreground">
          {plan.host_cpus} CPUs · {(plan.host_memory_bytes / 2 ** 30).toFixed(1)} GiB detected.
          {plan.workers} {plan.workers === 1 ? "worker" : "workers"}, with a shared budget of {plan.cpu_budget.toFixed(1)} CPUs and {(plan.memory_budget_bytes / 2 ** 30).toFixed(1)} GiB per worker.
          Capacity is reserved for Wolf and the operating system. Tools start only when their resource limits fit.
        </p>}
        <p className="text-xs text-muted-foreground">Recalculated for each new scan using its worker’s Docker host. Existing scans keep their limits.</p>
        {plan.warning && <p className="text-xs text-status-warning">{plan.warning}. Conservative fallback: {plan.concurrency} tools.</p>}
      </>}
    </section>
  );
}

type DatabaseInfo = {
  driver: string;
  user?: string;
  host?: string;
  database?: string;
  sslmode?: string;
  env_managed?: boolean;
  file_set?: boolean;
};

function DatabaseCard() {
  const q = useQuery({
    queryKey: ["admin-database"],
    queryFn: async () => (await api.get<DatabaseInfo>("/admin/database")).data,
  });
  const [dsn, setDsn] = useState("");
  const test = useMutation({
    mutationFn: () => api.post("/admin/database/test", { dsn }),
    onSuccess: () => toast.success("Connected"),
    onError: (e) => toast.error(e instanceof Error ? e.message : "Unreachable"),
  });
  const save = useMutation({
    mutationFn: () => api.put("/admin/database", { dsn }),
    onSuccess: () => {
      setDsn("");
      toast.success("Saved. Restart Wolf to use this database.");
    },
    onError: (e) => toast.error(e instanceof Error ? e.message : "Save failed"),
  });
  const info = q.data;
  const helmLocked = !!info?.env_managed;
  return (
    <section className="glass-card p-5 space-y-3">
      <h3 className="text-sm font-medium">Database</h3>
      <p className="text-xs text-muted-foreground">
        {info?.driver === "postgres" ? (
          <>
            Postgres {info.user ? `${info.user}@` : ""}
            {info.host || "—"}/{info.database || "—"}
            {info.sslmode ? ` · sslmode=${info.sslmode}` : ""}
          </>
        ) : (
          <>SQLite {info?.database ? `(${info.database})` : ""}</>
        )}
      </p>
      {helmLocked ? (
        <p className="text-xs text-muted-foreground">
          DSN comes from Helm (<code className="font-mono">postgres.mode</code>
          : bundled, cnpg, or external). Change values and helm upgrade.
        </p>
      ) : (
        <>
          <p className="text-xs text-muted-foreground">
            Point this instance at a remote Postgres. Restart Wolf after save.
            Helm installs should use <code className="font-mono">postgres.mode=external</code> instead.
          </p>
          <input
            type="password"
            autoComplete="off"
            value={dsn}
            aria-label="Postgres connection string"
            disabled={save.isPending}
            onChange={(e) => setDsn(e.target.value)}
            onKeyDown={(e) => { if (e.key === "Enter" && dsn.trim() && !save.isPending) { e.preventDefault(); save.mutate(); } }}
            placeholder="postgres://user:pass@host:5432/wolf?sslmode=require"
            className="w-full h-9 px-2 rounded-md bg-muted/40 border border-border text-sm font-mono"
          />
          <div className="flex gap-2">
            <button
              type="button"
              disabled={!dsn.trim() || test.isPending}
              onClick={() => test.mutate()}
              className="h-9 px-3 rounded-md border border-border text-sm hover:bg-muted/40 disabled:opacity-50"
            >
              Test
            </button>
            {dsn.trim() && <Button type="button" size="sm" disabled={save.isPending} onClick={() => save.mutate()}>
              Save
            </Button>}
          </div>
        </>
      )}
    </section>
  );
}

function TextSetting({ label, value, type = "text", onCommit, disabled }: {
  label: string; value: string; type?: string; onCommit: (value: string) => void; disabled?: boolean;
}) {
  const id = useId();
  const [draft, setDraft] = useState(value);
  useEffect(() => setDraft(value), [value]);
  const dirty = draft !== value;
  return <form className="space-y-1" onSubmit={(e) => { e.preventDefault(); if (dirty && !disabled) onCommit(draft); }}>
    <label htmlFor={id} className="block text-xs text-muted-foreground">{label}</label>
    <div className="flex items-center gap-2">
      <input id={id} type={type} autoComplete={type === "password" ? "off" : undefined}
        value={draft} disabled={disabled} onChange={(e) => setDraft(e.target.value)}
        className="min-w-0 w-full h-9 px-3 rounded-md bg-muted/40 border border-border text-sm" />
      {dirty && <Button type="submit" size="sm" disabled={disabled}>Save</Button>}
    </div>
  </form>;
}

function BoolToggle({ label, value, onChange, disabled }: {
  label: string; value: boolean; onChange: (value: boolean) => Promise<unknown>; disabled?: boolean;
}) {
  const [draft, setDraft] = useState(value);
  const [pending, setPending] = useState(false);
  useEffect(() => setDraft(value), [value]);
  async function toggle(next: boolean) {
    setDraft(next);
    setPending(true);
    try {
      await onChange(next);
    } catch {
      // The mutation owns the error toast; restore the last confirmed value.
      setDraft(value);
    } finally {
      setPending(false);
    }
  }
  return <div className="flex min-h-9 items-center gap-3">
    <Switch aria-label={label} checked={draft} onCheckedChange={toggle} disabled={disabled || pending} />
    <span className="text-xs text-muted-foreground">{draft ? "On" : "Off"}</span>
  </div>;
}

function ChoiceSetting({ label, value, options, onCommit, disabled }: {
  label: string; value: string; options: string[]; onCommit: (value: string) => void; disabled?: boolean;
}) {
  const [draft, setDraft] = useState(value);
  useEffect(() => setDraft(value), [value]);
  const dirty = draft !== value;
  return <form className="flex items-center gap-2" onSubmit={(e) => { e.preventDefault(); if (dirty && !disabled) onCommit(draft); }}>
    <Select value={draft} onValueChange={setDraft} disabled={disabled}>
      <SelectTrigger aria-label={label} className="h-9 min-w-0"><SelectValue /></SelectTrigger>
      <SelectContent>{options.map(option => <SelectItem key={option} value={option}>{option}</SelectItem>)}</SelectContent>
    </Select>
    {dirty && <Button type="submit" size="sm" disabled={disabled}>Save</Button>}
  </form>;
}

function IntInput({ label, value, min, max, onCommit, disabled }: {
  label: string; value: string; min?: number; max?: number;
  onCommit: (v: string) => void; disabled?: boolean;
}) {
  const [draft, setDraft] = useState(value);
  useEffect(() => setDraft(value), [value]);
  const dirty = draft !== value;
  const valid = draft.trim() !== "" && Number.isInteger(Number(draft)) &&
    (min === undefined || Number(draft) >= min) && (max === undefined || Number(draft) <= max);
  return (
    <form className="flex items-center gap-2" onSubmit={(e) => { e.preventDefault(); if (dirty && valid && !disabled) onCommit(draft); }}>
      <input type="number" aria-label={label} value={draft} min={min} max={max} step={1}
        onChange={(e) => setDraft(e.target.value)}
        className="w-24 h-9 px-2 rounded-md bg-muted/40 border border-border text-sm tabular-nums"
        disabled={disabled} />
      {dirty && <Button type="submit" size="sm" disabled={disabled || !valid}>Save</Button>}
    </form>
  );
}

function DiskCard() {
  const qc = useQueryClient();
  const q = useQuery({
    queryKey: ["admin", "disk"],
    queryFn: async () => {
      try {
        return (await api.get<DiskUsage>("/admin/disk")).data ?? null;
      } catch (e) {
        if (isNotFound(e)) return null;
        throw e;
      }
    },
  });
  const reap = useMutation({
    mutationFn: () =>
      api.post<{ removed: number; retained_in_use?: number }>("/admin/workspaces/reap", { max_age_hours: 72 }),
    onSuccess: ({ data }) => {
      toast.success(`Removed ${data.removed} old workspaces${data.retained_in_use ? `; retained ${data.retained_in_use} in use` : ""}`);
      qc.invalidateQueries({ queryKey: ["admin", "disk"] });
    },
    onError: (e) =>
      toast.error(e instanceof Error ? e.message : "Reap failed"),
  });
  if (q.data == null) return null;
  const rows: [string, number][] = [
    ["Artifacts", q.data.artifacts_bytes ?? 0],
    ["Workspaces", q.data.workspaces_bytes ?? 0],
    ["Database", q.data.db_bytes ?? 0],
  ];
  return (
    <section className="glass-card p-5 space-y-3">
      <div className="flex items-center gap-2">
        <HardDriveIcon className="size-4 text-muted-foreground" />
        <h3 className="text-sm font-medium">Disk</h3>
      </div>
      <dl className="grid grid-cols-3 gap-3 text-sm">
        {rows.map(([label, bytes]) => (
          <div key={label}>
            <dt className="text-xs text-muted-foreground">{label}</dt>
            <dd className="font-mono">{formatBytes(bytes)}</dd>
          </div>
        ))}
      </dl>
      <button
        type="button"
        onClick={() => reap.mutate()}
        disabled={reap.isPending}
        className="inline-flex items-center h-9 px-3 rounded-md border border-border text-sm hover:bg-muted/30 disabled:opacity-50"
      >
        {reap.isPending ? "Reaping…" : "Reap old workspaces"}
      </button>
    </section>
  );
}

function ScannerUpdatesTab() {
  const qc = useQueryClient();
  const q = useSettingsMap();
  const save = useMutation({
    mutationFn: (updates: Record<string, string>) =>
      api.put("/settings", updates),
    onSuccess: async (_, updates) => {
      qc.setQueryData<Record<string, string>>(["settings"], (previous) => ({ ...previous, ...updates }));
      toast.success("Settings saved");
      await Promise.all([
        qc.invalidateQueries({ queryKey: ["settings"] }),
        qc.invalidateQueries({ queryKey: ["scanner-scheduling"] }),
      ]);
    },
    onError: (e) => toast.error(e instanceof Error ? e.message : "Save failed"),
  });
  const testHook = useMutation({
    mutationFn: () => api.post("/webhooks/outbound/test"),
    onSuccess: () => toast.success("Test delivery sent"),
    onError: (e) =>
      toast.error(e instanceof Error ? e.message : "Test delivery failed"),
  });

  const settings = q.data ?? {};
  const notifyOnly = settings.scanner_notify_only !== "false";

  if (q.isLoading)
    return <p className="text-sm text-muted-foreground">Loading…</p>;
  if (q.isError) return <ErrorState title="Could not load settings" onRetry={() => q.refetch()} />;

  return (
    <section className="glass-card p-5 space-y-5">
      <div>
        <h3 className="text-sm font-medium">Scanner updates</h3>
        <p className="mt-1 text-xs text-muted-foreground">
          Factory discovery, candidates, and releases stay CLI (
          <code>wolf scanner-release</code>); this toggle is the Community
          default.
        </p>
      </div>
      <div className="grid gap-3 sm:grid-cols-[1fr_240px] items-center">
        <span className="text-sm">Notify only (do not auto-promote scanner releases)</span>
        <BoolToggle label="Notify only" value={notifyOnly} disabled={save.isPending}
          onChange={(value) => save.mutateAsync({ scanner_notify_only: value ? "true" : "false" })} />
      </div>
      <div className="space-y-4">
        {[
          ["github_webhook_secret", "GitHub webhook secret", "password"],
          ["outbound_webhook_url", "Outbound webhook URL", "url"],
          ["outbound_webhook_secret", "Outbound webhook secret", "password"],
        ].map(([key, label, type]) => <TextSetting key={key} label={label} type={type}
          value={settings[key] ?? ""} disabled={save.isPending}
          onCommit={(value) => save.mutate({ [key]: value })} />)}
        <Button type="button" size="sm" variant="outline" disabled={testHook.isPending} onClick={() => testHook.mutate()}>
          {testHook.isPending ? "Sending…" : "Test delivery"}
        </Button>
      </div>
    </section>
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
  const current = status.isError ? undefined : status.data;
  const enabled = current?.enabled;
  const required = current?.required;

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
          {current ? (enabled ? "On" : "Off") : "Unavailable"}
        </span>
      </div>

      {!current && (
        <ErrorState
          title="Two-factor status unavailable"
          description="Your two-factor settings could not be checked. Retry to load them."
          onRetry={() => void status.refetch()}
        />
      )}

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
      {current && !setup && !enabled && (
        <button
          type="button"
          onClick={() => begin.mutate()}
          disabled={begin.isPending}
          className="h-9 px-4 rounded-md bg-primary text-primary-foreground text-sm font-medium disabled:opacity-50"
        >
          {begin.isPending ? "Starting…" : "Set up two-factor authentication"}
        </button>
      )}

      {current && !setup && enabled && (
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
  const isAdmin = useIsAdmin();
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
    (isAdmin || (role !== "admin" && !customScopes.includes("admin"))) &&
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
          className="text-foreground underline underline-offset-2"
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
            <Select
              value={String(expiryDays)}
              onValueChange={(value) => setExpiryDays(Number(value))}
            >
              <SelectTrigger aria-label="Expires" className="w-full h-9 px-2 rounded-md bg-muted/40 border border-border text-sm"><SelectValue /></SelectTrigger>
              <SelectContent>
              {EXPIRY_OPTIONS.map((o) => (
                <SelectItem key={o.days} value={String(o.days)}>
                  {o.label}
                </SelectItem>
              ))}
              </SelectContent>
            </Select>
          </label>
        </div>

        <div className="space-y-1.5">
          <span className="text-xs text-muted-foreground">Access</span>
          <div className="grid sm:grid-cols-2 gap-1.5">
            {ROLE_PRESETS.filter((p) => p.key !== "admin" || isAdmin).map((p) => (
              <button
                key={p.key}
                type="button"
                onClick={() => setRole(p.key)}
                aria-pressed={role === p.key}
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
            {API_SCOPES.filter((s) => s !== "admin" || isAdmin).map((s) => (
              <label
                key={s}
                className="inline-flex items-center gap-1.5 text-xs cursor-pointer select-none"
              >
                <Checkbox
                  checked={customScopes.includes(s)}
                  onCheckedChange={() => toggleScope(s)}
                  aria-label={s}
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

      <div className="glass-card overflow-hidden">
        <DataTable
          data={q.data ?? []}
          columns={[
            { key: "name", header: "Name", sortAccessor: (t) => t.name, accessor: (t) => t.name },
            { key: "prefix", header: "Prefix", sortAccessor: (t) => t.token_prefix, accessor: (t) => `${t.token_prefix}…` },
            { key: "scopes", header: "Scopes", sortAccessor: (t) => t.scopes.join(" "), accessor: (t) => t.scopes.join(", ") },
            {
              key: "expires",
              header: "Expires",
              sortAccessor: (t) => t.expires_at ?? "",
              accessor: (t) => (t.expires_at ? new Date(t.expires_at).toLocaleDateString() : "never"),
            },
            {
              key: "actions",
              header: "",
              sortable: false,
              accessor: (t) =>
                t.revoked_at ? null : (
                  <button
                    type="button"
                    onClick={() => setPending({ id: t.id, label: t.name })}
                    className="inline-flex items-center gap-1 h-7 px-2 rounded-md border border-status-error/40 text-status-error hover:bg-status-error/10 text-xs"
                  >
                    <Trash2Icon className="size-3.5" /> Revoke
                  </button>
                ),
            },
          ]}
          keyExtractor={(t) => t.id}
          persistKey="wolf-account-apikeys"
          density="compact"
          loading={q.isLoading}
          isError={q.isError}
          onRetry={() => void q.refetch()}
          emptyMessage="No API keys yet."
          searchPlaceholder="Search keys..."
        />
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
    mutationFn: ({ host, ...body }: NewSecret) =>
      // A GitLab token is only usable for a git repo when bound to its host, which
      // only the source-credentials API can record.
      host
        ? api.post("/credentials", { type: body.key_type, name: body.key_name, secret: body.value, allowed_hosts: [host] })
        : api.post("/config/secrets", body),
    onSuccess: () => {
      toast.success("Secret added");
      qc.invalidateQueries({ queryKey: ["secrets"] });
      qc.invalidateQueries({ queryKey: ["credentials"] });
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
        <DataTable
          data={q.data ?? []}
          columns={[
            { key: "type", header: "Type", sortAccessor: (s) => s.key_type, accessor: (s) => <span className="font-mono text-xs">{s.key_type}</span> },
            {
              key: "name",
              header: "Name",
              sortAccessor: (s) => s.key_name,
              accessor: (s) => (
                <span>
                  {s.key_name}
                  {s.metadata?.login ? (
                    <div className="text-[11px] text-muted-foreground">
                      {s.metadata.login}
                      {s.metadata.scopes?.length
                        ? ` · ${s.metadata.scopes.join(", ")}`
                        : ""}
                    </div>
                  ) : null}
                </span>
              ),
            },
            { key: "value", header: "Value", sortable: false, accessor: () => "••••" },
            {
              key: "actions",
              header: "",
              sortable: false,
              accessor: (s) => (
                <button
                  type="button"
                  onClick={() => setPending({ id: s.id, label: s.key_name })}
                  disabled={del.isPending}
                  className="text-muted-foreground hover:text-destructive disabled:opacity-50"
                  aria-label="Delete"
                >
                  <Trash2Icon className="size-4" />
                </button>
              ),
            },
          ]}
          keyExtractor={(s) => s.id}
          persistKey="wolf-account-secrets"
          density="compact"
          loading={q.isLoading}
          isError={q.isError}
          onRetry={() => void q.refetch()}
          emptyMessage="No secrets stored."
          searchPlaceholder="Search secrets..."
        />
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

type NewSecret = { key_type: string; key_name: string; value: string; host?: string };

// "https://GitLab.example.com/group" -> "gitlab.example.com"
const bareHost = (v: string) => v.trim().replace(/^[a-z]+:\/\//i, "").replace(/[/?#].*$/, "").toLowerCase();

function NewSecretForm({
  onSubmit,
  disabled,
}: {
  onSubmit: (b: NewSecret) => void;
  disabled?: boolean;
}) {
  const [keyType, setKeyType] = useState("github_token");
  const [keyName, setKeyName] = useState("");
  const [value, setValue] = useState("");
  const [gitlabHost, setGitlabHost] = useState("gitlab.com");
  const host = keyType === "gitlab_token" ? bareHost(gitlabHost) : "";
  const missingHost = keyType === "gitlab_token" && !host;
  return (
    <form
      className="glass-card p-4 grid md:grid-cols-[200px_1fr_1fr_auto] gap-2 items-end"
      onSubmit={(e) => {
        e.preventDefault();
        if (!keyName.trim() || !value || missingHost) return;
        onSubmit({ key_type: keyType, key_name: keyName.trim(), value, ...(host && { host }) });
        setKeyName("");
        setValue("");
      }}
    >
      <Field label="Type">
        <Select
          value={keyType}
          onValueChange={(value) => setKeyType(value)}
        >
          <SelectTrigger aria-label="Type" className="w-full h-9 px-2 rounded-md bg-muted/40 border border-border text-sm"><SelectValue /></SelectTrigger>
          <SelectContent>
          {KEY_TYPES.map((k) => (
            <SelectItem key={k.value} value={k.value}>
              {k.label}
            </SelectItem>
          ))}
          </SelectContent>
        </Select>
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
      {keyType === "gitlab_token" && (
        <div className="md:col-span-3">
          <Field label="GitLab host">
            <input
              required
              value={gitlabHost}
              onChange={(e) => setGitlabHost(e.target.value)}
              placeholder="gitlab.com"
              className="w-full h-9 px-2 rounded-md bg-muted/40 border border-border text-sm font-mono"
            />
          </Field>
          <p className="mt-1 text-xs text-muted-foreground">
            Hostname only (e.g. <code className="font-mono">gitlab.example.com</code>). The token is bound to this host
            and sent nowhere else.
          </p>
        </div>
      )}
      <button
        type="submit"
        disabled={disabled || !keyName.trim() || !value || missingHost}
        className="inline-flex items-center gap-1 h-9 px-3 rounded-md bg-primary text-primary-foreground text-sm font-medium disabled:opacity-50"
      >
        <PlusIcon className="size-4" /> Add
      </button>
      {keyType === "github_token" && (
        <div className="md:col-span-4 mt-1">
          <GitHubTokenHelp />
        </div>
      )}
      {keyType === "gitlab_token" && (
        <div className="md:col-span-4 mt-1">
          <GitLabTokenHelp />
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
      toast.success("Node saved");
      return qc.invalidateQueries({ queryKey: ["remote-nodes"] });
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
        <DataTable
          data={nodes.data ?? []}
          columns={[
            {
              key: "name",
              header: "Name",
              sortAccessor: (n) => n.name,
              accessor: (n) => (
                <span>
                  <div className="font-medium">{n.name}</div>
                  {n.base_path ? (
                    <div className="text-xs text-muted-foreground font-mono">{n.base_path}</div>
                  ) : null}
                </span>
              ),
            },
            {
              key: "host",
              header: "Host",
              sortAccessor: (n) => n.host,
              accessor: (n) => (
                <span className="font-mono text-xs">
                  {n.username}@{n.host}:{n.port || 22}
                </span>
              ),
            },
            { key: "auth", header: "Auth", sortAccessor: (n) => n.auth_type, accessor: (n) => n.auth_type },
            {
              key: "status",
              header: "Status",
              sortAccessor: (n) => (n.enabled ? "enabled" : "disabled"),
              accessor: (n) => (
                <span className="text-xs">
                  <span className={n.enabled ? "text-status-success" : "text-muted-foreground"}>
                    {n.enabled ? "enabled" : "disabled"}
                  </span>
                  {n.last_check_status ? (
                    <span className="text-muted-foreground"> · {n.last_check_status}</span>
                  ) : null}
                  {n.last_check_error ? (
                    <div className="text-destructive truncate max-w-[240px]" title={n.last_check_error}>
                      {n.last_check_error}
                    </div>
                  ) : null}
                </span>
              ),
            },
            {
              key: "actions",
              header: "",
              sortable: false,
              accessor: (n) => (
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
                  <BoolToggle label={`Enable ${n.name}`} value={n.enabled} disabled={update.isPending}
                    onChange={(enabled) => update.mutateAsync({ id: n.id, body: { enabled } })} />
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
              ),
            },
          ]}
          keyExtractor={(n) => n.id}
          persistKey="wolf-account-nodes"
          density="compact"
          loading={nodes.isLoading}
          isError={nodes.isError}
          onRetry={() => void nodes.refetch()}
          emptyMessage="No remote nodes configured."
          searchPlaceholder="Search nodes..."
        />
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
          <Select
            value={authType}
            onValueChange={(value) => {
              setAuthType(value as "private_key" | "password");
              setSecretId("");
            }}
          >
            <SelectTrigger aria-label="Auth" className="w-full h-9 px-2 rounded-md bg-muted/40 border border-border text-sm"><SelectValue /></SelectTrigger>
            <SelectContent>
            <SelectItem value="private_key">Private key</SelectItem>
            <SelectItem value="password">Password</SelectItem>
            </SelectContent>
          </Select>
        </Field>
        <Field label="Credential secret">
          <Select
            value={secretId}
            onValueChange={(value) => setSecretId(value)}
          >
            <SelectTrigger aria-label="Credential secret" className="w-full h-9 px-2 rounded-md bg-muted/40 border border-border text-sm"><SelectValue placeholder="Select secret…" /></SelectTrigger>
            <SelectContent>
            {allowedSecrets.map((s) => (
              <SelectItem key={s.id} value={s.id}>
                {s.key_name}
              </SelectItem>
            ))}
            </SelectContent>
          </Select>
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
      return qc.invalidateQueries({ queryKey: ["users"] });
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
        <DataTable
          data={q.data ?? []}
          columns={[
            {
              key: "email",
              header: "Email",
              sortAccessor: (u) => u.email,
              accessor: (u) => (
                <span>
                  {u.email}
                  {u.id === meId ? (
                    <span className="ml-2 text-[10px] uppercase tracking-wide text-muted-foreground">
                      you
                    </span>
                  ) : null}
                </span>
              ),
            },
            {
              key: "role",
              header: "Role",
              sortAccessor: (u) => u.role,
              accessor: (u) => {
                const isMe = u.id === meId;
                const isAdmin = u.role === "admin";
                const lockDemote = isMe || (isAdmin && adminCount <= 1);
                if (isMe) return <RoleBadge role={u.role} />;
                return (
                  <ChoiceSetting label={`Role for ${u.email}`} value={isAdmin ? "admin" : "user"}
                    options={["user", "admin"]} disabled={setRole.isPending || lockDemote}
                    onCommit={(role) => setRole.mutate({ id: u.id, role })} />
                );
              },
            },
            {
              key: "created",
              header: "Created",
              sortAccessor: (u) => u.created_at,
              accessor: (u) => (
                <span className="text-xs text-muted-foreground">
                  {new Date(u.created_at).toLocaleDateString()}
                </span>
              ),
            },
            {
              key: "actions",
              header: "",
              sortable: false,
              accessor: (u) =>
                u.id === meId ? null : (
                  <button
                    type="button"
                    onClick={() => setPending({ id: u.id, label: u.email })}
                    disabled={del.isPending}
                    className="text-muted-foreground hover:text-destructive disabled:opacity-50"
                    aria-label="Delete"
                  >
                    <Trash2Icon className="size-4" />
                  </button>
                ),
            },
          ]}
          keyExtractor={(u) => u.id}
          persistKey="wolf-settings-users"
          density="compact"
          loading={q.isLoading}
          isError={q.isError}
          onRetry={() => void q.refetch()}
          emptyMessage="No users."
          searchPlaceholder="Search users..."
        />
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
          <Select
            value={role}
            onValueChange={(value) => setRole(value)}
          >
            <SelectTrigger aria-label="Role" className="w-full h-9 px-2 rounded-md bg-muted/40 border border-border text-sm"><SelectValue /></SelectTrigger>
            <SelectContent>
            <SelectItem value="user">User</SelectItem>
            <SelectItem value="admin">Admin</SelectItem>
            </SelectContent>
          </Select>
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
  local_present?: boolean;
  local_digest?: string;
  remote_digest?: string;
  updates_available: boolean;
  local_error?: string;
  remote_error?: string;
}

function ScannersTab() {
  const runtimeQ = useRuntimeCapabilities();
  const dockerImageManagement = runtimeQ.data?.docker_image_management === true;
  const cfgQ = useQuery({
    queryKey: ["scanners-config"],
    queryFn: async () =>
      (await api.get<ScannersConfig>("/scanners/config")).data,
    enabled: dockerImageManagement,
  });
  const [doctor, setDoctor] = useState<DoctorResult | null>(null);

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

  if (runtimeQ.isLoading || (dockerImageManagement && cfgQ.isLoading)) {
    return (
      <p className="text-sm text-muted-foreground" role="status">
        Loading scanner runtime…
      </p>
    );
  }
  if (runtimeQ.isError || typeof runtimeQ.data?.docker_image_management !== "boolean") {
    return <p role="alert" className="text-sm text-destructive">Could not check the scanner runtime. <button type="button" className="underline" onClick={() => void runtimeQ.refetch()}>Retry runtime check</button></p>;
  }
  if (!runtimeQ.data.docker_image_management) {
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

  return (
    <section className="space-y-4">
      {/* Readiness downloads are handled by the selected scan and image list. */}
      <div className="glass-card p-5">
        <div className="flex flex-wrap items-center gap-2">
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
              <dd className="min-w-0 break-all text-right">{v}</dd>
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
  const name = (image.split("@")[0].split("/").pop() ?? "").split(":")[0];
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
    mutationFn: async () => {
      const result: PullResult = { pulled: [], errors: [] };
      for (const image of wolfBuilt.filter((item) => item.updates_available)) {
        try {
          await api.post("/scanners/images/pull", { image: image.image });
          result.pulled.push(image.image);
        } catch (e) {
          result.errors!.push({ image: image.image, error: safeErrorMessage(e) });
        }
      }
      return result;
    },
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
        pullOne={{ ...pullOne, isPending: pullOne.isPending || pullAll.isPending }}
        pullAll={{ ...pullAll, isPending: pullAll.isPending || pullOne.isPending }}
        onRefresh={() => q.refetch()}
        refreshing={q.isFetching}
        emptyLabel="No Wolf-built scanner images configured."
      />
      <ImagesCard
        title="Upstream tool images"
        description="Third-party scanners pinned to an exact version in scanner-lock.yaml. They change only when that lock is bumped."
        items={upstream}
        probe={probeState}
        pullOne={{ ...pullOne, isPending: pullOne.isPending || pullAll.isPending }}
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
  /** Only the Wolf card gets a bulk action, scoped to its outdated images. */
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
              title="Update only the outdated Wolf scanner images shown in this card"
            >
              {pullAll.isPending ? (
                <Loader2Icon className="size-3.5 animate-spin" />
              ) : (
                <DownloadIcon className="size-3.5" />
              )}
              Update Wolf images
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
              <div className="w-full min-w-0 break-all font-mono text-xs sm:w-auto sm:flex-1 sm:min-w-48">
                {img.image}
              </div>
              {img.local_present && !img.local_digest ? (
                <span className="text-xs text-muted-foreground">Present (local build)</span>
              ) : <DigestPill label="local" value={img.local_digest} err={img.local_error} />}
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
                (!(img.local_present ?? Boolean(img.local_digest)) && !img.local_error)) && (
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
            ? "Image not pulled yet. Use this image's Pull or Update action."
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
