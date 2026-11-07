// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The review surface is default-off. Candidate previews may target a platform
// content host only. Custom domains, the dashboard origin, and credentialed
// URLs stay closed. The serving worker is not asked to inject a bridge.

import { describe, expect, it } from "vitest";

import {
  bridgePrerequisites,
  canOpenCandidatePreview,
  candidatePreviewSandbox,
  isCollabReviewEnabled,
  previewFrameDecision,
  supportedKinds,
  trustedDashboardOrigin,
} from "@/lib/collab";

describe("supportedKinds", () => {
  it("offers precise modes only on opt-in instrumented HTML", () => {
    expect(supportedKinds("instrumented-html")).toEqual([
      "point",
      "text",
      "component",
      "page",
    ]);
  });

  it("lets an image take a pin or a page comment", () => {
    expect(supportedKinds("image")).toEqual(["point", "page"]);
  });

  it("falls back to the whole page for plain or unknown HTML", () => {
    expect(supportedKinds("html")).toEqual(["page"]);
    expect(supportedKinds("unknown")).toEqual(["page"]);
  });
});

describe("isCollabReviewEnabled", () => {
  it("stays off unless the flag is exactly 1", () => {
    expect(isCollabReviewEnabled({})).toBe(false);
    expect(isCollabReviewEnabled({ NEXT_PUBLIC_COLLAB_REVIEW: "true" })).toBe(false);
    expect(isCollabReviewEnabled({ NEXT_PUBLIC_COLLAB_REVIEW: "1" })).toBe(true);
  });
});

describe("bridgePrerequisites", () => {
  it("reports the gate, the content suffix, and the closed trust gaps", () => {
    const report = bridgePrerequisites({});
    expect(report.enabled).toBe(false);
    expect(report.platformContentSuffixConfigured).toBe(true);
    expect(report.customDomainsTrusted).toBe(false);
    expect(report.servingWorkerInjectsBridge).toBe(false);
    expect(report.opaqueFrameSelections).toBe(false);
    expect(report.reasons.join(" ")).toMatch(/default off/i);
    expect(report.reasons.join(" ")).toMatch(/custom domain/i);
    expect(report.reasons.join(" ")).toMatch(/opaque/i);
  });
});

describe("trustedDashboardOrigin", () => {
  it("uses only an explicit dashboard origin and ignores content hosts", () => {
    expect(trustedDashboardOrigin({})).toBeNull();
    expect(trustedDashboardOrigin({ BETTER_AUTH_URL: "https://app.dropway.dev" })).toBe(
      "https://app.dropway.dev",
    );
    expect(trustedDashboardOrigin({ NEXT_PUBLIC_BETTER_AUTH_URL: "http://127.0.0.1:3000" })).toBe(
      "http://127.0.0.1:3000",
    );
    expect(trustedDashboardOrigin({ BETTER_AUTH_URL: "https://acme.dropwaycontent.com" })).toBeNull();
    expect(trustedDashboardOrigin({ BETTER_AUTH_URL: "https://app.dropway.dev/extra" })).toBeNull();
    expect(trustedDashboardOrigin({ BETTER_AUTH_URL: "http://evil.example" })).toBeNull();
    expect(trustedDashboardOrigin({ BETTER_AUTH_URL: "*" })).toBeNull();
  });
});

describe("candidate preview", () => {
  const parent = "https://app.dropway.dev";

  it("allows a platform content origin and rejects the others", () => {
    expect(canOpenCandidatePreview("https://acme.dropwaycontent.com", parent)).toEqual({
      ok: true,
    });
    expect(canOpenCandidatePreview("https://docs.example.com", parent).ok).toBe(false);
    expect(canOpenCandidatePreview("https://app.dropway.dev", parent).ok).toBe(false);
    expect(canOpenCandidatePreview("https://dropwaycontent.com", parent).ok).toBe(false);
    expect(canOpenCandidatePreview("https://evil-dropwaycontent.com", parent).ok).toBe(false);
    expect(canOpenCandidatePreview("http://acme.dropwaycontent.com", parent).ok).toBe(false);
  });

  it("sandboxes the frame off the app origin and drops credentialed URLs", () => {
    expect(candidatePreviewSandbox()).toBe("allow-scripts");
    expect(candidatePreviewSandbox().split(/\s+/)).not.toContain("allow-same-origin");
    const decision = previewFrameDecision(
      "https://acme.dropwaycontent.com/index.html",
      parent,
    );
    expect(decision).toEqual({
      ok: true,
      src: "https://acme.dropwaycontent.com/index.html",
      sandbox: "allow-scripts",
      referrerPolicy: "no-referrer",
      credentialless: true,
    });
    expect(
      previewFrameDecision(
        "https://acme.dropwaycontent.com/?token=secret",
        parent,
      ),
    ).toEqual({ ok: false, reason: "credential-in-url" });
  });
});
