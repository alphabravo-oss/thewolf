import { createFileRoute } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { toast } from "sonner";
import { api } from "@/lib/api";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { EnterpriseChrome, RecordCount, RecordsPanel } from "@/components/enterprise-records";
import { Field, SettingsCard } from "@/components/settings/shared";
import { AddButton } from "@/components/ui/add-button";
import { Button } from "@/components/ui/button";
import { ErrorState } from "@/components/ui/empty-state";
import { FormDialog } from "@/components/ui/form-dialog";
import { Input } from "@/components/ui/input";
import { Section } from "@/components/ui/section";
import { enterpriseModuleMeta } from "@/lib/enterprise-modules";
import { safeDisplayText, safeErrorMessage } from "@/lib/safe-display";

export const Route = createFileRoute("/_authed/enterprise/$")({
  component: EnterpriseModulePage,
});

function EnterpriseModulePage() {
  const { _splat } = Route.useParams();
  const key = _splat ?? "";
  const meta = enterpriseModuleMeta(key);
  const path = `/enterprise/${key}`;

  let kpis;
  let body;
  switch (key) {
    case "siem":
      body = <RecordsPanel path={path} title="Destinations" noun="destination" description="Send posts JSON to the stored HTTPS URL." send />;
      break;
    case "ticketing":
      body = <RecordsPanel path={path} title="Destinations" noun="destination" description="Where tickets are opened." send />;
      break;
    case "integrations":
      kpis = (
        <>
          <RecordCount title="SIEM destinations" path="/enterprise/siem" />
          <RecordCount title="Ticketing destinations" path="/enterprise/ticketing" />
        </>
      );
      body = (
        <>
          <RecordsPanel path="/enterprise/siem" title="SIEM" noun="destination" send />
          <RecordsPanel path="/enterprise/ticketing" title="Ticketing" noun="destination" send />
        </>
      );
      break;
    case "catalogs":
      body = <RecordsPanel path={path} title="Catalogs" noun="catalog" />;
      break;
    case "residency":
      body = <RecordsPanel path={path} title="Residency records" noun="residency record" typedDelete />;
      break;
    case "customer-keys":
      body = <RecordsPanel path={path} title="Key references" noun="key reference" description="References only. Do not store key material." typedDelete />;
      break;
    case "reports":
      body = <RecordsPanel path={path} title="Reports" noun="report" />;
      break;
    case "compliance":
      body = <RecordsPanel path={path} title="Compliance records" noun="record" description="Mappings, auditor reports, and legal hold." />;
      break;
    case "support":
      kpis = <RecordCount title="Support cases" path="/enterprise/support" />;
      body = (
        <>
          <RecordsPanel path={path} title="Cases" noun="case" />
          <DiagnosticsPanel />
        </>
      );
      break;
    case "diagnostics":
      body = <DiagnosticsPanel />;
      break;
    case "packaging":
      body = <PackagingPanel />;
      break;
    case "attack-paths":
      kpis = <RecordCount title="Attack paths" path={path} />;
      body = <RecordsPanel path={path} title="Computed paths" noun="path" addable={false} actions={<ComputeAttackPath />} />;
      break;
    case "verifications":
      kpis = <RecordCount title="Verifications" path={path} />;
      body = <RecordsPanel path={path} title="Verification runs" noun="verification" addable={false} actions={<RunVerification />} />;
      break;
    case "proof":
      body = null;
      break;
    default:
      body = <RecordsPanel path={path} title={meta.title} />;
  }

  return (
    <EnterpriseChrome title={meta.title} description={meta.purpose} kpis={kpis}>
      {body}
    </EnterpriseChrome>
  );
}

function DiagnosticsPanel() {
  const q = useQuery({
    queryKey: ["enterprise", "diagnostics"],
    queryFn: async () => (await api.get<Record<string, unknown>>("/enterprise/diagnostics")).data,
    retry: false,
  });
  if (q.isError) return <ErrorState title="Diagnostics are not available in this edition" onRetry={() => void q.refetch()} />;
  const d = q.data ?? {};
  return (
    <SettingsCard title="Runtime" description="Redacted runtime details to attach to a support case.">
      {q.isLoading ? (
        <p role="status" className="text-sm text-muted-foreground">Loading…</p>
      ) : (
        <>
          <dl className="grid gap-2 text-sm sm:grid-cols-2">
            {Object.entries(d).map(([k, v]) =>
              k === "settings" ? null : (
                <div key={k} className="rounded-md border border-border p-3">
                  <dt className="text-xs text-muted-foreground">{k}</dt>
                  <dd className="break-all font-mono text-xs">{typeof v === "string" ? v : JSON.stringify(v)}</dd>
                </div>
              ),
            )}
          </dl>
          <Section title="Raw diagnostics" summary="JSON as returned by the API" variant="plain">
            <pre className="max-h-96 overflow-auto rounded-md bg-muted/30 p-3 font-mono text-xs">{safeDisplayText(JSON.stringify(d, null, 2), 50_000)}</pre>
          </Section>
        </>
      )}
    </SettingsCard>
  );
}

function PackagingPanel() {
  const q = useQuery({
    queryKey: ["enterprise", "packaging"],
    queryFn: async () => (await api.get<Record<string, unknown>>("/enterprise/packaging")).data,
    retry: false,
  });
  if (q.isError) return <ErrorState title="Packaging catalog is not available in this edition" onRetry={() => void q.refetch()} />;
  return (
    <SettingsCard title="Delivery catalog" description="Authenticated delivery catalog for this overlay.">
      <Section title="Raw catalog" summary="JSON as returned by the API" defaultOpen>
        <pre className="max-h-[32rem] overflow-auto rounded-md bg-muted/30 p-3 font-mono text-xs">
          {q.isLoading ? "Loading…" : safeDisplayText(JSON.stringify(q.data ?? {}, null, 2), 100_000)}
        </pre>
      </Section>
    </SettingsCard>
  );
}

/** A vulnerability-id dialog for the two "run something" modules. */
function useRunByVulnerability(path: string, body: (id: string) => unknown, success: string) {
  const qc = useQueryClient();
  const [open, setOpen] = useState(false);
  const [id, setId] = useState("");
  const [touched, setTouched] = useState(false);
  const close = (next: boolean) => {
    setOpen(next);
    if (!next) {
      setId("");
      setTouched(false);
      run.reset();
    }
  };
  const run = useMutation({
    mutationFn: async () => api.post(path, body(id.trim())),
    onSuccess: () => {
      toast.success(success);
      close(false);
      qc.invalidateQueries({ queryKey: ["enterprise", path] });
    },
  });
  return { open, setOpen, id, setId, touched, setTouched, close, run };
}

function ComputeAttackPath() {
  const f = useRunByVulnerability("/enterprise/attack-paths", (id) => ({ vulnerability_id: id, evidence: [] }), "Path stored");
  return (
    <>
      <AddButton thing="attack path" onClick={() => f.setOpen(true)} />
      <FormDialog
        open={f.open}
        onOpenChange={f.close}
        title="Compute attack path"
        description="Builds a consensus path from evidence already clustered for that vulnerability id."
        submitLabel="Compute"
        pending={f.run.isPending}
        dirty={!!f.id}
        error={f.run.isError ? safeErrorMessage(f.run.error, "Failed") : undefined}
        onSubmit={() => {
          f.setTouched(true);
          if (f.id.trim()) f.run.mutate();
        }}
      >
        <Field label="Vulnerability id" error={f.touched && !f.id.trim() ? "Enter a vulnerability id." : undefined}>
          <Input value={f.id} onChange={(e) => f.setId(e.target.value)} aria-invalid={f.touched && !f.id.trim()} autoComplete="off" />
        </Field>
      </FormDialog>
    </>
  );
}

function RunVerification() {
  const f = useRunByVulnerability("/enterprise/verifications", (id) => ({ vulnerability_id: id, environment: "production" }), "Verification recorded");
  const [confirm, setConfirm] = useState(false);
  return (
    <>
      <Button type="button" size="sm" onClick={() => f.setOpen(true)}>Run verification</Button>
      <FormDialog
        open={f.open}
        onOpenChange={f.close}
        title="Run verification"
        description="Production-deny is the default. No exploit payload is executed."
        submitLabel="Review and run"
        dirty={!!f.id}
        onSubmit={() => {
          f.setTouched(true);
          if (f.id.trim()) setConfirm(true);
        }}
      >
        <Field label="Vulnerability id" error={f.touched && !f.id.trim() ? "Enter a vulnerability id." : undefined}>
          <Input value={f.id} onChange={(e) => f.setId(e.target.value)} aria-invalid={f.touched && !f.id.trim()} autoComplete="off" />
        </Field>
      </FormDialog>
      <ConfirmDialog
        open={confirm}
        onOpenChange={setConfirm}
        variant="default"
        title="Run verification?"
        description={`Records a governed verification for ${f.id.trim()} against production. Production is denied unless policy allows it.`}
        confirmLabel="Run verification"
        pending={f.run.isPending}
        onConfirm={() => f.run.mutate(undefined, { onSettled: () => setConfirm(false) })}
      />
    </>
  );
}
