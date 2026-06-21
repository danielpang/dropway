// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Per-site visit analytics. Emits a `site_visit` event to PostHog for each
// HTML page served, so the dashboard can report "visits to each site". This is
// strictly best-effort and OFF the response path: it never throws, never blocks
// (the caller schedules it via `waitUntil`), and no-ops entirely when no
// POSTHOG_KEY is configured.
//
// Privacy: visitors are identified by an ANONYMIZED, daily-rotating hash of
// IP+User-Agent — never the raw IP. The hash rotates each UTC day so it can't be
// used as a stable cross-day identifier; PostHog only ever stores the opaque
// hash. This approximates unique visitors AND total visits without retaining PII.

import type { RouteValue } from "./route";

/** Worker vars consumed by the analytics path (all optional → disabled when unset). */
export interface AnalyticsEnv {
  /** PostHog project API key (`phc_…`). UNSET → analytics is a no-op. */
  POSTHOG_KEY?: string;
  /** PostHog ingestion host. Defaults to US cloud. */
  POSTHOG_HOST?: string;
  /** Deployment label stamped as the `environment` property (e.g. "production"). */
  ENVIRONMENT?: string;
  /** Salt for the visitor hash. Falls back to POSTHOG_KEY when unset. */
  VISIT_SALT?: string;
}

const DEFAULT_HOST = "https://us.posthog.com";

/**
 * Whether a served response should count as a site visit: a GET of an HTML
 * document. Assets (CSS/JS/images) and HEAD probes are excluded so the metric
 * reflects page visits rather than every sub-resource fetch.
 */
export function isVisit(method: string, contentType: string | null): boolean {
  if (method !== "GET") return false;
  if (!contentType) return false;
  return contentType.toLowerCase().includes("text/html");
}

/**
 * Privacy-preserving daily visitor id: hex(sha256(`day:salt:ip:ua`)) truncated
 * to 32 hex chars. `day` (UTC YYYY-MM-DD) makes it rotate every day; the salt
 * keeps it unguessable. Deterministic within a day for a given IP+UA so repeat
 * visits collapse to one visitor.
 */
export async function dailyVisitorId(input: {
  ip: string;
  ua: string;
  salt: string;
  now: Date;
}): Promise<string> {
  const day = input.now.toISOString().slice(0, 10); // YYYY-MM-DD (UTC)
  const data = `${day}:${input.salt}:${input.ip}:${input.ua}`;
  const digest = await crypto.subtle.digest(
    "SHA-256",
    new TextEncoder().encode(data),
  );
  const bytes = new Uint8Array(digest);
  let hex = "";
  for (let i = 0; i < 16; i++) {
    hex += (bytes[i] ?? 0).toString(16).padStart(2, "0");
  }
  return hex;
}

/** The PostHog `/capture` payload for a site visit (pure → unit-tested). */
export function buildVisitPayload(input: {
  apiKey: string;
  distinctId: string;
  host: string;
  path: string;
  route: RouteValue;
  environment: string;
  now: Date;
}): Record<string, unknown> {
  return {
    api_key: input.apiKey,
    event: "site_visit",
    distinct_id: input.distinctId,
    timestamp: input.now.toISOString(),
    properties: {
      $host: input.host,
      site_id: input.route.site_id,
      org_id: input.route.org_id,
      version_id: input.route.version_id,
      access_mode: input.route.access_mode,
      path: input.path,
      environment: input.environment,
      $lib: "dropway-serving-worker",
    },
  };
}

/** Minimal fetch surface (so tests can inject without the runtime global). */
export type CaptureFetch = (
  input: string,
  init: { method: string; headers: Record<string, string>; body: string },
) => Promise<unknown>;

export interface VisitContext {
  request: Request;
  route: RouteValue;
  url: URL;
  /** Content-Type of the response being served (decides if it's a page visit). */
  contentType: string | null;
  now: Date;
}

/**
 * Best-effort site-visit capture. No-ops without a key or for non-page
 * responses; otherwise POSTs a `site_visit` event to PostHog. Never throws.
 * Scheduled by the caller via `waitUntil`, so it runs after the response is sent.
 */
export async function captureSiteVisit(
  env: AnalyticsEnv,
  ctx: VisitContext,
  fetchImpl: CaptureFetch = (input, init) =>
    fetch(input, init as RequestInit),
): Promise<void> {
  const key = env.POSTHOG_KEY;
  if (!key) return;
  if (!isVisit(ctx.request.method, ctx.contentType)) return;

  try {
    const ip = ctx.request.headers.get("CF-Connecting-IP") ?? "";
    const ua = ctx.request.headers.get("User-Agent") ?? "";
    const distinctId = await dailyVisitorId({
      ip,
      ua,
      salt: env.VISIT_SALT ?? key,
      now: ctx.now,
    });
    const payload = buildVisitPayload({
      apiKey: key,
      distinctId,
      host: ctx.url.host,
      path: ctx.url.pathname,
      route: ctx.route,
      environment: env.ENVIRONMENT ?? "production",
      now: ctx.now,
    });
    const host = (env.POSTHOG_HOST ?? DEFAULT_HOST).replace(/\/$/, "");
    await fetchImpl(`${host}/capture/`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(payload),
    });
  } catch {
    // Analytics must never affect content serving.
  }
}
