"use client";

import { useEffect, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Label } from "@/components/ui/label";
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "@/components/ui/tooltip";
import api, { getToken } from "@/lib/api";
import type {
  CollectionToolsResponse,
  RepoDetection,
  ScanConfig,
  ToolInfo,
} from "@/lib/types";

// Language → color mapping for pills
export const langColors: Record<string, string> = {
  go: "bg-cyan-100 text-cyan-800 dark:bg-cyan-900 dark:text-cyan-200",
  python: "bg-yellow-100 text-yellow-800 dark:bg-yellow-900 dark:text-yellow-200",
  javascript: "bg-amber-100 text-amber-800 dark:bg-amber-900 dark:text-amber-200",
  typescript: "bg-blue-100 text-blue-800 dark:bg-blue-900 dark:text-blue-200",
  rust: "bg-orange-100 text-orange-800 dark:bg-orange-900 dark:text-orange-200",
  java: "bg-red-100 text-red-800 dark:bg-red-900 dark:text-red-200",
  ruby: "bg-rose-100 text-rose-800 dark:bg-rose-900 dark:text-rose-200",
  php: "bg-indigo-100 text-indigo-800 dark:bg-indigo-900 dark:text-indigo-200",
  c: "bg-slate-100 text-slate-800 dark:bg-slate-900 dark:text-slate-200",
  "c++": "bg-slate-100 text-slate-800 dark:bg-slate-900 dark:text-slate-200",
  shell: "bg-green-100 text-green-800 dark:bg-green-900 dark:text-green-200",
};

export function getLangColor(lang: string): string {
  return langColors[lang.toLowerCase()] || "bg-muted text-muted-foreground";
}

interface ScanConfirmDialogProps {
  collectionId: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  scanConfig: ScanConfig;
  onConfirm: (config: ScanConfig) => void;
  /** When true, the dialog only saves config without starting a scan */
  configOnly?: boolean;
}

export function ScanConfirmDialog({
  collectionId,
  open,
  onOpenChange,
  scanConfig: initialConfig,
  onConfirm,
  configOnly = false,
}: ScanConfirmDialogProps) {
  const [config, setConfig] = useState<ScanConfig>(initialConfig);
  const [tools, setTools] = useState<ToolInfo[]>([]);
  const [repoSummary, setRepoSummary] = useState<RepoDetection[]>([]);
  const [loading, setLoading] = useState(false);

  // Install step state
  const [installPhase, setInstallPhase] = useState<"tools" | "installing" | "results">("tools");
  const [installing, setInstalling] = useState<string | null>(null);
  const [installLog, setInstallLog] = useState<string[]>([]);
  const [installResults, setInstallResults] = useState<
    Record<string, "pending" | "done" | "error" | "skipped">
  >({});
  const [showInstallLog, setShowInstallLog] = useState(true);
  const logEndRef = useRef<HTMLDivElement>(null);
  const cancelRef = useRef(false);

  // Sync when prop changes
  useEffect(() => {
    setConfig(initialConfig);
  }, [initialConfig]);

  // Reset install step when dialog closes
  useEffect(() => {
    if (!open) {
      setInstallPhase("tools");
      setInstalling(null);
      setInstallLog([]);
      setInstallResults({});
      setShowInstallLog(true);
      cancelRef.current = false;
    }
  }, [open]);

  // Fetch tools + repo summary when dialog opens
  useEffect(() => {
    if (!open) return;
    setLoading(true);
    api
      .get<CollectionToolsResponse>(`/collections/${collectionId}/tools`)
      .then((res) => {
        const data = res.data;
        const loadedTools = data.tools ?? [];
        setTools(loadedTools);
        setRepoSummary(data.repo_summary ?? []);
        // Auto-deselect tools that are unavailable+non-installable, or not relevant to this repo.
        const autoDisable = loadedTools
          .filter((t) => (!t.available && !t.installable) || !t.recommended)
          .map((t) => t.name);
        if (autoDisable.length > 0) {
          setConfig((prev) => {
            const current = prev.disabled_tools ?? [];
            const merged = [...new Set([...current, ...autoDisable])];
            return { ...prev, disabled_tools: merged };
          });
        }
      })
      .catch(() => {})
      .finally(() => setLoading(false));
  }, [open, collectionId]);

  // Auto-scroll install log
  useEffect(() => {
    const el = logEndRef.current?.parentElement;
    if (el) el.scrollTop = el.scrollHeight;
  }, [installLog]);

  // Tools are shown in a single flat list grouped by category.
  // Non-recommended tools are visually de-emphasized and auto-deselected.

  const groupByCategory = (list: ToolInfo[]) =>
    list.reduce<Record<string, ToolInfo[]>>((acc, t) => {
      const cat = t.category || "general";
      if (!acc[cat]) acc[cat] = [];
      acc[cat].push(t);
      return acc;
    }, {});

  const isDisabled = (name: string) =>
    config.disabled_tools?.includes(name) ?? false;

  const toggleTool = (name: string, enabled: boolean) => {
    const current = config.disabled_tools ?? [];
    const next = enabled
      ? current.filter((n) => n !== name)
      : [...current, name];
    setConfig({ ...config, disabled_tools: next });
  };

  // Bulk toggle: enable or disable a list of tools at once.
  const toggleTools = (names: string[], enabled: boolean) => {
    const current = config.disabled_tools ?? [];
    const next = enabled
      ? current.filter((n) => !names.includes(n))
      : [...new Set([...current, ...names])];
    setConfig({ ...config, disabled_tools: next });
  };

  // Check if all tools in a list are enabled.
  const allEnabled = (list: ToolInfo[]) =>
    list.every((t) => !isDisabled(t.name));

  // Check if some (but not all) tools in a list are enabled.
  const someEnabled = (list: ToolInfo[]) =>
    list.some((t) => !isDisabled(t.name)) && !allEnabled(list);

  // Compute missing tools that are enabled but not available
  const getMissingTools = () =>
    tools.filter((t) => !isDisabled(t.name) && !t.available);

  // Handle "Start Scan" click — intercept if missing tools
  const handleConfirm = () => {
    if (configOnly) {
      onConfirm(config);
      return;
    }
    const missing = getMissingTools();
    const hasInstallable = missing.some((t) => t.installable);
    if (missing.length > 0 && hasInstallable && installPhase === "tools") {
      // Initialize results and immediately start installing
      const results: Record<string, "pending" | "done" | "error" | "skipped"> = {};
      for (const t of missing) {
        results[t.name] = t.installable ? "pending" : "skipped";
      }
      setInstallResults(results);
      setInstallLog([]);
      setShowInstallLog(true);
      setInstallPhase("installing");
      return;
    }
    onConfirm(config);
  };

  // Install a single tool via SSE streaming
  const installTool = async (name: string) => {
    setInstalling(name);
    setInstallLog((prev) => [...prev, `--- Installing ${name} ---`]);

    const apiUrl =
      process.env.NEXT_PUBLIC_API_URL || "http://localhost:8778/api";
    const token = getToken();
    const headers: Record<string, string> = {};
    if (token) headers["Authorization"] = `Bearer ${token}`;

    try {
      const response = await fetch(`${apiUrl}/config/plugins/${name}/install`, {
        method: "POST",
        headers,
        credentials: "include",
      });

      if (!response.ok) {
        const err = await response.json().catch(() => null);
        setInstallLog((prev) => [
          ...prev,
          `Error: ${err?.error?.message || `Install failed (HTTP ${response.status})`}`,
        ]);
        setInstallResults((prev) => ({ ...prev, [name]: "error" }));
        setInstalling(null);
        return;
      }

      const reader = response.body?.getReader();
      const decoder = new TextDecoder();

      if (!reader) {
        setInstallResults((prev) => ({ ...prev, [name]: "error" }));
        setInstalling(null);
        return;
      }

      let buffer = "";

      while (true) {
        const { done, value } = await reader.read();
        if (done) break;

        buffer += decoder.decode(value, { stream: true });
        const lines = buffer.split("\n");
        buffer = lines.pop() || "";

        for (const line of lines) {
          if (line.startsWith("data: ")) {
            const data = line.slice(6);
            try {
              const parsed = JSON.parse(data);
              if (parsed.installed) {
                // Success — mark tool as available
                setTools((prev) =>
                  prev.map((t) =>
                    t.name === name ? { ...t, available: true } : t
                  )
                );
                setInstallResults((prev) => ({ ...prev, [name]: "done" }));
                setInstalling(null);
                return;
              }
              if (parsed.error) {
                setInstallLog((prev) => [...prev, `Error: ${parsed.error}`]);
                setInstallResults((prev) => ({ ...prev, [name]: "error" }));
                setInstalling(null);
                return;
              }
            } catch {
              // Not JSON — it's a log line
              setInstallLog((prev) => [...prev, data]);
            }
          }
        }
      }

      // Stream ended without explicit success/error — check if now available
      setInstallResults((prev) => ({ ...prev, [name]: "done" }));
      setTools((prev) =>
        prev.map((t) => (t.name === name ? { ...t, available: true } : t))
      );
    } catch (err) {
      setInstallLog((prev) => [
        ...prev,
        `Error: ${err instanceof Error ? err.message : "Connection failed"}`,
      ]);
      setInstallResults((prev) => ({ ...prev, [name]: "error" }));
    } finally {
      setInstalling(null);
    }
  };

  // Auto-install when entering "installing" phase
  useEffect(() => {
    if (installPhase !== "installing") return;
    cancelRef.current = false;

    const runInstalls = async () => {
      const missing = getMissingTools();
      const installable = missing.filter(
        (t) => t.installable && installResults[t.name] === "pending"
      );

      for (const tool of installable) {
        if (cancelRef.current) break;
        await installTool(tool.name);
      }
      if (cancelRef.current) return;

      // Always show results — let the user decide when to proceed
      setInstallPhase("results");
    };

    runInstalls();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [installPhase]);

  const renderToolCheckbox = (tool: ToolInfo) => {
    const card = (
      <label
        key={tool.name}
        className={`flex items-center gap-2 rounded-md border p-2 text-sm cursor-pointer hover:bg-muted/50 transition-colors ${!tool.recommended ? "border-dashed opacity-60" : ""}`}
      >
        <input
          type="checkbox"
          checked={!isDisabled(tool.name)}
          onChange={(e) => toggleTool(tool.name, e.target.checked)}
          className="rounded border-muted-foreground"
        />
        <span className="flex-1">{tool.name}</span>
        {!tool.recommended && (
          <span className="text-[10px] px-1.5 py-0.5 rounded bg-muted text-muted-foreground">
            not relevant
          </span>
        )}
        {!tool.available && tool.installable && (
          <span className="text-[10px] px-1.5 py-0.5 rounded bg-amber-100 text-amber-700 dark:bg-amber-900 dark:text-amber-300">
            not installed
          </span>
        )}
        {!tool.available && !tool.installable && (
          <span
            className="text-[10px] px-1.5 py-0.5 rounded bg-red-100 text-red-700 dark:bg-red-900 dark:text-red-300"
            title={tool.install_hint || "No automatic install method available"}
          >
            {tool.install_hint || "not installable"}
          </span>
        )}
      </label>
    );

    if (!tool.description) return <div key={tool.name}>{card}</div>;

    return (
      <Tooltip key={tool.name}>
        <TooltipTrigger asChild>
          <div>{card}</div>
        </TooltipTrigger>
        <TooltipContent side="top" className="max-w-sm">
          <p>{tool.description}</p>
        </TooltipContent>
      </Tooltip>
    );
  };

  // Compute install summary counts
  const getInstallSummary = () => {
    const entries = Object.entries(installResults);
    const installable = entries.filter(([, s]) => s !== "skipped");
    const done = installable.filter(([, s]) => s === "done").length;
    const failed = installable.filter(([, s]) => s === "error").length;
    const total = installable.length;
    return { done, failed, total };
  };

  // Retry failed installs
  const retryFailed = () => {
    setInstallResults((prev) => {
      const next = { ...prev };
      for (const key of Object.keys(next)) {
        if (next[key] === "error") next[key] = "pending";
      }
      return next;
    });
    setInstallPhase("installing");
  };

  // Cancel active install
  const cancelInstall = () => {
    cancelRef.current = true;
    setInstalling(null);
    setInstallPhase("results");
  };

  // Render install step (both "installing" and "results" phases)
  const renderInstallStep = () => {
    // Use installResults keys to build a stable tool list that doesn't
    // shrink as tools become available — tools stay visible after install.
    const trackedNames = Object.keys(installResults);
    const trackedTools = trackedNames
      .map((name) => tools.find((t) => t.name === name))
      .filter(Boolean) as ToolInfo[];
    const isInstalling = installPhase === "installing";
    const isResults = installPhase === "results";
    const { done, failed, total } = getInstallSummary();

    return (
      <div className="space-y-4 pt-2">
        {/* Summary line */}
        <div className="text-sm text-muted-foreground">
          {isInstalling && (
            <>Installing tools... ({done} of {total} complete)</>
          )}
          {isResults && failed > 0 && (
            <span className="text-amber-600 dark:text-amber-400">
              {done} of {total} tool{total !== 1 ? "s" : ""} installed
              &mdash; {failed} failed
            </span>
          )}
          {isResults && failed === 0 && (
            <span className="text-green-600 dark:text-green-400">
              All {done} tool{done !== 1 ? "s" : ""} installed successfully
            </span>
          )}
        </div>

        {/* Tool list with live status */}
        <div className="space-y-2">
          {trackedTools.map((tool) => {
            const status = installResults[tool.name];
            return (
              <div
                key={tool.name}
                className="flex items-center justify-between rounded-md border p-3"
              >
                <div>
                  <span className="font-medium text-sm">{tool.name}</span>
                  <span className="ml-2 text-xs text-muted-foreground">
                    {tool.category}
                  </span>
                </div>
                <div className="flex items-center gap-2">
                  {status === "done" && (
                    <span className="text-xs text-green-600 dark:text-green-400 flex items-center gap-1">
                      <svg className="w-3.5 h-3.5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                        <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M5 13l4 4L19 7" />
                      </svg>
                      Installed
                    </span>
                  )}
                  {status === "error" && (
                    <span className="text-xs text-red-600 dark:text-red-400 flex items-center gap-1">
                      <svg className="w-3.5 h-3.5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                        <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M6 18L18 6M6 6l12 12" />
                      </svg>
                      Failed
                    </span>
                  )}
                  {status === "pending" && installing === tool.name && (
                    <span className="text-xs text-blue-600 dark:text-blue-400 flex items-center gap-1">
                      <svg className="w-3.5 h-3.5 animate-spin" fill="none" viewBox="0 0 24 24">
                        <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" />
                        <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z" />
                      </svg>
                      Installing...
                    </span>
                  )}
                  {status === "pending" && installing !== tool.name && (
                    <span className="text-xs text-muted-foreground">Waiting...</span>
                  )}
                  {status === "skipped" && (
                    <span className="text-xs text-muted-foreground">Not installable</span>
                  )}
                </div>
              </div>
            );
          })}
        </div>

        {/* Install log */}
        {installLog.length > 0 && (
          <div className="space-y-1">
            <button
              type="button"
              onClick={() => setShowInstallLog(!showInstallLog)}
              className="text-xs text-muted-foreground hover:text-foreground transition-colors flex items-center gap-1"
            >
              <span className={`transition-transform ${showInstallLog ? "rotate-90" : ""}`}>
                &#9654;
              </span>
              Install log ({installLog.length} lines)
            </button>
            {showInstallLog && (
              <div className="max-h-48 overflow-y-auto rounded border bg-muted/30 p-2 font-mono text-xs">
                {installLog.map((line, i) => (
                  <div
                    key={i}
                    className={`whitespace-pre-wrap ${line.startsWith("---") ? "font-semibold text-foreground mt-2" : ""}`}
                  >
                    {line}
                  </div>
                ))}
                <div ref={logEndRef} />
              </div>
            )}
          </div>
        )}

        {/* Footer buttons */}
        <div className="flex gap-2 pt-2">
          {isInstalling && (
            <Button variant="outline" onClick={cancelInstall}>
              Cancel
            </Button>
          )}
          {isResults && (
            <>
              <Button onClick={() => onConfirm(config)} className="flex-1">
                Proceed to Scan
              </Button>
              {failed > 0 && (
                <Button variant="outline" onClick={retryFailed}>
                  Retry Failed
                </Button>
              )}
              <Button
                variant="ghost"
                onClick={() => {
                  setInstallPhase("tools");
                  setInstallLog([]);
                  setShowInstallLog(true);
                  setInstallResults({});
                }}
              >
                Back
              </Button>
            </>
          )}
        </div>
      </div>
    );
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-[90vw] sm:w-[1100px] max-h-[90vh] overflow-y-auto">
        <TooltipProvider delayDuration={300}>
        <DialogHeader>
          <DialogTitle>
            {installPhase === "installing"
              ? "Installing Tools..."
              : installPhase === "results"
                ? "Installation Complete"
                : configOnly
                  ? "Scan Configuration"
                  : "Confirm Scan"}
          </DialogTitle>
          <DialogDescription>
            {installPhase === "installing"
              ? "Installing missing tools. You can proceed to scan when ready."
              : installPhase === "results"
                ? "Review results and proceed to scan."
                : configOnly
                  ? "Configure tools and AI assessment for this collection."
                  : "Review detected languages and tools before starting the scan."}
          </DialogDescription>
        </DialogHeader>

        {loading ? (
          <div className="py-8 text-center text-sm text-muted-foreground">
            Detecting repository contents...
          </div>
        ) : installPhase !== "tools" ? (
          renderInstallStep()
        ) : (
          <div className="space-y-6 pt-2">
            {/* Repo Summary */}
            {repoSummary.length > 0 && (
              <div className="space-y-3">
                <Label className="text-base font-semibold">Repositories</Label>
                <div className="space-y-2">
                  {repoSummary.map((repo) => (
                    <div
                      key={repo.repo_id}
                      className="rounded-md border p-3 space-y-2"
                    >
                      <div className="flex items-center justify-between">
                        <span className="font-medium text-sm">
                          {repo.repo_name}
                        </span>
                        <span className="text-xs text-muted-foreground">
                          {repo.total_files} files total
                        </span>
                      </div>
                      {repo.languages &&
                        Object.keys(repo.languages).length > 0 && (
                          <div className="flex flex-wrap gap-1.5">
                            {Object.entries(repo.languages)
                              .sort(([, a], [, b]) => b - a)
                              .map(([lang, count]) => (
                                <span
                                  key={lang}
                                  className={`inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-xs font-medium ${getLangColor(lang)}`}
                                >
                                  {lang}
                                  <span className="opacity-70">
                                    {count} files
                                  </span>
                                </span>
                              ))}
                          </div>
                        )}
                      <div className="flex flex-wrap items-center gap-4 text-xs text-muted-foreground">
                        <span>{repo.source_files} source</span>
                        <span>{repo.test_files} tests</span>
                        <span>{repo.total_files - repo.source_files - repo.test_files} other</span>
                        {repo.frameworks?.length > 0 && (
                          <span>
                            Frameworks: {repo.frameworks.join(", ")}
                          </span>
                        )}
                        {repo.branches?.length > 0 && (
                          <span className="flex items-center gap-1.5 ml-auto">
                            <span className="text-muted-foreground">Branch:</span>
                            <select
                              value={config.branch_overrides?.[repo.repo_id] || repo.current_branch || repo.default_branch}
                              onChange={(e) => {
                                const branch = e.target.value;
                                const overrides = { ...(config.branch_overrides ?? {}) };
                                if (branch === (repo.current_branch || repo.default_branch)) {
                                  delete overrides[repo.repo_id];
                                } else {
                                  overrides[repo.repo_id] = branch;
                                }
                                setConfig({ ...config, branch_overrides: overrides });
                              }}
                              className="rounded border bg-background px-1.5 py-0.5 text-xs"
                            >
                              {repo.branches.map((b) => (
                                <option key={b} value={b}>
                                  {b}
                                  {b === repo.current_branch ? " (current)" : ""}
                                  {b === repo.default_branch && b !== repo.current_branch ? " (default)" : ""}
                                </option>
                              ))}
                            </select>
                          </span>
                        )}
                      </div>
                    </div>
                  ))}
                </div>
              </div>
            )}

            {/* Tool Selection */}
            <div className="space-y-3">
              <div className="flex items-center justify-between">
                <Label className="text-base font-semibold">
                  Tools
                  {tools.length > 0 && (
                    <span className="ml-2 text-xs font-normal text-muted-foreground">
                      ({tools.filter((t) => !isDisabled(t.name)).length}/{tools.length} selected)
                    </span>
                  )}
                </Label>
                {tools.length > 0 && (
                  <div className="flex gap-2 text-xs">
                    <button
                      type="button"
                      onClick={() => toggleTools(tools.map((t) => t.name), true)}
                      className="text-primary hover:underline"
                    >
                      Select all
                    </button>
                    <span className="text-muted-foreground">|</span>
                    <button
                      type="button"
                      onClick={() => toggleTools(tools.map((t) => t.name), false)}
                      className="text-muted-foreground hover:underline"
                    >
                      Deselect all
                    </button>
                  </div>
                )}
              </div>
              {tools.length === 0 ? (
                <p className="text-sm text-muted-foreground">
                  No tools detected. Add repositories first.
                </p>
              ) : (
                Object.entries(groupByCategory(tools))
                  .sort(([a], [b]) => a.localeCompare(b))
                  .map(([category, categoryTools]) => (
                    <div key={category} className="space-y-1.5">
                      <div className="flex items-center gap-2">
                        <input
                          type="checkbox"
                          checked={allEnabled(categoryTools)}
                          ref={(el) => { if (el) el.indeterminate = someEnabled(categoryTools); }}
                          onChange={(e) =>
                            toggleTools(categoryTools.map((t) => t.name), e.target.checked)
                          }
                          className="rounded border-muted-foreground"
                        />
                        <p className="text-xs font-medium text-muted-foreground uppercase tracking-wider">
                          {category}
                        </p>
                      </div>
                      <div className="grid gap-1 sm:grid-cols-2">
                        {categoryTools.map((tool) => renderToolCheckbox(tool))}
                      </div>
                    </div>
                  ))
              )}
            </div>

            {/* AI Assessment */}
            <div className="space-y-3 border-t pt-4">
              <label className="flex items-center gap-2 cursor-pointer">
                <input
                  type="checkbox"
                  checked={config.ai_enabled ?? false}
                  onChange={(e) =>
                    setConfig({ ...config, ai_enabled: e.target.checked })
                  }
                  className="rounded border-muted-foreground"
                />
                <span className="text-base font-semibold">AI Assessment</span>
              </label>
              <p className="text-xs text-muted-foreground">
                Enrich findings with contextual scoring, fix suggestions, and a
                scan summary.
                {config.ai_enabled && (
                  <> Using <span className="font-medium text-foreground">{config.ai_engine || "claude-code"}</span>
                  {config.ai_model ? ` (${config.ai_model})` : ""} — configure in <a href="/settings" className="underline hover:text-foreground">Settings</a>.</>
                )}
              </p>
            </div>

            {/* Actions */}
            <div className="flex gap-2 pt-2">
              <Button onClick={handleConfirm} className="flex-1">
                {configOnly
                  ? "Save Configuration"
                  : getMissingTools().length > 0
                    ? "Install Tools & Scan"
                    : "Start Scan"}
              </Button>
              <Button
                variant="outline"
                onClick={() => onOpenChange(false)}
              >
                Cancel
              </Button>
            </div>
          </div>
        )}
        </TooltipProvider>
      </DialogContent>
    </Dialog>
  );
}
