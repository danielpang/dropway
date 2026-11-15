import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";

vi.mock("next/navigation", () => ({ usePathname: () => "/rooms" }));
import { MainNav, visibleNavLinks } from "@/components/main-nav";

describe("Rooms navigation", () => {
  it("requires collaboration rollout even for admins and accepted guests", () => {
    for (const admin of [false, true]) {
      for (const shared of [false, true]) {
        expect(visibleNavLinks(admin, shared).some((link) => link.href === "/rooms")).toBe(false);
        expect(visibleNavLinks(admin, shared, true).some((link) => link.href === "/rooms")).toBe(true);
      }
    }
  });

  it("preserves the independent shared-project visibility rule", () => {
    expect(visibleNavLinks(false, false, true).some((link) => link.shared)).toBe(false);
    expect(visibleNavLinks(false, true, true).some((link) => link.shared)).toBe(true);
  });

  it("renders the active Rooms link only for enabled users", () => {
    expect(renderToStaticMarkup(createElement(MainNav))).not.toContain('href="/rooms"');
    const html = renderToStaticMarkup(createElement(MainNav, { collaborationEnabled: true }));
    expect(html).toMatch(/<a[^>]*aria-current="page"[^>]*href="\/rooms"[^>]*>Rooms<\/a>/);
  });
});
