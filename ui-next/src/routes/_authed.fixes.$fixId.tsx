// Fix detail — Phase 1 stub.
import { createFileRoute, Link } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { ArrowLeftIcon } from "lucide-react";
import { api } from "@/lib/api";
import type { Fix } from "@/lib/types";
import { CardSkeleton } from "@/components/skeleton";

export const Route = createFileRoute("/_authed/fixes/$fixId")({
  component: FixDetailPage,
});

function FixDetailPage() {
  const { fixId } = Route.useParams();
  const q = useQuery({
    queryKey: ["fix", fixId],
    queryFn: async () => {
      const r = await api.get<Fix>(`/fixes/${fixId}`);
      return r.data;
    },
  });
  if (q.isLoading || !q.data) return <CardSkeleton />;
  const f = q.data;
  return (
    <div className="p-6 space-y-6 max-w-5xl">
      <div className="flex items-center gap-3">
        <Link to="/fixes" className="size-9 grid place-items-center rounded-md hover:bg-muted/50">
          <ArrowLeftIcon className="size-4" />
        </Link>
        <h1 className="text-xl font-semibold">{f.branch_name}</h1>
      </div>
      <section className="glass-card p-5 space-y-2 text-sm">
        <div><strong>Status:</strong> {f.status}</div>
        <div><strong>Fixed:</strong> {f.findings_fixed}</div>
        <div><strong>Failed:</strong> {f.findings_failed}</div>
        <div><strong>PRs:</strong> {f.pr_urls.length === 0 ? "—" : f.pr_urls.join(", ")}</div>
      </section>
    </div>
  );
}
