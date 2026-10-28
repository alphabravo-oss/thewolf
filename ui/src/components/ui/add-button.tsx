// The standard "+ Add <thing>" trigger for PageHeader actions and CardHeaders.
// Forwards its ref and props so it can be a DialogTrigger/SheetTrigger `asChild`.
import { forwardRef } from "react";
import { PlusIcon } from "lucide-react";
import { Button, type ButtonProps } from "@/components/ui/button";

export const AddButton = forwardRef<
  HTMLButtonElement,
  Omit<ButtonProps, "children"> & { thing: string }
>(({ thing, size = "sm", ...props }, ref) => (
  <Button ref={ref} size={size} {...props}>
    <PlusIcon aria-hidden />
    Add {thing}
  </Button>
));
AddButton.displayName = "AddButton";
