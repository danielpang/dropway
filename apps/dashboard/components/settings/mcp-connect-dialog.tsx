"use client";

import * as React from "react";
import { Check, Copy } from "lucide-react";

import {
  Dialog,
  DialogBody,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { cn } from "@/lib/utils";

/**
 * "Connect" instructions for the Dropway MCP server. An authenticated MCP client
 * (Claude Cowork, Claude Code, Cursor, Codex) adds the MCP URL as a custom
 * connector; on first use the client hits the server, gets a 401 pointing at the
 * dashboard authorization server (RFC 9728/8414), and runs a browser OAuth flow —
 * the user signs in and approves "Authorize MCP access". After that the client can
 * list the org's sites and read their files (scoped to the org by RLS, honoring each
 * site's sharing settings). The only thing the user needs to paste is the URL below.
 *
 * Order is intentional (most → least guided for a non-technical user): Claude Cowork,
 * Claude Code, Cursor, Codex.
 */

type ToolKey = "cowork" | "claude-code" | "cursor" | "codex";

const TOOLS: { key: ToolKey; label: string }[] = [
  { key: "cowork", label: "Claude Cowork" },
  { key: "claude-code", label: "Claude Code" },
  { key: "cursor", label: "Cursor" },
  { key: "codex", label: "Codex" },
];

export function McpConnectDialog({
  open,
  onOpenChange,
  mcpUrl,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** Public base URL of the MCP server (the connector URL is `${mcpUrl}/mcp`). */
  mcpUrl: string;
}) {
  const [tool, setTool] = React.useState<ToolKey>("cowork");
  const connectorUrl = `${mcpUrl.replace(/\/$/, "")}/mcp`;

  return (
    <Dialog open={open} onOpenChange={onOpenChange} className="max-w-2xl">
      <DialogHeader>
        <DialogTitle>Connect an AI tool</DialogTitle>
        <DialogDescription>
          Add Dropway as an MCP connector so your AI tool can browse this
          organization&rsquo;s sites and read their files. You&rsquo;ll sign in
          and approve access in your browser the first time.
        </DialogDescription>
      </DialogHeader>
      <DialogBody className="space-y-4 pb-6">
        {/* The one value every tool needs. */}
        <div className="space-y-1.5">
          <p className="text-xs font-medium uppercase tracking-wide text-muted-foreground">
            MCP server URL
          </p>
          <CopyField value={connectorUrl} />
        </div>

        {/* Tool picker. */}
        <div
          role="tablist"
          aria-label="AI tool"
          className="flex flex-wrap gap-1 rounded-lg border border-border bg-muted/40 p-1"
        >
          {TOOLS.map((t) => (
            <button
              key={t.key}
              role="tab"
              type="button"
              aria-selected={tool === t.key}
              onClick={() => setTool(t.key)}
              className={cn(
                "flex-1 rounded-md px-3 py-1.5 text-sm font-medium transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
                tool === t.key
                  ? "bg-background text-foreground shadow-sm"
                  : "text-muted-foreground hover:text-foreground",
              )}
            >
              {t.label}
            </button>
          ))}
        </div>

        {/* Per-tool steps. */}
        <div className="rounded-lg border border-border p-4">
          {tool === "cowork" && <CoworkSteps url={connectorUrl} />}
          {tool === "claude-code" && <ClaudeCodeSteps url={connectorUrl} />}
          {tool === "cursor" && <CursorSteps url={connectorUrl} />}
          {tool === "codex" && <CodexSteps url={connectorUrl} />}
        </div>
      </DialogBody>
    </Dialog>
  );
}

// --- per-tool instruction bodies ---------------------------------------------

function CoworkSteps({ url }: { url: string }) {
  return (
    <Steps>
      <li>Open Claude and go to Settings → Connectors.</li>
      <li>
        Click <Kbd>Add custom connector</Kbd>.
      </li>
      <li>
        Paste the MCP server URL above and click <Kbd>Add</Kbd>:
        <CopyField value={url} className="mt-2" />
      </li>
      <li>
        Click <Kbd>Connect</Kbd>, sign in to Dropway, and approve{" "}
        <span className="font-medium text-foreground">
          &ldquo;Authorize MCP access&rdquo;
        </span>
        .
      </li>
      <li>
        Dropway now appears as a connector — Claude can list your sites and read
        their files on request.
      </li>
    </Steps>
  );
}

function ClaudeCodeSteps({ url }: { url: string }) {
  return (
    <Steps>
      <li>Register the server (run this in your terminal):</li>
      <CodeBlock text={`claude mcp add --transport http dropway ${url}`} />
      <li>
        In Claude Code, run <Kbd>/mcp</Kbd>, select{" "}
        <span className="font-medium text-foreground">dropway</span>, and choose{" "}
        <Kbd>Authenticate</Kbd>.
      </li>
      <li>
        Your browser opens — sign in to Dropway and approve{" "}
        <span className="font-medium text-foreground">
          &ldquo;Authorize MCP access&rdquo;
        </span>
        . Claude Code reconnects automatically.
      </li>
    </Steps>
  );
}

function CursorSteps({ url }: { url: string }) {
  const config = JSON.stringify(
    { mcpServers: { dropway: { url } } },
    null,
    2,
  );
  return (
    <Steps>
      <li>
        Open <Kbd>~/.cursor/mcp.json</Kbd> (or Settings → MCP →{" "}
        <Kbd>Add new MCP server</Kbd>) and add:
      </li>
      <CodeBlock text={config} />
      <li>
        Cursor detects the server and opens your browser to authorize — sign in
        to Dropway and approve{" "}
        <span className="font-medium text-foreground">
          &ldquo;Authorize MCP access&rdquo;
        </span>
        .
      </li>
    </Steps>
  );
}

function CodexSteps({ url }: { url: string }) {
  const config = `[mcp_servers.dropway]\nurl = "${url}"`;
  return (
    <Steps>
      <li>
        Add the server to <Kbd>~/.codex/config.toml</Kbd>:
      </li>
      <CodeBlock text={config} />
      <li>
        Start Codex. It connects over streamable HTTP and opens your browser to
        authorize — sign in to Dropway and approve{" "}
        <span className="font-medium text-foreground">
          &ldquo;Authorize MCP access&rdquo;
        </span>
        .
      </li>
    </Steps>
  );
}

// --- small shared UI ---------------------------------------------------------

function Steps({ children }: { children: React.ReactNode }) {
  return (
    <ol className="list-decimal space-y-3 pl-5 text-sm text-muted-foreground marker:text-muted-foreground/70">
      {children}
    </ol>
  );
}

function Kbd({ children }: { children: React.ReactNode }) {
  return (
    <kbd className="rounded border border-border bg-muted px-1.5 py-0.5 font-mono text-xs text-foreground">
      {children}
    </kbd>
  );
}

function CopyField({
  value,
  className,
}: {
  value: string;
  className?: string;
}) {
  const [copied, setCopied] = React.useState(false);
  async function copy() {
    try {
      await navigator.clipboard.writeText(value);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // Clipboard may be unavailable (insecure context); fail silently.
    }
  }
  return (
    <div
      className={cn(
        "flex items-center gap-2 rounded-md border border-border bg-muted/50 px-3 py-2",
        className,
      )}
    >
      <code className="min-w-0 flex-1 truncate font-mono text-xs text-foreground">
        {value}
      </code>
      <button
        type="button"
        onClick={copy}
        aria-label={copied ? "Copied" : "Copy"}
        className="shrink-0 rounded-sm p-1 text-muted-foreground transition-colors hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background"
      >
        {copied ? (
          <Check className="size-3.5 text-emerald-500" aria-hidden />
        ) : (
          <Copy className="size-3.5" aria-hidden />
        )}
      </button>
    </div>
  );
}

function CodeBlock({ text }: { text: string }) {
  const [copied, setCopied] = React.useState(false);
  async function copy() {
    try {
      await navigator.clipboard.writeText(text);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // Clipboard may be unavailable (insecure context); fail silently.
    }
  }
  return (
    <div className="relative rounded-md border border-border bg-muted/50">
      <pre className="overflow-x-auto p-3 pr-10 font-mono text-xs leading-relaxed text-foreground">
        {text}
      </pre>
      <button
        type="button"
        onClick={copy}
        aria-label={copied ? "Copied" : "Copy"}
        className="absolute right-2 top-2 rounded-sm p-1 text-muted-foreground transition-colors hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background"
      >
        {copied ? (
          <Check className="size-3.5 text-emerald-500" aria-hidden />
        ) : (
          <Copy className="size-3.5" aria-hidden />
        )}
      </button>
    </div>
  );
}
