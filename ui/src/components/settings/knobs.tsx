// Global toggles backed by GET/PUT /api/settings. A section lists the knobs it
// owns; anything else in the settings KV is left untouched.
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { api } from "@/lib/api";
import { ErrorState } from "@/components/ui/empty-state";
import { StatusBadge } from "@/components/ui/status-badge";
import { entitled, useEdition } from "@/lib/edition";
import { useSettingsMap } from "@/lib/flags";
import { BoolToggle, ChoiceSetting, IntInput, SettingRow, SettingsCard } from "./shared";

type Common = { key: string; label: string; help: string; alpha?: boolean; entitlement?: string };
export type Knob = Common &
  (
    | { type: "bool" }
    | { type: "int"; min: number; max: number }
    | { type: "choice"; options: string[] }
  );

export function useSaveSettings() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (updates: Record<string, string>) => api.put("/settings", updates),
    onSuccess: async (_, updates) => {
      qc.setQueryData<Record<string, string>>(["settings"], (previous) => ({ ...previous, ...updates }));
      toast.success("Settings saved");
      await Promise.all([
        qc.invalidateQueries({ queryKey: ["settings"] }),
        qc.invalidateQueries({ queryKey: ["scanner-scheduling"] }),
      ]);
    },
    onError: (e) => toast.error(e instanceof Error ? e.message : "Save failed"),
  });
}

export function KnobsCard({ title, description, knobs }: { title: string; description?: string; knobs: Knob[] }) {
  const q = useSettingsMap();
  const edition = useEdition();
  const save = useSaveSettings();
  if (q.isLoading) return <p role="status" className="text-sm text-muted-foreground">Loading…</p>;
  if (q.isError) return <ErrorState title="Could not load settings" onRetry={() => q.refetch()} />;
  const settings = q.data ?? {};
  return (
    <SettingsCard title={title} description={description}>
      <div className="space-y-5">
        {knobs.map((knob) => {
          const current = settings[knob.key] ?? (knob.type === "int" ? "0" : "");
          const locked = knob.entitlement !== undefined && !entitled(edition.data, knob.entitlement);
          return (
            <SettingRow
              key={knob.key}
              label={
                <span className="inline-flex items-center gap-1.5">
                  {knob.label}
                  {locked && <StatusBadge status="info" label="Enterprise" size="sm" showDot={false} />}
                  {knob.alpha && <StatusBadge status="warning" label="Alpha" size="sm" showDot={false} />}
                </span>
              }
              help={knob.help}
            >
              {knob.type === "bool" ? (
                <BoolToggle
                  label={knob.label}
                  value={!locked && current === "true"}
                  onChange={(v) => save.mutateAsync({ [knob.key]: v ? "true" : "false" })}
                  disabled={save.isPending || locked}
                />
              ) : knob.type === "choice" ? (
                <ChoiceSetting label={knob.label} value={current || knob.options[0]} options={knob.options}
                  onCommit={(value) => save.mutate({ [knob.key]: value })} disabled={save.isPending} />
              ) : (
                <IntInput label={knob.label} value={current} min={knob.min} max={knob.max}
                  onCommit={(v) => save.mutate({ [knob.key]: v })} disabled={save.isPending} />
              )}
            </SettingRow>
          );
        })}
      </div>
    </SettingsCard>
  );
}
