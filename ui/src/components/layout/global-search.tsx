// Topbar search input — ported from
// astronomer/frontend/src/components/layout/global-search.tsx.
//
// Intentionally lightweight: typing alone issues no request. The user presses
// Enter to run the search, or "/" to focus the field. Cmd/Ctrl+K stays reserved
// for the command palette.
//
// Wolf has no dedicated /search route, so Enter opens the command palette
// pre-seeded with the query instead of navigating — same gesture, same
// keyboard contract, routed at Wolf's existing surface.
import { useEffect, useRef, useState } from "react";
import { Search } from "lucide-react";
import { useUIStore } from "@/lib/store-ui";
import { cn } from "@/lib/utils";

export function GlobalSearch() {
  const inputRef = useRef<HTMLInputElement>(null);
  const [value, setValue] = useState("");
  const openPalette = useUIStore((s) => s.openPalette);

  // "/" focuses the search input unless the user is already typing in a form
  // control.
  useEffect(() => {
    function onKeydown(e: KeyboardEvent) {
      if (e.key !== "/" || e.metaKey || e.ctrlKey || e.altKey) return;
      const active = document.activeElement;
      const isTypingTarget =
        active instanceof HTMLInputElement ||
        active instanceof HTMLTextAreaElement ||
        active instanceof HTMLSelectElement ||
        active?.getAttribute("contenteditable") === "true";
      if (!isTypingTarget) {
        e.preventDefault();
        inputRef.current?.focus();
        inputRef.current?.select();
      }
    }
    document.addEventListener("keydown", onKeydown);
    return () => document.removeEventListener("keydown", onKeydown);
  }, []);

  return (
    <div className="relative w-full max-w-xs">
      <Search className="absolute left-2.5 top-1/2 -translate-y-1/2 h-3.5 w-3.5 text-muted-foreground pointer-events-none" />
      <input
        ref={inputRef}
        type="text"
        value={value}
        onChange={(e) => setValue(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter") {
            e.preventDefault();
            openPalette();
          }
          if (e.key === "Escape") inputRef.current?.blur();
        }}
        placeholder="Search repos, scans, findings..."
        aria-label="Global search"
        className={cn(
          "w-full h-8 pl-8 pr-12 rounded-md border border-border bg-background text-sm",
          "text-foreground placeholder:text-muted-foreground",
          "focus:outline-none focus:ring-1 focus:ring-ring focus:border-ring",
          "transition-colors",
        )}
      />
      <kbd
        className="absolute right-2 top-1/2 -translate-y-1/2 hidden md:inline-flex items-center gap-0.5
          px-1.5 py-0.5 rounded border border-border text-[10px] text-muted-foreground font-mono pointer-events-none"
      >
        /
      </kbd>
    </div>
  );
}
