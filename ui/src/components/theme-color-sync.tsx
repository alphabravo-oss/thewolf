// Keeps the browser-chrome colour (<meta name="theme-color">) in step with the
// active theme. Values are Astronomer's `--background` in each mode: white in
// light, near-black zinc in dark.
import { useEffect } from "react";
import { useTheme } from "@/lib/theme";

const THEME_COLORS = {
  dark: "#09090b",
  light: "#ffffff",
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
