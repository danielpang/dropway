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
    id: "embed-sites",
    date: "2026-07-18",
    label: "New",
    title: "Embed sites anywhere",
    summary:
      "Drop any deployed site into Notion, Linear, or any page as an iframe. Open Share, copy the embed code, and paste it in. The site must be public for the embed to show its content.",
    changes: [
      {
        id: "embed-copy-code",
        title: "Copy an embed code from Share",
        body: "Every deployed site has a Share button. Open it, set the width and height you want, and copy a ready made iframe snippet. A live preview shows exactly how it will look before you paste it in.",
      },
      {
        id: "embed-public-only",
        title: "Public sites only",
        body: "Embedding serves the site into a frame on other origins, so it only works when the site is public. If a site is private, visitors who are not signed in see a Sign in to view placeholder inside the embed, never the content.",
      },
      {
        id: "embed-remove-badge",
        title: "Remove the badge on paid plans",
        body: "Embeds carry a small Powered by Dropway badge. Pro plans and above can toggle it off, and the entitlement is enforced on our servers so it stays off wherever the site is framed.",
      },
    ],
  },
  {
    id: "chat-sharing",
    date: "2026-07-17",
    label: "New",
    title: "Share the conversation behind your site",
    summary:
      "Attach the AI session that built a site so visitors can read how it was made. Import a transcript from Claude Code, ChatGPT, or Cursor, or stream one in as you work over MCP and the CLI.",
    changes: [
      {
        id: "chat-how-this-was-made",
        title: "A How this was made panel",
        body: "Attach the session that built a site and visitors get a How this was made pill in the corner. It opens a drawer with the full transcript, served under the site's own access, so no Claude account is needed to read it.",
      },
      {
        id: "chat-import",
        title: "Import from Claude Code, ChatGPT, or Cursor",
        body: "Paste or upload a transcript and the format is detected automatically. Turn on activity annotations to condense tool runs and file edits into compact rows, so the story shows what the assistant did, not just what it said.",
      },
      {
        id: "chat-mcp-cli",
        title: "Append as you build over MCP and the CLI",
        body: "Stream a conversation into a log with the share_chat and append_chat MCP tools, or the dropway chat commands. Each site keeps one append-only log, so an agent can narrate its work while it builds.",
      },
      {
        id: "chat-library-controls",
        title: "Your library, your controls",
        body: "Sessions you do not attach live in an org Chats library for your team to read. Hide the panel without detaching, delete a single message if something slipped in, and owners can turn the whole feature off in Settings.",
      },
    ],
  },
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
      {
        id: "ai-builder-metered-usage",
        title: "Usage-based pricing",
        body: "The builder is charged per usage, at cost with no markup, on paid plans. Your builds are metered as you go and settled with the rest of your bill at the end of your billing cycle, so there is nothing to pay up front, and a note in the builder reminds you that usage is metered while you work.",
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
