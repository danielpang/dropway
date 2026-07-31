// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Unit tests for the className merge helper (lib/utils.ts `cn`). It wraps
// clsx + tailwind-merge so shadcn-style components can accept overriding
// `className` props; we assert conditional inclusion AND Tailwind conflict
// resolution (later class wins), which is the behavior callers rely on.

import { describe, expect, it } from "vitest";

import { cn, formatBytes, isUuid } from "@/lib/utils";

describe("cn (clsx + tailwind-merge)", () => {
  it("joins plain class strings", () => {
    expect(cn("a", "b", "c")).toBe("a b c");
  });

  it("drops falsy / conditional values (clsx semantics)", () => {
    expect(cn("a", false && "b", null, undefined, 0 as unknown as string, "c")).toBe("a c");
    expect(cn("base", { active: true, disabled: false })).toBe("base active");
  });

  it("flattens arrays of class values", () => {
    expect(cn(["a", "b"], "c")).toBe("a b c");
  });

  it("resolves conflicting Tailwind utilities so the LAST one wins", () => {
    // tailwind-merge dedupes same-property utilities, keeping the later class.
    expect(cn("px-2", "px-4")).toBe("px-4");
    expect(cn("text-sm text-base")).toBe("text-base");
    expect(cn("p-2", "p-4", "p-8")).toBe("p-8");
  });

  it("keeps non-conflicting utilities and lets an override replace the base", () => {
    expect(cn("rounded", "border")).toBe("rounded border");
    // A caller's className override beats the component default for the same prop.
    expect(cn("bg-white text-black", "bg-black")).toBe("text-black bg-black");
  });

  it("returns an empty string when given nothing meaningful", () => {
    expect(cn()).toBe("");
    expect(cn(false, null, undefined)).toBe("");
  });
});

describe("formatBytes (human storage sizes)", () => {
  it("renders whole bytes without decimals", () => {
    expect(formatBytes(0)).toBe("0 B");
    expect(formatBytes(512)).toBe("512 B");
  });

  it("scales to KB/MB/GB with one decimal (decimal units)", () => {
    expect(formatBytes(1000)).toBe("1.0 KB");
    expect(formatBytes(1500)).toBe("1.5 KB");
    expect(formatBytes(4096)).toBe("4.1 KB");
    expect(formatBytes(1_000_000)).toBe("1.0 MB");
    expect(formatBytes(2_500_000_000)).toBe("2.5 GB");
  });

  it("treats 0, negatives, and non-finite input as 0 B", () => {
    expect(formatBytes(-5)).toBe("0 B");
    expect(formatBytes(NaN)).toBe("0 B");
    expect(formatBytes(Infinity)).toBe("0 B");
  });
});

describe("isUuid (resource-id shape guard for [id] routes)", () => {
  it("accepts canonical UUIDs, case-insensitively", () => {
    // gen_random_uuid() output and the mixed/upper-case variants.
    expect(isUuid("019f5ccd-e63e-7da3-9fa8-054554637fc6")).toBe(true);
    expect(isUuid("123e4567-e89b-12d3-a456-426614174000")).toBe(true);
    expect(isUuid("123E4567-E89B-12D3-A456-426614174000")).toBe(true);
  });

  it("rejects the favicon/crawler probe segments that caused the 401 noise", () => {
    // These reach /sites/[id] (and siblings) as a stray path segment; each must
    // 404 locally instead of being forwarded to the API and rethrown as a 401.
    expect(isUuid("favicon.ico")).toBe(false);
    expect(isUuid("favicon.png")).toBe(false);
    expect(isUuid("apple-touch-icon.png")).toBe(false);
    expect(isUuid("robots.txt")).toBe(false);
  });

  it("rejects other non-UUID shapes", () => {
    expect(isUuid("")).toBe(false);
    expect(isUuid("not-a-uuid")).toBe(false);
    // Wrong length / stray characters around an otherwise-valid UUID.
    expect(isUuid(" 019f5ccd-e63e-7da3-9fa8-054554637fc6")).toBe(false);
    expect(isUuid("019f5ccd-e63e-7da3-9fa8-054554637fc6/settings")).toBe(false);
    expect(isUuid("019f5ccde63e7da39fa8054554637fc6")).toBe(false);
  });
});
