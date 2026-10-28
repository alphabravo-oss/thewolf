// Users & access → Single sign-on and Directory sync. Providers are configured
// on the Enterprise binary (environment), so these pages show what is live and
// how to set it; Community renders the locked treatment.
import { useQuery } from "@tanstack/react-query";
import { api } from "@/lib/api";
import { EditionGate } from "@/components/ui/edition-gate";
import { Section } from "@/components/ui/section";
import { StatusBadge } from "@/components/ui/status-badge";
import { RowActions, SettingsCard, SettingsTable } from "./shared";

const IDENTITY = "enterprise.identity";
type Provider = { name: string; kind: string };

const code = (s: string) => <code className="font-mono text-xs">{s}</code>;

export function SsoSection() {
  return (
    <EditionGate feature="Single sign-on" capability={IDENTITY} description="OIDC, SAML and LDAP sign-in. Local email and password sign-in stays available in every edition.">
      <SsoProviders />
    </EditionGate>
  );
}

function SsoProviders() {
  const q = useQuery({
    queryKey: ["auth", "providers"],
    queryFn: async () => (await api.get<{ providers?: Provider[]; local_break_glass?: boolean }>("/auth/providers")).data,
  });
  return (
    <div className="space-y-4">
      <SettingsCard
        title="Sign-in providers"
        label="Sign-in providers"
        description={q.data?.local_break_glass === false ? "Configured on this server." : "Configured on this server. Local sign-in stays on for break-glass access."}
        flush
      >
        <SettingsTable
          query={{ ...q, data: q.data?.providers ?? [] }}
          keyExtractor={(p) => p.name}
          persistKey="wolf-settings-sso"
          searchPlaceholder="Search providers..."
          empty={{ title: "Only local sign-in is configured", description: "Set the variables below and restart Wolf." }}
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
      <SettingsCard title="Configure a provider" description="Providers are read from the server environment at start-up.">
        <Section title="OIDC" summary="Okta, Entra ID, Google, Keycloak" variant="plain" defaultOpen>
          <p className="text-sm text-muted-foreground">
            {code("WOLF_OIDC_ISSUER")}, {code("WOLF_OIDC_CLIENT_ID")}, {code("WOLF_OIDC_CLIENT_SECRET")} and {code("WOLF_OIDC_REDIRECT_URL")}. Several providers: {code("WOLF_OIDC_PROVIDERS")} (JSON list).
          </p>
        </Section>
        <Section title="SAML" summary="Metadata URL or SSO URL" variant="plain">
          <p className="text-sm text-muted-foreground">
            {code("WOLF_SAML_METADATA_URL")} or {code("WOLF_SAML_SSO_URL")} with {code("WOLF_SAML_IDP_ENTITY_ID")}, plus {code("WOLF_SAML_SP_ENTITY_ID")} and {code("WOLF_SAML_ACS_URL")}.
          </p>
        </Section>
        <Section title="LDAP" summary="Directory bind" variant="plain">
          <p className="text-sm text-muted-foreground">
            {code("WOLF_LDAP_URL")}, {code("WOLF_LDAP_BIND_DN")}, {code("WOLF_LDAP_BIND_PASSWORD")}, {code("WOLF_LDAP_BASE_DN")}; optional {code("WOLF_LDAP_USER_FILTER")}, {code("WOLF_LDAP_EMAIL_ATTR")}, {code("WOLF_LDAP_START_TLS")}.
          </p>
        </Section>
      </SettingsCard>
    </div>
  );
}

export function DirectorySyncSection() {
  return (
    <EditionGate feature="Directory sync" capability={IDENTITY} description="SCIM 2.0 provisioning from your identity provider. Add users by hand under Users in every edition.">
      <SettingsCard title="SCIM 2.0" description="Users and groups are created in Wolf when your identity provider pushes them.">
        <dl className="grid gap-3 text-sm sm:grid-cols-2">
          <div className="rounded-md border border-border p-3">
            <dt className="text-xs text-muted-foreground">Base URL</dt>
            <dd className="break-all font-mono text-xs">{`${window.location.origin}/api/v1/scim/v2`}</dd>
          </div>
          <div className="rounded-md border border-border p-3">
            <dt className="text-xs text-muted-foreground">Authentication</dt>
            <dd className="text-xs">Bearer token from {code("WOLF_SCIM_TOKEN")} on the server</dd>
          </div>
        </dl>
      </SettingsCard>
    </EditionGate>
  );
}
