"use client";

import { useEffect } from "react";
import posthog from "posthog-js";

/**
 * Last-resort error boundary. Unlike app/error.tsx, this catches errors thrown
 * by the ROOT layout itself, and therefore REPLACES it — so it must render its
 * own <html>/<body>. Because the root layout (fonts, globals.css, theme) is gone
 * here, the page is intentionally self-contained with inline styles so it renders
 * even when nothing else can. This is the true "the dashboard failed to load"
 * fallback; the common API/page failures are handled by the branded boundaries
 * above (app/error.tsx, app/(app)/error.tsx).
 */
export default function GlobalError({
  error,
  reset,
}: {
  error: Error & { digest?: string };
  reset: () => void;
}) {
  useEffect(() => {
    console.error(error);
    try {
      // Best-effort: posthog may not have initialized if the root failed early.
      posthog?.captureException?.(error);
      posthog?.capture?.("error_page_viewed", { status: 500 });
    } catch {
      /* never mask the original error */
    }
  }, [error]);

  return (
    <html lang="en">
      <body
        style={{
          colorScheme: "light dark",
          margin: 0,
          minHeight: "100vh",
          display: "grid",
          placeItems: "center",
          padding: "2rem",
          font: "15px/1.6 system-ui, -apple-system, Segoe UI, Roboto, sans-serif",
        }}
      >
        <main style={{ maxWidth: "32rem", textAlign: "center" }}>
          {/* Static burst pipe — no animation lib (this is the last-resort
              boundary and must render with zero dependencies). */}
          <svg
            width="150"
            height="120"
            viewBox="0 0 260 210"
            fill="none"
            aria-hidden
            style={{ display: "block", margin: "0 auto 1.25rem" }}
          >
            <defs>
              <linearGradient id="ge-metal" x1="0" y1="0" x2="0" y2="1">
                <stop offset="0%" stopColor="#e2e8f0" />
                <stop offset="100%" stopColor="#64748b" />
              </linearGradient>
            </defs>
            <rect x="6" y="64" width="14" height="56" rx="3" fill="#94a3b8" stroke="#334155" />
            <rect x="240" y="64" width="14" height="56" rx="3" fill="#94a3b8" stroke="#334155" />
            <rect x="14" y="74" width="232" height="38" rx="19" fill="url(#ge-metal)" stroke="#475569" strokeWidth="1.5" />
            <rect x="115" y="70" width="36" height="46" rx="5" fill="#94a3b8" stroke="#334155" strokeWidth="1.5" />
            <path
              d="M133 73 L129 82 L136 89 L128 97 L134 105 L130 112"
              fill="none"
              stroke="#0f172a"
              strokeWidth="2.4"
              strokeLinejoin="round"
              strokeLinecap="round"
            />
            {/* Dripping water */}
            <path d="M134,112 C136.4,115.6 138.6,117.6 138.6,120.2 a4.6 4.6 0 1 1 -9.2 0 C129.4,117.6 131.6,115.6 134,112 Z" fill="#0ea5e9" />
            <path d="M134,150 C136.4,153.6 138.6,155.6 138.6,158.2 a4.6 4.6 0 1 1 -9.2 0 C129.4,155.6 131.6,153.6 134,150 Z" fill="#0ea5e9" opacity="0.85" />
            <ellipse cx="134" cy="198" rx="30" ry="5.5" fill="#0ea5e9" opacity="0.8" />
          </svg>
          <h1 style={{ fontSize: "1.5rem", margin: "0 0 .5rem" }}>
            Something went wrong
          </h1>
          <p style={{ opacity: 0.7, margin: "0 0 1.5rem" }}>
            The dashboard ran into an unexpected error and couldn&rsquo;t load.
            This is usually temporary — please try again.
            {error.digest ? ` (Reference: ${error.digest})` : ""}
          </p>
          <button
            type="button"
            onClick={() => reset()}
            style={{
              font: "inherit",
              fontWeight: 600,
              cursor: "pointer",
              padding: ".5rem 1rem",
              borderRadius: ".375rem",
              border: "1px solid currentColor",
              background: "transparent",
              color: "inherit",
            }}
          >
            Try again
          </button>
        </main>
      </body>
    </html>
  );
}
