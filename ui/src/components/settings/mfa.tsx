// Per-user two-factor authentication (TOTP): enroll in a dialog, show recovery
// codes once, disable with a code.
import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { api } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { ErrorState } from "@/components/ui/empty-state";
import { FormDialog } from "@/components/ui/form-dialog";
import { Input } from "@/components/ui/input";
import { StatusBadge } from "@/components/ui/status-badge";
import { safeErrorMessage } from "@/lib/safe-display";
import { SettingsCard } from "./shared";

interface MfaStatus {
  enabled: boolean;
  required: boolean;
}
interface MfaSetup {
  otpauth_uri: string;
  secret: string;
  qr: string; // PNG data URI
}

export function SecurityTab() {
  const qc = useQueryClient();
  const status = useQuery({
    queryKey: ["mfa-status"],
    queryFn: async () => (await api.get<MfaStatus>("/auth/mfa/status")).data,
  });
  const [setup, setSetup] = useState<MfaSetup | null>(null);
  const [disabling, setDisabling] = useState(false);
  const [code, setCode] = useState("");
  const [recovery, setRecovery] = useState<string[] | null>(null);
  const closeDialogs = () => {
    setSetup(null);
    setDisabling(false);
    setCode("");
    activate.reset();
    disable.reset();
  };

  const begin = useMutation({
    mutationFn: async () => (await api.post<MfaSetup>("/auth/mfa/setup")).data,
    onSuccess: (d) => {
      setSetup(d);
      setRecovery(null);
      setCode("");
    },
    onError: (e) => toast.error(safeErrorMessage(e, "Could not start setup")),
  });
  const activate = useMutation({
    mutationFn: async () => (await api.post<{ recovery_codes: string[] }>("/auth/mfa/activate", { code })).data,
    onSuccess: (d) => {
      setRecovery(d?.recovery_codes ?? []);
      closeDialogs();
      qc.invalidateQueries({ queryKey: ["mfa-status"] });
      toast.success("Two-factor authentication enabled");
    },
  });
  const disable = useMutation({
    mutationFn: async () => api.post("/auth/mfa/disable", { code }),
    onSuccess: () => {
      closeDialogs();
      qc.invalidateQueries({ queryKey: ["mfa-status"] });
      toast.success("Two-factor authentication disabled");
    },
  });

  if (status.isLoading) return <p role="status" className="text-sm text-muted-foreground">Loading…</p>;
  const current = status.isError ? undefined : status.data;
  const enabled = current?.enabled;
  const required = current?.required;

  return (
    <SettingsCard
      title="Two-factor authentication"
      description="Protect your account with a time-based code from an authenticator app (Google Authenticator, 1Password, Authy, …) in addition to your password."
      actions={
        <StatusBadge status={enabled ? "success" : "neutral"} label={current ? (enabled ? "On" : "Off") : "Unavailable"} size="sm" />
      }
    >
      {!current && (
        <ErrorState
          title="Two-factor status unavailable"
          description="Your two-factor settings could not be checked. Retry to load them."
          onRetry={() => void status.refetch()}
        />
      )}

      {/* One-time recovery codes, shown right after activation. */}
      {recovery && (
        <div className="space-y-2 rounded-md border border-status-warning/30 bg-status-warning/5 p-4">
          <p className="text-sm font-medium text-status-warning">Save your recovery codes</p>
          <p className="text-xs text-muted-foreground">
            Each code works once if you lose your device. Store them somewhere safe — they won't be shown again.
          </p>
          <div className="grid grid-cols-2 gap-1.5 font-mono text-sm">
            {recovery.map((c) => (
              <span key={c} className="rounded bg-muted/40 px-2 py-1 tracking-wider">{c}</span>
            ))}
          </div>
          <Button
            type="button"
            variant="link"
            className="h-auto p-0 text-xs"
            onClick={() => {
              void navigator.clipboard?.writeText(recovery.join("\n"));
              toast.success("Recovery codes copied");
            }}
          >
            Copy all
          </Button>
        </div>
      )}

      {current && !enabled && (
        <Button type="button" onClick={() => begin.mutate()} disabled={begin.isPending}>
          {begin.isPending ? "Starting…" : "Set up two-factor authentication"}
        </Button>
      )}
      {current && enabled &&
        (required ? (
          <p className="text-xs text-muted-foreground">
            Your administrator requires two-factor authentication, so it can't be turned off.
          </p>
        ) : (
          <Button type="button" variant="outline" onClick={() => setDisabling(true)}>Turn off two-factor</Button>
        ))}

      <FormDialog
        open={!!setup}
        onOpenChange={(o) => !o && closeDialogs()}
        title="Set up two-factor authentication"
        description="Scan the QR code with your authenticator app, then enter the 6-digit code to confirm."
        submitLabel="Activate"
        pending={activate.isPending}
        dirty={!!code}
        submitDisabled={code.length < 6}
        error={activate.isError ? safeErrorMessage(activate.error, "That code is not valid") : undefined}
        onSubmit={() => activate.mutate()}
      >
        {setup && (
          <>
            <div className="flex flex-wrap items-start gap-4">
              {/* A QR needs an opaque white quiet zone on every side to scan, in dark theme too. */}
              <div className="shrink-0 rounded-md bg-white p-4">
                <img src={setup.qr} alt="TOTP QR code" className="block size-40" width={160} height={160} />
              </div>
              <div className="min-w-0 flex-1 space-y-1 text-xs text-muted-foreground">
                <p>Can't scan? Enter this secret manually:</p>
                <code className="block break-all rounded bg-muted/40 px-2 py-1 font-mono">{setup.secret}</code>
              </div>
            </div>
            <Input
              inputMode="numeric"
              autoComplete="one-time-code"
              maxLength={6}
              placeholder="123456"
              aria-label="Authentication code"
              value={code}
              onChange={(e) => setCode(e.target.value.replace(/\D/g, ""))}
              className="w-40 font-mono tracking-widest"
            />
          </>
        )}
      </FormDialog>

      <FormDialog
        open={disabling}
        onOpenChange={(o) => !o && closeDialogs()}
        title="Turn off two-factor authentication?"
        description="Enter a current code from your authenticator app (or a recovery code) to confirm."
        submitLabel="Turn off"
        pending={disable.isPending}
        dirty={!!code}
        submitDisabled={code.length < 6}
        error={disable.isError ? safeErrorMessage(disable.error, "That code is not valid") : undefined}
        onSubmit={() => disable.mutate()}
      >
        <Input
          inputMode="numeric"
          autoComplete="one-time-code"
          maxLength={9}
          placeholder="code"
          aria-label="Code to disable"
          value={code}
          onChange={(e) => setCode(e.target.value.trim())}
          className="w-44 font-mono"
        />
      </FormDialog>
    </SettingsCard>
  );
}
