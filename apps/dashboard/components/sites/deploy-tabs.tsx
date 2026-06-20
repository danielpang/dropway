"use client";

import * as React from "react";
import Link from "next/link";
import { ArrowRight, Bot, Terminal } from "lucide-react";

import { ConnectTabs } from "@/components/docs/connect-tabs";
import { DeployInstructions } from "@/components/sites/deploy-instructions";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { cn } from "@/lib/utils";

/**
 * "More ways to deploy", a tabbed panel offering the two non-dashboard deploy
 * paths. Defaults to MCP (Deploy via an AI assistant) because most users are
 * non-technical and will ship from a chat tool before reaching for a terminal;
 * the CLI tab carries the slug-aware commands. Each tab links to its full
 * in-app reference (/mcp, /cli) so nothing redirects off the dashboard.
 */
type TabKey = "mcp" | "cli";

const TABS: { key: TabKey; label: string; icon: typeof Bot }[] = [
  { key: "mcp", label: "Deploy via MCP", icon: Bot },
  { key: "cli", label: "Deploy via CLI", icon: Terminal },
];

export function DeployTabs({
  slug,
  mcpConnectorUrl,
}: {
  slug: string;
  /** Full MCP connector URL (`${MCP_URL}/mcp`) for the connect instructions. */
  mcpConnectorUrl: string;
}) {
  const [tab, setTab] = React.useState<TabKey>("mcp");

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">More ways to deploy</CardTitle>
        <CardDescription>
          {tab === "mcp"
            ? "Connect an AI assistant once, then just ask it to deploy, it calls deploy_site and hands back the live URL."
            : "Prefer the terminal? Push the same folder with one command. Each deploy prints a version id you can publish or roll back to."}
        </CardDescription>
        <div
          role="tablist"
          aria-label="Deploy method"
          className="mt-2 flex gap-1 rounded-lg border border-border bg-muted/40 p-1"
        >
          {TABS.map((t) => {
            const Icon = t.icon;
            const active = tab === t.key;
            return (
              <button
                key={t.key}
                role="tab"
                type="button"
                aria-selected={active}
                onClick={() => setTab(t.key)}
                className={cn(
                  // No whitespace-nowrap: on very narrow phones the label wraps
                  // inside the button rather than forcing the card off-screen.
                  "flex flex-1 items-center justify-center gap-1.5 rounded-md px-2.5 py-1.5 text-center text-sm font-medium transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring sm:px-3",
                  active
                    ? "bg-background text-foreground shadow-sm"
                    : "text-muted-foreground hover:text-foreground",
                )}
              >
                <Icon className="size-4 shrink-0" aria-hidden />
                {t.label}
              </button>
            );
          })}
        </div>
      </CardHeader>
      <CardContent className="space-y-5">
        {tab === "mcp" ? (
          <>
            <ConnectTabs mcpUrl={mcpConnectorUrl} />
            <ReferenceLink href="/mcp" label="Full MCP reference" />
          </>
        ) : (
          <>
            <DeployInstructions slug={slug} />
            <ReferenceLink href="/cli" label="Full CLI reference" />
          </>
        )}
      </CardContent>
    </Card>
  );
}

function ReferenceLink({ href, label }: { href: string; label: string }) {
  return (
    <Link
      href={href}
      className="inline-flex items-center gap-1.5 rounded-sm text-sm font-medium text-primary underline-offset-4 transition-colors hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background"
    >
      {label}
      <ArrowRight className="size-3.5" aria-hidden />
    </Link>
  );
}
