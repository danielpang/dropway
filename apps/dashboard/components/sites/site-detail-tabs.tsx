"use client";

import * as React from "react";
import { Info, MessageSquare, Rocket } from "lucide-react";

import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";

/**
 * Page-level tabs for the site detail view. The site detail page is a server
 * component, so it renders each section server-side and hands the finished
 * markup in as slots (`deploy`, `details`, `comments`); this client component
 * only owns which one is visible. All panels stay mounted (hidden, not
 * unmounted) so client state inside them — the comment composer, the deploy
 * dropzone and its success state — survives tab switches.
 *
 * Deploy is the default tab: shipping (and the resulting live URL) is the
 * primary thing you do here, so it stays front-and-center while the secondary
 * material — version metadata, provenance, discussion — moves off the initial
 * view.
 *
 * Underline styling here deliberately differs from the segmented pill of the
 * inner "more ways to deploy" tabs (MCP/CLI), so a nested tablist doesn't read
 * as the same control.
 */
type TabKey = "deploy" | "details" | "comments";

const ORDER: TabKey[] = ["deploy", "details", "comments"];

export function SiteDetailTabs({
  deploy,
  details,
  comments,
  commentCount,
}: {
  deploy: React.ReactNode;
  details: React.ReactNode;
  comments: React.ReactNode;
  commentCount: number;
}) {
  const [tab, setTab] = React.useState<TabKey>("deploy");

  const tabs: {
    key: TabKey;
    label: string;
    icon: typeof Info;
    count?: number;
  }[] = [
    { key: "deploy", label: "Deploy", icon: Rocket },
    { key: "details", label: "Details", icon: Info },
    {
      key: "comments",
      label: "Comments",
      icon: MessageSquare,
      count: commentCount,
    },
  ];

  // Roving arrow-key navigation across the tablist (WAI-ARIA tabs pattern).
  function onKeyDown(e: React.KeyboardEvent) {
    const idx = ORDER.indexOf(tab);
    if (e.key === "ArrowRight" || e.key === "ArrowDown") {
      e.preventDefault();
      setTab(ORDER[(idx + 1) % ORDER.length]);
    } else if (e.key === "ArrowLeft" || e.key === "ArrowUp") {
      e.preventDefault();
      setTab(ORDER[(idx - 1 + ORDER.length) % ORDER.length]);
    }
  }

  return (
    <div>
      <div
        role="tablist"
        aria-label="Site sections"
        onKeyDown={onKeyDown}
        className="flex items-center gap-1 overflow-x-auto border-b border-border"
      >
        {tabs.map((t) => {
          const Icon = t.icon;
          const active = tab === t.key;
          return (
            <button
              key={t.key}
              role="tab"
              type="button"
              id={`site-tab-${t.key}`}
              aria-selected={active}
              aria-controls={`site-panel-${t.key}`}
              tabIndex={active ? 0 : -1}
              onClick={() => setTab(t.key)}
              className={cn(
                "-mb-px inline-flex shrink-0 items-center gap-1.5 border-b-2 px-3 py-2 text-sm font-medium transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background",
                active
                  ? "border-foreground text-foreground"
                  : "border-transparent text-muted-foreground hover:text-foreground",
              )}
            >
              <Icon className="size-4 shrink-0" aria-hidden />
              {t.label}
              {t.count ? (
                <Badge variant="muted" className="ml-0.5 font-normal">
                  {t.count}
                </Badge>
              ) : null}
            </button>
          );
        })}
      </div>

      <div className="pt-6">
        <Panel tab="deploy" active={tab === "deploy"}>
          {deploy}
        </Panel>
        <Panel tab="details" active={tab === "details"}>
          {details}
        </Panel>
        <Panel tab="comments" active={tab === "comments"}>
          {comments}
        </Panel>
      </div>
    </div>
  );
}

function Panel({
  tab,
  active,
  children,
}: {
  tab: TabKey;
  active: boolean;
  children: React.ReactNode;
}) {
  return (
    <div
      role="tabpanel"
      id={`site-panel-${tab}`}
      aria-labelledby={`site-tab-${tab}`}
      hidden={!active}
      className="space-y-6"
    >
      {children}
    </div>
  );
}
