import { REMEDIATION, useEdition } from "@/lib/edition";
import { KnobsCard, type Knob } from "./knobs";
import { SettingsCard } from "./shared";

const FEATURES: Knob[] = [
  {
    key: "ai_enabled",
    label: "AI features",
    alpha: true,
    help: "Master switch for AI-assisted finding enrichment and fix suggestions. When off, scans complete normally but no AI prompts are issued.",
    type: "bool",
  },
  {
    key: "autofix_enabled",
    label: "Autonomous fixing",
    alpha: true,
    entitlement: REMEDIATION,
    help: "Turns on Fixes and Agents. Queue work from a scan or finding. Sign in to an engine under Fixer engines, or store Anthropic/OpenAI keys under Secrets & credentials. Default off.",
    type: "bool",
  },
];

const SCANNING: Knob[] = [
  {
    key: "remote_scan_dirty_policy",
    label: "Remote dirty worktrees",
    help: "Default fail blocks SSH scans when uncommitted remote changes would be omitted from git archive. Allow records the dirty state but scans committed content only.",
    type: "choice",
    options: ["fail", "allow"],
  },
];

export function GeneralSection() {
  const edition = useEdition();
  const product = edition.data?.product ?? "Wolf Community";
  return (
    <div className="space-y-4">
      <SettingsCard title="Edition" description={`${product} — ${edition.data?.edition ?? "community"}.`}>
        <p className="text-sm text-muted-foreground">
          {edition.data?.licensed
            ? "A commercial license is active."
            : "No commercial license is installed in this binary."}{" "}
          See the License section for entitlements and install.
        </p>
      </SettingsCard>
      <KnobsCard title="Features" knobs={FEATURES} />
      <KnobsCard title="Scanning" knobs={SCANNING} />
    </div>
  );
}
