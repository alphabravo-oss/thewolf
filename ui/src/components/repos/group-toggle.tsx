// Group-by toggle for /repos. The current value lives in the URL as
// `?group=<value>` so a deep-link reproduces the same layout. It switches the
// layout of one list, so it is a radio group (not tabs, which need panels).
import { cn } from "@/lib/utils";

export type RepoGroupBy = "none" | "source_type" | "collection" | "language";

const OPTIONS: { value: RepoGroupBy; label: string }[] = [
  { value: "none", label: "Flat" },
  { value: "source_type", label: "Source" },
  { value: "collection", label: "Collection" },
  { value: "language", label: "Language" },
];

interface GroupToggleProps {
  value: RepoGroupBy;
  onChange: (next: RepoGroupBy) => void;
}

export function GroupToggle({ value, onChange }: GroupToggleProps) {
  return (
    <div
      role="radiogroup"
      aria-label="Group repositories by"
      className="inline-flex h-8 items-center rounded-md bg-muted p-0.5 text-muted-foreground"
    >
      {OPTIONS.map((opt) => {
        const checked = opt.value === value;
        return (
          <button
            key={opt.value}
            type="button"
            role="radio"
            aria-checked={checked}
            onClick={() => onChange(opt.value)}
            className={cn(
              "inline-flex h-7 items-center rounded-sm px-2.5 text-xs font-medium transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
              checked && "bg-background text-foreground shadow-sm",
            )}
          >
            {opt.label}
          </button>
        );
      })}
    </div>
  );
}
