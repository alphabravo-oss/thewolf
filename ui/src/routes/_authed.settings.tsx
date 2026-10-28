// Administration settings; personal controls also live under /account.
//
// Layout only: a left sub-nav and the active section (components/settings/*).
// Tab state lives in the URL (?tab=…) so deep links work and refresh keeps the
// section. Scan presets are deliberately absent — wolf auto-detects per-repo
// language/framework.
import { createFileRoute, Link, redirect, useNavigate } from "@tanstack/react-router";
import { api } from "@/lib/api";
import { SETTINGS_SECTIONS, TAB_ALIASES } from "@/components/settings/sections";
import { PageHeader, PageShell } from "@/components/ui/page";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { cn } from "@/lib/utils";

const PERSONAL_SECTIONS = ["apikeys", "secrets", "nodes", "security"] as const;

export const Route = createFileRoute("/_authed/settings")({
  // Accept any string here; beforeLoad does the routing/redirects.
  validateSearch: (s: Record<string, unknown>): { tab: string; advanced?: boolean } => ({
    tab: typeof s.tab === "string" ? s.tab : "general",
    ...(s.advanced === true || s.advanced === "1" || s.advanced === "true" ? { advanced: true } : {}),
  }),
  beforeLoad: async ({ search }) => {
    const tab = (search as { tab?: string }).tab ?? "";
    if (tab === "audit") throw redirect({ to: "/audit" });
    const alias = TAB_ALIASES[tab];
    if (alias) throw redirect({ to: "/settings", search: alias });
    // Settings is admin-only; bounce everyone else to their own account.
    const me = await api.get<{ role: string }>("/auth/me").then((r) => r.data).catch(() => null);
    if (me?.role !== "admin") {
      const section = PERSONAL_SECTIONS.find((p) => p === tab) ?? "profile";
      throw redirect({ to: "/account", search: { section } });
    }
  },
  component: SettingsPage,
});

const GROUPS = [...new Set(SETTINGS_SECTIONS.map((s) => s.group))];

function SettingsPage() {
  const { tab, advanced } = Route.useSearch();
  const navigate = useNavigate();
  const active = SETTINGS_SECTIONS.find((s) => s.key === tab) ?? SETTINGS_SECTIONS[0];
  const go = (next: string) => navigate({ to: "/settings", search: { tab: next } });

  return (
    <PageShell className="min-w-0">
      <PageHeader
        title="Settings"
        description={
          <>
            Administration and global oversight. Your own profile, keys and secrets are also under{" "}
            <Link to="/account" search={{ section: "profile" }} className="text-foreground underline underline-offset-2">
              Account
            </Link>
            .
          </>
        }
      />
      <div className="grid gap-6 lg:grid-cols-[200px_minmax(0,1fr)]">
        <div className="lg:hidden">
          <Select value={active.key} onValueChange={go}>
            <SelectTrigger aria-label="Administration section"><SelectValue /></SelectTrigger>
            <SelectContent>
              {SETTINGS_SECTIONS.map(({ key, label }) => <SelectItem key={key} value={key}>{label}</SelectItem>)}
            </SelectContent>
          </Select>
        </div>
        <nav aria-label="Administration settings" className="hidden space-y-4 lg:block">
          {GROUPS.map((group) => (
            <div key={group} className="space-y-1">
              <p className="eyebrow px-3">{group}</p>
              {SETTINGS_SECTIONS.filter((s) => s.group === group).map(({ key, label, Icon }) => (
                <button
                  key={key}
                  type="button"
                  aria-current={active.key === key ? "page" : undefined}
                  onClick={() => go(key)}
                  className={cn(
                    "flex w-full items-center gap-2 rounded-md px-3 py-2 text-left text-sm",
                    active.key === key ? "bg-accent font-medium text-foreground" : "text-muted-foreground hover:bg-accent/50 hover:text-foreground",
                  )}
                >
                  <Icon className="size-4" aria-hidden />
                  {label}
                </button>
              ))}
            </div>
          ))}
        </nav>

        <div className="section-page space-y-4">
          <div>
            <h2 className="text-lg font-semibold tracking-tight">{active.label}</h2>
            <p className="mt-0.5 text-sm text-muted-foreground">{active.description}</p>
          </div>
          {active.render({ advanced })}
        </div>
      </div>
    </PageShell>
  );
}
