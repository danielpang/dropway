"use client";

import * as React from "react";
import { ShieldAlert } from "lucide-react";

import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";
import type { AuditEvent } from "@/lib/api";
import { isSecurityAction } from "@/lib/audit-actions";

/**
 * Renders a page of audit events as an accessible table.
 * Columns: time, actor, action, target, ip. Access-mode / security-relevant
 * rows (revocations, unshares, role changes, sharing-policy flips …) are tinted
 * amber and flagged so an admin can scan for the events that matter.
 *
 * Timestamps are rendered client-side (the viewer's locale/timezone). The table
 * is purely presentational; pagination is driven by the server component via URL
 * search params, so this component takes a ready page of rows.
 */
export function AuditTable({ events }: { events: AuditEvent[] }) {
  if (events.length === 0) {
    return (
      <div className="rounded-lg border border-dashed border-border p-10 text-center text-sm text-muted-foreground">
        No audit events yet. Security-relevant actions like sharing changes,
        member removals, and access revocations will appear here as they happen.
      </div>
    );
  }

  return (
    <div className="overflow-x-auto rounded-lg border border-border">
      <table className="w-full min-w-[44rem] border-collapse text-sm">
        <caption className="sr-only">Recent audit events</caption>
        <thead>
          <tr className="border-b border-border bg-muted/40 text-left">
            <Th className="w-44">Time</Th>
            <Th className="w-48">Actor</Th>
            <Th>Action</Th>
            <Th>Target</Th>
            <Th className="w-36">IP</Th>
          </tr>
        </thead>
        <tbody>
          {events.map((ev, i) => (
            <AuditRow key={ev.id ?? `${ev.created_at ?? "?"}-${i}`} event={ev} />
          ))}
        </tbody>
      </table>
    </div>
  );
}

function Th({
  children,
  className,
}: {
  children: React.ReactNode;
  className?: string;
}) {
  return (
    <th
      scope="col"
      className={cn(
        "px-3 py-2.5 text-xs font-medium uppercase tracking-wide text-muted-foreground",
        className,
      )}
    >
      {children}
    </th>
  );
}

function AuditRow({ event }: { event: AuditEvent }) {
  const security = isSecurityAction(event.action);
  const actor =
    event.actor_label ??
    event.actor_user ??
    (event.actor_token ? "token" : null);

  return (
    <tr
      className={cn(
        "border-b border-border/70 align-top last:border-b-0",
        security ? "bg-amber-500/[0.06]" : "hover:bg-muted/30",
      )}
    >
      <td className="px-3 py-2.5 tabular-nums text-muted-foreground">
        <Timestamp iso={event.created_at} />
      </td>
      <td className="px-3 py-2.5">
        <span className="block max-w-[12rem] truncate font-medium text-foreground" title={actor ?? undefined}>
          {actor ?? <span className="text-muted-foreground">system</span>}
        </span>
        {event.actor_token && event.actor_user && (
          <span className="text-xs text-muted-foreground">via token</span>
        )}
      </td>
      <td className="px-3 py-2.5">
        <span className="inline-flex items-center gap-1.5">
          {security && (
            <ShieldAlert
              className="size-3.5 shrink-0 text-amber-600 dark:text-amber-400"
              aria-hidden
            />
          )}
          <code
            className={cn(
              "rounded bg-muted px-1.5 py-0.5 font-mono text-xs",
              security && "text-amber-700 dark:text-amber-300",
            )}
          >
            {event.action ?? "unknown"}
          </code>
          {security && (
            <Badge variant="outline" className="border-amber-500/40 text-amber-700 dark:text-amber-300">
              security
            </Badge>
          )}
        </span>
        {event.metadata && Object.keys(event.metadata).length > 0 && (
          <MetadataDetails metadata={event.metadata} />
        )}
      </td>
      <td className="px-3 py-2.5">
        <span className="block max-w-[16rem] truncate text-foreground" title={event.target ?? undefined}>
          {event.target ?? <span className="text-muted-foreground">None</span>}
        </span>
      </td>
      <td className="px-3 py-2.5 font-mono text-xs text-muted-foreground">
        {event.ip ?? "None"}
      </td>
    </tr>
  );
}

/** Collapsible jsonb metadata, kept out of the way until an admin expands it. */
function MetadataDetails({ metadata }: { metadata: Record<string, unknown> }) {
  return (
    <details className="mt-1 text-xs text-muted-foreground">
      <summary className="cursor-pointer select-none rounded focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
        details
      </summary>
      <pre className="mt-1 max-w-md overflow-x-auto whitespace-pre-wrap break-words rounded bg-muted px-2 py-1.5 font-mono text-[11px] leading-relaxed">
        {safeStringify(metadata)}
      </pre>
    </details>
  );
}

function safeStringify(value: unknown): string {
  try {
    return JSON.stringify(value, null, 2);
  } catch {
    return String(value);
  }
}

/**
 * Renders an ISO timestamp in the viewer's locale. Rendered after mount to avoid
 * an SSR/client hydration mismatch (the server's locale/timezone differs from
 * the browser's); until then it shows the raw date portion as a stable fallback.
 */
function Timestamp({ iso }: { iso: string | undefined }) {
  const [mounted, setMounted] = React.useState(false);
  React.useEffect(() => setMounted(true), []);

  if (!iso) return <span>Unknown</span>;
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return <span title={iso}>{iso}</span>;

  return (
    <time dateTime={iso} title={iso} suppressHydrationWarning>
      {mounted ? d.toLocaleString() : iso.slice(0, 10)}
    </time>
  );
}
