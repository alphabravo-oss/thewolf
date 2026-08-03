import { ArrowRightIcon, ChevronsLeftIcon } from "lucide-react";
import { Button } from "@/components/ui/button";

export function CursorNavigation({
  currentCursor,
  nextCursor,
  loading,
  label,
  onCursorChange,
}: {
  currentCursor?: string;
  nextCursor?: string;
  loading: boolean;
  label: string;
  onCursorChange: (cursor?: string) => void;
}) {
  if (!currentCursor && !nextCursor) return null;
  return (
    <nav
      aria-label={`${label} pagination`}
      className="flex flex-wrap items-center justify-between gap-2 border-t border-border/50 px-4 py-3"
    >
      <Button
        type="button"
        size="sm"
        variant="outline"
        disabled={!currentCursor || loading}
        onClick={() => onCursorChange(undefined)}
      >
        <ChevronsLeftIcon aria-hidden="true" /> First page
      </Button>
      <span className="text-xs text-muted-foreground" aria-live="polite">
        {loading
          ? "Loading page…"
          : currentCursor
            ? "Cursor page loaded"
            : "First page"}
      </span>
      <Button
        type="button"
        size="sm"
        variant="outline"
        disabled={!nextCursor || loading}
        onClick={() => onCursorChange(nextCursor)}
      >
        Next page <ArrowRightIcon aria-hidden="true" />
      </Button>
    </nav>
  );
}
