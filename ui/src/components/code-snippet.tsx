import { cn } from "@/lib/utils";

export function CodeSnippet({
  code,
  filePath,
  lineStart,
  lineEnd,
  className,
}: {
  code: string;
  filePath?: string;
  lineStart?: number;
  lineEnd?: number;
  className?: string;
}) {
  const lines = code.split("\n");
  const startLine = lineStart ?? 1;

  return (
    <div className={cn("rounded-md border bg-muted/50 overflow-hidden", className)}>
      {filePath && (
        <div className="px-3 py-1.5 text-xs text-muted-foreground bg-muted border-b font-mono">
          {filePath}
          {lineStart != null && `:${lineStart}`}
          {lineEnd != null && lineEnd !== lineStart && `-${lineEnd}`}
        </div>
      )}
      <pre className="overflow-x-auto p-3 text-sm">
        <code>
          {lines.map((line, i) => (
            <div key={i} className="flex">
              <span className="text-muted-foreground select-none w-10 shrink-0 text-right pr-3 font-mono text-xs leading-6">
                {startLine + i}
              </span>
              <span className="font-mono leading-6 whitespace-pre">{line}</span>
            </div>
          ))}
        </code>
      </pre>
    </div>
  );
}
