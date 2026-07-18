/**
 * Changelog content, as data. The /changelog page renders these entries in a
 * Cursor-style layout: a sticky date rail on the left, the release notes on the
 * right, and a copy-able permalink on every entry AND every individual change so
 * you can link someone straight to one item (e.g. /changelog#skills-versioning).
 *
 * Ordering: newest first (the page renders them in array order). Each `id` and
 * each change `id` is a stable anchor target — treat them as permalinks and only
 * append, never renumber, so shared links keep resolving.
 *
 * Copy style mirrors the rest of the app: no em or en dashes in user-facing text.
 */

export type ChangelogChange = {
  /** Stable anchor id (permalink target). Unique across the whole changelog. */
  id: string;
  title: string;
  body: string;
};

export type ChangelogEntry = {
  /** Stable anchor id (permalink target) for the whole release. */
  id: string;
  /** ISO date (YYYY-MM-DD); rendered in the left rail. */
  date: string;
  /** Short pill next to the title, e.g. "New" or "Improved". */
  label?: string;
  title: string;
  /** One or two sentence lead under the title. */
  summary: string;
  changes: ChangelogChange[];
};

export const CHANGELOG: ChangelogEntry[] = [
  {
    id: "ai-builder-usage-billing",
    date: "2026-07-18",
    label: "Improved",
    title: "Usage-based pricing for the AI builder",
    summary:
      "The AI builder is charged per usage, at cost with no markup. Available on paid plans, your builds are metered as you go and billed to your account at the end of your billing cycle, so there is nothing to pay up front.",
    changes: [
      {
        id: "ai-builder-metered-usage",
        title: "Pay only for what you build, at cost",
        body: "Each build meters the model usage it takes to generate and revise your site, passed through at cost with no markup. There is no seat to buy: you are charged for the work you actually run.",
      },
      {
        id: "ai-builder-billing-cycle",
        title: "Billed at the end of your cycle",
        body: "Metered usage is added to your account and settled with the rest of your bill at the end of your billing cycle. A note in the builder reminds you that usage is metered while you work.",
      },
      {
        id: "ai-builder-paid-plans",
        title: "Available on paid plans",
        body: "The AI builder is included on paid plans, which already have a card on file, so metered usage lands on your regular invoice. Free plans can upgrade to turn it on.",
      },
    ],
  },
  {
    id: "ai-website-builder-skills-library",
    date: "2026-07-09",
    label: "New",
    title: "AI website builder and skills library",
    summary:
      "Create or evolve a site with the AI builder, then publish it instantly. Drag skills into a shared library where teammates and AI tools can find and use them.",
    changes: [
      {
        id: "ai-builder-chat",
        title: "Build from a prompt",
        body: "Open any site and chat with the builder to create or change a page. It writes the HTML, CSS, and JS for you, running in a sandbox, and shows its progress as it goes.",
      },
      {
        id: "ai-builder-preview",
        title: "Live preview and publish",
        body: "Every turn refreshes a live preview of the draft. When it looks right, publish it as a new immutable version, with the same instant rollback as any other deploy.",
      },
      {
        id: "ai-builder-models",
        title: "Pick your model",
        body: "Choose the model that drives a build from the model picker. Requests are proxied through the dashboard so your session, not an API key, authorizes the work.",
      },
      {
        id: "skills-library",
        title: "Share skills across your org",
        body: "Drag a skill in to publish it to the org library. Search it, filter by admin-curated folders (engineering, product, marketing by default), and download any skill in one click.",
      },
      {
        id: "skills-versioning",
        title: "Versioning and editing",
        body: "Edit a skill in place and every change is kept as a version, so you can see how it evolved and pull an earlier one. Update checks flag when a newer version of a skill you use is available.",
      },
      {
        id: "skills-mcp",
        title: "Reach skills over MCP",
        body: "Connected AI tools can list and download your org's skills through the Dropway MCP server, honoring the same access rules as the dashboard.",
      },
    ],
  },
  {
    id: "embed-sites",
    date: "2026-07-06",
    label: "New",
    title: "Embed sites",
    summary:
      "Paste any site into Notion, Linear, Confluence, and others using an iframe. Sites need to be public to be embedded so gated sites never show inside an <iframe>.",
    changes: [
      {
        id: "embed-sites-actionable",
        title: "Embed sites are now available",
        body: "Every site now has an embed option. You can share an embed URL in the dashboard or generate an <iframe> snippet to copy and paste. Use the embed dialog for any site, including AI builder sites.",
      },
      {
        id: "embed-sites-public-only",
        title: "Sites must be public to embed",
        body: "Only public sites can be embedded; gated sites (password, allowlist, org-only) show a 'Sign in to view' placeholder inside the embed frame. This is enforced at embed time to ensure security.",
      },
      {
        id: "embed-sites-frame",
        title: "Framed, chrome-stripped rendering",
        body: "Embeds render as framable documents with no browser chrome. Just an iframe you control. Pro+ orgs can remove the 'Powered by Dropway' badge with ?badge=0, but usage counts toward metered AI builder usage.",
      },
    ],
  },
];
