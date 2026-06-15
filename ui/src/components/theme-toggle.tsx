// Dark/light theme toggle. Uses next-themes (which manages the `dark`/`light`
// class on <html> + localStorage persistence). Styled as a sidebar nav-item
// so it sits cleanly in the footer alongside Settings / Sign out.
import { useEffect, useState } from "react";
import { useTheme } from "next-themes";
import { SunIcon, MoonIcon } from "lucide-react";

export function ThemeToggle() {
  const { resolvedTheme, setTheme } = useTheme();
  const [mounted, setMounted] = useState(false);

  // resolvedTheme is undefined until mounted; guard to avoid a hydration flash.
  useEffect(() => setMounted(true), []);

  const isDark = resolvedTheme === "dark";

  return (
    <button
      type="button"
      onClick={() => setTheme(isDark ? "light" : "dark")}
      className="nav-item w-full text-left"
      aria-label={isDark ? "Switch to light mode" : "Switch to dark mode"}
      title={isDark ? "Switch to light mode" : "Switch to dark mode"}
    >
      {mounted && isDark ? <SunIcon /> : <MoonIcon />}
      <span className="truncate">
        {mounted ? (isDark ? "Light mode" : "Dark mode") : "Theme"}
      </span>
    </button>
  );
}
