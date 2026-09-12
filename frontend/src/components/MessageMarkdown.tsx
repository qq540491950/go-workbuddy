import { memo, useState } from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { Check, Copy } from "lucide-react";
import { cn } from "@/lib/utils";

function CopyButton({ text, className }: { text: string; className?: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <button
      className={cn(
        "inline-flex items-center gap-1 rounded-md border bg-background/80 px-1.5 py-0.5 text-[11px] text-muted-foreground transition-colors hover:text-foreground",
        className,
      )}
      onClick={async () => {
        try {
          await navigator.clipboard.writeText(text);
        } catch {
          // Clipboard API may be unavailable; fall back silently.
          const ta = document.createElement("textarea");
          ta.value = text;
          document.body.appendChild(ta);
          ta.select();
          document.execCommand("copy");
          ta.remove();
        }
        setCopied(true);
        setTimeout(() => setCopied(false), 1500);
      }}
    >
      {copied ? <Check className="h-3 w-3" /> : <Copy className="h-3 w-3" />}
      {copied ? "已复制" : "复制"}
    </button>
  );
}

function CodeBlock({ children, className, ...rest }: any) {
  const raw = typeof children === "string" ? children : "";
  const match = /language-(\w+)/.exec(className || "");
  const isBlock = String(className || "").includes("language-") || String(raw).includes("\n");
  if (!isBlock) {
    return (
      <code className="rounded bg-muted px-1 py-0.5 font-mono text-[0.85em]" {...rest}>
        {children}
      </code>
    );
  }
  return (
    <div className="group/code relative my-2 overflow-hidden rounded-lg border">
      <div className="flex items-center justify-between border-b bg-muted/60 px-3 py-1">
        <span className="font-mono text-[11px] text-muted-foreground">{match?.[1] ?? "text"}</span>
        <CopyButton text={raw} className="border-0 bg-transparent" />
      </div>
      <pre className="overflow-x-auto p-3 text-xs leading-relaxed">
        <code className="font-mono" {...rest}>
          {children}
        </code>
      </pre>
    </div>
  );
}

/** MessageMarkdown renders assistant text as GitHub-flavored Markdown. */
export const MessageMarkdown = memo(function MessageMarkdown({ content }: { content: string }) {
  return (
    <div className="message-md space-y-1.5 text-sm leading-relaxed">
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        components={{
          code: CodeBlock,
          pre: ({ children }: any) => <>{children}</>,
          a: ({ href, children }: any) => (
            <a href={href} target="_blank" rel="noreferrer" className="text-primary underline underline-offset-2">
              {children}
            </a>
          ),
          ul: ({ children }: any) => <ul className="ml-4 list-disc space-y-0.5">{children}</ul>,
          ol: ({ children }: any) => <ol className="ml-4 list-decimal space-y-0.5">{children}</ol>,
          h1: ({ children }: any) => <h1 className="mt-2 text-base font-semibold">{children}</h1>,
          h2: ({ children }: any) => <h2 className="mt-2 text-base font-semibold">{children}</h2>,
          h3: ({ children }: any) => <h3 className="mt-1.5 text-sm font-semibold">{children}</h3>,
          blockquote: ({ children }: any) => (
            <blockquote className="border-l-2 border-border pl-3 text-muted-foreground">{children}</blockquote>
          ),
          table: ({ children }: any) => (
            <div className="my-2 overflow-x-auto rounded-lg border">
              <table className="w-full text-xs">{children}</table>
            </div>
          ),
          th: ({ children }: any) => <th className="border-b bg-muted/50 px-2 py-1 text-left font-medium">{children}</th>,
          td: ({ children }: any) => <td className="border-b px-2 py-1">{children}</td>,
        }}
      >
        {content}
      </ReactMarkdown>
    </div>
  );
});

export { CopyButton };
