// Personal account area — everything scoped to the current user. Reached from
// the top-right avatar menu. Admins get a separate /settings area for the
// system + global oversight surfaces.
import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { PageHeader } from "@/components/ui/page";
import { KeyIcon, KeyRoundIcon, LockIcon, ServerIcon, UserIcon } from "lucide-react";
import { AccountTab } from "@/components/settings/account";
import { SecurityTab } from "@/components/settings/mfa";
import { ApiKeysTab } from "@/components/settings/api-keys";
import { SecretsTab } from "@/components/settings/secrets";
import { NodesTab } from "@/components/settings/nodes";

type SectionKey = "profile" | "security" | "apikeys" | "secrets" | "nodes";

export const Route = createFileRoute("/_authed/account")({
  validateSearch: (s: Record<string, unknown>) => ({
    section:
      typeof s.section === "string" && /^(profile|security|apikeys|secrets|nodes)$/.test(s.section)
        ? (s.section as SectionKey)
        : ("profile" as SectionKey),
  }),
  component: AccountPage,
});

const SECTIONS: { key: SectionKey; label: string; Icon: typeof UserIcon }[] = [
  { key: "profile", label: "Profile", Icon: UserIcon },
  { key: "security", label: "Security", Icon: LockIcon },
  { key: "apikeys", label: "API Keys", Icon: KeyRoundIcon },
  { key: "secrets", label: "Secrets", Icon: KeyIcon },
  { key: "nodes", label: "Nodes", Icon: ServerIcon },
];

function AccountPage() {
  const { section } = Route.useSearch();
  const navigate = useNavigate();

  return (
    <div className="page stack min-w-0">
      <PageHeader
        title="Account"
        description="Your profile, sign-in security, and personal API keys."
      />
      <div className="grid gap-6 lg:grid-cols-[180px_minmax(0,1fr)]">
      <div className="lg:hidden">
        <Select value={section} onValueChange={(section) => navigate({ to: "/account", search: { section: section as SectionKey } })}>
          <SelectTrigger aria-label="Account section"><SelectValue /></SelectTrigger>
          <SelectContent>{SECTIONS.map(({key, label}) => <SelectItem key={key} value={key}>{label}</SelectItem>)}</SelectContent>
        </Select>
      </div>
      <nav aria-label="Account sections" className="hidden lg:block space-y-1">
        {SECTIONS.map(({ key, label, Icon }) => {
          const active = section === key;
          return (
            <button
              key={key}
              type="button"
              aria-current={active ? "page" : undefined}
              onClick={() => navigate({ to: "/account", search: { section: key } })}
              className={
                "flex w-full items-center gap-2 px-3 py-2 text-sm rounded-md text-left " +
                (active
                  ? "bg-accent font-medium text-foreground"
                  : "text-muted-foreground hover:bg-accent/50 hover:text-foreground")
              }
            >
              <Icon className="size-4" /> {label}
            </button>
          );
        })}
      </nav>

      <div className="section-page">
      {section === "profile" && <AccountTab />}
      {section === "security" && <SecurityTab />}
      {section === "apikeys" && <ApiKeysTab />}
      {section === "secrets" && <SecretsTab />}
      {section === "nodes" && <NodesTab />}
      </div>
      </div>
    </div>
  );
}
