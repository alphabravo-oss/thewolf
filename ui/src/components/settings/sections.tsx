// The Settings sections: one entry per left-nav item. Each renders a page of
// titled Cards; the route owns only layout and the ?tab= deep links.
import type { ComponentType, ReactNode } from "react";
import {
  BadgeCheckIcon, CalendarClockIcon, DatabaseIcon, GitBranchIcon, KeyIcon, KeyRoundIcon, LayersIcon,
  RefreshCwIcon, ServerIcon, SettingsIcon, ShieldCheckIcon, UserIcon, UsersIcon, WrenchIcon,
} from "lucide-react";
import { FixerSettings } from "@/components/fixes/fixer-settings";
import { GitAccessCard } from "@/components/fixes/git-access";
import { LicenseTab } from "@/components/license-settings";
import { EditionGate } from "@/components/ui/edition-gate";
import { REMEDIATION } from "@/lib/edition";
import { AccountSection } from "./account";
import { ApiKeysSection } from "./api-keys";
import { DatabaseSection } from "./database";
import { GeneralSection } from "./general";
import { ImagesSection } from "./images";
import { NodesSection } from "./nodes";
import { ScannersSection } from "./scanners";
import { SchedulingSection } from "./scheduling";
import { SecretsSection } from "./secrets";
import { SecuritySection } from "./security";
import { UsersSection } from "./users";

export type SectionDef = {
  key: string;
  label: string;
  group: string;
  Icon: ComponentType<{ className?: string; "aria-hidden"?: boolean }>;
  description: string;
  render: (ctx: { advanced?: boolean }) => ReactNode;
};

export const SETTINGS_SECTIONS: SectionDef[] = [
  { key: "general", label: "General", group: "Workspace", Icon: SettingsIcon, description: "Edition and instance-wide switches.", render: () => <GeneralSection /> },
  { key: "account", label: "Account & MFA", group: "Workspace", Icon: UserIcon, description: "Your own profile, password and two-factor authentication.", render: () => <AccountSection /> },
  { key: "users", label: "Users", group: "Workspace", Icon: UsersIcon, description: "Who can sign in and what they can change.", render: () => <UsersSection /> },
  { key: "security", label: "Security", group: "Workspace", Icon: ShieldCheckIcon, description: "Sign-in policy for every account.", render: () => <SecuritySection /> },
  { key: "apikeys", label: "API keys", group: "Credentials", Icon: KeyRoundIcon, description: "Scoped keys for the CLI and CI, and an overview of every user's keys.", render: () => <ApiKeysSection /> },
  { key: "secrets", label: "Secrets & credentials", group: "Credentials", Icon: KeyIcon, description: "Encrypted tokens, SSH material and model keys.", render: () => <SecretsSection /> },
  { key: "git", label: "Git access", group: "Credentials", Icon: GitBranchIcon, description: "Host-bound tokens the fixer uses to push branches and open pull requests.", render: () => <GitAccessCard /> },
  {
    key: "fixer", label: "Fixer engines", group: "Scanning & fixing", Icon: WrenchIcon, description: "Which AI engine fixes findings, its model, and how it signs in.",
    render: () => (
      <EditionGate feature="Autonomous AI fixing" capability={REMEDIATION} description="Scanning, findings, baselines and reports are fully available in Community.">
        <FixerSettings />
      </EditionGate>
    ),
  },
  { key: "scanners", label: "Scanners & updates", group: "Scanning & fixing", Icon: RefreshCwIcon, description: "Scanner runtime, diagnostics and the update policy.", render: ({ advanced }) => <ScannersSection advanced={advanced} /> },
  { key: "images", label: "Images", group: "Scanning & fixing", Icon: LayersIcon, description: "Scanner image digests and updates.", render: () => <ImagesSection /> },
  { key: "nodes", label: "Nodes", group: "Scanning & fixing", Icon: ServerIcon, description: "Remote SSH hosts Wolf can scan.", render: () => <NodesSection /> },
  { key: "scheduling", label: "Scheduling", group: "Scanning & fixing", Icon: CalendarClockIcon, description: "How many scanner tools run at once.", render: () => <SchedulingSection /> },
  { key: "database", label: "Database & disk", group: "System", Icon: DatabaseIcon, description: "Where Wolf stores data and how much space it uses.", render: () => <DatabaseSection /> },
  { key: "license", label: "License", group: "System", Icon: BadgeCheckIcon, description: "Edition, entitlements and license install.", render: () => <LicenseTab /> },
];

// Old ?tab= values that now live inside another section.
export const TAB_ALIASES: Record<string, { tab: string; advanced?: boolean }> = {
  "scanner-updates": { tab: "scanners", advanced: true },
};

