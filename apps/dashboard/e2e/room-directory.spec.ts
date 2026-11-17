import { test, expect } from "@playwright/test";

test.skip(
  process.env.ROOMS_PREVIEW_E2E !== "1",
  "Requires the development rooms preview",
);

test("browse rooms and resources, switch views, and navigate back", async ({
  page,
}) => {
  await page.goto("/dev/rooms-preview");
  await expect(
    page.getByRole("heading", { name: "Rooms", exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Open room Studio launch" }),
  ).toBeVisible();
  await expect(page.locator("iframe")).toHaveCount(0);
  await page.getByRole("button", { name: "Open room Studio launch" }).click();
  await expect(page).toHaveURL(/room=project-launch$/);
  await expect(
    page.getByRole("button", { name: "Open image Summer campaign.png" }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Open site studio-launch" }),
  ).toBeVisible();
  await expect(page.locator("iframe")).toHaveCount(0);
  await page.getByRole("button", { name: "Cards view" }).click();
  await expect(
    page.getByRole("button", { name: "Cards view" }),
  ).toHaveAttribute("aria-pressed", "true");
  await page
    .getByRole("textbox", { name: "Search room contents" })
    .fill("summer");
  await expect(
    page.getByRole("button", { name: "Open site studio-launch" }),
  ).toHaveCount(0);
  await page
    .getByRole("button", { name: "Open image Summer campaign.png" })
    .click();
  await expect(
    page.getByRole("combobox", { name: "Site or image", exact: true }),
  ).toHaveText("Summer campaign.png");
  await expect(page.locator("iframe")).toBeVisible();
  await page.goBack();
  await expect(
    page.getByRole("button", { name: "Open site studio-launch" }),
  ).toBeVisible();
  await page.getByRole("button", { name: "Open room Brand refresh" }).click();
  await expect(
    page.getByRole("button", { name: "Open site brand-guidelines" }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Open image Summer campaign.png" }),
  ).toHaveCount(0);
  await page.getByRole("button", { name: "All rooms", exact: true }).click();
  await expect(page).toHaveURL(/\/dev\/rooms-preview$/);
  await expect(
    page.getByRole("button", { name: "Open site brand-guidelines" }),
  ).toHaveCount(0);
});

test("mobile directory drills into a room and returns to the room list", async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/dev/rooms-preview");
  await page.getByRole("button", { name: "Open room Studio launch" }).click();
  await expect(
    page.getByRole("button", { name: "Open site studio-launch" }),
  ).toBeVisible();
  await expect(page.getByRole("region", { name: "Room list" })).toBeHidden();
  await page.getByRole("button", { name: "All rooms", exact: true }).click();
  await expect(
    page.getByRole("button", { name: "Open room Brand refresh" }),
  ).toBeVisible();
  expect(
    await page.evaluate(() => document.documentElement.scrollWidth),
  ).toBeLessThanOrEqual(390);
});

test("clearing room navigation returns to the directory even while creating a room", async ({ page }) => {
  await page.goto("/dev/rooms-preview");
  await page.getByRole("button", { name: "New room", exact: true }).click();
  await expect(page.getByRole("region", { name: "Create project", exact: true })).toBeVisible();
  await expect(page).toHaveURL(/view=create/);
  await page.getByRole("button", { name: "Cancel", exact: true }).click();
  await expect(page.getByRole("button", { name: "Open room Studio launch" })).toBeVisible();
  await page.getByRole("button", { name: "Open room Studio launch" }).click();
  await page.getByRole("button", { name: "Open site studio-launch" }).click();
  // The app header links to /rooms with no query parameters. The development
  // fixture lives outside that auth-protected route; exercise the same reset.
  await page.evaluate(() => window.history.pushState(null, "", "/dev/rooms-preview"));
  await expect(page.getByRole("heading", { name: "Rooms", exact: true })).toBeVisible();
  await expect(page.locator("iframe")).toHaveCount(0);
});

test("resolving a comment hides its open card and keeps it in resolved comments", async ({
  page,
}) => {
  await page.goto("/dev/rooms-preview?room=project-launch&site=site-launch");
  await page.getByRole("button", { name: "Canvas", exact: true }).click();
  const card = page.locator("#thread-thread-copy");
  await expect(card).toBeVisible();
  await card
    .getByRole("button", { name: "Resolve comment", exact: true })
    .click();
  await expect(card).toHaveCount(0);
  await expect(
    page.getByRole("button", { name: "Open comments", exact: true }),
  ).toHaveAttribute("aria-pressed", "true");
  await page
    .getByRole("button", { name: "Resolved comments", exact: true })
    .click();
  await expect(card).toBeVisible();
  await expect(
    card.getByRole("button", { name: "Resolve comment", exact: true }),
  ).toHaveCount(0);
  await expect(page.getByText("Version sign-off", { exact: true })).toHaveCount(
    0,
  );
  await expect(
    page.getByText("Site-wide comment history", { exact: true }),
  ).toHaveCount(0);
  await page
    .getByRole("button", { name: "Open comments", exact: true })
    .click();
  await expect(card).toHaveCount(0);
});

test("the real session shows a preview while discussion requests are still pending", async ({
  page,
}) => {
  let finishDiscussion!: () => void;
  const discussionReady = new Promise<void>((resolve) => {
    finishDiscussion = resolve;
  });
  let discussionRequested = false;
  const room = {
    ID: "project-launch",
    OrgID: "demo-org",
    Name: "Studio launch",
    OwnerUserID: "demo-owner",
    Role: "owner",
    CreatedAt: "",
    UpdatedAt: "",
    Unread: 0,
  };
  await page.route("**/api/rooms/detail?*", (route) =>
    route.fulfill({
      json: {
        ok: true,
        detail: {
          room,
          members: [],
          sites: [{ siteId: "site-launch", createdBy: "demo-owner" }],
        },
      },
    }),
  );
  await page.route("**/api/rooms/discussion?*", async (route) => {
    discussionRequested = true;
    await discussionReady;
    await route.fulfill({
      json: {
        ok: true,
        members: [],
        threads: [],
        candidates: [],
        threadCursor: null,
        candidateCursor: null,
      },
    });
  });
  await page.route("**/api/rooms/revisions?*", (route) =>
    route.fulfill({
      json: { ok: true, versions: [{ id: "version-3", number: 3 }] },
    }),
  );
  await page.route("**/api/rooms/preview?*", (route) =>
    route.fulfill({
      json: {
        ok: true,
        snapshot: {
          snapshotId: "ready",
          siteId: "site-launch",
          versionId: "version-3",
          page: "/",
          contentHash: "hash-3",
          renderer: "static-v1",
          omittedAssets: 0,
          html: '<html data-review-snapshot="ready"><body><main id="review-content"><h1>Preview is ready</h1></main></body></html>',
        },
      },
    }),
  );
  try {
    await page.goto(
      "/dev/rooms-preview?session=1&room=project-launch&site=site-launch",
    );
    await expect(
      page
        .frameLocator('iframe[title="Immutable static review"]')
        .getByRole("heading", { name: "Preview is ready" }),
    ).toBeVisible();
    expect(discussionRequested).toBe(true);
    await expect(
      page.getByText("Loading comments…", { exact: true }),
    ).toBeVisible();
  } finally {
    finishDiscussion();
  }
  await expect(
    page.getByText("Loading comments…", { exact: true }),
  ).toHaveCount(0);
  await expect(
    page.getByText("No open comments.", { exact: false }),
  ).toBeVisible();
});

test("resolved permalinks reveal their historical version and resolving an image pin closes its card", async ({ page }) => {
  await page.goto("/dev/rooms-preview?room=project-launch#thread-thread-old");
  await expect(page.getByRole("button", { name: "Resolved comments", exact: true })).toHaveAttribute("aria-pressed", "true");
  await expect(page.locator("#thread-thread-old")).toBeVisible();
  await expect(page.getByRole("combobox", { name: "Viewing version", exact: true })).toHaveText("Revision 1");

  await page.goto("/dev/rooms-preview?room=project-launch&site=image-campaign");
  await page.frameLocator('iframe[title="Immutable static review"]').getByRole("button", { name: /^Open comment 1:/ }).click();
  const popover = page.getByRole("dialog", { name: "Comment thread" });
  await expect(popover).toBeVisible();
  await popover.getByRole("button", { name: "Resolve comment", exact: true }).click();
  await expect(popover).toHaveCount(0);
  await expect(page.locator("#thread-thread-image")).toHaveCount(0);
});
