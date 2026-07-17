/**
 * Pure builders for the "Share → Embed" surface. The serving layer renders any
 * site URL with `?embed=1` as a framable, chrome-stripped document (see
 * edge/serving-worker/src/embed.ts and services/serve/internal/serve/embed.go), so
 * the dashboard only has to shape the URL + the <iframe> snippet the user copies.
 */

/** Default iframe height (px) when the user hasn't overridden it. */
export const DEFAULT_EMBED_HEIGHT = 600;

/** Default iframe width when the user hasn't overridden it. */
export const DEFAULT_EMBED_WIDTH = "100%";

/**
 * Build the embed URL: the site's live URL with `?embed=1`, plus `&badge=0` when the
 * (entitled) org is removing the "Powered by Dropway" badge. Falls back to string
 * concatenation if `liveUrl` somehow isn't a parseable URL, so it never throws.
 */
export function buildEmbedUrl(liveUrl: string, removeBadge: boolean): string {
  try {
    const u = new URL(liveUrl);
    u.searchParams.set("embed", "1");
    if (removeBadge) u.searchParams.set("badge", "0");
    return u.toString();
  } catch {
    const sep = liveUrl.includes("?") ? "&" : "?";
    return `${liveUrl}${sep}embed=1${removeBadge ? "&badge=0" : ""}`;
  }
}

/**
 * Build the <iframe> snippet a user pastes into Notion/Linear/etc. Width falls back
 * to 100%, height to 600px. The title is attribute-escaped (it's the site slug — a
 * DNS label — but escape defensively). Responsive by default: `max-width:100%`.
 */
export function buildEmbedSnippet(
  embedUrl: string,
  width: string,
  height: string,
  title: string,
): string {
  const w = width.trim() || DEFAULT_EMBED_WIDTH;
  const h = String(Number.parseInt(height, 10) || DEFAULT_EMBED_HEIGHT);
  const safeTitle = title.replace(/&/g, "&amp;").replace(/"/g, "&quot;").replace(/</g, "&lt;");
  return (
    `<iframe src="${embedUrl}" width="${w}" height="${h}" ` +
    `style="border:0;border-radius:8px;max-width:100%" loading="lazy" ` +
    `title="${safeTitle}"></iframe>`
  );
}
