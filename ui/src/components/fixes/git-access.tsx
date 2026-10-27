// Git access for the AI fixer: host-bound GitHub / GitLab tokens (source
// credentials) it uses to push a fix branch and open a pull or merge request,
// plus what each host's token needs. Scope text is shared with the Secrets
// form (FixerTokenScopes) so the two cannot drift.
import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { api } from "@/lib/api";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { FixerTokenScopes } from "@/components/github-token-help";
import { bareHost } from "@/components/settings/secrets";
import { Field, RowActions, SettingsCard, SettingsTable } from "@/components/settings/shared";
import { AddButton } from "@/components/ui/add-button";
import { FieldError, FormDialog } from "@/components/ui/form-dialog";
import { Input } from "@/components/ui/input";
import { Section } from "@/components/ui/section";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { safeErrorMessage } from "@/lib/safe-display";

interface SourceCredential {
  id: string;
  type: string;
  name: string;
  allowed_hosts: string[];
}

const TYPES = [
  { value: "github_token", label: "GitHub token", host: "github.com" },
  { value: "gitlab_token", label: "GitLab token", host: "gitlab.com" },
];
const typeLabel = (t: string) => TYPES.find((x) => x.value === t)?.label ?? t;

export function GitAccessCard() {
  const qc = useQueryClient();
  const q = useQuery({
    queryKey: ["credentials"],
    queryFn: async () => (await api.get<SourceCredential[]>("/credentials")).data ?? [],
  });
  const [adding, setAdding] = useState(false);
  const [type, setType] = useState("github_token");
  const [name, setName] = useState("");
  const [secret, setSecret] = useState("");
  const [host, setHost] = useState("github.com");
  const [touched, setTouched] = useState(false);
  const [deleting, setDeleting] = useState<SourceCredential | null>(null);

  const close = (open: boolean) => {
    setAdding(open);
    if (!open) {
      setType("github_token");
      setName("");
      setSecret("");
      setHost("github.com");
      setTouched(false);
      create.reset();
    }
  };
  const create = useMutation({
    mutationFn: () => api.post("/credentials", { type, name: name.trim(), secret, allowed_hosts: [bareHost(host)] }),
    onSuccess: () => {
      toast.success("Credential added");
      close(false);
      qc.invalidateQueries({ queryKey: ["credentials"] });
    },
  });
  const del = useMutation({
    mutationFn: (id: string) => api.delete(`/credentials/${id}`),
    onSuccess: () => {
      toast.success("Credential deleted");
      qc.invalidateQueries({ queryKey: ["credentials"] });
      setDeleting(null);
    },
    onError: (e) => toast.error(safeErrorMessage(e, "Delete failed")),
  });
  const pickType = (next: string) => {
    // Keep a hand-typed host; only swap the default of the type being left.
    const was = TYPES.find((t) => t.value === type)?.host;
    setType(next);
    if (host === was) setHost(TYPES.find((t) => t.value === next)?.host ?? "");
  };
  const hostBare = bareHost(host);
  const miss = (v: string) => touched && !v.trim();

  return (
    <SettingsCard
      title="Git access"
      label="Git access"
      description="To push a fix branch and open a pull or merge request, Wolf needs a token bound to the repo's host. The token is sent to that host only."
      actions={<AddButton thing="credential" onClick={() => setAdding(true)} />}
      flush
    >
      <SettingsTable
        query={q}
        keyExtractor={(c) => c.id}
        persistKey="wolf-git-credentials"
        searchPlaceholder="Search credentials..."
        empty={{ title: "No git credentials", description: "Add a GitHub or GitLab token so the fixer can open pull requests.", actionLabel: "Add credential", onAction: () => setAdding(true) }}
        columns={[
          { key: "name", header: "Name", sortAccessor: (c) => c.name, accessor: (c) => c.name },
          { key: "type", header: "Type", sortAccessor: (c) => c.type, accessor: (c) => typeLabel(c.type) },
          { key: "hosts", header: "Bound to", sortAccessor: (c) => (c.allowed_hosts ?? []).join(","), accessor: (c) => <span className="font-mono text-xs">{(c.allowed_hosts ?? []).join(", ") || "—"}</span> },
          { key: "actions", header: "", sortable: false, accessor: (c) => <RowActions label={c.name} actions={[{ label: "Delete credential", destructive: true, onSelect: () => setDeleting(c) }]} /> },
        ]}
      />
      <div className="space-y-3 border-t border-border p-5">
        <Section title="Token scopes and private hosts" summary="What each host needs" variant="plain">
          <div className="grid gap-3 text-xs text-muted-foreground lg:grid-cols-2">
            <div className="space-y-1.5 rounded-lg border border-border p-3">
              <h3 className="text-sm font-medium text-foreground">GitHub</h3>
              <ul className="list-disc space-y-1 pl-4"><FixerTokenScopes host="github" /></ul>
            </div>
            <div className="space-y-1.5 rounded-lg border border-border p-3">
              <h3 className="text-sm font-medium text-foreground">GitLab (gitlab.com or self-hosted)</h3>
              <ul className="list-disc space-y-1 pl-4"><FixerTokenScopes host="gitlab" /></ul>
            </div>
          </div>
          <ul className="mt-3 list-disc space-y-1 pl-4 text-xs text-muted-foreground">
            <li>
              Private-network hosts need <code className="font-mono">WOLF_GIT_ALLOWED_CIDRS</code>; a private CA needs{" "}
              <code className="font-mono">WOLF_GIT_CA_BUNDLE</code> (path to a PEM bundle mounted into the app and fixer containers).
            </li>
            <li>SSH remotes are not supported for fixing yet. Use an https URL.</li>
            <li>Git hosts other than GitHub and GitLab are not supported for fixing.</li>
          </ul>
        </Section>
      </div>

      <FormDialog
        open={adding}
        onOpenChange={close}
        title="Add credential"
        description="A GitHub or GitLab token, bound to one host. Stored encrypted; the value cannot be read back."
        submitLabel="Add credential"
        pending={create.isPending}
        dirty={!!name || !!secret}
        error={create.isError ? safeErrorMessage(create.error, "Could not add credential") : undefined}
        onSubmit={() => {
          setTouched(true);
          if (name.trim() && secret && hostBare) create.mutate();
        }}
      >
        <Field label="Provider">
          <Select value={type} onValueChange={pickType}>
            <SelectTrigger aria-label="Provider"><SelectValue /></SelectTrigger>
            <SelectContent>{TYPES.map((t) => <SelectItem key={t.value} value={t.value}>{t.label}</SelectItem>)}</SelectContent>
          </Select>
        </Field>
        <Field label="Name">
          <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="e.g. github-fixer" aria-invalid={miss(name)} />
          <FieldError>{miss(name) ? "Give the credential a name." : undefined}</FieldError>
        </Field>
        <Field label="Token">
          <Input type="password" value={secret} onChange={(e) => setSecret(e.target.value)} placeholder="paste token" className="font-mono" autoComplete="off" aria-invalid={touched && !secret} />
          <FieldError>{touched && !secret ? "Paste the token." : undefined}</FieldError>
        </Field>
        <Field label="Host" hint={<>Hostname only (e.g. <code className="font-mono">gitlab.example.com</code>). Use your GitHub Enterprise host if you have one.</>}>
          <Input value={host} onChange={(e) => setHost(e.target.value)} className="font-mono" aria-invalid={touched && !hostBare} />
          <FieldError>{touched && !hostBare ? "Enter the hostname." : undefined}</FieldError>
        </Field>
        <ul className="list-disc space-y-1 pl-4 text-xs text-muted-foreground">
          <FixerTokenScopes host={type === "gitlab_token" ? "gitlab" : "github"} />
        </ul>
      </FormDialog>

      <ConfirmDialog
        open={!!deleting}
        onOpenChange={(o) => !o && setDeleting(null)}
        title={`Delete credential “${deleting?.name ?? ""}”?`}
        description="Fixes and repos that use it can no longer push or open pull requests until you add another. This cannot be undone."
        confirmLabel="Delete credential"
        pending={del.isPending}
        onConfirm={() => deleting && del.mutate(deleting.id)}
      />
    </SettingsCard>
  );
}
