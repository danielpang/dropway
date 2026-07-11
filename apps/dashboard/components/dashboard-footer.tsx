import Link from "next/link";

import { ContactDialog } from "@/components/contact/contact-dialog";

/**
 * App shell footer. Sits below the main content on every authenticated page with
 * the Changelog link and the Contact popup. `contactEnabled` is resolved
 * server-side (SUPPORT_EMAIL is set) so the Contact control only appears on a
 * deployment that can actually receive the message.
 */
export function DashboardFooter({
  contactEnabled,
}: {
  contactEnabled: boolean;
}) {
  const year = new Date().getFullYear();
  const linkClass =
    "rounded-sm text-muted-foreground transition-colors hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background";

  return (
    <footer className="border-t border-border">
      <div className="container flex flex-col items-center justify-between gap-3 py-6 text-sm sm:flex-row">
        <p className="text-muted-foreground">
          &copy; {year} Dropway
        </p>
        <nav
          aria-label="Footer"
          className="flex items-center gap-4 font-medium"
        >
          <Link href="/changelog" className={linkClass}>
            Changelog
          </Link>
          {contactEnabled ? <ContactDialog className="font-medium" /> : null}
        </nav>
      </div>
    </footer>
  );
}
