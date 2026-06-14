import Link from "next/link";
import { ChevronLeft, ChevronRight } from "lucide-react";

import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

/**
 * Server-rendered prev/next pager for the audit log. Pagination state lives in
 * the URL (`?page=N`) so the page stays a server component and is shareable /
 * back-button friendly. Disabled edges render as inert buttons (no link).
 */
export function AuditPagination({
  page,
  hasPrev,
  hasNext,
  total,
  pageSize,
  count,
}: {
  page: number;
  hasPrev: boolean;
  hasNext: boolean;
  total: number | null;
  pageSize: number;
  /** Rows shown on this page (for the "showing N" label when total is unknown). */
  count: number;
}) {
  const from = page * pageSize + (count > 0 ? 1 : 0);
  const to = page * pageSize + count;

  return (
    <div className="flex items-center justify-between gap-4 text-sm text-muted-foreground">
      <p aria-live="polite">
        {total != null ? (
          <>
            Showing <span className="text-foreground">{from}</span>–
            <span className="text-foreground">{to}</span> of{" "}
            <span className="text-foreground">{total}</span>
          </>
        ) : count > 0 ? (
          <>
            Showing <span className="text-foreground">{from}</span>–
            <span className="text-foreground">{to}</span>
          </>
        ) : (
          "No events"
        )}
      </p>

      <div className="flex items-center gap-2">
        <PagerButton
          href={hasPrev ? pageHref(page - 1) : undefined}
          label="Previous page"
        >
          <ChevronLeft aria-hidden />
          Previous
        </PagerButton>
        <PagerButton
          href={hasNext ? pageHref(page + 1) : undefined}
          label="Next page"
        >
          Next
          <ChevronRight aria-hidden />
        </PagerButton>
      </div>
    </div>
  );
}

/**
 * Build the href for a 0-based page index. The URL is 1-based (`?page=2` is the
 * second page) and page 0 drops the param entirely for a clean canonical URL.
 */
function pageHref(zeroBasedPage: number): string {
  return zeroBasedPage <= 0 ? "/audit" : `/audit?page=${zeroBasedPage + 1}`;
}

function PagerButton({
  href,
  label,
  children,
}: {
  href: string | undefined;
  label: string;
  children: React.ReactNode;
}) {
  if (!href) {
    return (
      <Button
        variant="outline"
        size="sm"
        disabled
        aria-label={label}
        className="pointer-events-none gap-1"
      >
        {children}
      </Button>
    );
  }
  return (
    <Button asChild variant="outline" size="sm" className={cn("gap-1")}>
      <Link href={href} aria-label={label} scroll={false}>
        {children}
      </Link>
    </Button>
  );
}
