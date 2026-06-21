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
          {/* Static cracked prism — no animation lib (this is the last-resort
              boundary and must render with zero dependencies). */}
          <svg
            width="120"
            height="110"
            viewBox="0 0 240 220"
            fill="none"
            aria-hidden
            style={{ display: "block", margin: "0 auto 1.25rem" }}
          >
            <defs>
              <linearGradient id="ge-prism" x1="0" y1="0" x2="1" y2="1">
                <stop offset="0%" stopColor="#f43f5e" />
                <stop offset="50%" stopColor="#22c55e" />
                <stop offset="100%" stopColor="#a855f7" />
              </linearGradient>
            </defs>
            <polygon
              points="120,30 68,150 172,150"
              fill="url(#ge-prism)"
              fillOpacity="0.85"
              stroke="rgba(255,255,255,0.5)"
              strokeWidth="1.5"
              strokeLinejoin="round"
            />
            {/* Fracture lines */}
            <path
              d="M120 30 L112 150 M120 92 L68 150 M120 92 L172 150"
              stroke="rgba(255,255,255,0.7)"
              strokeWidth="1.5"
            />
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
