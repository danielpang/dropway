// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Unit tests for the pure audit-action highlight matcher (lib/audit.ts
// `isSecurityAction`). The viewer highlights security-relevant rows by matching
// the dotted `action` string against a small pattern set; we assert it stays
// correct (and resilient) across the verb families the Go API may emit.
//
// `loadAuditPage` is excluded here — it awaits a live Go API call (api.listAudit)
// and is exercised by the integration suite; only the framework-free matcher is
// unit-tested.

import { describe, expect, it } from "vitest";

import { AUDIT_PAGE_SIZE } from "@/lib/audit";
import { isSecurityAction } from "@/lib/audit-actions";

describe("AUDIT_PAGE_SIZE", () => {
  it("is a fixed positive page size", () => {
    expect(AUDIT_PAGE_SIZE).toBe(25);
  });
});

describe("isSecurityAction (highlight matcher)", () => {
  it("highlights revocation / unshare / sharing-policy / access-mode families", () => {
    for (const action of [
      "token.revoked",
      "org.revoke_access",
      "site.unshared",
      "org.external_sharing.disabled",
      "org.external.sharing.enabled",
      "site.access_mode.changed",
      "site.access_change", // the canonical Go action (the audit-MEDIUM gap)
      "site.access.updated",
      "site.allowlist.added",
      "member.removed",
      "member.role.changed",
      "org.suspended",
      "billing.suspended",
      "billing.past_due",
      "token.issued",
      "site.password.set",
    ]) {
      expect(isSecurityAction(action)).toBe(true);
    }
  });

  it("matches case-insensitively", () => {
    expect(isSecurityAction("TOKEN.REVOKED")).toBe(true);
    expect(isSecurityAction("Member.Removed")).toBe(true);
  });

  it("does NOT highlight ordinary, non-security actions", () => {
    for (const action of [
      "site.created",
      "site.published",
      "version.uploaded",
      "domain.verified",
      "member.invited",
      "org.renamed",
    ]) {
      expect(isSecurityAction(action)).toBe(false);
    }
  });

  it("does not highlight `member.invited` despite the member.* family (anchored prefix)", () => {
    // The member pattern is anchored to removed|role, so an invite is not flagged.
    expect(isSecurityAction("member.invited")).toBe(false);
    expect(isSecurityAction("member.role.granted")).toBe(true);
  });

  it("the external-sharing pattern accepts `_`/`.` separators but not a hyphen", () => {
    // The pattern is /external[_.]sharing/ — underscore or dot only, by design.
    expect(isSecurityAction("org.external_sharing.disabled")).toBe(true);
    expect(isSecurityAction("org.external.sharing.disabled")).toBe(true);
    expect(isSecurityAction("org.external-sharing.disabled")).toBe(false);
  });

  it("returns false for null / undefined / empty action", () => {
    expect(isSecurityAction(null)).toBe(false);
    expect(isSecurityAction(undefined)).toBe(false);
    expect(isSecurityAction("")).toBe(false);
  });
});
