// Integrations: SIEM and ticketing destinations (Enterprise). Each destination
// is a name and an http(s) URL Wolf posts JSON to, with an optional
// Authorization header; "Send test" posts a wolf.test event through the server.
import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { api } from "@/lib/api";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { AddButton } from "@/components/ui/add-button";
import { EditionGate } from "@/components/ui/edition-gate";
import { FormDialog } from "@/components/ui/form-dialog";
import { Input } from "@/components/ui/input";
import { safeErrorMessage } from "@/lib/safe-display";
import { Field, RowActions, SettingsCard, SettingsTable, shortDate } from "./shared";

type Destination = { id: string; name?: string; url?: string; created_at?: string };
const blank = { name: "", url: "", authorization: "" };

export function IntegrationsSection() {
  return (
    <EditionGate
      feature="SIEM and ticketing destinations"
      capability="enterprise.integrations"
      description="Send Wolf events to your SIEM or ticketing system. Findings, reports and the API stay available in Community."
    >
      <div className="space-y-4">
        <Destinations path="/enterprise/siem" title="SIEM" description="Security events are posted as JSON." />
        <Destinations path="/enterprise/ticketing" title="Ticketing" description="Where Wolf opens tickets for findings and fixes." />
      </div>
    </EditionGate>
  );
}

function validUrl(s: string) {
  try {
    const u = new URL(s.trim());
    return (u.protocol === "https:" || u.protocol === "http:") && !!u.host;
  } catch {
    return false;
  }
}

function Destinations({ path, title, description }: { path: string; title: string; description: string }) {
  const qc = useQueryClient();
  const key = ["integrations", path];
  const q = useQuery({
    queryKey: key,
    queryFn: async () => {
      const r = await api.get<Destination[]>(path);
      return Array.isArray(r.data) ? r.data : [];
    },
  });
  const [adding, setAdding] = useState(false);
  const [form, setForm] = useState(blank);
  const [touched, setTouched] = useState(false);
  const [testing, setTesting] = useState<Destination | null>(null);
  const [deleting, setDeleting] = useState<Destination | null>(null);
  const label = (d: Destination) => d.name || d.id;

  const close = (open: boolean) => {
    setAdding(open);
    if (!open) {
      setForm(blank);
      setTouched(false);
      create.reset();
    }
  };
  const create = useMutation({
    mutationFn: () =>
      api.post(path, {
        name: form.name.trim(),
        url: form.url.trim(),
        ...(form.authorization.trim() ? { authorization: form.authorization.trim() } : {}),
      }),
    onSuccess: () => {
      toast.success("Destination added");
      close(false);
      return qc.invalidateQueries({ queryKey: key });
    },
  });
  const send = useMutation({
    mutationFn: (id: string) => api.post<{ status?: number }>(`${path}/${encodeURIComponent(id)}/send`, { event: "wolf.test" }),
    onSuccess: (r) => toast.success(r.data?.status ? `Delivered (HTTP ${r.data.status})` : "Delivered"),
    onError: (e) => toast.error(safeErrorMessage(e, "Send failed")),
    onSettled: () => setTesting(null),
  });
  const del = useMutation({
    mutationFn: (id: string) => api.delete(`${path}/${encodeURIComponent(id)}`),
    onSuccess: () => {
      toast.success("Destination deleted");
      setDeleting(null);
      return qc.invalidateQueries({ queryKey: key });
    },
    onError: (e) => toast.error(safeErrorMessage(e, "Delete failed")),
  });

  const nameError = touched && !form.name.trim() ? "Give the destination a name." : undefined;
  const urlError = touched && !validUrl(form.url) ? "Enter an http(s) URL." : undefined;

  return (
    <SettingsCard title={title} label={title} description={description} actions={<AddButton thing="destination" onClick={() => setAdding(true)} />} flush>
      <SettingsTable
        query={q}
        keyExtractor={(d) => d.id}
        persistKey={`wolf-settings${path.replace(/\//g, "-")}`}
        searchPlaceholder={`Search ${title} destinations...`}
        empty={{ title: "No destinations yet", actionLabel: "Add destination", onAction: () => setAdding(true) }}
        columns={[
          { key: "name", header: "Name", sortAccessor: label, accessor: (d) => <span className="font-medium">{label(d)}</span> },
          { key: "url", header: "URL", sortAccessor: (d) => d.url ?? "", accessor: (d) => <span className="break-all font-mono text-xs text-muted-foreground">{d.url || "—"}</span> },
          { key: "created", header: "Added", sortAccessor: (d) => d.created_at ?? "", accessor: (d) => <span className="text-xs text-muted-foreground">{shortDate(d.created_at)}</span> },
          {
            key: "actions", header: "", sortable: false,
            accessor: (d) => (
              <RowActions
                label={label(d)}
                actions={[
                  { label: "Send test", onSelect: () => setTesting(d) },
                  { label: "Delete destination", destructive: true, onSelect: () => setDeleting(d) },
                ]}
              />
            ),
          },
        ]}
      />

      <FormDialog
        open={adding}
        onOpenChange={close}
        title={`Add ${title} destination`}
        description="Wolf posts JSON to this URL."
        submitLabel="Add destination"
        pending={create.isPending}
        dirty={!!(form.name || form.url || form.authorization)}
        error={create.isError ? safeErrorMessage(create.error, "Create failed") : undefined}
        onSubmit={() => {
          setTouched(true);
          if (form.name.trim() && validUrl(form.url)) create.mutate();
        }}
      >
        <Field label="Name" error={nameError}>
          <Input value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} aria-invalid={!!nameError} autoComplete="off" />
        </Field>
        <Field label="URL" error={urlError}>
          <Input type="url" value={form.url} onChange={(e) => setForm({ ...form, url: e.target.value })} aria-invalid={!!urlError} placeholder="https://siem.example.com/collector" autoComplete="off" />
        </Field>
        <Field label="Authorization header (optional)" hint="Sent as the Authorization header. Stored with the destination and visible to admins, so use a token scoped to this collector.">
          <Input value={form.authorization} onChange={(e) => setForm({ ...form, authorization: e.target.value })} placeholder="Bearer …" autoComplete="off" spellCheck={false} />
        </Field>
      </FormDialog>

      <ConfirmDialog
        open={!!testing}
        onOpenChange={(o) => !o && setTesting(null)}
        variant="default"
        title={`Send a test event to “${testing ? label(testing) : ""}”?`}
        description="Posts a wolf.test event to the destination now."
        confirmLabel="Send test"
        pending={send.isPending}
        onConfirm={() => testing && send.mutate(testing.id)}
      />
      <ConfirmDialog
        open={!!deleting}
        onOpenChange={(o) => !o && setDeleting(null)}
        title={`Delete destination “${deleting ? label(deleting) : ""}”?`}
        description="Wolf stops sending events to it. This cannot be undone."
        confirmLabel="Delete destination"
        pending={del.isPending}
        onConfirm={() => deleting && del.mutate(deleting.id)}
      />
    </SettingsCard>
  );
}
