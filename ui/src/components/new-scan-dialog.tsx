// New-scan dialog: pick a repo, optionally narrow the tool list, override
// branch, then start via useScanWithPreflight (same path as Scan this).
import { Link } from "@tanstack/react-router";
import { useForm, useStore } from "@tanstack/react-form";
import { useQuery } from "@tanstack/react-query";
import { api } from "@/lib/api";
import { useScanWithPreflight } from "@/components/scan-preflight";
import { BranchSelect } from "@/components/branch-select";
import { Button } from "@/components/ui/button";
import { FormDialog } from "@/components/ui/form-dialog";
import type { Repo } from "@/lib/types";

type ScannerSummary = { name: string; category: string; languages: string[] };

const selectClass = "mt-1 h-9 w-full rounded-md border border-input bg-background px-2 text-sm";

// Mounted only while open so every opening starts from a blank form.
export function NewScanDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (open: boolean) => void }) {
  return open ? <NewScanDialogBody onOpenChange={onOpenChange} /> : null;
}

function NewScanDialogBody({ onOpenChange }: { onOpenChange: (open: boolean) => void }) {
  const scan = useScanWithPreflight();
  const repos = useQuery({
    queryKey: ["repos", "for-scan"],
    queryFn: async () => {
      const r = await api.get<Repo[]>("/repos");
      return r.data ?? [];
    },
  });
  const scanners = useQuery({
    queryKey: ["scanners", "list"],
    queryFn: async () => {
      const r = await api.get<ScannerSummary[]>("/scanners/list");
      return r.data ?? [];
    },
  });
  const form = useForm({
    defaultValues: { repoId: "", branch: "", toolMode: "auto" as "auto" | "explicit", picked: new Set<string>() },
    validators: { onSubmit: ({ value }) => !value.repoId ? "Choose a repository." :
      value.toolMode === "explicit" && value.picked.size === 0 ? "Choose at least one scanner." : undefined },
    onSubmit: ({ value }) => {
      const repo = repos.data?.find((r) => r.id === value.repoId);
      scan.launch({ repo_id: value.repoId, branch: value.branch.trim() || repo?.default_branch || "main",
        ...(value.toolMode === "explicit" ? { tools: Array.from(value.picked).sort() } : {}) });
    },
  });
  const { repoId, branch, toolMode, picked } = useStore(form.store, (state) => state.values);
  const errors = useStore(form.store, (state) => state.errors);
  const setRepoId = (value: string) => form.setFieldValue("repoId", value);
  const setBranch = (value: string) => form.setFieldValue("branch", value);
  const setToolMode = (value: "auto" | "explicit") => form.setFieldValue("toolMode", value);
  const setPicked = (value: Set<string> | ((prev: Set<string>) => Set<string>)) => form.setFieldValue("picked", value);

  const selectedRepo = repos.data?.find((r) => r.id === repoId);

  // Group scanners by category for a tidier checkbox list.
  const grouped = (() => {
    const map = new Map<string, ScannerSummary[]>();
    for (const s of scanners.data ?? []) {
      const key = s.category || "other";
      if (!map.has(key)) map.set(key, []);
      map.get(key)!.push(s);
    }
    return Array.from(map.entries()).sort(([a], [b]) => a.localeCompare(b));
  })();

  const togglePick = (name: string) => {
    setPicked((prev) => {
      const next = new Set(prev);
      if (next.has(name)) next.delete(name);
      else next.add(name);
      return next;
    });
  };
  const pickAll = () => setPicked(new Set((scanners.data ?? []).map((s) => s.name)));
  const pickNone = () => setPicked(new Set());

  return (
    <>
      <FormDialog
        open
        onOpenChange={(next) => { if (!next) onOpenChange(false); }}
        title="New scan"
        description="Choose a repository and, optionally, the tools to run."
        onSubmit={() => void form.handleSubmit()}
        submitLabel={scan.busy ? "Starting…" : toolMode === "explicit" ? `Start scan (${picked.size})` : "Start scan"}
        submitDisabled={!repoId || scan.busy || (toolMode === "explicit" && picked.size === 0)}
        dirty={!!repoId}
        error={errors.length > 0 ? errors.join(" ") : undefined}
        presentation="sheet"
      >
        {repos.isError && <p role="alert" className="text-sm text-destructive-text">Could not load repositories. <Button type="button" variant="link" className="h-auto p-0" onClick={() => void repos.refetch()}>Retry</Button></p>}
        <label className="block">
          <span className="text-xs text-muted-foreground">Repo *</span>
          {repos.isLoading ? (
            <div className="mt-1 text-xs text-muted-foreground">Loading…</div>
          ) : repos.isError ? null : !repos.data || repos.data.length === 0 ? (
            <div className="mt-1 text-xs text-muted-foreground">
              No repos. Add one from a{" "}
              <Link to="/collections" className="underline">
                collection
              </Link>{" "}
              first.
            </div>
          ) : (
            <select
              name="repo_id"
              value={repoId}
              onChange={(e) => {
                setRepoId(e.target.value);
                const r = repos.data?.find((x) => x.id === e.target.value);
                if (r) setBranch("");
              }}
              required
              className={selectClass}
            >
              <option value="">Pick a repo…</option>
              {repos.data.map((r) => (
                <option key={r.id} value={r.id}>
                  {r.name} — {r.source_path}
                </option>
              ))}
            </select>
          )}
        </label>

        {selectedRepo && (
          <label className="block">
            <span className="text-xs text-muted-foreground">Branch</span>
            <BranchSelect
              repoId={selectedRepo.id}
              value={branch}
              onChange={setBranch}
              defaultBranch={selectedRepo.default_branch}
              className={selectClass}
            />
          </label>
        )}

        {selectedRepo && (
          <div className="space-y-2">
            <div className="text-xs text-muted-foreground">Tools to run</div>
            <div className="flex gap-1.5">
              <Button type="button" size="sm" variant={toolMode === "auto" ? "default" : "outline"} aria-pressed={toolMode === "auto"} onClick={() => setToolMode("auto")}>
                Auto (by detected language)
              </Button>
              <Button type="button" size="sm" variant={toolMode === "explicit" ? "default" : "outline"} aria-pressed={toolMode === "explicit"} onClick={() => setToolMode("explicit")}>
                Pick tools
              </Button>
            </div>

            {toolMode === "explicit" && (
              <div className="space-y-2 rounded-md border border-border bg-muted/30 p-3">
                {scanners.isLoading ? (
                  <div className="text-xs text-muted-foreground">Loading scanners…</div>
                ) : (
                  <>
                    <div className="flex items-center justify-between text-xs">
                      <span className="text-muted-foreground">
                        {picked.size} of {scanners.data?.length ?? 0} selected
                      </span>
                      <div className="flex gap-2">
                        <button type="button" onClick={pickAll} className="text-muted-foreground underline hover:text-foreground">All</button>
                        <button type="button" onClick={pickNone} className="text-muted-foreground underline hover:text-foreground">None</button>
                      </div>
                    </div>
                    <div className="max-h-64 space-y-2 overflow-y-auto">
                      {grouped.map(([cat, items]) => (
                        <div key={cat}>
                          <div className="eyebrow">{cat}</div>
                          <div className="grid grid-cols-1 gap-1 text-xs sm:grid-cols-3">
                            {items.map((s) => (
                              <label key={s.name} className="flex cursor-pointer items-center gap-1.5 rounded px-1.5 py-1 hover:bg-muted">
                                <input
                                  type="checkbox"
                                  name="tools"
                                  value={s.name}
                                  checked={picked.has(s.name)}
                                  onChange={() => togglePick(s.name)}
                                  className="accent-primary"
                                />
                                <span className="truncate" title={s.languages.join(", ") || "any"}>{s.name}</span>
                              </label>
                            ))}
                          </div>
                        </div>
                      ))}
                    </div>
                  </>
                )}
              </div>
            )}
          </div>
        )}
      </FormDialog>
      {scan.dialog}
    </>
  );
}
