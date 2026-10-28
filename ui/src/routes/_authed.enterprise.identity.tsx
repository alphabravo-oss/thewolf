import { createFileRoute } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { api } from "@/lib/api";
import { EnterpriseChrome, RecordCount, RecordsPanel } from "@/components/enterprise-records";
import { RowActions, SettingsCard, SettingsTable } from "@/components/settings/shared";
import { MetricCard } from "@/components/ui/metric-card";
import { Section } from "@/components/ui/section";
import { StatusBadge } from "@/components/ui/status-badge";

export const Route = createFileRoute("/_authed/enterprise/identity")({
  component: IdentityPage,
});

type Provider = { name: string; kind: string };

function IdentityPage() {
  const providers = useQuery({
    queryKey: ["auth", "providers"],
    queryFn: async () => {
      const r = await api.get<{ providers?: Provider[]; local_break_glass?: boolean }>("/auth/providers");
      return r.data;
    },
  });
  const list = providers.data?.providers ?? [];

  return (
    <EnterpriseChrome
      title="Identity"
      description="SSO, directory, group mapping, and SCIM. Local email/password stays available for break-glass."
      kpis={
        <>
          <MetricCard title="Sign-in providers" value={providers.isLoading ? "…" : list.length} subtitle={providers.data?.local_break_glass ? "Local break-glass enabled" : undefined} />
          <RecordCount title="Group mappings" path="/enterprise/group-mappings" />
        </>
      }
    >
      <SettingsCard title="Providers" description="Configured on this Enterprise binary." flush>
        <SettingsTable
          query={{ ...providers, data: list }}
          keyExtractor={(p) => p.name}
          persistKey="wolf-enterprise-providers"
          searchPlaceholder="Search providers..."
          empty={{ title: "Only local authentication is configured" }}
          columns={[
            { key: "name", header: "Name", sortAccessor: (p) => p.name, accessor: (p) => <span className="font-medium capitalize">{p.name}</span> },
            { key: "kind", header: "Kind", sortAccessor: (p) => p.kind, accessor: (p) => <StatusBadge status="info" label={p.kind} size="sm" showDot={false} /> },
            {
              key: "actions", header: "", sortable: false,
              accessor: (p) => (
                <RowActions
                  label={p.name}
                  actions={[p.kind === "redirect" && { label: "Test sign-in", onSelect: () => window.location.assign(`/api/v1/auth/sso/${encodeURIComponent(p.name)}/start`) }]}
                />
              ),
            },
          ]}
        />
      </SettingsCard>
      <RecordsPanel
        path="/enterprise/group-mappings"
        title="Group mappings"
        noun="group mapping"
        description="Map IdP groups to Wolf roles (name is the IdP group)."
      />
      <SettingsCard title="SCIM 2.0" description="IdP provisioning against this instance.">
        <p className="text-sm text-muted-foreground">
          Users and groups are created in Wolf when the IdP pushes them.
        </p>
        <Section title="Connection details" summary="Base URL and token" variant="plain" defaultOpen>
          <p className="text-sm text-muted-foreground">
            Base URL <code className="font-mono text-xs">/api/v1/scim/v2</code>. Set{" "}
            <code className="font-mono text-xs">WOLF_SCIM_TOKEN</code> and send it as a Bearer token.
          </p>
        </Section>
      </SettingsCard>
    </EnterpriseChrome>
  );
}
