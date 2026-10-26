import { CopyIcon } from "lucide-react";
import { toast } from "sonner";

export function RepositoryCISnippet({ repoId }: { repoId: string }) {
  const snippet = `# Requires wolf and jq; set WOLF_SERVER and WOLF_TOKEN in CI secrets.
set -euo pipefail
: "\${WOLF_SERVER:?Set your Wolf server URL}"
: "\${WOLF_TOKEN:?Set your Wolf API token}"
: "\${WOLF_BRANCH:?Set the tracked branch to scan (for example main)}"
# Installs or updates this repository's CI policy.
wolf policy create --name ci-new-and-secrets --scope repo --scope-id '${repoId}' --mode enforce --enabled --rules-json '[{"id":"new-findings","baseline_state":["new","resurfaced"],"suppressed":false,"action":"fail"},{"id":"secrets","category":["secrets"],"suppressed":false,"action":"fail"},{"id":"hardcoded-secrets","fine_category":["hardcoded-secret"],"suppressed":false,"action":"fail"}]'
scan_id=$(wolf scan create --repo '${repoId}' --branch "$WOLF_BRANCH" --profile pr -o json | jq -er '.data.id')
wolf scan wait "$scan_id" --timeout 30m
wolf scan gate "$scan_id" --fail-exit-code`;

  return <>
    <h2 className="text-sm font-medium">CI: new findings and secrets</h2>
    <p className="text-sm text-muted-foreground">Fails on unsuppressed new or resurfaced findings, secrets in any baseline state, and incomplete scanner coverage.</p>
    <p className="text-xs text-muted-foreground">Install Wolf CLI and jq in your runner, then set the server URL, API token, and tracked branch. This script saves an enforced repository policy named ci-new-and-secrets.</p>
    <pre className="max-h-80 overflow-auto rounded-md border border-border bg-muted/30 p-4 text-xs leading-relaxed" tabIndex={0} aria-label="CI snippet"><code>{snippet}</code></pre>
    <button type="button" onClick={async () => { try { await navigator.clipboard.writeText(snippet); toast.success("CI snippet copied"); } catch { toast.error("Could not copy. Select and copy the snippet above."); } }} className="inline-flex h-9 items-center gap-2 rounded-md border border-border px-3 text-sm hover:bg-muted/50"><CopyIcon className="size-4" />Copy CI snippet</button>
  </>;
}
