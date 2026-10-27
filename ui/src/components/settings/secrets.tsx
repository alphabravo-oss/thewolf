// Secrets: git tokens, SSH material and model keys. Values are encrypted
// server-side and only a mask comes back. A GitLab token is bound to its host
// through the source-credentials API (see create below).
import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { api } from "@/lib/api";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { GitHubTokenHelp } from "@/components/github-token-help";
import { GitLabTokenHelp } from "@/components/gitlab-token-help";
import { AddButton } from "@/components/ui/add-button";
import { FormDialog } from "@/components/ui/form-dialog";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { safeErrorMessage } from "@/lib/safe-display";
import type { AdminSecret } from "@/lib/types";
import { Field, RowActions, SettingsCard, SettingsTable, useOwnerLookup } from "./shared";

export interface MaskedSecret {
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

type NewSecret = { key_type: string; key_name: string; value: string; host?: string };

// "https://GitLab.example.com/group" -> "gitlab.example.com"
export const bareHost = (v: string) => v.trim().replace(/^[a-z]+:\/\//i, "").replace(/[/?#].*$/, "").toLowerCase();

export function SecretsTab() {
  const qc = useQueryClient();
  const q = useQuery({
    queryKey: ["secrets"],
    queryFn: async () => (await api.get<MaskedSecret[]>("/config/secrets")).data ?? [],
  });
  const [adding, setAdding] = useState(false);
  const [keyType, setKeyType] = useState("github_token");
  const [keyName, setKeyName] = useState("");
  const [value, setValue] = useState("");
  const [gitlabHost, setGitlabHost] = useState("gitlab.com");
  const [touched, setTouched] = useState(false);
  const [deleting, setDeleting] = useState<MaskedSecret | null>(null);

  const close = (open: boolean) => {
    setAdding(open);
    if (!open) {
      setKeyName("");
      setValue("");
      setTouched(false);
      create.reset();
    }
  };
  const create = useMutation({
    mutationFn: ({ host, ...body }: NewSecret) =>
      // A GitLab token is only usable for a git repo when bound to its host, which
      // only the source-credentials API can record.
      host
        ? api.post("/credentials", { type: body.key_type, name: body.key_name, secret: body.value, allowed_hosts: [host] })
        : api.post("/config/secrets", body),
    onSuccess: () => {
      toast.success("Secret added");
      close(false);
      qc.invalidateQueries({ queryKey: ["secrets"] });
      qc.invalidateQueries({ queryKey: ["credentials"] });
    },
  });
  const del = useMutation({
    mutationFn: (id: string) => api.delete(`/config/secrets/${id}`),
    onSuccess: () => {
      toast.success("Secret deleted");
      qc.invalidateQueries({ queryKey: ["secrets"] });
      setDeleting(null);
    },
    onError: (e) => toast.error(safeErrorMessage(e, "Delete failed")),
  });

  const host = keyType === "gitlab_token" ? bareHost(gitlabHost) : "";
  const missingHost = keyType === "gitlab_token" && !host;

  return (
    <SettingsCard
      title="Your secrets"
      description="Git tokens, SSH material, and model keys (Anthropic / OpenAI) for the fixer. Values are encrypted; only a mask is shown after save."
      actions={<AddButton thing="secret" onClick={() => setAdding(true)} />}
      flush
    >
      <SettingsTable
        query={q}
        keyExtractor={(s) => s.id}
        persistKey="wolf-account-secrets"
        searchPlaceholder="Search secrets..."
        empty={{ title: "No secrets stored", description: "Add a git token or a model key so scans and fixes can authenticate.", actionLabel: "Add secret", onAction: () => setAdding(true) }}
        columns={[
          { key: "type", header: "Type", sortAccessor: (s) => s.key_type, accessor: (s) => <span className="font-mono text-xs">{s.key_type}</span> },
          {
            key: "name", header: "Name", sortAccessor: (s) => s.key_name,
            accessor: (s) => (
              <span>
                {s.key_name}
                {s.metadata?.login ? (
                  <div className="text-[11px] text-muted-foreground">
                    {s.metadata.login}{s.metadata.scopes?.length ? ` · ${s.metadata.scopes.join(", ")}` : ""}
                  </div>
                ) : null}
              </span>
            ),
          },
          { key: "value", header: "Value", sortable: false, accessor: () => <span className="font-mono text-muted-foreground">••••</span> },
          { key: "actions", header: "", sortable: false, accessor: (s) => <RowActions label={s.key_name} actions={[{ label: "Delete secret", destructive: true, onSelect: () => setDeleting(s) }]} /> },
        ]}
      />

      <FormDialog
        open={adding}
        onOpenChange={close}
        title="Add secret"
        description="Stored encrypted. You will not be able to read the value again."
        submitLabel="Add secret"
        presentation="sheet"
        pending={create.isPending}
        dirty={!!keyName || !!value}
        error={create.isError ? safeErrorMessage(create.error, "Create failed") : undefined}
        onSubmit={() => {
          setTouched(true);
          if (!keyName.trim() || !value || missingHost) return;
          create.mutate({ key_type: keyType, key_name: keyName.trim(), value, ...(host && { host }) });
        }}
      >
        <Field label="Type">
          <Select value={keyType} onValueChange={setKeyType}>
            <SelectTrigger aria-label="Type"><SelectValue /></SelectTrigger>
            <SelectContent>{KEY_TYPES.map((k) => <SelectItem key={k.value} value={k.value}>{k.label}</SelectItem>)}</SelectContent>
          </Select>
        </Field>
        <Field label="Name" error={touched && !keyName.trim() ? "Give the secret a name." : undefined}>
          <Input value={keyName} onChange={(e) => setKeyName(e.target.value)} placeholder="e.g. github-personal" aria-invalid={touched && !keyName.trim()} />
        </Field>
        <Field label="Value" error={touched && !value ? "Paste the secret value." : undefined}>
          <Input type="password" value={value} onChange={(e) => setValue(e.target.value)} placeholder="paste secret" className="font-mono" autoComplete="off" aria-invalid={touched && !value} />
        </Field>
        {keyType === "gitlab_token" && (
          <Field label="GitLab host" hint={<>Hostname only (e.g. <code className="font-mono">gitlab.example.com</code>). The token is bound to this host and sent nowhere else.</>} error={touched && missingHost ? "Enter the GitLab hostname." : undefined}>
            <Input value={gitlabHost} onChange={(e) => setGitlabHost(e.target.value)} placeholder="gitlab.com" className="font-mono" aria-invalid={touched && missingHost} />
          </Field>
        )}
        {keyType === "github_token" && <GitHubTokenHelp compact />}
        {keyType === "gitlab_token" && <GitLabTokenHelp />}
      </FormDialog>

      <ConfirmDialog
        open={!!deleting}
        onOpenChange={(o) => !o && setDeleting(null)}
        title={`Delete secret “${deleting?.key_name ?? ""}”?`}
        description="Anything using it (scans, fixes, nodes) loses access. This cannot be undone."
        confirmLabel="Delete secret"
        pending={del.isPending}
        onConfirm={() => deleting && del.mutate(deleting.id)}
      />
    </SettingsCard>
  );
}

export function AllSecretsCard() {
  const qc = useQueryClient();
  const ownerOf = useOwnerLookup();
  const q = useQuery({
    queryKey: ["admin", "secrets"],
    queryFn: async () => (await api.get<AdminSecret[]>("/admin/secrets")).data ?? [],
  });
  const [deleting, setDeleting] = useState<AdminSecret | null>(null);
  const del = useMutation({
    mutationFn: (id: string) => api.delete(`/config/secrets/${id}`),
    onSuccess: () => {
      toast.success("Secret deleted");
      qc.invalidateQueries({ queryKey: ["admin", "secrets"] });
      setDeleting(null);
    },
    onError: (e) => toast.error(safeErrorMessage(e, "Delete failed")),
  });
  return (
    <SettingsCard
      title="All users' secrets"
      description={<>Every user's secrets. Values are <strong>masked</strong> — you can see a secret exists and delete it, but never read another user's value.</>}
      flush
    >
      <SettingsTable
        query={q}
        keyExtractor={(s) => s.id}
        persistKey="wolf-admin-secrets"
        searchPlaceholder="Search secrets..."
        empty={{ title: "No secrets" }}
        columns={[
          { key: "owner", header: "Owner", sortAccessor: (s) => ownerOf(s.user_id), accessor: (s) => ownerOf(s.user_id) },
          { key: "type", header: "Type", sortAccessor: (s) => s.key_type, accessor: (s) => <span className="font-mono text-xs">{s.key_type}</span> },
          { key: "name", header: "Name", sortAccessor: (s) => s.key_name, accessor: (s) => s.key_name },
          { key: "value", header: "Value", sortable: false, accessor: () => <span className="font-mono text-muted-foreground">••••</span> },
          { key: "actions", header: "", sortable: false, accessor: (s) => <RowActions label={s.key_name} actions={[{ label: "Delete secret", destructive: true, onSelect: () => setDeleting(s) }]} /> },
        ]}
      />
      <ConfirmDialog
        open={!!deleting}
        onOpenChange={(o) => !o && setDeleting(null)}
        title={deleting ? `Delete ${ownerOf(deleting.user_id)}'s secret “${deleting.key_name}”?` : "Delete secret?"}
        description="This cannot be undone."
        confirmLabel="Delete secret"
        pending={del.isPending}
        onConfirm={() => deleting && del.mutate(deleting.id)}
      />
    </SettingsCard>
  );
}

export function SecretsSection() {
  return (
    <div className="space-y-4">
      <SecretsTab />
      <AllSecretsCard />
    </div>
  );
}
