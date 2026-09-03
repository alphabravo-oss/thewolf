import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { PlusIcon, Trash2Icon } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { api, ApiError, isNotFound } from "@/lib/api";
import { useVersion } from "@/lib/edition";
import { Button } from "@/components/ui/button";
import { EmptyState } from "@/components/ui/empty-state";
import { Input } from "@/components/ui/input";
import { PageHeader, PageSection, PageShell } from "@/components/ui/page";
import type { ReactNode } from "react";

export function EnterpriseChrome({
  title,
  description,
  children,
}: {
  title: string;
  description?: string;
  children: ReactNode;
}) {
  const v = useVersion();
  return (
    <PageShell>
      <PageHeader
        eyebrow={v.data?.product ?? "Enterprise"}
        title={title}
        description={description}
      />
      {children}
    </PageShell>
  );
}

export type EnterpriseRecord = {
  id: string;
  name?: string;
  kind?: string;
  [key: string]: unknown;
};

export function RecordsPanel({
  path,
  title,
  description,
  send,
}: {
  path: string;
  title: string;
  description?: string;
  send?: boolean;
}) {
  const qc = useQueryClient();
  const [name, setName] = useState("");
  const q = useQuery({
    queryKey: ["enterprise", path],
    queryFn: async () => {
      const r = await api.get<EnterpriseRecord[]>(path);
      return Array.isArray(r.data) ? r.data : [];
    },
  });
  const create = useMutation({
    mutationFn: async () => {
      const r = await api.post<EnterpriseRecord>(path, { name: name.trim() });
      return r.data;
    },
    onSuccess: () => {
      setName("");
      qc.invalidateQueries({ queryKey: ["enterprise", path] });
    },
    onError: (e) => toast.error(e instanceof Error ? e.message : "Create failed"),
  });
  const del = useMutation({
    mutationFn: async (id: string) => api.delete(`${path}/${encodeURIComponent(id)}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["enterprise", path] }),
    onError: (e) => toast.error(e instanceof Error ? e.message : "Delete failed"),
  });
  const postSend = useMutation({
    mutationFn: async (id: string) =>
      api.post(`${path}/${encodeURIComponent(id)}/send`, { event: "wolf.test" }),
    onSuccess: () => toast.success("Delivered"),
    onError: (e) => toast.error(e instanceof Error ? e.message : "Send failed"),
  });

  if (q.isError) {
    const unavailable = isNotFound(q.error) || (q.error instanceof ApiError && q.error.status === 404);
    return (
      <EmptyState
        title={unavailable ? "Not in this edition" : "Could not load"}
        description={
          unavailable
            ? "This Enterprise module is not available on Wolf Community."
            : q.error instanceof Error
              ? q.error.message
              : "Request failed"
        }
      />
    );
  }

  const rows = q.data ?? [];
  return (
    <PageSection title={title} description={description}>
      <form
        className="flex flex-wrap gap-2"
        onSubmit={(e) => {
          e.preventDefault();
          if (name.trim()) create.mutate();
        }}
      >
        <Input
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="Name"
          className="max-w-xs"
        />
        <Button type="submit" size="sm" disabled={create.isPending || !name.trim()}>
          <PlusIcon />
          Add
        </Button>
      </form>
      {q.isLoading ? (
        <p className="text-sm text-muted-foreground">Loading…</p>
      ) : rows.length === 0 ? (
        <p className="text-sm text-muted-foreground">None yet.</p>
      ) : (
        <ul className="divide-y divide-border rounded-md border border-border">
          {rows.map((row) => (
            <li key={row.id} className="flex items-center gap-3 px-3 py-2 text-sm">
              <div className="min-w-0 flex-1">
                <p className="font-medium truncate">{row.name || row.id}</p>
                <p className="text-xs text-muted-foreground font-mono truncate">{row.id}</p>
              </div>
              {send ? (
                <Button
                  type="button"
                  size="sm"
                  variant="outline"
                  onClick={() => postSend.mutate(row.id)}
                  disabled={postSend.isPending}
                >
                  Send
                </Button>
              ) : null}
              <Button
                type="button"
                size="icon"
                variant="ghost"
                aria-label={`Delete ${row.name || row.id}`}
                onClick={() => del.mutate(row.id)}
              >
                <Trash2Icon />
              </Button>
            </li>
          ))}
        </ul>
      )}
    </PageSection>
  );
}
