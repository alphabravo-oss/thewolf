// Settings → License & support: edition, what it includes in plain words, the
// license install flow (a dialog; validate before install), the diagnostics
// bundle for support cases, and how to reach us.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { toast } from "sonner";
import { api } from "@/lib/api";
import { SettingsCard } from "@/components/settings/shared";
import { Button } from "@/components/ui/button";
import { ErrorState } from "@/components/ui/empty-state";
import { FormDialog } from "@/components/ui/form-dialog";
import { CheckIcon, MinusIcon } from "lucide-react";
import { ENTERPRISE_INFO_URL, entitled, shortRev, useVersion } from "@/lib/edition";
import { safeErrorMessage } from "@/lib/safe-display";

type EditionInfo = {
  edition?: string;
  licensed?: boolean;
  entitlements?: Record<string, boolean>;
  mcp?: { enabled?: boolean };
};

/** Real Enterprise capabilities, in the words users look for. Stub modules are not listed. */
const INCLUDED: { capability: string; label: string }[] = [
  { capability: "enterprise.identity", label: "Single sign-on (OIDC, SAML, LDAP) and SCIM directory sync" },
  { capability: "enterprise.remediation", label: "Autonomous AI fixing with pull requests" },
  { capability: "enterprise.integrations", label: "SIEM and ticketing destinations" },
  { capability: "enterprise.support", label: "Support with a diagnostics bundle" },
];

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
  const diagnostics = useMutation({
    mutationFn: async () => (await api.get<unknown>("/enterprise/diagnostics")).data,
    onSuccess: (data) => {
      const url = URL.createObjectURL(new Blob([JSON.stringify(data, null, 2)], { type: "application/json" }));
      const a = Object.assign(document.createElement("a"), { href: url, download: `wolf-diagnostics-${new Date().toISOString().slice(0, 10)}.json` });
      a.click();
      URL.revokeObjectURL(url);
    },
    onError: (e) => toast.error(safeErrorMessage(e, "Could not collect diagnostics")),
  });

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
          {/* The Enterprise binary is the entitlement (/edition licensed); /license only reports an installed license file. */}
          {edition?.licensed || license?.commercial_license
            ? "Licensed. This Enterprise build includes its commercial entitlements; no license file is needed."
            : "No commercial license. Community scanning, findings, and local auth stay available."}
        </p>
        {license?.data_intact === false ? (
          <p className="text-xs text-status-error">License state must not conceal customer data.</p>
        ) : (
          <p className="text-xs text-muted-foreground">
            Expired or invalid licenses never delete or hide data
            {license?.community_fallback ? " · Community operations remain available" : ""}.
          </p>
        )}
        {edition?.mcp ? <p className="text-xs text-muted-foreground">MCP {edition.mcp.enabled ? "enabled" : "off"}</p> : null}
      </SettingsCard>

      <SettingsCard title="What's included" label="What's included" description="Scanning, findings, reports, users and the API are included in every edition.">
        <ul className="space-y-2 text-sm">
          {INCLUDED.map(({ capability, label }) => {
            const on = entitled(edition, capability);
            return (
              <li key={capability} className="flex items-start gap-2">
                {on ? <CheckIcon aria-hidden className="mt-0.5 size-4 shrink-0 text-status-success" /> : <MinusIcon aria-hidden className="mt-0.5 size-4 shrink-0 text-muted-foreground" />}
                <span className={on ? "" : "text-muted-foreground"}>{label}<span className="sr-only">{on ? " (included)" : " (not included)"}</span></span>
              </li>
            );
          })}
        </ul>
      </SettingsCard>

      <SettingsCard
        title="Support"
        label="Support"
        description="Attach a diagnostics bundle to a support case. It holds the edition, runtime and settings, with secrets masked; no findings or source code."
        actions={
          entitled(edition, "enterprise.support") ? (
            <Button size="sm" variant="outline" disabled={diagnostics.isPending} onClick={() => diagnostics.mutate()}>
              {diagnostics.isPending ? "Collecting…" : "Download diagnostics bundle"}
            </Button>
          ) : null
        }
      >
        <a href={ENTERPRISE_INFO_URL} target="_blank" rel="noreferrer" className="text-sm font-medium text-primary underline underline-offset-4 hover:no-underline">
          Contact AlphaBravo
        </a>
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
