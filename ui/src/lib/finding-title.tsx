import { useQuery } from "@tanstack/react-query";
import { api } from "@/lib/api";
import type { Finding } from "@/lib/types";

// Scanner titles sometimes contain only a rule identifier. Prefer the report's prose.
type FindingText = {
  title?: string;
  rule_id?: string;
  message?: string;
  description?: string;
  fine_category?: string;
};

export function findingTitle(finding: FindingText): string {
  const title = finding.title?.trim().replace(/^(?:CVE-\d{4}-\d+|GHSA-[\w-]+|CWE-\d+):\s*/i, "");
  const text = [title, finding.message, finding.description].find((value) => value?.trim() && value.trim() !== finding.rule_id && !isRuleTitle(value.trim()));
  if (text) {
    const sentence = text.trim().split(/\n|(?<=[.!?])\s+(?=[A-Z])/)[0].replace(/\s+/g, " ");
    return sentence.length > 180 ? `${sentence.slice(0, 177).replace(/\s+\S*$/, "")}…` : sentence;
  }
  const label = finding.fine_category || (finding.rule_id || title || "").split(".").at(-1);
  if (!label || /^[A-Z]\d{3,}$/.test(label)) return "Security finding";
  const words = label.replace(/[-_]+/g, " ");
  return words.charAt(0).toUpperCase() + words.slice(1);
}

// The rule ID is secondary text; omit it when the title already says it.
export function findingRuleHint(finding: FindingText & { rule_id?: string }): string | undefined {
  const rule = finding.rule_id?.trim();
  return rule && !findingTitle(finding).includes(rule) ? rule : undefined;
}

function isRuleTitle(title: string) {
  return /^(?:[A-Z]\d{3,}|[\w-]+(?:\.[\w-]+){2,})$/.test(title);
}

// Canonical vulnerability/evidence responses omit descriptions. Resolve only
// rule-only titles through their linked finding, reusing the detail query cache.
export function FindingTitle({ finding, findingId }: { finding: FindingText; findingId?: string }) {
  const query = useQuery({
    queryKey: ["finding", findingId],
    queryFn: async () => (await api.get<Finding>(`/findings/${findingId}`)).data,
    enabled: !!findingId && isRuleTitle(finding.title || ""),
    staleTime: 60_000,
  });
  return findingTitle(query.data ?? finding);
}
