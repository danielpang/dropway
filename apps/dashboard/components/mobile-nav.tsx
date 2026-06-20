"use client";

import * as React from "react";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { Menu, X } from "lucide-react";

import { Button } from "@/components/ui/button";
import { SignOutButton } from "@/components/sign-out-button";
import { NAV_LINKS, isNavActive } from "@/components/main-nav";
import { cn } from "@/lib/utils";

/**
 * Compact primary navigation for small screens. The desktop header lays the five
 * sections + actions out in a single row that overflows a phone viewport, so on
 * mobile those links collapse behind this hamburger menu (the desktop <MainNav>
 * is hidden at the same breakpoint). Closes on route change, Escape, or an
 * outside click. `admin` mirrors MainNav's gate for the owner/admin-only Audit
 * link (the page and the Go API re-check the role regardless).
 */
export function MobileNav({ admin = false }: { admin?: boolean }) {
  const pathname = usePathname();
  const [open, setOpen] = React.useState(false);
  const containerRef = React.useRef<HTMLDivElement>(null);

  const links = NAV_LINKS.filter((link) => !link.admin || admin);

  // Close the menu whenever navigation lands on a new path.
  React.useEffect(() => {
    setOpen(false);
  }, [pathname]);

  // Escape to close + click-away dismissal while open.
  React.useEffect(() => {
    if (!open) return;

    function onKeyDown(e: KeyboardEvent) {
      if (e.key === "Escape") setOpen(false);
    }
    function onPointerDown(e: PointerEvent) {
      if (
        containerRef.current &&
        !containerRef.current.contains(e.target as Node)
      ) {
        setOpen(false);
      }
    }
    document.addEventListener("keydown", onKeyDown);
    document.addEventListener("pointerdown", onPointerDown);
    return () => {
      document.removeEventListener("keydown", onKeyDown);
      document.removeEventListener("pointerdown", onPointerDown);
    };
  }, [open]);

  return (
    <div ref={containerRef} className="relative">
      <Button
        type="button"
        variant="ghost"
        size="icon"
        aria-label={open ? "Close menu" : "Open menu"}
        aria-expanded={open}
        aria-haspopup="menu"
        onClick={() => setOpen((o) => !o)}
      >
        {open ? <X aria-hidden /> : <Menu aria-hidden />}
      </Button>

      {open && (
        <div
          role="menu"
          aria-label="Primary"
          className="absolute right-0 top-full z-50 mt-2 w-52 origin-top-right rounded-lg border border-border bg-popover p-1.5 text-popover-foreground shadow-lg animate-fade-in"
        >
          <nav className="flex flex-col">
            {links.map((link) => {
              const active = isNavActive(pathname, link.match);
              return (
                <Link
                  key={link.href}
                  href={link.href}
                  role="menuitem"
                  aria-current={active ? "page" : undefined}
                  className={cn(
                    "rounded-md px-3 py-2 text-sm font-medium transition-colors",
                    "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background",
                    active
                      ? "bg-secondary text-secondary-foreground"
                      : "text-muted-foreground hover:bg-muted hover:text-foreground",
                  )}
                >
                  {link.label}
                </Link>
              );
            })}
          </nav>
          <div className="my-1.5 h-px bg-border" aria-hidden />
          <SignOutButton className="w-full justify-start" />
        </div>
      )}
    </div>
  );
}
