// Banner for leftover scans/findings after a repo was deleted without purge.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { api } from "@/lib/api";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { useState } from "react";

export function OrphanRecordsBanner() {
  const qc = useQueryClient();
  const [open, setOpen] = useState(false);
  const q = useQuery({
    queryKey: ["scan-orphans"],
    queryFn: async () => {
      const r = await api.get<{
        scan_ids: string[];
        count: number;
        scan_count?: number;
        finding_count?: number;
      }>("/scans/orphans");
      return r.data;
    },
  });
  const purge = useMutation({
    mutationFn: () => api.delete<{ scan_ids: string[]; purged: number }>("/scans/orphans"),
    onSuccess: (r) => {
      toast.success(
        `Removed ${r.data?.purged ?? 0} leftover scan${(r.data?.purged ?? 0) === 1 ? "" : "s"}`,
      );
      setOpen(false);
      qc.invalidateQueries({ queryKey: ["scan-orphans"] });
      qc.invalidateQueries({ queryKey: ["scans"] });
      qc.invalidateQueries({ queryKey: ["findings"] });
      qc.invalidateQueries({ queryKey: ["fleet"] });
    },
    onError: (e) =>
      toast.error(e instanceof Error ? e.message : "Cleanup failed"),
  });

  const scans = q.data?.scan_count ?? q.data?.scan_ids?.length ?? 0;
  const findings = q.data?.finding_count ?? 0;
  const count = (q.data?.count ?? 0) || scans + findings;
  if (!count) return null;

  const bits: string[] = [];
  if (scans > 0) bits.push(`${scans} scan${scans === 1 ? "" : "s"}`);
  if (findings > 0) bits.push(`${findings} finding${findings === 1 ? "" : "s"}`);
  const what = bits.join(" and ") || `${count} leftover record${count === 1 ? "" : "s"}`;

  return (
    <>
      <div className="mx-4 mt-4 md:mx-6 rounded-md border border-amber-500/40 bg-amber-500/10 px-4 py-3 text-sm flex flex-wrap items-center justify-between gap-3">
        <p>
          Leftover records remain after a repo was deleted: {what}. They are
          not tied to any project. Source code was never removed.
        </p>
        <button
          type="button"
          onClick={() => setOpen(true)}
          className="h-8 px-3 rounded-md bg-amber-500/20 border border-amber-500/40 text-xs font-medium hover:bg-amber-500/30"
        >
          Remove leftover records
        </button>
      </div>
      <ConfirmDialog
        open={open}
        onOpenChange={setOpen}
        title="Remove leftover records?"
        description={`Permanently delete ${what} from repos that are already gone. This cannot be undone.`}
        confirmLabel="Remove records"
        pending={purge.isPending}
        onConfirm={() => purge.mutate()}
      />
    </>
  );
}
