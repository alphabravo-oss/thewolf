// ui-next/src/routes/_authed.audit.index.tsx
import { useQuery } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import { api } from "@/lib/api";
import { EmptyState } from "@/components/empty-state";
import { ScrollTextIcon } from "lucide-react";

type AuditEntry = {
  id: string;
  method: string;
  path: string;
  status_code: number;
  user_id: string;
  token_id?: string;
  resource_id?: string;
  action: string;
  created_at: string;
};

export const Route = createFileRoute("/_authed/audit/")({
  component: AuditPage,
});

function AuditPage() {
  const q = useQuery({
    queryKey: ["audit-log"],
    queryFn: async () => {
      const { data } = await api.get<{ data: AuditEntry[] }>(
        "/audit-log?limit=100",
      );
      return data.data ?? [];
    },
  });

  if (q.isLoading) {
    return <div className="text-sm text-muted-foreground">Loading…</div>;
  }
  if (q.isError) {
    const msg = (q.error as Error).message;
    if (/403|forbidden/i.test(msg)) {
      return (
        <EmptyState
          icon={ScrollTextIcon}
          title="Admin only"
          description="The audit log is only readable by users with the admin scope."
        />
      );
    }
    return <div className="text-sm text-destructive">{msg}</div>;
  }
  const rows = q.data ?? [];
  if (rows.length === 0) {
    return (
      <EmptyState
        icon={ScrollTextIcon}
        title="No audit entries yet"
        description="Mutating API requests will land here once anyone changes anything."
      />
    );
  }

  return (
    <div>
      <div className="mb-4">
        <h1 className="text-2xl font-semibold tracking-tight">Audit log</h1>
        <p className="text-sm text-muted-foreground mt-1">
          Every mutating API request — POST, PUT, DELETE — with the actor, the
          resource, and the response status. Most recent 100.
        </p>
      </div>
      <div className="overflow-x-auto rounded-md border border-border/60">
        <table className="w-full text-sm">
          <thead className="bg-muted/30 text-xs uppercase text-muted-foreground">
            <tr>
              <th className="text-left px-3 py-2">Method</th>
              <th className="text-left px-3 py-2">Path</th>
              <th className="text-left px-3 py-2">Status</th>
              <th className="text-left px-3 py-2">Actor</th>
              <th className="text-left px-3 py-2">When</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((r) => (
              <tr key={r.id} className="border-t border-border/40">
                <td className="px-3 py-2 font-mono text-xs">{r.method}</td>
                <td className="px-3 py-2 font-mono text-xs truncate max-w-[40ch]">
                  {r.path}
                </td>
                <td className="px-3 py-2">
                  <StatusPill code={r.status_code} />
                </td>
                <td className="px-3 py-2 text-xs">
                  {r.token_id ? `token:${r.token_id.slice(0, 8)}…` : `user:${r.user_id.slice(0, 8)}…`}
                </td>
                <td className="px-3 py-2 text-xs text-muted-foreground">
                  {new Date(r.created_at).toLocaleString()}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}

function StatusPill({ code }: { code: number }) {
  const tone =
    code >= 500
      ? "bg-destructive/15 text-destructive"
      : code >= 400
        ? "bg-amber-500/15 text-amber-400"
        : code >= 300
          ? "bg-sky-500/15 text-sky-400"
          : "bg-emerald-500/15 text-emerald-400";
  return (
    <span className={`inline-flex h-5 items-center rounded px-1.5 text-xs ${tone}`}>
      {code}
    </span>
  );
}
