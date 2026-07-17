import type { Metadata } from "next";
import Link from "next/link";
import { ArrowLeft, Terminal } from "lucide-react";

import { CodeBlock } from "@/components/docs/code-block";
import { ConnectTabs } from "@/components/docs/connect-tabs";
import { Callout, Code, DocHero, DocTable, Section } from "@/components/docs/doc";
import { Button } from "@/components/ui/button";
import { MCP_URL } from "@/lib/env";

export const metadata: Metadata = {
  title: "MCP reference",
  description:
    "Connect the Dropway MCP server to Claude, Cursor, or Codex. Let an AI assistant browse, create, deploy, and re-share your sites, and share the chat behind a build, scoped to your org with OAuth.",
};

/**
 * In-app MCP reference. Mirrors the dropway-www /mcp page so the content matches,
 * but renders inside the authenticated app shell, signed-in users get the docs
 * without being bounced to the marketing site. The connector URL is derived from
 * the same MCP_URL env the Settings "Connect" dialog uses.
 */
export default function McpReferencePage() {
  const connectorUrl = `${MCP_URL.replace(/\/$/, "")}/mcp`;

  return (
    <div className="mx-auto max-w-3xl space-y-14">
      <div className="space-y-6">
        <Link
          href="/dashboard"
          className="inline-flex items-center gap-1.5 rounded-sm text-sm text-muted-foreground transition-colors hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background"
        >
          <ArrowLeft className="size-4" aria-hidden />
          Back to sites
        </Link>
        <DocHero
          eyebrow="MCP reference"
          title="The Dropway MCP server"
          lead="Connect Dropway to your AI tools over the Model Context Protocol. An assistant can browse, create, deploy, and re-share your sites, all scoped to your organization and gated by OAuth."
        />
      </div>

      <Section
        id="connect"
        title="Connect your tool"
        lead="Add Dropway as a custom connector. The first time you use it, a browser tab opens to sign in and approve access. No API keys to copy."
      >
        <ConnectTabs mcpUrl={connectorUrl} />
        <p className="pt-1">All four use the same endpoint:</p>
        <CodeBlock label="MCP server URL" code={connectorUrl} />
      </Section>

      <Section
        id="tools"
        title="What the assistant can do"
        lead="Reads run directly off your org's data; writes go through the same authorization, quota, and audit as the dashboard."
      >
        <DocTable
          head={["Tool", "What it does"]}
          rows={[
            [
              <Code key="t">list_sites</Code>,
              "List your sites with their access mode, live status, and URL.",
            ],
            [
              <Code key="t">list_files</Code>,
              "List the files in a site's current version.",
            ],
            [<Code key="t">read_file</Code>, "Read one file from a site."],
            [
              <Code key="t">download_site</Code>,
              "Download every file of a site at once.",
            ],
            [<Code key="t">create_site</Code>, "Create a new site."],
            [
              <Code key="t">deploy_site</Code>,
              "Upload files and publish them to a live URL.",
            ],
            [
              <Code key="t">set_site_access</Code>,
              "Change a site's sharing (public, org-only, password, or allowlist). Owner/admin only.",
            ],
            [
              <Code key="t">list_skills</Code>,
              "List/search the org's shared skills, filterable by folder or presets.",
            ],
            [
              <Code key="t">download_skill</Code>,
              "Download a shared skill's files (for .claude/skills/).",
            ],
            [
              <Code key="t">download_skill_folder</Code>,
              "Download every skill in a folder at once (e.g. the preset starters).",
            ],
            [
              <Code key="t">upload_skill</Code>,
              "Share a skill (SKILL.md + files) with your org, optionally into folders.",
            ],
            [
              <Code key="t">share_chat</Code>,
              "Share the session transcript as a chat log: to your org's library, or attached to a site as its “How this was made” panel.",
            ],
            [
              <Code key="t">append_chat</Code>,
              "Add follow-up turns or action annotations to a shared chat as the work continues.",
            ],
            [
              <Code key="t">get_site_chat</Code>,
              "Read the shared chat log attached to a site (the transcript behind its “How this was made” panel).",
            ],
          ]}
        />
      </Section>

      <Section
        id="chat"
        title="Share the story behind a site"
        lead="When an assistant builds and deploys a site, it can publish the conversation that produced it alongside the artifact."
      >
        <p>
          Call <Code>share_chat</Code> to save the session transcript as a Dropway
          chat log. Attach it to a site (by slug) and it renders as that
          site&rsquo;s &ldquo;How this was made&rdquo; panel, the story behind the
          artifact, served under the site&rsquo;s own access control. Leave it
          unattached and it goes to your organization&rsquo;s chat library instead.
        </p>
        <p>
          As the work continues, <Code>append_chat</Code> adds follow-up turns and
          short action annotations (a file edit, a tool run) so the log stays
          current. <Code>get_site_chat</Code> reads a site&rsquo;s attached log
          back. One log attaches per site, and a gated site keeps its chat as
          private as its files.
        </p>
        <Callout>
          Prefer the terminal? The same logs are managed with{" "}
          <Link
            href="/cli#chat"
            className="font-medium text-primary underline-offset-4 hover:underline"
          >
            <Code>dropway chat</Code>
          </Link>
          .
        </Callout>
      </Section>

      <Section
        id="security"
        title="How access works"
        lead="The MCP server reuses Dropway's identity and authorization, it never gets a side door."
      >
        <ul className="space-y-3">
          <li className="flex items-start gap-2.5">
            <span className="mt-2 size-1.5 shrink-0 rounded-full bg-primary" />
            <span>
              <span className="font-medium text-foreground">OAuth 2.1.</span>{" "}
              Connecting runs a standard browser sign-in and consent against your
              Dropway account; the tool receives a short-lived token, not a
              password or a long-lived key.
            </span>
          </li>
          <li className="flex items-start gap-2.5">
            <span className="mt-2 size-1.5 shrink-0 rounded-full bg-primary" />
            <span>
              <span className="font-medium text-foreground">
                Scoped to your org.
              </span>{" "}
              Every call is bound to one organization and filtered by the same
              row-level security as the rest of the platform.
            </span>
          </li>
          <li className="flex items-start gap-2.5">
            <span className="mt-2 size-1.5 shrink-0 rounded-full bg-primary" />
            <span>
              <span className="font-medium text-foreground">
                Owner controlled.
              </span>{" "}
              Owners and admins can turn MCP access off for the whole org in{" "}
              <Link
                href="/settings"
                className="font-medium text-primary underline-offset-4 hover:underline"
              >
                Settings
              </Link>
              ; it takes effect immediately, even for already-issued tokens.
            </span>
          </li>
        </ul>
      </Section>

      <Section
        id="self-host"
        title="Self-hosting"
        lead="The MCP server ships with the self-host stack."
      >
        <p>
          When you run Dropway yourself, the MCP server comes up alongside the
          rest on port <Code>8092</Code>. Use your own URL as the connector
          endpoint instead of the hosted one:
        </p>
        <CodeBlock label="self-host MCP URL" code="http://localhost:8092/mcp" />
        <Callout>
          See the{" "}
          <a
            href="https://github.com/danielpang/dropway"
            target="_blank"
            rel="noreferrer"
            className="font-medium text-primary underline-offset-4 hover:underline"
          >
            self-host guide
          </a>{" "}
          for the full setup, including the audience and issuer settings the token
          verifier expects.
        </Callout>
      </Section>

      <div className="flex flex-wrap items-center justify-between gap-4 rounded-2xl border border-border bg-card p-6">
        <div className="space-y-1">
          <p className="font-medium text-foreground">Prefer the terminal?</p>
          <p className="text-sm text-muted-foreground">
            The CLI does the same deploy from your shell.
          </p>
        </div>
        <Button asChild variant="outline">
          <Link href="/cli">
            <Terminal aria-hidden />
            CLI reference
          </Link>
        </Button>
      </div>
    </div>
  );
}
