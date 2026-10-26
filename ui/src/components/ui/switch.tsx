import type { ComponentProps } from "react";
import { cn } from "@/lib/utils";

type SwitchProps = Omit<ComponentProps<"button">, "onChange"> & {
  checked: boolean;
  onCheckedChange: (checked: boolean) => void;
};

export function Switch({ checked, onCheckedChange, className, ...props }: SwitchProps) {
  return (
    <button {...props} type="button" role="switch" aria-checked={checked}
      data-state={checked ? "checked" : "unchecked"}
      onClick={() => onCheckedChange(!checked)}
      className={cn("inline-flex h-6 w-11 shrink-0 items-center rounded-full border border-border p-0.5 transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background disabled:cursor-not-allowed disabled:opacity-50", checked ? "bg-primary" : "bg-muted-foreground", className)}>
      <span aria-hidden="true" className={cn("size-4 rounded-full bg-background shadow-sm transition-transform", checked ? "translate-x-5" : "translate-x-0")} />
    </button>
  );
}
