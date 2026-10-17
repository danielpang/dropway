// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Page-level tests for app/(app)/dashboard/page.tsx — the wiring AROUND the
// pure listing rule that test/sites-visibility.test.ts pins. The page is an
// async RSC and there is no DOM runner, so these tests await the component and
// walk the returned element tree without rendering: host elements ("p", "ul")
// are matched by tag + className, inner components (SiteRow, FilterChip,
// EmptyState) by function name, and nothing is expanded — SiteRow's slugs and
// FilterChip's counts are read straight off props.
//
// What only this level can catch:
//   - the page actually APPLIES isSiteVisibleTo before deriving rows and both
//     chip counts (a teammate's private site must be gone from all three);
//   - the degraded fail-open: when the org can't resolve, everything is shown
//     (else a transient auth hiccup would hide the viewer's OWN private sites
//     and read as data loss) and the footnote/filters are dropped;
//   - the footnote copy is the new role-blind sentence and is centred
//     (`text-center`) — the visible half of this feature.

import { describe, expect, it, vi } from "vitest";

const { listSitesMock, loadActiveOrgMock } = vi.hoisted(() => ({
  listSitesMock: vi.fn<() => Promise<unknown[]>>(),
  loadActiveOrgMock: vi.fn<() => Promise<unknown>>(),
}));

vi.mock("@/lib/api", async (importOriginal) => {
  const mod = await importOriginal<typeof import("@/lib/api")>();
  return { ...mod, api: { ...mod.api, listSites: listSitesMock } };
});
vi.mock("@/lib/billing-server", () => ({
  loadOrgBillingState: vi.fn(async () => ({ readOnly: false })),
}));
vi.mock("@/lib/org", () => ({
  loadActiveOrg: loadActiveOrgMock,
  canManage: (role: string) => role === "owner" || role === "admin",
}));

import DashboardPage from "@/app/(app)/dashboard/page";
import { ApiError } from "@/lib/api";

const ME = "user-me";
const THEM = "user-them";

/** An ActiveOrg where the viewer is an org ADMIN — the role this feature demoted. */
const adminOrg = {
  organizationId: "org-1",
  name: "Acme",
  slug: "acme",
  myUserId: ME,
  myRole: "admin",
  members: [
    { id: "m1", userId: ME, email: "me@acme.test", name: "Me", role: "admin", createdAt: null },
    { id: "m2", userId: THEM, email: "ana@acme.test", name: "Ana Ruiz", role: "member", createdAt: null },
  ],
  invitations: [],
};

const SITES = [
  { id: "s-shared", slug: "shared", owner_id: THEM, feed_visible: true },
  { id: "s-their-private", slug: "their-private", owner_id: THEM, feed_visible: false },
  { id: "s-my-private", slug: "my-private", owner_id: ME, feed_visible: false },
  // Absent feed_visible must read as shared; absent owner_id keeps it out of Mine.
  { id: "s-unowned", slug: "unowned" },
];

type El = { type: unknown; props: Record<string, unknown> };

/** Depth-first over a React element tree, descending through props.children only. */
function* walk(node: unknown): Generator<El> {
  if (node == null || typeof node !== "object") return;
  if (Array.isArray(node)) {
    for (const child of node) yield* walk(child);
    return;
  }
  const el = node as El;
  if (el.props && typeof el.props === "object") {
    yield el;
    yield* walk(el.props.children);
  }
}

function findAll(tree: unknown, pred: (el: El) => boolean): El[] {
  return [...walk(tree)].filter(pred);
}

function isComponent(el: El, name: string): boolean {
  return typeof el.type === "function" && el.type.name === name;
}

/** The slugs of the sites the page put on screen, in render order. */
function rowSlugs(tree: unknown): string[] {
  return findAll(tree, (el) => isComponent(el, "SiteRow")).map(
    (el) => (el.props.site as { slug: string }).slug,
  );
}

/** [All count, Mine count] as passed to the filter chips, or null when hidden. */
function chipCounts(tree: unknown): number[] | null {
  const chips = findAll(tree, (el) => isComponent(el, "FilterChip"));
  return chips.length > 0 ? chips.map((el) => el.props.count as number) : null;
}

/** The footnote <p> under the grid, or null when the page dropped it. */
function footnote(tree: unknown): El | null {
  return (
    findAll(
      tree,
      (el) =>
        el.type === "p" &&
        typeof el.props.children === "string" &&
        (el.props.children as string).includes("marked private"),
    )[0] ?? null
  );
}

async function renderPage(searchParams: { owner?: string } = {}) {
  return DashboardPage({ searchParams: Promise.resolve(searchParams) });
}

describe("DashboardPage (All list)", () => {
  it("hides a teammate's private site from an org admin, in rows AND both chip counts", async () => {
    listSitesMock.mockResolvedValue(SITES);
    loadActiveOrgMock.mockResolvedValue(adminOrg);

    const tree = await renderPage();
    // The teammate's private site is gone; the viewer's own private site stays
    // (the trap door: /dashboard is the only unconditional route to it).
    expect(rowSlugs(tree)).toEqual(["shared", "my-private", "unowned"]);
    // All counts the filtered set, Mine only owned sites (unowned is not Mine).
    expect(chipCounts(tree)).toEqual([3, 1]);
  });

  it("renders the role-blind footnote, centred", async () => {
    listSitesMock.mockResolvedValue(SITES);
    loadActiveOrgMock.mockResolvedValue(adminOrg);

    const tree = await renderPage();
    const note = footnote(tree);
    expect(note).not.toBeNull();
    expect(note!.props.children).toBe(
      "Sites marked private are only shown to their owner.",
    );
    expect(note!.props.className).toContain("text-center");
    // The pre-#142-reversal admin copy must not survive anywhere in the tree.
    expect(JSON.stringify(tree)).not.toContain("including ones marked private");
  });

  it("shows only the viewer's own sites under ?owner=me", async () => {
    listSitesMock.mockResolvedValue(SITES);
    loadActiveOrgMock.mockResolvedValue(adminOrg);

    const tree = await renderPage({ owner: "me" });
    expect(rowSlugs(tree)).toEqual(["my-private"]);
  });

  it("shows an admin the empty state when the org holds only teammates' private sites", async () => {
    // Accepted consequence of reversing #142: same state a plain member sees.
    listSitesMock.mockResolvedValue([
      { id: "s-their-private", slug: "their-private", owner_id: THEM, feed_visible: false },
    ]);
    loadActiveOrgMock.mockResolvedValue(adminOrg);

    const tree = await renderPage();
    expect(rowSlugs(tree)).toEqual([]);
    expect(findAll(tree, (el) => isComponent(el, "EmptyState"))).toHaveLength(1);
    expect(chipCounts(tree)).toBeNull();
    expect(footnote(tree)).toBeNull();
  });
});

describe("DashboardPage (degraded: org unresolved)", () => {
  it("fails open — every site is listed, so the viewer's own private sites can't vanish", async () => {
    listSitesMock.mockResolvedValue(SITES);
    loadActiveOrgMock.mockRejectedValue(new Error("auth hiccup"));

    const tree = await renderPage();
    expect(rowSlugs(tree)).toEqual(["shared", "their-private", "my-private", "unowned"]);
    // The footnote's claim would be false for this unfiltered list, and the
    // filter chips would compare against an unknown viewer — both are dropped.
    expect(footnote(tree)).toBeNull();
    expect(chipCounts(tree)).toBeNull();
  });

  it("ignores ?owner=me while degraded (no viewer identity to match against)", async () => {
    listSitesMock.mockResolvedValue(SITES);
    loadActiveOrgMock.mockRejectedValue(new Error("auth hiccup"));

    const tree = await renderPage({ owner: "me" });
    expect(rowSlugs(tree)).toEqual(["shared", "their-private", "my-private", "unowned"]);
  });
});

describe("DashboardPage (API failure)", () => {
  it("shows the inline error card and neither footnote nor chips", async () => {
    listSitesMock.mockRejectedValue(new ApiError(503, "API 503 on /v1/sites", null));
    loadActiveOrgMock.mockResolvedValue(adminOrg);

    const tree = await renderPage();
    expect(rowSlugs(tree)).toEqual([]);
    expect(footnote(tree)).toBeNull();
    expect(chipCounts(tree)).toBeNull();
    expect(JSON.stringify(tree)).toContain("The API returned 503.");
  });
});
