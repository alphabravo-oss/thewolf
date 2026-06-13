// ui/src/components/severity-badge.tsx
import { cva, type VariantProps } from "class-variance-authority";
import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";

const severityVariants = cva("", {
  variants: {
    severity: {
      critical: "border-red-500/40 bg-red-500/15 text-red-300",
      high:     "border-orange-500/40 bg-orange-500/15 text-orange-300",
      medium:   "border-amber-500/40 bg-amber-500/15 text-amber-300",
      low:      "border-sky-500/40 bg-sky-500/15 text-sky-300",
      info:     "border-zinc-500/40 bg-zinc-500/15 text-zinc-300",
    },
  },
  defaultVariants: { severity: "info" },
});

type Props = VariantProps<typeof severityVariants> & {
  className?: string;
  children?: React.ReactNode;
};

export function SeverityBadge({ severity, className, children }: Props) {
  return (
    <Badge variant="outline" className={cn(severityVariants({ severity }), className)}>
      {children ?? severity}
    </Badge>
  );
}
