// Nodes: remote Linux hosts scanned over SSH. Credentials are encrypted
// secrets; a node only references the one it uses. Add and edit share a dialog.
import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { api } from "@/lib/api";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { AddButton } from "@/components/ui/add-button";
import { FieldError, FormDialog } from "@/components/ui/form-dialog";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { StatusBadge } from "@/components/ui/status-badge";
import { safeErrorMessage } from "@/lib/safe-display";
import type { RemoteNode } from "@/lib/types";
import type { MaskedSecret } from "./secrets";
import { Field, RowActions, SettingsCard, SettingsTable, useOwnerLookup } from "./shared";

type Draft = {
  name: string; host: string; port: string; username: string;
  auth_type: "private_key" | "password"; secretId: string; knownHosts: string; basePath: string;
};
const blank: Draft = { name: "", host: "", port: "22", username: "", auth_type: "private_key", secretId: "", knownHosts: "", basePath: "" };
const toDraft = (n: RemoteNode): Draft => ({
  name: n.name, host: n.host, port: String(n.port || 22), username: n.username, auth_type: n.auth_type,
  secretId: n.credential_secret_id ?? "", knownHosts: n.known_hosts ?? "", basePath: n.base_path ?? "",
});

export function NodesTab() {
  const qc = useQueryClient();
  const nodes = useQuery({
    queryKey: ["remote-nodes"],
    queryFn: async () => (await api.get<RemoteNode[]>("/nodes")).data ?? [],
  });
  const secrets = useQuery({
    queryKey: ["secrets"],
    queryFn: async () => (await api.get<MaskedSecret[]>("/config/secrets")).data ?? [],
  });
  // `dialog` is "new", the node being edited, or null (closed).
  const [dialog, setDialog] = useState<"new" | RemoteNode | null>(null);
  const [draft, setDraft] = useState<Draft>(blank);
  const [touched, setTouched] = useState(false);
  const [deleting, setDeleting] = useState<RemoteNode | null>(null);
  const [disabling, setDisabling] = useState<RemoteNode | null>(null);
  const editing = dialog && dialog !== "new" ? dialog : null;
  const refresh = () => qc.invalidateQueries({ queryKey: ["remote-nodes"] });

  const openDialog = (d: "new" | RemoteNode) => {
    setDraft(d === "new" ? blank : toDraft(d));
    setTouched(false);
    save.reset();
    setDialog(d);
  };
  const body = () => ({
    name: draft.name.trim(),
    host: draft.host.trim(),
    port: Number.parseInt(draft.port, 10) || 22,
    username: draft.username.trim(),
    auth_type: draft.auth_type,
    credential_secret_id: draft.secretId || undefined,
    known_hosts: draft.knownHosts.trim(),
    base_path: draft.basePath.trim(),
  });
  const save = useMutation({
    mutationFn: () => (editing ? api.put(`/nodes/${editing.id}`, body()) : api.post("/nodes", { ...body(), enabled: true })),
    onSuccess: () => {
      toast.success(editing ? "Node saved" : "Node added");
      setDialog(null);
      return refresh();
    },
  });
  const setEnabled = useMutation({
    mutationFn: ({ id, enabled }: { id: string; enabled: boolean }) => api.put(`/nodes/${id}`, { enabled }),
    onSuccess: (_, { enabled }) => {
      toast.success(enabled ? "Node enabled" : "Node disabled");
      setDisabling(null);
      return refresh();
    },
    onError: (e) => toast.error(safeErrorMessage(e, "Update failed")),
  });
  const check = useMutation({
    mutationFn: (id: string) => api.post(`/nodes/${id}/check`),
    onSuccess: () => {
      toast.success("Node check passed");
      return refresh();
    },
    onError: (e) => {
      toast.error(safeErrorMessage(e, "Node check failed"));
      return refresh();
    },
  });
  const del = useMutation({
    mutationFn: (id: string) => api.delete(`/nodes/${id}`),
    onSuccess: () => {
      toast.success("Node deleted");
      refresh();
      setDeleting(null);
    },
    onError: (e) => toast.error(safeErrorMessage(e, "Delete failed")),
  });

  const allowedSecrets = (secrets.data ?? []).filter((s) => (draft.auth_type === "private_key" ? s.key_type === "ssh_private_key" : s.key_type === "ssh_password"));
  const set = (patch: Partial<Draft>) => setDraft((d) => ({ ...d, ...patch }));
  const missing = (v: string) => touched && !v.trim();
  const valid = !!(draft.name.trim() && draft.host.trim() && draft.username.trim() && draft.secretId);

  return (
    <SettingsCard
      title="Your SSH nodes"
      description="Remote Linux hosts Wolf can scan over SSH. Repos on a node are scanned from a git archive."
      actions={<AddButton thing="node" onClick={() => openDialog("new")} />}
      flush
    >
      <SettingsTable
        query={nodes}
        keyExtractor={(n) => n.id}
        persistKey="wolf-account-nodes"
        searchPlaceholder="Search nodes..."
        empty={{ title: "No remote nodes", description: "Add an SSH host to scan code that lives on a server.", actionLabel: "Add node", onAction: () => openDialog("new") }}
        columns={[
          {
            key: "name", header: "Name", sortAccessor: (n) => n.name,
            accessor: (n) => (
              <span>
                <div className="font-medium">{n.name}</div>
                {n.base_path ? <div className="font-mono text-xs text-muted-foreground">{n.base_path}</div> : null}
              </span>
            ),
          },
          { key: "host", header: "Host", sortAccessor: (n) => n.host, accessor: (n) => <span className="font-mono text-xs">{n.username}@{n.host}:{n.port || 22}</span> },
          { key: "auth", header: "Auth", sortAccessor: (n) => n.auth_type, accessor: (n) => n.auth_type.replace("_", " ") },
          {
            key: "status", header: "Status", sortAccessor: (n) => (n.enabled ? "enabled" : "disabled"),
            accessor: (n) => (
              <div className="space-y-1">
                <div className="flex flex-wrap items-center gap-1.5">
                  <StatusBadge status={n.enabled ? "enabled" : "disabled"} size="sm" />
                  {n.last_check_status ? <StatusBadge status={n.last_check_status} size="sm" showDot={false} /> : null}
                </div>
                {n.last_check_error ? <div className="max-w-[240px] truncate text-xs text-destructive-text" title={n.last_check_error}>{n.last_check_error}</div> : null}
              </div>
            ),
          },
          {
            key: "actions", header: "", sortable: false,
            accessor: (n) => (
              <RowActions
                label={n.name}
                actions={[
                  { label: "Check node", disabled: check.isPending, onSelect: () => check.mutate(n.id) },
                  { label: "Edit node", onSelect: () => openDialog(n) },
                  n.enabled
                    ? { label: "Disable node", onSelect: () => setDisabling(n) }
                    : { label: "Enable node", onSelect: () => setEnabled.mutate({ id: n.id, enabled: true }) },
                  { label: "Delete node", destructive: true, onSelect: () => setDeleting(n) },
                ]}
              />
            ),
          },
        ]}
      />

      <FormDialog
        open={!!dialog}
        onOpenChange={(o) => !o && setDialog(null)}
        title={editing ? `Edit ${editing.name}` : "Add node"}
        description="Credentials come from a stored secret; add the SSH key or password under Your secrets first."
        submitLabel={editing ? "Save node" : "Add node"}
        presentation="sheet"
        pending={save.isPending}
        dirty={JSON.stringify(draft) !== JSON.stringify(editing ? toDraft(editing) : blank)}
        error={save.isError ? safeErrorMessage(save.error, "Save failed") : undefined}
        onSubmit={() => {
          setTouched(true);
          if (valid) save.mutate();
        }}
      >
        <div className="grid gap-3 sm:grid-cols-2">
          <Field label="Name">
            <Input value={draft.name} onChange={(e) => set({ name: e.target.value })} placeholder="dev-box" aria-invalid={missing(draft.name)} />
            <FieldError>{missing(draft.name) ? "Name is required." : undefined}</FieldError>
          </Field>
          <Field label="Username">
            <Input value={draft.username} onChange={(e) => set({ username: e.target.value })} placeholder="alice" aria-invalid={missing(draft.username)} />
            <FieldError>{missing(draft.username) ? "Username is required." : undefined}</FieldError>
          </Field>
          <Field label="Host">
            <Input value={draft.host} onChange={(e) => set({ host: e.target.value })} placeholder="dev.example.com" className="font-mono" aria-invalid={missing(draft.host)} />
            <FieldError>{missing(draft.host) ? "Host is required." : undefined}</FieldError>
          </Field>
          <Field label="Port">
            <Input value={draft.port} onChange={(e) => set({ port: e.target.value })} inputMode="numeric" />
          </Field>
          <Field label="Auth">
            <Select value={draft.auth_type} onValueChange={(v) => set({ auth_type: v as Draft["auth_type"], secretId: "" })}>
              <SelectTrigger aria-label="Auth"><SelectValue /></SelectTrigger>
              <SelectContent>
                <SelectItem value="private_key">Private key</SelectItem>
                <SelectItem value="password">Password</SelectItem>
              </SelectContent>
            </Select>
          </Field>
          <Field label="Credential secret">
            <Select value={draft.secretId} onValueChange={(v) => set({ secretId: v })}>
              <SelectTrigger aria-label="Credential secret" aria-invalid={touched && !draft.secretId}><SelectValue placeholder="Select secret…" /></SelectTrigger>
              <SelectContent>{allowedSecrets.map((s) => <SelectItem key={s.id} value={s.id}>{s.key_name}</SelectItem>)}</SelectContent>
            </Select>
            <FieldError>{touched && !draft.secretId ? "Pick the SSH secret to use." : undefined}</FieldError>
          </Field>
        </div>
        <Field label="Base path">
          <Input value={draft.basePath} onChange={(e) => set({ basePath: e.target.value })} placeholder="/home/alice/code" className="font-mono" />
        </Field>
        <Field label="Known hosts">
          <textarea
            value={draft.knownHosts}
            onChange={(e) => set({ knownHosts: e.target.value })}
            placeholder="dev.example.com ssh-ed25519 AAAA…"
            className="min-h-20 w-full rounded-md border border-input bg-background px-3 py-2 font-mono text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
          />
        </Field>
      </FormDialog>

      <ConfirmDialog
        open={!!disabling}
        onOpenChange={(o) => !o && setDisabling(null)}
        variant="default"
        title={`Disable node “${disabling?.name ?? ""}”?`}
        description="Scans of repos on this node are refused until it is enabled again."
        confirmLabel="Disable node"
        pending={setEnabled.isPending}
        onConfirm={() => disabling && setEnabled.mutate({ id: disabling.id, enabled: false })}
      />
      <ConfirmDialog
        open={!!deleting}
        onOpenChange={(o) => !o && setDeleting(null)}
        title={`Delete node “${deleting?.name ?? ""}”?`}
        description="Repos using this node must be removed first."
        confirmLabel="Delete node"
        pending={del.isPending}
        onConfirm={() => deleting && del.mutate(deleting.id)}
      />
    </SettingsCard>
  );
}

export function AllNodesCard() {
  const qc = useQueryClient();
  const ownerOf = useOwnerLookup();
  const q = useQuery({
    queryKey: ["admin", "nodes"],
    queryFn: async () => (await api.get<RemoteNode[]>("/nodes")).data ?? [],
  });
  const [deleting, setDeleting] = useState<RemoteNode | null>(null);
  const del = useMutation({
    mutationFn: (id: string) => api.delete(`/nodes/${id}`),
    onSuccess: () => {
      toast.success("Node deleted");
      qc.invalidateQueries({ queryKey: ["admin", "nodes"] });
      setDeleting(null);
    },
    onError: (e) => toast.error(safeErrorMessage(e, "Delete failed")),
  });
  return (
    <SettingsCard title="All users' SSH nodes" description="Every user's SSH nodes." flush>
      <SettingsTable
        query={q}
        keyExtractor={(n) => n.id}
        persistKey="wolf-admin-nodes"
        searchPlaceholder="Search nodes..."
        empty={{ title: "No nodes" }}
        columns={[
          { key: "owner", header: "Owner", sortAccessor: (n) => ownerOf(n.user_id), accessor: (n) => ownerOf(n.user_id) },
          { key: "name", header: "Name", sortAccessor: (n) => n.name, accessor: (n) => n.name },
          { key: "host", header: "Host", sortAccessor: (n) => n.host, accessor: (n) => <span className="font-mono text-xs">{n.username}@{n.host}:{n.port}</span> },
          { key: "enabled", header: "Status", sortAccessor: (n) => (n.enabled ? "enabled" : "disabled"), accessor: (n) => <StatusBadge status={n.enabled ? "enabled" : "disabled"} size="sm" /> },
          { key: "actions", header: "", sortable: false, accessor: (n) => <RowActions label={n.name} actions={[{ label: "Delete node", destructive: true, onSelect: () => setDeleting(n) }]} /> },
        ]}
      />
      <ConfirmDialog
        open={!!deleting}
        onOpenChange={(o) => !o && setDeleting(null)}
        title={deleting ? `Delete ${ownerOf(deleting.user_id)}'s node “${deleting.name}”?` : "Delete node?"}
        description="This cannot be undone."
        confirmLabel="Delete node"
        pending={del.isPending}
        onConfirm={() => deleting && del.mutate(deleting.id)}
      />
    </SettingsCard>
  );
}

export function NodesSection() {
  return (
    <div className="space-y-4">
      <NodesTab />
      <AllNodesCard />
    </div>
  );
}
