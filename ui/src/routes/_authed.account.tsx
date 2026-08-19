// Personal account area — everything scoped to the current user. Reached from
// the top-right avatar menu. Admins get a separate /settings area for the
// system + global oversight surfaces.
import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { PageHeader } from "@/components/ui/page";
import { KeyIcon, KeyRoundIcon, LockIcon, ServerIcon, UserIcon } from "lucide-react";
import {
  AccountTab,
  SecurityTab,
  ApiKeysTab,
  SecretsTab,
  NodesTab,
} from "./_authed.settings";

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
    <div className="page stack page--narrow">
      <PageHeader
        title="Account"
        description="Your profile, sign-in security, and personal API keys."
      />
      <nav className="flex gap-1 border-b border-border">
        {SECTIONS.map(({ key, label, Icon }) => {
          const active = section === key;
          return (
            <button
              key={key}
              type="button"
              onClick={() => navigate({ to: "/account", search: { section: key } })}
              className={
                "inline-flex items-center gap-1.5 px-3 h-9 text-sm border-b-2 -mb-px " +
                (active
                  ? "border-primary text-foreground"
                  : "border-transparent text-muted-foreground hover:text-foreground")
              }
            >
              <Icon className="size-4" /> {label}
            </button>
          );
        })}
      </nav>

      {section === "profile" && <AccountTab />}
      {section === "security" && <SecurityTab />}
      {section === "apikeys" && <ApiKeysTab />}
      {section === "secrets" && <SecretsTab />}
      {section === "nodes" && <NodesTab />}
    </div>
  );
}
