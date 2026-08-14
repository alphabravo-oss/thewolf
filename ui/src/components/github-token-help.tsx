export function GitHubTokenHelp({ compact = false }: { compact?: boolean }) {
  return (
    <div
      className={
        compact
          ? "rounded-md border border-border/40 bg-muted/15 px-3 py-2 text-xs text-muted-foreground space-y-1.5"
          : "rounded-md border border-border/40 bg-muted/15 px-4 py-3 text-sm text-muted-foreground space-y-2"
      }
    >
      <p className="text-foreground/80 font-medium">
        What this token needs depends on what you want Wolf to do
      </p>
      <ul className="list-disc pl-4 space-y-1">
        <li>
          <span className="text-foreground/80">Scan public repos:</span> a
          classic PAT with <code className="font-mono">public_repo</code>, or a
          fine-grained token with Contents: Read on those repos.
        </li>
        <li>
          <span className="text-foreground/80">Scan private repos:</span>{" "}
          classic <code className="font-mono">repo</code>, or fine-grained
          Contents: Read.
        </li>
        <li>
          <span className="text-foreground/80">Push a fix branch:</span>{" "}
          classic <code className="font-mono">repo</code> (includes write), or
          fine-grained Contents: Read and write. Wolf never pushes to the
          default branch.
        </li>
        <li>
          <span className="text-foreground/80">
            Push when the branch changes GitHub Actions:
          </span>{" "}
          also classic <code className="font-mono">workflow</code>, or
          fine-grained Workflows: Read and write. Without this, GitHub rejects
          the push even though the agent already kept the fixes locally.
        </li>
      </ul>
      <p>
        Create a token at{" "}
        <a
          href="https://github.com/settings/tokens"
          target="_blank"
          rel="noreferrer"
          className="underline text-foreground/80"
        >
          github.com/settings/tokens
        </a>
        . Save it here, then retry any waiting push.
      </p>
    </div>
  );
}
