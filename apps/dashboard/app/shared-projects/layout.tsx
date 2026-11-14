import type { Metadata } from "next";
import Link from "next/link";
import { BrandMark } from "@/components/brand-mark";
import { SharedProjectSignOut } from "@/components/shared-projects/sign-out";
export const metadata: Metadata = {
  title: "Shared projects",
  referrer: "no-referrer",
  robots: { index: false, follow: false },
};
export default function SharedProjectsLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return (
    <div className="min-h-dvh">
      <header className="border-b border-border">
        <div className="container flex h-14 items-center justify-between">
          <Link
            href="/shared-projects"
            className="flex items-center gap-2 font-medium"
          >
            <BrandMark />
            Shared with me
          </Link>
          <SharedProjectSignOut />
        </div>
      </header>
      <main className="container space-y-6 py-8">{children}</main>
    </div>
  );
}
