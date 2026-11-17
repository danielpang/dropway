// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Unit tests for lib/analytics-shared.ts — the pure property builder that every
// server event runs through. It always stamps `environment`, adds `org_id` when
// the event belongs to an org (and leaves it out otherwise, rather than sending
// a placeholder), and drops undefined event properties rather than recording
// them as empty keys. The org's name is not an event property: it is set on the
// org's PostHog group instead.

import { describe, expect, it } from "vitest";

import { buildEventProperties } from "@/lib/analytics-shared";

describe("buildEventProperties", () => {
  it("stamps environment and org_id", () => {
    const props = buildEventProperties({
      environment: "production",
      orgId: "org_123",
    });
    expect(props).toEqual({
      environment: "production",
      org_id: "org_123",
    });
  });

  it("leaves org_id out when the org is not known (no placeholder)", () => {
    for (const orgId of [null, undefined, ""]) {
      const props = buildEventProperties({ environment: "staging", orgId });
      expect(props).toEqual({ environment: "staging" });
    }
  });

  it("never sends an organization property", () => {
    expect("organization" in buildEventProperties({ environment: "production", orgId: "org_1" })).toBe(false);
  });

  it("merges event-specific properties and drops undefined values", () => {
    const props = buildEventProperties({
      environment: "production",
      orgId: "org_1",
      properties: { site_id: "s_1", site_slug: undefined, count: 0 },
    });
    expect(props.site_id).toBe("s_1");
    expect(props.count).toBe(0); // falsy-but-defined is kept
    expect("site_slug" in props).toBe(false); // undefined is dropped
  });

  it("lets event properties coexist with the base", () => {
    const props = buildEventProperties({
      environment: "dev",
      orgId: "org_1",
      properties: { hostname: "docs.acme.com" },
    });
    expect(props).toMatchObject({
      environment: "dev",
      org_id: "org_1",
      hostname: "docs.acme.com",
    });
  });
});
