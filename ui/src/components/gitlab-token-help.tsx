import { FixerTokenScopes } from "@/components/github-token-help";

export function GitLabTokenHelp() {
  return (
    <div className="rounded-md border border-border bg-muted/15 px-4 py-3 text-sm text-muted-foreground space-y-2">
      <p className="text-foreground/80 font-medium">What a GitLab token needs for the fixer</p>
      <ul className="list-disc pl-4 space-y-1"><FixerTokenScopes host="gitlab" /></ul>
      <p>Create one under User settings → Access tokens (or the project&apos;s Settings → Access tokens).</p>
    </div>
  );
}
