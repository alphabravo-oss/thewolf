// Settings page — General / Secrets / Users / Scanners.
//
// Tab state lives in the URL (?tab=…) so deep links work and refresh
// preserves which section the user was on. Scan presets were deliberately
// omitted — wolf auto-detects per-repo language/framework, no manual
// preset list is needed.
import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  CheckIcon,
  DownloadIcon,
  HammerIcon,
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
  UploadCloudIcon,
  UserIcon,
  UsersIcon,
} from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { toast } from "sonner";
import { api } from "@/lib/api";
import { useMe } from "@/lib/me";
import type { ApiToken, ApiTokenCreated, RemoteNode } from "@/lib/types";
import { BuildConsole, type BuildTarget } from "@/components/scanners/build-console";
import { DockerHubCredentialCard } from "@/components/scanners/dockerhub-credential";
import {
  useScannerImages,
  type ScannerImageStatus,
} from "@/lib/scanner-build";

export const Route = createFileRoute("/_authed/settings")({
  validateSearch: (s: Record<string, unknown>) => ({
    tab:
      typeof s.tab === "string" &&
      /^(account|general|security|apikeys|secrets|nodes|users|scanners)$/.test(s.tab)
        ? (s.tab as TabKey)
        : ("account" as TabKey),
  }),
  component: SettingsPage,
});

type TabKey =
  | "account"
  | "general"
  | "security"
  | "apikeys"
  | "secrets"
  | "nodes"
  | "users"
  | "scanners";

// adminOnly tabs are hidden for regular users. Account, Security, API Keys,
// Secrets + Nodes are per-user, so everyone can manage their own.
const TABS: { key: TabKey; label: string; Icon: typeof SettingsIcon; adminOnly?: boolean }[] = [
  { key: "account", label: "Account", Icon: UserIcon },
  { key: "general", label: "General", Icon: SettingsIcon, adminOnly: true },
  { key: "security", label: "Security", Icon: LockIcon },
  { key: "apikeys", label: "API Keys", Icon: KeyRoundIcon },
  { key: "secrets", label: "Secrets", Icon: KeyIcon },
  { key: "nodes", label: "Nodes", Icon: ServerIcon },
  { key: "users", label: "Users", Icon: UsersIcon, adminOnly: true },
  { key: "scanners", label: "Scanners", Icon: ShieldIcon, adminOnly: true },
];

function SettingsPage() {
  const { tab } = Route.useSearch();
  const navigate = useNavigate();
  const me = useMe();
  const isAdmin = me.data?.role === "admin";

  // Regular users see only the per-user tabs (Secrets, Nodes). Admins see all.
  const visibleTabs = TABS.filter((t) => isAdmin || !t.adminOnly);
  // If a non-admin lands on an admin tab (e.g. a saved ?tab=users link),
  // fall back to the first tab they can see.
  const activeTab = visibleTabs.some((t) => t.key === tab)
    ? tab
    : visibleTabs[0]?.key ?? "secrets";

  return (
    <div className="page stack page--narrow">
      <h1 className="text-2xl font-semibold tracking-tight">Settings</h1>
      <nav className="flex gap-1 border-b border-border/40">
        {visibleTabs.map(({ key, label, Icon }) => {
          const active = activeTab === key;
          return (
            <button
              key={key}
              type="button"
              onClick={() => navigate({ to: "/settings", search: { tab: key } })}
              className={
                "inline-flex items-center gap-1.5 px-3 h-9 text-sm border-b-2 -mb-px " +
                (active
                  ? "border-primary text-foreground"
                  : "border-transparent text-muted-foreground hover:text-foreground")
              }
            >
              <Icon className="size-4" /> {label}
            </button>
          );
        })}
      </nav>

      {activeTab === "account" && <AccountTab />}
      {activeTab === "general" && isAdmin && <GeneralTab />}
      {activeTab === "security" && <SecurityTab />}
      {activeTab === "apikeys" && <ApiKeysTab />}
      {activeTab === "secrets" && <SecretsTab />}
      {activeTab === "nodes" && <NodesTab />}
      {activeTab === "users" && isAdmin && <UsersTab />}
      {activeTab === "scanners" && isAdmin && <ScannersTab />}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Account — per-user profile (display name, email, password) + links to the
// other personal surfaces (API keys, two-factor).
// ---------------------------------------------------------------------------

function AccountTab() {
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
  const emailChanged = !!me.data && email.trim().toLowerCase() !== me.data.email;

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
      api.put("/auth/password", { current_password: curPw, new_password: newPw }),
    onSuccess: () => {
      toast.success("Password updated");
      setCurPw("");
      setNewPw("");
      setConfirmPw("");
    },
    onError: (e) => toast.error(e instanceof Error ? e.message : "Update failed"),
  });

  if (me.isLoading) return <p className="text-sm text-muted-foreground">Loading…</p>;
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
            className="w-full h-9 px-3 rounded-md bg-muted/40 border border-border/40 text-sm"
          />
        </label>
        <label className="block space-y-1">
          <span className="text-xs text-muted-foreground">Email</span>
          <input
            type="email"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            className="w-full h-9 px-3 rounded-md bg-muted/40 border border-border/40 text-sm"
          />
        </label>
        {emailChanged && (
          <label className="block space-y-1">
            <span className="text-xs text-muted-foreground">
              Current password <span className="text-amber-400">(required to change email)</span>
            </span>
            <input
              type="password"
              value={currentPw}
              onChange={(e) => setCurrentPw(e.target.value)}
              autoComplete="current-password"
              className="w-full h-9 px-3 rounded-md bg-muted/40 border border-border/40 text-sm"
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
              className="w-full h-9 px-3 rounded-md bg-muted/40 border border-border/40 text-sm"
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
              className="w-full h-9 px-3 rounded-md bg-muted/40 border border-border/40 text-sm"
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
                (pwMismatch ? "border-red-500" : "border-border/40")
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
            onClick={() => navigate({ to: "/settings", search: { tab: "apikeys" } })}
            className="inline-flex items-center gap-1.5 h-9 px-3 rounded-md border border-border/40 text-sm hover:bg-muted/40"
          >
            <KeyRoundIcon className="size-4" /> API Keys
          </button>
          <button
            type="button"
            onClick={() => navigate({ to: "/settings", search: { tab: "security" } })}
            className="inline-flex items-center gap-1.5 h-9 px-3 rounded-md border border-border/40 text-sm hover:bg-muted/40"
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
    help: "Master switch for the autonomous fix engine. v1 is dry-run, per-finding, verified, and branch-only — it produces a fix branch + diff for review and never pushes or opens a PR. When off, the Fixes surface, the worker, and the execute API are all dark. Default off.",
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
      const r = await api.get<SettingRow[] | Record<string, string>>("/settings");
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
    mutationFn: (updates: Record<string, string>) => api.put("/settings", updates),
    onSuccess: () => {
      toast.success("Settings saved");
      qc.invalidateQueries({ queryKey: ["settings"] });
    },
    onError: (e) => toast.error(e instanceof Error ? e.message : "Save failed"),
  });

  if (q.isLoading) return <p className="text-sm text-muted-foreground">Loading…</p>;
  const settings = q.data ?? {};

  return (
    <section className="glass-card p-5 space-y-5">
      {GENERAL_KNOBS.map((knob) => {
        const current = settings[knob.key] ?? "";
        return (
          <div key={knob.key} className="grid md:grid-cols-[1fr_240px] gap-4 items-start">
            <div>
              <label className="text-sm font-medium inline-flex items-center gap-1.5">
                {knob.label}
                {"alpha" in knob && knob.alpha && (
                  <span className="rounded px-1.5 py-0.5 text-3xs font-semibold uppercase tracking-wide bg-amber-500/15 text-amber-500 border border-amber-500/30">
                    Alpha
                  </span>
                )}
              </label>
              <p className="text-xs text-muted-foreground mt-0.5">{knob.help}</p>
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
                className="w-full h-9 px-2 rounded-md bg-muted/40 border border-border/40 text-sm"
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
          ? "bg-emerald-500/10 border-emerald-500/40 text-emerald-300"
          : "bg-muted/40 border-border/40 text-muted-foreground hover:text-foreground") +
        " disabled:opacity-50"
      }
    >
      <span
        className={
          "size-2 rounded-full " + (value ? "bg-emerald-400" : "bg-muted-foreground/50")
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
        className="w-24 h-9 px-2 rounded-md bg-muted/40 border border-border/40 text-sm tabular-nums"
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

function SecurityTab() {
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
    onError: (e) => toast.error(e instanceof Error ? e.message : "Could not start setup"),
  });
  const activate = useMutation({
    mutationFn: async () =>
      (await api.post<{ recovery_codes: string[] }>("/auth/mfa/activate", { code })).data,
    onSuccess: (d) => {
      setRecovery(d?.recovery_codes ?? []);
      setSetup(null);
      setCode("");
      qc.invalidateQueries({ queryKey: ["mfa-status"] });
      toast.success("Two-factor authentication enabled");
    },
    onError: (e) => toast.error(e instanceof Error ? e.message : "That code is not valid"),
  });
  const disable = useMutation({
    mutationFn: async () => api.post("/auth/mfa/disable", { code }),
    onSuccess: () => {
      setCode("");
      qc.invalidateQueries({ queryKey: ["mfa-status"] });
      toast.success("Two-factor authentication disabled");
    },
    onError: (e) => toast.error(e instanceof Error ? e.message : "That code is not valid"),
  });

  if (status.isLoading) return <p className="text-sm text-muted-foreground">Loading…</p>;
  const enabled = status.data?.enabled ?? false;
  const required = status.data?.required ?? false;

  return (
    <section className="glass-card p-5 space-y-5 max-w-xl">
      <div className="flex items-start gap-3">
        <LockIcon className="size-5 mt-0.5 text-muted-foreground" />
        <div>
          <h2 className="text-sm font-medium">Two-factor authentication</h2>
          <p className="text-xs text-muted-foreground mt-0.5">
            Protect your account with a time-based code from an authenticator app
            (Google Authenticator, 1Password, Authy, …) in addition to your password.
          </p>
        </div>
        <span
          className={
            "ml-auto shrink-0 rounded px-2 py-0.5 text-xs font-medium border " +
            (enabled
              ? "bg-emerald-500/10 border-emerald-500/40 text-emerald-300"
              : "bg-muted/40 border-border/40 text-muted-foreground")
          }
        >
          {enabled ? "On" : "Off"}
        </span>
      </div>

      {/* One-time recovery codes, shown right after activation. */}
      {recovery && (
        <div className="rounded-md border border-amber-500/30 bg-amber-500/5 p-4 space-y-2">
          <p className="text-sm font-medium text-amber-300">Save your recovery codes</p>
          <p className="text-xs text-muted-foreground">
            Each code works once if you lose your device. Store them somewhere safe —
            they won't be shown again.
          </p>
          <div className="grid grid-cols-2 gap-1.5 font-mono text-sm">
            {recovery.map((c) => (
              <span key={c} className="rounded bg-muted/40 px-2 py-1 tracking-wider">
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
            1. Scan this with your authenticator app, then enter the 6-digit code to confirm.
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
              className="w-32 h-9 px-3 rounded-md bg-muted/40 border border-border/40 text-sm font-mono tracking-widest"
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
              Your administrator requires two-factor authentication, so it can't be turned off.
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
                className="w-44 h-9 px-3 rounded-md bg-muted/40 border border-border/40 text-sm font-mono"
              />
              <button
                type="button"
                onClick={() => disable.mutate()}
                disabled={disable.isPending || code.length < 6}
                className="h-9 px-4 rounded-md border border-red-500/40 text-red-300 hover:bg-red-500/10 text-sm disabled:opacity-50"
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
  "read:loops",
  "write:loops",
  "read:config",
  "write:config",
  "admin",
] as const;

// Role presets map to the scope aliases the backend (apikey.ParseScopes) knows.
const ROLE_PRESETS = [
  { key: "read-only", label: "Read-only", help: "Read every resource; no writes." },
  { key: "read-write", label: "Read & write", help: "Read and write everything except admin." },
  { key: "admin", label: "Admin (full)", help: "Full access, including settings and users." },
  { key: "custom", label: "Custom", help: "Pick exact scopes." },
] as const;

const EXPIRY_OPTIONS = [
  { days: 30, label: "30 days" },
  { days: 90, label: "90 days" },
  { days: 365, label: "1 year" },
  { days: 0, label: "Never" },
];

function ApiKeysTab() {
  const qc = useQueryClient();
  const q = useQuery({
    queryKey: ["api-tokens"],
    queryFn: async () => (await api.get<ApiToken[]>("/auth/tokens")).data ?? [],
  });

  const [name, setName] = useState("");
  const [role, setRole] = useState<(typeof ROLE_PRESETS)[number]["key"]>("read-only");
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
    onError: (e) => toast.error(e instanceof Error ? e.message : "Could not create key"),
  });
  const revoke = useMutation({
    mutationFn: (id: string) => api.delete(`/auth/tokens/${id}`),
    onSuccess: () => {
      toast.success("Key revoked");
      qc.invalidateQueries({ queryKey: ["api-tokens"] });
    },
    onError: (e) => toast.error(e instanceof Error ? e.message : "Revoke failed"),
  });

  const toggleScope = (s: string) =>
    setCustomScopes((prev) => (prev.includes(s) ? prev.filter((x) => x !== s) : [...prev, s]));

  const canCreate =
    name.trim().length > 0 && (role !== "custom" || customScopes.length > 0) && !create.isPending;

  const origin = typeof window !== "undefined" ? window.location.origin : "https://wolf.example.com";

  return (
    <section className="space-y-4 max-w-2xl">
      <p className="text-sm text-muted-foreground">
        API keys are scoped, revocable credentials for the{" "}
        <code className="text-foreground">wolf</code> CLI, CI pipelines, and agents. They{" "}
        <strong>bypass two-factor auth</strong> by design, so treat them like passwords. Browse the
        full API at{" "}
        <a href="/api/v1/docs" target="_blank" rel="noreferrer" className="text-foreground hover:underline">
          /api/v1/docs
        </a>
        .
      </p>

      {/* One-time secret reveal. */}
      {created && (
        <div className="rounded-md border border-emerald-500/30 bg-emerald-500/5 p-4 space-y-3">
          <p className="text-sm font-medium text-emerald-300">
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
            <p className="text-xs text-muted-foreground">Use it with the CLI:</p>
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
              className="w-full h-9 px-3 rounded-md bg-muted/40 border border-border/40 text-sm"
            />
          </label>
          <label className="block space-y-1">
            <span className="text-xs text-muted-foreground">Expires</span>
            <select
              value={expiryDays}
              onChange={(e) => setExpiryDays(Number(e.target.value))}
              className="w-full h-9 px-2 rounded-md bg-muted/40 border border-border/40 text-sm"
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
                    : "border-border/40 hover:border-border")
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
                <span className={s === "admin" ? "text-amber-300 font-mono" : "font-mono"}>{s}</span>
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
          <div className="p-5 text-sm text-muted-foreground">No API keys yet.</div>
        ) : (
          <table className="w-full text-sm">
            <thead className="text-xs text-muted-foreground border-b border-border/30">
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
                const expired = !!t.expires_at && new Date(t.expires_at) < new Date();
                return (
                  <tr key={t.id} className="border-t border-border/20 align-top">
                    <td className="px-4 py-2">
                      <div className="font-medium">{t.name}</div>
                      {(revoked || expired) && (
                        <span className="text-[10px] uppercase tracking-wide text-muted-foreground">
                          {revoked ? "revoked" : "expired"}
                        </span>
                      )}
                    </td>
                    <td className="px-4 py-2 font-mono text-xs text-muted-foreground">{t.token_prefix}…</td>
                    <td className="px-4 py-2">
                      <div className="flex flex-wrap gap-1">
                        {t.scopes.map((s) => (
                          <span
                            key={s}
                            className="text-[10px] font-mono rounded bg-muted/40 border border-border/40 px-1.5 py-0.5"
                          >
                            {s}
                          </span>
                        ))}
                      </div>
                    </td>
                    <td className="px-4 py-2 text-xs text-muted-foreground">
                      {t.expires_at ? new Date(t.expires_at).toLocaleDateString() : "never"}
                    </td>
                    <td className="px-4 py-2 text-right">
                      {!revoked && (
                        <button
                          type="button"
                          onClick={() => revoke.mutate(t.id)}
                          className="inline-flex items-center gap-1 h-7 px-2 rounded-md border border-red-500/40 text-red-300 hover:bg-red-500/10 text-xs"
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
  created_at: string;
}

const KEY_TYPES = [
  { value: "github_token", label: "GitHub token" },
  { value: "gitlab_token", label: "GitLab token" },
  { value: "ssh_private_key", label: "SSH private key" },
  { value: "ssh_password", label: "SSH password" },
  { value: "anthropic_key", label: "Anthropic API key" },
  { value: "openai_key", label: "OpenAI API key" },
  { value: "custom", label: "Custom" },
];

function SecretsTab() {
  const qc = useQueryClient();
  const q = useQuery({
    queryKey: ["secrets"],
    queryFn: async () => {
      const r = await api.get<MaskedSecret[]>("/config/secrets");
      return r.data ?? [];
    },
  });
  const del = useMutation({
    mutationFn: (id: string) => api.delete(`/config/secrets/${id}`),
    onSuccess: () => {
      toast.success("Secret deleted");
      qc.invalidateQueries({ queryKey: ["secrets"] });
    },
    onError: (e) => toast.error(e instanceof Error ? e.message : "Delete failed"),
  });
  const create = useMutation({
    mutationFn: (body: { key_type: string; key_name: string; value: string }) =>
      api.post("/config/secrets", body),
    onSuccess: () => {
      toast.success("Secret added");
      qc.invalidateQueries({ queryKey: ["secrets"] });
    },
    onError: (e) => toast.error(e instanceof Error ? e.message : "Create failed"),
  });

  return (
    <section className="space-y-4">
      <NewSecretForm onSubmit={(b) => create.mutate(b)} disabled={create.isPending} />
      <div className="glass-card overflow-hidden">
        {q.isLoading ? (
          <div className="p-5 text-sm text-muted-foreground">Loading…</div>
        ) : !q.data || q.data.length === 0 ? (
          <div className="p-5 text-sm text-muted-foreground">No secrets stored.</div>
        ) : (
          <table className="w-full text-sm">
            <thead className="text-xs text-muted-foreground border-b border-border/30">
              <tr>
                <th className="text-left px-4 py-2">Type</th>
                <th className="text-left px-4 py-2">Name</th>
                <th className="text-left px-4 py-2">Value</th>
                <th className="text-right px-4 py-2 w-12"></th>
              </tr>
            </thead>
            <tbody>
              {q.data.map((s) => (
                <tr key={s.id} className="border-t border-border/20">
                  <td className="px-4 py-2 font-mono text-xs">{s.key_type}</td>
                  <td className="px-4 py-2">{s.key_name}</td>
                  <td className="px-4 py-2 font-mono text-xs text-muted-foreground">{s.value}</td>
                  <td className="px-4 py-2 text-right">
                    <button
                      type="button"
                      onClick={() => {
                        if (window.confirm(`Delete secret "${s.key_name}"? This cannot be undone.`)) {
                          del.mutate(s.id);
                        }
                      }}
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
          className="w-full h-9 px-2 rounded-md bg-muted/40 border border-border/40 text-sm"
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
          className="w-full h-9 px-2 rounded-md bg-muted/40 border border-border/40 text-sm"
        />
      </Field>
      <Field label="Value">
        <input
          required
          type="password"
          value={value}
          onChange={(e) => setValue(e.target.value)}
          placeholder="paste secret"
          className="w-full h-9 px-2 rounded-md bg-muted/40 border border-border/40 text-sm font-mono"
        />
      </Field>
      <button
        type="submit"
        disabled={disabled || !keyName.trim() || !value}
        className="inline-flex items-center gap-1 h-9 px-3 rounded-md bg-primary text-primary-foreground text-sm font-medium disabled:opacity-50"
      >
        <PlusIcon className="size-4" /> Add
      </button>
    </form>
  );
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
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

function NodesTab() {
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
    onError: (e) => toast.error(e instanceof Error ? e.message : "Create failed"),
  });
  const update = useMutation({
    mutationFn: ({ id, body }: { id: string; body: Record<string, unknown> }) =>
      api.put(`/nodes/${id}`, body),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["remote-nodes"] });
    },
    onError: (e) => toast.error(e instanceof Error ? e.message : "Update failed"),
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
  const del = useMutation({
    mutationFn: (id: string) => api.delete(`/nodes/${id}`),
    onSuccess: () => {
      toast.success("Node deleted");
      qc.invalidateQueries({ queryKey: ["remote-nodes"] });
    },
    onError: (e) => toast.error(e instanceof Error ? e.message : "Delete failed"),
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
          <div className="p-5 text-sm text-muted-foreground">No remote nodes configured.</div>
        ) : (
          <table className="w-full text-sm">
            <thead className="text-xs text-muted-foreground border-b border-border/30">
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
                <tr key={n.id} className="border-t border-border/20">
                  <td className="px-4 py-2">
                    <div className="font-medium">{n.name}</div>
                    {n.base_path && (
                      <div className="text-xs text-muted-foreground font-mono">{n.base_path}</div>
                    )}
                  </td>
                  <td className="px-4 py-2 font-mono text-xs">
                    {n.username}@{n.host}:{n.port || 22}
                  </td>
                  <td className="px-4 py-2 text-xs">{n.auth_type}</td>
                  <td className="px-4 py-2 text-xs">
                    <span className={n.enabled ? "text-emerald-500" : "text-muted-foreground"}>
                      {n.enabled ? "enabled" : "disabled"}
                    </span>
                    {n.last_check_status && (
                      <span className="text-muted-foreground"> · {n.last_check_status}</span>
                    )}
                    {n.last_check_error && (
                      <div className="text-destructive truncate max-w-[240px]" title={n.last_check_error}>
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
                        {check.isPending ? <Loader2Icon className="size-4 animate-spin" /> : <CheckIcon className="size-4" />}
                      </button>
                      <button
                        type="button"
                        onClick={() => update.mutate({ id: n.id, body: { enabled: !n.enabled } })}
                        disabled={update.isPending}
                        className="h-8 px-2 rounded-md border border-border/40 text-xs hover:bg-muted/40 disabled:opacity-50"
                      >
                        {n.enabled ? "Disable" : "Enable"}
                      </button>
                      <button
                        type="button"
                        onClick={() => {
                          if (window.confirm(`Delete node "${n.name}"? Repos using it must be removed first.`)) del.mutate(n.id);
                        }}
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
  const [authType, setAuthType] = useState<"private_key" | "password">("private_key");
  const [secretId, setSecretId] = useState("");
  const [knownHosts, setKnownHosts] = useState("");
  const [basePath, setBasePath] = useState("");
  const allowedSecrets = secrets.filter((s) =>
    authType === "private_key" ? s.key_type === "ssh_private_key" : s.key_type === "ssh_password",
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
          <input required value={name} onChange={(e) => setName(e.target.value)} placeholder="dev-box" className="w-full h-9 px-2 rounded-md bg-muted/40 border border-border/40 text-sm" />
        </Field>
        <Field label="Host">
          <input required value={host} onChange={(e) => setHost(e.target.value)} placeholder="dev.example.com" className="w-full h-9 px-2 rounded-md bg-muted/40 border border-border/40 text-sm font-mono" />
        </Field>
        <Field label="Port">
          <input required value={port} onChange={(e) => setPort(e.target.value)} inputMode="numeric" className="w-full h-9 px-2 rounded-md bg-muted/40 border border-border/40 text-sm" />
        </Field>
        <Field label="Username">
          <input required value={username} onChange={(e) => setUsername(e.target.value)} placeholder="alice" className="w-full h-9 px-2 rounded-md bg-muted/40 border border-border/40 text-sm" />
        </Field>
      </div>
      <div className="grid md:grid-cols-[180px_1fr_1fr_auto] gap-2 items-end">
        <Field label="Auth">
          <select value={authType} onChange={(e) => { setAuthType(e.target.value as "private_key" | "password"); setSecretId(""); }} className="w-full h-9 px-2 rounded-md bg-muted/40 border border-border/40 text-sm">
            <option value="private_key">Private key</option>
            <option value="password">Password</option>
          </select>
        </Field>
        <Field label="Credential secret">
          <select value={secretId} onChange={(e) => setSecretId(e.target.value)} className="w-full h-9 px-2 rounded-md bg-muted/40 border border-border/40 text-sm">
            <option value="">Select secret…</option>
            {allowedSecrets.map((s) => (
              <option key={s.id} value={s.id}>{s.key_name}</option>
            ))}
          </select>
        </Field>
        <Field label="Base path">
          <input value={basePath} onChange={(e) => setBasePath(e.target.value)} placeholder="/home/alice/code" className="w-full h-9 px-2 rounded-md bg-muted/40 border border-border/40 text-sm font-mono" />
        </Field>
        <button type="submit" disabled={disabled || !secretId || !name.trim() || !host.trim() || !username.trim()} className="inline-flex items-center gap-1 h-9 px-3 rounded-md bg-primary text-primary-foreground text-sm font-medium disabled:opacity-50">
          <PlusIcon className="size-4" /> Add
        </button>
      </div>
      <Field label="Known hosts">
        <textarea value={knownHosts} onChange={(e) => setKnownHosts(e.target.value)} placeholder="dev.example.com ssh-ed25519 AAAA…" className="w-full min-h-20 px-2 py-2 rounded-md bg-muted/40 border border-border/40 text-sm font-mono" />
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
    onError: (e) => toast.error(e instanceof Error ? e.message : "Create failed"),
  });
  const setRole = useMutation({
    mutationFn: ({ id, role }: { id: string; role: string }) =>
      api.put(`/users/${id}/role`, { role }),
    onSuccess: () => {
      toast.success("Role updated");
      qc.invalidateQueries({ queryKey: ["users"] });
    },
    onError: (e) => toast.error(e instanceof Error ? e.message : "Role change failed"),
  });
  const del = useMutation({
    mutationFn: (id: string) => api.delete(`/users/${id}`),
    onSuccess: () => {
      toast.success("User deleted");
      qc.invalidateQueries({ queryKey: ["users"] });
    },
    onError: (e) => toast.error(e instanceof Error ? e.message : "Delete failed"),
  });
  const meId = meQ.data?.id;
  const adminCount = (q.data ?? []).filter((u) => u.role === "admin").length;
  return (
    <section className="space-y-4">
      <NewUserForm onSubmit={(b) => create.mutate(b)} disabled={create.isPending} />
      <div className="glass-card overflow-hidden">
        {q.isLoading ? (
          <div className="p-5 text-sm text-muted-foreground">Loading…</div>
        ) : (
          <table className="w-full text-sm">
            <thead className="text-xs text-muted-foreground border-b border-border/30">
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
                  <tr key={u.id} className="border-t border-border/20">
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
                          onChange={(e) => setRole.mutate({ id: u.id, role: e.target.value })}
                          className="h-7 px-1.5 rounded-md bg-muted/40 border border-border/40 text-xs disabled:opacity-60"
                          title={
                            lockDemote ? "There must be at least one admin" : "Change role"
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
                          onClick={() => {
                            if (window.confirm(`Delete user "${u.email}"? This cannot be undone.`)) {
                              del.mutate(u.id);
                            }
                          }}
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
          : "bg-muted/40 text-muted-foreground border-border/40")
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
          className="inline-flex items-center gap-1.5 h-9 px-3 rounded-md border border-border/60 text-sm hover:bg-muted/30"
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
        onSubmit({ email: email.trim().toLowerCase(), password, role });
        reset();
      }}
    >
      <div className="grid sm:grid-cols-3 gap-3">
        <Field label="Email">
          <input
            required
            autoFocus
            type="email"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            placeholder="user@example.com"
            className="w-full h-9 px-2 rounded-md bg-muted/40 border border-border/40 text-sm"
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
            className="w-full h-9 px-2 rounded-md bg-muted/40 border border-border/40 text-sm"
          />
        </Field>
        <Field label="Role">
          <select
            value={role}
            onChange={(e) => setRole(e.target.value)}
            className="w-full h-9 px-2 rounded-md bg-muted/40 border border-border/40 text-sm"
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
          className="h-9 px-3 rounded-md border border-border/60 text-sm hover:bg-muted/30"
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

interface ScannerToolStatus {
  name: string;
  display_name: string;
  category: string;
  integration_tier: "default" | "bucket" | "upstream" | string;
  bucket?: string;
  pinned_version?: string;
  latest_version?: string;
  latest_reference?: string;
  freshness_status?: string;
  version_check_error?: string;
  version_checked_at?: string;
  canonical_image?: string;
  configured_image?: string;
  image_present?: boolean;
  overridden: boolean;
  uses_latest_tag: boolean;
}

interface ScannerVersionCheck {
  tool_name: string;
  pinned_version: string;
  latest_version?: string;
  latest_reference?: string;
  status: string;
  checked_at: string;
  error?: string;
  source_type: string;
  source_url?: string;
}

function ScannersTab() {
  const qc = useQueryClient();
  const cfgQ = useQuery({
    queryKey: ["scanners-config"],
    queryFn: async () => (await api.get<ScannersConfig>("/scanners/config")).data,
  });
  const [doctor, setDoctor] = useState<DoctorResult | null>(null);
  const [pull, setPull] = useState<PullResult | null>(null);

  const doctorMut = useMutation({
    mutationFn: async () => (await api.post<DoctorResult>("/scanners/doctor")).data,
    onSuccess: (d) => {
      setDoctor(d);
      if (d?.overall_ok) toast.success("All scanner checks passed");
      else toast.error("Scanner checks failed — see report below");
    },
    onError: (e) =>
      toast.error(e instanceof Error ? e.message : "Doctor failed"),
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
    },
    onError: (e) =>
      toast.error(e instanceof Error ? e.message : "Pull failed"),
  });

  if (cfgQ.isLoading) {
    return <p className="text-sm text-muted-foreground">Loading…</p>;
  }
  if (cfgQ.isError || !cfgQ.data) {
    return (
      <p className="text-sm text-destructive">
        Failed to load scanner config — is the container backend initialized?
      </p>
    );
  }

  const cfg = cfgQ.data;
  const rows: Array<[string, React.ReactNode]> = [
    ["Default image", <code className="text-xs">{cfg.image}</code>],
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
            className="inline-flex items-center gap-1.5 h-9 px-3 rounded-md border border-border/60 text-sm hover:bg-muted/30 disabled:opacity-50"
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
            wolf-scanners* images are published to Docker Hub
            (alphabravodevops/*); first scan triggers an auto-pull
            per the IfNotPresent policy, but pre-pulling saves the
            wait on the first real scan. If pull errors out for the
            default image, surface the registry hint as a banner. */}
        {wolfBuiltMissing && (
          <div className="mt-3 rounded-md border border-amber-500/40 bg-amber-500/10 px-3 py-2 text-xs text-amber-200">
            <strong>Couldn't pull {cfg.image}.</strong> Check your network /
            registry credentials. The default registry is{" "}
            <code>docker.io/alphabravodevops</code>; override via{" "}
            <code>WOLF_SCANNERS_IMAGE</code> to point at a mirror or a
            locally-built image.
          </div>
        )}

        {/* Doctor result panel. */}
        {doctor && (
          <div className="mt-4 rounded-md border border-border/40 bg-muted/20 p-3 text-sm space-y-1">
            <div className="flex items-center gap-2 mb-1">
              {doctor.overall_ok ? (
                <CheckIcon className="size-4 text-emerald-400" />
              ) : (
                <span className="text-destructive">●</span>
              )}
              <span className="font-medium">
                {doctor.overall_ok
                  ? "All checks passed"
                  : "Some checks failed"}
              </span>
            </div>
            <ul className="text-xs space-y-0.5 font-mono">
              {doctor.checks.map((c, i) => (
                <li key={i} className="flex items-start gap-2">
                  <span
                    className={c.ok ? "text-emerald-400" : "text-destructive"}
                  >
                    {c.ok ? "✓" : "✗"}
                  </span>
                  <span className="flex-1">
                    {c.label}
                    {c.detail && (
                      <span className="text-muted-foreground">
                        {" "}
                        — {c.detail}
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
          <div className="mt-4 rounded-md border border-border/40 bg-muted/20 p-3 text-sm space-y-1">
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
                    {e.image}: {e.error}
                  </li>
                ))}
              </ul>
            )}
          </div>
        )}
      </div>

      <ScannerImagesPanel />
      <ImagesPanel />
      <ScannerToolsPanel />

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
              <li key={tool} className="flex items-center gap-3 font-mono text-xs">
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

function ScannerToolsPanel() {
  const qc = useQueryClient();
  const q = useQuery({
    queryKey: ["scanner-tools"],
    queryFn: async () =>
      (await api.get<ScannerToolStatus[]>("/scanners/tools")).data ?? [],
    refetchOnWindowFocus: false,
  });
  const checkAll = useMutation({
    mutationFn: async () =>
      (
        await api.post<ScannerVersionCheck[]>("/scanners/tools/check-updates", {
          force: true,
        })
      ).data ?? [],
    onSuccess: (rows) => {
      toast.success(`Checked ${rows.length} scanner tool(s)`);
      qc.invalidateQueries({ queryKey: ["scanner-tools"] });
    },
    onError: (e) =>
      toast.error(e instanceof Error ? e.message : "Version check failed"),
  });
  const checkOne = useMutation({
    mutationFn: async (name: string) =>
      (
        await api.post<ScannerVersionCheck>(
          `/scanners/tools/${encodeURIComponent(name)}/check-update`,
        )
      ).data,
    onSuccess: (_, name) => {
      toast.success(`Checked ${name}`);
      qc.invalidateQueries({ queryKey: ["scanner-tools"] });
    },
    onError: (e) =>
      toast.error(e instanceof Error ? e.message : "Version check failed"),
  });

  const tools = q.data ?? [];
  const updateCount = tools.filter(
    (t) => t.freshness_status === "update_available",
  ).length;

  return (
    <div className="glass-card p-5">
      <div className="flex flex-wrap items-center justify-between gap-3 mb-3">
        <div>
          <h3 className="text-sm font-medium">Scanner tools</h3>
          <p className="text-xs text-muted-foreground mt-0.5">
            {tools.length} tools
            {updateCount > 0 && (
              <span className="text-amber-300"> · {updateCount} update available</span>
            )}
          </p>
        </div>
        <button
          type="button"
          onClick={() => checkAll.mutate()}
          disabled={checkAll.isPending}
          className="inline-flex items-center gap-1 h-8 px-2.5 rounded-md border border-border/60 text-xs hover:bg-muted/30 disabled:opacity-50"
        >
          {checkAll.isPending ? (
            <Loader2Icon className="size-3.5 animate-spin" />
          ) : (
            <RefreshCwIcon className="size-3.5" />
          )}
          Check tool versions
        </button>
      </div>

      {q.isLoading ? (
        <p className="text-xs text-muted-foreground">Loading scanner tools…</p>
      ) : q.isError ? (
        <p className="text-xs text-destructive">Failed to load scanner tools.</p>
      ) : tools.length > 0 ? (
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead className="text-xs text-muted-foreground border-b border-border/30">
              <tr>
                <th className="text-left font-medium py-2 pr-3">Tool</th>
                <th className="text-left font-medium py-2 pr-3">Tier</th>
                <th className="text-left font-medium py-2 pr-3">Pinned</th>
                <th className="text-left font-medium py-2 pr-3">Latest</th>
                <th className="text-left font-medium py-2 pr-3">Status</th>
                <th className="text-left font-medium py-2 pr-3">Image</th>
                <th className="text-right font-medium py-2 pl-3">Action</th>
              </tr>
            </thead>
            <tbody>
              {tools.map((tool) => (
                <tr key={tool.name} className="border-b border-border/15 last:border-0">
                  <td className="py-2 pr-3">
                    <div className="font-medium">{tool.display_name || tool.name}</div>
                    <div className="text-[11px] text-muted-foreground">
                      {tool.name} · {tool.category}
                    </div>
                  </td>
                  <td className="py-2 pr-3">
                    <TierPill tool={tool} />
                  </td>
                  <td className="py-2 pr-3 font-mono text-xs">
                    {tool.pinned_version || "—"}
                  </td>
                  <td className="py-2 pr-3 font-mono text-xs">
                    {tool.latest_version || "—"}
                  </td>
                  <td className="py-2 pr-3">
                    <FreshnessPill tool={tool} />
                  </td>
                  <td className="py-2 pr-3 max-w-[260px]">
                    <div
                      className="font-mono text-xs truncate"
                      title={tool.configured_image || tool.canonical_image || ""}
                    >
                      {tool.configured_image || tool.canonical_image || "—"}
                    </div>
                    {(tool.image_present !== undefined || tool.overridden || tool.uses_latest_tag) && (
                      <div className="mt-1 flex gap-1">
                        {tool.image_present !== undefined && (
                          <span
                            className={
                              "text-[10px] uppercase tracking-wide border rounded px-1.5 py-0.5 " +
                              (tool.image_present
                                ? "text-emerald-300 bg-emerald-500/10 border-emerald-500/30"
                                : "text-rose-300 bg-rose-500/10 border-rose-500/30")
                            }
                          >
                            {tool.image_present ? "pulled" : "missing"}
                          </span>
                        )}
                        {tool.overridden && (
                          <span className="text-[10px] uppercase tracking-wide text-sky-300 bg-sky-500/10 border border-sky-500/30 rounded px-1.5 py-0.5">
                            override
                          </span>
                        )}
                        {tool.uses_latest_tag && (
                          <span className="text-[10px] uppercase tracking-wide text-amber-300 bg-amber-500/10 border border-amber-500/30 rounded px-1.5 py-0.5">
                            latest
                          </span>
                        )}
                      </div>
                    )}
                  </td>
                  <td className="py-2 pl-3 text-right">
                    <button
                      type="button"
                      onClick={() => checkOne.mutate(tool.name)}
                      disabled={checkOne.isPending}
                      className="inline-flex items-center justify-center h-7 w-7 rounded-md border border-border/60 hover:bg-muted/30 disabled:opacity-50"
                      title={`Check ${tool.name}`}
                    >
                      {checkOne.isPending && checkOne.variables === tool.name ? (
                        <Loader2Icon className="size-3.5 animate-spin" />
                      ) : (
                        <RefreshCwIcon className="size-3.5" />
                      )}
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : (
        <p className="text-xs text-muted-foreground">No scanner tools found.</p>
      )}
    </div>
  );
}

function TierPill({ tool }: { tool: ScannerToolStatus }) {
  const label =
    tool.integration_tier === "bucket" && tool.bucket
      ? `${tool.bucket} bucket`
      : tool.integration_tier;
  return (
    <span className="text-[10px] uppercase tracking-wide text-muted-foreground bg-muted/30 border border-border/30 rounded px-1.5 py-0.5">
      {label}
    </span>
  );
}

function FreshnessPill({ tool }: { tool: ScannerToolStatus }) {
  const status = tool.freshness_status || "not checked";
  const className =
    status === "update_available"
      ? "text-amber-300 bg-amber-500/10 border-amber-500/30"
      : status === "current"
        ? "text-emerald-300 bg-emerald-500/10 border-emerald-500/30"
        : status === "check_failed"
          ? "text-red-300 bg-red-500/10 border-red-500/30"
          : "text-muted-foreground bg-muted/20 border-border/30";
  return (
    <span
      className={`text-[10px] uppercase tracking-wide border rounded px-1.5 py-0.5 ${className}`}
      title={tool.version_check_error || tool.version_checked_at || status}
    >
      {status.replaceAll("_", " ")}
    </span>
  );
}

// The four wolf-built scanner image variants, in build order. Each maps onto a
// row in ScannerImagesPanel with a Rebuild button. `suffix` is appended to the
// default repo (wolf-scanners) to find the variant's image in GET
// /scanners/images so we can show its local vs. remote digest.
const SCANNER_VARIANTS: {
  name: string;
  label: string;
  suffix: string;
  // licenseNote surfaces a usage restriction the operator must clear before
  // enabling the tool (only CodeQL today — it is not open source).
  licenseNote?: string;
  // localOnly buckets are never pulled from / pushed to a registry (CodeQL —
  // its license forbids redistribution). The UI offers only a local rebuild.
  localOnly?: boolean;
}[] = [
  { name: "default", label: "Default", suffix: "" },
  { name: "jvm", label: "JVM", suffix: "-jvm" },
  { name: "rust", label: "Rust", suffix: "-rust" },
  {
    name: "codeql",
    label: "CodeQL",
    suffix: "-codeql",
    localOnly: true,
    licenseNote:
      "CodeQL is not open source. It is free only for analyzing open-source code; scanning private or commercial code requires a GitHub Advanced Security license. Confirm your entitlement before enabling it. It is built locally only — never pulled or pushed.",
  },
];

// Match a variant to its image-status row. The status list is keyed by full
// image ref (e.g. alphabravodevops/wolf-scanners-jvm:2.0.0). We strip the tag,
// then match the repo's trailing suffix — being careful that "" (default) only
// matches a ref whose repo has none of the other variant suffixes.
function statusForVariant(
  images: ScannerImageStatus[],
  suffix: string,
): ScannerImageStatus | undefined {
  const repoOf = (ref: string) => ref.split(":")[0];
  if (suffix === "") {
    return images.find((img) => {
      const repo = repoOf(img.image);
      return (
        /wolf-scanners$/.test(repo) ||
        (!/-jvm$|-rust$|-codeql$/.test(repo) && /wolf-scanners/.test(repo))
      );
    });
  }
  return images.find((img) => repoOf(img.image).endsWith(suffix));
}

// ScannerImagesPanel — per-variant rebuild rows + a live build console.
//
// Every variant always shows a "Rebuild (local)" button (a `--load` build into
// the local Docker daemon) regardless of whether DockerHub credentials exist —
// local builds never need credentials. When a `dockerhub_token` secret is
// configured, a small "push to DockerHub" toggle appears beside each button,
// turning the action into "Rebuild & push". With no secret, the toggle is
// replaced by a one-line hint linking to the DockerHub credential card below;
// the build button stays active either way. A header "Rebuild all" action runs
// all four variants in sequence.
function ScannerImagesPanel() {
  const imagesQ = useScannerImages();
  const images = imagesQ.data ?? [];

  // Whether a DockerHub token secret exists — gates the push toggle only.
  const secretsQ = useQuery({
    queryKey: ["config", "secrets", "all"],
    queryFn: async () =>
      (await api.get<{ key_type: string }[]>("/config/secrets")).data ?? [],
  });
  const hasDockerHubToken = useMemo(
    () => (secretsQ.data ?? []).some((s) => s.key_type === "dockerhub_token"),
    [secretsQ.data],
  );

  // Per-variant push-toggle state. Only consulted when hasDockerHubToken.
  const [pushVariants, setPushVariants] = useState<Record<string, boolean>>({});
  const togglePush = (name: string) =>
    setPushVariants((prev) => ({ ...prev, [name]: !prev[name] }));

  // Multi-arch (linux/amd64+arm64) toggle. Implies push (a manifest list can't
  // be loaded locally), so it's only useful with a DockerHub token + a buildx
  // builder on the host running Wolf.
  const [multiArch, setMultiArch] = useState(false);

  // The build the console should stream. Bumping `nonce` re-runs the same one.
  const [target, setTarget] = useState<BuildTarget | null>(null);
  const startBuild = (variant: string, push: boolean) =>
    setTarget((prev) => ({
      variant,
      push: push || multiArch,
      multiArch,
      nonce: (prev?.nonce ?? 0) + 1,
    }));

  return (
    <div className="space-y-4">
      <div className="glass-card p-5 space-y-4">
        <div className="flex items-center justify-between gap-3">
          <div className="flex items-center gap-2">
            <HammerIcon className="size-4 text-muted-foreground" />
            <h3 className="text-sm font-medium">Scanner images</h3>
            <span className="chip">wolf-built</span>
          </div>
          <div className="flex items-center gap-2">
            {hasDockerHubToken && (
              <label
                className="inline-flex items-center gap-1.5 text-xs text-muted-foreground cursor-pointer select-none"
                title="Build linux/amd64 + linux/arm64 with buildx and push a multi-arch manifest. Requires a QEMU buildx builder on the host running Wolf."
              >
                <input
                  type="checkbox"
                  checked={multiArch}
                  onChange={() => setMultiArch((v) => !v)}
                  className="size-3.5 accent-primary"
                />
                multi-arch
              </label>
            )}
            <button
              type="button"
              onClick={() => startBuild("all", false)}
              className="inline-flex items-center gap-1.5 h-8 px-2.5 rounded-md border border-border/60 text-xs hover:bg-muted/30"
              title={
                multiArch
                  ? "Rebuild all four variants multi-arch and push to DockerHub"
                  : "Rebuild all four variants in sequence (local --load; never needs credentials)"
              }
            >
              <HammerIcon className="size-3.5" />{" "}
              {multiArch ? "Rebuild all & push" : "Rebuild all"}
            </button>
          </div>
        </div>

        <p className="text-xs text-muted-foreground max-w-prose">
          Rebuild the wolf-built scanner images from the embedded build context.
          Local rebuilds load straight into the Docker daemon and{" "}
          <strong>never require credentials</strong>. Publishing to DockerHub is
          opt-in.
        </p>

        <ul className="space-y-2 text-sm">
          {SCANNER_VARIANTS.map((v) => {
            const status = statusForVariant(images, v.suffix);
            const push = hasDockerHubToken && !v.localOnly && !!pushVariants[v.name];
            return (
              <li
                key={v.name}
                className="flex flex-wrap items-center gap-3 border-b border-border/20 pb-2 last:border-0 last:pb-0"
              >
                <div className="min-w-24 font-medium">{v.label}</div>
                <div className="path flex-1 min-w-0 break-all">
                  {status?.image ?? `wolf-scanners${v.suffix}`}
                </div>
                <DigestPill
                  label="local"
                  value={status?.local_digest}
                  err={status?.local_error}
                />
                {v.localOnly ? (
                  <span className="text-[10px] uppercase tracking-wide font-medium text-muted-foreground bg-muted/40 border border-border/40 rounded px-1.5 py-0.5">
                    local only
                  </span>
                ) : (
                  <DigestPill
                    label="remote"
                    value={status?.remote_digest}
                    err={status?.remote_error}
                  />
                )}
                {v.localOnly ? null : status?.updates_available ? (
                  <span className="text-[10px] uppercase tracking-wide font-medium text-amber-300 bg-amber-500/10 border border-amber-500/30 rounded px-1.5 py-0.5">
                    update available
                  </span>
                ) : status?.local_digest ? (
                  <span className="text-[10px] uppercase tracking-wide font-medium text-emerald-300 bg-emerald-500/10 border border-emerald-500/30 rounded px-1.5 py-0.5">
                    up to date
                  </span>
                ) : null}

                {hasDockerHubToken && !v.localOnly && (
                  <label className="inline-flex items-center gap-1.5 text-xs text-muted-foreground cursor-pointer select-none">
                    <input
                      type="checkbox"
                      checked={!!pushVariants[v.name]}
                      onChange={() => togglePush(v.name)}
                      className="size-3.5 accent-primary"
                    />
                    push to DockerHub
                  </label>
                )}

                <button
                  type="button"
                  onClick={() => startBuild(v.name, push)}
                  className="inline-flex items-center gap-1.5 h-7 px-2.5 rounded-md bg-primary text-primary-foreground text-xs font-medium"
                  title={
                    push
                      ? "Rebuild from the embedded context and push to DockerHub"
                      : "Rebuild from the embedded context and load into the local Docker daemon (no credentials needed)"
                  }
                >
                  {push ? (
                    <UploadCloudIcon className="size-3.5" />
                  ) : (
                    <HammerIcon className="size-3.5" />
                  )}
                  {push ? "Rebuild & push" : "Rebuild (local)"}
                </button>

                {v.licenseNote && (
                  <p className="basis-full text-xs text-amber-300/90 bg-amber-500/5 border border-amber-500/20 rounded px-2 py-1.5">
                    ⚠ <span className="font-medium">License:</span> {v.licenseNote}
                  </p>
                )}
              </li>
            );
          })}
        </ul>

        {!hasDockerHubToken && (
          <p className="text-xs text-muted-foreground">
            <a
              href="#dockerhub-credential"
              className="text-primary hover:underline"
            >
              Add a DockerHub token
            </a>{" "}
            to publish images. Local rebuilds work without it.
          </p>
        )}
      </div>

      <BuildConsole target={target} />

      <div id="dockerhub-credential">
        <DockerHubCredentialCard />
      </div>
    </div>
  );
}

// ImagesPanel — per-image digests + update detection + per-image pull.
//
// The list of images is whatever wolf is currently configured to use
// (default + per-tool overrides + upstream-pinned scanner images). For
// each, we show the LOCAL pull-time digest and the REMOTE current
// digest. When they differ, an "Update" button appears that pulls
// just that image. "Check for updates" re-runs the probe.
function ImagesPanel() {
  const qc = useQueryClient();
  const q = useQuery({
    queryKey: ["scanner-images"],
    queryFn: async () =>
      (await api.get<ImageStatus[]>("/scanners/images")).data ?? [],
    // Don't auto-refetch — registry probes shell out per image and we
    // don't want to hammer Docker Hub. User-driven only.
    refetchOnWindowFocus: false,
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
    },
    onError: (e) => toast.error(e instanceof Error ? e.message : "Pull failed"),
  });

  return (
    <div className="glass-card p-5">
      <div className="flex items-center justify-between mb-3">
        <h3 className="text-sm font-medium">Scanner images</h3>
        <button
          type="button"
          onClick={() => qc.invalidateQueries({ queryKey: ["scanner-images"] })}
          disabled={q.isFetching}
          className="inline-flex items-center gap-1 h-8 px-2.5 rounded-md border border-border/60 text-xs hover:bg-muted/30 disabled:opacity-50"
          title="Re-probe registries for the latest manifest digests"
        >
          {q.isFetching ? (
            <Loader2Icon className="size-3.5 animate-spin" />
          ) : (
            <DownloadIcon className="size-3.5" />
          )}
          Check for updates
        </button>
      </div>

      {q.isLoading ? (
        <p className="text-xs text-muted-foreground">
          Probing local + remote digests…
        </p>
      ) : q.data && q.data.length > 0 ? (
        <ul className="space-y-2 text-sm">
          {q.data.map((img) => (
            <li
              key={img.image}
              className="flex flex-wrap items-center gap-3 border-b border-border/20 pb-2 last:border-0 last:pb-0"
            >
              <div className="font-mono text-xs flex-1 min-w-0 break-all">
                {img.image}
              </div>
              <DigestPill label="local" value={img.local_digest} err={img.local_error} />
              <DigestPill
                label="remote"
                value={img.remote_digest}
                err={img.remote_error}
              />
              {img.updates_available && (
                <span className="text-[10px] uppercase tracking-wide font-medium text-amber-300 bg-amber-500/10 border border-amber-500/30 rounded px-1.5 py-0.5">
                  update available
                </span>
              )}
              {(img.updates_available || (!img.local_digest && !img.local_error)) && (
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
      ) : (
        <p className="text-xs text-muted-foreground">No images configured.</p>
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
        className="text-[10px] uppercase tracking-wide text-red-300 bg-red-500/10 border border-red-500/30 rounded px-1.5 py-0.5"
        title={err}
      >
        {label}: error
      </span>
    );
  }
  if (!value) {
    return (
      <span
        className="text-[10px] uppercase tracking-wide text-muted-foreground bg-muted/20 border border-border/30 rounded px-1.5 py-0.5"
        title={label === "local" ? "image not pulled yet — use 'Set up scanners' or the per-image Update" : "no manifest available"}
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
      className="text-[10px] font-mono uppercase tracking-tight text-muted-foreground bg-muted/30 border border-border/30 rounded px-1.5 py-0.5"
      title={value}
    >
      {label}: {short}…
    </span>
  );
}
