/**
 * Shared, framework-free helpers for analytics event shaping. Kept dependency-
 * free (no posthog-node, no "server-only") so the pure property-building logic
 * can be unit-tested under node and reused by both client and server capture.
 */

/** The fixed properties every Dropway event carries, plus any event-specific
 * extras. `environment` is always present; `org_id` is present whenever the
 * event belongs to an org. The org's display name is not part of this shared
 * shape: it is set on the org's PostHog group (see identifyOrganization), so it
 * shows wherever events are broken down by organization and follows renames.
 * (organization_created alone also sends it, as organization_name.) */
export interface EventPropertyInput {
  environment: string;
  /** Active org id, or null/undefined when not known (e.g. a fresh signup). */
  orgId?: string | null;
  /** Event-specific properties (site_id, hostname, …). Undefined values are dropped. */
  properties?: Record<string, unknown>;
}

/**
 * Build the flat property bag for an event: always stamps `environment`, adds
 * `org_id` when the org is known (and leaves it out otherwise, rather than a
 * placeholder), and merges event-specific properties last — with `undefined`
 * values stripped so PostHog doesn't record empty keys.
 */
export function buildEventProperties(
  input: EventPropertyInput,
): Record<string, unknown> {
  const base: Record<string, unknown> = { environment: input.environment };
  if (input.orgId) base.org_id = input.orgId;

  for (const [key, value] of Object.entries(input.properties ?? {})) {
    if (value !== undefined) base[key] = value;
  }
  return base;
}
