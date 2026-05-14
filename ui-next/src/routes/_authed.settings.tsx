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
  KeyIcon,
  Loader2Icon,
  PlusIcon,
  SettingsIcon,
  ShieldIcon,
  Trash2Icon,
  UsersIcon,
} from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { api } from "@/lib/api";

export const Route = createFileRoute("/_authed/settings")({
  validateSearch: (s: Record<string, unknown>) => ({
    tab:
      typeof s.tab === "string" && /^(general|secrets|users|scanners)$/.test(s.tab)
        ? (s.tab as TabKey)
        : ("general" as TabKey),
  }),
  component: SettingsPage,
});

type TabKey = "general" | "secrets" | "users" | "scanners";

const TABS: { key: TabKey; label: string; Icon: typeof SettingsIcon }[] = [
  { key: "general", label: "General", Icon: SettingsIcon },
  { key: "secrets", label: "Secrets", Icon: KeyIcon },
  { key: "users", label: "Users", Icon: UsersIcon },
  { key: "scanners", label: "Scanners", Icon: ShieldIcon },
];

function SettingsPage() {
  const { tab } = Route.useSearch();
  const navigate = useNavigate();
  return (
    <div className="p-6 space-y-6 max-w-4xl">
      <h1 className="text-2xl font-semibold tracking-tight">Settings</h1>
      <nav className="flex gap-1 border-b border-border/40">
        {TABS.map(({ key, label, Icon }) => {
          const active = tab === key;
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

      {tab === "general" && <GeneralTab />}
      {tab === "secrets" && <SecretsTab />}
      {tab === "users" && <UsersTab />}
      {tab === "scanners" && <ScannersTab />}
    </div>
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
    help: "Master switch for AI-assisted finding enrichment and fix suggestions. When off, scans complete normally but no AI prompts are issued.",
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
              <label className="text-sm font-medium">{knob.label}</label>
              <p className="text-xs text-muted-foreground mt-0.5">{knob.help}</p>
            </div>
            {knob.type === "bool" ? (
              <BoolToggle
                value={current === "true"}
                onChange={(v) => m.mutate({ [knob.key]: v ? "true" : "false" })}
                disabled={m.isPending}
              />
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
// Users — list, create (admin flavor), delete. Self-delete blocked
// server-side; the UI also hides the delete button on the current user's
// row as a safety belt.
// ---------------------------------------------------------------------------

interface UserSummary {
  id: string;
  email: string;
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
    mutationFn: (body: { email: string; password: string }) => api.post("/users", body),
    onSuccess: () => {
      toast.success("User created");
      qc.invalidateQueries({ queryKey: ["users"] });
    },
    onError: (e) => toast.error(e instanceof Error ? e.message : "Create failed"),
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
                <th className="text-left px-4 py-2">Created</th>
                <th className="text-right px-4 py-2 w-12"></th>
              </tr>
            </thead>
            <tbody>
              {(q.data ?? []).map((u) => {
                const isMe = u.id === meId;
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

function NewUserForm({
  onSubmit,
  disabled,
}: {
  onSubmit: (b: { email: string; password: string }) => void;
  disabled?: boolean;
}) {
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  return (
    <form
      className="glass-card p-4 grid md:grid-cols-[1fr_1fr_auto] gap-2 items-end"
      onSubmit={(e) => {
        e.preventDefault();
        if (!email.trim() || password.length < 8) return;
        onSubmit({ email: email.trim().toLowerCase(), password });
        setEmail("");
        setPassword("");
      }}
    >
      <Field label="Email">
        <input
          required
          type="email"
          value={email}
          onChange={(e) => setEmail(e.target.value)}
          placeholder="user@example.com"
          className="w-full h-9 px-2 rounded-md bg-muted/40 border border-border/40 text-sm"
        />
      </Field>
      <Field label="Password (≥8 chars)">
        <input
          required
          type="password"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          minLength={8}
          className="w-full h-9 px-2 rounded-md bg-muted/40 border border-border/40 text-sm"
        />
      </Field>
      <button
        type="submit"
        disabled={disabled || !email.trim() || password.length < 8}
        className="inline-flex items-center gap-1 h-9 px-3 rounded-md bg-primary text-primary-foreground text-sm font-medium disabled:opacity-50"
      >
        <PlusIcon className="size-4" /> Add user
      </button>
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
