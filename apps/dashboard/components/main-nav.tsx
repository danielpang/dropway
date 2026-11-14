"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";

import { cn } from "@/lib/utils";

/**
 * A navigation entry. `match` is the path prefix used for active state; `admin`
 * hides it from non-admins (a convenience gate, pages re-check server-side).
 */
export type NavLink = { href: string; label: string; match: string; admin?: boolean; shared?: boolean };

/**
 * Day-to-day workspace sections. These stay inline in the desktop header; the
 * rarely visited org and reference pages live in the account menu instead, so
 * the header no longer overflows into a wall of ten links.
 */
export const NAV_LINKS: NavLink[] = [
  { href: "/dashboard", label: "Sites", match: "/dashboard" },
  { href: "/shared-projects", label: "Shared with me", match: "/shared-projects", shared: true },
  { href: "/feed", label: "Feed", match: "/feed" },
  { href: "/skills", label: "Skills", match: "/skills" },
  { href: "/chats", label: "Chats", match: "/chats" },
];

/** Organization administration, grouped in the account menu. */
export const ORG_LINKS: NavLink[] = [
  { href: "/members", label: "Members", match: "/members" },
  // Audit is owner/admin only (the page also re-gates server-side).
  { href: "/audit", label: "Audit log", match: "/audit", admin: true },
  { href: "/billing", label: "Billing", match: "/billing" },
  { href: "/settings", label: "Settings", match: "/settings" },
];

/** Reference docs (the in-app MCP + CLI pages), grouped in the account menu. */
export const DOCS_LINKS: NavLink[] = [
  { href: "/mcp", label: "MCP server", match: "/mcp" },
  { href: "/cli", label: "CLI", match: "/cli" },
];

/**
 * Whether a nav link is the active section for the current path. Matches the
 * exact path or any sub-route, and treats /sites/* as part of the Sites
 * ("/dashboard") section so a site detail page keeps Sites lit.
 */
export function isNavActive(pathname: string, match: string): boolean {
  return (
    pathname === match ||
    pathname.startsWith(`${match}/`) ||
    (match === "/dashboard" && pathname.startsWith("/sites"))
  );
}

/**
 * Primary navigation in the app shell (desktop). Highlights the active section
 * by path prefix (so /sites/[id] keeps "Sites" lit). Client component for
 * `usePathname`.
 */
export function MainNav({ hasSharedProjects = false }: { hasSharedProjects?: boolean }) {
  const pathname = usePathname();

  return (
    <nav className="flex items-center gap-1" aria-label="Primary">
      {visibleNavLinks(false, hasSharedProjects).map((link) => {
        const active = isNavActive(pathname, link.match);
        return (
          <Link
            key={link.href}
            href={link.href}
            aria-current={active ? "page" : undefined}
            className={cn(
              "rounded-md px-3 py-1.5 text-sm font-medium transition-colors",
              "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background",
              active
                ? "bg-secondary text-secondary-foreground"
                : "text-muted-foreground hover:text-foreground",
            )}
          >
            {link.label}
          </Link>
        );
      })}
    </nav>
  );
}

export function visibleNavLinks(admin = false, hasSharedProjects = false): NavLink[] {
  return NAV_LINKS.filter((link) => (!link.admin || admin) && (!link.shared || hasSharedProjects));
}
