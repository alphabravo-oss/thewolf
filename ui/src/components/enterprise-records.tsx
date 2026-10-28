// Enterprise admin building blocks: the page chrome (header, edition gate,
// preview note) and RecordsPanel, a DataTable of a module's records with a
// `+ Add` dialog, row actions (send / delete behind a confirm) and the raw
// payload tucked into a collapsed Section. Record schemas belong to the
// Enterprise overlay, so the dialog sends `name` plus optional JSON fields.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type ReactNode } from "react";
import { Link } from "@tanstack/react-router";
import { toast } from "sonner";
import { api, ApiError, isNotFound } from "@/lib/api";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { Field, RowActions, SettingsCard, SettingsTable, shortDate, TypedConfirmDialog } from "@/components/settings/shared";
import { AddButton } from "@/components/ui/add-button";
import { EditionGate } from "@/components/ui/edition-gate";
import { EmptyState } from "@/components/ui/empty-state";
import { FieldError, FormDialog } from "@/components/ui/form-dialog";
import { Input } from "@/components/ui/input";
import { MetricCard } from "@/components/ui/metric-card";
import { PageHeader, PageShell } from "@/components/ui/page";
import { Section } from "@/components/ui/section";
import { safeDisplayText, safeErrorMessage } from "@/lib/safe-display";

export function EnterpriseChrome({
  title,
  description,
  actions,
  kpis,
  children,
}: {
  title: string;
  description?: string;
  actions?: ReactNode;
  /** KPI strip: MetricCards shown under the header while the module is unlocked. */
  kpis?: ReactNode;
  children: ReactNode;
}) {
  return (
    <PageShell>
      <PageHeader
        eyebrow={<Link to="/enterprise" className="hover:underline">Enterprise</Link>}
        title={title}
        description={description}
        actions={actions}
      />
      <EditionGate
        feature={title}
        description="This module is not available in Community. Your existing bookmark still works; no tenancy or policy enforcement is provided here."
      >
        <p role="note" className="rounded-lg border border-border bg-muted/30 p-4 text-sm text-muted-foreground">
          Preview: these records describe configuration. Creating a record does not enforce tenant isolation, access policies, compliance, or data residency.
        </p>
        {kpis ? <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">{kpis}</div> : null}
        {children}
      </EditionGate>
    </PageShell>
  );
}

export type EnterpriseRecord = {
  id: string;
  name?: string;
  kind?: string;
  created_at?: string;
  [key: string]: unknown;
};

export function useEnterpriseRecords(path: string, enabled = true) {
  return useQuery({
    enabled,
    queryKey: ["enterprise", path],
    queryFn: async () => {
      const r = await api.get<EnterpriseRecord[]>(path);
      return Array.isArray(r.data) ? r.data : [];
    },
    retry: false,
  });
}

/** KPI tile with a module's record count. */
export function RecordCount({ title, path, subtitle }: { title: string; path: string; subtitle?: string }) {
  const q = useEnterpriseRecords(path);
  return <MetricCard title={title} value={q.isLoading ? "…" : q.isError ? "—" : q.data?.length ?? 0} subtitle={subtitle} />;
}

function parseExtra(text: string): { value?: Record<string, unknown>; error?: string } {
  if (!text.trim()) return { value: {} };
  try {
    const v: unknown = JSON.parse(text);
    if (v && typeof v === "object" && !Array.isArray(v)) return { value: v as Record<string, unknown> };
  } catch {
    // fall through to the shared message
  }
  return { error: "Must be a JSON object, e.g. {\"region\": \"eu-west-1\"}." };
}

export function RecordsPanel({
  path,
  title,
  description,
  noun = "record",
  send,
  addable = true,
  typedDelete,
  actions,
}: {
  path: string;
  title: string;
  description?: ReactNode;
  /** Singular name used in `+ Add <noun>` and the confirm copy. */
  noun?: string;
  /** Show a "Send test event" row action (SIEM, ticketing). */
  send?: boolean;
  /** False when records are created elsewhere (attack paths, verifications). */
  addable?: boolean;
  /** Require typing the name to delete (customer keys, residency). */
  typedDelete?: boolean;
  /** Extra header actions, e.g. a Compute button. */
  actions?: ReactNode;
}) {
  const qc = useQueryClient();
  const q = useEnterpriseRecords(path);
  const [adding, setAdding] = useState(false);
  const [name, setName] = useState("");
  const [extra, setExtra] = useState("");
  const [touched, setTouched] = useState(false);
  const [sending, setSending] = useState<EnterpriseRecord | null>(null);
  const [deleting, setDeleting] = useState<EnterpriseRecord | null>(null);
  const refresh = () => qc.invalidateQueries({ queryKey: ["enterprise", path] });

  const close = (open: boolean) => {
    setAdding(open);
    if (!open) {
      setName("");
      setExtra("");
      setTouched(false);
      create.reset();
    }
  };
  const parsed = parseExtra(extra);
  const create = useMutation({
    mutationFn: () => api.post<EnterpriseRecord>(path, { ...parsed.value, name: name.trim() }),
    onSuccess: () => {
      toast.success(`${noun[0].toUpperCase()}${noun.slice(1)} added`);
      close(false);
      return refresh();
    },
  });
  const del = useMutation({
    mutationFn: (id: string) => api.delete(`${path}/${encodeURIComponent(id)}`),
    onSuccess: () => {
      toast.success(`${noun[0].toUpperCase()}${noun.slice(1)} deleted`);
      setDeleting(null);
      return refresh();
    },
    onError: (e) => toast.error(safeErrorMessage(e, "Delete failed")),
  });
  const deliver = useMutation({
    mutationFn: (id: string) => api.post(`${path}/${encodeURIComponent(id)}/send`, { event: "wolf.test" }),
    onSuccess: () => {
      toast.success("Delivered");
      setSending(null);
    },
    onError: (e) => {
      toast.error(safeErrorMessage(e, "Send failed"));
      setSending(null);
    },
  });

  if (q.isError) {
    const unavailable = isNotFound(q.error) || (q.error instanceof ApiError && q.error.status === 404);
    return (
      <EmptyState
        title={unavailable ? "Not in this edition" : "Could not load"}
        description={unavailable ? "This Enterprise module is not available on Wolf Community." : safeErrorMessage(q.error, "Request failed")}
        actionLabel={unavailable ? undefined : "Retry"}
        onAction={unavailable ? undefined : () => void q.refetch()}
      />
    );
  }

  const rows = q.data ?? [];
  const label = (r: EnterpriseRecord) => r.name || r.id;
  const nameMissing = touched && !name.trim();
  const openAdd = () => setAdding(true);

  return (
    <SettingsCard
      title={title}
      label={title}
      description={description}
      actions={<>{actions}{addable && <AddButton thing={noun} onClick={openAdd} />}</>}
      flush
    >
      <SettingsTable
        query={q}
        keyExtractor={(r) => r.id}
        persistKey={`wolf-enterprise${path.replace(/\//g, "-")}`}
        searchPlaceholder={`Search ${title.toLowerCase()}...`}
        empty={{ title: `No ${noun}s yet`, actionLabel: addable ? `Add ${noun}` : undefined, onAction: addable ? openAdd : undefined }}
        columns={[
          { key: "name", header: "Name", sortAccessor: label, accessor: (r) => <span className="font-medium">{label(r)}</span> },
          ...(rows.some((r) => r.kind) ? [{ key: "kind", header: "Kind", sortAccessor: (r: EnterpriseRecord) => r.kind ?? "", accessor: (r: EnterpriseRecord) => r.kind ?? "—" }] : []),
          { key: "id", header: "ID", sortAccessor: (r) => r.id, accessor: (r) => <span className="font-mono text-xs text-muted-foreground">{r.id}</span> },
          ...(rows.some((r) => r.created_at) ? [{ key: "created", header: "Created", sortAccessor: (r: EnterpriseRecord) => r.created_at ?? "", accessor: (r: EnterpriseRecord) => <span className="text-xs text-muted-foreground">{shortDate(r.created_at)}</span> }] : []),
          {
            key: "actions", header: "", sortable: false,
            accessor: (r) => (
              <RowActions
                label={label(r)}
                actions={[
                  send && { label: "Send test event", onSelect: () => setSending(r) },
                  { label: `Delete ${noun}`, destructive: true, onSelect: () => setDeleting(r) },
                ]}
              />
            ),
          },
        ]}
      />
      {rows.length > 0 && (
        <div>
          <Section title="Raw records" summary="JSON as returned by the API" variant="plain">
            <pre className="max-h-96 overflow-auto rounded-md bg-muted/30 p-3 font-mono text-xs">
              {safeDisplayText(JSON.stringify(rows, null, 2), 50_000)}
            </pre>
          </Section>
        </div>
      )}

      <FormDialog
        open={adding}
        onOpenChange={close}
        title={`Add ${noun}`}
        description="Records describe configuration; nothing is enforced until the module consumes it."
        submitLabel={`Add ${noun}`}
        pending={create.isPending}
        dirty={!!name || !!extra}
        error={create.isError ? safeErrorMessage(create.error, "Create failed") : undefined}
        onSubmit={() => {
          setTouched(true);
          if (name.trim() && !parsed.error) create.mutate();
        }}
      >
        <Field label="Name" error={nameMissing ? "Give the record a name." : undefined}>
          <Input value={name} onChange={(e) => setName(e.target.value)} aria-invalid={nameMissing} autoComplete="off" />
        </Field>
        <Section title="Additional fields" summary="Optional JSON" variant="plain">
          <Field label="Fields (JSON object)" hint="Merged into the request next to the name.">
            <textarea
              value={extra}
              onChange={(e) => setExtra(e.target.value)}
              rows={5}
              spellCheck={false}
              className="w-full rounded-md border border-input bg-background px-3 py-2 font-mono text-xs focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
              placeholder='{"key": "value"}'
            />
          </Field>
          <FieldError>{touched ? parsed.error : undefined}</FieldError>
        </Section>
      </FormDialog>

      <ConfirmDialog
        open={!!sending}
        onOpenChange={(o) => !o && setSending(null)}
        variant="default"
        title={`Send a test event to “${sending ? label(sending) : ""}”?`}
        description="Posts a wolf.test event to the destination now."
        confirmLabel="Send test event"
        pending={deliver.isPending}
        onConfirm={() => sending && deliver.mutate(sending.id)}
      />
      {typedDelete ? (
        <TypedConfirmDialog
          open={!!deleting}
          onOpenChange={(o) => !o && setDeleting(null)}
          expected={deleting ? label(deleting) : ""}
          title={`Delete ${noun} “${deleting ? label(deleting) : ""}”?`}
          description="Anything that references it stops resolving. This cannot be undone."
          confirmLabel={`Delete ${noun}`}
          pending={del.isPending}
          onConfirm={() => deleting && del.mutate(deleting.id)}
        />
      ) : (
        <ConfirmDialog
          open={!!deleting}
          onOpenChange={(o) => !o && setDeleting(null)}
          title={`Delete ${noun} “${deleting ? label(deleting) : ""}”?`}
          description="This cannot be undone."
          confirmLabel={`Delete ${noun}`}
          pending={del.isPending}
          onConfirm={() => deleting && del.mutate(deleting.id)}
        />
      )}
    </SettingsCard>
  );
}
