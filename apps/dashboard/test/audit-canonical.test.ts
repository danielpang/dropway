// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Guards isSecurityAction against the CANONICAL audit-action vocabulary the Go
// API actually emits (internal/audit/audit.go) — not hand-picked strings. This
// is the regression test for the audit MEDIUM finding: site.access_change must be
// highlighted (the old matcher silently missed it).

import { describe, expect, it } from "vitest";

import { isSecurityAction } from "@/lib/audit-actions";

// Mirrors internal/audit/audit.go's Action constants + the expected highlight.
const CANONICAL_ACTIONS: Array<[action: string, highlight: boolean]> = [
  ["site.create", false],
  ["site.access_change", true], // the regression: access-mode/sharing-tier flip
  ["site.allowlist_add", true],
  ["site.allowlist_remove", true],
  ["site.revoke_access", true],
  ["org.allow_external_sharing", true],
  ["member.revoke", true],
  ["member.invite", true], // membership addition: who can reach the org
  ["member.join", true],
  ["deploy.finalize", false],
  ["deploy.publish", false],
  ["domain.add", false],
  ["domain.verify", false],
];

describe("isSecurityAction over the canonical Go action vocabulary", () => {
  for (const [action, highlight] of CANONICAL_ACTIONS) {
    it(`${action} → ${highlight ? "highlighted" : "normal"}`, () => {
      expect(isSecurityAction(action)).toBe(highlight);
    });
  }

  it("handles null/empty defensively", () => {
    expect(isSecurityAction(null)).toBe(false);
    expect(isSecurityAction(undefined)).toBe(false);
    expect(isSecurityAction("")).toBe(false);
  });
});
