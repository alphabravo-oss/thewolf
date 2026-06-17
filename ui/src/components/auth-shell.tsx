// Split-screen auth layout shared by /login and /register. Left: a marketing
// hero (logo, two-tone headline, feature pills, AlphaBravo footer); right: the
// form column. Styled to a refined enterprise dark aesthetic via theme tokens,
// so it still respects light mode. The hero collapses on < lg screens.
import { useState } from "react";
import { ArrowRightIcon, EyeIcon, EyeOffIcon } from "lucide-react";
import { WolfLogo } from "@/components/wolf-logo";

function Brand({ className = "" }: { className?: string }) {
  return (
    <div className={"flex items-center gap-2.5 " + className}>
      <WolfLogo className="size-8" />
      <div className="leading-tight">
        <div className="text-sm font-semibold">The Wolf</div>
        <div className="text-[10px] uppercase tracking-[0.18em] text-muted-foreground">
          by AlphaBravo
        </div>
      </div>
    </div>
  );
}

function Feature({ dot, label }: { dot: string; label: string }) {
  return (
    <span className="inline-flex items-center gap-1.5">
      <span className={"size-1.5 rounded-full " + dot} />
      {label}
    </span>
  );
}

export function AuthShell({ children }: { children: React.ReactNode }) {
  return (
    <div className="min-h-screen grid lg:grid-cols-2 bg-background text-foreground">
      {/* Hero (left) */}
      <div className="relative hidden lg:flex flex-col justify-between p-12 border-r border-border/60 overflow-hidden">
        <div
          aria-hidden
          className="pointer-events-none absolute inset-0 -z-10 opacity-70 [background:radial-gradient(55%_45%_at_25%_0%,color-mix(in_oklab,var(--primary)_16%,transparent),transparent_72%)]"
        />
        <Brand />

        <div className="max-w-md space-y-4">
          <h1 className="text-[2.5rem] font-bold leading-[1.08] tracking-tight">
            Code security &amp;{" "}
            <span className="text-muted-foreground">autonomous fixing</span>
          </h1>
          <p className="text-sm leading-relaxed text-muted-foreground">
            Scan, triage, and remediate findings across every repository from a single
            control plane. Built for security teams operating at scale.
          </p>
        </div>

        <div className="space-y-6">
          <div className="flex flex-wrap gap-x-5 gap-y-2 text-xs text-muted-foreground">
            <Feature dot="bg-emerald-500" label="Multi-scanner SAST" />
            <Feature dot="bg-blue-500" label="SARIF & quality gates" />
            <Feature dot="bg-violet-400" label="Autonomous fixing" />
          </div>
          <div className="text-xs text-muted-foreground">
            Built by{" "}
            <a
              href="https://alphabravo.io"
              target="_blank"
              rel="noreferrer"
              className="text-foreground hover:underline"
            >
              AlphaBravo
            </a>
          </div>
        </div>
      </div>

      {/* Form (right) */}
      <div className="flex items-center justify-center p-6 sm:p-10">
        <div className="w-full max-w-sm space-y-7">
          <Brand className="lg:hidden" />
          {children}
        </div>
      </div>
    </div>
  );
}

// Shared input styling — flat, dark, subtle border + focus ring.
export const authInputCls =
  "w-full h-10 px-3 rounded-lg bg-muted/20 border border-border text-sm placeholder:text-muted-foreground/50 outline-none focus:border-ring focus:ring-2 focus:ring-ring/25 transition-colors";

export function AuthField({
  label,
  children,
  hint,
}: {
  label: string;
  children: React.ReactNode;
  hint?: React.ReactNode;
}) {
  return (
    <label className="block space-y-1.5">
      <span className="text-sm font-medium">{label}</span>
      {children}
      {hint}
    </label>
  );
}

// Full-width primary action — inverted (light on dark) like the reference.
export function AuthSubmit({
  loading,
  children,
  ...props
}: React.ButtonHTMLAttributes<HTMLButtonElement> & { loading?: boolean }) {
  return (
    <button
      {...props}
      className="group w-full h-10 rounded-lg bg-foreground text-background text-sm font-medium hover:opacity-90 disabled:opacity-50 inline-flex items-center justify-center gap-1.5 transition-opacity"
    >
      {children}
      {!loading && (
        <ArrowRightIcon className="size-4 transition-transform group-hover:translate-x-0.5" />
      )}
    </button>
  );
}

// Password input with a show/hide eye toggle, matching the reference.
export function PasswordInput({
  invalid,
  ...props
}: React.InputHTMLAttributes<HTMLInputElement> & { invalid?: boolean }) {
  const [show, setShow] = useState(false);
  return (
    <div className="relative">
      <input
        {...props}
        type={show ? "text" : "password"}
        className={
          authInputCls +
          " pr-10" +
          (invalid ? " border-red-500 focus:border-red-500 focus:ring-red-500/25" : "")
        }
      />
      <button
        type="button"
        tabIndex={-1}
        onClick={() => setShow((s) => !s)}
        aria-label={show ? "Hide password" : "Show password"}
        className="absolute right-2.5 top-1/2 -translate-y-1/2 text-muted-foreground hover:text-foreground"
      >
        {show ? <EyeOffIcon className="size-4" /> : <EyeIcon className="size-4" />}
      </button>
    </div>
  );
}
