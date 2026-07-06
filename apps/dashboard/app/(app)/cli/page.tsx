import type { Metadata } from "next";
import Link from "next/link";
import { ArrowLeft, Bot } from "lucide-react";

import { CodeBlock } from "@/components/docs/code-block";
import { Callout, Code, DocHero, DocTable, Section } from "@/components/docs/doc";
import { Button } from "@/components/ui/button";

export const metadata: Metadata = {
  title: "CLI reference",
  description:
    "The Dropway CLI: deploy a folder of static files to a live, access-controlled URL from your terminal. Install, authenticate, create a site, upload a folder, and list or read your sites.",
};

/** The Go module path users `go install` to build the CLI from source. */
const CLI_MODULE_PATH = "github.com/danielpang/dropway/cli/cmd/dropway";

/**
 * In-app CLI reference. Mirrors the dropway-www /cli page so the content matches,
 * but renders inside the authenticated app shell so signed-in users get the docs
 * without leaving the dashboard.
 */
export default function CliReferencePage() {
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
          eyebrow="CLI reference"
          title="The Dropway CLI"
          lead="dropway deploys a folder of static files to a live, access-controlled URL from your terminal. It hashes files locally, uploads only what changed, and prints the live URL."
        />
      </div>

      <Section
        id="install"
        title="Install"
        lead="The CLI is a single static Go binary. The quickest way to get it is the install script, which downloads the right build for your OS and architecture from GitHub Releases, verifies its checksum, and drops it on your PATH."
      >
        <CodeBlock
          label="install"
          code="curl -fsSL https://raw.githubusercontent.com/danielpang/dropway/main/install.sh | sh"
        />
        <p>
          The script installs to <Code>/usr/local/bin</Code> (falling back to{" "}
          <Code>~/.local/bin</Code>). Set <Code>DROPWAY_INSTALL_DIR</Code> to
          choose another location, or <Code>DROPWAY_VERSION</Code> to pin a
          specific release.
        </p>
        <p>
          Prefer to build from source? Install it with Go instead (this puts a{" "}
          <Code>dropway</Code> binary under <Code>$(go env GOPATH)/bin</Code>):
        </p>
        <CodeBlock label="install" code={`go install ${CLI_MODULE_PATH}@latest`} />
        <p>Either way, check it worked:</p>
        <CodeBlock label="terminal" code="dropway version" />
      </Section>

      <Section
        id="authenticate"
        title="Log in"
        lead="Run dropway login. A browser tab opens, you sign in and approve access, and the CLI stores a token locally so every later command just works."
      >
        <CodeBlock label="terminal" code="dropway login" />
        <p>
          The CLI opens your browser to the Dropway sign-in page (the same account
          you use in the dashboard). After you approve, it saves your credentials
          to <Code>~/.config/dropway/credentials.json</Code>. That&rsquo;s it, no
          tokens to copy. Sign out any time with <Code>dropway logout</Code>.
        </p>
        <Callout title="CI and scripts">
          For non-interactive environments, skip the browser and set a deploy
          token instead: <Code>DROPWAY_TOKEN</Code> takes precedence over the
          stored login, so the same <Code>dropway deploy</Code> commands work in a
          pipeline.
        </Callout>
        <p>
          Self-hosting? Point the CLI at your own instance with <Code>--api</Code>{" "}
          (or the <Code>DROPWAY_API</Code> environment variable); it defaults to
          the hosted <Code>https://api.dropway.dev</Code>. <Code>dropway login</Code>{" "}
          honors the same flag.
        </p>
        <CodeBlock
          label="terminal"
          code="dropway login --api http://localhost:8080   # self-host"
        />
      </Section>

      <Section
        id="commands"
        title="Commands"
        lead="dropway <command> [flags]. The everyday commands are deploy, sites, and read; gc and dr rebuild are operator utilities."
      >
        <DocTable
          head={["Command", "What it does"]}
          rows={[
            [
              <Code key="c">login</Code>,
              "Sign in via the browser and store credentials locally.",
            ],
            [<Code key="c">logout</Code>, "Remove the stored credentials."],
            [
              <Code key="c">deploy &lt;dir&gt;</Code>,
              "Hash a folder, upload changed files, finalize, and publish to a live URL.",
            ],
            [
              <Code key="c">sites</Code>,
              "List the sites you own, or every site in the org with --all.",
            ],
            [
              <Code key="c">read &lt;url-or-slug&gt;</Code>,
              "Fetch a site's served content over HTTP and print it to stdout.",
            ],
            [
              <Code key="c">skills push &lt;dir&gt;</Code>,
              "Share a skill (a folder with a SKILL.md) with your org, optionally into folders with --folder.",
            ],
            [
              <Code key="c">skills list</Code>,
              "List the org's shared skills; filter with --search, --folder, or --presets.",
            ],
            [
              <Code key="c">skills pull &lt;name&gt;</Code>,
              "Download a shared skill into .claude/skills/ (or a whole folder with --folder).",
            ],
            [<Code key="c">version</Code>, "Print the CLI version."],
            [
              <Code key="c">gc</Code>,
              "Operator: garbage-collect orphaned blobs (keep the current and last N versions).",
            ],
            [
              <Code key="c">dr rebuild</Code>,
              "Operator: rebuild the edge routing projection from Postgres (disaster-recovery drill).",
            ],
          ]}
        />
      </Section>

      <Section
        id="deploy"
        title="dropway deploy"
        lead="Walk a directory, compute a SHA-256 per file, and run the full deploy: prepare, upload only-changed blobs, finalize, publish, then print the live URL."
      >
        <p>
          Without <Code>--send</Code> it is a dry run: it prints the manifest it
          would upload and makes no network calls. Add <Code>--send</Code> to
          actually deploy (run <Code>dropway login</Code> first, or set{" "}
          <Code>DROPWAY_TOKEN</Code>).
        </p>
        <DocTable
          head={["Flag", "Description"]}
          rows={[
            [
              <Code key="f">--send</Code>,
              "Actually run the deploy. Without it, print the plan only (requires sign-in).",
            ],
            [
              <Code key="f">--new</Code>,
              <>
                Create a new site before deploying (requires <Code>--site</Code>).
              </>,
            ],
            [
              <Code key="f">--site &lt;slug&gt;</Code>,
              <>
                The slug for the new site (used with <Code>--new</Code>).
              </>,
            ],
            [
              <Code key="f">--site-id &lt;id&gt;</Code>,
              "Deploy to an existing site by id.",
            ],
            [
              <Code key="f">--api &lt;url&gt;</Code>,
              <>
                API base URL (defaults to <Code>https://api.dropway.dev</Code>, or{" "}
                <Code>DROPWAY_API</Code>).
              </>,
            ],
          ]}
        />
      </Section>

      <Section
        id="examples"
        title="Examples"
        lead="The common flows: preview, create, and upload."
      >
        <p className="text-foreground">Preview a deploy (dry run, no upload)</p>
        <CodeBlock label="terminal" code="dropway deploy ./dist" />

        <p className="pt-2 text-foreground">Create a new site and deploy to it</p>
        <CodeBlock
          label="terminal"
          code="dropway deploy ./dist --new --site my-docs --send"
        />
        <p>
          This creates the <Code>my-docs</Code> site, uploads the folder, and
          prints something like{" "}
          <Code>https://my-org--my-docs.dropwaycontent.com</Code>.
        </p>

        <p className="pt-2 text-foreground">
          Upload a folder to an existing site
        </p>
        <CodeBlock
          label="terminal"
          code="dropway deploy ./dist --site-id 1a2b3c4d --send"
        />
        <p>
          Only files whose contents changed are uploaded (everything is
          content-addressed), then the new version is published. Roll back any
          time from the dashboard.
        </p>

        <p className="pt-2 text-foreground">Deploy to a self-hosted instance</p>
        <CodeBlock
          label="terminal"
          code={`dropway login --api http://localhost:8080
dropway deploy ./dist --api http://localhost:8080 --new --site demo --send`}
        />
      </Section>

      <Section
        id="browse"
        title="List and read sites"
        lead="dropway sites lists what you've shipped; dropway read fetches a site's served content straight to your terminal."
      >
        <p>
          <Code>dropway sites</Code> lists the sites you own. Add{" "}
          <Code>--all</Code> to list every site in your organization, with an
          owner column.
        </p>
        <CodeBlock
          label="terminal"
          code={`dropway sites          # sites you own
dropway sites --all    # every site in the org`}
        />
        <p className="pt-2 text-foreground">Read a site over HTTP</p>
        <p>
          <Code>dropway read</Code> fetches served content and writes it to
          stdout, so you can pipe it elsewhere. Pass a full URL, or a site slug
          the CLI resolves to its live URL first.
        </p>
        <CodeBlock
          label="terminal"
          code={`dropway read my-docs
dropway read https://my-org--my-docs.dropwaycontent.com`}
        />
        <p>
          Public sites need no sign-in. A gated site returns its sign-in page
          instead of the content, since the fetch is a plain HTTP request.
        </p>
      </Section>

      <Section
        id="permissions"
        title="Changing permissions"
        lead="Sharing and access control live in the dashboard and the MCP server, not the CLI."
      >
        <p>
          New sites start at your organization&rsquo;s default visibility
          (org-only). To make a site public, password-protected, or limited to an
          email allowlist, change its access in the dashboard, or use the{" "}
          <Code>set_site_access</Code> tool on the Dropway MCP server.
        </p>
        <Callout title="Manage access from an AI tool">
          The{" "}
          <Link
            href="/mcp"
            className="font-medium text-primary underline-offset-4 hover:underline"
          >
            Dropway MCP server
          </Link>{" "}
          exposes <Code>create_site</Code>, <Code>deploy_site</Code>, and{" "}
          <Code>set_site_access</Code> so an assistant can create, deploy, and
          re-share sites for you in chat.
        </Callout>
      </Section>

      <div className="flex flex-wrap items-center justify-between gap-4 rounded-2xl border border-border bg-card p-6">
        <div className="space-y-1">
          <p className="font-medium text-foreground">
            Prefer to deploy from an AI assistant?
          </p>
          <p className="text-sm text-muted-foreground">
            Connect the Dropway MCP server to Claude, Cursor, or Codex.
          </p>
        </div>
        <Button asChild variant="outline">
          <Link href="/mcp">
            <Bot aria-hidden />
            MCP reference
          </Link>
        </Button>
      </div>
    </div>
  );
}
