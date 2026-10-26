import { Select, SelectTrigger, SelectValue, SelectContent, SelectItem } from "@/components/ui/select";
// Branch picker for scan-trigger surfaces. Live-fetches the repo's
// branches from GET /api/repos/{id}/branches using the shared Select,
// annotating the repo's default branch. A failed live list is shown as
// an error with Retry — we never pretend other branches do not exist.
import { useQuery } from "@tanstack/react-query";
import { api } from "@/lib/api";

type BranchesResponse = {
  branches: string[];
  default_branch: string;
  current_branch: string;
};

export function BranchSelect({
  repoId,
  value,
  onChange,
  defaultBranch,
  className,
  name = "branch",
}: {
  repoId: string;
  value: string;
  onChange: (branch: string) => void;
  defaultBranch?: string;
  className?: string;
  name?: string;
}) {
  const q = useQuery({
    queryKey: ["repo-branches", repoId],
    queryFn: async () => {
      const r = await api.get<BranchesResponse>(`/repos/${repoId}/branches`);
      return r.data;
    },
    enabled: !!repoId,
    retry: false,
  });

  const fallback = defaultBranch || value || "main";
  const live =
    q.data?.branches && q.data.branches.length > 0 ? q.data.branches : [];
  const selected = value || q.data?.default_branch || fallback;
  const branches = [...new Set([...(live.length ? live : [fallback]), selected])];
  const missingSelection = q.isSuccess && !live.includes(selected);
  const repoDefault = q.data?.default_branch || defaultBranch || "";

  const cls =
    className ??
    "h-9 px-2 rounded-md bg-background border border-muted/40 text-sm";

  return (
    <div className="flex flex-col items-stretch gap-1 min-w-0">
      <div className="flex items-center gap-1">
        <Select name={name} value={selected} onValueChange={onChange} disabled={!repoId || q.isLoading}><SelectTrigger className={cls} aria-invalid={q.isError || undefined} title={q.isError ? branchListError(q.error) : undefined} aria-label="Branch"><SelectValue /></SelectTrigger><SelectContent>
          {branches.map((b) => (<SelectItem key={b} value={b}>
              {b}
              {b === selected && missingSelection ? " (not in current refs)" : b === repoDefault ? " (default)" : ""}
            </SelectItem>
          ))}
        </SelectContent></Select>
        {q.isError && (
          <button
            type="button"
            onClick={() => void q.refetch()}
            className="h-9 px-2 rounded-md border border-border text-sm hover:bg-muted/50"
          >
            Retry
          </button>
        )}
      </div>
      {missingSelection && <p className="text-xs text-status-warning">This branch is not in the current refs. Choose an available branch or refresh the list before scanning.</p>}
      {q.isError && (
        <p className="text-[11px] text-destructive leading-snug max-w-[18rem]">
          Could not load live branches: {branchListError(q.error)}. Default is
          still selectable.
        </p>
      )}
    </div>
  );
}

function branchListError(err: unknown): string {
  if (err instanceof Error && err.message.trim()) {
    return err.message;
  }
  return "request failed";
}
