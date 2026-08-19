// Registration. Mirrors login.tsx; redirects to / after success.
import { createFileRoute, useNavigate, redirect } from "@tanstack/react-router";
import { useForm } from "@tanstack/react-form";
import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { toast } from "sonner";
import {
  AuthShell,
  AuthField,
  AuthSubmit,
  PasswordInput,
  authInputCls,
} from "@/components/auth-shell";
import { api, hasSession } from "@/lib/api";
import type { AuthResponse } from "@/lib/types";

export const Route = createFileRoute("/register")({
  beforeLoad: async () => {
    if (await hasSession()) throw redirect({ to: "/" });
  },
  component: RegisterPage,
});

interface AuthSettings {
  registration_enabled: boolean;
  has_users: boolean;
}

function RegisterPage() {
  const navigate = useNavigate();
  const [submitting, setSubmitting] = useState(false);
  const authSettings = useQuery({
    queryKey: ["auth-settings"],
    queryFn: async () => (await api.get<AuthSettings>("/auth/settings")).data,
  });

  const form = useForm({
    defaultValues: { email: "", password: "", passwordConfirm: "" },
    onSubmit: async ({ value }) => {
      if (value.password !== value.passwordConfirm) {
        toast.error("Passwords don't match");
        return;
      }
      setSubmitting(true);
      try {
        // Only send the fields the API knows about.
        await api.post<AuthResponse>("/auth/register", {
          email: value.email,
          password: value.password,
        });
        toast.success("Welcome to Wolf");
        navigate({ to: "/" });
      } catch (e) {
        toast.error(e instanceof Error ? e.message : "Registration failed");
      } finally {
        setSubmitting(false);
      }
    },
  });

  const registrationDisabled =
    authSettings.data?.has_users && !authSettings.data.registration_enabled;

  return (
    <AuthShell>
      <div className="space-y-1.5">
        <h2 className="text-2xl font-semibold tracking-tight">Create your account</h2>
        <p className="text-sm text-muted-foreground">Get started with The Wolf.</p>
      </div>

      {registrationDisabled ? (
        <div className="space-y-4">
          <p className="text-sm text-muted-foreground">
            Registration is disabled. Ask an administrator to create an account from
            Settings → Users.
          </p>
          <a
            href="/login"
            className="block text-center w-full h-10 leading-10 rounded-lg bg-foreground text-background text-sm font-medium hover:opacity-90"
          >
            Back to sign in
          </a>
        </div>
      ) : (
        <form
          onSubmit={(e) => {
            e.preventDefault();
            form.handleSubmit();
          }}
          className="space-y-4"
        >
          <form.Field name="email">
            {(field) => (
              <AuthField label="Email">
                <input
                  type="email"
                  name="email"
                  required
                  autoComplete="email"
                  placeholder="you@example.com"
                  value={field.state.value}
                  onChange={(e) => field.handleChange(e.target.value)}
                  className={authInputCls}
                />
              </AuthField>
            )}
          </form.Field>
          <form.Field name="password">
            {(field) => (
              <AuthField
                label="Password"
                hint={
                  <span className="text-[11px] text-muted-foreground">At least 12 characters.</span>
                }
              >
                <PasswordInput
                  name="password"
                  required
                  autoComplete="new-password"
                  minLength={12}
                  value={field.state.value}
                  onChange={(e) => field.handleChange(e.target.value)}
                />
              </AuthField>
            )}
          </form.Field>
          <form.Field name="passwordConfirm">
            {(field) => {
              const pw = form.getFieldValue("password");
              const mismatch = field.state.value.length > 0 && field.state.value !== pw;
              return (
                <AuthField
                  label="Confirm password"
                  hint={
                    mismatch ? (
                      <span className="text-[11px] text-status-error">Passwords don't match.</span>
                    ) : undefined
                  }
                >
                  <PasswordInput
                    name="passwordConfirm"
                    required
                    autoComplete="new-password"
                    minLength={12}
                    value={field.state.value}
                    onChange={(e) => field.handleChange(e.target.value)}
                    invalid={mismatch}
                  />
                </AuthField>
              );
            }}
          </form.Field>
          <AuthSubmit
            type="submit"
            disabled={submitting || authSettings.isLoading}
            loading={submitting}
          >
            {submitting ? "Creating account…" : "Create account"}
          </AuthSubmit>
        </form>
      )}

      <div className="text-center text-xs text-muted-foreground">
        Have an account?{" "}
        <a href="/login" className="text-foreground hover:underline">
          Sign in
        </a>
      </div>
    </AuthShell>
  );
}
