import { useMutation, useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { toast } from "sonner";
import { api } from "@/lib/api";
import { shortRev, useVersion } from "@/lib/edition";

type EditionInfo = {
  edition?: string;
  modules?: string[];
  entitlements?: Record<string, boolean>;
  mcp?: { enabled?: boolean };
  limits?: {
    repos?: number;
    users?: number;
    workers?: number;
    source?: string;
    enforced?: boolean;
  };
};

type LicenseInfo = {
  edition?: string;
  product?: string;
  commercial_license?: boolean;
  valid?: boolean;
  reason?: string;
  data_intact?: boolean;
  community_fallback?: boolean;
};

export function LicenseTab() {
  const [blob, setBlob] = useState("");
  const editionQ = useQuery({
    queryKey: ["edition"],
    queryFn: async () => (await api.get<EditionInfo>("/edition")).data,
  });
  const licenseQ = useQuery({
    queryKey: ["license"],
    queryFn: async () => (await api.get<LicenseInfo>("/license")).data,
  });

  const validate = useMutation({
    mutationFn: async () =>
      (await api.post<LicenseInfo>("/license/validate", { license: blob })).data,
    onError: (e) =>
      toast.error(e instanceof Error ? e.message : "Validate failed"),
  });
  const install = useMutation({
    mutationFn: async () =>
      (await api.post<LicenseInfo>("/license/install", { license: blob })).data,
    onSuccess: () => toast.success("License installed"),
    onError: (e) =>
      toast.error(e instanceof Error ? e.message : "Install failed"),
  });

  const versionQ = useVersion();
  const edition = editionQ.data;
  const license = licenseQ.data;
  const entitlements = Object.entries(edition?.entitlements ?? {}).sort(
    ([a], [b]) => a.localeCompare(b),
  );

  return (
    <div className="space-y-4">
      <section className="glass-card p-5 space-y-2">
        <h3 className="text-sm font-medium">Edition</h3>
        <p className="text-sm">
          {versionQ.data?.product ?? license?.product ?? "Wolf Community"}
          <span className="text-muted-foreground">
            {" "}
            · {versionQ.data?.edition ?? edition?.edition ?? license?.edition ?? "community"}
          </span>
        </p>
        <p className="text-xs text-muted-foreground font-mono">
          Community v{versionQ.data?.community?.version ?? versionQ.data?.version ?? ""}
          {versionQ.data?.community?.commit ? ` (${shortRev(versionQ.data.community.commit)})` : ""}
        </p>
        {versionQ.data?.overlay ? (
          <p className="text-xs text-muted-foreground font-mono">
            Enterprise overlay {shortRev(versionQ.data.overlay.version || versionQ.data.overlay.commit)}
          </p>
        ) : null}
        <p className="text-xs text-muted-foreground">
          {license?.commercial_license
            ? "A commercial license is active."
            : "No commercial license is installed in this binary. Community scanning, findings, and local auth stay available."}
        </p>
        {license?.data_intact === false ? (
          <p className="text-xs text-status-error">License state must not conceal customer data.</p>
        ) : (
          <p className="text-xs text-muted-foreground">
            Expired or invalid licenses never delete or hide data
            {license?.community_fallback ? " · Community operations remain available" : ""}.
          </p>
        )}
        {edition?.modules && edition.modules.length > 0 ? (
          <p className="text-xs text-muted-foreground font-mono">
            modules: {edition.modules.join(", ")}
          </p>
        ) : null}
        {edition?.mcp ? (
          <p className="text-xs text-muted-foreground">
            MCP {edition.mcp.enabled ? "enabled" : "off"}
          </p>
        ) : null}
        {edition?.limits ? (
          <p className="text-xs text-muted-foreground">
            Evaluation ceiling: {edition.limits.repos} repos, {edition.limits.users}{" "}
            users, {edition.limits.workers} scan worker
            {edition.limits.enforced ? " (enforced)" : " (not enforced)"}
            {edition.limits.source ? ` · ${edition.limits.source}` : ""}
          </p>
        ) : null}
      </section>

      <section className="glass-card p-5 space-y-3">
        <h3 className="text-sm font-medium">Entitlements</h3>
        {entitlements.length === 0 ? (
          <p className="text-sm text-muted-foreground">None reported.</p>
        ) : (
          <ul className="space-y-1">
            {entitlements.map(([name, granted]) => (
              <li
                key={name}
                className="flex items-center justify-between gap-3 text-sm"
              >
                <span className="font-mono text-xs">{name}</span>
                <span
                  className={
                    granted
                      ? "text-xs text-status-success"
                      : "text-xs text-muted-foreground"
                  }
                >
                  {granted ? "granted" : "denied"}
                </span>
              </li>
            ))}
          </ul>
        )}
      </section>

      <section className="glass-card p-5 space-y-3">
        <h3 className="text-sm font-medium">Install license</h3>
        <p className="text-xs text-muted-foreground">
          Paste or upload a commercial license file. Community binaries reject
          install and never store the blob. Wolf Enterprise verifies signed
          licenses.
        </p>
        <input
          type="file"
          className="block text-xs"
          onChange={(e) => {
            const file = e.target.files?.[0];
            if (!file) return;
            void file.text().then(setBlob);
          }}
        />
        <textarea
          value={blob}
          onChange={(e) => setBlob(e.target.value)}
          rows={6}
          className="w-full rounded-md border border-border bg-background px-3 py-2 font-mono text-xs"
          placeholder="-----BEGIN LICENSE-----"
          spellCheck={false}
        />
        <div className="flex flex-wrap gap-2">
          <button
            type="button"
            disabled={validate.isPending}
            onClick={() => validate.mutate()}
            className="h-8 px-3 rounded-md border border-border text-xs hover:bg-muted/40 disabled:opacity-50"
          >
            Validate
          </button>
          <button
            type="button"
            disabled={!blob.trim() || install.isPending}
            onClick={() => install.mutate()}
            className="h-8 px-3 rounded-md border border-border text-xs hover:bg-muted/40 disabled:opacity-50"
          >
            Install
          </button>
        </div>
        {validate.data ? (
          <p className="text-xs text-muted-foreground">
            {validate.data.reason ??
              (validate.data.valid ? "Valid" : "Not valid on this binary")}
          </p>
        ) : null}
      </section>
    </div>
  );
}
