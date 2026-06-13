// One source-type-tabbed form for creating repositories. Used standalone
// from the Repos page, and threaded through a collection when called from
// the collection detail page (sets collectionId so the new repo is
// auto-linked).
import { useState } from "react";
import { XIcon } from "lucide-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { Link } from "@tanstack/react-router";
import { api } from "@/lib/api";

type Mode = "local" | "github" | "git" | "ssh";

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
  const qc = useQueryClient();

  const create = useMutation({
    mutationFn: async () => {
      const body: Record<string, unknown> = {
        name: name.trim(),
        source_type: mode === "git" ? "git" : mode,
        source_path: sourcePath.trim(),
        default_branch: branch.trim() || "main",
      };
      if (mode === "ssh") body.remote_node_id = remoteNodeId;
      const { data } = await api.post<{ data: { id: string } }>("/repos", body);
      const repoId = data.data.id;
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
    <div className="border border-border/40 rounded-lg p-3 mb-4 space-y-3 bg-muted/10">
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
            <Field
              label="GitHub repo"
              hint='Accepts owner/repo, github.com/owner/repo, or a full https://github.com/... URL.'
            >
              <input
                value={sourcePath}
                onChange={(e) => setSourcePath(e.target.value)}
                required
                placeholder="alphabravo-oss/thewolf"
                className="w-full h-9 px-2 rounded-md bg-background border border-muted/40 text-sm font-mono"
              />
            </Field>
            <div className="text-xs text-muted-foreground">
              Private repository? Add a{" "}
              <Link
                to="/settings"
                search={{ tab: "secrets" }}
                className="underline underline-offset-2"
              >
                github_token secret
              </Link>{" "}
              and it'll be used automatically.
            </div>
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
