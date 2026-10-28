// Building blocks shared by every Settings section: a titled card, per-field
// save controls (no <form>s outside dialogs), a trailing row-action menu, and a
// type-the-name confirmation for the highest-risk deletes.
import { useEffect, useId, useState, type KeyboardEvent, type ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { MoreHorizontalIcon, PlusIcon } from "lucide-react";
import { api } from "@/lib/api";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader } from "@/components/ui/card";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { DataTable, type Column } from "@/components/ui/data-table";
import { EmptyState } from "@/components/ui/empty-state";
import { FieldError } from "@/components/ui/form-dialog";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { cn } from "@/lib/utils";

/** A titled Card. `flush` drops the body padding for a full-bleed DataTable. */
export function SettingsCard({
  title,
  description,
  actions,
  label,
  flush,
  className,
  children,
}: {
  title: ReactNode;
  description?: ReactNode;
  actions?: ReactNode;
  /** Makes the card a named landmark (also what tests/AT find it by). */
  label?: string;
  /** The body is a DataTable: tighter padding so the table gets the width. */
  flush?: boolean;
  className?: string;
  children?: ReactNode;
}) {
  return (
    <Card role={label ? "region" : undefined} aria-label={label} className={cn("min-w-0 overflow-hidden", className)}>
      <CardHeader className="items-start">
        <div className="min-w-0 space-y-1">
          <h3 className="text-[0.95rem] font-semibold leading-none tracking-tight">{title}</h3>
          {description ? <CardDescription>{description}</CardDescription> : null}
        </div>
        {actions ? <div className="flex shrink-0 flex-wrap items-center gap-2">{actions}</div> : null}
      </CardHeader>
      {children ? <CardContent className={cn("space-y-4", flush && "p-4")}>{children}</CardContent> : null}
    </Card>
  );
}

/** One label + help + control row inside a card, for the per-field settings. */
export function SettingRow({ label, help, children }: { label: ReactNode; help?: ReactNode; children: ReactNode }) {
  return (
    <div className="grid items-start gap-3 md:grid-cols-[1fr_240px]">
      <div className="min-w-0">
        <div className="text-sm font-medium">{label}</div>
        {help ? <p className="mt-0.5 text-xs text-muted-foreground">{help}</p> : null}
      </div>
      <div className="min-w-0">{children}</div>
    </div>
  );
}

/** Label above a control for dialog bodies. Wraps the control so the label names it. */
export function Field({ label, hint, error, children }: { label: string; hint?: ReactNode; error?: ReactNode; children: ReactNode }) {
  return (
    <div>
      <label className="block space-y-1.5">
        <span className="block text-xs font-medium text-muted-foreground">{label}</span>
        {children}
      </label>
      {hint ? <p className="mt-1.5 text-xs text-muted-foreground">{hint}</p> : null}
      <FieldError>{error}</FieldError>
    </div>
  );
}

const onEnter = (fn: () => void) => (e: KeyboardEvent) => {
  if (e.key === "Enter") {
    e.preventDefault();
    fn();
  }
};

export function TextSetting({ label, value, type = "text", onCommit, disabled, hideLabel }: {
  label: string; value: string; type?: string; onCommit: (value: string) => void; disabled?: boolean;
  /** The surrounding SettingRow already shows the label; keep it for assistive tech only. */
  hideLabel?: boolean;
}) {
  const id = useId();
  const [draft, setDraft] = useState(value);
  useEffect(() => setDraft(value), [value]);
  const dirty = draft !== value;
  const commit = () => dirty && !disabled && onCommit(draft);
  return (
    <div className="space-y-1">
      <label htmlFor={id} className={hideLabel ? "sr-only" : "block text-xs text-muted-foreground"}>{label}</label>
      <div className="flex items-center gap-2">
        <Input id={id} type={type} autoComplete={type === "password" ? "off" : undefined}
          value={draft} disabled={disabled} onChange={(e) => setDraft(e.target.value)} onKeyDown={onEnter(commit)}
          className="h-9 min-w-0" />
        {dirty && <Button type="button" size="sm" disabled={disabled} onClick={commit}>Save</Button>}
      </div>
    </div>
  );
}

export function BoolToggle({ label, value, onChange, disabled }: {
  label: string; value: boolean; onChange: (value: boolean) => Promise<unknown>; disabled?: boolean;
}) {
  const [draft, setDraft] = useState(value);
  const [pending, setPending] = useState(false);
  useEffect(() => setDraft(value), [value]);
  async function toggle(next: boolean) {
    setDraft(next);
    setPending(true);
    try {
      await onChange(next);
    } catch {
      // The mutation owns the error toast; restore the last confirmed value.
      setDraft(value);
    } finally {
      setPending(false);
    }
  }
  return (
    <div className="flex min-h-9 items-center gap-3">
      <Switch aria-label={label} checked={draft} onCheckedChange={toggle} disabled={disabled || pending} />
      <span className="text-xs text-muted-foreground">{draft ? "On" : "Off"}</span>
    </div>
  );
}

export function ChoiceSetting({ label, value, options, onCommit, disabled }: {
  label: string; value: string; options: string[]; onCommit: (value: string) => void; disabled?: boolean;
}) {
  const [draft, setDraft] = useState(value);
  useEffect(() => setDraft(value), [value]);
  const dirty = draft !== value;
  return (
    <div className="flex items-center gap-2">
      <Select value={draft} onValueChange={setDraft} disabled={disabled}>
        <SelectTrigger aria-label={label} className="h-9 min-w-0"><SelectValue /></SelectTrigger>
        <SelectContent>{options.map((option) => <SelectItem key={option} value={option}>{option}</SelectItem>)}</SelectContent>
      </Select>
      {dirty && <Button type="button" size="sm" disabled={disabled} onClick={() => onCommit(draft)}>Save</Button>}
    </div>
  );
}

export function IntInput({ label, value, min, max, onCommit, disabled }: {
  label: string; value: string; min?: number; max?: number;
  onCommit: (v: string) => void; disabled?: boolean;
}) {
  const [draft, setDraft] = useState(value);
  useEffect(() => setDraft(value), [value]);
  const dirty = draft !== value;
  const valid = draft.trim() !== "" && Number.isInteger(Number(draft)) &&
    (min === undefined || Number(draft) >= min) && (max === undefined || Number(draft) <= max);
  const commit = () => dirty && valid && !disabled && onCommit(draft);
  return (
    <div className="flex items-center gap-2">
      <Input type="number" aria-label={label} value={draft} min={min} max={max} step={1}
        onChange={(e) => setDraft(e.target.value)} onKeyDown={onEnter(commit)}
        className="h-9 w-24 tabular-nums" disabled={disabled} />
      {dirty && <Button type="button" size="sm" disabled={disabled || !valid} onClick={commit}>Save</Button>}
    </div>
  );
}

export type RowAction = {
  label: string;
  onSelect: () => void;
  destructive?: boolean;
  disabled?: boolean;
};

/** Trailing "⋯" menu for a table row. Falsy entries are dropped; renders nothing if none remain. */
export function RowActions({ label, actions }: { label: string; actions: (RowAction | false | null | undefined)[] }) {
  const items = actions.filter((a): a is RowAction => !!a);
  if (items.length === 0) return null;
  return (
    <div className="flex justify-end">
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button type="button" variant="ghost" size="icon" className="size-8" aria-label={`Actions for ${label}`}>
            <MoreHorizontalIcon />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end">
          {items.map((a) => (
            <DropdownMenuItem key={a.label} disabled={a.disabled} onSelect={a.onSelect}
              className={a.destructive ? "text-destructive-text focus:text-destructive-text" : undefined}>
              {a.label}
            </DropdownMenuItem>
          ))}
        </DropdownMenuContent>
      </DropdownMenu>
    </div>
  );
}

/** ConfirmDialog that only enables the action once the target's name is typed. */
export function TypedConfirmDialog({ open, onOpenChange, expected, ...rest }: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** Exact text the operator must type (the record's name). */
  expected: string;
  title: string;
  description?: ReactNode;
  confirmLabel: string;
  pending?: boolean;
  onConfirm: () => void;
}) {
  const [typed, setTyped] = useState("");
  useEffect(() => {
    if (!open) setTyped("");
  }, [open]);
  const id = useId();
  return (
    <ConfirmDialog open={open} onOpenChange={onOpenChange} confirmDisabled={typed !== expected} {...rest}>
      <label htmlFor={id} className="block space-y-1.5 text-sm">
        <span>Type <code className="font-mono font-semibold">{expected}</code> to confirm.</span>
        <Input id={id} value={typed} autoComplete="off" onChange={(e) => setTyped(e.target.value)} />
      </label>
    </ConfirmDialog>
  );
}

/** user_id -> email for the admin oversight tables (no backend join). */
export function useOwnerLookup() {
  const q = useQuery({
    queryKey: ["users"],
    queryFn: async () => (await api.get<{ id: string; email: string }[]>("/users")).data ?? [],
  });
  const map = new Map((q.data ?? []).map((u) => [u.id, u.email]));
  return (id?: string) => (id ? (map.get(id) ?? `${id.slice(0, 8)}…`) : "—");
}

export const shortDate = (iso?: string) => (iso ? new Date(iso).toLocaleDateString() : "—");

/**
 * DataTable for a settings list: loading/error come from the table, and an
 * empty list swaps to an EmptyState carrying the `+ Add` action.
 */
export function SettingsTable<T>({ query, columns, keyExtractor, persistKey, searchPlaceholder, empty }: {
  query: { data?: T[]; isLoading: boolean; isError: boolean; refetch: () => unknown };
  columns: Column<T>[];
  keyExtractor: (row: T) => string;
  persistKey: string;
  searchPlaceholder: string;
  empty: { title: string; description?: string; actionLabel?: string; onAction?: () => void };
}) {
  const rows = query.data ?? [];
  if (!query.isLoading && !query.isError && rows.length === 0) {
    return (
      <EmptyState
        className="py-10"
        title={empty.title}
        description={empty.description}
        actionLabel={empty.actionLabel}
        onAction={empty.onAction}
        actionIcon={PlusIcon}
      />
    );
  }
  return (
    <DataTable
      data={rows}
      columns={columns}
      keyExtractor={keyExtractor}
      persistKey={persistKey}
      density="compact"
      loading={query.isLoading}
      isError={query.isError}
      onRetry={() => void query.refetch()}
      searchPlaceholder={searchPlaceholder}
    />
  );
}
