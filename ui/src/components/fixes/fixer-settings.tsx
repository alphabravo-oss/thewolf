// Fixer settings — per-engine cards (sign-in state, model, effort, default), the
// login console, and the prompt templates.
import { useEffect, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { useSettingsMap } from "@/lib/flags";
import { api } from "@/lib/api";
import { useFixEngines, type FixerPromptTemplate } from "@/lib/fixes";
import { FixerConsolePanel } from "@/components/fixes/fixer-console";
import { FixerEngines } from "@/components/fixes/fixer-engines";

export function FixerSettings() {
  const qc = useQueryClient();
  const engines = useFixEngines(true);
  const settingsQ = useSettingsMap();
  const [signIn, setSignIn] = useState({ engine: "", nonce: 0 });
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

  if (settingsQ.isPending || engines.isPending) return <p role="status" className="text-sm text-muted-foreground">Loading fixer settings…</p>;
  if (settingsQ.isError || engines.isError) return <p role="alert" className="text-sm text-destructive">Could not load fixer settings.</p>;

  return (
    <section className="space-y-4">
      <FixerEngines
        catalog={catalog}
        worker={worker}
        settings={settings}
        saving={save.isPending}
        onSave={(updates) => save.mutate(updates)}
        onSignIn={(engine) => {
          setSignIn((previous) => ({ engine, nonce: previous.nonce + 1 }));
          document.getElementById("fixer-sign-in")?.scrollIntoView({ behavior: "smooth", block: "start" });
        }}
      />

      <div id="fixer-sign-in" className="glass-card p-5 space-y-3 text-sm scroll-mt-20">
        <h2 className="text-sm font-medium">Sign in to an engine</h2>
        <p className="text-xs text-muted-foreground">
          Sign-in runs on the fixer worker, not the API server. Use the buttons below or
          the “Sign in” button on an engine card, then follow the URL and code in the
          terminal. Session files stay in the fixer-home volume (Kubernetes: the fixer
          PVC at <code className="font-mono">/home/wolf</code>).
        </p>
        <FixerConsolePanel
          allowShell={engines.data?.console_shell === true}
          worker={worker}
          request={signIn}
        />
        <p className="text-xs text-muted-foreground">
          Grok (xAI API) and Direct API have no CLI login: store an{" "}
          <code className="font-mono">xai_key</code>, <code className="font-mono">anthropic_key</code> or{" "}
          <code className="font-mono">openai_key</code> under Account → Secrets.
        </p>
        <p className="text-xs text-muted-foreground">
          CLI equivalent if you exec into the worker:
        </p>
        <pre className="text-xs bg-muted/30 rounded-md p-3 overflow-x-auto font-mono">
          wolf fixer login claude{"\n"}
          wolf fixer login codex{"\n"}
          wolf fixer login opencode{"\n"}
          wolf fixer login grok{"\n"}
          wolf fixer status
        </pre>
        {engines.data?.oauth_hint && (
          <p className="text-xs text-muted-foreground">{engines.data.oauth_hint}</p>
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
