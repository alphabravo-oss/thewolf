// Login. Uses TanStack Form for type-safe form state + validation; redirects
// to the original target (or /) on success. Two-step when the account has 2FA.
import { createFileRoute, useNavigate, redirect } from "@tanstack/react-router";
import { useForm } from "@tanstack/react-form";
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

type LoginSearch = { from?: string };

export const Route = createFileRoute("/login")({
  validateSearch: (search): LoginSearch => ({
    from: typeof search.from === "string" ? search.from : undefined,
  }),
  beforeLoad: async () => {
    if (await hasSession()) throw redirect({ to: "/" });
  },
  component: LoginPage,
});

function LoginPage() {
  const navigate = useNavigate();
  const search = Route.useSearch();
  const [submitting, setSubmitting] = useState(false);
  // When the account has 2FA, the password step returns a challenge token and
  // we switch to the code step instead of completing the login.
  const [mfaToken, setMfaToken] = useState<string | null>(null);
  const [code, setCode] = useState("");

  // Route the user after a session is established, honoring forced enrollment.
  function afterLogin(res: AuthResponse) {
    if (res.enrollment_required) {
      toast.message("Set up two-factor authentication to continue");
      navigate({ to: "/settings", search: { tab: "security" } });
      return;
    }
    toast.success("Welcome back");
    navigate({ to: (search.from as "/" | undefined) ?? "/" });
  }

  const form = useForm({
    defaultValues: { email: "", password: "" },
    onSubmit: async ({ value }) => {
      setSubmitting(true);
      try {
        const res = (await api.post<AuthResponse>("/auth/login", value)).data;
        if (res?.mfa_required && res.mfa_token) {
          setMfaToken(res.mfa_token);
          return; // advance to the code step
        }
        afterLogin(res);
      } catch (e) {
        toast.error(e instanceof Error ? e.message : "Login failed");
      } finally {
        setSubmitting(false);
      }
    },
  });

  async function submitCode(e: React.FormEvent) {
    e.preventDefault();
    if (!mfaToken) return;
    setSubmitting(true);
    try {
      const res = (
        await api.post<AuthResponse>("/auth/mfa/login", { mfa_token: mfaToken, code })
      ).data;
      afterLogin(res);
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "That code is not valid");
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <AuthShell>
      {mfaToken ? (
        <>
          <div className="space-y-1.5">
            <h2 className="text-2xl font-semibold tracking-tight">Two-factor authentication</h2>
            <p className="text-sm text-muted-foreground">
              Enter the 6-digit code from your authenticator app, or a recovery code.
            </p>
          </div>
          <form onSubmit={submitCode} className="space-y-4">
            <AuthField label="Authentication code">
              <input
                inputMode="numeric"
                autoComplete="one-time-code"
                autoFocus
                required
                placeholder="123456"
                value={code}
                onChange={(e) => setCode(e.target.value.trim())}
                className={authInputCls + " font-mono tracking-[0.3em]"}
              />
            </AuthField>
            <AuthSubmit type="submit" disabled={submitting || code.length < 6} loading={submitting}>
              {submitting ? "Verifying…" : "Verify"}
            </AuthSubmit>
            <button
              type="button"
              onClick={() => {
                setMfaToken(null);
                setCode("");
              }}
              className="w-full text-center text-xs text-muted-foreground hover:text-foreground"
            >
              Back to sign in
            </button>
          </form>
        </>
      ) : (
        <>
          <div className="space-y-1.5">
            <h2 className="text-2xl font-semibold tracking-tight">Sign in to The Wolf</h2>
            <p className="text-sm text-muted-foreground">
              Enter your credentials to continue.
            </p>
          </div>
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
                <AuthField label="Password">
                  <PasswordInput
                    name="password"
                    required
                    autoComplete="current-password"
                    value={field.state.value}
                    onChange={(e) => field.handleChange(e.target.value)}
                  />
                </AuthField>
              )}
            </form.Field>
            <AuthSubmit type="submit" disabled={submitting} loading={submitting}>
              {submitting ? "Signing in…" : "Sign in"}
            </AuthSubmit>
          </form>
          <div className="text-center text-xs text-muted-foreground">
            Don't have an account?{" "}
            <a href="/register" className="text-foreground hover:underline">
              Register
            </a>
          </div>
        </>
      )}
    </AuthShell>
  );
}
