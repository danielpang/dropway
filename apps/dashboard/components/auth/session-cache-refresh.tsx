"use client";

import { useEffect } from "react";

import { getSession } from "@/lib/auth-client";

/**
 * Keeps Better Auth's session cookie cache warm (lib/auth.ts
 * `session.cookieCache`, maxAge 5 min).
 *
 * Server components can read the signed cache cookie but can never SET one
 * (Next.js forbids cookies during RSC render), so only requests through the
 * /api/auth route handler re-sign it. Nothing else in the app calls get-session
 * from the browser, which means that without this ping the cookie would expire
 * five minutes after sign-in and every subsequent server render would silently
 * fall back to per-call Postgres session lookups — exactly the cost the cache
 * exists to remove.
 *
 * One fetch on mount (each full page load), then every 4 minutes — just inside
 * the 5-minute cookie window — while the tab is visible; hidden tabs skip the
 * tick and re-warm on their next interval after becoming visible. Runs after
 * hydration, entirely off the rendering critical path, and a failed ping only
 * means the DB fallback (today's behavior).
 */
export function SessionCacheRefresh() {
  useEffect(() => {
    void getSession();
    const id = setInterval(() => {
      if (document.visibilityState === "visible") void getSession();
    }, 4 * 60 * 1000);
    return () => clearInterval(id);
  }, []);

  return null;
}
