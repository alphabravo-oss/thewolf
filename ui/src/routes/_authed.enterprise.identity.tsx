import { createFileRoute } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { api } from "@/lib/api";
import { EnterpriseChrome, RecordsPanel } from "@/components/enterprise-records";
import { PageSection } from "@/components/ui/page";

export const Route = createFileRoute("/_authed/enterprise/identity")({
  component: IdentityPage,
});

type Provider = { name: string; kind: string };

function IdentityPage() {
  const providers = useQuery({
    queryKey: ["auth", "providers"],
    queryFn: async () => {
      const r = await api.get<{ providers?: Provider[]; local_break_glass?: boolean }>(
        "/auth/providers",
      );
      return r.data;
    },
  });
  const list = providers.data?.providers ?? [];

  return (
    <EnterpriseChrome
      title="Identity"
      description="SSO, directory, group mapping, and SCIM. Local email/password stays available for break-glass."
    >
      <PageSection title="Providers" description="Configured on this Enterprise binary.">
        {providers.isLoading ? (
          <p className="text-sm text-muted-foreground">Loading…</p>
        ) : list.length === 0 ? (
          <p className="text-sm text-muted-foreground">Only local authentication is configured.</p>
        ) : (
          <ul className="grid gap-2 sm:grid-cols-2">
            {list.map((p) => (
              <li key={p.name} className="glass-card p-4 space-y-2">
                <p className="text-sm font-medium capitalize">{p.name}</p>
                <p className="text-xs text-muted-foreground">{p.kind}</p>
                {p.kind === "redirect" ? (
                  <a
                    href={`/api/v1/auth/sso/${encodeURIComponent(p.name)}/start`}
                    className="inline-flex h-8 items-center text-xs font-medium text-primary hover:underline"
                  >
                    Test sign-in
                  </a>
                ) : null}
              </li>
            ))}
          </ul>
        )}
        {providers.data?.local_break_glass ? (
          <p className="text-xs text-muted-foreground">Local break-glass sign-in remains enabled.</p>
        ) : null}
      </PageSection>
      <RecordsPanel
        path="/enterprise/group-mappings"
        title="Group mappings"
        description="Map IdP groups to Wolf roles (name is the IdP group)."
      />
      <PageSection title="SCIM 2.0" description="IdP provisioning against this instance.">
        <p className="text-sm text-muted-foreground">
          Base URL <code className="font-mono text-xs">/api/v1/scim/v2</code>. Set{" "}
          <code className="font-mono text-xs">WOLF_SCIM_TOKEN</code> and send it as a Bearer token.
          Users and groups are created in Wolf when the IdP pushes them.
        </p>
      </PageSection>
    </EnterpriseChrome>
  );
}
