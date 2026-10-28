import { Select, SelectTrigger, SelectValue, SelectContent, SelectItem } from "@/components/ui/select";
import { useState } from "react";
import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useDebouncedValue } from "@tanstack/react-pacer";
import type { PaginationState, SortingState } from "@tanstack/react-table";
import { toast } from "sonner";
import { api } from "@/lib/api";
import { DataTable, type Column } from "@/components/ui/data-table";
import { Section } from "@/components/ui/section";

type Metric = { covered: number; total: number };
type Requirement = { metric: string; minimum: number; scope?: string; path_prefix?: string };
type Rule = { id: string; action: string; test_coverage?: Requirement; [key: string]: unknown };
type Policy = { id: string; name: string; scope: string; scope_id: string; mode: string; rules_json: string; updated_at?: string };
type CoverageData = {
  status: string; scan_id: string; repo_id: string; commit_sha: string; file_count: number; formats: string[];
  scope_note: string; policy: Policy;
  checks: { rule_id: string; metric: string; scope: string; path_prefix?: string; minimum: number; actual?: number; status: string; reason: string }[] | null;
  report: { imported_at: string; metrics: Record<string, Metric>; metric_files: Record<string, number>; sources: { format: string }[]; excluded_paths: string[]; changed?: { base_commit_sha: string; metric: Metric; changed_lines: number } } | null;
};
type CoverageFile = { path: string; metrics: Record<string, Metric>; uncovered_lines: number[]; uncovered_count: number };
const metrics = ["lines", "branches", "statements", "functions"];
const inputClass = "h-9 w-full min-w-0 rounded-md border border-border bg-background px-3 text-sm";
const buttonClass = "min-h-9 rounded-md border border-border px-3 py-2 text-sm hover:bg-muted/50 disabled:opacity-50";
const EMPTY_FILES: CoverageFile[] = [];

function percent(metric?: Metric) {
  return !metric ? "Not measured" : metric.total === 0 ? "Not applicable" : `${(100 * metric.covered / metric.total).toFixed(1)}%`;
}

export function RepositoryTestCoverage({ scanId, repoId, editable }: { scanId: string; repoId: string; editable: boolean }) {
  const qc = useQueryClient();
  const [metric, setMetric] = useState("lines");
  const [scope, setScope] = useState("overall");
  const [search, setSearch] = useState("");
  const [debouncedSearch] = useDebouncedValue(search, { wait: 250 });
  const [pagination, setPagination] = useState<PaginationState>({ pageIndex: 0, pageSize: 25 });
  const [sorting, setSorting] = useState<SortingState>([{ id: "coverage", desc: false }]);
  const summary = useQuery({ queryKey: ["test-coverage", scanId], queryFn: async () => (await api.get<CoverageData>(`/scans/${scanId}/test-coverage`)).data });
  const files = useQuery({
    queryKey: ["test-coverage-files", scanId, scope, metric, debouncedSearch, pagination, sorting],
    enabled: summary.data?.status === "measured" && summary.data.repo_id === repoId,
    placeholderData: keepPreviousData,
    queryFn: async () => {
      const p = new URLSearchParams({ q: debouncedSearch, scope, metric, page: String(pagination.pageIndex + 1), per_page: String(pagination.pageSize), sort: sorting[0]?.id ?? "coverage", order: sorting[0]?.desc ? "desc" : "asc" });
      const r = await api.get<CoverageFile[]>(`/scans/${scanId}/test-coverage/files?${p}`);
      return { rows: r.data, total: r.meta?.total ?? 0 };
    },
  });
  async function refresh() {
    await Promise.all([qc.invalidateQueries({ queryKey: ["test-coverage", scanId] }), qc.invalidateQueries({ queryKey: ["test-coverage-files", scanId] }), qc.invalidateQueries({ queryKey: ["scan", scanId, "gate"] })]);
  }
  if (summary.isLoading) return <p role="status" className="text-sm text-muted-foreground">Loading test coverage…</p>;
  if (summary.isError) return <p role="alert" className="text-sm">Could not load test coverage. <button className="underline" onClick={() => void summary.refetch()}>Retry test coverage</button></p>;
  const data = summary.data;
  if (!data || data.repo_id !== repoId) return <p role="alert">This scan does not belong to this repository.</p>;
  const report = data.report;
  const columns: Column<CoverageFile>[] = [
    { key: "path", header: "Source file", accessor: (f) => <span className="break-all font-mono text-xs">{f.path}</span> },
    { key: "coverage", header: `${metric[0].toUpperCase()}${metric.slice(1)} covered`, accessor: (f) => <span className="whitespace-nowrap tabular-nums">{percent(f.metrics[metric])}{f.metrics[metric]?.total > 0 && <span className="block text-xs text-muted-foreground">{f.metrics[metric].covered.toLocaleString()} / {f.metrics[metric].total.toLocaleString()}</span>}</span> },
    { key: "uncovered", header: "Uncovered lines", sortable: false, accessor: (f) => !("lines" in f.metrics) ? <span className="text-muted-foreground">Not measured</span> : f.uncovered_count === 0 ? "None" : <Section variant="plain" className="max-w-64" triggerClassName="text-sm text-foreground" title={`${f.uncovered_count.toLocaleString()} lines`}><p className="mt-2 break-words text-xs">{f.uncovered_lines.join(", ")}{f.uncovered_count > f.uncovered_lines.length && " … (first 100 shown)"}</p></Section> },
  ];
  return <section aria-label="Automated test coverage" className="space-y-6">
    <div className="space-y-1"><h2 className="text-lg font-semibold">Automated test coverage</h2><p className="text-sm text-muted-foreground">{data.scope_note}</p><p className="break-all text-xs text-muted-foreground">Commit: <code>{data.commit_sha || "Not resolved yet"}</code>{report && ` · Imported ${new Date(report.imported_at).toLocaleString()}`}</p></div>
    {data.status === "source_mismatch" && <p role="alert" className="rounded-lg border border-status-error p-3 text-sm text-status-error">This report does not match the scanned source. Import coverage from the exact clean commit before using it for a coverage requirement.</p>}
    {report ? <>
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-4">{metrics.map((name) => <div key={name} className="rounded-lg border border-border p-4"><h3 className="text-sm capitalize text-muted-foreground">{name}</h3><p className="mt-2 text-2xl font-semibold tabular-nums">{percent(report.metrics[name])}</p><p className="mt-1 text-xs text-muted-foreground">{report.metrics[name] ? `${report.metrics[name].covered.toLocaleString()} of ${report.metrics[name].total.toLocaleString()} · ${report.metric_files[name]} of ${data.file_count} reported files` : "Not provided by the test tooling"}</p></div>)}</div>
      <p className="text-xs text-muted-foreground">{data.file_count.toLocaleString()} reported source files · {Array.from(new Set(report.sources.map((s) => s.format))).join(", ")}</p>
      {report.changed && <div className="rounded-lg border border-border p-4"><h3 className="font-medium">Changed-code coverage</h3><p className="mt-1 text-xl font-semibold">{percent(report.changed.metric)}</p><p className="mt-1 break-all text-xs text-muted-foreground">{report.changed.metric.covered} of {report.changed.metric.total} measured changed lines · Base {report.changed.base_commit_sha.slice(0, 12)} · CI diff-cover report</p><p className="mt-1 text-xs text-muted-foreground">Changed lines absent from the coverage report are not measured.</p></div>}
      {report.excluded_paths.length > 0 && <Section variant="plain" className="text-sm" triggerClassName="text-foreground" title={`Reported exclusions (${report.excluded_paths.length})`}><ul className="mt-2 space-y-1">{report.excluded_paths.map((p, i) => <li key={i} className="break-all font-mono text-xs">{p}</li>)}</ul></Section>}
    </> : <div className="rounded-lg border border-dashed border-border p-6"><h3 className="font-semibold">Not measured</h3><p className="mt-2 text-sm text-muted-foreground">Run automated tests with coverage enabled in CI, then import the report for this commit. Test-file estimates do not satisfy coverage requirements.</p></div>}
    {(data.checks?.length ?? 0) > 0 && <section aria-label="Coverage policy results" className="space-y-2"><h3 className="font-semibold">Coverage requirements</h3>{data.checks!.map((c) => <div key={c.rule_id} className="rounded-lg border border-border p-3"><div className="flex flex-wrap items-center gap-2 text-sm"><strong className={c.status === "pass" ? "text-status-success" : c.status === "warn" ? "text-status-warning" : c.status === "not_applicable" ? "text-muted-foreground" : "text-status-error"}>{c.status === "incomplete" ? "Evidence missing" : c.status.replaceAll("_", " ")}</strong><span>{c.scope === "changed" ? "Changed code" : "Overall"} · {c.metric} ≥ {c.minimum}%{c.path_prefix && ` · ${c.path_prefix}`}</span></div><p className="mt-1 text-sm text-muted-foreground">{c.reason}</p></div>)}</section>}
    {report && <section aria-label="Coverage by file" className="space-y-3"><h3 className="font-semibold">Coverage by file</h3><div className="flex flex-wrap items-end gap-3"><label className="min-w-48 flex-1 text-sm">Search source files<input className={`${inputClass} mt-1`} value={search} onChange={(e) => { setSearch(e.target.value); setPagination((p) => ({ ...p, pageIndex: 0 })); }} /></label><label className="text-sm">Metric<Select value={String(metric)} onValueChange={(value) => { setMetric((value === '__empty__' ? '' : value)); setPagination((p) => ({ ...p, pageIndex: 0 })); }}><SelectTrigger className={`${inputClass} mt-1`} aria-label="Coverage metric"><SelectValue /></SelectTrigger><SelectContent>{metrics.map((m) =><SelectItem key={m} value={String(m)}>{m}</SelectItem>)}</SelectContent></Select></label>{report.changed && <label className="text-sm">Code scope<Select value={String(scope)} onValueChange={(value) => { setScope((value === '__empty__' ? '' : value)); setPagination((p) => ({ ...p, pageIndex: 0 })); }}><SelectTrigger className={`${inputClass} mt-1`} aria-label="Code scope"><SelectValue /></SelectTrigger><SelectContent><SelectItem value={"overall"}>All reported files</SelectItem><SelectItem value={"changed"}>Changed code</SelectItem></SelectContent></Select></label>}</div><DataTable data={files.data?.rows ?? EMPTY_FILES} columns={columns} keyExtractor={(f) => f.path} searchable={false} loading={files.isLoading || files.isPlaceholderData} isError={files.isError} onRetry={() => void files.refetch()} serverSide={{ rowCount: files.data?.total ?? 0, sorting, onSortingChange: (next) => { setSorting(next); setPagination((p) => ({ ...p, pageIndex: 0 })); }, pagination, onPaginationChange: setPagination }} emptyMessage="No source files match this view." /></section>}
    {editable && <>
      <CoverageImport key={scanId} scanId={scanId} commit={data.commit_sha} formats={data.formats} onSaved={refresh} />
      <CoveragePolicy key={`${data.policy.id}:${data.policy.updated_at}:${data.policy.rules_json}`} policy={data.policy} repoId={repoId} onSaved={refresh} />
    </>}
  </section>;
}

type Draft = { name: string; format: string; content: string; strip_prefix: string; path_prefix: string };
function detectFormat(text: string) {
  if (/^mode: (set|count|atomic)/.test(text)) return "go";
  if (/^SF:/m.test(text)) return "lcov";
  if (text.includes("<CoverageSession")) return "opencover";
  if (text.includes("<report") && text.includes("<sourcefile")) return "jacoco";
  if (text.includes("<lineToCover")) return "sonar";
  if (text.includes("<coverage") && text.includes("<class")) return "cobertura";
  if (text.includes("<coverage") && text.includes("<project")) return "clover";
  if (text.includes('"statementMap"')) return "istanbul";
  if (text.includes('"coverage"')) return "simplecov";
  return "";
}

function CoverageImport({ scanId, commit, formats, onSaved }: { scanId: string; commit: string; formats: string[]; onSaved: () => Promise<void> }) {
  const [drafts, setDrafts] = useState<Draft[]>([]);
  const [testedCommit, setTestedCommit] = useState("");
  const [exclusions, setExclusions] = useState("");
  const [error, setError] = useState("");
  const mutation = useMutation({ mutationFn: () => api.put(`/scans/${scanId}/test-coverage`, { commit_sha: testedCommit.trim(), reports: drafts.map(({ name: _name, ...d }) => d), excluded_paths: exclusions.split("\n").map((v) => v.trim()).filter(Boolean) }), onSuccess: async () => { setDrafts([]); toast.success("Measured coverage imported"); await onSaved(); }, onError: (e) => setError(e instanceof Error ? e.message : "Import failed") });
  async function selectFiles(files: FileList | null) {
    setError("");
    const selected = Array.from(files ?? []);
    if (selected.length > 64 || selected.reduce((sum, f) => sum + f.size, 0) > 20 * 1024 * 1024) { setError("Select up to 64 reports totaling no more than 20 MiB."); return; }
    try { setDrafts(await Promise.all(selected.map(async (f) => { const content = await f.text(); return { name: f.name, content, format: detectFormat(content), strip_prefix: "", path_prefix: "" }; }))); } catch { setError("Could not read the selected reports."); }
  }
  function edit(index: number, field: keyof Draft, value: string) { setDrafts((rows) => rows.map((r, i) => i === index ? { ...r, [field]: value } : r)); }
  return <Section title="Import coverage reports"><form className="mt-1 space-y-4" onSubmit={(e) => { e.preventDefault(); setError(""); mutation.mutate(); }}>
    <p className="text-sm text-muted-foreground">Import reports generated by tests of this exact commit. An import replaces the complete coverage bundle for this scan. Merge overlapping test shards with your coverage tool first.</p>
    <label className="block text-sm">Coverage report files<input aria-label="Coverage report files" type="file" multiple className="mt-2 block w-full min-w-0 text-sm" onChange={(e) => void selectFiles(e.target.files)} /></label>
    {drafts.map((d, i) => <fieldset key={`${d.name}:${i}`} className="grid min-w-0 gap-3 rounded-md border border-border p-3 sm:grid-cols-3"><legend className="max-w-full break-all px-1 text-sm">{d.name}</legend><label className="text-sm">Report format<Select required value={String(d.format)} onValueChange={(value) => edit(i, "format", (value === '__empty__' ? '' : value))}><SelectTrigger aria-label={`Format for ${d.name}`} className={`${inputClass} mt-1`}><SelectValue /></SelectTrigger><SelectContent><SelectItem value={String("") || '__empty__'}>Select format</SelectItem>{formats.map((f) =><SelectItem key={f} value={String(f)}>{f}</SelectItem>)}</SelectContent></Select></label><label className="text-sm">Remove path prefix<input aria-label={`Remove prefix for ${d.name}`} className={`${inputClass} mt-1`} placeholder="CI workspace / Go module" value={d.strip_prefix} onChange={(e) => edit(i, "strip_prefix", e.target.value)} /></label><label className="text-sm">Add source root<input aria-label={`Source root for ${d.name}`} className={`${inputClass} mt-1`} placeholder="e.g. frontend or src/main/java" value={d.path_prefix} onChange={(e) => edit(i, "path_prefix", e.target.value)} /></label></fieldset>)}
    <label className="block text-sm">Tested commit SHA<input className={`${inputClass} mt-1 font-mono`} required pattern="([0-9a-f]{40}|[0-9a-f]{64})" placeholder={commit || "Full commit SHA from your test run"} value={testedCommit} onChange={(e) => setTestedCommit(e.target.value)} /></label>
    <label className="block text-sm">Exclusions used by the test tool (one per line)<textarea className="mt-1 block min-h-20 w-full rounded-md border border-border bg-background p-3 text-sm" value={exclusions} onChange={(e) => setExclusions(e.target.value)} /></label>
    {error && <p role="alert" className="text-sm text-status-error">{error}</p>}
    <button className={buttonClass} disabled={!commit || !drafts.length || mutation.isPending}>{mutation.isPending ? "Importing…" : "Import measured coverage"}</button>
    <Section variant="plain" className="text-sm" triggerClassName="text-foreground" title="Automate imports from CI"><pre className="mt-2 max-w-full overflow-x-auto rounded-md bg-muted/50 p-3 text-xs">{`wolf scan import-coverage ${scanId} \\\n  --commit "$TESTED_COMMIT" --format lcov --file coverage/lcov.info\n\nwolf scan gate ${scanId} --fail-exit-code`}</pre><p className="mt-2 text-xs text-muted-foreground">Use --request-file for mixed formats and --diff-cover-file with --base-commit for changed-line coverage. The CLI accepts the same nine formats as this upload.</p></Section>
  </form></Section>;
}

function CoveragePolicy({ policy, repoId, onSaved }: { policy: Policy; repoId: string; onSaved: () => Promise<void> }) {
  const original = JSON.parse(policy.rules_json) as Rule[];
  const [rules, setRules] = useState<Rule[]>(original.filter((r) => r.test_coverage));
  const mutation = useMutation({ mutationFn: () => {
    const body = { name: policy.name, scope: "repo", scope_id: repoId, mode: policy.mode, enabled: true, rules: [...original.filter((r) => !r.test_coverage), ...rules] };
    return policy.scope === "repo" ? api.put(`/policies/${policy.id}`, body) : api.post("/policies", body);
  }, onSuccess: async () => { toast.success("Coverage requirements saved"); await onSaved(); } });
  function edit(index: number, change: Partial<Requirement>) { setRules((rows) => rows.map((r, i) => i === index ? { ...r, test_coverage: { ...r.test_coverage!, ...change } } : r)); }
  return <Section title="Configure coverage requirements"><form className="mt-1 space-y-4" onSubmit={(e) => { e.preventDefault(); mutation.mutate(); }}>
    <p className="text-sm text-muted-foreground">Current policy: {policy.name} ({policy.scope}). Saving creates or updates this repository’s policy and preserves its security rules. Use a source root to set separate requirements for components with different coverage metrics.</p>
    {rules.map((r, i) => <fieldset key={r.id} className="grid min-w-0 gap-3 rounded-md border border-border p-3 sm:grid-cols-2 lg:grid-cols-4"><legend className="px-1 text-sm">Requirement {i + 1}</legend><label className="text-sm">Scope<Select value={String((r.test_coverage?.scope || "overall") ?? '') || '__empty__'} onValueChange={(value) => edit(i, { scope: (value === '__empty__' ? '' : value), ...((value === '__empty__' ? '' : value) === "changed" ? { metric: "lines" } : {}) })}><SelectTrigger aria-label={`Scope for requirement ${i + 1}`} className={`${inputClass} mt-1`}><SelectValue /></SelectTrigger><SelectContent><SelectItem value={"overall"}>Overall</SelectItem><SelectItem value={"changed"}>Changed code</SelectItem></SelectContent></Select></label><label className="text-sm">Metric<Select value={String((r.test_coverage?.metric) ?? '') || '__empty__'} onValueChange={(value) => edit(i, { metric: (value === '__empty__' ? '' : value) })}><SelectTrigger aria-label={`Metric for requirement ${i + 1}`} className={`${inputClass} mt-1`}><SelectValue /></SelectTrigger><SelectContent>{(r.test_coverage?.scope === "changed" ? ["lines"] : metrics).map((m) =><SelectItem key={m} value={String(m)}>{m}</SelectItem>)}</SelectContent></Select></label><label className="text-sm">Minimum (%)<input aria-label={`Minimum for requirement ${i + 1}`} type="number" min="0" max="100" step="0.1" required className={`${inputClass} mt-1`} value={r.test_coverage?.minimum} onChange={(e) => edit(i, { minimum: Number(e.target.value) })} /></label><label className="text-sm">Source root (optional)<input aria-label={`Source root for requirement ${i + 1}`} className={`${inputClass} mt-1`} value={r.test_coverage?.path_prefix || ""} placeholder="All reported files" onChange={(e) => edit(i, { path_prefix: e.target.value })} /></label><button type="button" className={`${buttonClass} sm:col-span-2 lg:col-span-4 justify-self-start`} onClick={() => setRules((rows) => rows.filter((_, index) => index !== i))}>Remove requirement {i + 1}</button></fieldset>)}
    {rules.length === 0 && <p className="text-sm text-muted-foreground">No test coverage minimum is configured.</p>}
    <div className="flex flex-wrap gap-2"><button type="button" className={buttonClass} onClick={() => setRules((rows) => [...rows, { id: `test-coverage-${crypto.randomUUID()}`, action: "fail", test_coverage: { metric: "lines", minimum: 80, scope: "overall" } }])}>Add requirement</button><button className={buttonClass} disabled={mutation.isPending}>{mutation.isPending ? "Saving…" : "Save coverage requirements"}</button></div>
    {mutation.isError && <p role="alert" className="text-sm text-status-error">{mutation.error.message}</p>}
  </form></Section>;
}
