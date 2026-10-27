// Per-user profile: display name inline, email and password through dialogs
// (both need the current password). Rendered on /account and in Settings.
import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { api } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { FormDialog } from "@/components/ui/form-dialog";
import { Input } from "@/components/ui/input";
import { useMe } from "@/lib/me";
import { safeErrorMessage } from "@/lib/safe-display";
import { SecurityTab } from "./mfa";
import { Field, SettingRow, SettingsCard, TextSetting } from "./shared";

export function AccountTab() {
  const qc = useQueryClient();
  const me = useMe();
  const [dialog, setDialog] = useState<"email" | "password" | null>(null);
  const [email, setEmail] = useState("");
  const [currentPw, setCurrentPw] = useState("");
  const [newPw, setNewPw] = useState("");
  const [confirmPw, setConfirmPw] = useState("");

  const close = () => {
    setDialog(null);
    setEmail("");
    setCurrentPw("");
    setNewPw("");
    setConfirmPw("");
    profile.reset();
    password.reset();
  };
  const profile = useMutation({
    mutationFn: (updates: { display_name?: string; email?: string; current_password?: string }) =>
      api.put("/auth/profile", updates),
    onSuccess: (_, updates) => {
      toast.success("Profile saved");
      if (updates.email) close();
      return qc.invalidateQueries({ queryKey: ["me"] });
    },
    onError: (e, updates) => {
      if (!updates.email) toast.error(safeErrorMessage(e, "Save failed"));
    },
  });
  const password = useMutation({
    mutationFn: () => api.put("/auth/password", { current_password: currentPw, new_password: newPw }),
    onSuccess: () => {
      toast.success("Password updated");
      close();
    },
  });

  if (me.isLoading) return <p role="status" className="text-sm text-muted-foreground">Loading…</p>;
  const pwMismatch = confirmPw.length > 0 && confirmPw !== newPw;
  const emailValid = !!email.trim() && email.trim().toLowerCase() !== me.data?.email && !!currentPw;
  const pwValid = !!currentPw && newPw.length >= 12 && newPw === confirmPw;

  return (
    <SettingsCard title="Profile" description="How you appear in Wolf and how you sign in.">
      <div className="space-y-5">
        <SettingRow label="Display name">
          <TextSetting label="Display name" value={me.data?.display_name ?? ""} disabled={profile.isPending}
            onCommit={(display_name) => profile.mutate({ display_name })} />
        </SettingRow>
        <SettingRow label="Email" help={me.data?.email}>
          <Button type="button" variant="outline" size="sm" onClick={() => setDialog("email")}>Change email</Button>
        </SettingRow>
        <SettingRow label="Password" help="At least 12 characters.">
          <Button type="button" variant="outline" size="sm" onClick={() => setDialog("password")}>Change password</Button>
        </SettingRow>
      </div>

      <FormDialog
        open={dialog === "email"}
        onOpenChange={(o) => !o && close()}
        title="Change email"
        description="Confirm with your current password."
        submitLabel="Save email"
        pending={profile.isPending}
        dirty={!!email || !!currentPw}
        submitDisabled={!emailValid}
        error={profile.isError ? safeErrorMessage(profile.error, "Save failed") : undefined}
        onSubmit={() => profile.mutate({ email: email.trim(), current_password: currentPw })}
      >
        <Field label="New email">
          <Input type="email" value={email} onChange={(e) => setEmail(e.target.value)} autoComplete="email" />
        </Field>
        <Field label="Current password">
          <Input type="password" value={currentPw} onChange={(e) => setCurrentPw(e.target.value)} autoComplete="current-password" />
        </Field>
      </FormDialog>

      <FormDialog
        open={dialog === "password"}
        onOpenChange={(o) => !o && close()}
        title="Change password"
        description="At least 12 characters."
        submitLabel="Update password"
        pending={password.isPending}
        dirty={!!currentPw || !!newPw || !!confirmPw}
        submitDisabled={!pwValid}
        error={password.isError ? safeErrorMessage(password.error, "Update failed") : undefined}
        onSubmit={() => password.mutate()}
      >
        <Field label="Current password">
          <Input type="password" value={currentPw} onChange={(e) => setCurrentPw(e.target.value)} autoComplete="current-password" />
        </Field>
        <Field label="New password">
          <Input type="password" value={newPw} onChange={(e) => setNewPw(e.target.value)} autoComplete="new-password" minLength={12} />
        </Field>
        <Field label="Confirm new password" error={pwMismatch ? "Passwords do not match." : undefined}>
          <Input type="password" value={confirmPw} onChange={(e) => setConfirmPw(e.target.value)} autoComplete="new-password" aria-invalid={pwMismatch} />
        </Field>
      </FormDialog>
    </SettingsCard>
  );
}

/** Settings → Account & MFA: your own sign-in, same as the avatar-menu Account page. */
export function AccountSection() {
  return (
    <div className="space-y-4">
      <AccountTab />
      <SecurityTab />
    </div>
  );
}
