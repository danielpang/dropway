"use client";

import * as React from "react";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { useTheme } from "next-themes";
import {
  BookOpen,
  ChevronDown,
  CreditCard,
  LogOut,
  Menu,
  Monitor,
  Moon,
  ScrollText,
  Settings,
  Sun,
  Terminal,
  Users,
  type LucideIcon,
} from "lucide-react";

import {
  DOCS_LINKS,
  NAV_LINKS,
  ORG_LINKS,
  isNavActive,
  type NavLink,
} from "@/components/main-nav";
import { authClient } from "@/lib/auth-client";
import { cn } from "@/lib/utils";

const ICONS: Record<string, LucideIcon> = {
  "/members": Users,
  "/audit": ScrollText,
  "/billing": CreditCard,
  "/settings": Settings,
  "/mcp": BookOpen,
  "/cli": Terminal,
};

const THEMES = [
  { value: "system", label: "System", Icon: Monitor },
  { value: "light", label: "Light", Icon: Sun },
  { value: "dark", label: "Dark", Icon: Moon },
] as const;

const ITEM_SELECTOR = '[role="menuitem"],[role="menuitemradio"]';

/**
 * Focusable menu items in DOM order. Items hidden by a breakpoint (the mobile
 * only primary section) have no layout box, so they're skipped.
 */
function visibleItems(menu: HTMLElement | null): HTMLElement[] {
  if (!menu) return [];
  return Array.from(menu.querySelectorAll<HTMLElement>(ITEM_SELECTOR)).filter(
    (el) => el.offsetParent !== null,
  );
}

export type AccountMenuUser = {
  name?: string | null;
  email?: string | null;
  image?: string | null;
};

/**
 * The header's account menu: who you are, which org you're in, the org admin
 * pages, reference docs, theme, and sign out, behind one avatar trigger.
 *
 * Below `md` the desktop <MainNav> is hidden, so the menu also carries the
 * primary sections (the trigger gains a hamburger glyph there to signal it).
 *
 * Hand-rolled rather than Radix (the dashboard keeps its dependency set small),
 * but follows the WAI-ARIA menu button pattern: arrow keys / Home / End move
 * between items, Escape closes and returns focus to the trigger, Tab or an
 * outside click closes, and any navigation closes it.
 */
export function AccountMenu({
  user,
  orgName,
  admin = false,
}: {
  user: AccountMenuUser;
  orgName?: string | null;
  admin?: boolean;
}) {
  const pathname = usePathname();
  const [open, setOpen] = React.useState(false);
  const [signingOut, setSigningOut] = React.useState(false);
  const rootRef = React.useRef<HTMLDivElement>(null);
  const triggerRef = React.useRef<HTMLButtonElement>(null);
  const menuRef = React.useRef<HTMLDivElement>(null);
  const menuId = React.useId();

  const orgLinks = ORG_LINKS.filter((link) => !link.admin || admin);
  // Light the trigger when the current page lives inside the menu, so the
  // header still shows where you are after primary nav loses the link.
  const activeInside = [...orgLinks, ...DOCS_LINKS].some((link) =>
    isNavActive(pathname, link.match),
  );

  const displayName = user.name?.trim() || user.email || "Account";

  React.useEffect(() => {
    setOpen(false);
  }, [pathname]);

  // Outside click closes. Pointerdown (not click) so a press that starts on the
  // page dismisses before any underlying handler runs.
  React.useEffect(() => {
    if (!open) return;
    function onPointerDown(e: PointerEvent) {
      if (rootRef.current && !rootRef.current.contains(e.target as Node)) {
        setOpen(false);
      }
    }
    document.addEventListener("pointerdown", onPointerDown);
    return () => document.removeEventListener("pointerdown", onPointerDown);
  }, [open]);

  // Move focus into the menu on open.
  React.useEffect(() => {
    if (open) visibleItems(menuRef.current)[0]?.focus();
  }, [open]);

  function close({ restoreFocus }: { restoreFocus: boolean }) {
    setOpen(false);
    if (restoreFocus) triggerRef.current?.focus();
  }

  function onTriggerKeyDown(e: React.KeyboardEvent) {
    if (e.key === "ArrowDown" || e.key === "ArrowUp") {
      e.preventDefault();
      setOpen(true);
    }
  }

  function onMenuKeyDown(e: React.KeyboardEvent) {
    const items = visibleItems(menuRef.current);
    const index = items.indexOf(document.activeElement as HTMLElement);
    const focusAt = (i: number) =>
      items[(i + items.length) % items.length]?.focus();

    switch (e.key) {
      case "ArrowDown":
        e.preventDefault();
        focusAt(index + 1);
        break;
      case "ArrowUp":
        e.preventDefault();
        focusAt(index < 0 ? -1 : index - 1);
        break;
      case "Home":
        e.preventDefault();
        focusAt(0);
        break;
      case "End":
        e.preventDefault();
        focusAt(-1);
        break;
      case "Escape":
        e.preventDefault();
        close({ restoreFocus: true });
        break;
      case "Tab":
        close({ restoreFocus: false });
        break;
    }
  }

  async function signOut() {
    setSigningOut(true);
    try {
      await authClient.signOut();
    } finally {
      window.location.assign("/sign-in");
    }
  }

  function renderLink(link: NavLink, withIcon: boolean) {
    const active = isNavActive(pathname, link.match);
    const Icon = ICONS[link.href];
    return (
      <Link
        key={link.href}
        href={link.href}
        role="menuitem"
        tabIndex={-1}
        aria-current={active ? "page" : undefined}
        // Same-path clicks don't change `pathname`, so close explicitly.
        onClick={() => setOpen(false)}
        className={cn(itemClass, active && "bg-secondary text-foreground")}
      >
        {withIcon && Icon ? <Icon aria-hidden className="size-4 shrink-0" /> : null}
        {link.label}
      </Link>
    );
  }

  return (
    <div ref={rootRef} className="relative">
      <button
        ref={triggerRef}
        type="button"
        aria-haspopup="menu"
        aria-expanded={open}
        aria-controls={open ? menuId : undefined}
        aria-label={`Account menu for ${displayName}`}
        onClick={() => setOpen((o) => !o)}
        onKeyDown={onTriggerKeyDown}
        className={cn(
          "flex h-9 items-center gap-2 rounded-full border border-border py-1 pl-1 pr-2 text-sm transition-colors",
          "hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background",
          (open || activeInside) && "bg-muted",
        )}
      >
        <Avatar user={user} />
        {orgName ? (
          <span className="hidden max-w-[10rem] truncate font-medium sm:inline">
            {orgName}
          </span>
        ) : null}
        <ChevronDown
          aria-hidden
          className={cn(
            "hidden size-4 text-muted-foreground transition-transform md:block",
            open && "rotate-180",
          )}
        />
        <Menu aria-hidden className="size-4 text-muted-foreground md:hidden" />
      </button>

      {open ? (
        <div
          ref={menuRef}
          id={menuId}
          role="menu"
          aria-label="Account"
          onKeyDown={onMenuKeyDown}
          className="absolute right-0 top-full z-50 mt-2 max-h-[calc(100dvh-4.5rem)] w-64 origin-top-right overflow-y-auto rounded-xl border border-border bg-popover p-1.5 text-popover-foreground shadow-lg animate-fade-in [animation-duration:150ms]"
        >
          <div className="flex items-center gap-3 px-2.5 pb-2.5 pt-2">
            <Avatar user={user} size="lg" />
            <div className="min-w-0">
              <p className="truncate text-sm font-medium">{displayName}</p>
              {user.name && user.email ? (
                <p className="truncate text-xs text-muted-foreground">{user.email}</p>
              ) : null}
            </div>
          </div>

          {/* Primary sections, only where the inline header nav is hidden. */}
          <div role="group" aria-label="Workspace" className="md:hidden">
            <Separator />
            {NAV_LINKS.map((link) => renderLink(link, false))}
          </div>

          <Separator />
          <div role="group" aria-label="Organization">
            <GroupLabel>{orgName ?? "Organization"}</GroupLabel>
            {orgLinks.map((link) => renderLink(link, true))}
          </div>

          <Separator />
          <div role="group" aria-label="Developers">
            <GroupLabel>Developers</GroupLabel>
            {DOCS_LINKS.map((link) => renderLink(link, true))}
          </div>

          <Separator />
          <ThemeRow />

          <Separator />
          <button
            type="button"
            role="menuitem"
            tabIndex={-1}
            onClick={signOut}
            disabled={signingOut}
            className={cn(itemClass, "w-full disabled:opacity-60")}
          >
            <LogOut aria-hidden className="size-4 shrink-0" />
            {signingOut ? "Signing out…" : "Sign out"}
          </button>
        </div>
      ) : null}
    </div>
  );
}

const itemClass = cn(
  "flex items-center gap-2.5 rounded-md px-2.5 py-1.5 text-left text-sm text-muted-foreground transition-colors",
  "hover:bg-muted hover:text-foreground focus-visible:bg-muted focus-visible:text-foreground focus-visible:outline-none",
);

function Separator() {
  return <div role="separator" className="-mx-1.5 my-1.5 h-px bg-border" />;
}

function GroupLabel({ children }: { children: React.ReactNode }) {
  return (
    <p className="truncate px-2.5 pb-1 pt-1.5 text-xs font-medium text-muted-foreground/80">
      {children}
    </p>
  );
}

/**
 * Inline System / Light / Dark switch. Choosing a theme keeps the menu open so
 * the change is visible in place. The selection is unknown until mount (the
 * theme lives in localStorage), so nothing is marked checked on the server.
 */
function ThemeRow() {
  const { theme, setTheme } = useTheme();
  const [mounted, setMounted] = React.useState(false);
  React.useEffect(() => setMounted(true), []);
  const current = mounted ? (theme ?? "system") : null;

  return (
    <div
      role="group"
      aria-label="Theme"
      className="flex items-center justify-between gap-2 px-2.5 py-1"
    >
      <span className="text-sm text-muted-foreground">Theme</span>
      <div className="flex rounded-md border border-border p-0.5">
        {THEMES.map(({ value, label, Icon }) => {
          const checked = current === value;
          return (
            <button
              key={value}
              type="button"
              role="menuitemradio"
              tabIndex={-1}
              aria-checked={checked}
              aria-label={`${label} theme`}
              title={label}
              onClick={() => setTheme(value)}
              className={cn(
                "flex size-7 items-center justify-center rounded transition-colors",
                "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
                checked
                  ? "bg-secondary text-foreground"
                  : "text-muted-foreground hover:text-foreground",
              )}
            >
              <Icon aria-hidden className="size-3.5" />
            </button>
          );
        })}
      </div>
    </div>
  );
}

function initials(user: AccountMenuUser): string {
  const source = user.name?.trim() || user.email?.split("@")[0] || "";
  const parts = source.split(/[\s._-]+/).filter(Boolean);
  const letters =
    parts.length >= 2 ? `${parts[0]![0]}${parts[1]![0]}` : source.slice(0, 2);
  return letters.toUpperCase() || "?";
}

function Avatar({ user, size = "sm" }: { user: AccountMenuUser; size?: "sm" | "lg" }) {
  const [broken, setBroken] = React.useState(false);
  const box = size === "lg" ? "size-9 text-sm" : "size-7 text-[11px]";

  if (user.image && !broken) {
    return (
      // Provider avatars (Google etc.) come from arbitrary hosts, so a plain
      // <img> rather than next/image and its remotePatterns allowlist.
      // eslint-disable-next-line @next/next/no-img-element
      <img
        src={user.image}
        alt=""
        referrerPolicy="no-referrer"
        onError={() => setBroken(true)}
        className={cn(box, "shrink-0 rounded-full object-cover")}
      />
    );
  }
  return (
    <span
      aria-hidden
      className={cn(
        box,
        "flex shrink-0 items-center justify-center rounded-full bg-secondary font-semibold text-secondary-foreground",
      )}
    >
      {initials(user)}
    </span>
  );
}
