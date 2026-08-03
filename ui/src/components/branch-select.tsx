// Branch picker for scan-trigger surfaces. Live-fetches the repo's
// branches from GET /api/repos/{id}/branches and renders a <select>,
// annotating the repo's default branch. While the fetch is in flight (or
// if it fails), it renders a single-option select containing just the
// default branch, so the surrounding form is never blocked — scanning the
// default branch always remains possible.
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
  const branches =
    q.data?.branches && q.data.branches.length > 0
      ? q.data.branches
      : [fallback];
  const repoDefault = q.data?.default_branch || defaultBranch || "";

  const cls =
    className ??
    "h-9 px-2 rounded-md bg-background border border-muted/40 text-sm";

  return (
    <select
      name={name}
      value={value || repoDefault || fallback}
      onChange={(e) => onChange(e.target.value)}
      disabled={!repoId || q.isLoading}
      className={cls}
    >
      {branches.map((b) => (
        <option key={b} value={b}>
          {b}
          {b === repoDefault ? " (default)" : ""}
        </option>
      ))}
    </select>
  );
}
