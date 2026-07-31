// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Unit tests for the Sites listing rule (lib/sites-visibility.ts): which sites
// the org-wide /dashboard list shows, and the owner byline label. Both are pure,
// so they're covered here rather than through a render — the page itself is an
// RSC and there is no DOM runner.
//
// The two footguns these pin down: `feed_visible` is OPTIONAL on the generated
// Site schema (absent must read as visible, matching the API default of true),
// and an absent `owner_id` must never match a null viewer id.

import { describe, expect, it } from "vitest";

import { isSiteVisibleTo, ownerLabel } from "@/lib/sites-visibility";

const ME = "user-me";
const THEM = "user-them";

const member = { userId: ME, canManage: false };
const admin = { userId: ME, canManage: true };
const anonymous = { userId: null, canManage: false };

describe("isSiteVisibleTo", () => {
  const cases: Array<{
    name: string;
    site: { owner_id?: string; feed_visible?: boolean };
    viewer: { userId: string | null; canManage: boolean };
    visible: boolean;
  }> = [
    {
      name: "member sees a teammate's shared site",
      site: { owner_id: THEM, feed_visible: true },
      viewer: member,
      visible: true,
    },
    {
      name: "member does NOT see a teammate's hidden site",
      site: { owner_id: THEM, feed_visible: false },
      viewer: member,
      visible: false,
    },
    {
      name: "member sees their OWN hidden site",
      site: { owner_id: ME, feed_visible: false },
      viewer: member,
      visible: true,
    },
    {
      name: "admin sees a teammate's hidden site",
      site: { owner_id: THEM, feed_visible: false },
      viewer: admin,
      visible: true,
    },
    {
      name: "admin sees an unowned hidden site",
      site: { feed_visible: false },
      viewer: admin,
      visible: true,
    },
    {
      name: "absent feed_visible reads as visible (API default is true)",
      site: { owner_id: THEM },
      viewer: member,
      visible: true,
    },
    {
      name: "a null viewer id never matches an owner_id",
      site: { owner_id: THEM, feed_visible: false },
      viewer: anonymous,
      visible: false,
    },
    {
      name: "a null viewer id never matches an ABSENT owner_id either",
      site: { feed_visible: false },
      viewer: anonymous,
      visible: false,
    },
    {
      name: "a hidden site with no owner is invisible to a plain member",
      site: { feed_visible: false },
      viewer: member,
      visible: false,
    },
    {
      name: "an empty site object is visible (nothing marks it hidden)",
      site: {},
      viewer: anonymous,
      visible: true,
    },
  ];

  for (const c of cases) {
    it(c.name, () => {
      expect(isSiteVisibleTo(c.site, c.viewer)).toBe(c.visible);
    });
  }

  it("keeps the viewer's own sites when filtering a mixed list", () => {
    const sites = [
      { id: "a", owner_id: ME, feed_visible: false },
      { id: "b", owner_id: THEM, feed_visible: false },
      { id: "c", owner_id: THEM, feed_visible: true },
      { id: "d", owner_id: ME },
    ];
    expect(
      sites.filter((s) => isSiteVisibleTo(s, member)).map((s) => s.id),
    ).toEqual(["a", "c", "d"]);
    expect(sites.filter((s) => isSiteVisibleTo(s, admin)).map((s) => s.id)).toEqual([
      "a",
      "b",
      "c",
      "d",
    ]);
  });
});

describe("ownerLabel", () => {
  const members = [
    { userId: ME, name: "Me Myself", email: "me@acme.test" },
    { userId: THEM, name: "Ana Ruiz", email: "ana@acme.test" },
    { userId: "user-nameless", name: null, email: "nameless@acme.test" },
  ];

  it("labels the viewer's own site 'You'", () => {
    expect(ownerLabel(ME, { userId: ME }, members)).toBe("You");
  });

  it("uses a teammate's name from the roster", () => {
    expect(ownerLabel(THEM, { userId: ME }, members)).toBe("Ana Ruiz");
  });

  it("falls back to the teammate's email when they have no name", () => {
    expect(ownerLabel("user-nameless", { userId: ME }, members)).toBe(
      "nameless@acme.test",
    );
  });

  it("falls back to 'A teammate' for an owner who has left the org", () => {
    expect(ownerLabel("user-gone", { userId: ME }, members)).toBe("A teammate");
  });

  it("falls back to 'A teammate' for an absent owner_id", () => {
    expect(ownerLabel(undefined, { userId: ME }, members)).toBe("A teammate");
  });

  it("does not say 'You' when the viewer id is unknown", () => {
    expect(ownerLabel(THEM, { userId: null }, members)).toBe("Ana Ruiz");
    expect(ownerLabel("user-gone", { userId: null }, members)).toBe("A teammate");
  });

  it("falls back to 'A teammate' when the roster row is blank", () => {
    expect(
      ownerLabel(THEM, { userId: ME }, [{ userId: THEM, name: null, email: "" }]),
    ).toBe("A teammate");
  });
});
