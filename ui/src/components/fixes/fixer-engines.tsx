// One card per fixer engine: who it is, whether it is signed in, and (when it
// is) its own model and effort pickers with a "Use as default" button. Replaces
// a single shared harness/model form that blurred which engine a model
// belonged to.
import { useState } from "react";
import { LogInIcon } from "lucide-react";
import { Link } from "@tanstack/react-router";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { StatusBadge } from "@/components/ui/status-badge";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import type { FixCatalogEngine, FixCatalogModel, FixEngineStatus } from "@/lib/fixes";

// Who is behind each engine, in plain words.
const ENGINE_INFO: Record<string, { vendor: string; about: string; loginId?: string }> = {
  "claude-code": { vendor: "Anthropic", about: "Claude Code CLI · Claude subscription or API key", loginId: "claude" },
  codex: { vendor: "OpenAI", about: "Codex CLI · ChatGPT sign-in or API key", loginId: "codex" },
  opencode: { vendor: "OpenCode", about: "Multi-provider CLI · its own provider logins (OpenAI via ChatGPT)", loginId: "opencode" },
  "grok-build": { vendor: "xAI", about: "Grok Build CLI · device sign-in or API key", loginId: "grok" },
  grok: { vendor: "xAI", about: "xAI API · no CLI, uses an xAI API key" },
  api: { vendor: "API", about: "Direct Anthropic / OpenAI / xAI API keys · no CLI" },
};

export function isConnected(row?: FixEngineStatus) {
  return Boolean(row && row.available && (row.auth === "oauth" || row.auth === "api_key"));
}

type Settings = Record<string, string>;

export function FixerEngines({ catalog, worker, settings, saving, onSave, onSignIn }: {
  catalog: FixCatalogEngine[];
  worker: FixEngineStatus[];
  settings: Settings;
  saving: boolean;
  onSave: (updates: Record<string, string>) => void;
  onSignIn: (loginId: string) => void;
}) {
  const defaultEngine = settings.fixer_engine || "auto";
  const rowFor = (name: string) => worker.find((r) => r.name === name);
  const ordered = [...catalog].sort((a, b) => {
    const rank = (e: FixCatalogEngine) => (e.name === defaultEngine ? 0 : isConnected(rowFor(e.name)) ? 1 : 2);
    return rank(a) - rank(b);
  });
  return (
    <section aria-label="Fixer engines" className="space-y-4 text-sm">
      <p className="text-sm text-muted-foreground">
        Each engine signs in on its own. Pick one as the default for new fixes and choose its model
        and effort. A single fix can still override them.
      </p>
      <AutoCard active={defaultEngine === "auto"} saving={saving} onUse={() => onSave({ fixer_engine: "auto", fixer_model: "", fixer_effort: "" })} />
      <div className="grid gap-3 lg:grid-cols-2">
        {ordered.map((engine) => (
          <EngineCard
            key={`${engine.name}:${defaultEngine === engine.name ? `${settings.fixer_model}:${settings.fixer_effort}` : ""}`}
            engine={engine}
            row={rowFor(engine.name)}
            isDefault={defaultEngine === engine.name}
            settings={settings}
            saving={saving}
            onSave={onSave}
            onSignIn={onSignIn}
          />
        ))}
      </div>
    </section>
  );
}

function AutoCard({ active, saving, onUse }: { active: boolean; saving: boolean; onUse: () => void }) {
  return (
    <Card className={`flex items-center justify-between gap-3 p-4 ${active ? "border-primary/50 bg-primary/5" : ""}`}>
      <div>
        <div className="font-medium">Auto</div>
        <div className="text-xs text-muted-foreground">Use the first signed-in engine; fall back to the API.</div>
      </div>
      {active ? <DefaultChip /> : <Button size="sm" variant="outline" disabled={saving} onClick={onUse}>Use Auto</Button>}
    </Card>
  );
}

function DefaultChip() {
  return <StatusBadge status="info" label="Default" size="sm" showDot={false} />;
}

function EngineCard({ engine, row, isDefault, settings, saving, onSave, onSignIn }: {
  engine: FixCatalogEngine;
  row?: FixEngineStatus;
  isDefault: boolean;
  settings: Settings;
  saving: boolean;
  onSave: (updates: Record<string, string>) => void;
  onSignIn: (loginId: string) => void;
}) {
  const info = ENGINE_INFO[engine.name] ?? { vendor: engine.label, about: "" };
  const connected = isConnected(row);
  const models = engine.models ?? [];
  const savedModel = isDefault ? settings.fixer_model : "";
  const initialModel = models.some((m) => m.id === savedModel) ? savedModel : models.find((m) => m.default)?.id ?? models[0]?.id ?? "";
  const [model, setModel] = useState(initialModel);
  const efforts = effortsFor(models.find((m) => m.id === model), engine);
  const savedEffort = isDefault ? settings.fixer_effort : "";
  const pickEffort = (list: { id: string }[], wanted: string) =>
    list.find((e) => e.id === wanted)?.id ?? list.find((e) => e.id === "high")?.id ?? list.find((e) => e.id === "medium")?.id ?? list[0]?.id ?? "";
  const initialEffort = pickEffort(effortsFor(models.find((m) => m.id === initialModel), engine), savedEffort);
  const [effort, setEffort] = useState(initialEffort);
  const effortValid = pickEffort(efforts, effort);
  const dirty = isDefault && (model !== initialModel || effortValid !== initialEffort);

  return (
    <Card role="article" aria-label={engine.label} className={`space-y-3 p-4 ${isDefault ? "border-primary/50 bg-primary/5" : ""}`}>
      <header className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <div className="flex items-center gap-2">
            <h3 className="font-medium truncate">{engine.label}</h3>
            {isDefault && <DefaultChip />}
          </div>
          <p className="text-xs text-muted-foreground">{info.about}</p>
        </div>
        <StatusBadge status={connected ? "connected" : "neutral"} label={connected ? "Signed in" : "Not connected"} size="sm" className="shrink-0" />
      </header>

      {connected ? (
        <>
          {(row?.account || row?.detail) && <p className="text-xs text-muted-foreground truncate">{row?.account || row?.detail}</p>}
          {models.length > 0 && (
            <div className="grid gap-2 sm:grid-cols-2">
              <Labeled label="Model">
                <Select value={model} onValueChange={setModel} disabled={saving}>
                  <SelectTrigger aria-label={`${engine.label} model`} className="h-9 min-w-0"><SelectValue /></SelectTrigger>
                  <SelectContent>{models.map((m) => <SelectItem key={m.id} value={m.id}>{modelLabel(m)}</SelectItem>)}</SelectContent>
                </Select>
              </Labeled>
              {efforts.length > 0 && (
                <Labeled label="Effort">
                  <Select value={effortValid} onValueChange={setEffort} disabled={saving}>
                    <SelectTrigger aria-label={`${engine.label} effort`} className="h-9 min-w-0"><SelectValue /></SelectTrigger>
                    <SelectContent>{efforts.map((e) => <SelectItem key={e.id} value={e.id}>{e.label}</SelectItem>)}</SelectContent>
                  </Select>
                </Labeled>
              )}
            </div>
          )}
          {!isDefault && (
            <Button size="sm" disabled={saving} onClick={() => onSave({ fixer_engine: engine.name, fixer_model: model, fixer_effort: effortValid })}>
              Use {engine.label} as default
            </Button>
          )}
          {isDefault && dirty && (
            <Button size="sm" disabled={saving} onClick={() => onSave({ fixer_engine: engine.name, fixer_model: model, fixer_effort: effortValid })}>
              Save model & effort
            </Button>
          )}
        </>
      ) : (
        <div className="space-y-2">
          <p className="text-xs text-muted-foreground">{row?.detail || "Not connected yet."}</p>
          {info.loginId ? (
            <Button size="sm" variant="outline" onClick={() => onSignIn(info.loginId!)}>
              <LogInIcon className="size-3.5" />
              Sign in to {engine.label}
            </Button>
          ) : (
            <p className="text-xs text-muted-foreground">
              Add an API key under <Link to="/settings" search={{ tab: "secrets" }} className="underline">Secrets &amp; credentials</Link>.
            </p>
          )}
        </div>
      )}
    </Card>
  );
}

function Labeled({ label, children }: { label: string; children: React.ReactNode }) {
  return <div className="space-y-1"><div className="text-xs text-muted-foreground">{label}</div>{children}</div>;
}

function effortsFor(model: FixCatalogModel | undefined, engine: FixCatalogEngine) {
  return model?.efforts && model.efforts.length > 0 ? model.efforts : engine.efforts ?? [];
}

function modelLabel(m: FixCatalogModel) {
  return [m.label, m.context_k ? `${m.context_k}k ctx` : "", m.speed].filter(Boolean).join(" · ");
}
