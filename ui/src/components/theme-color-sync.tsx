// Keeps the browser-chrome colour (<meta name="theme-color">) in step with the
// active theme. Values are `--background` in each mode (globals.css). The meta
// tag needs a literal, and the `.dark` class is not applied yet when this
// effect runs, so the tokens cannot be read back from the stylesheet.
import { useEffect } from "react";
import { useTheme } from "@/lib/theme";

/* eslint-disable no-restricted-syntax */
const THEME_COLORS = {
  dark: "#0e1114",
  light: "#eef0f3",
} as const;
/* eslint-enable no-restricted-syntax */

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
