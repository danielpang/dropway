"use client";

import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";

/**
 * Markdown renderer for the AI builder's assistant messages. react-markdown
 * never injects raw HTML, so model output is safe to render as-is; links open
 * in a new tab because the chat lives next to an iframe preview the user is
 * mid-flow in. Styling is inline per element (the app has no typography
 * plugin), tuned for a compact chat bubble rather than an article: tight
 * margins, scaled-down headings, and code blocks that scroll horizontally
 * instead of stretching the bubble.
 */
export function ChatMarkdown({ text }: { text: string }) {
  return (
    <div className="space-y-2 [&>*:first-child]:mt-0 [&>*:last-child]:mb-0">
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        components={{
          a: ({ children, href }) => (
            <a
              href={href}
              target="_blank"
              rel="noopener noreferrer"
              className="font-medium underline underline-offset-2 hover:opacity-80"
            >
              {children}
            </a>
          ),
          p: ({ children }) => <p className="leading-relaxed">{children}</p>,
          ul: ({ children }) => (
            <ul className="list-disc space-y-1 pl-5">{children}</ul>
          ),
          ol: ({ children }) => (
            <ol className="list-decimal space-y-1 pl-5">{children}</ol>
          ),
          li: ({ children }) => <li className="leading-relaxed">{children}</li>,
          h1: ({ children }) => (
            <p className="text-sm font-semibold">{children}</p>
          ),
          h2: ({ children }) => (
            <p className="text-sm font-semibold">{children}</p>
          ),
          h3: ({ children }) => (
            <p className="text-sm font-semibold">{children}</p>
          ),
          h4: ({ children }) => (
            <p className="text-sm font-semibold">{children}</p>
          ),
          blockquote: ({ children }) => (
            <blockquote className="border-l-2 border-border pl-3 text-muted-foreground">
              {children}
            </blockquote>
          ),
          pre: ({ children }) => (
            <pre className="overflow-x-auto rounded-md bg-background/60 p-2.5 text-xs leading-relaxed [&_code]:rounded-none [&_code]:bg-transparent [&_code]:p-0">
              {children}
            </pre>
          ),
          code: ({ children }) => (
            <code className="rounded bg-background/60 px-1 py-0.5 font-mono text-[0.8em]">
              {children}
            </code>
          ),
          table: ({ children }) => (
            <div className="overflow-x-auto">
              <table className="w-full border-collapse text-xs">
                {children}
              </table>
            </div>
          ),
          th: ({ children }) => (
            <th className="border border-border px-2 py-1 text-left font-semibold">
              {children}
            </th>
          ),
          td: ({ children }) => (
            <td className="border border-border px-2 py-1">{children}</td>
          ),
          hr: () => <hr className="border-border" />,
        }}
      >
        {text}
      </ReactMarkdown>
    </div>
  );
}
