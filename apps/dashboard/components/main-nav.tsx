"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";

import { cn } from "@/lib/utils";

/** Top-level app sections. `match` is the path prefix used for active state. */
const LINKS: { href: string; label: string; match: string }[] = [
  { href: "/dashboard", label: "Sites", match: "/dashboard" },
  { href: "/members", label: "Members", match: "/members" },
  { href: "/billing", label: "Billing", match: "/billing" },
  { href: "/settings", label: "Settings", match: "/settings" },
];

/**
 * Primary navigation in the app shell. Highlights the active section by path
 * prefix (so /sites/[id] keeps "Sites" lit). Client component for `usePathname`.
 */
export function MainNav() {
  const pathname = usePathname();

  return (
    <nav className="flex items-center gap-1" aria-label="Primary">
      {LINKS.map((link) => {
        const active =
          pathname === link.match || pathname.startsWith(`${link.match}/`) ||
          // /sites/* belongs to the Sites section.
          (link.match === "/dashboard" && pathname.startsWith("/sites"));
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
