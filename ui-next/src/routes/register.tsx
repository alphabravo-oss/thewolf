// Registration. Mirrors login.tsx; redirects to / after success.
import { createFileRoute, useNavigate, redirect } from "@tanstack/react-router";
import { useForm } from "@tanstack/react-form";
import { useState } from "react";
import { toast } from "sonner";
import { WolfLogo } from "@/components/wolf-logo";
import { api, getToken, setToken } from "@/lib/api";
import type { AuthResponse } from "@/lib/types";

export const Route = createFileRoute("/register")({
  beforeLoad: () => {
    if (getToken()) throw redirect({ to: "/" });
  },
  component: RegisterPage,
});

function RegisterPage() {
  const navigate = useNavigate();
  const [submitting, setSubmitting] = useState(false);

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
        const res = await api.post<AuthResponse>("/auth/register", {
          email: value.email,
          password: value.password,
        });
        setToken(res.data.access_token);
        toast.success("Welcome to Wolf");
        navigate({ to: "/" });
      } catch (e) {
        toast.error(e instanceof Error ? e.message : "Registration failed");
      } finally {
        setSubmitting(false);
      }
    },
  });

  return (
    <div className="min-h-screen grid place-items-center bg-background p-6">
      <div className="w-full max-w-sm glass-card p-7 space-y-5">
        <div className="flex flex-col items-center gap-2">
          <WolfLogo className="size-10" />
          <div className="text-lg font-semibold">Create your Wolf account</div>
        </div>
        <form
          onSubmit={(e) => {
            e.preventDefault();
            form.handleSubmit();
          }}
          className="space-y-3"
        >
          <form.Field name="email">
            {(field) => (
              <label className="block space-y-1">
                <span className="text-xs text-muted-foreground">Email</span>
                <input
                  type="email"
                  name="email"
                  required
                  autoComplete="email"
                  value={field.state.value}
                  onChange={(e) => field.handleChange(e.target.value)}
                  className="w-full h-9 px-3 rounded-md bg-muted/30 border border-border focus:border-ring focus:bg-muted/50 outline-none text-sm"
                />
              </label>
            )}
          </form.Field>
          <form.Field name="password">
            {(field) => (
              <label className="block space-y-1">
                <span className="text-xs text-muted-foreground">Password</span>
                <input
                  type="password"
                  name="password"
                  required
                  autoComplete="new-password"
                  minLength={12}
                  value={field.state.value}
                  onChange={(e) => field.handleChange(e.target.value)}
                  className="w-full h-9 px-3 rounded-md bg-muted/30 border border-border focus:border-ring focus:bg-muted/50 outline-none text-sm"
                />
                <span className="text-[10px] text-muted-foreground">
                  At least 12 characters.
                </span>
              </label>
            )}
          </form.Field>
          <form.Field name="passwordConfirm">
            {(field) => {
              const pw = form.getFieldValue("password");
              const mismatch =
                field.state.value.length > 0 && field.state.value !== pw;
              return (
                <label className="block space-y-1">
                  <span className="text-xs text-muted-foreground">
                    Confirm password
                  </span>
                  <input
                    type="password"
                    name="passwordConfirm"
                    required
                    autoComplete="new-password"
                    minLength={12}
                    value={field.state.value}
                    onChange={(e) => field.handleChange(e.target.value)}
                    aria-invalid={mismatch}
                    className={`w-full h-9 px-3 rounded-md bg-muted/30 border outline-none text-sm focus:bg-muted/50 ${
                      mismatch
                        ? "border-red-500 focus:border-red-500"
                        : "border-border focus:border-ring"
                    }`}
                  />
                  {mismatch && (
                    <span className="text-[10px] text-red-500">
                      Passwords don't match.
                    </span>
                  )}
                </label>
              );
            }}
          </form.Field>
          <button
            type="submit"
            disabled={submitting}
            className="w-full h-9 rounded-md bg-primary text-primary-foreground text-sm font-medium hover:opacity-90 disabled:opacity-60"
          >
            {submitting ? "Creating account…" : "Create account"}
          </button>
        </form>
        <div className="text-center text-xs text-muted-foreground">
          Have an account?{" "}
          <a href="/login" className="text-foreground hover:underline">
            Sign in
          </a>
        </div>
      </div>
    </div>
  );
}
