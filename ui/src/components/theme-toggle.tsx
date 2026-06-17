// Dark/light theme toggle. Uses next-themes (which manages the `dark`/`light`
// class on <html> + localStorage persistence). Styled as a sidebar nav-item
// so it sits cleanly in the footer alongside Settings / Sign out.
import { useEffect, useState } from "react";
import { useTheme } from "next-themes";
import { SunIcon, MoonIcon } from "lucide-react";

export function ThemeToggle({ compact = false }: { compact?: boolean }) {
  const { resolvedTheme, setTheme } = useTheme();
  const [mounted, setMounted] = useState(false);

  // resolvedTheme is undefined until mounted; guard to avoid a hydration flash.
  useEffect(() => setMounted(true), []);

  const isDark = resolvedTheme === "dark";
  const label = isDark ? "Switch to light mode" : "Switch to dark mode";

  // Compact icon button for the top bar.
  if (compact) {
    return (
      <button
        type="button"
        onClick={() => setTheme(isDark ? "light" : "dark")}
        className="grid size-9 place-items-center rounded-md text-muted-foreground hover:text-foreground hover:bg-muted/50 outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
        aria-label={label}
        title={label}
      >
        {mounted && isDark ? <SunIcon className="size-4" /> : <MoonIcon className="size-4" />}
      </button>
    );
  }

  return (
    <button
      type="button"
      onClick={() => setTheme(isDark ? "light" : "dark")}
      className="nav-item w-full text-left"
      aria-label={label}
      title={label}
    >
      {mounted && isDark ? <SunIcon /> : <MoonIcon />}
      <span className="truncate">
        {mounted ? (isDark ? "Light mode" : "Dark mode") : "Theme"}
      </span>
    </button>
  );
}
