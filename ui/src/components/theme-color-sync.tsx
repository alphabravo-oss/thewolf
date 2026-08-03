import { useEffect } from "react";
import { useTheme } from "next-themes";

const THEME_COLORS = {
  dark: "#0a0c10",
  light: "#f8f9fb",
} as const;

export function ThemeColorSync() {
  const { resolvedTheme } = useTheme();

  useEffect(() => {
    if (resolvedTheme !== "dark" && resolvedTheme !== "light") return;
    document
      .querySelector<HTMLMetaElement>('meta[data-theme-color-sync="true"]')
      ?.setAttribute("content", THEME_COLORS[resolvedTheme]);
  }, [resolvedTheme]);

  return null;
}
