// Enterprise admin modules: what the hub lists and what each module page shows
// as its title. `capability` is the entitlement that unlocks the module (null =
// part of core Wolf); `to`/`search` is where its primary action goes.
import {
  BuildingIcon, FileCheckIcon, FileTextIcon, GlobeIcon, KeyRoundIcon, LifeBuoyIcon, PackageIcon, PlugIcon,
  RouteIcon, ScrollTextIcon, ShieldCheckIcon, StethoscopeIcon, TicketIcon, WrenchIcon,
  RadioTowerIcon, type LucideIcon,
} from "lucide-react";

export type EnterpriseGroup = "Identity & access" | "Governance" | "Integrations" | "Data" | "Operations";

export type EnterpriseModule = {
  /** Splat key under /enterprise/, or the page's own slug. */
  key: string;
  title: string;
  group: EnterpriseGroup;
  purpose: string;
  capability: string | null;
  to: string;
  search?: Record<string, string>;
  action: string;
  Icon: LucideIcon;
  /** Records endpoint whose length is shown as the card's status. */
  countPath?: string;
};

export const ENTERPRISE_GROUPS: EnterpriseGroup[] = ["Identity & access", "Governance", "Integrations", "Data", "Operations"];

export const ENTERPRISE_MODULES: EnterpriseModule[] = [
  { key: "identity", title: "Identity", group: "Identity & access", purpose: "SSO, directory sync, group mapping and SCIM. Local sign-in stays as break-glass.", capability: "enterprise.identity", to: "/enterprise/identity", action: "Manage identity", Icon: KeyRoundIcon, countPath: "/enterprise/group-mappings" },
  { key: "tenancy", title: "Tenancy", group: "Identity & access", purpose: "Workspaces, applications, projects and custom roles.", capability: "enterprise.tenancy", to: "/enterprise/tenancy", action: "Manage tenancy", Icon: BuildingIcon, countPath: "/enterprise/workspaces" },
  { key: "compliance", title: "Compliance", group: "Governance", purpose: "Control mappings, auditor reports and legal hold.", capability: "enterprise.compliance", to: "/enterprise/compliance", action: "Open compliance", Icon: FileCheckIcon, countPath: "/enterprise/compliance" },
  { key: "reports", title: "Reports", group: "Governance", purpose: "Enterprise compliance reports.", capability: "enterprise.compliance", to: "/enterprise/reports", action: "Open reports", Icon: FileTextIcon, countPath: "/enterprise/reports" },
  { key: "audit", title: "Audit log", group: "Governance", purpose: "Every administrative action, who did it and when. Included in every edition.", capability: null, to: "/audit", action: "Open audit log", Icon: ScrollTextIcon },
  { key: "siem", title: "SIEM", group: "Integrations", purpose: "Webhook destinations that receive Wolf events as JSON.", capability: "enterprise.integrations", to: "/enterprise/siem", action: "Manage SIEM", Icon: RadioTowerIcon, countPath: "/enterprise/siem" },
  { key: "ticketing", title: "Ticketing", group: "Integrations", purpose: "Ticket destinations for findings and fixes.", capability: "enterprise.integrations", to: "/enterprise/ticketing", action: "Manage ticketing", Icon: TicketIcon, countPath: "/enterprise/ticketing" },
  { key: "catalogs", title: "Plugin catalogs", group: "Integrations", purpose: "Certified third-party catalogs. Not the first-party scanner factory.", capability: "enterprise.plugins", to: "/enterprise/catalogs", action: "Manage catalogs", Icon: PlugIcon, countPath: "/enterprise/catalogs" },
  { key: "residency", title: "Residency", group: "Data", purpose: "Region pinning and customer-managed key references. No key material is stored.", capability: "enterprise.residency", to: "/enterprise/residency", action: "Manage residency", Icon: GlobeIcon, countPath: "/enterprise/residency" },
  { key: "customer-keys", title: "Customer keys", group: "Data", purpose: "KMS and URI references for customer-managed encryption.", capability: "enterprise.residency", to: "/enterprise/customer-keys", action: "Manage keys", Icon: KeyRoundIcon, countPath: "/enterprise/customer-keys" },
  { key: "fixer", title: "Fixer fleet", group: "Operations", purpose: "Autonomous AI fixing: engines, sign-in and git access.", capability: "enterprise.remediation", to: "/settings", search: { tab: "fixer" }, action: "Open fixer settings", Icon: WrenchIcon },
  { key: "attack-paths", title: "Attack paths", group: "Operations", purpose: "Consensus paths from clustered scanner evidence. Not a runtime CPG.", capability: "enterprise.intelligence", to: "/enterprise/attack-paths", action: "Open attack paths", Icon: RouteIcon, countPath: "/enterprise/attack-paths" },
  { key: "verifications", title: "Verification", group: "Operations", purpose: "Governed runtime verification. Production is denied by default.", capability: "enterprise.verification", to: "/enterprise/verifications", action: "Open verification", Icon: ShieldCheckIcon, countPath: "/enterprise/verifications" },
  { key: "packaging", title: "Packaging", group: "Operations", purpose: "Authenticated delivery catalog for this overlay.", capability: "enterprise.packaging", to: "/enterprise/packaging", action: "Open packaging", Icon: PackageIcon },
  { key: "support", title: "Support", group: "Operations", purpose: "Support cases and live diagnostics.", capability: "enterprise.support", to: "/enterprise/support", action: "Open support", Icon: LifeBuoyIcon, countPath: "/enterprise/support" },
  { key: "diagnostics", title: "Diagnostics", group: "Operations", purpose: "Redacted runtime diagnostics to attach to a support case.", capability: "enterprise.support", to: "/enterprise/diagnostics", action: "Open diagnostics", Icon: StethoscopeIcon },
];

/** Titles for pages without a hub card (still reachable by URL). */
export const EXTRA_TITLES: Record<string, { title: string; purpose: string }> = {
  integrations: { title: "Integrations", purpose: "SIEM, ticketing, and extra SCM destinations." },
  proof: { title: "Overlay proof", purpose: "Registration probe. Harmless." },
};

export function enterpriseModuleMeta(key: string): { title: string; purpose: string } {
  const m = ENTERPRISE_MODULES.find((x) => x.key === key);
  if (m) return m;
  return EXTRA_TITLES[key] ?? { title: key.replace(/-/g, " ") || "Enterprise", purpose: "Enterprise module." };
}

