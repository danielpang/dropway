"use client";

import { useState } from "react";

import { CodeBlock } from "@/components/docs/code-block";
import { cn } from "@/lib/utils";

/**
 * Tabbed install instructions for connecting an MCP client to the Dropway MCP
 * server. One panel per client, ordered most → least guided for a non-technical
 * user: Claude Cowork, Claude Code, Cursor, Codex. The full connector URL is
 * passed in (`${MCP_URL}/mcp`) so it stays in sync with the page that renders it.
 */

type TabKey = "cowork" | "claude-code" | "cursor" | "codex";

const TABS: { key: TabKey; label: string }[] = [
  { key: "cowork", label: "Claude Cowork" },
  { key: "claude-code", label: "Claude Code" },
  { key: "cursor", label: "Cursor" },
  { key: "codex", label: "Codex" },
];

export function ConnectTabs({ mcpUrl }: { mcpUrl: string }) {
  const [tab, setTab] = useState<TabKey>("cowork");

  return (
    <div>
      <div
        role="tablist"
        aria-label="MCP client"
        className="flex flex-wrap gap-1 rounded-xl border border-border bg-muted/40 p-1"
      >
        {TABS.map((t) => (
          <button
            key={t.key}
            role="tab"
            type="button"
            aria-selected={tab === t.key}
            onClick={() => setTab(t.key)}
            className={cn(
              "flex-1 whitespace-nowrap rounded-lg px-3 py-2 text-sm font-medium transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
              tab === t.key
                ? "bg-card text-foreground shadow-sm"
                : "text-muted-foreground hover:text-foreground",
            )}
          >
            {t.label}
          </button>
        ))}
      </div>

      <div className="mt-5">
        {tab === "cowork" && <Cowork url={mcpUrl} />}
        {tab === "claude-code" && <ClaudeCode url={mcpUrl} />}
        {tab === "cursor" && <Cursor url={mcpUrl} />}
        {tab === "codex" && <Codex url={mcpUrl} />}
      </div>
    </div>
  );
}

function Steps({ children }: { children: React.ReactNode }) {
  return (
    <ol className="space-y-4 text-sm leading-relaxed text-muted-foreground [counter-reset:step]">
      {children}
    </ol>
  );
}

function Step({ children }: { children: React.ReactNode }) {
  return (
    <li className="relative pl-9 [counter-increment:step] before:absolute before:left-0 before:top-0 before:flex before:h-6 before:w-6 before:items-center before:justify-center before:rounded-full before:bg-primary/10 before:font-mono before:text-xs before:font-semibold before:text-primary before:[content:counter(step)]">
      {children}
    </li>
  );
}

function Kbd({ children }: { children: React.ReactNode }) {
  return (
    <kbd className="rounded border border-border bg-muted px-1.5 py-0.5 font-mono text-xs text-foreground">
      {children}
    </kbd>
  );
}

const Authorize = () => (
  <>
    sign in to Dropway and approve{" "}
    <span className="font-medium text-foreground">
      &ldquo;Authorize MCP access&rdquo;
    </span>
  </>
);

function Cowork({ url }: { url: string }) {
  return (
    <Steps>
      <Step>Open Claude and go to Settings → Connectors.</Step>
      <Step>
        Click <Kbd>Add custom connector</Kbd> and paste the MCP server URL:
        <CodeBlock className="mt-2" label="MCP server URL" code={url} />
      </Step>
      <Step>
        Click <Kbd>Connect</Kbd>, then <Authorize />. Dropway now appears as a
        connector and can browse and deploy your sites on request.
      </Step>
    </Steps>
  );
}

function ClaudeCode({ url }: { url: string }) {
  return (
    <Steps>
      <Step>
        Register the server in your terminal:
        <CodeBlock
          className="mt-2"
          label="terminal"
          code={`claude mcp add --transport http dropway ${url}`}
        />
      </Step>
      <Step>
        In Claude Code, run <Kbd>/mcp</Kbd>, select{" "}
        <span className="font-medium text-foreground">dropway</span>, and choose{" "}
        <Kbd>Authenticate</Kbd>.
      </Step>
      <Step>
        Your browser opens. <Authorize />. Claude Code reconnects automatically.
      </Step>
    </Steps>
  );
}

function Cursor({ url }: { url: string }) {
  const config = JSON.stringify({ mcpServers: { dropway: { url } } }, null, 2);
  return (
    <Steps>
      <Step>
        Open <Kbd>~/.cursor/mcp.json</Kbd> (or Settings → MCP →{" "}
        <Kbd>Add new MCP server</Kbd>) and add:
        <CodeBlock className="mt-2" label="~/.cursor/mcp.json" code={config} />
      </Step>
      <Step>
        Cursor detects the server and opens your browser to authorize.{" "}
        <Authorize />.
      </Step>
    </Steps>
  );
}

function Codex({ url }: { url: string }) {
  const config = `[mcp_servers.dropway]\nurl = "${url}"`;
  return (
    <Steps>
      <Step>
        Add the server to <Kbd>~/.codex/config.toml</Kbd>:
        <CodeBlock className="mt-2" label="~/.codex/config.toml" code={config} />
      </Step>
      <Step>
        Start Codex. It connects over streamable HTTP and opens your browser to
        authorize. <Authorize />.
      </Step>
    </Steps>
  );
}
