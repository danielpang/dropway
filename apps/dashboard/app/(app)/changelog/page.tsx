import type { Metadata } from "next";
import Link from "next/link";
import { ArrowLeft } from "lucide-react";

import { Permalink } from "@/components/changelog/permalink";
import { CHANGELOG, type ChangelogEntry } from "@/lib/changelog";

export const metadata: Metadata = {
  title: "Changelog",
  description: "New features and improvements in Dropway.",
};

/** Format a YYYY-MM-DD string in UTC so it never drifts a day by timezone. */
function formatDate(iso: string): string {
  const [y, m, d] = iso.split("-").map(Number) as [number, number, number];
  return new Date(Date.UTC(y, m - 1, d)).toLocaleDateString("en-US", {
    year: "numeric",
    month: "long",
    day: "numeric",
    timeZone: "UTC",
  });
}

/**
 * The in-app changelog. A Cursor-style layout: a sticky date rail on the left of
 * each release, the notes on the right, and a copy-able permalink on every
 * release and every individual change (hover a heading to reveal the link icon),
 * so a link can point at one specific item. Content lives in lib/changelog.ts.
 */
export default function ChangelogPage() {
  return (
    <div className="mx-auto max-w-4xl space-y-12">
      <div className="space-y-6">
        <Link
          href="/dashboard"
          className="inline-flex items-center gap-1.5 rounded-sm text-sm text-muted-foreground transition-colors hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background"
        >
          <ArrowLeft className="size-4" aria-hidden />
          Back to sites
        </Link>
        <div className="space-y-3">
          <p className="font-mono text-xs font-semibold uppercase tracking-wider text-primary">
            Changelog
          </p>
          <h1 className="text-3xl font-semibold tracking-tight sm:text-4xl">
            What is new in Dropway
          </h1>
          <p className="text-pretty text-base leading-relaxed text-muted-foreground sm:text-lg">
            New features and improvements, newest first. Hover any heading to
            grab a link straight to it.
          </p>
        </div>
      </div>

      <div className="divide-y divide-border border-t border-border">
        {CHANGELOG.map((entry) => (
          <Entry key={entry.id} entry={entry} />
        ))}
      </div>
    </div>
  );
}

function Entry({ entry }: { entry: ChangelogEntry }) {
  return (
    <article className="grid gap-6 py-10 sm:py-14 md:grid-cols-[10rem_1fr] md:gap-10">
      {/* Left rail: date sits sticky beside the notes on wide screens. */}
      <div className="md:sticky md:top-24 md:self-start">
        <time
          dateTime={entry.date}
          className="text-sm font-medium text-muted-foreground"
        >
          {formatDate(entry.date)}
        </time>
      </div>

      <div className="space-y-6">
        <div className="space-y-3">
          <Permalink
            id={entry.id}
            as="h2"
            headingClassName="text-2xl font-semibold tracking-tight"
          >
            <span className="inline-flex items-center gap-2.5">
              {entry.title}
              {entry.label ? (
                <span className="rounded-full border border-primary/30 bg-primary/[0.06] px-2.5 py-0.5 text-xs font-medium text-primary">
                  {entry.label}
                </span>
              ) : null}
            </span>
          </Permalink>
          <p className="text-pretty leading-relaxed text-muted-foreground">
            {entry.summary}
          </p>
        </div>

        <div className="space-y-5">
          {entry.changes.map((change) => (
            <div key={change.id} className="space-y-1.5">
              <Permalink
                id={change.id}
                as="h3"
                headingClassName="text-base font-semibold tracking-tight text-foreground"
              >
                {change.title}
              </Permalink>
              <p className="text-[0.95rem] leading-relaxed text-muted-foreground">
                {change.body}
              </p>
            </div>
          ))}
        </div>
      </div>
    </article>
  );
}
