"use client";

import * as React from "react";
import { useRouter } from "next/navigation";
import {
  CheckCircle2,
  Copy,
  Loader2,
  Plus,
  RefreshCw,
  Trash2,
  XCircle,
} from "lucide-react";

import {
  addDomainAction,
  refreshDomainStatusAction,
  removeDomainAction,
} from "@/app/(app)/sites/[id]/domains/actions";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogBody,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import type { Domain } from "@/lib/api";

const HOSTNAME_RE =
  /^(?=.{1,253}$)(?!-)[a-z0-9-]{1,63}(?<!-)(?:\.(?!-)[a-z0-9-]{1,63}(?<!-))+$/;

/** Domains in these states are still in flight → keep polling. */
function isInFlight(d: Domain): boolean {
  return (
    d.verify_status === "pending" ||
    d.verify_status === "verifying" ||
    (d.verify_status === "verified" && d.tls_status === "pending")
  );
}

export function DomainsManager({
  siteId,
  initialDomains,
  disabled,
}: {
  siteId: string;
  initialDomains: Domain[];
  disabled: boolean;
}) {
  const router = useRouter();
  const [domains, setDomains] = React.useState<Domain[]>(initialDomains);
  const [hostname, setHostname] = React.useState("");
  const [adding, setAdding] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);
  const [touched, setTouched] = React.useState(false);

  React.useEffect(() => setDomains(initialDomains), [initialDomains]);

  const valid = HOSTNAME_RE.test(hostname.trim().toLowerCase());

  function upsert(updated: Domain) {
    setDomains((prev) => {
      const idx = prev.findIndex((d) => d.id === updated.id);
      if (idx === -1) return [...prev, updated];
      const next = [...prev];
      next[idx] = updated;
      return next;
    });
  }

  function remove(id: string) {
    setDomains((prev) => prev.filter((d) => d.id !== id));
  }

  async function onAdd(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    setTouched(true);
    if (!valid) return;

    setAdding(true);
    const result = await addDomainAction({ siteId, hostname });
    if (result.ok) {
      upsert(result.domain);
      setHostname("");
      setTouched(false);
      router.refresh();
    } else {
      setError(result.message);
    }
    setAdding(false);
  }

  return (
    <div className="space-y-5">
      {!disabled && (
        <form onSubmit={onAdd} className="space-y-2">
          <Label htmlFor="domain-hostname" className="sr-only">
            Domain
          </Label>
          <div className="flex flex-col gap-2 sm:flex-row">
            <Input
              id="domain-hostname"
              name="domain-hostname"
              placeholder="docs.acme.com"
              value={hostname}
              onChange={(e) => setHostname(e.target.value)}
              aria-invalid={touched && !valid}
              className="font-mono"
              disabled={adding}
            />
            <Button type="submit" disabled={adding} aria-busy={adding}>
              {adding ? (
                <Loader2 className="animate-spin" aria-hidden />
              ) : (
                <Plus aria-hidden />
              )}
              Add domain
            </Button>
          </div>
          {touched && !valid && (
            <p className="text-xs text-destructive">
              Enter a valid domain, e.g. docs.acme.com.
            </p>
          )}
          {error && (
            <p
              role="alert"
              className="rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive"
            >
              {error}
            </p>
          )}
        </form>
      )}

      {domains.length === 0 ? (
        <p className="rounded-md border border-dashed border-border px-3 py-6 text-center text-sm text-muted-foreground">
          No custom domains yet.
        </p>
      ) : (
        <ul className="space-y-3">
          {domains.map((domain) => (
            <li key={domain.id ?? domain.hostname}>
              <DomainCard
                domain={domain}
                siteId={siteId}
                canManage={!disabled}
                onUpdate={upsert}
                onRemove={remove}
              />
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

/** One domain: hostname, status badges, the DCV record, and a status poller. */
function DomainCard({
  domain,
  siteId,
  canManage,
  onUpdate,
  onRemove,
}: {
  domain: Domain;
  siteId: string;
  canManage: boolean;
  onUpdate: (d: Domain) => void;
  onRemove: (id: string) => void;
}) {
  const router = useRouter();
  const [refreshing, setRefreshing] = React.useState(false);
  const [copied, setCopied] = React.useState(false);
  const [pollError, setPollError] = React.useState<string | null>(null);
  const [confirmRemove, setConfirmRemove] = React.useState(false);
  const [removing, setRemoving] = React.useState(false);

  async function onRemoveConfirmed() {
    if (!domain.id) return;
    setRemoving(true);
    setPollError(null);
    const result = await removeDomainAction({ siteId, domainId: domain.id });
    if (result.ok) {
      onRemove(domain.id);
      setConfirmRemove(false);
      router.refresh();
    } else {
      setPollError(result.message);
      setRemoving(false);
    }
  }

  const inFlight = isInFlight(domain);

  const refresh = React.useCallback(
    async (manual: boolean) => {
      if (!domain.id) return;
      if (manual) setRefreshing(true);
      const result = await refreshDomainStatusAction(domain.id);
      if (result.ok) {
        onUpdate(result.domain);
        setPollError(null);
        if (manual) router.refresh();
      } else {
        setPollError(result.message);
      }
      if (manual) setRefreshing(false);
    },
    [domain.id, onUpdate, router],
  );

  // Auto-poll while the domain is still verifying (every 10s). Stops once the
  // domain reaches a terminal state (active or failed).
  React.useEffect(() => {
    if (!inFlight || !domain.id) return;
    const t = setInterval(() => void refresh(false), 10_000);
    return () => clearInterval(t);
  }, [inFlight, domain.id, refresh]);

  async function copyRecord() {
    if (!domain.dcv_record) return;
    try {
      await navigator.clipboard.writeText(domain.dcv_record);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // Clipboard may be blocked; the record is visible to copy manually.
    }
  }

  const verified = domain.verify_status === "verified";
  const failed =
    domain.verify_status === "failed" || domain.tls_status === "failed";
  const active = verified && domain.tls_status === "issued";

  return (
    <div className="rounded-lg border border-border bg-card p-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <span className="font-mono text-sm font-medium text-foreground">
          {domain.hostname}
        </span>
        <div className="flex items-center gap-2">
          <StatusBadge active={active} failed={failed} inFlight={inFlight} />
          {domain.id && (
            <Button
              type="button"
              variant="ghost"
              size="sm"
              onClick={() => void refresh(true)}
              disabled={refreshing}
              aria-label="Refresh verification status"
            >
              <RefreshCw
                className={refreshing ? "animate-spin" : undefined}
                aria-hidden
              />
              {refreshing ? "Checking" : "Refresh"}
            </Button>
          )}
          {canManage && domain.id && (
            <Button
              type="button"
              variant="ghost"
              size="icon"
              className="size-9 text-muted-foreground hover:text-destructive"
              onClick={() => setConfirmRemove(true)}
              aria-label={`Remove ${domain.hostname}`}
            >
              <Trash2 aria-hidden />
            </Button>
          )}
        </div>
      </div>

      {/* DCV record to create (shown until active) */}
      {!active && domain.dcv_record && (
        <div className="mt-3 space-y-1.5">
          <p className="text-xs text-muted-foreground">
            Create this DNS record at your registrar, then verify:
          </p>
          <div className="flex items-center justify-between gap-2 rounded-md border border-border bg-muted/50 px-3 py-2">
            <code className="min-w-0 truncate font-mono text-xs text-foreground">
              {domain.dcv_record}
            </code>
            <Button
              type="button"
              variant="ghost"
              size="icon"
              className="size-7 shrink-0"
              onClick={copyRecord}
              aria-label="Copy DNS record"
            >
              {copied ? (
                <CheckCircle2 className="text-emerald-500" aria-hidden />
              ) : (
                <Copy aria-hidden />
              )}
            </Button>
          </div>
        </div>
      )}

      {/* Detailed sub-states */}
      <div className="mt-3 flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted-foreground">
        <span>
          Verification:{" "}
          <span className="font-medium text-foreground">
            {domain.verify_status ?? "pending"}
          </span>
        </span>
        <span>
          TLS:{" "}
          <span className="font-medium text-foreground">
            {domain.tls_status ?? "pending"}
          </span>
        </span>
      </div>

      {active && (
        <p className="mt-2 text-xs text-emerald-700 dark:text-emerald-400">
          Live — your site now serves from this domain.
        </p>
      )}
      {pollError && (
        <p role="alert" className="mt-2 text-xs text-destructive">
          {pollError}
        </p>
      )}

      <Dialog open={confirmRemove} onOpenChange={(o) => !removing && setConfirmRemove(o)}>
        <DialogHeader>
          <DialogTitle>Remove custom domain?</DialogTitle>
        </DialogHeader>
        <DialogBody>
          <p className="text-sm text-muted-foreground">
            <span className="font-mono text-foreground">{domain.hostname}</span>{" "}
            will stop serving this site. You can add it again later, but it will
            need to be re-verified.
          </p>
        </DialogBody>
        <DialogFooter>
          <Button
            type="button"
            variant="ghost"
            onClick={() => setConfirmRemove(false)}
            disabled={removing}
          >
            Cancel
          </Button>
          <Button
            type="button"
            variant="destructive"
            onClick={() => void onRemoveConfirmed()}
            disabled={removing}
            aria-busy={removing}
          >
            {removing ? <Loader2 className="animate-spin" aria-hidden /> : <Trash2 aria-hidden />}
            Remove
          </Button>
        </DialogFooter>
      </Dialog>
    </div>
  );
}

function StatusBadge({
  active,
  failed,
  inFlight,
}: {
  active: boolean;
  failed: boolean;
  inFlight: boolean;
}) {
  if (active) {
    return (
      <Badge variant="success">
        <CheckCircle2 className="size-3" aria-hidden />
        Active
      </Badge>
    );
  }
  if (failed) {
    return (
      <Badge variant="outline" className="border-destructive/40 text-destructive">
        <XCircle className="size-3" aria-hidden />
        Failed
      </Badge>
    );
  }
  if (inFlight) {
    return (
      <Badge variant="muted">
        <Loader2 className="size-3 animate-spin" aria-hidden />
        Verifying
      </Badge>
    );
  }
  return <Badge variant="muted">Pending</Badge>;
}
