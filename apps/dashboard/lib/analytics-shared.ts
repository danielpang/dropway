/**
 * Shared, framework-free helpers for analytics event shaping. Kept dependency-
 * free (no posthog-node, no "server-only") so the pure property-building logic
 * can be unit-tested under node and reused by both client and server capture.
 */

/** The fixed properties every Dropway event carries, plus any event-specific
 * extras. `organization` and `environment` are always present so PostHog can
 * segment by tenant and deploy without per-event special-casing. */
export interface EventPropertyInput {
  environment: string;
  /** Active org id, or null/undefined when not yet known (e.g. fresh signup). */
  organization?: string | null;
  /** Human-readable org name, when cheaply available. */
  organizationName?: string | null;
  /** Event-specific properties (site_id, hostname, …). Undefined values are dropped. */
  properties?: Record<string, unknown>;
}

/** Sentinel used when an event fires before the user has an organization. */
export const NO_ORGANIZATION = "none";

/**
 * Build the flat property bag for an event: always stamps `environment` and
 * `organization` (defaulting to NO_ORGANIZATION), folds in the optional org name,
 * and merges event-specific properties last — with `undefined` values stripped
 * so PostHog doesn't record empty keys.
 */
export function buildEventProperties(
  input: EventPropertyInput,
): Record<string, unknown> {
  const base: Record<string, unknown> = {
    environment: input.environment,
    organization: input.organization || NO_ORGANIZATION,
  };
  if (input.organizationName) base.organization_name = input.organizationName;

  for (const [key, value] of Object.entries(input.properties ?? {})) {
    if (value !== undefined) base[key] = value;
  }
  return base;
}
