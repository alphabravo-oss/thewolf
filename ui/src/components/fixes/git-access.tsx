// What the AI fixer needs from a git host: token scopes per host and the
// deployment knobs for private networks. Scope text is shared with the
// Account → Secrets form (FixerTokenScopes) so the two cannot drift.
import { Link } from "@tanstack/react-router";
import { FixerTokenScopes } from "@/components/github-token-help";

export function GitAccessCard() {
  return (
    <section aria-label="Git access" className="glass-card p-5 space-y-3 text-sm">
      <div>
        <h2 className="text-sm font-medium">Git access</h2>
        <p className="mt-1 text-xs text-muted-foreground">
          To push a fix branch and open a pull or merge request, Wolf needs a token for the
          repo&apos;s host. Add it under{" "}
          <Link to="/account" search={{ section: "secrets" }} className="underline">Account → Secrets</Link>.
        </p>
      </div>
      <div className="grid gap-3 lg:grid-cols-2 text-xs text-muted-foreground">
        <div className="rounded-lg border border-border p-3 space-y-1.5">
          <h3 className="text-sm font-medium text-foreground">GitHub</h3>
          <ul className="list-disc pl-4 space-y-1"><FixerTokenScopes host="github" /></ul>
        </div>
        <div className="rounded-lg border border-border p-3 space-y-1.5">
          <h3 className="text-sm font-medium text-foreground">GitLab (gitlab.com or self-hosted)</h3>
          <ul className="list-disc pl-4 space-y-1"><FixerTokenScopes host="gitlab" /></ul>
        </div>
      </div>
      <ul className="list-disc pl-4 space-y-1 text-xs text-muted-foreground">
        <li>
          Private-network hosts need <code className="font-mono">WOLF_GIT_ALLOWED_CIDRS</code>; a private CA needs{" "}
          <code className="font-mono">WOLF_GIT_CA_BUNDLE</code> (path to a PEM bundle mounted into the app and fixer containers).
        </li>
        <li>SSH remotes are not supported for fixing yet. Use an https URL.</li>
        <li>Git hosts other than GitHub and GitLab are not supported for fixing.</li>
      </ul>
    </section>
  );
}
