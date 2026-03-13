import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";

export function Markdown({ children, className }: { children: string; className?: string }) {
  return (
    <div className={`prose prose-sm dark:prose-invert max-w-none ${className ?? ""}`}>
      <ReactMarkdown remarkPlugins={[remarkGfm]}>{children}</ReactMarkdown>
    </div>
  );
}
