import Link from "next/link";

import { ThemeToggle } from "@/components/theme-toggle";

/** Centered, theme-aware shell for the accept-invitation flow (mirrors /authz). */
export default function AcceptInvitationLayout({
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
            S
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
