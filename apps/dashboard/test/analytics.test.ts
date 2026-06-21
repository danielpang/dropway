// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Unit tests for lib/analytics-shared.ts — the pure property builder that every
// server/client event runs through. It guarantees `environment` + `organization`
// are always present (so PostHog can segment by deploy + tenant) and that
// undefined event properties are dropped rather than recorded as empty keys.

import { describe, expect, it } from "vitest";

import {
  NO_ORGANIZATION,
  buildEventProperties,
} from "@/lib/analytics-shared";

describe("buildEventProperties", () => {
  it("always stamps environment and organization", () => {
    const props = buildEventProperties({
      environment: "production",
      organization: "org_123",
    });
    expect(props).toEqual({
      environment: "production",
      organization: "org_123",
    });
  });

  it("defaults a missing/empty organization to the NO_ORGANIZATION sentinel", () => {
    for (const org of [null, undefined, ""]) {
      const props = buildEventProperties({ environment: "staging", organization: org });
      expect(props.organization).toBe(NO_ORGANIZATION);
      expect(props.environment).toBe("staging");
    }
  });

  it("includes organization_name only when provided", () => {
    expect(
      buildEventProperties({
        environment: "production",
        organization: "org_1",
        organizationName: "Acme",
      }).organization_name,
    ).toBe("Acme");

    expect(
      "organization_name" in
        buildEventProperties({ environment: "production", organization: "org_1" }),
    ).toBe(false);
  });

  it("merges event-specific properties and drops undefined values", () => {
    const props = buildEventProperties({
      environment: "production",
      organization: "org_1",
      properties: { site_id: "s_1", site_slug: undefined, count: 0 },
    });
    expect(props.site_id).toBe("s_1");
    expect(props.count).toBe(0); // falsy-but-defined is kept
    expect("site_slug" in props).toBe(false); // undefined is dropped
  });

  it("lets event properties override nothing reserved but coexist with the base", () => {
    const props = buildEventProperties({
      environment: "dev",
      organization: "org_1",
      properties: { hostname: "docs.acme.com" },
    });
    expect(props).toMatchObject({
      environment: "dev",
      organization: "org_1",
      hostname: "docs.acme.com",
    });
  });
});
