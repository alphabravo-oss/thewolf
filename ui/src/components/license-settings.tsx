// Settings → License: edition and evaluation ceiling, entitlements, and the
// install flow (a dialog; validate before install).
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { toast } from "sonner";
import { api } from "@/lib/api";
import { SettingsCard, SettingsTable } from "@/components/settings/shared";
import { Button } from "@/components/ui/button";
import { ErrorState } from "@/components/ui/empty-state";
import { FormDialog } from "@/components/ui/form-dialog";
import { StatusBadge } from "@/components/ui/status-badge";
import { shortRev, useVersion } from "@/lib/edition";
import { safeErrorMessage } from "@/lib/safe-display";

type EditionInfo = {
  edition?: string;
  modules?: string[];
  entitlements?: Record<string, boolean>;
  mcp?: { enabled?: boolean };
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
  const qc = useQueryClient();
  const [installing, setInstalling] = useState(false);
  const [blob, setBlob] = useState("");
  const editionQ = useQuery({
    queryKey: ["edition"],
    queryFn: async () => (await api.get<EditionInfo>("/edition")).data,
  });
  const licenseQ = useQuery({
    queryKey: ["license"],
    queryFn: async () => (await api.get<LicenseInfo>("/license")).data,
  });
  const close = (open: boolean) => {
    setInstalling(open);
    if (!open) {
      setBlob("");
      validate.reset();
      install.reset();
    }
  };
  const validate = useMutation({
    mutationFn: async () => (await api.post<LicenseInfo>("/license/validate", { license: blob })).data,
    onError: (e) => toast.error(safeErrorMessage(e, "Validate failed")),
  });
  const install = useMutation({
    mutationFn: async () => (await api.post<LicenseInfo>("/license/install", { license: blob })).data,
    onSuccess: () => {
      toast.success("License installed");
      close(false);
      qc.invalidateQueries({ queryKey: ["license"] });
      qc.invalidateQueries({ queryKey: ["edition"] });
    },
  });

  const versionQ = useVersion();
  const edition = editionQ.data;
  const version = versionQ.data?.community?.version || versionQ.data?.version;
  const commit = versionQ.data?.community?.commit || versionQ.data?.commit;
  const license = licenseQ.data;
  const entitlements = Object.entries(edition?.entitlements ?? {})
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([name, granted]) => ({ name, granted }));

  if (editionQ.isPending || licenseQ.isPending) return <p role="status" className="text-sm text-muted-foreground">Loading license…</p>;
  if (editionQ.isError || licenseQ.isError) return <ErrorState title="Could not load license" onRetry={() => { void editionQ.refetch(); void licenseQ.refetch(); }} />;

  return (
    <div className="space-y-4">
      <SettingsCard
        title="Edition"
        description={
          <>
            {versionQ.data?.product ?? license?.product ?? "Wolf Community"} · {versionQ.data?.edition ?? edition?.edition ?? license?.edition ?? "community"}
          </>
        }
        actions={<Button size="sm" onClick={() => setInstalling(true)}>Install license</Button>}
      >
        {version && version !== "unknown" && (
          <p className="font-mono text-xs text-muted-foreground">
            v{version.replace(/^v/, "")}{commit && commit !== "unknown" ? ` · ${shortRev(commit)}` : ""}
          </p>
        )}
        {versionQ.data?.overlay ? (
          <p className="font-mono text-xs text-muted-foreground">
            Enterprise overlay {shortRev(versionQ.data.overlay.version || versionQ.data.overlay.commit)}
          </p>
        ) : null}
        <p className="text-sm text-muted-foreground">
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
          <p className="font-mono text-xs text-muted-foreground">modules: {edition.modules.join(", ")}</p>
        ) : null}
        {edition?.mcp ? <p className="text-xs text-muted-foreground">MCP {edition.mcp.enabled ? "enabled" : "off"}</p> : null}
      </SettingsCard>

      <SettingsCard title="Entitlements" description="What this binary and license grant." flush>
        <SettingsTable
          query={{ data: entitlements, isLoading: false, isError: false, refetch: () => undefined }}
          keyExtractor={(e) => e.name}
          persistKey="wolf-license-entitlements"
          searchPlaceholder="Search entitlements..."
          empty={{ title: "None reported" }}
          columns={[
            { key: "name", header: "Capability", sortAccessor: (e) => e.name, accessor: (e) => <span className="font-mono text-xs">{e.name}</span> },
            {
              key: "state", header: "State", sortAccessor: (e) => (e.granted ? "granted" : "denied"),
              accessor: (e) => <StatusBadge status={e.granted ? "active" : "neutral"} label={e.granted ? "Granted" : "Denied"} size="sm" />,
            },
          ]}
        />
      </SettingsCard>

      <FormDialog
        open={installing}
        onOpenChange={close}
        title="Install license"
        description="Paste or upload a commercial license file. Community binaries reject install and never store the blob. Wolf Enterprise verifies signed licenses."
        submitLabel="Install"
        presentation="sheet"
        pending={install.isPending}
        dirty={!!blob}
        submitDisabled={!blob.trim()}
        error={install.isError ? safeErrorMessage(install.error, "Install failed") : undefined}
        onSubmit={() => install.mutate()}
      >
        <input
          type="file"
          aria-label="License file"
          className="block text-xs"
          onChange={(e) => {
            const file = e.target.files?.[0];
            if (!file) return;
            void file.text().then(setBlob).catch(() => toast.error("Could not read license file"));
          }}
        />
        <textarea
          aria-label="License contents"
          value={blob}
          onChange={(e) => setBlob(e.target.value)}
          rows={8}
          className="w-full rounded-md border border-input bg-background px-3 py-2 font-mono text-xs"
          placeholder="-----BEGIN LICENSE-----"
          spellCheck={false}
        />
        <div className="flex flex-wrap items-center gap-3">
          <Button type="button" variant="outline" size="sm" disabled={!blob.trim() || validate.isPending} onClick={() => validate.mutate()}>
            {validate.isPending ? "Validating…" : "Validate"}
          </Button>
          {validate.data ? (
            <p className="text-xs text-muted-foreground">
              {validate.data.reason ?? (validate.data.valid ? "Valid" : "Not valid on this binary")}
            </p>
          ) : null}
        </div>
      </FormDialog>
    </div>
  );
}
