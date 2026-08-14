// Fixer settings — OAuth persist status, default harness/model/effort.
import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { api } from "@/lib/api";
import { useFixEngines, type FixCatalogEngine, type FixerPromptTemplate } from "@/lib/fixes";
import { FixerConsolePanel } from "@/components/fixes/fixer-console";

export function FixerSettings() {
  const qc = useQueryClient();
  const engines = useFixEngines(true);
  const settingsQ = useQuery({
    queryKey: ["settings"],
    queryFn: async () => {
      const r = await api.get<{ key: string; value: string }[] | Record<string, string>>(
        "/settings",
      );
      const out: Record<string, string> = {};
      if (Array.isArray(r.data)) {
        for (const row of r.data) out[row.key] = row.value;
      } else if (r.data && typeof r.data === "object") {
        for (const [k, v] of Object.entries(r.data)) out[k] = String(v);
      }
      return out;
    },
  });
  const save = useMutation({
    mutationFn: (updates: Record<string, string>) => api.put("/settings", updates),
    onSuccess: () => {
      toast.success("Fixer defaults saved");
      qc.invalidateQueries({ queryKey: ["settings"] });
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

  return (
    <section className="space-y-5">
      <div className="glass-card p-5 space-y-4 text-sm">
        <div>
          <h2 className="text-sm font-medium">Fixer harness</h2>
          <p className="mt-1 text-xs text-muted-foreground">
            Default engine for every new fix. Claude, Codex, and OpenCode log
            in on the worker. Grok uses an xAI API key.
          </p>
        </div>
        <label className="block space-y-1">
          <span className="text-xs text-muted-foreground">Harness</span>
          <select
            value={engineName}
            onChange={(e) =>
              save.mutate({
                fixer_engine: e.target.value,
                fixer_model: "",
              })
            }
            className="w-full h-9 px-2 rounded-md bg-muted/40 border border-border/40 text-sm"
          >
            <option value="auto">Auto (first logged-in CLI, then API)</option>
            {catalog.map((e) => (
              <option key={e.name} value={e.name}>
                {e.label}
              </option>
            ))}
          </select>
        </label>
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
                    ? "text-emerald-300 bg-emerald-500/10 border-emerald-500/30"
                    : "text-muted-foreground bg-muted/20 border-border/30"
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
        {selected && (
          <label className="block space-y-1">
            <span className="text-xs text-muted-foreground">
              Model{live ? " · live from worker" : ""}
            </span>
            <select
              value={model}
              onChange={(e) =>
                save.mutate({
                  fixer_engine: engineName === "auto" ? selected.name : engineName,
                  fixer_model: e.target.value,
                })
              }
              className="w-full h-9 px-2 rounded-md bg-muted/40 border border-border/40 text-sm"
            >
              {liveModels.map((m) => (
                <option key={m.id} value={m.id}>
                  {m.label}
                  {m.provider ? ` · ${m.provider}` : ""}
                  {m.context_k ? ` · ${m.context_k}k ctx` : ""}
                  {m.speed ? ` · ${m.speed}` : ""}
                </option>
              ))}
            </select>
          </label>
        )}
        {efforts.length > 0 && (
          <label className="block space-y-1">
            <span className="text-xs text-muted-foreground">Effort / speed</span>
            <select
              value={effort}
              onChange={(e) => save.mutate({ fixer_effort: e.target.value })}
              className="w-full h-9 px-2 rounded-md bg-muted/40 border border-border/40 text-sm"
            >
              {efforts.map((e) => (
                <option key={e.id} value={e.id}>
                  {e.label}
                  {e.hint ? ` — ${e.hint}` : ""}
                </option>
              ))}
            </select>
          </label>
        )}
      </div>

      <FixerPromptEditors
        prompts={engines.data?.prompts}
        saving={save.isPending}
        onSave={(updates) => save.mutate(updates)}
      />
    </section>
  );
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
      <div className="flex items-start justify-between gap-3">
        <div>
          <div className="font-medium">{title}</div>
          <p className="text-xs text-muted-foreground">{hint}</p>
        </div>
        <div className="flex gap-2 shrink-0">
          <button
            type="button"
            disabled={saving}
            onClick={() => {
              setDraft(tmpl.default);
              onSave({ [tmpl.key]: "" });
            }}
            className="h-8 px-2 rounded-md border border-border/50 text-xs hover:bg-muted/40 disabled:opacity-50"
          >
            Reset to default
          </button>
          <button
            type="button"
            disabled={saving || !dirty}
            onClick={() => onSave({ [tmpl.key]: draft })}
            className="h-8 px-2 rounded-md bg-primary text-primary-foreground text-xs font-medium disabled:opacity-50"
          >
            Save
          </button>
        </div>
      </div>
      <textarea
        value={draft}
        onChange={(e) => setDraft(e.target.value)}
        spellCheck={false}
        rows={14}
        className="w-full min-h-48 font-mono text-[11px] leading-relaxed rounded-md bg-muted/30 border border-border/40 p-3"
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
