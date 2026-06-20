import { cn } from "@/lib/utils";

/**
 * Shared primitives for the in-app reference pages (/mcp, /cli). Ported from the
 * dropway-www docs components so the content reads identically, minus the
 * marketing-only scroll-reveal animation — these render inside the authenticated
 * app shell, which already supplies the header and chrome.
 */

/** Page header band for a reference page. */
export function DocHero({
  eyebrow,
  title,
  lead,
}: {
  eyebrow: string;
  title: string;
  lead: string;
}) {
  return (
    <div className="space-y-3">
      <p className="font-mono text-xs font-semibold uppercase tracking-wider text-primary">
        {eyebrow}
      </p>
      <h1 className="text-3xl font-semibold tracking-tight sm:text-4xl">
        {title}
      </h1>
      <p className="text-pretty text-base leading-relaxed text-muted-foreground sm:text-lg">
        {lead}
      </p>
    </div>
  );
}

/** A titled reference section with a stable anchor id. */
export function Section({
  id,
  title,
  lead,
  children,
  className,
}: {
  id: string;
  title: string;
  lead?: string;
  children: React.ReactNode;
  className?: string;
}) {
  return (
    <section id={id} className={cn("scroll-mt-20", className)}>
      <h2 className="text-xl font-semibold tracking-tight sm:text-2xl">
        {title}
      </h2>
      {lead ? (
        <p className="mt-2.5 leading-relaxed text-muted-foreground">{lead}</p>
      ) : null}
      <div className="mt-5 space-y-4 text-[0.95rem] leading-relaxed text-muted-foreground">
        {children}
      </div>
    </section>
  );
}

/** Inline code token, for prose. */
export function Code({ children }: { children: React.ReactNode }) {
  return (
    <code className="rounded-md border border-border bg-muted/60 px-1.5 py-0.5 font-mono text-[0.85em] text-foreground">
      {children}
    </code>
  );
}

/** A simple bordered reference table. Scrolls internally on tiny screens rather
 * than forcing the page sideways. */
export function DocTable({
  head,
  rows,
}: {
  head: string[];
  rows: React.ReactNode[][];
}) {
  return (
    <div className="overflow-x-auto rounded-xl border border-border">
      <table className="w-full border-collapse text-left text-sm">
        <thead>
          <tr className="border-b border-border bg-muted/50">
            {head.map((h) => (
              <th key={h} className="px-4 py-2.5 font-semibold text-foreground">
                {h}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((row, i) => (
            <tr
              key={i}
              className="border-b border-border align-top last:border-0"
            >
              {row.map((cell, j) => (
                <td key={j} className="px-4 py-2.5 text-muted-foreground">
                  {cell}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

/** A highlighted callout for notes/caveats. */
export function Callout({
  title,
  children,
}: {
  title?: string;
  children: React.ReactNode;
}) {
  return (
    <div className="rounded-xl border border-primary/30 bg-primary/[0.06] px-4 py-3.5 text-sm leading-relaxed text-muted-foreground">
      {title ? <p className="font-medium text-foreground">{title}</p> : null}
      <div className={title ? "mt-1" : undefined}>{children}</div>
    </div>
  );
}
