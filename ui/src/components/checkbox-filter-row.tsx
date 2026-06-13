// A horizontal row of checkbox "pills" for filtering a list by a facet
// (severity, category, etc). Model-agnostic: the parent decides what
// "checked" means and what a toggle does, so this works whether the
// parent tracks an included-set or an excluded-set.
import { cn } from "@/lib/utils";

export function CheckboxFilterRow({
  label,
  options,
  isChecked,
  onToggle,
  counts,
}: {
  label: string;
  options: string[];
  isChecked: (value: string) => boolean;
  onToggle: (value: string) => void;
  // Optional per-option count, shown in parentheses.
  counts?: Map<string, number>;
}) {
  if (options.length === 0) return null;
  return (
    <div className="flex flex-wrap items-center gap-1.5">
      <span className="text-muted-foreground mr-0.5">{label}:</span>
      {options.map((opt) => {
        const checked = isChecked(opt);
        const count = counts?.get(opt);
        return (
          <button
            key={opt}
            type="button"
            onClick={() => onToggle(opt)}
            aria-pressed={checked}
            className={cn(
              "h-7 px-2 rounded-md border transition inline-flex items-center gap-1.5",
              checked
                ? "bg-primary/15 border-primary/40 text-foreground"
                : "border-muted/40 text-muted-foreground hover:bg-muted/30",
            )}
          >
            <span
              className={cn(
                "size-3 rounded-[3px] border grid place-items-center text-[9px] leading-none",
                checked
                  ? "bg-primary border-primary text-primary-foreground"
                  : "border-muted-foreground/50",
              )}
            >
              {checked ? "✓" : ""}
            </span>
            {opt}
            {count !== undefined && (
              <span className="tabular-nums opacity-70">({count})</span>
            )}
          </button>
        );
      })}
    </div>
  );
}
