const C = ({ children }: { children: string }) => <code className="font-mono">{children}</code>;
const L = ({ children }: { children: string }) => <span className="text-foreground/80">{children}</span>;

/** The token scopes the AI fixer needs, as list items. One source for the Fixer
 *  "Git access" card and the Account → Secrets form so they cannot drift. */
export function FixerTokenScopes({ host }: { host: "github" | "gitlab" }) {
  if (host === "gitlab") {
    return (
      <>
        <li><L>Scopes:</L> <C>api</C> and <C>write_repository</C> on a personal or project access token.</li>
        <li><L>Role:</L> Developer or higher (Maintainer if the target branch is protected from pushes).</li>
        <li><L>Same instance:</L> create the token on the GitLab that hosts the repo, and bind it to that host (self-hosted works for any host).</li>
      </>
    );
  }
  return (
    <>
      <li><L>Fine-grained token:</L> Contents: Read and write <em>and</em> Pull requests: Read and write on the target repos.</li>
      <li><L>Classic token:</L> <C>repo</C>. Add <C>workflow</C> only if the fix touches <C>.github/workflows</C> (fine-grained: Workflows: Read and write).</li>
    </>
  );
}

export function GitHubTokenHelp({ compact = false }: { compact?: boolean }) {
  return (
    <div
      className={
        compact
          ? "rounded-md border border-border bg-muted/15 px-3 py-2 text-xs text-muted-foreground space-y-1.5"
          : "rounded-md border border-border bg-muted/15 px-4 py-3 text-sm text-muted-foreground space-y-2"
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
      </ul>
      <p className="text-foreground/80">To let the AI fixer push a branch and open a PR:</p>
      <ul className="list-disc pl-4 space-y-1"><FixerTokenScopes host="github" /></ul>
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
