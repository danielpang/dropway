// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Unit tests for the className merge helper (lib/utils.ts `cn`). It wraps
// clsx + tailwind-merge so shadcn-style components can accept overriding
// `className` props; we assert conditional inclusion AND Tailwind conflict
// resolution (later class wins), which is the behavior callers rely on.

import { describe, expect, it } from "vitest";

import { cn } from "@/lib/utils";

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
