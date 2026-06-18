import type { Metadata } from "next";
import { ScrollText, ShieldAlert } from "lucide-react";

import { AuditPagination } from "@/components/audit/audit-pagination";
import { AuditTable } from "@/components/audit/audit-table";
import { Card } from "@/components/ui/card";
import { loadAuditPage } from "@/lib/audit";
import { canManage, loadActiveOrg } from "@/lib/org";

export const metadata: Metadata = { title: "Audit log" };

// The audit log mutates constantly and is per-tenant; always render live.
export const dynamic = "force-dynamic";

/**
 * Audit log viewer.
 *
 * Owner/admin only — a paginated, newest-first view of app.audit_log for the
 * active org: who did what, to what, from where, and when. Access-mode /
 * security-relevant rows (revocations, unshares, sharing-policy flips, role
 * changes) are highlighted so an admin can scan for the events that matter.
 *
 * Two boundaries gate this:
 *   1. UI role gate (here) — non-admins never see the page.
 *   2. The Go API independently RLS-scopes rows to the org AND re-checks the
 *      owner/admin role on /v1/audit (this UI gate is convenience, not the
 *      security boundary).
 *
 * The endpoint is part of the Go agent's Phase-4 work; if it isn't on this build
 * yet (404), the loader reports `available:false` and we show an explanatory
 * empty state instead of crashing.
 */
export default async function AuditPage({
  searchParams,
}: {
  searchParams: Promise<Record<string, string | string[] | undefined>>;
}) {
  const org = await loadActiveOrg();

  if (!org) {
    return (
      <div className="mx-auto max-w-5xl">
        <Card className="border-dashed p-10 text-center text-sm text-muted-foreground">
          Couldn&rsquo;t load your organization. Reload to try again.
        </Card>
      </div>
    );
  }

  // Role gate: only owners/admins may view the audit log.
  if (!canManage(org.myRole)) {
    return (
      <div className="mx-auto max-w-5xl space-y-8">
        <Header />
        <Card className="flex items-start gap-3 border-amber-500/30 bg-amber-500/5 p-6 text-sm">
          <ShieldAlert
            className="mt-0.5 size-4 shrink-0 text-amber-600 dark:text-amber-400"
            aria-hidden
          />
          <p className="text-muted-foreground">
            Only owners and admins can view the organization&rsquo;s audit log.
            Ask an admin if you need access to security events.
          </p>
        </Card>
      </div>
    );
  }

  const sp = await searchParams;
  const page = parsePage(sp.page);
  const audit = await loadAuditPage(page);

  return (
    <div className="mx-auto max-w-5xl space-y-6">
      <Header />

      {!audit.available ? (
        <Card className="flex items-start gap-3 border-dashed p-6 text-sm">
          <ScrollText className="mt-0.5 size-4 shrink-0 text-muted-foreground" aria-hidden />
          <p className="text-muted-foreground">
            {audit.forbidden
              ? "You don't have permission to view the audit log."
              : "Audit logging isn't enabled on this deployment yet. Once the API exposes /v1/audit, recent security events will appear here."}
          </p>
        </Card>
      ) : audit.error ? (
        <Card
          role="alert"
          className="border-destructive/40 bg-destructive/5 p-6 text-sm text-destructive"
        >
          {audit.error}
        </Card>
      ) : (
        <div className="space-y-4">
          <AuditTable events={audit.events} />
          <AuditPagination
            page={audit.page}
            hasPrev={audit.hasPrev}
            hasNext={audit.hasNext}
            total={audit.total}
            pageSize={audit.pageSize}
            count={audit.events.length}
          />
        </div>
      )}
    </div>
  );
}

function Header() {
  return (
    <div className="space-y-1">
      <h1 className="flex items-center gap-2 text-2xl font-semibold tracking-tight">
        <ScrollText className="size-5 text-muted-foreground" aria-hidden />
        Audit log
      </h1>
      <p className="text-muted-foreground">
        A record of security-relevant actions in your organization — sharing
        changes, member and role changes, and access revocations. Highlighted
        rows are access-mode or security-sensitive.
      </p>
    </div>
  );
}

/** Parse a 1-based `?page=` query param into a 0-based, non-negative index. */
function parsePage(raw: string | string[] | undefined): number {
  const v = Array.isArray(raw) ? raw[0] : raw;
  const n = Number(v);
  if (!Number.isFinite(n) || n <= 1) return 0;
  return Math.floor(n) - 1;
}
