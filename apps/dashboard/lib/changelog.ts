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
    id: "ai-website-builder",
    date: "2026-07-09",
    label: "New",
    title: "AI website builder",
    summary:
      "Describe the page you want and watch it come together. The builder generates a site from a prompt, streams its work live, and hands you a real, access-controlled URL the moment you publish.",
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
    ],
  },
  {
    id: "skills",
    date: "2026-06-24",
    label: "New",
    title: "Skills library",
    summary:
      "A shared library of skills for your whole org: upload once, and every teammate (and connected AI tool) can find and pull them. Curated into folders, versioned, and available over MCP.",
    changes: [
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
];
