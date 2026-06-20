import Link from "next/link";

import { ThemeToggle } from "@/components/theme-toggle";

/**
 * Shell for the viewer authz exchange. Mirrors the auth route group's quiet,
 * theme-aware backdrop: this is a first-impression, platform-controlled surface
 * (the password prompt + access decisions) that hostile tenant JS can never
 * render or script, so it deliberately looks like Dropway, not the content.
 */
export default function AuthzLayout({
  children,
}: Readonly<{ children: React.ReactNode }>) {
  return (
    <div className="auth-backdrop relative flex min-h-dvh flex-col">
      <header className="flex items-center justify-between px-6 py-5">
        <Link
          href="/"
          className="flex items-center gap-2 text-sm font-semibold tracking-tight focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background rounded-md"
        >
          <span
            aria-hidden
            className="grid size-6 place-items-center rounded-md bg-primary text-primary-foreground text-xs font-bold"
          >
            D
          </span>
          Dropway
        </Link>
        <ThemeToggle />
      </header>

      <main className="flex flex-1 items-center justify-center px-4 pb-16">
        <div className="w-full max-w-sm animate-fade-in">{children}</div>
      </main>
    </div>
  );
}
