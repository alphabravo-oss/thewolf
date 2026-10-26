import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { toast } from "sonner";
import { api } from "@/lib/api";
import { canModify, useMe } from "@/lib/me";
import type { Repo, Secret } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Checkbox } from "@/components/ui/checkbox";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { ErrorState, LoadingState } from "@/components/ui/empty-state";

interface Target {
  url: string;
  mode: "baseline" | "full" | "api";
  api_spec_url: string;
  api_format: "openapi" | "graphql";
  auth_secret_id: string;
  revision: number;
  approved_by: string;
  allow_private: boolean;
  active_approved: boolean;
}

export function RepositoryDASTTarget({ repo }: { repo: Repo }) {
  const query = useQuery({ queryKey: ["dast-target", repo.id], queryFn: async () => (await api.get<Target>(`/repos/${repo.id}/dast-target`)).data });
  return <section aria-labelledby="dast-heading" className="glass-card space-y-4 p-5">
    <div><h2 id="dast-heading" className="text-base font-semibold">DAST target</h2><p className="mt-1 text-sm text-muted-foreground">Scan a running application with ZAP and Nuclei. An administrator must authorize the target before scanning.</p></div>
    {query.isPending ? <LoadingState /> : query.isError ? <ErrorState onRetry={() => void query.refetch()} /> : query.data && <TargetForm key={query.data.revision} repo={repo} target={query.data} />}
  </section>;
}

function TargetForm({ repo, target }: { repo: Repo; target: Target }) {
  const me = useMe();
  const qc = useQueryClient();
  const editable = canModify(me.data, repo.user_id);
  const admin = me.data?.role === "admin";
  const [form, setForm] = useState(target);
  const [privateOK, setPrivateOK] = useState(false);
  const [activeOK, setActiveOK] = useState(false);
  const dirty = form.url !== target.url || form.mode !== target.mode || form.api_spec_url !== target.api_spec_url || form.api_format !== target.api_format || form.auth_secret_id !== target.auth_secret_id;
  const secrets = useQuery({ queryKey: ["secrets"], queryFn: async () => (await api.get<Secret[]>("/config/secrets")).data ?? [], enabled: editable });
  const save = useMutation({
    mutationFn: async (approve: boolean) => approve
      ? api.post(`/repos/${repo.id}/dast-target/approve`, { revision: target.revision, allow_private: privateOK, approve_active: activeOK })
      : api.put(`/repos/${repo.id}/dast-target`, { url: form.url.trim(), mode: form.mode, api_spec_url: form.api_spec_url.trim(), api_format: form.api_format, auth_secret_id: form.auth_secret_id, revision: target.revision }),
    onSuccess: (_, approve) => { void qc.invalidateQueries({ queryKey: ["dast-target", repo.id] }); toast.success(approve ? "DAST target approved" : "DAST target saved. Administrator approval is required."); },
    onError: (error) => { toast.error(error instanceof Error ? error.message : "Unable to save DAST target"); },
  });
  const disabled = !editable || save.isPending;
  return <form className="space-y-4" onSubmit={(event) => { event.preventDefault(); if (dirty) save.mutate(false); }}>
    <p role="status" className="text-sm font-medium">{dirty ? "Unsaved changes — saving clears approval" : !target.url ? "Not configured — DAST scanners are skipped" : target.approved_by ? `Approved${target.active_approved ? " for active attacks" : ""}${target.allow_private ? " · Private network allowed" : ""}` : "Awaiting administrator approval — DAST scanners are skipped"}</p>
    <div className="grid gap-4 sm:grid-cols-2">
      <div className="space-y-2 sm:col-span-2"><Label htmlFor="dast-url">Target URL</Label><Input id="dast-url" type="url" placeholder="https://staging.example.com" value={form.url} disabled={disabled} onChange={(e) => setForm({ ...form, url: e.target.value })} aria-describedby="dast-scope" /><p id="dast-scope" className="text-xs text-muted-foreground">Only this origin (scheme, host and port) is reachable. Clear the URL to disable DAST. Metadata and link-local destinations are blocked.</p></div>
      <div className="space-y-2"><Label htmlFor="dast-mode">ZAP mode</Label><Select value={form.mode} disabled={disabled} onValueChange={(mode: Target["mode"]) => setForm({ ...form, mode })}><SelectTrigger id="dast-mode"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="baseline">Baseline — passive (default)</SelectItem><SelectItem value="full">Full — active attacks</SelectItem><SelectItem value="api">API — passive spec import</SelectItem></SelectContent></Select></div>
      {form.mode === "api" && <><div className="space-y-2"><Label htmlFor="dast-format">API format</Label><Select value={form.api_format} disabled={disabled} onValueChange={(api_format: Target["api_format"]) => setForm({ ...form, api_format })}><SelectTrigger id="dast-format"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="openapi">OpenAPI</SelectItem><SelectItem value="graphql">GraphQL</SelectItem></SelectContent></Select></div><div className="space-y-2 sm:col-span-2"><Label htmlFor="dast-spec">API specification URL or GraphQL endpoint</Label><Input id="dast-spec" type="url" required value={form.api_spec_url} disabled={disabled} onChange={(e) => setForm({ ...form, api_spec_url: e.target.value })} /><p className="text-xs text-muted-foreground">Must use the same origin as the target URL.</p></div></>}
      <div className="space-y-2 sm:col-span-2"><Label htmlFor="dast-secret">Auth-header secret</Label><Select value={form.auth_secret_id || "none"} disabled={disabled || secrets.isPending || secrets.isError} onValueChange={(id) => setForm({ ...form, auth_secret_id: id === "none" ? "" : id })}><SelectTrigger id="dast-secret"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="none">No authentication headers</SelectItem>{(secrets.data ?? []).filter((s) => s.key_type === "custom").map((s) => <SelectItem key={s.id} value={s.id}>{s.key_name}</SelectItem>)}</SelectContent></Select><p className="text-xs text-muted-foreground">Use a custom secret owned by the repository owner, containing a JSON object such as {`{"Authorization":"Bearer …"}`}. Manage secrets in <Link to="/account" search={{ section: "secrets" }} className="underline">Account</Link>.</p>{secrets.isError && <p role="alert" className="text-sm text-destructive">Unable to load secrets. <button type="button" className="underline" onClick={() => void secrets.refetch()}>Retry</button></p>}</div>
    </div>
    {form.mode === "full" && <p role="alert" className="rounded-md border border-destructive/40 p-3 text-sm text-destructive">Active scans send attack payloads and can modify or delete application data. Use only targets you are authorized to test. Full mode requires separate administrator approval.</p>}
    <p className="text-xs text-muted-foreground">Baseline and API modes crawl and send requests but do not run ZAP active attack rules. Nuclei runs HTTP vulnerability templates against the approved target.</p>
    {editable && <Button type="submit" disabled={save.isPending || !dirty}>{save.isPending ? "Saving…" : "Save target"}</Button>}
    {admin && target.url && !target.approved_by && !dirty && <div className="space-y-3 rounded-md border border-border p-4">
      <p className="text-sm">Approve only a target you own or have permission to scan.</p>
      <label className="flex items-start gap-2 text-sm"><Checkbox checked={privateOK} onCheckedChange={(v) => setPrivateOK(v === true)} disabled={save.isPending} />Allow this target on a private network</label>
      {target.mode === "full" && <label className="flex items-start gap-2 text-sm"><Checkbox checked={activeOK} onCheckedChange={(v) => setActiveOK(v === true)} disabled={save.isPending} />I explicitly authorize active attacks against this target</label>}
      <Button type="button" disabled={save.isPending || (target.mode === "full" && !activeOK)} onClick={() => save.mutate(true)}>Approve target</Button>
    </div>}
  </form>;
}
