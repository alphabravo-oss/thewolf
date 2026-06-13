// Login. Uses TanStack Form for type-safe form state + validation; redirects
// to the original target (or /) on success.
import { createFileRoute, useNavigate, redirect } from "@tanstack/react-router";
import { useForm } from "@tanstack/react-form";
import { useState } from "react";
import { toast } from "sonner";
import { WolfLogo } from "@/components/wolf-logo";
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

  const form = useForm({
    defaultValues: { email: "", password: "" },
    onSubmit: async ({ value }) => {
      setSubmitting(true);
      try {
        await api.post<AuthResponse>("/auth/login", value);
        toast.success("Welcome back");
        navigate({ to: (search.from as "/" | undefined) ?? "/" });
      } catch (e) {
        toast.error(e instanceof Error ? e.message : "Login failed");
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
          <div className="text-lg font-semibold">Sign in to The Wolf</div>
          <div className="text-[10px] uppercase tracking-wider text-muted-foreground -mt-1">
            by AlphaBravo
          </div>
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
              <Field label="Email">
                <input
                  type="email"
                  name="email"
                  required
                  autoComplete="email"
                  value={field.state.value}
                  onChange={(e) => field.handleChange(e.target.value)}
                  className="w-full h-9 px-3 rounded-md bg-muted/30 border border-border focus:border-ring focus:bg-muted/50 outline-none text-sm"
                />
              </Field>
            )}
          </form.Field>
          <form.Field name="password">
            {(field) => (
              <Field label="Password">
                <input
                  type="password"
                  name="password"
                  required
                  autoComplete="current-password"
                  value={field.state.value}
                  onChange={(e) => field.handleChange(e.target.value)}
                  className="w-full h-9 px-3 rounded-md bg-muted/30 border border-border focus:border-ring focus:bg-muted/50 outline-none text-sm"
                />
              </Field>
            )}
          </form.Field>
          <button
            type="submit"
            disabled={submitting}
            className="w-full h-9 rounded-md bg-primary text-primary-foreground text-sm font-medium hover:opacity-90 disabled:opacity-60"
          >
            {submitting ? "Signing in…" : "Sign in"}
          </button>
        </form>
        <div className="text-center text-xs text-muted-foreground">
          Don't have an account?{" "}
          <a href="/register" className="text-foreground hover:underline">
            Register
          </a>
        </div>
      </div>
    </div>
  );
}

function Field({
  label,
  children,
}: {
  label: string;
  children: React.ReactNode;
}) {
  return (
    <label className="block space-y-1">
      <span className="text-xs text-muted-foreground">{label}</span>
      {children}
    </label>
  );
}
