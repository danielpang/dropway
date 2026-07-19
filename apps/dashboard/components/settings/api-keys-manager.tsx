"use client";

import * as React from "react";
import { useRouter } from "next/navigation";
import {
  AlertTriangle,
  Check,
  Copy,
  KeyRound,
  Loader2,
  Trash2,
} from "lucide-react";

import {
  createApiKeyAction,
  revokeApiKeyAction,
  setApiKeysEnabledAction,
} from "@/app/(app)/settings/api-keys/actions";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogBody,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import type { ApiKey, ApiKeyCreated } from "@/lib/api";

/**
 * The org API-keys management surface (owner/admin only). It bundles:
 *  - the org-wide kill switch (PATCH /v1/orgs/api-keys),
 *  - key creation with a ONE-TIME secret reveal (POST /v1/api-keys),
 *  - the key list with per-key revoke (DELETE /v1/api-keys/{id}).
 *
 * The Go API is the authz boundary and re-checks owner/admin on every write; keyed
 * callers are refused entirely (a key can't manage keys). This component only
 * renders for managers — non-managers see a read-only notice from the page.
 */
export function ApiKeysManager({
  initialKeys,
  initialEnabled,
}: {
  initialKeys: ApiKey[];
  initialEnabled: boolean;
}) {
  const router = useRouter();

  const [keys, setKeys] = React.useState<ApiKey[]>(initialKeys);
  const [enabled, setEnabled] = React.useState(initialEnabled);
  const [togglePending, setTogglePending] = React.useState(false);
  const [confirmDisableOpen, setConfirmDisableOpen] = React.useState(false);

  const [name, setName] = React.useState("");
  const [creating, setCreating] = React.useState(false);
  const [createError, setCreateError] = React.useState<string | null>(null);
  const [revealed, setRevealed] = React.useState<ApiKeyCreated | null>(null);

  const [revokeTarget, setRevokeTarget] = React.useState<ApiKey | null>(null);
  const [revokePending, setRevokePending] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);

  async function commitToggle(next: boolean) {
    setError(null);
    setTogglePending(true);
    const result = await setApiKeysEnabledAction({ enabled: next });
    if (result.ok) {
      setEnabled(result.enabled);
      router.refresh();
    } else {
      setError(result.message);
    }
    setTogglePending(false);
  }

  function onToggle(next: boolean) {
    setError(null);
    if (!next) {
      setConfirmDisableOpen(true);
      return;
    }
    void commitToggle(true);
  }

  async function onCreate(e: React.FormEvent) {
    e.preventDefault();
    setCreateError(null);
    setCreating(true);
    const result = await createApiKeyAction({ name });
    if (result.ok) {
      setRevealed(result.key);
      setKeys((prev) => [result.key, ...prev]);
      setName("");
      router.refresh();
    } else {
      setCreateError(result.message);
    }
    setCreating(false);
  }

  async function onRevoke() {
    if (!revokeTarget) return;
    setError(null);
    setRevokePending(true);
    const result = await revokeApiKeyAction({ id: revokeTarget.id });
    if (result.ok) {
      setKeys((prev) =>
        prev.map((k) => (k.id === result.key.id ? result.key : k)),
      );
      setRevokeTarget(null);
      router.refresh();
    } else {
      setError(result.message);
    }
    setRevokePending(false);
  }

  const activeKeys = keys.filter((k) => !k.revoked_at);

  return (
    <div className="space-y-6">
      {/* Kill switch */}
      <div className="flex items-start justify-between gap-4">
        <div className="space-y-1">
          <p id="apikeys-label" className="text-sm font-medium text-foreground">
            Allow API key access
          </p>
          <p id="apikeys-desc" className="text-sm text-muted-foreground">
            When on, API keys in this organization can authenticate. Turning it
            off rejects every key immediately; the keys are kept, so you can
            turn it back on any time.
          </p>
        </div>
        <div className="pt-0.5">
          {togglePending ? (
            <span className="grid size-6 place-items-center text-muted-foreground">
              <Loader2 className="size-4 animate-spin" aria-hidden />
            </span>
          ) : (
            <Switch
              checked={enabled}
              onCheckedChange={onToggle}
              disabled={togglePending}
              aria-labelledby="apikeys-label"
              aria-describedby="apikeys-desc"
            />
          )}
        </div>
      </div>

      {/* Create */}
      <form
        onSubmit={onCreate}
        className="space-y-3 border-t border-border pt-6"
      >
        <div className="space-y-1.5">
          <Label htmlFor="apikey-name">Create a key</Label>
          <p className="text-sm text-muted-foreground">
            Give it a name you&rsquo;ll recognize later (e.g. &ldquo;GitHub
            Actions&rdquo;). The secret is shown once, right after you create
            it.
          </p>
        </div>
        <div className="flex flex-col gap-2 sm:flex-row">
          <Input
            id="apikey-name"
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="GitHub Actions"
            maxLength={100}
            disabled={creating}
            className="sm:max-w-xs"
          />
          <Button type="submit" disabled={creating || name.trim() === ""}>
            {creating ? (
              <Loader2 className="animate-spin" aria-hidden />
            ) : (
              <KeyRound className="size-4" aria-hidden />
            )}
            Create key
          </Button>
        </div>
        {createError && (
          <p
            role="alert"
            className="rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive"
          >
            {createError}
          </p>
        )}
      </form>

      {/* List */}
      <div className="space-y-2 border-t border-border pt-6">
        <p className="text-sm font-medium text-foreground">
          Keys {activeKeys.length > 0 && `(${activeKeys.length} active)`}
        </p>
        {keys.length === 0 ? (
          <p className="rounded-md border border-dashed border-border px-3 py-6 text-center text-sm text-muted-foreground">
            No API keys yet.
          </p>
        ) : (
          <ul className="divide-y divide-border rounded-md border border-border">
            {keys.map((k) => (
              <li
                key={k.id}
                className="flex items-center justify-between gap-4 px-3 py-3"
              >
                <div className="min-w-0 space-y-0.5">
                  <p className="flex items-center gap-2 text-sm font-medium text-foreground">
                    <span className="truncate">{k.name}</span>
                    {k.revoked_at && (
                      <Badge variant="muted" className="font-normal">
                        Revoked
                      </Badge>
                    )}
                  </p>
                  <p className="truncate font-mono text-xs text-muted-foreground">
                    {k.key_prefix}… · created {formatDate(k.created_at)}
                    {k.last_used_at
                      ? ` · last used ${formatDate(k.last_used_at)}`
                      : " · never used"}
                  </p>
                </div>
                {!k.revoked_at && (
                  <Button
                    type="button"
                    variant="ghost"
                    size="sm"
                    className="shrink-0 text-destructive hover:text-destructive"
                    onClick={() => setRevokeTarget(k)}
                  >
                    <Trash2 className="size-4" aria-hidden />
                    Revoke
                  </Button>
                )}
              </li>
            ))}
          </ul>
        )}
      </div>

      {error && (
        <p
          role="alert"
          className="rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive"
        >
          {error}
        </p>
      )}

      {/* One-time reveal */}
      <RevealDialog keyValue={revealed} onClose={() => setRevealed(null)} />

      {/* Revoke confirm */}
      <Dialog
        open={revokeTarget !== null}
        onOpenChange={(next) => {
          if (!next) setRevokeTarget(null);
        }}
      >
        <DialogHeader>
          <div className="mb-1 grid size-10 place-items-center rounded-lg bg-amber-500/10 text-amber-600 dark:text-amber-400">
            <AlertTriangle className="size-5" aria-hidden />
          </div>
          <DialogTitle>Revoke this key?</DialogTitle>
          <DialogDescription>
            {revokeTarget ? (
              <>
                <span className="font-medium text-foreground">
                  {revokeTarget.name}
                </span>{" "}
                ({revokeTarget.key_prefix}…) will stop working immediately. Any
                CI job or script using it will start failing. This cannot be
                undone.
              </>
            ) : null}
          </DialogDescription>
        </DialogHeader>
        <DialogBody />
        <DialogFooter>
          <Button
            type="button"
            variant="outline"
            onClick={() => setRevokeTarget(null)}
            disabled={revokePending}
          >
            Cancel
          </Button>
          <Button
            type="button"
            variant="destructive"
            onClick={() => void onRevoke()}
            disabled={revokePending}
            aria-busy={revokePending}
          >
            {revokePending ? (
              <Loader2 className="animate-spin" aria-hidden />
            ) : null}
            Revoke key
          </Button>
        </DialogFooter>
      </Dialog>

      {/* Disable-kill-switch confirm */}
      <Dialog
        open={confirmDisableOpen}
        onOpenChange={(next) => {
          if (!next) setConfirmDisableOpen(false);
        }}
      >
        <DialogHeader>
          <div className="mb-1 grid size-10 place-items-center rounded-lg bg-amber-500/10 text-amber-600 dark:text-amber-400">
            <AlertTriangle className="size-5" aria-hidden />
          </div>
          <DialogTitle>Disable API key access?</DialogTitle>
          <DialogDescription>
            Every API key in this organization will stop working immediately,
            and any CI job or script using one will start failing. The keys are
            kept — you can re-enable access at any time.
          </DialogDescription>
        </DialogHeader>
        <DialogBody />
        <DialogFooter>
          <Button
            type="button"
            variant="outline"
            onClick={() => setConfirmDisableOpen(false)}
            disabled={togglePending}
          >
            Cancel
          </Button>
          <Button
            type="button"
            variant="destructive"
            onClick={() => {
              setConfirmDisableOpen(false);
              void commitToggle(false);
            }}
            disabled={togglePending}
          >
            Disable access
          </Button>
        </DialogFooter>
      </Dialog>
    </div>
  );
}

/**
 * The one-time secret reveal. Because the plaintext is never recoverable, the
 * dialog makes copying easy and warns that this is the only chance to save it.
 */
function RevealDialog({
  keyValue,
  onClose,
}: {
  keyValue: ApiKeyCreated | null;
  onClose: () => void;
}) {
  const [copied, setCopied] = React.useState(false);

  React.useEffect(() => {
    if (keyValue) setCopied(false);
  }, [keyValue]);

  async function copy() {
    if (!keyValue) return;
    try {
      await navigator.clipboard.writeText(keyValue.key);
      setCopied(true);
    } catch {
      // Clipboard blocked (e.g. insecure context) — the value is still visible
      // for the user to select manually.
    }
  }

  return (
    <Dialog
      open={keyValue !== null}
      onOpenChange={(next) => {
        if (!next) onClose();
      }}
    >
      <DialogHeader>
        <div className="mb-1 grid size-10 place-items-center rounded-lg bg-emerald-500/10 text-emerald-600 dark:text-emerald-400">
          <KeyRound className="size-5" aria-hidden />
        </div>
        <DialogTitle>Copy your API key now</DialogTitle>
        <DialogDescription>
          This is the only time the full secret is shown. Store it somewhere
          safe (a CI secret, a password manager). You won&rsquo;t be able to see
          it again — if you lose it, revoke the key and create a new one.
        </DialogDescription>
      </DialogHeader>
      <DialogBody>
        {keyValue && (
          <div className="flex items-center gap-2">
            <code className="min-w-0 flex-1 truncate rounded-md border border-border bg-muted px-3 py-2 font-mono text-sm">
              {keyValue.key}
            </code>
            <Button type="button" variant="outline" onClick={() => void copy()}>
              {copied ? (
                <Check className="size-4 text-emerald-600" aria-hidden />
              ) : (
                <Copy className="size-4" aria-hidden />
              )}
              {copied ? "Copied" : "Copy"}
            </Button>
          </div>
        )}
      </DialogBody>
      <DialogFooter>
        <Button type="button" onClick={onClose}>
          Done
        </Button>
      </DialogFooter>
    </Dialog>
  );
}

/** Render an ISO timestamp as a short, locale-friendly date. */
function formatDate(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "unknown";
  return d.toLocaleDateString(undefined, {
    year: "numeric",
    month: "short",
    day: "numeric",
  });
}
