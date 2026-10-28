// API keys: scoped, revocable credentials for the CLI / CI / agents. The secret
// is shown exactly once at creation. These bypass MFA by design, so revoking a
// key that has been used asks for its name.
import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { api } from "@/lib/api";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { AddButton } from "@/components/ui/add-button";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { FieldError, FormDialog } from "@/components/ui/form-dialog";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { StatusBadge } from "@/components/ui/status-badge";
import { useIsAdmin } from "@/lib/me";
import { safeErrorMessage } from "@/lib/safe-display";
import type { ApiToken, ApiTokenCreated } from "@/lib/types";
import { Field, RowActions, SettingsCard, SettingsTable, shortDate, TypedConfirmDialog, useOwnerLookup } from "./shared";

const API_SCOPES = [
  "read:repos", "write:repos", "read:scans", "write:scans", "read:findings", "write:findings",
  "read:fixes", "write:fixes", "read:config", "write:config", "admin",
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

type Role = (typeof ROLE_PRESETS)[number]["key"];

function tokenState(t: ApiToken) {
  if (t.revoked_at) return { status: "revoked", label: "Revoked" };
  if (t.expires_at && new Date(t.expires_at) < new Date()) return { status: "stale", label: "Expired" };
  return { status: "active", label: "Active" };
}

/** Shared by "Your keys" and the admin all-keys table: a revoke flow keyed on the token. */
function KeysTable({ queryKey, path, ownerOf, onAdd }: {
  queryKey: string[];
  path: string;
  ownerOf?: (id?: string) => string;
  onAdd?: () => void;
}) {
  const qc = useQueryClient();
  const q = useQuery({ queryKey, queryFn: async () => (await api.get<ApiToken[]>(path)).data ?? [] });
  const [revoking, setRevoking] = useState<ApiToken | null>(null);
  const revoke = useMutation({
    mutationFn: (id: string) => api.delete(`/auth/tokens/${id}`),
    onSuccess: () => {
      toast.success("Key revoked");
      qc.invalidateQueries({ queryKey: ["api-tokens"] });
      qc.invalidateQueries({ queryKey: ["admin", "tokens"] });
      setRevoking(null);
    },
    onError: (e) => toast.error(safeErrorMessage(e, "Revoke failed")),
  });
  const inUse = !!revoking?.last_used_at;
  const label = revoking ? (ownerOf ? `${ownerOf(revoking.user_id)}'s key “${revoking.name}”` : `API key “${revoking.name}”`) : "";
  const common = {
    open: !!revoking,
    onOpenChange: (o: boolean) => !o && setRevoking(null),
    title: `Revoke ${label}?`,
    confirmLabel: "Revoke key",
    pending: revoke.isPending,
    onConfirm: () => revoking && revoke.mutate(revoking.id),
  };
  return (
    <>
      <SettingsTable
        query={q}
        keyExtractor={(t) => t.id}
        persistKey={ownerOf ? "wolf-admin-apikeys" : "wolf-account-apikeys"}
        searchPlaceholder="Search keys..."
        empty={{ title: "No API keys yet", description: "Keys let the wolf CLI and CI pipelines call the API.", actionLabel: onAdd && "Add API key", onAction: onAdd }}
        columns={[
          ...(ownerOf ? [{ key: "owner", header: "Owner", sortAccessor: (t: ApiToken) => ownerOf(t.user_id), accessor: (t: ApiToken) => ownerOf(t.user_id) }] : []),
          { key: "name", header: "Name", sortAccessor: (t) => t.name, accessor: (t) => t.name },
          { key: "prefix", header: "Prefix", sortAccessor: (t) => t.token_prefix, accessor: (t) => <span className="font-mono text-xs text-muted-foreground">{t.token_prefix}…</span> },
          { key: "scopes", header: "Scopes", sortAccessor: (t) => t.scopes.join(" "), accessor: (t) => <span className="text-xs">{t.scopes.join(", ")}</span> },
          { key: "state", header: "Status", sortAccessor: (t) => tokenState(t).label, accessor: (t) => { const s = tokenState(t); return <StatusBadge status={s.status} label={s.label} size="sm" />; } },
          { key: "used", header: "Last used", sortAccessor: (t) => t.last_used_at ?? "", accessor: (t) => <span className="text-xs text-muted-foreground">{shortDate(t.last_used_at)}</span> },
          { key: "expires", header: "Expires", sortAccessor: (t) => t.expires_at ?? "", accessor: (t) => <span className="text-xs text-muted-foreground">{t.expires_at ? shortDate(t.expires_at) : "never"}</span> },
          {
            key: "actions", header: "", sortable: false,
            accessor: (t) => <RowActions label={t.name} actions={[!t.revoked_at && { label: "Revoke key", destructive: true, onSelect: () => setRevoking(t) }]} />,
          },
        ]}
      />
      {inUse ? (
        <TypedConfirmDialog {...common} expected={revoking!.name}
          description="This key has been used. Anything still relying on it stops working immediately. This cannot be undone." />
      ) : (
        <ConfirmDialog {...common} description="The key stops working immediately. This cannot be undone." />
      )}
    </>
  );
}

export function ApiKeysTab() {
  const isAdmin = useIsAdmin();
  const qc = useQueryClient();
  const [adding, setAdding] = useState(false);
  const [name, setName] = useState("");
  const [role, setRole] = useState<Role>("read-only");
  const [customScopes, setCustomScopes] = useState<string[]>(["read:scans"]);
  const [expiryDays, setExpiryDays] = useState(90);
  const [touched, setTouched] = useState(false);
  const [created, setCreated] = useState<ApiTokenCreated | null>(null);

  const close = (open: boolean) => {
    setAdding(open);
    if (!open) {
      setName("");
      setRole("read-only");
      setCustomScopes(["read:scans"]);
      setExpiryDays(90);
      setTouched(false);
      create.reset();
    }
  };
  const create = useMutation({
    mutationFn: async () =>
      (await api.post<ApiTokenCreated>("/auth/tokens", {
        name: name.trim(),
        scopes: role === "custom" ? customScopes : [role],
        expires_in_days: expiryDays,
      })).data,
    onSuccess: (d) => {
      setCreated(d ?? null);
      close(false);
      qc.invalidateQueries({ queryKey: ["api-tokens"] });
      qc.invalidateQueries({ queryKey: ["admin", "tokens"] });
    },
  });
  const toggleScope = (s: string) =>
    setCustomScopes((prev) => (prev.includes(s) ? prev.filter((x) => x !== s) : [...prev, s]));
  const nameMissing = touched && !name.trim();
  const noScopes = role === "custom" && customScopes.length === 0;
  const origin = typeof window !== "undefined" ? window.location.origin : "https://wolf.example.com";

  return (
    <SettingsCard
      title="Your API keys"
      description={
        <>
          Scoped, revocable credentials for the <code className="text-foreground">wolf</code> CLI, CI pipelines and agents. They{" "}
          <strong>bypass two-factor auth</strong> by design, so treat them like passwords. API reference at{" "}
          <a href="/api/v1/docs" target="_blank" rel="noreferrer" className="text-foreground underline underline-offset-2">/api/v1/docs</a>.
        </>
      }
      actions={<AddButton thing="API key" onClick={() => setAdding(true)} />}
      flush
    >
      <KeysTable queryKey={["api-tokens"]} path="/auth/tokens" onAdd={() => setAdding(true)} />

      <FormDialog
        open={adding}
        onOpenChange={close}
        title="Add API key"
        description="The key is shown once after it is created."
        submitLabel="Create key"
        presentation="sheet"
        pending={create.isPending}
        dirty={!!name}
        submitDisabled={noScopes}
        error={create.isError ? safeErrorMessage(create.error, "Could not create key") : undefined}
        onSubmit={() => {
          setTouched(true);
          if (name.trim()) create.mutate();
        }}
      >
        <div className="grid gap-3 sm:grid-cols-2">
          <Field label="Name" error={nameMissing ? "Give the key a name." : undefined}>
            <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="ci-pipeline" aria-invalid={nameMissing} />
          </Field>
          <Field label="Expires">
            <Select value={String(expiryDays)} onValueChange={(v) => setExpiryDays(Number(v))}>
              <SelectTrigger aria-label="Expires"><SelectValue /></SelectTrigger>
              <SelectContent>
                {EXPIRY_OPTIONS.map((o) => <SelectItem key={o.days} value={String(o.days)}>{o.label}</SelectItem>)}
              </SelectContent>
            </Select>
          </Field>
        </div>
        <div className="space-y-1.5">
          <span className="block text-xs font-medium text-muted-foreground">Access</span>
          <div className="grid gap-1.5 sm:grid-cols-2">
            {ROLE_PRESETS.filter((p) => p.key !== "admin" || isAdmin).map((p) => (
              <button
                key={p.key}
                type="button"
                onClick={() => setRole(p.key)}
                aria-pressed={role === p.key}
                className={"rounded-md border px-3 py-2 text-left text-sm " + (role === p.key ? "border-primary bg-primary/10" : "border-border hover:bg-accent")}
              >
                <div className="font-medium">{p.label}</div>
                <div className="text-xs text-muted-foreground">{p.help}</div>
              </button>
            ))}
          </div>
        </div>
        {role === "custom" && (
          <div>
            <div className="grid grid-cols-2 gap-1.5 sm:grid-cols-3">
              {API_SCOPES.filter((s) => s !== "admin" || isAdmin).map((s) => (
                <label key={s} className="inline-flex cursor-pointer select-none items-center gap-1.5 text-xs">
                  <Checkbox checked={customScopes.includes(s)} onCheckedChange={() => toggleScope(s)} aria-label={s} />
                  <span className={s === "admin" ? "font-mono text-status-warning" : "font-mono"}>{s}</span>
                </label>
              ))}
            </div>
            <FieldError>{noScopes ? "Pick at least one scope." : undefined}</FieldError>
          </div>
        )}
      </FormDialog>

      {/* One-time secret reveal. */}
      <Dialog open={!!created} onOpenChange={(o) => !o && setCreated(null)}>
        <DialogContent className="sm:max-w-lg">
          <DialogHeader>
            <DialogTitle>Key created</DialogTitle>
            <DialogDescription>Copy it now — it won't be shown again.</DialogDescription>
          </DialogHeader>
          {created && (
            <div className="min-w-0 space-y-3">
              <div className="flex items-center gap-2">
                <code className="min-w-0 flex-1 break-all rounded bg-muted/50 px-2 py-1.5 font-mono text-sm">{created.token}</code>
                <Button type="button" size="sm" onClick={() => { void navigator.clipboard?.writeText(created.token); toast.success("Key copied"); }}>Copy</Button>
              </div>
              <p className="text-xs text-muted-foreground">Use it with the CLI:</p>
              <pre className="overflow-x-auto rounded bg-muted/50 p-2 font-mono text-xs">{`export WOLF_SERVER=${origin}\nexport WOLF_TOKEN=${created.token}\nwolf scans list`}</pre>
            </div>
          )}
          <DialogFooter><Button type="button" onClick={() => setCreated(null)}>Done</Button></DialogFooter>
        </DialogContent>
      </Dialog>
    </SettingsCard>
  );
}

export function AllApiKeysCard() {
  const ownerOf = useOwnerLookup();
  return (
    <SettingsCard
      title="All users' API keys"
      description="Every user's keys. Keys are hash-only — names, scopes and usage are visible; the secret is never recoverable."
      flush
    >
      <KeysTable queryKey={["admin", "tokens"]} path="/admin/tokens" ownerOf={ownerOf} />
    </SettingsCard>
  );
}

export function ApiKeysSection() {
  return (
    <div className="space-y-4">
      <ApiKeysTab />
      <AllApiKeysCard />
    </div>
  );
}
