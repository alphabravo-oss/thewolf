import { parseFrameworks } from "@/lib/parse-frameworks";

export function FrameworksChips({ raw }: { raw: string | null | undefined }) {
  const items = parseFrameworks(raw);
  if (items.length === 0) {
    return <span className="text-muted-foreground">—</span>;
  }
  return (
    <div className="flex flex-wrap gap-1.5">
      {items.map((name) => (
        <span
          key={name}
          className="inline-flex h-6 items-center rounded-md border border-border bg-muted/30 px-2 text-xs text-foreground/80"
        >
          {name}
        </span>
      ))}
    </div>
  );
}
