export type GuestRole = "viewer" | "reviewer";
export interface ProjectShare {
  id: string;
  email: string;
  role: GuestRole;
  expires_at: string;
  accepted_at: string | null;
  revoked_at: string | null;
}
export interface GuestSite {
  id: string;
  slug: string;
  title: string;
  current_version_id: string | null;
  current_content_hash: string;
}
export interface GuestProject {
  id: string;
  name: string;
  role: GuestRole;
  sites: GuestSite[];
}
export interface GuestReply {
  id: string;
  author_id: string;
  body: string;
  created_at: string;
}
export interface GuestThread {
  id: string;
  site_id: string;
  version_id: string;
  content_hash: string;
  page: string;
  anchor_kind: string;
  anchor_state: string;
  status: string;
  created_at: string;
  replies: GuestReply[];
  replies_next_cursor?: string;
}
export function object(value: unknown): Record<string, unknown> {
  return value && typeof value === "object" && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : {};
}
const str = (value: unknown) => (typeof value === "string" ? value : "");
const optional = (value: unknown) => (typeof value === "string" ? value : null);
export function parseShare(value: unknown): ProjectShare | null {
  const r = object(value);
  return str(r.id) &&
    str(r.email) &&
    (r.role === "viewer" || r.role === "reviewer")
    ? {
        id: str(r.id),
        email: str(r.email),
        role: r.role,
        expires_at: str(r.expires_at),
        accepted_at: optional(r.accepted_at),
        revoked_at: optional(r.revoked_at),
      }
    : null;
}
export function parseGuestProject(value: unknown): GuestProject | null {
  const r = object(value);
  if (!str(r.id) || (r.role !== "viewer" && r.role !== "reviewer")) return null;
  return {
    id: str(r.id),
    name: str(r.name),
    role: r.role,
    sites: Array.isArray(r.sites)
      ? r.sites.flatMap((value) => {
          const s = object(value);
          return str(s.id)
            ? [
                {
                  id: str(s.id),
                  slug: str(s.slug),
                  title: str(s.title),
                  current_version_id: optional(s.current_version_id),
                  current_content_hash: str(s.current_content_hash),
                },
              ]
            : [];
        })
      : [],
  };
}
export function parseGuestReply(value: unknown): GuestReply | null {
  const r = object(value);
  return str(r.id) && typeof r.body === "string"
    ? {
        id: str(r.id),
        author_id: str(r.author_id),
        body: r.body,
        created_at: str(r.created_at),
      }
    : null;
}
export function parseGuestThread(value: unknown): GuestThread | null {
  const r = object(value);
  if (!str(r.id) || !str(r.site_id) || !str(r.version_id)) return null;
  return {
    id: str(r.id),
    site_id: str(r.site_id),
    version_id: str(r.version_id),
    content_hash: str(r.content_hash),
    page: str(r.page),
    anchor_kind: str(r.anchor_kind),
    anchor_state: str(r.anchor_state),
    status: str(r.status),
    created_at: str(r.created_at),
    replies: Array.isArray(r.replies)
      ? r.replies.flatMap((item) => {
          const reply = parseGuestReply(item);
          return reply ? [reply] : [];
        })
      : [],
    replies_next_cursor: optional(r.replies_next_cursor) ?? undefined,
  };
}
export function invitationToken(hash: string): string | null {
  const token = new URLSearchParams(hash.replace(/^#/, "")).get("invite");
  return token && /^[A-Za-z0-9_-]{32,128}$/.test(token) ? token : null;
}
