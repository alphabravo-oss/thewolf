// Theme provider — ported from astronomer/frontend/src/lib/theme.tsx so both
// consoles share one theme contract: a three-way `light | dark | system`
// preference, persisted under a namespaced key, applied by toggling the `dark`
// class on <html> (which is what globals.css keys its `@custom-variant dark`
// off) alongside `color-scheme` for native form controls and scrollbars.
//
// This replaces next-themes. The behaviour that matters at call sites is
// identical, minus the SSR machinery Wolf never needed as a pure SPA.
import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";

export type Theme = "light" | "dark" | "system";

// Namespaced key — NEVER bare `theme`: other applications may share the same
// origin and parse the bare `theme` key. Values are stored raw
// ("light" | "dark" | "system") so prefs written by next-themes survive the
// swap away from it.
export const THEME_STORAGE_KEY = "wolf-theme";

interface ThemeContextValue {
  theme: Theme;
  /** The concrete theme in effect — `system` resolved against the OS. */
  resolvedTheme: "light" | "dark";
  setTheme: (theme: Theme) => void;
}

const ThemeContext = createContext<ThemeContextValue>({
  theme: "dark",
  resolvedTheme: "dark",
  setTheme: () => {},
});

function readStoredTheme(): Theme {
  try {
    const stored = localStorage.getItem(THEME_STORAGE_KEY);
    return stored === "light" || stored === "dark" || stored === "system"
      ? stored
      : "dark";
  } catch {
    return "dark";
  }
}

function prefersDark(): boolean {
  return (
    typeof window !== "undefined" &&
    window.matchMedia("(prefers-color-scheme: dark)").matches
  );
}

export function resolveTheme(theme: Theme): "light" | "dark" {
  if (theme === "system") return prefersDark() ? "dark" : "light";
  return theme;
}

function applyTheme(theme: Theme) {
  const dark = resolveTheme(theme) === "dark";
  document.documentElement.classList.toggle("dark", dark);
  document.documentElement.style.colorScheme = dark ? "dark" : "light";
}

export function ThemeProvider({ children }: { children: ReactNode }) {
  const [theme, setThemeState] = useState<Theme>(readStoredTheme);
  const [systemDark, setSystemDark] = useState(prefersDark);

  const setTheme = useCallback((next: Theme) => {
    setThemeState(next);
    try {
      localStorage.setItem(THEME_STORAGE_KEY, next);
    } catch {
      // Storage unavailable (private mode/quota): theme still applies in-session.
    }
  }, []);

  useEffect(() => {
    applyTheme(theme);
    const mq = window.matchMedia("(prefers-color-scheme: dark)");
    const onChange = () => {
      setSystemDark(mq.matches);
      if (theme === "system") applyTheme("system");
    };
    mq.addEventListener("change", onChange);
    return () => mq.removeEventListener("change", onChange);
  }, [theme]);

  const value = useMemo(
    () => ({
      theme,
      resolvedTheme: (theme === "system"
        ? systemDark
          ? "dark"
          : "light"
        : theme) as "light" | "dark",
      setTheme,
    }),
    [theme, systemDark, setTheme],
  );

  return <ThemeContext.Provider value={value}>{children}</ThemeContext.Provider>;
}

export function useTheme(): ThemeContextValue {
  return useContext(ThemeContext);
}
