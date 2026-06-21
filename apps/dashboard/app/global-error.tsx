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
