import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { api, isNotFound } from "@/lib/api";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { Button } from "@/components/ui/button";
import { FormDialog } from "@/components/ui/form-dialog";
import { Input } from "@/components/ui/input";
import { MetricCard } from "@/components/ui/metric-card";
import type { DiskUsage } from "@/lib/types";
import { formatBytes } from "@/lib/utils";
import { Field, SettingsCard } from "./shared";

type DatabaseInfo = {
  driver: string;
  user?: string;
  host?: string;
  database?: string;
  sslmode?: string;
  env_managed?: boolean;
  file_set?: boolean;
};

function DatabaseCard() {
  const q = useQuery({
    queryKey: ["admin-database"],
    queryFn: async () => (await api.get<DatabaseInfo>("/admin/database")).data,
  });
  const [open, setOpen] = useState(false);
  const [dsn, setDsn] = useState("");
  const close = (next: boolean) => {
    setOpen(next);
    if (!next) setDsn("");
  };
  const test = useMutation({
    mutationFn: () => api.post("/admin/database/test", { dsn }),
    onSuccess: () => toast.success("Connected"),
    onError: (e) => toast.error(e instanceof Error ? e.message : "Unreachable"),
  });
  const save = useMutation({
    mutationFn: () => api.put("/admin/database", { dsn }),
    onSuccess: () => {
      close(false);
      toast.success("Saved. Restart Wolf to use this database.");
    },
    onError: (e) => toast.error(e instanceof Error ? e.message : "Save failed"),
  });
  const info = q.data;
  const helmLocked = !!info?.env_managed;
  return (
    <SettingsCard
      title="Database"
      description={
        info?.driver === "postgres"
          ? `Postgres ${info.user ? `${info.user}@` : ""}${info.host || "—"}/${info.database || "—"}${info.sslmode ? ` · sslmode=${info.sslmode}` : ""}`
          : `SQLite${info?.database ? ` (${info.database})` : ""}`
      }
      actions={!helmLocked && <Button size="sm" variant="outline" onClick={() => setOpen(true)}>Change connection</Button>}
    >
      <p className="text-xs text-muted-foreground">
        {helmLocked ? (
          <>DSN comes from Helm (<code className="font-mono">postgres.mode</code>: bundled, cnpg, or external). Change values and helm upgrade.</>
        ) : (
          <>Point this instance at a remote Postgres; restart Wolf after saving. Helm installs should use <code className="font-mono">postgres.mode=external</code> instead.</>
        )}
      </p>
      <FormDialog
        open={open}
        onOpenChange={close}
        title="Change database connection"
        description="The new DSN is stored for the next start. Test it before saving."
        submitLabel="Save"
        pending={save.isPending}
        dirty={!!dsn}
        submitDisabled={!dsn.trim()}
        onSubmit={() => save.mutate()}
      >
        <Field label="Postgres connection string">
          <Input type="password" autoComplete="off" value={dsn} onChange={(e) => setDsn(e.target.value)}
            placeholder="postgres://user:pass@host:5432/wolf?sslmode=require" className="font-mono" />
        </Field>
        <Button type="button" variant="outline" size="sm" disabled={!dsn.trim() || test.isPending} onClick={() => test.mutate()}>
          {test.isPending ? "Testing…" : "Test connection"}
        </Button>
      </FormDialog>
    </SettingsCard>
  );
}

function DiskCard() {
  const qc = useQueryClient();
  const [confirm, setConfirm] = useState(false);
  const q = useQuery({
    queryKey: ["admin", "disk"],
    queryFn: async () => {
      try {
        return (await api.get<DiskUsage>("/admin/disk")).data ?? null;
      } catch (e) {
        if (isNotFound(e)) return null;
        throw e;
      }
    },
  });
  const reap = useMutation({
    mutationFn: () =>
      api.post<{ removed: number; retained_in_use?: number }>("/admin/workspaces/reap", { max_age_hours: 72 }),
    onSuccess: ({ data }) => {
      setConfirm(false);
      toast.success(`Removed ${data.removed} old workspaces${data.retained_in_use ? `; retained ${data.retained_in_use} in use` : ""}`);
      qc.invalidateQueries({ queryKey: ["admin", "disk"] });
    },
    onError: (e) => toast.error(e instanceof Error ? e.message : "Reap failed"),
  });
  if (q.data == null) return null;
  return (
    <SettingsCard
      title="Disk"
      description="Space used by scan artifacts, cloned workspaces and the database."
      actions={<Button size="sm" variant="outline" onClick={() => setConfirm(true)}>Reap old workspaces</Button>}
    >
      <div className="grid gap-3 sm:grid-cols-3">
        <MetricCard title="Artifacts" value={formatBytes(q.data.artifacts_bytes ?? 0)} />
        <MetricCard title="Workspaces" value={formatBytes(q.data.workspaces_bytes ?? 0)} />
        <MetricCard title="Database" value={formatBytes(q.data.db_bytes ?? 0)} />
      </div>
      <ConfirmDialog
        open={confirm}
        onOpenChange={setConfirm}
        title="Reap old workspaces?"
        description="Deletes cloned workspaces untouched for 72 hours. Workspaces in use are kept; scan results are not affected."
        confirmLabel="Reap"
        pending={reap.isPending}
        onConfirm={() => reap.mutate()}
      />
    </SettingsCard>
  );
}

export function DatabaseSection() {
  return (
    <div className="space-y-4">
      <DatabaseCard />
      <DiskCard />
    </div>
  );
}
