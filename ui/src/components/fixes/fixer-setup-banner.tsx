import { Link } from "@tanstack/react-router";
import { KeyIcon, TerminalIcon, WrenchIcon } from "lucide-react";
import { useFixEngines } from "@/lib/fixes";
import { useMe } from "@/lib/me";

export function FixerSetupBanner() {
  const me = useMe();
  const engines = useFixEngines(true);
  if (engines.isLoading) return null;

  const worker = engines.data?.worker ?? [];
  const keys = engines.data?.api_keys;
  const workerLive = worker.length > 0;
  const signedIn = worker.some(
    (row) => row.auth === "oauth" || row.auth === "api_key",
  );
  const hasApiKey = Boolean(keys?.anthropic || keys?.openai || keys?.xai);
  const ready = (workerLive && signedIn) || hasApiKey;
  if (ready) return null;

  const settingsFixer = me.data?.role === "admin";

  return (
    <section className="glass-card p-5">
      <div className="flex flex-col gap-1 sm:flex-row sm:items-start sm:justify-between">
        <div>
          <h2 className="text-sm font-semibold">Finish fixer setup</h2>
          <p className="mt-1 text-sm text-muted-foreground max-w-3xl">
            Autonomous fixing is on. Wolf still needs a model provider before
            it can open a branch.
          </p>
        </div>
      </div>
      <ol className="mt-4 grid gap-3 md:grid-cols-3">
        <li className="flex items-start gap-3 rounded-md border border-border/60 bg-background/40 p-3">
          <TerminalIcon className="size-4 mt-0.5 text-muted-foreground shrink-0" />
          <span className="text-sm">
            {settingsFixer ? (
              <>
                <Link
                  to="/settings"
                  search={{ tab: "fixer" }}
                  className="font-medium text-foreground hover:underline"
                >
                  Settings → Fixer
                </Link>
                <span className="block mt-1 text-muted-foreground">
                  Start the login console on the fixer worker.
                </span>
              </>
            ) : (
              <>Ask an admin to open Settings → Fixer and run the login console.</>
            )}
          </span>
        </li>
        <li className="flex items-start gap-3 rounded-md border border-border/60 bg-background/40 p-3">
          <KeyIcon className="size-4 mt-0.5 text-muted-foreground shrink-0" />
          <span className="text-sm">
            <Link
              to="/account"
              search={{ section: "secrets" }}
              className="font-medium text-foreground hover:underline"
            >
              Account → Secrets
            </Link>
            <span className="block mt-1 text-muted-foreground">
              Or store an Anthropic, OpenAI, or xAI/Grok key.
            </span>
          </span>
        </li>
        <li className="flex items-start gap-3 rounded-md border border-border/60 bg-background/40 p-3">
          <WrenchIcon className="size-4 mt-0.5 text-muted-foreground shrink-0" />
          <span className="text-sm">
            <span className="font-medium">Queue a Fix</span>
            <span className="block mt-1 text-muted-foreground">
              Open a scan or finding and click Fix. Agents appear after the
              first job is queued.
            </span>
          </span>
        </li>
      </ol>
      {!workerLive && (
        <p className="mt-3 text-xs text-muted-foreground">
          No fixer worker is reporting in. On this host:{" "}
          <code className="font-mono">
            docker compose --profile fixer up -d fixer
          </code>
        </p>
      )}
    </section>
  );
}
