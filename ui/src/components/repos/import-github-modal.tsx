// Multi-step GitHub import wizard. Picks a github_token secret, asks GitHub
// for every repo in an org (or user account), lets the operator multi-select
// from the result, then fans out POST /repos calls — optionally tagging the
// new repos into a chosen collection. The modal stays on the screen until
// every import finishes so the user can see partial-failure counts.
import { useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { Loader2Icon } from "lucide-react";
import { toast } from "sonner";
import { api } from "@/lib/api";
import type { Collection, Repo } from "@/lib/types";

// Server returns the masked-secret shape (see internal/api/routes/config.go).
// The shared Secret type in lib/types.ts has drifted to expect a
// `masked_value` field, which the API does not actually send — define the
// real wire shape locally so the wizard renders correctly.
interface MaskedSecret {
  id: string;
  key_type: string;
  key_name: string;
  value: string; // already masked
  created_at: string;
}
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";

type Step = "credential" | "org" | "select" | "target";

interface GitHubRepo {
  name: string;
  full_name: string;
  default_branch: string;
  private: boolean;
  archived: boolean;
  language?: string;
}

export function ImportGitHubModal({ onClose }: { onClose: () => void }) {
  const qc = useQueryClient();
  const [step, setStep] = useState<Step>("credential");
  const [secretId, setSecretId] = useState<string>("");
  const [org, setOrg] = useState<string>("");
  const [repos, setRepos] = useState<GitHubRepo[]>([]);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [collectionId, setCollectionId] = useState<string>("");
  const [showArchived, setShowArchived] = useState(false);
  const [showPrivate, setShowPrivate] = useState(true);
  const [showPublic, setShowPublic] = useState(true);

  // Step 1: load secrets and filter to github_token.
  const secretsQ = useQuery({
    queryKey: ["config", "secrets", "all"],
    queryFn: async () => {
      const r = await api.get<MaskedSecret[]>("/config/secrets");
      return r.data ?? [];
    },
  });
  const githubSecrets = useMemo(
    () => (secretsQ.data ?? []).filter((s) => s.key_type === "github_token"),
    [secretsQ.data],
  );

  // Step 2: list org repos.
  const listMut = useMutation({
    mutationFn: async () => {
      const r = await api.post<GitHubRepo[]>("/sources/github/list-org-repos", {
        org: org.trim(),
        secret_id: secretId || undefined,
      });
      return r.data ?? [];
    },
    onSuccess: (data) => {
      setRepos(data);
      setSelected(new Set(data.map((r) => r.full_name)));
      setStep("select");
    },
    onError: (e) => {
      toast.error(e instanceof Error ? e.message : "Failed to list repos");
    },
  });

  // Step 4: collections (for the optional target picker).
  const collectionsQ = useQuery({
    queryKey: ["collections", "all"],
    queryFn: async () => {
      const r = await api.get<Collection[]>("/collections");
      return r.data ?? [];
    },
    enabled: step === "target",
  });

  // Final import: N parallel POST /repos.
  const importMut = useMutation({
    mutationFn: async () => {
      const chosen = repos.filter((r) => selected.has(r.full_name));
      const results = await Promise.allSettled(
        chosen.map(async (r) => {
          const created = await api.post<Repo>("/repos", {
            name: r.name,
            source_type: "github",
            source_path: r.full_name,
            default_branch: r.default_branch || "main",
          });
          if (collectionId && created.data?.id) {
            await api.post(`/collections/${collectionId}/repos`, {
              repo_id: created.data.id,
            });
          }
          return created.data;
        }),
      );
      const ok = results.filter((r) => r.status === "fulfilled").length;
      const failed = results.length - ok;
      return { ok, failed };
    },
    onSuccess: ({ ok, failed }) => {
      qc.invalidateQueries({ queryKey: ["repos"] });
      qc.invalidateQueries({ queryKey: ["collections"] });
      if (failed === 0) {
        toast.success(`Imported ${ok} repo${ok === 1 ? "" : "s"}`);
      } else {
        toast.warning(`Imported ${ok}; ${failed} failed`);
      }
      onClose();
    },
    onError: (e) => {
      toast.error(e instanceof Error ? e.message : "Failed to import repos");
    },
  });

  const visibleRepos = useMemo(() => {
    return repos.filter((r) => {
      if (!showArchived && r.archived) return false;
      if (!showPrivate && r.private) return false;
      if (!showPublic && !r.private) return false;
      return true;
    });
  }, [repos, showArchived, showPrivate, showPublic]);

  const toggleRepo = (fullName: string) => {
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(fullName)) next.delete(fullName);
      else next.add(fullName);
      return next;
    });
  };

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-3xl max-h-[85vh] flex flex-col">
        <DialogHeader>
          <DialogTitle>Import from GitHub</DialogTitle>
          <DialogDescription>
            Pick a credential, list an org's repos, then bulk-import the ones you want to track.
          </DialogDescription>
        </DialogHeader>

        {step === "credential" && (
          <div className="space-y-3 py-2">
            {secretsQ.isLoading ? (
              <div className="text-sm text-muted-foreground">Loading secrets…</div>
            ) : githubSecrets.length === 0 ? (
              <div className="rounded-md border border-border bg-muted/30 p-4 text-sm">
                No <code className="font-mono">github_token</code> secret found.{" "}
                <Link
                  to="/settings"
                  search={{ tab: "secrets" }}
                  className="text-primary underline"
                  onClick={onClose}
                >
                  Add one in Settings → Secrets
                </Link>{" "}
                first.
              </div>
            ) : (
              <div className="space-y-1">
                <Label className="text-xs">Use credential</Label>
                <div className="space-y-1">
                  {githubSecrets.map((s) => {
                    const active = secretId === s.id;
                    return (
                      <button
                        type="button"
                        key={s.id}
                        className={
                          "block w-full rounded-md border px-3 py-2 text-left text-sm transition " +
                          (active
                            ? "border-primary bg-accent"
                            : "border-border hover:bg-muted/40")
                        }
                        onClick={() => setSecretId(s.id)}
                      >
                        <div className="font-medium">{s.key_name}</div>
                        <div className="text-xs text-muted-foreground font-mono">
                          {/* The list endpoint masks the value before sending
                              it over the wire — show it as-is so the user can
                              tell two same-named tokens apart by last 4 chars. */}
                          {s.value || "•••"}
                        </div>
                      </button>
                    );
                  })}
                </div>
              </div>
            )}
          </div>
        )}

        {step === "org" && (
          <div className="space-y-3 py-2">
            <div className="space-y-1">
              <Label htmlFor="gh-org" className="text-xs">
                GitHub org or user
              </Label>
              <Input
                id="gh-org"
                value={org}
                onChange={(e) => setOrg(e.target.value)}
                placeholder="acme"
                autoFocus
              />
              <p className="text-xs text-muted-foreground">
                We try the org endpoint first; if that 404s we fall back to /users.
              </p>
            </div>
            {listMut.isError && (
              <div className="text-xs text-destructive">
                {listMut.error instanceof Error ? listMut.error.message : "Failed"}
              </div>
            )}
          </div>
        )}

        {step === "select" && (
          <div className="flex-1 min-h-0 flex flex-col gap-2 py-1">
            <div className="flex flex-wrap items-center gap-3 text-xs">
              <label className="inline-flex items-center gap-1.5">
                <Checkbox
                  checked={showPublic}
                  onCheckedChange={(v) => setShowPublic(!!v)}
                />
                Public
              </label>
              <label className="inline-flex items-center gap-1.5">
                <Checkbox
                  checked={showPrivate}
                  onCheckedChange={(v) => setShowPrivate(!!v)}
                />
                Private
              </label>
              <label className="inline-flex items-center gap-1.5">
                <Checkbox
                  checked={showArchived}
                  onCheckedChange={(v) => setShowArchived(!!v)}
                />
                Show archived
              </label>
              <span className="ml-auto text-muted-foreground">
                {selected.size} of {visibleRepos.length} selected
              </span>
            </div>
            <div className="flex-1 min-h-0 overflow-y-auto rounded-md border border-border/40">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead className="w-10">
                      <Checkbox
                        aria-label="Select all visible"
                        checked={
                          visibleRepos.length > 0 &&
                          visibleRepos.every((r) => selected.has(r.full_name))
                        }
                        onCheckedChange={(v) => {
                          if (v) {
                            setSelected(
                              new Set([
                                ...selected,
                                ...visibleRepos.map((r) => r.full_name),
                              ]),
                            );
                          } else {
                            const remove = new Set(
                              visibleRepos.map((r) => r.full_name),
                            );
                            setSelected(
                              new Set(
                                [...selected].filter((id) => !remove.has(id)),
                              ),
                            );
                          }
                        }}
                      />
                    </TableHead>
                    <TableHead>Repo</TableHead>
                    <TableHead>Branch</TableHead>
                    <TableHead>Visibility</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {visibleRepos.map((r) => (
                    <TableRow key={r.full_name}>
                      <TableCell>
                        <Checkbox
                          checked={selected.has(r.full_name)}
                          onCheckedChange={() => toggleRepo(r.full_name)}
                          aria-label={`Select ${r.full_name}`}
                        />
                      </TableCell>
                      <TableCell className="font-mono text-xs">
                        {r.full_name}
                        {r.archived && (
                          <span className="ml-2 rounded bg-muted px-1.5 py-0.5 text-[10px] uppercase text-muted-foreground">
                            archived
                          </span>
                        )}
                      </TableCell>
                      <TableCell className="text-xs">
                        {r.default_branch || "main"}
                      </TableCell>
                      <TableCell className="text-xs">
                        {r.private ? "private" : "public"}
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          </div>
        )}

        {step === "target" && (
          <div className="space-y-3 py-2">
            <div className="space-y-1">
              <Label className="text-xs">Add to collection (optional)</Label>
              <div className="max-h-64 overflow-y-auto space-y-1">
                <button
                  type="button"
                  className={
                    "block w-full rounded-md border px-3 py-2 text-left text-sm transition " +
                    (collectionId === ""
                      ? "border-primary bg-accent"
                      : "border-border hover:bg-muted/40")
                  }
                  onClick={() => setCollectionId("")}
                >
                  <div className="font-medium">No collection</div>
                  <div className="text-xs text-muted-foreground">
                    Import without grouping.
                  </div>
                </button>
                {(collectionsQ.data ?? []).map((c) => {
                  const active = collectionId === c.id;
                  return (
                    <button
                      type="button"
                      key={c.id}
                      className={
                        "block w-full rounded-md border px-3 py-2 text-left text-sm transition " +
                        (active
                          ? "border-primary bg-accent"
                          : "border-border hover:bg-muted/40")
                      }
                      onClick={() => setCollectionId(c.id)}
                    >
                      <div className="font-medium">{c.name}</div>
                      {c.description && (
                        <div className="text-xs text-muted-foreground truncate">
                          {c.description}
                        </div>
                      )}
                    </button>
                  );
                })}
              </div>
            </div>
          </div>
        )}

        <DialogFooter>
          {step === "credential" && (
            <>
              <Button variant="ghost" onClick={onClose}>
                Cancel
              </Button>
              <Button
                onClick={() => setStep("org")}
                disabled={!secretId}
              >
                Next
              </Button>
            </>
          )}
          {step === "org" && (
            <>
              <Button variant="ghost" onClick={() => setStep("credential")}>
                Back
              </Button>
              <Button
                onClick={() => listMut.mutate()}
                disabled={!org.trim() || listMut.isPending}
              >
                {listMut.isPending ? (
                  <>
                    <Loader2Icon className="size-3.5 animate-spin" /> Loading…
                  </>
                ) : (
                  "List repos"
                )}
              </Button>
            </>
          )}
          {step === "select" && (
            <>
              <Button variant="ghost" onClick={() => setStep("org")}>
                Back
              </Button>
              <Button
                onClick={() => setStep("target")}
                disabled={selected.size === 0}
              >
                Next ({selected.size})
              </Button>
            </>
          )}
          {step === "target" && (
            <>
              <Button variant="ghost" onClick={() => setStep("select")}>
                Back
              </Button>
              <Button
                onClick={() => importMut.mutate()}
                disabled={importMut.isPending || selected.size === 0}
              >
                {importMut.isPending ? (
                  <>
                    <Loader2Icon className="size-3.5 animate-spin" /> Importing…
                  </>
                ) : (
                  `Import ${selected.size} repo${selected.size === 1 ? "" : "s"}`
                )}
              </Button>
            </>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
