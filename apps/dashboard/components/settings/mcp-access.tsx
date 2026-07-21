"use client";

import * as React from "react";
import { useRouter } from "next/navigation";
import { AlertTriangle, Loader2, Plug } from "lucide-react";

import { setMcpEnabledAction } from "@/app/(app)/settings/actions";
import { McpConnectDialog } from "@/components/settings/mcp-connect-dialog";
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
import { Switch } from "@/components/ui/switch";

/**
 * The org "Allow MCP access" control plus the "Connect an AI tool" launcher.
 *
 * The toggle (owner/admin only) flips org_meta.mcp_enabled via PATCH /v1/orgs/mcp.
 * Disabling cuts off the Dropway MCP server for the whole org immediately, the
 * resource server re-checks the flag per request, so already-connected tools stop
 * working at once, so it goes through a confirmation first.
 *
 * The "Connect" button is available to every member (anyone can wire up their own AI
 * tool); it opens the per-tool instructions. When MCP is disabled org-wide the
 * connect launcher is shown but flagged as currently off.
 */
export function McpAccess({
  initialEnabled,
  canManage,
  mcpUrl,
}: {
  initialEnabled: boolean;
  canManage: boolean;
  mcpUrl: string;
}) {
  const router = useRouter();

  const [enabled, setEnabled] = React.useState(initialEnabled);
  const [pending, setPending] = React.useState(false);
  const [confirmOpen, setConfirmOpen] = React.useState(false);
  const [connectOpen, setConnectOpen] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);
  const [notice, setNotice] = React.useState<string | null>(null);

  async function commit(next: boolean) {
    setError(null);
    setNotice(null);
    setPending(true);
    const result = await setMcpEnabledAction({ enabled: next });
    if (result.ok) {
      setEnabled(result.mcpEnabled);
      setNotice(
        result.mcpEnabled
          ? "MCP access enabled."
          : "MCP access disabled. Connected AI tools can no longer reach this organization.",
      );
      router.refresh();
    } else {
      setError(result.message);
    }
    setPending(false);
  }

  function onToggle(next: boolean) {
    setError(null);
    setNotice(null);
    if (!next) {
      // Disabling cuts off every connected tool → confirm first.
      setConfirmOpen(true);
      return;
    }
    void commit(true);
  }

  return (
    <div className="space-y-4">
      {/* Connect launcher, available to everyone. */}
      <div className="flex items-start justify-between gap-4">
        <div className="space-y-1">
          <p className="flex items-center gap-2 text-sm font-medium text-foreground">
            Connect an AI tool
            {!enabled && (
              <Badge variant="muted" className="font-normal">
                Disabled for this org
              </Badge>
            )}
          </p>
          <p className="text-sm text-muted-foreground">
            Add Dropway as an MCP connector in Claude, Cursor, or Codex.
          </p>
        </div>
        <Button
          type="button"
          variant="outline"
          onClick={() => setConnectOpen(true)}
          className="shrink-0"
        >
          <Plug className="size-4" aria-hidden />
          Connect
        </Button>
      </div>

      {/* Org-wide toggle, owner/admin only. */}
      {canManage ? (
        <div className="flex items-start justify-between gap-4 border-t border-border pt-4">
          <div className="space-y-1">
            <p id="mcp-label" className="text-sm font-medium text-foreground">
              Allow MCP access
            </p>
            <p id="mcp-desc" className="text-sm text-muted-foreground">
              Members can connect AI tools through the MCP server. Turning it
              off blocks all access immediately.
            </p>
          </div>
          <div className="pt-0.5">
            {pending ? (
              <span className="grid size-6 place-items-center text-muted-foreground">
                <Loader2 className="size-4 animate-spin" aria-hidden />
              </span>
            ) : (
              <Switch
                checked={enabled}
                onCheckedChange={onToggle}
                disabled={pending}
                aria-labelledby="mcp-label"
                aria-describedby="mcp-desc"
              />
            )}
          </div>
        </div>
      ) : (
        <div className="flex items-center justify-between gap-3 border-t border-border pt-4 text-sm">
          <span className="text-muted-foreground">
            Only owners and admins can change org-wide MCP access.
          </span>
          <Badge variant={enabled ? "success" : "muted"} className="font-normal">
            {enabled ? "Enabled" : "Disabled"}
          </Badge>
        </div>
      )}

      {error && (
        <p
          role="alert"
          className="rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive"
        >
          {error}
        </p>
      )}
      {notice && (
        <p
          role="status"
          className="rounded-md border border-emerald-500/30 bg-emerald-500/10 px-3 py-2 text-sm text-emerald-700 dark:text-emerald-400"
        >
          {notice}
        </p>
      )}

      <McpConnectDialog
        open={connectOpen}
        onOpenChange={setConnectOpen}
        mcpUrl={mcpUrl}
      />

      <Dialog
        open={confirmOpen}
        onOpenChange={(next) => {
          if (!next) setConfirmOpen(false);
        }}
      >
        <DialogHeader>
          <div className="mb-1 grid size-10 place-items-center rounded-lg bg-amber-500/10 text-amber-600 dark:text-amber-400">
            <AlertTriangle className="size-5" aria-hidden />
          </div>
          <DialogTitle>Disable MCP access?</DialogTitle>
          <DialogDescription>
            Every connected AI tool immediately loses access. Existing OAuth
            grants stop working. You can re-enable it any time.
          </DialogDescription>
        </DialogHeader>
        <DialogBody />
        <DialogFooter>
          <Button
            type="button"
            variant="outline"
            onClick={() => setConfirmOpen(false)}
            disabled={pending}
          >
            Cancel
          </Button>
          <Button
            type="button"
            variant="destructive"
            onClick={() => {
              setConfirmOpen(false);
              void commit(false);
            }}
            disabled={pending}
            aria-busy={pending}
          >
            {pending ? <Loader2 className="animate-spin" aria-hidden /> : null}
            Disable MCP access
          </Button>
        </DialogFooter>
      </Dialog>
    </div>
  );
}
