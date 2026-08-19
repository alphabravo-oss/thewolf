// Shared data table — ported from astronomer/frontend/src/components/ui/data-table.tsx.
//
// Astronomer runs TanStack Table v8 (`useReactTable` + `getXRowModel()`
// options). Wolf has v9 installed, which replaced that constructor with
// `useTable` and moved every optional row model into a `tableFeatures()` slot.
// The public props below are unchanged from Astronomer's — the same
// `Column<T>` descriptor, the same toolbar/search/facet/pagination behaviour —
// so pages read identically across the two consoles. Only the engine differs.
//
// v9 notes for anyone diffing this against Astronomer's copy:
//   - features are registered once, at module scope (they must be static)
//   - `table.getState()`      -> `table.state`
//   - `sortingFn`             -> `sortFn`
//   - `VisibilityState`       -> `ColumnVisibilityState`
//   - `getFacetedRowModel()`  -> `facetedRowModel` feature slot
//   - resizing split out of sizing into its own feature
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import type { ReactNode } from "react";
import { useEffect, useMemo, useRef, useState } from "react";
import { useVirtualizer } from "@tanstack/react-virtual";
import {
  useTable,
  tableFeatures,
  columnFacetingFeature,
  columnFilteringFeature,
  columnResizingFeature,
  columnSizingFeature,
  columnVisibilityFeature,
  globalFilteringFeature,
  rowPaginationFeature,
  rowSelectionFeature,
  rowSortingFeature,
  createFacetedRowModel,
  createFacetedUniqueValues,
  createFilteredRowModel,
  createPaginatedRowModel,
  createSortedRowModel,
  type ColumnDef,
  type SortingState,
  type RowSelectionState,
  type ColumnVisibilityState,
  type ColumnFiltersState,
  type PaginationState,
  type ColumnSizingState,
  type Updater,
  type Column as RtColumn,
  type Row as RtRow,
} from "@tanstack/react-table";
import type { Virtualizer } from "@tanstack/react-virtual";
import { useDebouncedValue } from "@tanstack/react-pacer";
import {
  ChevronDown,
  ChevronUp,
  ChevronsUpDown,
  ChevronLeft,
  ChevronRight,
  Search,
  SlidersHorizontal,
  Filter,
  X,
} from "lucide-react";
import { cn } from "@/lib/utils";

// ============================================================
// Feature registration (v9)
//
// v9 tree-shakes on explicit registration: a state slice or method simply does
// not exist unless its feature is listed here. Row-model slots must follow the
// feature they depend on.
//
// Pagination is always registered even though the virtualized branch doesn't
// paginate — a feature set is a static type, so branching it would fork the
// table's type. Virtualized mode instead reads the full sorted row model
// directly and hides the pager, which is the same end state Astronomer reaches
// by omitting the pagination row model.
// ============================================================
const features = tableFeatures({
  columnFilteringFeature,
  globalFilteringFeature,
  rowSortingFeature,
  rowPaginationFeature,
  rowSelectionFeature,
  columnVisibilityFeature,
  columnSizingFeature,
  columnResizingFeature,
  columnFacetingFeature,
  filteredRowModel: createFilteredRowModel(),
  sortedRowModel: createSortedRowModel(),
  paginatedRowModel: createPaginatedRowModel(),
  facetedRowModel: createFacetedRowModel(),
  facetedUniqueValues: createFacetedUniqueValues(),
});

type Features = typeof features;

// ============================================================
// Types
// ============================================================

export interface Column<T> {
  key: string;
  header: string;
  accessor: (row: T) => React.ReactNode;
  sortAccessor?: (row: T) => string | number;
  sortable?: boolean;
  filterable?: boolean;
  hidden?: boolean;
  width?: string;
  align?: "left" | "center" | "right";
  /**
   * When set, renders a faceted multi-select filter for this column in the
   * toolbar. The facet options are derived automatically from the column's
   * `sortAccessor` value (so faceted columns should define a `sortAccessor`
   * that returns the scalar to filter on).
   */
  filter?: { label?: string };
}

interface DataTableProps<T> {
  data: T[];
  columns: Column<T>[];
  keyExtractor: (row: T) => string;
  density?: "compact" | "comfortable";
  searchable?: boolean;
  searchPlaceholder?: string;
  selectable?: boolean;
  onRowClick?: (row: T) => void;
  onSelectionChange?: (selected: T[]) => void;
  bulkActions?: (selected: T[]) => ReactNode;
  pageSize?: number;
  loadingRows?: number;
  emptyMessage?: string;
  loading?: boolean;
  /**
   * When true, the table renders a single distinct error row (styled unlike the
   * empty state) instead of data/loading/empty. Pass `query.isError` here.
   */
  isError?: boolean;
  /** Message shown in the error row. */
  errorMessage?: string;
  /** Optional retry action; when provided, an inline Retry button is shown. */
  onRetry?: () => void;
  toolbar?: ReactNode;
  className?: string;
  /**
   * When set, the user's column-visibility choices are persisted to
   * localStorage under this key and restored on next mount. Omit for
   * ephemeral (non-persisted) tables.
   */
  persistKey?: string;
  /**
   * Opt into interactive column resizing. When false (the default), fixed
   * widths come from each column's `width`. When true, columns become
   * drag-resizable and their pixel sizes are persisted under
   * `dt:<persistKey>:sizing` if `persistKey` is set.
   */
  resizable?: boolean;
  /**
   * Opt into row virtualization for large datasets. When true, the table
   * renders a DIV-based ARIA grid that windows the *full* filtered+sorted row
   * model (pagination is disabled and the footer hidden); only rows in (and
   * near) the viewport are mounted. Search/sort/facet/selection still apply
   * over the full row model.
   *
   * Not compatible with `serverSide` — if both are passed, `serverSide` is
   * ignored.
   */
  virtualized?: boolean;
  /**
   * Opt into server-driven pagination. `data` holds only the current page's
   * rows; the table will not slice further. The caller owns the pagination
   * state and feeds it into its query params so each page is a separate fetch.
   */
  serverSide?: {
    rowCount: number;
    pagination: PaginationState;
    onPaginationChange: (next: PaginationState) => void;
  };
}

const visibilityStorageKey = (persistKey: string) => `dt:${persistKey}:visibility`;
const sizingStorageKey = (persistKey: string) => `dt:${persistKey}:sizing`;

function readPersistedVisibility(persistKey: string | undefined): ColumnVisibilityState | null {
  if (!persistKey || typeof window === "undefined") return null;
  try {
    const raw = window.localStorage.getItem(visibilityStorageKey(persistKey));
    return raw ? (JSON.parse(raw) as ColumnVisibilityState) : null;
  } catch {
    return null;
  }
}

function readPersistedSizing(persistKey: string | undefined): ColumnSizingState | null {
  if (!persistKey || typeof window === "undefined") return null;
  try {
    const raw = window.localStorage.getItem(sizingStorageKey(persistKey));
    return raw ? (JSON.parse(raw) as ColumnSizingState) : null;
  } catch {
    return null;
  }
}

// Sort value for a column: prefer sortAccessor, else the stringified cell
// output, so columns without a sortAccessor still sort sensibly.
function sortValue<T>(col: Column<T>, row: T): string | number {
  if (col.sortAccessor) return col.sortAccessor(row);
  const val = col.accessor(row);
  return val?.toString() ?? "";
}

/**
 * Text a column contributes to the global search.
 *
 * `accessor` returns a ReactNode, and most columns render an element (a link, a
 * badge, a two-line cell) rather than a bare string. Stringifying one of those
 * yields "[object Object]", which matches nothing and — worse — matches
 * *everything* for a query like "object". So: use `sortAccessor` when the
 * column declares one, since that is by definition the column's scalar, and
 * otherwise only accept a primitive `accessor` result.
 */
function searchValue<T>(col: Column<T>, row: T): string {
  if (col.sortAccessor) return String(col.sortAccessor(row) ?? "");
  const val = col.accessor(row);
  if (val == null) return "";
  const t = typeof val;
  return t === "string" || t === "number" || t === "boolean" ? String(val) : "";
}

// A stable empty array — a fresh `[]` fallback each render would invalidate
// every data-dependent row model on every render.
const EMPTY_ROWS: never[] = [];

/**
 * v9 constrains row data to `Record<string, any> | Array<any>`. TypeScript only
 * infers an implicit index signature for type *aliases*, so a plain `interface`
 * row type (which most of Wolf's models are) fails that constraint even though
 * it is structurally fine at runtime.
 *
 * Keeping the public generic as bare `T` — matching Astronomer's signature —
 * and widening to `RowOf<T>` only where a TanStack generic demands it means
 * call sites never have to restate their model as a type alias.
 */
// eslint-disable-next-line @typescript-eslint/no-explicit-any -- mirrors v9's own `RowData = Record<string, any>` constraint; `unknown` would reject every real model.
type RowOf<T> = T & Record<string, any>;

// ============================================================
// DataTable
// ============================================================

export function DataTable<T>({
  data,
  columns,
  keyExtractor,
  density = "comfortable",
  searchable = true,
  searchPlaceholder = "Search...",
  selectable = false,
  onRowClick,
  onSelectionChange,
  bulkActions,
  pageSize = 20,
  loadingRows,
  emptyMessage = "No results found",
  loading = false,
  isError = false,
  errorMessage = "Failed to load — try again",
  onRetry,
  toolbar,
  className,
  persistKey,
  resizable = false,
  virtualized = false,
  serverSide,
}: DataTableProps<T>) {
  // serverSide pagination is incompatible with virtualization (the virtualizer
  // windows a fully-loaded row model), so it is ignored when virtualized.
  const effectiveServerSide = virtualized ? undefined : serverSide;

  // The search input is controlled by `searchInput` (instant) while the filter
  // the table actually applies is the 200ms-debounced copy — typing doesn't
  // re-filter the row model on every keystroke.
  const [searchInput, setSearchInput] = useState("");
  const [globalFilter] = useDebouncedValue(searchInput, { wait: 200 });
  const [sorting, setSorting] = useState<SortingState>([]);
  const [columnFilters, setColumnFilters] = useState<ColumnFiltersState>([]);
  const [rowSelection, setRowSelection] = useState<RowSelectionState>({});
  const [pagination, setPagination] = useState<PaginationState>({ pageIndex: 0, pageSize });
  const [columnVisibility, setColumnVisibility] = useState<ColumnVisibilityState>(() =>
    Object.fromEntries(columns.filter((c) => c.hidden).map((c) => [c.key, false])),
  );
  const [columnSizing, setColumnSizing] = useState<ColumnSizingState>({});
  const [showColumnToggle, setShowColumnToggle] = useState(false);

  // Restore persisted visibility/sizing after mount rather than during the
  // first render, so the initial paint is deterministic.
  useEffect(() => {
    const stored = readPersistedVisibility(persistKey);
    if (stored) setColumnVisibility((prev) => ({ ...prev, ...stored }));
  }, [persistKey]);

  useEffect(() => {
    if (!resizable) return;
    const stored = readPersistedSizing(persistKey);
    if (stored) setColumnSizing((prev) => ({ ...prev, ...stored }));
  }, [persistKey, resizable]);

  const cellPadding = density === "compact" ? "px-3 py-2" : "px-4 py-3";
  const selectPadding = density === "compact" ? "px-3 py-2" : "px-3 py-3";
  const skeletonRows = loadingRows ?? Math.min(pageSize, 8);

  // Lookup of the original Column descriptor by key — used by the global filter
  // without threading metadata through the table's typed column meta.
  const colByKey = useMemo(() => new Map(columns.map((c) => [c.key, c])), [columns]);

  const columnDefs = useMemo<ColumnDef<Features, RowOf<T>, unknown>[]>(
    () =>
      columns.map((col) => ({
        id: col.key,
        accessorFn: (row: RowOf<T>) => sortValue(col, row),
        enableSorting: col.sortable !== false,
        enableHiding: true,
        enableColumnFilter: !!col.filter,
        // Faceted multi-select: keep the row when nothing is selected, else
        // when its stringified value is among the selected facet values.
        filterFn: (row, columnId, value) => {
          const selected = (value as string[]) ?? [];
          return selected.length === 0 || selected.includes(String(row.getValue(columnId)));
        },
        // Numeric sortAccessors sort numerically; everything else by locale.
        // The table negates this for descending, so return the ascending
        // comparison. (v8 called this `sortingFn`.)
        sortFn: (a, b, columnId) => {
          const av = a.getValue(columnId);
          const bv = b.getValue(columnId);
          if (typeof av === "number" && typeof bv === "number") {
            return av === bv ? 0 : av < bv ? -1 : 1;
          }
          return String(av).localeCompare(String(bv));
        },
      })),
    [columns],
  );

  const table = useTable<Features, RowOf<T>>({
    features,
    data: (data ?? EMPTY_ROWS) as RowOf<T>[],
    columns: columnDefs,
    getRowId: keyExtractor,
    state: {
      globalFilter,
      sorting,
      columnFilters,
      rowSelection,
      columnVisibility,
      columnSizing,
      pagination: effectiveServerSide ? effectiveServerSide.pagination : pagination,
    },
    enableColumnResizing: resizable,
    columnResizeMode: "onChange",
    onColumnSizingChange: (updater: Updater<ColumnSizingState>) =>
      setColumnSizing((prev) => {
        const next = typeof updater === "function" ? updater(prev) : updater;
        if (persistKey && typeof window !== "undefined") {
          try {
            window.localStorage.setItem(sizingStorageKey(persistKey), JSON.stringify(next));
          } catch {
            /* best-effort persistence — ignore quota/availability errors */
          }
        }
        return next;
      }),
    manualPagination: !!effectiveServerSide,
    ...(effectiveServerSide ? { rowCount: effectiveServerSide.rowCount } : {}),
    onPaginationChange: (updater: Updater<PaginationState>) => {
      if (effectiveServerSide) {
        const next =
          typeof updater === "function" ? updater(effectiveServerSide.pagination) : updater;
        effectiveServerSide.onPaginationChange(next);
        return;
      }
      setPagination((prev) => (typeof updater === "function" ? updater(prev) : updater));
    },
    enableRowSelection: selectable,
    enableSortingRemoval: false, // 2-state toggle (asc ⇄ desc), never back to unsorted
    sortDescFirst: false, // always start ascending, even for numeric columns
    // Polling tables must not snap back to page 1 on every refetch; the search
    // handler resets the page explicitly instead.
    autoResetPageIndex: false,
    // Global search: any *visible* column whose stringified accessor output
    // contains the query. Ignores columnId and checks all visible cells, so a
    // single match includes the row.
    globalFilterFn: (row, _columnId, filterValue) => {
      const q = String(filterValue ?? "").toLowerCase().trim();
      if (!q) return true;
      return row.getVisibleCells().some((cell) => {
        const original = colByKey.get(cell.column.id);
        if (!original) return false;
        return searchValue(original, row.original).toLowerCase().includes(q);
      });
    },
    // Programmatic setGlobalFilter routes through the same debounce as typing.
    onGlobalFilterChange: (updater: Updater<string>) =>
      setSearchInput((prev) => (typeof updater === "function" ? updater(prev) : updater)),
    onSortingChange: setSorting,
    onColumnFiltersChange: setColumnFilters,
    onColumnVisibilityChange: (updater: Updater<ColumnVisibilityState>) =>
      setColumnVisibility((prev) => {
        const next = typeof updater === "function" ? updater(prev) : updater;
        // Never allow hiding the last visible column.
        const visibleCount = columns.filter((c) => next[c.key] !== false).length;
        if (visibleCount < 1) return prev;
        if (persistKey && typeof window !== "undefined") {
          try {
            window.localStorage.setItem(visibilityStorageKey(persistKey), JSON.stringify(next));
          } catch {
            /* best-effort persistence */
          }
        }
        return next;
      }),
    onRowSelectionChange: (updater: Updater<RowSelectionState>) =>
      setRowSelection((prev) => {
        const next = typeof updater === "function" ? updater(prev) : updater;
        onSelectionChange?.(data.filter((row) => next[keyExtractor(row)]));
        return next;
      }),
  });

  // Snap back to page 1 when the debounced search takes effect.
  useEffect(() => {
    if (table.state.pagination.pageIndex !== 0) table.setPageIndex(0);
  }, [globalFilter, table]);

  // A column is visible unless explicitly toggled off. Derived from the
  // visibility state we own, so the memo deps are exactly what it reads.
  const activeColumns = useMemo(
    () => columns.filter((c) => columnVisibility[c.key] !== false),
    [columns, columnVisibility],
  );

  const facetColumns = activeColumns.filter((c) => c.filter);

  // Virtualized mode windows the full filtered+sorted model; paginated mode
  // takes the current page. (v8 achieved this by omitting the pagination row
  // model entirely, which v9's static feature sets don't allow.)
  const rows = virtualized ? table.getSortedRowModel().rows : table.getRowModel().rows;

  // Header objects keyed by column id — wires each resizable column's drag
  // handle. Only consulted when `resizable` is true.
  const headerByKey = useMemo(
    () => new Map(table.getHeaderGroups()[0]?.headers.map((h) => [h.column.id, h])),
    [table, columnSizing, columnVisibility, columns],
  );
  const selectedRows = table.getSelectedRowModel().rows.map((r) => r.original);
  const filteredCount = table.getFilteredRowModel().rows.length;
  const totalPages = table.getPageCount();
  const page = table.state.pagination.pageIndex;
  const effPageSize = effectiveServerSide ? effectiveServerSide.pagination.pageSize : pageSize;
  const totalRows = effectiveServerSide ? effectiveServerSide.rowCount : filteredCount;

  // ---- Virtualization ----
  const scrollRef = useRef<HTMLDivElement | null>(null);
  const estimateSize = density === "compact" ? 40 : 52;
  const rowVirtualizer = useVirtualizer({
    count: rows.length,
    getScrollElement: () => scrollRef.current,
    estimateSize: () => estimateSize,
    overscan: 12,
  });
  const [focusedRowIndex, setFocusedRowIndex] = useState(-1);

  // Move keyboard focus between virtual rows. Off-screen rows aren't mounted,
  // so scroll the target into view first, then focus it next frame.
  const focusRowAt = (target: number) => {
    if (target < 0 || target >= rows.length) return;
    setFocusedRowIndex(target);
    rowVirtualizer.scrollToIndex(target, { align: "auto" });
    requestAnimationFrame(() => {
      const el =
        scrollRef.current?.querySelector<HTMLElement>(`[data-row-index="${target}"]`) ?? null;
      el?.focus();
    });
  };

  return (
    <div className={cn("space-y-3", className)}>
      {/* Toolbar */}
      <div className="flex items-center justify-between gap-3">
        <div className="flex items-center gap-2 flex-1">
          {searchable && (
            <div className="relative max-w-sm flex-1">
              <Search className="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground" />
              <input
                type="text"
                placeholder={searchPlaceholder}
                value={searchInput}
                onChange={(e) => setSearchInput(e.target.value)}
                aria-label={searchPlaceholder}
                className="w-full h-9 pl-9 pr-8 rounded-md border border-border bg-background text-sm
                  placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring"
              />
              {searchInput && (
                <button
                  type="button"
                  onClick={() => setSearchInput("")}
                  aria-label="Clear search"
                  className="absolute right-2.5 top-1/2 -translate-y-1/2 text-muted-foreground hover:text-foreground"
                >
                  <X className="h-3.5 w-3.5" />
                </button>
              )}
            </div>
          )}
          {facetColumns.map((col) => {
            const column = table.getColumn(col.key);
            if (!column) return null;
            return (
              <FacetedFilter
                key={col.key}
                column={column}
                label={col.filter?.label ?? col.header}
                onChange={() => table.setPageIndex(0)}
              />
            );
          })}
          {toolbar}
        </div>

        <div className="relative">
          <button
            type="button"
            onClick={() => setShowColumnToggle(!showColumnToggle)}
            className="inline-flex items-center gap-1.5 h-9 px-3 rounded-md border border-border
              text-sm text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
          >
            <SlidersHorizontal className="h-4 w-4" />
            Columns
          </button>

          {showColumnToggle && (
            <div className="absolute right-0 top-full mt-1 w-48 rounded-md border border-border bg-popover p-1 shadow-lg z-50">
              {columns.map((col) => {
                const column = table.getColumn(col.key);
                const isVisible = column?.getIsVisible() ?? true;
                return (
                  <label
                    key={col.key}
                    className="flex items-center gap-2 px-2 py-1.5 rounded text-sm hover:bg-accent cursor-pointer"
                  >
                    <input
                      type="checkbox"
                      checked={isVisible}
                      onChange={() => column?.toggleVisibility()}
                      className="rounded border-border text-primary focus:ring-ring"
                    />
                    {col.header}
                  </label>
                );
              })}
            </div>
          )}
        </div>
      </div>

      {selectable && selectedRows.length > 0 && bulkActions ? (
        <div
          className="flex flex-wrap items-center justify-between gap-2 rounded-md border border-border bg-muted/30 px-3 py-2 text-sm"
          aria-live="polite"
        >
          <span className="text-muted-foreground">
            {selectedRows.length} {selectedRows.length === 1 ? "row" : "rows"} selected
          </span>
          <div className="flex flex-wrap items-center gap-2">{bulkActions(selectedRows)}</div>
        </div>
      ) : null}

      {virtualized ? (
        <VirtualizedGrid
          activeColumns={activeColumns}
          table={table}
          rows={rows}
          rowVirtualizer={rowVirtualizer}
          scrollRef={scrollRef}
          totalRows={rows.length}
          selectable={selectable}
          resizable={resizable}
          cellPadding={cellPadding}
          selectPadding={selectPadding}
          rowHeight={estimateSize}
          loading={loading}
          skeletonRows={skeletonRows}
          emptyMessage={emptyMessage}
          isError={isError}
          errorMessage={errorMessage}
          onRetry={onRetry}
          keyExtractor={keyExtractor}
          onRowClick={onRowClick}
          focusedRowIndex={focusedRowIndex}
          setFocusedRowIndex={setFocusedRowIndex}
          focusRowAt={focusRowAt}
        />
      ) : (
        <div className="rounded-lg border border-border overflow-hidden">
          <div className="overflow-x-auto">
            <Table className="w-full text-sm">
              <TableHeader>
                <TableRow className="border-b border-border bg-muted/50">
                  {selectable && (
                    <TableHead className={cn("w-10", selectPadding)}>
                      <input
                        type="checkbox"
                        checked={table.getIsAllPageRowsSelected()}
                        onChange={table.getToggleAllPageRowsSelectedHandler()}
                        aria-label="Select all rows on this page"
                        className="rounded border-border text-primary focus:ring-ring"
                      />
                    </TableHead>
                  )}
                  {activeColumns.map((col) => {
                    const column = table.getColumn(col.key);
                    const sorted = column?.getIsSorted();
                    const header = resizable ? headerByKey.get(col.key) : undefined;
                    return (
                      <TableHead
                        key={col.key}
                        aria-sort={
                          col.sortable !== false
                            ? sorted === "asc"
                              ? "ascending"
                              : sorted === "desc"
                                ? "descending"
                                : "none"
                            : undefined
                        }
                        className={cn(
                          cellPadding,
                          "font-medium text-muted-foreground whitespace-nowrap",
                          resizable && "relative",
                          col.align === "center" && "text-center",
                          col.align === "right" && "text-right",
                          col.sortable !== false && "cursor-pointer select-none hover:text-foreground",
                        )}
                        style={
                          resizable
                            ? { width: column?.getSize() }
                            : col.width
                              ? { width: col.width }
                              : undefined
                        }
                        onClick={() => col.sortable !== false && column?.toggleSorting()}
                      >
                        <div
                          className={cn(
                            "flex items-center gap-1",
                            col.align === "center" && "justify-center",
                            col.align === "right" && "justify-end",
                          )}
                        >
                          {col.header}
                          {col.sortable !== false && (
                            <span className="text-muted-foreground/50">
                              {sorted === "asc" ? (
                                <ChevronUp className="h-3.5 w-3.5" />
                              ) : sorted === "desc" ? (
                                <ChevronDown className="h-3.5 w-3.5" />
                              ) : (
                                <ChevronsUpDown className="h-3 w-3" />
                              )}
                            </span>
                          )}
                        </div>
                        {resizable && header?.column.getCanResize() && (
                          <span
                            role="separator"
                            aria-orientation="vertical"
                            data-resize-handle=""
                            onMouseDown={header.getResizeHandler()}
                            onTouchStart={header.getResizeHandler()}
                            onClick={(e) => e.stopPropagation()}
                            className={cn(
                              "absolute right-0 top-0 h-full w-1 cursor-col-resize select-none touch-none",
                              "bg-transparent hover:bg-border/80",
                              header.column.getIsResizing() && "bg-primary/60",
                            )}
                          />
                        )}
                      </TableHead>
                    );
                  })}
                </TableRow>
              </TableHeader>
              <TableBody>
                {isError ? (
                  <TableRow>
                    <TableCell
                      colSpan={activeColumns.length + (selectable ? 1 : 0)}
                      className="px-4 py-12 text-center"
                    >
                      <div className="flex flex-col items-center gap-3 text-sm text-destructive">
                        <span>{errorMessage}</span>
                        {onRetry && (
                          <button
                            type="button"
                            onClick={onRetry}
                            className="inline-flex items-center gap-1.5 h-8 px-3 rounded-md border border-destructive/50
                              text-destructive hover:bg-destructive/10 transition-colors"
                          >
                            Retry
                          </button>
                        )}
                      </div>
                    </TableCell>
                  </TableRow>
                ) : loading ? (
                  Array.from({ length: skeletonRows }).map((_, i) => (
                    <TableRow key={i} className="border-b border-border last:border-0">
                      {selectable && (
                        <TableCell className={selectPadding}>
                          <div className="h-4 w-4 rounded bg-muted animate-pulse" />
                        </TableCell>
                      )}
                      {activeColumns.map((col) => (
                        <TableCell
                          key={col.key}
                          className={cellPadding}
                          style={resizable ? { width: table.getColumn(col.key)?.getSize() } : undefined}
                        >
                          <div
                            className="h-4 w-24 max-w-full rounded bg-muted animate-pulse"
                            style={{ width: col.width ? `min(100%, ${col.width})` : undefined }}
                          />
                        </TableCell>
                      ))}
                    </TableRow>
                  ))
                ) : rows.length === 0 ? (
                  <TableRow>
                    <TableCell
                      colSpan={activeColumns.length + (selectable ? 1 : 0)}
                      className="px-4 py-12 text-center text-muted-foreground"
                    >
                      {emptyMessage}
                    </TableCell>
                  </TableRow>
                ) : (
                  rows.map((row) => {
                    const key = keyExtractor(row.original);
                    const isSelected = row.getIsSelected();
                    return (
                      <TableRow
                        key={key}
                        className={cn(
                          "border-b border-border last:border-0 transition-colors",
                          onRowClick && "cursor-pointer hover:bg-muted/50",
                          isSelected && "bg-muted/30",
                        )}
                        onClick={() => onRowClick?.(row.original)}
                      >
                        {selectable && (
                          <TableCell className={selectPadding} onClick={(e) => e.stopPropagation()}>
                            <input
                              type="checkbox"
                              checked={isSelected}
                              onChange={row.getToggleSelectedHandler()}
                              aria-label="Select row"
                              className="rounded border-border text-primary focus:ring-ring"
                            />
                          </TableCell>
                        )}
                        {activeColumns.map((col) => (
                          <TableCell
                            key={col.key}
                            className={cn(
                              cellPadding,
                              col.align === "center" && "text-center",
                              col.align === "right" && "text-right",
                            )}
                            style={resizable ? { width: table.getColumn(col.key)?.getSize() } : undefined}
                          >
                            {col.accessor(row.original)}
                          </TableCell>
                        ))}
                      </TableRow>
                    );
                  })
                )}
              </TableBody>
            </Table>
          </div>
        </div>
      )}

      {/* Pagination — hidden in virtualized mode (the virtualizer windows the
          full row model, so there are no pages). */}
      {!virtualized && totalPages > 1 && (
        <div className="flex items-center justify-between text-sm">
          <span className="text-muted-foreground">
            Showing {totalRows === 0 ? 0 : page * effPageSize + 1}-{page * effPageSize + rows.length} of{" "}
            {totalRows.toLocaleString()}
          </span>
          <div className="flex items-center gap-1">
            <button
              type="button"
              onClick={() => table.previousPage()}
              disabled={!table.getCanPreviousPage()}
              aria-label="Previous page"
              className="inline-flex items-center justify-center h-8 w-8 rounded-md border border-border
                text-muted-foreground hover:text-foreground hover:bg-accent disabled:opacity-50
                disabled:pointer-events-none transition-colors"
            >
              <ChevronLeft className="h-4 w-4" />
            </button>
            {Array.from({ length: Math.min(totalPages, 5) }).map((_, i) => {
              const pageNum =
                totalPages <= 5 ? i : Math.max(0, Math.min(page - 2, totalPages - 5)) + i;
              return (
                <button
                  type="button"
                  key={pageNum}
                  onClick={() => table.setPageIndex(pageNum)}
                  aria-current={pageNum === page ? "page" : undefined}
                  className={cn(
                    "inline-flex items-center justify-center h-8 w-8 rounded-md text-sm transition-colors",
                    pageNum === page
                      ? "bg-primary text-primary-foreground"
                      : "text-muted-foreground hover:text-foreground hover:bg-accent",
                  )}
                >
                  {pageNum + 1}
                </button>
              );
            })}
            <button
              type="button"
              onClick={() => table.nextPage()}
              disabled={!table.getCanNextPage()}
              aria-label="Next page"
              className="inline-flex items-center justify-center h-8 w-8 rounded-md border border-border
                text-muted-foreground hover:text-foreground hover:bg-accent disabled:opacity-50
                disabled:pointer-events-none transition-colors"
            >
              <ChevronRight className="h-4 w-4" />
            </button>
          </div>
        </div>
      )}
    </div>
  );
}

// ============================================================
// VirtualizedGrid — DIV-based ARIA grid used when `virtualized` is set.
//
// A position:absolute table row breaks native table layout, so this branch does
// NOT reuse the semantic table primitives. It renders a scroll container with
// role="grid", a sticky header row, and absolutely positioned virtual rows.
// Column widths are shared between header and rows so columns stay aligned.
//
// NOTE: off-screen rows are not mounted, so the browser's native Ctrl-F will
// not match text outside the rendered window. Inherent to virtualization.
// ============================================================

type AnyTable<T> = ReturnType<typeof useTable<Features, RowOf<T>>>;

function VirtualizedGrid<T>({
  activeColumns,
  table,
  rows,
  rowVirtualizer,
  scrollRef,
  totalRows,
  selectable,
  resizable,
  cellPadding,
  selectPadding,
  rowHeight,
  loading,
  skeletonRows,
  emptyMessage,
  isError,
  errorMessage,
  onRetry,
  keyExtractor,
  onRowClick,
  focusedRowIndex,
  setFocusedRowIndex,
  focusRowAt,
}: {
  activeColumns: Column<T>[];
  table: AnyTable<T>;
  rows: RtRow<Features, RowOf<T>>[];
  rowVirtualizer: Virtualizer<HTMLDivElement, Element>;
  scrollRef: React.RefObject<HTMLDivElement | null>;
  totalRows: number;
  selectable: boolean;
  resizable: boolean;
  cellPadding: string;
  selectPadding: string;
  rowHeight: number;
  loading: boolean;
  skeletonRows: number;
  emptyMessage: string;
  isError: boolean;
  errorMessage: string;
  onRetry?: () => void;
  keyExtractor: (row: T) => string;
  onRowClick?: (row: T) => void;
  focusedRowIndex: number;
  setFocusedRowIndex: (i: number) => void;
  focusRowAt: (i: number) => void;
}) {
  const colStyle = (col: Column<T>): React.CSSProperties => {
    const width = resizable ? `${table.getColumn(col.key)?.getSize()}px` : col.width;
    return width ? { width, flex: `0 0 ${width}`, minWidth: width } : { flex: "1 1 0", minWidth: 0 };
  };
  const selectColStyle: React.CSSProperties = { flex: "0 0 2.5rem", width: "2.5rem" };

  const alignClass = (col: Column<T>) =>
    cn(
      col.align === "center" && "text-center justify-center",
      col.align === "right" && "text-right justify-end",
    );

  const virtualItems = rowVirtualizer.getVirtualItems();

  return (
    <div className="rounded-lg border border-border overflow-hidden">
      <div
        ref={scrollRef}
        role="grid"
        aria-rowcount={totalRows}
        aria-colcount={activeColumns.length + (selectable ? 1 : 0)}
        aria-multiselectable={selectable || undefined}
        // The grid container is the single Tab entry point. Rows are focused
        // programmatically and stay out of the Tab order, so the grid remains
        // reachable even when the focused row has been virtualized away.
        tabIndex={0}
        onKeyDown={(e) => {
          if (e.target !== e.currentTarget) return;
          if (e.key === "ArrowDown" || e.key === "ArrowUp") {
            e.preventDefault();
            focusRowAt(focusedRowIndex >= 0 ? focusedRowIndex : 0);
          }
        }}
        className="relative max-h-[28rem] overflow-auto text-sm outline-none focus:ring-1 focus:ring-inset focus:ring-ring"
      >
        {/* Sticky header row */}
        <div
          role="row"
          aria-rowindex={1}
          className="sticky top-0 z-10 flex border-b border-border bg-muted/50 text-muted-foreground"
        >
          {selectable && (
            <div
              role="columnheader"
              className={cn("flex items-center", selectPadding)}
              style={selectColStyle}
            >
              <input
                type="checkbox"
                checked={table.getIsAllPageRowsSelected()}
                onChange={table.getToggleAllPageRowsSelectedHandler()}
                aria-label="Select all rows"
                className="rounded border-border text-primary focus:ring-ring"
              />
            </div>
          )}
          {activeColumns.map((col) => {
            const column = table.getColumn(col.key);
            const sorted = column?.getIsSorted();
            return (
              <div
                key={col.key}
                role="columnheader"
                aria-sort={
                  col.sortable !== false
                    ? sorted === "asc"
                      ? "ascending"
                      : sorted === "desc"
                        ? "descending"
                        : "none"
                    : undefined
                }
                className={cn(
                  cellPadding,
                  "flex items-center gap-1 font-medium whitespace-nowrap",
                  col.sortable !== false && "cursor-pointer select-none hover:text-foreground",
                  alignClass(col),
                )}
                style={colStyle(col)}
                onClick={() => col.sortable !== false && column?.toggleSorting()}
              >
                {col.header}
                {col.sortable !== false && (
                  <span className="text-muted-foreground/50">
                    {sorted === "asc" ? (
                      <ChevronUp className="h-3.5 w-3.5" />
                    ) : sorted === "desc" ? (
                      <ChevronDown className="h-3.5 w-3.5" />
                    ) : (
                      <ChevronsUpDown className="h-3 w-3" />
                    )}
                  </span>
                )}
              </div>
            );
          })}
        </div>

        {/* Body */}
        {isError ? (
          <div role="row">
            <div role="gridcell" className="px-4 py-12 text-center">
              <div className="flex flex-col items-center gap-3 text-sm text-destructive">
                <span>{errorMessage}</span>
                {onRetry && (
                  <button
                    type="button"
                    onClick={onRetry}
                    className="inline-flex items-center gap-1.5 h-8 px-3 rounded-md border border-destructive/50
                      text-destructive hover:bg-destructive/10 transition-colors"
                  >
                    Retry
                  </button>
                )}
              </div>
            </div>
          </div>
        ) : loading ? (
          <div>
            {Array.from({ length: skeletonRows }).map((_, i) => (
              <div key={i} role="row" className="flex border-b border-border" style={{ height: rowHeight }}>
                {selectable && (
                  <div
                    role="gridcell"
                    className={cn("flex items-center", selectPadding)}
                    style={selectColStyle}
                  >
                    <div className="h-4 w-4 rounded bg-muted animate-pulse" />
                  </div>
                )}
                {activeColumns.map((col) => (
                  <div
                    key={col.key}
                    role="gridcell"
                    className={cn("flex items-center", cellPadding)}
                    style={colStyle(col)}
                  >
                    <div
                      className="h-4 w-24 max-w-full rounded bg-muted animate-pulse"
                      style={{ width: col.width ? `min(100%, ${col.width})` : undefined }}
                    />
                  </div>
                ))}
              </div>
            ))}
          </div>
        ) : rows.length === 0 ? (
          <div role="row">
            <div role="gridcell" className="px-4 py-12 text-center text-muted-foreground">
              {emptyMessage}
            </div>
          </div>
        ) : (
          <div style={{ height: rowVirtualizer.getTotalSize(), position: "relative", width: "100%" }}>
            {virtualItems.map((virtualRow) => {
              const row = rows[virtualRow.index];
              const key = keyExtractor(row.original);
              const isSelected = row.getIsSelected();
              return (
                <div
                  key={key}
                  // measureElement reads each row's real height for variable-size
                  // rows; data-index lets the virtualizer key the measurement.
                  ref={rowVirtualizer.measureElement}
                  data-index={virtualRow.index}
                  data-row-index={virtualRow.index}
                  role="row"
                  // 1-based, and +1 again because the sticky header is row 1.
                  aria-rowindex={virtualRow.index + 2}
                  aria-selected={selectable ? isSelected : undefined}
                  // Programmatically focusable only: the grid container owns the
                  // Tab stop, so a virtualized-out focused row can't strand
                  // keyboard users outside the grid.
                  tabIndex={-1}
                  onFocus={() => setFocusedRowIndex(virtualRow.index)}
                  onKeyDown={(e) => {
                    if (e.key === "ArrowDown") {
                      e.preventDefault();
                      focusRowAt(virtualRow.index + 1);
                    } else if (e.key === "ArrowUp") {
                      e.preventDefault();
                      focusRowAt(virtualRow.index - 1);
                    } else if (e.key === "Enter" && onRowClick) {
                      e.preventDefault();
                      onRowClick(row.original);
                    }
                  }}
                  onClick={() => onRowClick?.(row.original)}
                  className={cn(
                    "absolute left-0 top-0 flex w-full border-b border-border transition-colors",
                    "focus:outline-none focus:ring-1 focus:ring-inset focus:ring-ring",
                    onRowClick && "cursor-pointer hover:bg-muted/50",
                    isSelected && "bg-muted/30",
                  )}
                  style={{ transform: `translateY(${virtualRow.start}px)` }}
                >
                  {selectable && (
                    <div
                      role="gridcell"
                      className={cn("flex items-center", selectPadding)}
                      style={selectColStyle}
                      onClick={(e) => e.stopPropagation()}
                    >
                      <input
                        type="checkbox"
                        checked={isSelected}
                        onChange={row.getToggleSelectedHandler()}
                        aria-label="Select row"
                        className="rounded border-border text-primary focus:ring-ring"
                      />
                    </div>
                  )}
                  {activeColumns.map((col) => (
                    <div
                      key={col.key}
                      role="gridcell"
                      className={cn("flex items-center", cellPadding, alignClass(col))}
                      style={colStyle(col)}
                    >
                      {col.accessor(row.original)}
                    </div>
                  ))}
                </div>
              );
            })}
          </div>
        )}
      </div>
    </div>
  );
}

// ============================================================
// FacetedFilter — multi-select dropdown driven by the column's faceted unique
// values. Rendered in the toolbar for columns with a `filter` config.
// ============================================================

function FacetedFilter<T>({
  column,
  label,
  onChange,
}: {
  column: RtColumn<Features, RowOf<T>, unknown>;
  label: string;
  onChange?: () => void;
}) {
  const [open, setOpen] = useState(false);
  const selected = (column.getFilterValue() as string[] | undefined) ?? [];
  const options = Array.from(column.getFacetedUniqueValues().keys())
    .map((v) => String(v))
    .filter((v) => v !== "")
    .sort();

  const apply = (next: string[]) => {
    column.setFilterValue(next.length ? next : undefined);
    onChange?.();
  };
  const toggle = (value: string) =>
    apply(selected.includes(value) ? selected.filter((v) => v !== value) : [...selected, value]);

  return (
    <div className="relative">
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        className={cn(
          "inline-flex items-center gap-1.5 h-9 px-3 rounded-md border border-border text-sm transition-colors",
          selected.length > 0
            ? "border-primary/50 text-foreground bg-accent"
            : "text-muted-foreground hover:text-foreground hover:bg-accent",
        )}
      >
        <Filter className="h-3.5 w-3.5" />
        {label}
        {selected.length > 0 && (
          <span className="ml-0.5 inline-flex items-center justify-center min-w-5 h-5 px-1 rounded-full bg-primary text-primary-foreground text-2xs">
            {selected.length}
          </span>
        )}
      </button>

      {open && (
        <div className="absolute left-0 top-full mt-1 w-52 rounded-md border border-border bg-popover p-1 shadow-lg z-50">
          {options.length === 0 ? (
            <p className="px-2 py-1.5 text-sm text-muted-foreground">No values</p>
          ) : (
            options.map((opt) => (
              <label
                key={opt}
                className="flex items-center gap-2 px-2 py-1.5 rounded text-sm hover:bg-accent cursor-pointer"
              >
                <input
                  type="checkbox"
                  checked={selected.includes(opt)}
                  onChange={() => toggle(opt)}
                  className="rounded border-border text-primary focus:ring-ring"
                />
                {opt}
              </label>
            ))
          )}
          {selected.length > 0 && (
            <button
              type="button"
              onClick={() => apply([])}
              className="w-full mt-1 px-2 py-1.5 rounded text-sm text-muted-foreground hover:text-foreground hover:bg-accent text-left"
            >
              Clear filter
            </button>
          )}
        </div>
      )}
    </div>
  );
}
