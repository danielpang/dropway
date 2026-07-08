"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";

import { cn } from "@/lib/utils";

/** Top-level app sections. `match` is the path prefix used for active state. */
export type NavLink = { href: string; label: string; match: string; admin?: boolean };

/** Shared by the desktop nav and the mobile menu so they never drift. */
export const NAV_LINKS: NavLink[] = [
  { href: "/dashboard", label: "My Sites", match: "/dashboard" },
  { href: "/feed", label: "Feed", match: "/feed" },
  { href: "/skills", label: "Skills", match: "/skills" },
  { href: "/members", label: "Members", match: "/members" },
  // Audit is owner/admin only (the page also re-gates server-side).
  { href: "/audit", label: "Audit", match: "/audit", admin: true },
  { href: "/billing", label: "Billing", match: "/billing" },
  { href: "/settings", label: "Settings", match: "/settings" },
];

/**
 * Reference docs (the in-app MCP + CLI pages). Kept separate from NAV_LINKS so
 * they render as their own group, after a divider on desktop, in their own
 * section in the mobile menu, rather than mixing in with the org's app sections.
 */
export const DOCS_LINKS: NavLink[] = [
  { href: "/mcp", label: "MCP", match: "/mcp" },
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
 * Primary navigation in the app shell. Highlights the active section by path
 * prefix (so /sites/[id] keeps "Sites" lit). Client component for `usePathname`.
 *
 * `admin` controls visibility of admin-only entries (the Audit log). This is a
 * convenience gate, the audit page itself re-checks owner/admin server-side and
 * the Go API independently enforces the role on /v1/audit.
 */
export function MainNav({ admin = false }: { admin?: boolean }) {
  const pathname = usePathname();
  const links = NAV_LINKS.filter((link) => !link.admin || admin);

  function renderLink(link: NavLink) {
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
  }

  return (
    <nav className="flex items-center gap-1" aria-label="Primary">
      {links.map(renderLink)}
      <span className="mx-1 h-5 w-px bg-border" aria-hidden />
      {DOCS_LINKS.map(renderLink)}
    </nav>
  );
}
