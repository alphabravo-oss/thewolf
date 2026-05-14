// `g <letter>` route shortcuts. Listened to once at the app shell level;
// no per-page wiring needed.
//
//   g d → /
//   g c → /collections
//   g s → /scans
//   g f → /findings
//   g x → /fixes
//   g l → /loops
//   g n → /scanners
//   g , → /settings
//
// The map is kept in sync with the bindings shown in shortcuts-overlay.tsx.
import { useEffect } from "react";
import { useNavigate } from "@tanstack/react-router";

type Target = "/" | "/collections" | "/scans" | "/findings" | "/fixes" | "/loops" | "/scanners" | "/settings";

const map: Record<string, Target> = {
  d: "/",
  c: "/collections",
  s: "/scans",
  f: "/findings",
  x: "/fixes",
  l: "/loops",
  n: "/scanners",
  ",": "/settings",
};

export function useRouteShortcuts() {
  const navigate = useNavigate();

  useEffect(() => {
    let pending = false;
    let timer: ReturnType<typeof setTimeout> | null = null;

    function onKey(e: KeyboardEvent) {
      const tgt = e.target as HTMLElement | null;
      const inField =
        tgt &&
        (tgt.tagName === "INPUT" ||
          tgt.tagName === "TEXTAREA" ||
          tgt.tagName === "SELECT" ||
          tgt.isContentEditable);
      if (inField) return;
      if (e.metaKey || e.ctrlKey || e.altKey) return;

      if (e.key === "g" && !pending) {
        pending = true;
        timer = setTimeout(() => {
          pending = false;
        }, 800);
        return;
      }
      if (pending) {
        const dest = map[e.key.toLowerCase()];
        pending = false;
        if (timer) clearTimeout(timer);
        if (dest) {
          e.preventDefault();
          navigate({ to: dest });
        }
      }
    }
    window.addEventListener("keydown", onKey);
    return () => {
      window.removeEventListener("keydown", onKey);
      if (timer) clearTimeout(timer);
    };
  }, [navigate]);
}
