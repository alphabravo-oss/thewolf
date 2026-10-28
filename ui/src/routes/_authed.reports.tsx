// Reports (Enterprise, enterprise.compliance): the live "Security posture"
// report, saved snapshots and weekly/monthly schedules. Saved and scheduled
// reports cover every repository, so those tabs are admin-only (the API agrees).
import { useState, type ReactNode } from "react";
import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowLeftIcon, Loader2Icon, PrinterIcon, SaveIcon } from "lucide-react";
import { toast } from "sonner";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { SecurityReportView } from "@/components/reports/security-report";
import { Field, RowActions, SettingsCard, SettingsTable, shortDate } from "@/components/settings/shared";
import { AddButton } from "@/components/ui/add-button";
import { Button } from "@/components/ui/button";
import { EditionGate } from "@/components/ui/edition-gate";
import { ErrorState, LoadingState } from "@/components/ui/empty-state";
import { FormDialog } from "@/components/ui/form-dialog";
import { Input } from "@/components/ui/input";
import { PageHeader, PageShell } from "@/components/ui/page";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { api } from "@/lib/api";
import { useIsAdmin } from "@/lib/me";
import { scopeName, useSecurityReport, type ReportSchedule, type SavedReport } from "@/lib/reports";
import { safeErrorMessage } from "@/lib/safe-display";
import type { Collection } from "@/lib/types";

type Tab = "current" | "saved" | "schedules";
type Search = { tab?: Tab; report?: string; collection?: string };

export const Route = createFileRoute("/_authed/reports")({
  validateSearch: (s: Record<string, unknown>): Search => ({
    ...(s.tab === "saved" || s.tab === "schedules" ? { tab: s.tab } : {}),
    ...(typeof s.report === "string" && s.report ? { report: s.report } : {}),
    ...(typeof s.collection === "string" && s.collection ? { collection: s.collection } : {}),
  }),
  component: ReportsPage,
});

const ALL = "__all__";
const print = () => window.print();
const pdfButton = <Button variant="outline" onClick={print}><PrinterIcon />Download PDF</Button>;

function ReportsPage() {
  const search = Route.useSearch();
  const navigate = useNavigate();
  const admin = useIsAdmin();
  const go = (next: Search) => void navigate({ to: "/reports", search: next });
  const gate = (children: ReactNode) => <EditionGate feature="Reports" capability="enterprise.compliance"
    description="A print-ready security posture report for the whole fleet or one collection, saved snapshots, and weekly or monthly schedules. The Home dashboard and the API stay available in Community.">{children}</EditionGate>;
  if (search.report) return <PageShell>{gate(<SavedReportPage id={search.report} back={() => go({ tab: "saved" })} />)}</PageShell>;
  return <PageShell>
    <PageHeader className="print-hidden" title="Reports" description="Security posture across your repositories, ready to print or save." />
    {gate(<Tabs value={search.tab ?? "current"} onValueChange={(t) => go({ ...(t === "current" ? {} : { tab: t as Tab }) })}>
      <TabsList className="print-hidden">
        <TabsTrigger value="current">Current report</TabsTrigger>
        {admin && <TabsTrigger value="saved">Saved reports</TabsTrigger>}
        {admin && <TabsTrigger value="schedules">Schedules</TabsTrigger>}
      </TabsList>
      <TabsContent value="current" className="mt-5"><CurrentReport collection={search.collection} setCollection={(c) => go(c ? { collection: c } : {})} admin={admin} /></TabsContent>
      {admin && <TabsContent value="saved" className="mt-5"><SavedReports open={(id) => go({ report: id })} /></TabsContent>}
      {admin && <TabsContent value="schedules" className="mt-5"><Schedules /></TabsContent>}
    </Tabs>)}
  </PageShell>;
}

function useCollections() {
  return useQuery({ queryKey: ["collections", "all"], queryFn: async () => (await api.get<Collection[]>("/collections")).data ?? [] });
}

function ScopeSelect({ value, onChange, id }: { value?: string; onChange: (id?: string) => void; id?: string }) {
  const cols = useCollections();
  return <Select value={value ?? ALL} onValueChange={(v) => onChange(v === ALL ? undefined : v)}>
    <SelectTrigger id={id} aria-label="Report scope" className="h-9 w-64 max-w-full"><SelectValue /></SelectTrigger>
    <SelectContent>
      <SelectItem value={ALL}>All repositories</SelectItem>
      {(cols.data ?? []).map((c) => <SelectItem key={c.id} value={c.id}>{c.name}</SelectItem>)}
    </SelectContent>
  </Select>;
}

function CurrentReport({ collection, setCollection, admin }: { collection?: string; setCollection: (id?: string) => void; admin: boolean }) {
  const q = useSecurityReport(collection);
  const qc = useQueryClient();
  const save = useMutation({
    mutationFn: async () => (await api.post<SavedReport>("/enterprise/reports/generate", { collection_id: collection ?? "" })).data,
    onSuccess: () => {
      toast.success("Snapshot saved to Saved reports");
      return qc.invalidateQueries({ queryKey: ["reports", "saved"] });
    },
    onError: (e) => toast.error(safeErrorMessage(e, "Could not save the snapshot")),
  });
  return <div className="space-y-5">
    <div className="print-hidden flex flex-wrap items-center gap-2">
      <ScopeSelect value={collection} onChange={setCollection} />
      <div className="ml-auto flex flex-wrap gap-2">
        {admin && <Button variant="outline" disabled={save.isPending || !q.data} onClick={() => save.mutate()}>{save.isPending ? <Loader2Icon className="animate-spin" /> : <SaveIcon />}Save snapshot</Button>}
        {pdfButton}
      </div>
    </div>
    {q.isPending ? <LoadingState title="Building report" />
      : q.isError ? <ErrorState title="The report could not be built" description={safeErrorMessage(q.error, "Try again.")} onRetry={() => void q.refetch()} />
      : <SecurityReportView report={q.data} />}
  </div>;
}

function SavedReportPage({ id, back }: { id: string; back: () => void }) {
  const q = useQuery({ queryKey: ["reports", "saved", id], queryFn: async () => (await api.get<SavedReport>(`/enterprise/reports/${encodeURIComponent(id)}`)).data });
  const r = q.data;
  return <div className="space-y-5">
    <PageHeader className="print-hidden" title={r?.title || "Saved report"} breadcrumbs={[{ label: "Reports", link: { to: "/reports", search: { tab: "saved" } } }]}
      description={r ? `${r.source === "schedule" ? "Scheduled" : "Saved"} snapshot · ${scopeName(r.scope?.name)}` : undefined}
      actions={<div className="flex gap-2"><Button variant="ghost" onClick={back}><ArrowLeftIcon />Saved reports</Button>{r?.data && pdfButton}</div>} />
    {q.isPending ? <LoadingState title="Loading report" />
      : q.isError || !r?.data ? <ErrorState title="The saved report could not be loaded" description={q.isError ? safeErrorMessage(q.error, "It may have been deleted.") : "This record has no report data."} onRetry={() => void q.refetch()} />
      : <SecurityReportView report={r.data} title={r.title} />}
  </div>;
}

function SavedReports({ open }: { open: (id: string) => void }) {
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["reports", "saved"], queryFn: async () => (await api.get<SavedReport[]>("/enterprise/reports")).data ?? [] });
  const [deleting, setDeleting] = useState<SavedReport | null>(null);
  const del = useMutation({
    mutationFn: (id: string) => api.delete(`/enterprise/reports/${encodeURIComponent(id)}`),
    onSuccess: () => {
      toast.success("Report deleted");
      setDeleting(null);
      return qc.invalidateQueries({ queryKey: ["reports", "saved"] });
    },
    onError: (e) => toast.error(safeErrorMessage(e, "Delete failed")),
  });
  const title = (r: SavedReport) => r.title || r.id;
  return <SettingsCard title="Saved reports" label="Saved reports" description="Dated snapshots saved by hand or by a schedule. Each one renders exactly as it was generated." flush>
    <SettingsTable query={q} keyExtractor={(r) => r.id} persistKey="wolf-reports-saved" searchPlaceholder="Search saved reports..."
      empty={{ title: "No saved reports yet", description: "Use Save snapshot on the current report, or add a schedule." }}
      columns={[
        { key: "title", header: "Title", sortAccessor: title, accessor: (r) => <button type="button" className="text-left font-medium text-primary hover:underline underline-offset-4" onClick={() => open(r.id)}>{title(r)}</button> },
        { key: "scope", header: "Scope", sortAccessor: (r) => scopeName(r.scope?.name), accessor: (r) => scopeName(r.scope?.name) },
        { key: "generated", header: "Generated", sortAccessor: (r) => r.generated_at ?? "", accessor: (r) => <span className="text-xs text-muted-foreground">{r.generated_at ? new Date(r.generated_at).toLocaleString() : "—"}</span> },
        { key: "source", header: "Source", sortAccessor: (r) => r.source ?? "", accessor: (r) => r.source === "schedule" ? "Schedule" : "Manual" },
        { key: "actions", header: "", sortable: false, accessor: (r) => <RowActions label={title(r)} actions={[{ label: "View", onSelect: () => open(r.id) }, { label: "Delete report", destructive: true, onSelect: () => setDeleting(r) }]} /> },
      ]} />
    <ConfirmDialog open={!!deleting} onOpenChange={(o) => !o && setDeleting(null)} title={`Delete report “${deleting ? title(deleting) : ""}”?`}
      description="The saved snapshot is removed. This cannot be undone." confirmLabel="Delete report" pending={del.isPending} onConfirm={() => deleting && del.mutate(deleting.id)} />
  </SettingsCard>;
}

type Destination = { kind: string; id: string; name: string };
const DESTINATION_KINDS = [["integrations", "Integration"], ["siem", "SIEM"], ["ticketing", "Ticketing"]] as const;

// Webhook-style destinations a schedule can post a link to. Kinds the license
// does not include answer 404 and simply contribute nothing.
function useDestinations() {
  return useQuery({
    queryKey: ["reports", "destinations"],
    queryFn: async () => (await Promise.all(DESTINATION_KINDS.map(async ([kind, label]) => {
      const rows = await api.get<{ id: string; name?: string }[]>(`/enterprise/${kind}`).then((r) => r.data ?? [], () => []);
      return rows.map((d) => ({ kind, id: d.id, name: `${label}: ${d.name || d.id}` }));
    }))).flat() as Destination[],
  });
}

const NONE = "__none__";
const blank = { name: "", collection: undefined as string | undefined, cadence: "weekly" as "weekly" | "monthly", destination: NONE, enabled: true };

function Schedules() {
  const qc = useQueryClient();
  const key = ["reports", "schedules"];
  const q = useQuery({ queryKey: key, queryFn: async () => (await api.get<ReportSchedule[]>("/enterprise/report-schedules")).data ?? [] });
  const cols = useCollections();
  const dests = useDestinations();
  const [adding, setAdding] = useState(false);
  const [form, setForm] = useState(blank);
  const [touched, setTouched] = useState(false);
  const [deleting, setDeleting] = useState<ReportSchedule | null>(null);
  const close = (o: boolean) => {
    setAdding(o);
    if (!o) {
      setForm(blank);
      setTouched(false);
      create.reset();
    }
  };
  const create = useMutation({
    mutationFn: () => {
      const d = dests.data?.find((x) => `${x.kind}/${x.id}` === form.destination);
      return api.post("/enterprise/report-schedules", {
        name: form.name.trim(), cadence: form.cadence, enabled: form.enabled,
        collection_id: form.collection ?? "", collection_name: cols.data?.find((c) => c.id === form.collection)?.name ?? "",
        integration_kind: d?.kind ?? "", integration_id: d?.id ?? "", integration_name: d?.name ?? "",
        // Links in the delivered message point back at this Wolf.
        link_base: window.location.origin,
      });
    },
    onSuccess: () => {
      toast.success("Schedule added. The first snapshot is saved within 15 minutes.");
      close(false);
      return qc.invalidateQueries({ queryKey: key });
    },
  });
  const toggle = useMutation({
    mutationFn: (s: ReportSchedule) => api.put(`/enterprise/report-schedules/${encodeURIComponent(s.id)}`, { ...s, enabled: !s.enabled }),
    onSettled: () => qc.invalidateQueries({ queryKey: key }),
    onError: (e) => toast.error(safeErrorMessage(e, "Could not update the schedule")),
  });
  const del = useMutation({
    mutationFn: (id: string) => api.delete(`/enterprise/report-schedules/${encodeURIComponent(id)}`),
    onSuccess: () => {
      toast.success("Schedule deleted");
      setDeleting(null);
      return qc.invalidateQueries({ queryKey: key });
    },
    onError: (e) => toast.error(safeErrorMessage(e, "Delete failed")),
  });
  const label = (s: ReportSchedule) => s.name || s.id;
  const nameError = touched && !form.name.trim() ? "Give the schedule a name." : undefined;
  return <SettingsCard title="Schedules" label="Schedules" flush
    description="Each schedule saves a dated snapshot weekly (every 7 days) or monthly (once per calendar month), and can post a link to a webhook or Slack destination."
    actions={<AddButton thing="schedule" onClick={() => setAdding(true)} />}>
    <SettingsTable query={q} keyExtractor={(s) => s.id} persistKey="wolf-reports-schedules" searchPlaceholder="Search schedules..."
      empty={{ title: "No schedules yet", actionLabel: "Add schedule", onAction: () => setAdding(true) }}
      columns={[
        { key: "name", header: "Name", sortAccessor: label, accessor: (s) => <span className="font-medium">{label(s)}</span> },
        { key: "scope", header: "Scope", sortAccessor: (s) => scopeName(s.collection_name), accessor: (s) => scopeName(s.collection_name) },
        { key: "cadence", header: "Cadence", sortAccessor: (s) => s.cadence ?? "", accessor: (s) => s.cadence === "monthly" ? "Monthly" : "Weekly" },
        { key: "delivery", header: "Delivery", sortAccessor: (s) => s.integration_name ?? "", accessor: (s) => <span className="text-xs">{s.integration_id ? s.integration_name || s.integration_id : "Saved in Wolf only"}</span> },
        { key: "last", header: "Last run", sortAccessor: (s) => s.last_run_at ?? "", accessor: (s) => <span className="text-xs text-muted-foreground">{shortDate(s.last_run_at)}{s.last_error ? <span className="block text-destructive-text">{s.last_error}</span> : null}</span> },
        { key: "enabled", header: "Enabled", sortAccessor: (s) => (s.enabled ? 1 : 0), accessor: (s) => <Switch aria-label={`Enable ${label(s)}`} checked={!!s.enabled} disabled={toggle.isPending} onCheckedChange={() => toggle.mutate(s)} /> },
        { key: "actions", header: "", sortable: false, accessor: (s) => <RowActions label={label(s)} actions={[{ label: "Delete schedule", destructive: true, onSelect: () => setDeleting(s) }]} /> },
      ]} />
    <FormDialog open={adding} onOpenChange={close} title="Add report schedule" description="Snapshots are saved under Saved reports." submitLabel="Add schedule"
      pending={create.isPending} dirty={!!form.name || form.destination !== NONE || !!form.collection}
      error={create.isError ? safeErrorMessage(create.error, "Create failed") : undefined}
      onSubmit={() => {
        setTouched(true);
        if (form.name.trim()) create.mutate();
      }}>
      <Field label="Name" error={nameError}><Input value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} aria-invalid={!!nameError} autoComplete="off" placeholder="Weekly fleet posture" /></Field>
      <div className="space-y-1.5"><span className="block text-xs font-medium text-muted-foreground">Scope</span><ScopeSelect value={form.collection} onChange={(collection) => setForm({ ...form, collection })} /></div>
      <div className="space-y-1.5"><span className="block text-xs font-medium text-muted-foreground">Cadence</span>
        <Select value={form.cadence} onValueChange={(v) => setForm({ ...form, cadence: v as typeof form.cadence })}>
          <SelectTrigger aria-label="Cadence" className="h-9"><SelectValue /></SelectTrigger>
          <SelectContent><SelectItem value="weekly">Weekly</SelectItem><SelectItem value="monthly">Monthly</SelectItem></SelectContent>
        </Select>
      </div>
      <div className="space-y-1.5"><span className="block text-xs font-medium text-muted-foreground">Post a link to (optional)</span>
        <Select value={form.destination} onValueChange={(destination) => setForm({ ...form, destination })}>
          <SelectTrigger aria-label="Delivery destination" className="h-9"><SelectValue /></SelectTrigger>
          <SelectContent>
            <SelectItem value={NONE}>No delivery: save in Wolf only</SelectItem>
            {(dests.data ?? []).map((d) => <SelectItem key={`${d.kind}/${d.id}`} value={`${d.kind}/${d.id}`}>{d.name}</SelectItem>)}
          </SelectContent>
        </Select>
        <p className="text-xs text-muted-foreground">Wolf posts {"{text, report_id, url}"} as JSON, which a Slack incoming webhook accepts. Add destinations under Settings → Integrations.</p>
      </div>
      <div className="flex items-center gap-3"><Switch aria-label="Enabled" checked={form.enabled} onCheckedChange={(enabled) => setForm({ ...form, enabled })} /><span className="text-sm">Enabled</span></div>
    </FormDialog>
    <ConfirmDialog open={!!deleting} onOpenChange={(o) => !o && setDeleting(null)} title={`Delete schedule “${deleting ? label(deleting) : ""}”?`}
      description="No more snapshots are saved for it. Reports it already saved stay under Saved reports." confirmLabel="Delete schedule" pending={del.isPending} onConfirm={() => deleting && del.mutate(deleting.id)} />
  </SettingsCard>;
}
