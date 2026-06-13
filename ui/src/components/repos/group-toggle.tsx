// Group-by toggle for /repos. The current value lives in the URL as
// `?group=<value>` so a deep-link reproduces the same layout. Rendered
// as shadcn <Tabs>.
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";

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
    <Tabs value={value} onValueChange={(v) => onChange(v as RepoGroupBy)}>
      <TabsList className="h-8 p-0.5">
        {OPTIONS.map((opt) => (
          <TabsTrigger
            key={opt.value}
            value={opt.value}
            className="h-7 px-2.5 text-xs"
          >
            {opt.label}
          </TabsTrigger>
        ))}
      </TabsList>
    </Tabs>
  );
}
