import { createFileRoute } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { toast } from "sonner";
import { api } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { PageSection } from "@/components/ui/page";
import { EnterpriseChrome, RecordsPanel } from "@/components/enterprise-records";

export const Route = createFileRoute("/_authed/enterprise/$")({
  component: EnterpriseModulePage,
});

const titles: Record<string, { title: string; description: string }> = {
  catalogs: { title: "Plugin catalogs", description: "Certified third-party catalogs. Not the first-party scanner factory." },
  compliance: { title: "Compliance", description: "Mappings, auditor reports, and legal hold." },
  reports: { title: "Reports", description: "Enterprise compliance reports." },
  support: { title: "Support", description: "Support cases and live diagnostics." },
  diagnostics: { title: "Diagnostics", description: "Redacted runtime diagnostics for support." },
  integrations: { title: "Integrations", description: "SIEM, ticketing, and extra SCM destinations." },
  siem: { title: "SIEM", description: "Webhook destinations. Send posts JSON to the stored HTTPS URL." },
  ticketing: { title: "Ticketing", description: "Ticket destinations." },
  residency: { title: "Residency", description: "Region and customer-managed key references. Do not store key material." },
  "customer-keys": { title: "Customer keys", description: "KMS/URI references only." },
  "attack-paths": { title: "Attack paths", description: "Consensus paths from clustered scanner evidence. Not a runtime CPG." },
  verifications: { title: "Verification", description: "Governed runtime verification. Production is denied by default." },
  packaging: { title: "Packaging", description: "Authenticated delivery catalog for this overlay." },
  proof: { title: "Overlay proof", description: "Registration probe. Harmless." },
};

function EnterpriseModulePage() {
  const { _splat } = Route.useParams();
  const key = _splat ?? "";
  const meta = titles[key] ?? {
    title: key.replace(/-/g, " ") || "Enterprise",
    description: "Enterprise module.",
  };
  const path = `/enterprise/${key}`;

  return (
    <EnterpriseChrome title={meta.title} description={meta.description}>
      {key === "diagnostics" || key === "support" ? <DiagnosticsPanel /> : null}
      {key === "packaging" ? <PackagingPanel /> : null}
      {key === "attack-paths" ? <AttackPathPanel /> : null}
      {key === "verifications" ? <VerifyPanel /> : null}
      {key === "siem" || key === "ticketing" ? (
        <RecordsPanel path={path} title={meta.title} send />
      ) : key === "diagnostics" || key === "packaging" || key === "proof" ? null : (
        <RecordsPanel path={path} title={meta.title} />
      )}
      {key === "support" ? <RecordsPanel path="/enterprise/support" title="Cases" /> : null}
      {key === "integrations" ? (
        <>
          <RecordsPanel path="/enterprise/siem" title="SIEM" send />
          <RecordsPanel path="/enterprise/ticketing" title="Ticketing" send />
        </>
      ) : null}
    </EnterpriseChrome>
  );
}

function DiagnosticsPanel() {
  const q = useQuery({
    queryKey: ["enterprise", "diagnostics"],
    queryFn: async () => (await api.get<Record<string, unknown>>("/enterprise/diagnostics")).data,
  });
  if (q.isError) {
    return <p className="text-sm text-muted-foreground">Diagnostics are not available in this edition.</p>;
  }
  const d = q.data ?? {};
  return (
    <PageSection title="Runtime">
      <dl className="grid gap-2 sm:grid-cols-2 text-sm">
        {Object.entries(d).map(([k, v]) =>
          k === "settings" ? null : (
            <div key={k} className="glass-card p-3">
              <dt className="text-xs text-muted-foreground">{k}</dt>
              <dd className="font-mono text-xs break-all">{typeof v === "string" ? v : JSON.stringify(v)}</dd>
            </div>
          ),
        )}
      </dl>
    </PageSection>
  );
}

function PackagingPanel() {
  const q = useQuery({
    queryKey: ["enterprise", "packaging"],
    queryFn: async () => (await api.get<Record<string, unknown>>("/enterprise/packaging")).data,
  });
  if (q.isError) {
    return <p className="text-sm text-muted-foreground">Packaging catalog is not available in this edition.</p>;
  }
  return (
    <pre className="glass-card p-4 text-xs overflow-auto">
      {q.isLoading ? "Loading…" : JSON.stringify(q.data ?? {}, null, 2)}
    </pre>
  );
}

function AttackPathPanel() {
  const qc = useQueryClient();
  const [id, setId] = useState("");
  const run = useMutation({
    mutationFn: async () =>
      api.post("/enterprise/attack-paths", { vulnerability_id: id.trim(), evidence: [] }),
    onSuccess: () => {
      setId("");
      qc.invalidateQueries({ queryKey: ["enterprise", "/enterprise/attack-paths"] });
      toast.success("Path stored");
    },
    onError: (e) => toast.error(e instanceof Error ? e.message : "Failed"),
  });
  return (
    <PageSection title="Compute" description="Builds a consensus path from evidence already clustered for that vulnerability id.">
      <form
        className="flex flex-wrap gap-2"
        onSubmit={(e) => {
          e.preventDefault();
          if (id.trim()) run.mutate();
        }}
      >
        <Input value={id} onChange={(e) => setId(e.target.value)} placeholder="Vulnerability id" className="max-w-sm" />
        <Button type="submit" size="sm" disabled={run.isPending || !id.trim()}>
          Compute
        </Button>
      </form>
    </PageSection>
  );
}

function VerifyPanel() {
  const qc = useQueryClient();
  const [id, setId] = useState("");
  const run = useMutation({
    mutationFn: async () =>
      api.post("/enterprise/verifications", { vulnerability_id: id.trim(), environment: "production" }),
    onSuccess: () => {
      setId("");
      qc.invalidateQueries({ queryKey: ["enterprise", "/enterprise/verifications"] });
      toast.success("Verification recorded");
    },
    onError: (e) => toast.error(e instanceof Error ? e.message : "Failed"),
  });
  return (
    <PageSection title="Run" description="Production-deny is the default. No exploit payload is executed.">
      <form
        className="flex flex-wrap gap-2"
        onSubmit={(e) => {
          e.preventDefault();
          if (id.trim()) run.mutate();
        }}
      >
        <Input value={id} onChange={(e) => setId(e.target.value)} placeholder="Vulnerability id" className="max-w-sm" />
        <Button type="submit" size="sm" disabled={run.isPending || !id.trim()}>
          Verify
        </Button>
      </form>
    </PageSection>
  );
}
