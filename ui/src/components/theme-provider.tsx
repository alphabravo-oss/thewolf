"use client";

import { createContext, useContext, useEffect, useState, useSyncExternalStore } from "react";

type Theme = "light" | "dark" | "system";

interface ThemeContextValue {
  theme: Theme;
  setTheme: (theme: Theme) => void;
}

const ThemeContext = createContext<ThemeContextValue>({
  theme: "system",
  setTheme: () => {},
});

function getStoredTheme(): Theme {
  if (typeof window === "undefined") return "system";
  return (localStorage.getItem("wolf-theme") as Theme | null) ?? "system";
}

function subscribeToStorage(callback: () => void) {
  window.addEventListener("storage", callback);
  return () => window.removeEventListener("storage", callback);
}

export function ThemeProvider({
  children,
  defaultTheme = "system",
}: {
  children: React.ReactNode;
  defaultTheme?: Theme;
}) {
  const storedTheme = useSyncExternalStore(
    subscribeToStorage,
    getStoredTheme,
    () => defaultTheme,
  );
  const [theme, setTheme] = useState<Theme>(storedTheme);

  useEffect(() => {
    const root = document.documentElement;
    root.classList.remove("light", "dark");

    if (theme === "system") {
      const systemDark = window.matchMedia(
        "(prefers-color-scheme: dark)",
      ).matches;
      root.classList.add(systemDark ? "dark" : "light");
    } else {
      root.classList.add(theme);
    }

    localStorage.setItem("wolf-theme", theme);
  }, [theme]);

  return (
    <ThemeContext.Provider value={{ theme, setTheme }}>
      {children}
    </ThemeContext.Provider>
  );
}

export const useTheme = () => useContext(ThemeContext);
