// Keyboard-shortcuts cheatsheet. Triggered by `?` (when not inside an
// input) and by selecting the entry in the command palette. Lists every
// global binding wolf supports.
import { useEffect } from "react";
import { XIcon } from "lucide-react";
import { useUIStore } from "@/lib/store-ui";

const groups: { title: string; items: { keys: string; label: string }[] }[] = [
  {
    title: "Global",
    items: [
      { keys: "⌘K", label: "Open command palette" },
      { keys: "?", label: "Show this cheatsheet" },
      { keys: "Esc", label: "Close any open overlay" },
    ],
  },
  {
    title: "Navigation",
    items: [
      { keys: "g d", label: "Go to dashboard" },
      { keys: "g c", label: "Go to collections" },
      { keys: "g s", label: "Go to scans" },
      { keys: "g f", label: "Go to findings" },
      { keys: "g x", label: "Go to fixes" },
      { keys: "g l", label: "Go to loops" },
    ],
  },
  {
    title: "Findings table",
    items: [
      { keys: "j / k", label: "Next / previous row" },
      { keys: "Enter", label: "Open finding side-panel" },
      { keys: "x", label: "Toggle row selection" },
      { keys: "Shift+M", label: "Bulk mark selected" },
      { keys: "/", label: "Focus search" },
    ],
  },
];

export function ShortcutsOverlay() {
  const open = useUIStore((s) => s.shortcutsOpen);
  const close = useUIStore((s) => s.closeShortcuts);
  const toggle = useUIStore((s) => s.toggleShortcuts);

  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (e.key === "?" && !isEditableTarget(e.target)) {
        e.preventDefault();
        toggle();
      }
      if (e.key === "Escape" && open) {
        e.preventDefault();
        close();
      }
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open, toggle, close]);

  if (!open) return null;

  return (
    <div
      role="dialog"
      aria-label="Keyboard shortcuts"
      className="fixed inset-0 z-50 grid place-items-center bg-black/50 backdrop-blur-sm animate-fade-in p-4"
      onClick={close}
    >
      <div
        onClick={(e) => e.stopPropagation()}
        className="w-[min(40rem,100%)] glass-card bg-popover/95 shadow-2xl border p-6"
      >
        <div className="flex items-center justify-between mb-4">
          <h2 className="text-lg font-semibold">Keyboard shortcuts</h2>
          <button
            onClick={close}
            className="size-7 grid place-items-center rounded-md hover:bg-muted/50"
            aria-label="Close"
          >
            <XIcon className="size-4" />
          </button>
        </div>
        <div className="grid grid-cols-1 sm:grid-cols-2 gap-x-8 gap-y-4">
          {groups.map((g) => (
            <div key={g.title}>
              <div className="text-xs uppercase tracking-wide text-muted-foreground mb-2">
                {g.title}
              </div>
              <ul className="space-y-1.5">
                {g.items.map((it) => (
                  <li
                    key={it.keys}
                    className="flex items-center justify-between gap-3 text-sm"
                  >
                    <span className="text-foreground/90">{it.label}</span>
                    <kbd className="text-2xs px-1.5 py-0.5 rounded bg-muted/70 text-muted-foreground font-mono">
                      {it.keys}
                    </kbd>
                  </li>
                ))}
              </ul>
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}

function isEditableTarget(t: EventTarget | null): boolean {
  if (!t || !(t instanceof HTMLElement)) return false;
  const tag = t.tagName;
  return (
    tag === "INPUT" ||
    tag === "TEXTAREA" ||
    tag === "SELECT" ||
    t.isContentEditable
  );
}
