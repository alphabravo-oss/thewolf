// Fixer settings — OAuth persist status, default harness/model/effort.
import { useEffect, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { useSettingsMap } from "@/lib/flags";
import { api } from "@/lib/api";
import { useFixEngines, type FixCatalogEngine, type FixerPromptTemplate } from "@/lib/fixes";
import { FixerConsolePanel } from "@/components/fixes/fixer-console";

export function FixerSettings() {
  const qc = useQueryClient();
  const engines = useFixEngines(true);
  const settingsQ = useSettingsMap();
  const save = useMutation({
    mutationFn: (updates: Record<string, string>) => api.put("/settings", updates),
    onSuccess: async (_, updates) => {
      qc.setQueryData<Record<string, string>>(["settings"], (previous) => ({ ...previous, ...updates }));
      toast.success("Fixer defaults saved");
      await Promise.all([
        qc.invalidateQueries({ queryKey: ["settings"] }),
        qc.invalidateQueries({ queryKey: ["fix-engines"] }),
      ]);
    },
    onError: (e) => toast.error(e instanceof Error ? e.message : "Save failed"),
  });

  const catalog = engines.data?.catalog ?? [];
  const worker = engines.data?.worker ?? [];
  const settings = settingsQ.data ?? {};
  const engineName = settings.fixer_engine || "auto";
  const selected =
    catalog.find((e) => e.name === engineName) ??
    catalog.find((e) => e.name === "opencode") ??
    catalog.find((e) => e.name === "claude-code");
  const liveModels = selected?.models ?? [];
  const model =
    settings.fixer_model && liveModels.some((m) => m.id === settings.fixer_model)
      ? settings.fixer_model
      : liveModels.find((m) => m.default)?.id || liveModels[0]?.id || "";
  const selectedModel = liveModels.find((m) => m.id === model);
  const efforts =
    selectedModel?.efforts && selectedModel.efforts.length > 0
      ? selectedModel.efforts
      : selected?.efforts ?? [];
  const effort =
    settings.fixer_effort && efforts.some((e) => e.id === settings.fixer_effort)
      ? settings.fixer_effort
      : efforts.find((e) => e.id === "high")?.id ||
        efforts.find((e) => e.id === "medium")?.id ||
        efforts[0]?.id ||
        "high";
  const live = Boolean(selected && liveModels.some((m) => m.plan === "live"));

  if (settingsQ.isPending || engines.isPending) return <p role="status" className="text-sm text-muted-foreground">Loading fixer settings…</p>;
  if (settingsQ.isError || engines.isError) return <p role="alert" className="text-sm text-destructive">Could not load fixer settings.</p>;

  return (
    <section className="space-y-4">
      <div className="glass-card p-5 space-y-4 text-sm">
        <div>
          <h2 className="text-sm font-medium">Fixer harness</h2>
          <p className="mt-1 text-xs text-muted-foreground">
            Default engine for every new fix. Claude, Codex, and OpenCode log
            in on the worker. Grok uses an xAI API key.
          </p>
        </div>
        <FixerChoice label="Harness" value={engineName} disabled={save.isPending}
          options={[{ value: "auto", label: "Auto (first logged-in CLI, then API)" }, ...catalog.map(e => ({ value: e.name, label: e.label }))]}
          onSave={(value) => save.mutate({ fixer_engine: value, fixer_model: "", fixer_effort: "" })} />
      </div>

      <div className="glass-card p-5 space-y-3 text-sm">
        <h2 className="text-sm font-medium">Subscription login</h2>
        <p className="text-xs text-muted-foreground">
          Login runs on the separate Debian fixer worker, not the Alpine API
          box. Click the terminal and use it like a local shell. Session files
          stay in the fixer-home volume. Docker:{" "}
          <code className="font-mono">docker compose --profile fixer up -d</code>.
          Kubernetes: enable the fixer Deployment and mount the fixer-home PVC
          at <code className="font-mono">/home/wolf</code>.
        </p>
        <FixerConsolePanel
          allowShell={engines.data?.console_shell === true}
          worker={worker}
        />
        <p className="text-xs text-muted-foreground">
          Grok has no worker CLI login. Store an{" "}
          <code className="font-mono">xai_key</code> under Account → Secrets,
          then pick Grok (xAI) as the harness.
        </p>
        <p className="text-xs text-muted-foreground">
          CLI equivalent if you exec into the worker:
        </p>
        <pre className="text-xs bg-muted/30 rounded-md p-3 overflow-x-auto font-mono">
          wolf fixer login claude{"\n"}
          wolf fixer login codex{"\n"}
          wolf fixer login opencode{"\n"}
          wolf fixer status
        </pre>
        {engines.data?.oauth_hint && (
          <p className="text-xs text-muted-foreground">{engines.data.oauth_hint}</p>
        )}
        <div className="divide-y divide-border/30">
          {(worker.length ? worker : fallbackRows(catalog)).map((row) => (
            <div key={row.name} className="py-3 flex items-start justify-between gap-3">
              <div>
                <div className="font-medium">{labelFor(catalog, row.name)}</div>
                <div className="text-xs text-muted-foreground">
                  {row.detail || row.auth}
                  {row.account ? ` · ${row.account}` : ""}
                </div>
                {row.usage && (
                  <div className="text-xs text-muted-foreground mt-0.5">Usage: {row.usage}</div>
                )}
                {row.session_paths && row.session_paths.length > 0 && (
                  <div className="text-[11px] font-mono text-muted-foreground mt-0.5">
                    persist: {row.session_paths.join(" · ")}
                  </div>
                )}
              </div>
              <span
                className={`text-[10px] uppercase tracking-wide border rounded px-1.5 py-0.5 ${
                  row.auth === "oauth" || row.auth === "api_key"
                    ? "text-status-success bg-status-success/10 border-status-success/30"
                    : "text-muted-foreground bg-muted/20 border-border"
                }`}
              >
                {row.auth === "none" ? "not connected" : row.auth}
              </span>
            </div>
          ))}
        </div>
      </div>

      <div className="glass-card p-5 space-y-4 text-sm">
        <h2 className="text-sm font-medium">Default model & effort</h2>
        <p className="text-xs text-muted-foreground">
          {live
            ? "Models come from the logged-in worker (OpenCode `models`), not a baked list."
            : "Used for every new fix unless the job overrides them."}{" "}
          Effort is OpenCode <code className="font-mono">--variant</code>, Claude{" "}
          <code className="font-mono">--effort</code>, or Codex reasoning effort.
        </p>
        {selected && liveModels.length > 0 && <FixerChoice label={live ? "Model · live from worker" : "Model"}
          value={model} disabled={save.isPending}
          options={liveModels.map(m => ({ value: m.id, label: [m.label, m.provider, m.context_k ? `${m.context_k}k ctx` : "", m.speed].filter(Boolean).join(" · ") }))}
          onSave={(value) => save.mutate({ fixer_engine: engineName === "auto" ? selected.name : engineName, fixer_model: value, fixer_effort: "" })} />}
        {efforts.length > 0 && <FixerChoice label="Effort / speed" value={effort} disabled={save.isPending}
          options={efforts.map(e => ({ value: e.id, label: e.label + (e.hint ? ` — ${e.hint}` : "") }))}
          onSave={(value) => save.mutate({ fixer_effort: value })} />}
      </div>

      <FixerPromptEditors
        prompts={engines.data?.prompts}
        saving={save.isPending}
        onSave={(updates) => save.mutate(updates)}
      />
    </section>
  );
}

function FixerChoice({ label, value, options, disabled, onSave }: {
  label: string; value: string; options: { value: string; label: string }[];
  disabled: boolean; onSave: (value: string) => void;
}) {
  const [draft, setDraft] = useState(value);
  useEffect(() => setDraft(value), [value]);
  const dirty = draft !== value;
  return <form className="space-y-1" onSubmit={(e) => { e.preventDefault(); if (dirty && !disabled) onSave(draft); }}>
    <div className="text-xs text-muted-foreground">{label}</div>
    <div className="flex items-center gap-2">
      <Select value={draft} onValueChange={setDraft} disabled={disabled}>
        <SelectTrigger aria-label={label} className="h-9 min-w-0"><SelectValue /></SelectTrigger>
        <SelectContent>{options.map(option => <SelectItem key={option.value} value={option.value}>{option.label}</SelectItem>)}</SelectContent>
      </Select>
      {dirty && <Button type="submit" size="sm" disabled={disabled}>Save</Button>}
    </div>
  </form>;
}

function FixerPromptEditors({
  prompts,
  saving,
  onSave,
}: {
  prompts?: {
    initial: FixerPromptTemplate;
    followup: FixerPromptTemplate;
    placeholder: string;
  };
  saving: boolean;
  onSave: (updates: Record<string, string>) => void;
}) {
  if (!prompts) return null;
  return (
    <div className="glass-card p-5 space-y-5 text-sm">
      <div>
        <h2 className="text-sm font-medium">Fixer prompts</h2>
        <p className="mt-1 text-xs text-muted-foreground">
          The first agent pass uses the initial prompt. After a child scan,
          later passes use the follow-up. Each turn is one scanner: Wolf
          writes every finding from that tool to a review file. Keep{" "}
          <code className="font-mono">{"{{findings_file}}"}</code>,{" "}
          <code className="font-mono">{"{{tool}}"}</code>, and{" "}
          <code className="font-mono">{"{{count}}"}</code> in the template.
        </p>
      </div>
      <PromptEditor
        title="First pass"
        hint="Fix everything that is real. Time is not a reason to skip."
        tmpl={prompts.initial}
        saving={saving}
        onSave={onSave}
      />
      <PromptEditor
        title="Follow-up passes"
        hint="After rescan. More lenient, still not noise."
        tmpl={prompts.followup}
        saving={saving}
        onSave={onSave}
      />
    </div>
  );
}

function PromptEditor({
  title,
  hint,
  tmpl,
  saving,
  onSave,
}: {
  title: string;
  hint: string;
  tmpl: FixerPromptTemplate;
  saving: boolean;
  onSave: (updates: Record<string, string>) => void;
}) {
  const [draft, setDraft] = useState(tmpl.value);
  useEffect(() => {
    setDraft(tmpl.value);
  }, [tmpl.value]);
  const dirty = draft !== tmpl.value;
  return (
    <div className="space-y-2">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <div className="font-medium">{title}</div>
          <p className="text-xs text-muted-foreground">{hint}</p>
        </div>
        <div className="flex gap-2 shrink-0">
          <button
            type="button"
            disabled={saving}
            onClick={() => {
              onSave({ [tmpl.key]: "" });
            }}
            className="h-8 px-2 rounded-md border border-border text-xs hover:bg-muted/40 disabled:opacity-50"
          >
            Reset to default
          </button>
          {dirty && <Button type="button" size="sm" disabled={saving} onClick={() => onSave({ [tmpl.key]: draft })}>
            Save
          </Button>}
        </div>
      </div>
      <textarea
        aria-label={title}
        disabled={saving}
        onKeyDown={(e) => { if (e.key === "Enter" && (e.ctrlKey || e.metaKey) && dirty && !saving) { e.preventDefault(); onSave({ [tmpl.key]: draft }); } }}
        value={draft}
        onChange={(e) => setDraft(e.target.value)}
        spellCheck={false}
        rows={14}
        className="w-full min-h-48 font-mono text-[11px] leading-relaxed rounded-md bg-muted/30 border border-border p-3"
      />
    </div>
  );
}

function labelFor(catalog: FixCatalogEngine[], name: string) {
  return catalog.find((e) => e.name === name)?.label ?? name;
}

function fallbackRows(catalog: FixCatalogEngine[]): {
  name: string;
  auth: string;
  detail?: string;
  account?: string;
  usage?: string;
  session_paths?: string[];
}[] {
  return catalog.map((e) => ({
    name: e.name,
    auth: "none",
    detail: "worker has not reported status yet",
    session_paths: e.session_paths,
  }));
}
