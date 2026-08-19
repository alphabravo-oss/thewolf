// One source-type-tabbed form for creating repositories. Used standalone
// from the Repos page, and threaded through a collection when called from
// the collection detail page (sets collectionId so the new repo is
// auto-linked).
import { useEffect, useState } from "react";
import { Loader2Icon, XIcon } from "lucide-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { Link } from "@tanstack/react-router";
import { api } from "@/lib/api";
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
      toast.success("Repository created");
      onDone(repoId);
    },
    onError: (e) => {
      const msg = e instanceof Error ? e.message : "Create failed";
      toast.error(msg);
    },
  });

  const submitDisabled =
    !name.trim() ||
    !sourcePath.trim() ||
    (mode === "github" && !isLikelyGitHubSource(sourcePath)) ||
    (mode === "ssh" && !remoteNodeId) ||
    create.isPending;

  return (
    <div className="border border-border rounded-lg p-3 mb-4 space-y-3 bg-muted/10">
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

      <div className="flex gap-1 text-xs">
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

      <form
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
            hint="Must be under a path Docker can bind-mount (typically anywhere under /Users on macOS)."
          >
            <input
              value={sourcePath}
              onChange={(e) => setSourcePath(e.target.value)}
              required
              placeholder="/Users/me/code/my-project"
              className="w-full h-9 px-2 rounded-md bg-background border border-muted/40 text-sm font-mono"
            />
          </Field>
        )}

        {mode === "github" && (
          <>
            <Field label="Use credential">
              <select
                value={githubSecretId}
                onChange={(e) => {
                  setGithubSecretId(e.target.value);
                  setSourcePath("");
                }}
                className="w-full h-9 px-2 rounded-md bg-background border border-muted/40 text-sm"
                disabled={secretsQ.isLoading}
              >
                <option value="">
                  {secretsQ.isLoading
                    ? "Loading GitHub tokens..."
                    : "No credential (type a public repo)"}
                </option>
                {githubTokens.map((secret) => (
                  <option key={secret.id} value={secret.id}>
                    {secret.key_name}
                    {secret.value ? ` (${secret.value})` : ""}
                  </option>
                ))}
              </select>
            </Field>
            {githubTokens.length === 0 && (
              <div className="text-xs text-muted-foreground">
                Add a{" "}
                <Link
                  to="/settings"
                  search={{ tab: "secrets" }}
                  className="underline underline-offset-2"
                >
                  github_token
                </Link>{" "}
                with repo read access to pick from a dropdown.
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
                  <select
                    value={sourcePath}
                    onChange={(e) => {
                      const full = e.target.value;
                      setSourcePath(full);
                      const picked = (githubReposQ.data ?? []).find(
                        (repo) => repo.full_name === full,
                      );
                      if (picked) {
                        if (!name.trim()) setName(picked.name);
                        if (picked.default_branch) setBranch(picked.default_branch);
                      }
                    }}
                    required
                    className="w-full h-9 px-2 rounded-md bg-background border border-muted/40 text-sm font-mono"
                  >
                    <option value="">Select a project…</option>
                    {(githubReposQ.data ?? []).map((repo) => (
                      <option key={repo.full_name} value={repo.full_name}>
                        {repo.full_name}
                        {repo.private ? " (private)" : ""}
                      </option>
                    ))}
                  </select>
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
          <Field label="Git URL">
            <input
              value={sourcePath}
              onChange={(e) => setSourcePath(e.target.value)}
              required
              placeholder="https://gitlab.example.com/team/repo.git"
              className="w-full h-9 px-2 rounded-md bg-background border border-muted/40 text-sm font-mono"
            />
          </Field>
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

        <div className="flex justify-end">
          <button
            type="submit"
            disabled={submitDisabled}
            className="h-8 px-3 rounded-md bg-primary text-primary-foreground text-sm font-medium hover:opacity-90 disabled:opacity-50"
          >
            {create.isPending ? "Creating…" : collectionId ? "Create + add" : "Create"}
          </button>
        </div>
      </form>
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
      const { data } = await api.get<{ data: Array<{ id: string; name: string; host: string }> }>(
        "/nodes",
      );
      return data.data ?? [];
    },
  });
  if (nodes.isLoading) {
    return (
      <select
        disabled
        className="w-full h-9 px-2 rounded-md bg-background border border-muted/40 text-sm opacity-60"
      >
        <option>Loading nodes…</option>
      </select>
    );
  }
  if ((nodes.data ?? []).length === 0) {
    return (
      <div className="text-xs text-muted-foreground">
        No SSH nodes configured.{" "}
        <Link to="/settings" search={{ tab: "nodes" }} className="underline underline-offset-2">
          Add one in Settings → Nodes
        </Link>
        .
      </div>
    );
  }
  return (
    <select
      value={value}
      onChange={(e) => onChange(e.target.value)}
      required
      className="w-full h-9 px-2 rounded-md bg-background border border-muted/40 text-sm"
    >
      <option value="">Pick a node…</option>
      {nodes.data!.map((n) => (
        <option key={n.id} value={n.id}>
          {n.name} ({n.host})
        </option>
      ))}
    </select>
  );
}
