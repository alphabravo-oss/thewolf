// One source-type-tabbed form for creating repositories. Used standalone
// from the Repos page, and threaded through a collection when called from
// the collection detail page (sets collectionId so the new repo is
// auto-linked).
import { useEffect, useState } from "react";
import { Loader2Icon, XIcon } from "lucide-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { Link } from "@tanstack/react-router";
import { useScanWithPreflight } from "@/components/scan-preflight";
import { BrowsePathModal } from "@/components/browse-path-modal";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Checkbox } from "@/components/ui/checkbox";
import { Button } from "@/components/ui/button";
import { api } from "@/lib/api";
import { isCommunityLimit, COMMUNITY_LIMIT_COPY } from "@/lib/safe-display";
import type { Repo } from "@/lib/types";

type Mode = "local" | "github" | "git" | "ssh";

interface MaskedSecret {
  id: string;
  key_type: string;
  key_name: string;
  value: string;
}

interface GitHubRepoOption {
  name: string;
  full_name: string;
  default_branch: string;
  private: boolean;
  archived?: boolean;
}

interface SourceCredential {
  id: string;
  type: string;
  name: string;
  allowed_hosts: string[];
}

// Hostname of a remote git URL, or "" while the user is still typing one.
function gitUrlHost(s: string): string {
  try {
    return new URL(s.trim()).hostname.toLowerCase();
  } catch {
    return "";
  }
}

const hostAllowed = (allowed: string[], host: string) =>
  allowed.some((h) => h === host || (h.startsWith("*.") && host.endsWith(h.slice(1))));

export type AddRepoFormProps = {
  /** When set, the form attaches the new repo to this collection on success. */
  collectionId?: string;
  /** Called after a successful create (or on Close). Parents typically navigate or refetch. */
  onDone: (repoId: string | null) => void;
};

// Mirrors internal/scantarget/github.go ParseGitHubSource. Kept lenient: the
// server is the source of truth, but failing client-side avoids a round trip
// for obvious typos.
function isLikelyGitHubSource(s: string): boolean {
  const v = s.trim()
    .replace(/^https?:\/\/github\.com\//, "")
    .replace(/^github\.com\//, "")
    .replace(/^git@github\.com:/, "")
    .replace(/\.git$/, "")
    .replace(/^\/+|\/+$/g, "");
  const parts = v.split("/");
  return parts.length === 2 && parts.every((p) => p.length > 0 && !/[\s@:]/.test(p));
}

export function AddRepoForm({ collectionId, onDone }: AddRepoFormProps) {
  const [mode, setMode] = useState<Mode>("local");
  const [name, setName] = useState("");
  const [sourcePath, setSourcePath] = useState("");
  const [branch, setBranch] = useState("main");
  const [remoteNodeId, setRemoteNodeId] = useState("");
  const [githubSecretId, setGithubSecretId] = useState("");
  const [gitCredentialId, setGitCredentialId] = useState("");
  const [scanNow, setScanNow] = useState(true);
  const [browse, setBrowse] = useState(false);
  const [createdRepoId, setCreatedRepoId] = useState<string | null>(null);
  const scan = useScanWithPreflight();
  const qc = useQueryClient();
  const secretsQ = useQuery({
    queryKey: ["config", "secrets", "github-token"],
    queryFn: async () => {
      const r = await api.get<MaskedSecret[]>("/config/secrets");
      return (r.data ?? []).filter((s) => s.key_type === "github_token");
    },
    enabled: mode === "github",
  });
  const githubTokens = secretsQ.data ?? [];
  useEffect(() => {
    if (mode !== "github") return;
    if (!githubSecretId && githubTokens.length === 1) {
      setGithubSecretId(githubTokens[0].id);
    }
  }, [mode, githubSecretId, githubTokens]);

  const credentialsQ = useQuery({
    queryKey: ["credentials"],
    queryFn: async () => (await api.get<SourceCredential[]>("/credentials")).data ?? [],
    enabled: mode === "git",
  });
  const gitHost = gitUrlHost(sourcePath);
  const gitCredentials = gitHost
    ? (credentialsQ.data ?? []).filter((c) => (c.type === "gitlab_token" || c.type === "git_https") && hostAllowed(c.allowed_hosts ?? [], gitHost))
    : [];
  // Only send a credential that still matches the URL's host.
  const gitCredentialValid = gitCredentials.some((c) => c.id === gitCredentialId) ? gitCredentialId : "";

  const githubReposQ = useQuery({
    queryKey: ["github-repos", githubSecretId],
    enabled: mode === "github" && !!githubSecretId,
    queryFn: async () => {
      const r = await api.post<GitHubRepoOption[]>(
        "/sources/github/list-org-repos",
        { secret_id: githubSecretId },
      );
      return (r.data ?? []).filter((repo) => !repo.archived);
    },
  });

  const create = useMutation({
    mutationFn: async () => {
      const body: Record<string, unknown> = {
        name: name.trim(),
        source_type: mode === "git" ? "git" : mode,
        source_path: sourcePath.trim(),
        default_branch: branch.trim() || "main",
      };
      if (mode === "ssh") body.remote_node_id = remoteNodeId;
      if (mode === "github" && githubSecretId) {
        body.credential_secret_id = githubSecretId;
      }
      if (mode === "git" && gitCredentialValid) body.credential_secret_id = gitCredentialValid;
      const { data } = await api.post<Repo>("/repos", body);
      const repoId = data.id;
      if (collectionId) {
        await api.post(`/collections/${collectionId}/repos`, { repo_id: repoId });
      }
      return repoId;
    },
    onSuccess: (repoId) => {
      qc.invalidateQueries({ queryKey: ["repos"] });
      if (collectionId) {
        qc.invalidateQueries({ queryKey: ["collection", collectionId] });
      }
      qc.invalidateQueries({ queryKey: ["scans"] });
      toast.success("Repository created");
      setCreatedRepoId(repoId);
      if (scanNow) scan.configure({ repo_id: repoId, profile: "standard" });
      else onDone(repoId);
    },
    onError: (e) => {
      toast.error(isCommunityLimit(e) ? COMMUNITY_LIMIT_COPY : e instanceof Error ? e.message : "Create failed");
    },
  });

  const submitDisabled =
    !name.trim() ||
    !sourcePath.trim() ||
    (mode === "github" && !isLikelyGitHubSource(sourcePath)) ||
    (mode === "ssh" && !remoteNodeId) ||
    create.isPending || scan.busy;

  return (
    <div className="border border-border rounded-lg p-3 mb-4 space-y-3 bg-muted/10">
      {scan.dialog}
      {browse && <BrowsePathModal initialPath={sourcePath} onClose={() => setBrowse(false)} onSelect={(path) => { setSourcePath(path); if (!name.trim()) setName(path.split("/").filter(Boolean).at(-1) || "repository"); setBrowse(false); }} />}
      <div className="flex items-center justify-between">
        <div className="text-xs text-muted-foreground">
          {collectionId ? "Add a repo to this collection" : "Add a repository"}
        </div>
        <button
          type="button"
          onClick={() => onDone(null)}
          className="size-7 grid place-items-center rounded hover:bg-muted/40"
          aria-label="Close"
        >
          <XIcon className="size-4" />
        </button>
      </div>

      <div className="flex flex-wrap gap-1 text-xs">
        {(
          [
            ["local", "Local path"],
            ["github", "GitHub"],
            ["git", "Remote git URL"],
            ["ssh", "SSH node"],
          ] as const
        ).map(([m, label]) => (
          <button
            key={m}
            type="button"
            onClick={() => setMode(m)}
            className={`h-7 px-2.5 rounded-md transition ${
              mode === m
                ? "bg-primary text-primary-foreground"
                : "hover:bg-muted/40 text-muted-foreground"
            }`}
            aria-pressed={mode === m}
          >
            {label}
          </button>
        ))}
      </div>

      {createdRepoId ? <div className="space-y-3 text-sm"><p>Repository added. Start its first scan when you are ready.</p><div className="flex flex-wrap gap-2"><Button disabled={scan.busy} onClick={() => scan.configure({ repo_id: createdRepoId, profile: "standard" })}>Scan repository</Button><Button variant="outline" onClick={() => onDone(createdRepoId)}>View repository</Button></div></div> : <form
        onSubmit={(e) => {
          e.preventDefault();
          if (!submitDisabled) create.mutate();
        }}
        className="space-y-3"
      >
        <Field label="Name">
          <input
            value={name}
            onChange={(e) => setName(e.target.value)}
            required
            placeholder={mode === "github" ? "owner/repo" : "my-project"}
            className="w-full h-9 px-2 rounded-md bg-background border border-muted/40 text-sm"
          />
        </Field>

        {mode === "local" && (
          <Field
            label="Absolute path on host"
            hint="A folder on the Wolf server, under an allowed repository root. Docker must be able to read it."
          >
            <div className="flex gap-2"><input
              value={sourcePath}
              onChange={(e) => setSourcePath(e.target.value)}
              required
              placeholder="/srv/repos/my-project"
              className="min-w-0 w-full h-9 px-2 rounded-md bg-background border border-muted/40 text-sm font-mono"
            /><Button variant="outline" type="button" size="sm" onClick={() => setBrowse(true)}>Browse</Button></div>
          </Field>
        )}

        {mode === "github" && (
          <>
            <Field label="Use credential">
              <Select value={githubSecretId || "__public__"} onValueChange={(value) => { setGithubSecretId(value === "__public__" ? "" : value); setSourcePath(""); }} disabled={secretsQ.isLoading}>
                <SelectTrigger aria-label="Use credential"><SelectValue placeholder="Choose credential" /></SelectTrigger>
                <SelectContent><SelectItem value="__public__">{secretsQ.isLoading ? "Loading credentials…" : "Public repository · no credential"}</SelectItem>{githubTokens.map((secret) => <SelectItem key={secret.id} value={secret.id}>{secret.key_name}</SelectItem>)}</SelectContent>
              </Select>
              {secretsQ.isError && <p role="alert" className="text-xs text-destructive">Credentials could not be loaded. <button type="button" className="underline" onClick={() => void secretsQ.refetch()}>Retry credentials</button></p>}
            </Field>
            {githubTokens.length === 0 && (
              <div className="text-xs text-muted-foreground">
                Add a{" "}
                <Link
                  to="/account"
                  search={{ section: "secrets" }}
                  className="underline underline-offset-2"
                >
                  GitHub credential
                </Link>{" "}
                with repository read access to browse private projects.
              </div>
            )}
            {githubSecretId ? (
              <Field
                label="GitHub project"
                hint="Repos this token can read. Pick one instead of typing owner/repo."
              >
                {githubReposQ.isLoading ? (
                  <div className="inline-flex items-center gap-2 h-9 text-xs text-muted-foreground">
                    <Loader2Icon className="size-3.5 animate-spin" />
                    Loading projects…
                  </div>
                ) : githubReposQ.isError ? (
                  <div className="text-xs text-destructive">
                    {githubReposQ.error instanceof Error
                      ? githubReposQ.error.message
                      : "Could not list repos for this token"}
                  </div>
                ) : (
                  <Select value={sourcePath} onValueChange={(full) => {
                    setSourcePath(full);
                    const picked = githubReposQ.data?.find((repo) => repo.full_name === full);
                    if (picked) { if (!name.trim()) setName(picked.name); if (picked.default_branch) setBranch(picked.default_branch); }
                  }} required>
                    <SelectTrigger aria-label="GitHub project"><SelectValue placeholder="Select a project…" /></SelectTrigger>
                    <SelectContent>{(githubReposQ.data ?? []).map((repo) => <SelectItem key={repo.full_name} value={repo.full_name}>{repo.full_name}{repo.private ? " (private)" : ""}</SelectItem>)}</SelectContent>
                  </Select>
                )}
              </Field>
            ) : (
              <Field
                label="GitHub repo"
                hint="Accepts owner/repo, github.com/owner/repo, or a full https://github.com/... URL."
              >
                <input
                  value={sourcePath}
                  onChange={(e) => setSourcePath(e.target.value)}
                  required
                  placeholder="alphabravo-oss/thewolf"
                  className="w-full h-9 px-2 rounded-md bg-background border border-muted/40 text-sm font-mono"
                />
              </Field>
            )}
          </>
        )}

        {mode === "git" && (
          <>
            <Field label="Git URL">
              <input
                value={sourcePath}
                onChange={(e) => setSourcePath(e.target.value)}
                required
                placeholder="https://gitlab.example.com/team/repo.git"
                className="w-full h-9 px-2 rounded-md bg-background border border-muted/40 text-sm font-mono"
              />
            </Field>
            {gitCredentials.length > 0 ? (
              <Field label="Use credential" hint="Needed to scan private repos and for the AI fixer to push a fix branch.">
                <Select value={gitCredentialValid || "__none__"} onValueChange={(value) => setGitCredentialId(value === "__none__" ? "" : value)}>
                  <SelectTrigger aria-label="Use credential"><SelectValue placeholder="Choose credential" /></SelectTrigger>
                  <SelectContent><SelectItem value="__none__">No credential</SelectItem>{gitCredentials.map((c) => <SelectItem key={c.id} value={c.id}>{c.name}</SelectItem>)}</SelectContent>
                </Select>
              </Field>
            ) : gitHost && !credentialsQ.isLoading ? (
              <div className="text-xs text-muted-foreground">
                No credential is bound to <span className="font-mono">{gitHost}</span>. For private repos or AI fixes,{" "}
                <Link to="/account" search={{ section: "secrets" }} className="underline underline-offset-2">
                  add a GitLab token
                </Link>{" "}
                with that host.
              </div>
            ) : null}
            {credentialsQ.isError && <p role="alert" className="text-xs text-destructive">Credentials could not be loaded. <button type="button" className="underline" onClick={() => void credentialsQ.refetch()}>Retry credentials</button></p>}
          </>
        )}

        {mode === "ssh" && (
          <>
            <Field label="SSH node">
              <NodePicker value={remoteNodeId} onChange={setRemoteNodeId} />
            </Field>
            <Field label="Absolute remote path">
              <input
                value={sourcePath}
                onChange={(e) => setSourcePath(e.target.value)}
                required
                placeholder="/srv/code/my-project"
                className="w-full h-9 px-2 rounded-md bg-background border border-muted/40 text-sm font-mono"
              />
            </Field>
          </>
        )}

        <Field label="Default branch">
          <input
            value={branch}
            onChange={(e) => setBranch(e.target.value)}
            placeholder="main"
            className="w-full h-9 px-2 rounded-md bg-background border border-muted/40 text-sm"
          />
        </Field>

        <div className="flex items-center justify-between gap-3">
          <label className="flex items-center gap-2 text-sm">
            <Checkbox checked={scanNow} onCheckedChange={(checked) => setScanNow(checked === true)} />
            Scan now
          </label>
          <button
            type="submit"
            disabled={submitDisabled}
            className="h-8 px-3 rounded-md bg-primary text-primary-foreground text-sm font-medium hover:opacity-90 disabled:opacity-50"
          >
            {create.isPending ? "Creating…" : collectionId ? "Create + add" : "Create"}
          </button>
        </div>
      </form>}
    </div>
  );
}

function Field({
  label,
  hint,
  children,
}: {
  label: string;
  hint?: string;
  children: React.ReactNode;
}) {
  return (
    <label className="block space-y-1">
      <div className="text-xs text-muted-foreground">{label}</div>
      {children}
      {hint && <div className="text-xs text-muted-foreground/80">{hint}</div>}
    </label>
  );
}

function NodePicker({
  value,
  onChange,
}: {
  value: string;
  onChange: (id: string) => void;
}) {
  // Fetched lazily so we don't pay for it when the user is on a non-SSH tab.
  const nodes = useQuery({
    queryKey: ["nodes"],
    queryFn: async () => {
      const { data } = await api.get<Array<{ id: string; name: string; host: string }>>(
        "/nodes",
      );
      return data ?? [];
    },
  });
  if (nodes.isLoading) return <p className="text-xs text-muted-foreground" role="status">Loading nodes…</p>;
  if (nodes.isError) return <p role="alert" className="text-xs text-destructive">SSH nodes could not be loaded. <button type="button" onClick={() => void nodes.refetch()} className="underline">Retry nodes</button></p>;
  if ((nodes.data ?? []).length === 0) {
    return (
      <div className="text-xs text-muted-foreground">
        No SSH nodes configured.{" "}
        <Link to="/account" search={{ section: "nodes" }} className="underline underline-offset-2">
          Add one in Account → Nodes
        </Link>
        .
      </div>
    );
  }
  return (
    <Select value={value} onValueChange={onChange} required>
      <SelectTrigger aria-label="SSH node"><SelectValue placeholder="Pick a node…" /></SelectTrigger>
      <SelectContent>{nodes.data!.map((n) => <SelectItem key={n.id} value={n.id}>{n.name} ({n.host})</SelectItem>)}</SelectContent>
    </Select>
  );
}
