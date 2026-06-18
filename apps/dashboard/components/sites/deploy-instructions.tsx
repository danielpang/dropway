"use client";

import * as React from "react";
import { Check, Copy } from "lucide-react";

import { cn } from "@/lib/utils";

/**
 * "Deploy via CLI" panel. Phase 1's publish loop is driven by the CLI
 * (`dropway deploy`), which prepares → finalizes → publishes against the Go API
 * with the site's slug. This shows the exact commands with copy-to-clipboard.
 */
export function DeployInstructions({ slug }: { slug: string }) {
  const steps = React.useMemo(
    () => [
      {
        label: "Install the CLI",
        command:
          "curl -fsSL https://raw.githubusercontent.com/danielpang/dropway/main/install.sh | sh",
      },
      {
        label: "Authenticate (opens your browser)",
        command: "dropway login",
      },
      {
        label: `Deploy a folder to ${slug}`,
        command: `dropway deploy ./dist --site ${slug}`,
      },
    ],
    [slug],
  );

  return (
    <ol className="space-y-4">
      {steps.map((step, i) => (
        <li key={step.command} className="flex gap-3">
          <span
            aria-hidden
            className="mt-0.5 grid size-6 shrink-0 place-items-center rounded-full border border-border text-xs font-medium text-muted-foreground"
          >
            {i + 1}
          </span>
          <div className="min-w-0 flex-1 space-y-1.5">
            <p className="text-sm text-foreground">{step.label}</p>
            <CommandLine command={step.command} />
          </div>
        </li>
      ))}
    </ol>
  );
}

function CommandLine({ command }: { command: string }) {
  const [copied, setCopied] = React.useState(false);

  async function copy() {
    try {
      await navigator.clipboard.writeText(command);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // Clipboard may be unavailable (insecure context); fail silently.
    }
  }

  return (
    <div className="flex items-center gap-2 rounded-md border border-border bg-muted/50 px-3 py-2">
      <code className="min-w-0 flex-1 truncate font-mono text-xs text-foreground">
        <span className="select-none text-muted-foreground">$ </span>
        {command}
      </code>
      <button
        type="button"
        onClick={copy}
        aria-label={copied ? "Copied" : "Copy command"}
        className={cn(
          "shrink-0 rounded-sm p-1 text-muted-foreground transition-colors hover:text-foreground",
          "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background",
        )}
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
