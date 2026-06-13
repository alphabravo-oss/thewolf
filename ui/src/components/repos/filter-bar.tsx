// Filter bar for /repos. Each chip is a shadcn DropdownMenu with checkbox
// items. Selected values bind to URL query params (?source=…&collection=…
// &status=…) so the filter state is shareable. Filtering is applied
// client-side in the parent route.
import { ChevronDownIcon, FilterIcon, XIcon } from "lucide-react";
import {
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Button } from "@/components/ui/button";

export type RepoSourceFilter = "local" | "github" | "ssh" | "git";
export type RepoStatusFilter = "clean" | "open-high" | "open-critical" | "none" | "failed";

export interface ReposFilters {
  source: RepoSourceFilter[];
  collection: string[];
  status: RepoStatusFilter[];
}

const SOURCE_OPTIONS: { value: RepoSourceFilter; label: string }[] = [
  { value: "local", label: "Local" },
  { value: "github", label: "GitHub" },
  { value: "ssh", label: "SSH" },
  { value: "git", label: "Git URL" },
];

const STATUS_OPTIONS: { value: RepoStatusFilter; label: string }[] = [
  { value: "clean", label: "Clean" },
  { value: "open-high", label: "Open high" },
  { value: "open-critical", label: "Open critical" },
  { value: "none", label: "No scans" },
  { value: "failed", label: "Last scan failed" },
];

interface CollectionOption {
  id: string;
  name: string;
}

interface FilterBarProps {
  filters: ReposFilters;
  onChange: (next: ReposFilters) => void;
  collections: CollectionOption[];
  // Set of repo IDs currently visible after filtering — used by the
  // "Select all visible" action surfaced when the parent route enables
  // bulk-select mode (see Task B13).
  onSelectAllVisible?: () => void;
  visibleCount?: number;
}

export function FilterBar({
  filters,
  onChange,
  collections,
  onSelectAllVisible,
  visibleCount,
}: FilterBarProps) {
  const totalActive =
    filters.source.length + filters.collection.length + filters.status.length;

  const toggle = <T extends string>(arr: T[], v: T): T[] =>
    arr.includes(v) ? arr.filter((x) => x !== v) : [...arr, v];

  return (
    <div className="flex flex-wrap items-center gap-2">
      <div className="inline-flex items-center gap-1.5 text-xs text-muted-foreground">
        <FilterIcon className="size-3.5" />
        Filter
      </div>

      <ChipMenu
        label="Source"
        count={filters.source.length}
        options={SOURCE_OPTIONS.map((o) => ({ value: o.value, label: o.label }))}
        selected={filters.source}
        onToggle={(v) =>
          onChange({ ...filters, source: toggle(filters.source, v as RepoSourceFilter) })
        }
      />

      <ChipMenu
        label="Collection"
        count={filters.collection.length}
        options={collections.map((c) => ({ value: c.id, label: c.name }))}
        selected={filters.collection}
        onToggle={(v) =>
          onChange({ ...filters, collection: toggle(filters.collection, v) })
        }
        emptyHint="No collections yet"
      />

      <ChipMenu
        label="Last scan"
        count={filters.status.length}
        options={STATUS_OPTIONS.map((o) => ({ value: o.value, label: o.label }))}
        selected={filters.status}
        onToggle={(v) =>
          onChange({ ...filters, status: toggle(filters.status, v as RepoStatusFilter) })
        }
      />

      {totalActive > 0 && (
        <Button
          variant="ghost"
          size="sm"
          className="h-7 gap-1 text-xs text-muted-foreground"
          onClick={() => onChange({ source: [], collection: [], status: [] })}
        >
          <XIcon className="size-3.5" />
          Clear ({totalActive})
        </Button>
      )}

      {onSelectAllVisible && typeof visibleCount === "number" && visibleCount > 0 && (
        <Button
          variant="ghost"
          size="sm"
          className="h-7 gap-1 text-xs"
          onClick={onSelectAllVisible}
        >
          Select all visible ({visibleCount})
        </Button>
      )}
    </div>
  );
}

interface ChipMenuProps {
  label: string;
  count: number;
  options: { value: string; label: string }[];
  selected: string[];
  onToggle: (value: string) => void;
  emptyHint?: string;
}

function ChipMenu({ label, count, options, selected, onToggle, emptyHint }: ChipMenuProps) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button
          variant="outline"
          size="sm"
          className="h-7 gap-1.5 text-xs"
        >
          {label}
          {count > 0 && (
            <span className="inline-flex h-4 min-w-4 items-center justify-center rounded-full bg-primary px-1 text-[10px] font-medium text-primary-foreground">
              {count}
            </span>
          )}
          <ChevronDownIcon className="size-3.5 opacity-60" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="w-56">
        <DropdownMenuLabel className="text-xs">{label}</DropdownMenuLabel>
        <DropdownMenuSeparator />
        {options.length === 0 ? (
          <div className="px-2 py-3 text-xs text-muted-foreground">
            {emptyHint ?? "No options"}
          </div>
        ) : (
          options.map((opt) => (
            <DropdownMenuCheckboxItem
              key={opt.value}
              checked={selected.includes(opt.value)}
              onSelect={(e) => {
                e.preventDefault();
                onToggle(opt.value);
              }}
            >
              {opt.label}
            </DropdownMenuCheckboxItem>
          ))
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
