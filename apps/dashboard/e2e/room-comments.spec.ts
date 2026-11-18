import { test, expect } from "@playwright/test";

// The development preview uses the real workspace and local, deterministic data.
// Run with ROOMS_PREVIEW_E2E=1 against `next dev`; unavailable in production.
test.skip(process.env.ROOMS_PREVIEW_E2E !== "1", "Requires the development rooms preview");
test("clears posted room comments and replies", async ({ page }) => {
  await page.goto("/dev/rooms-preview?room=project-launch&site=site-launch");
  await page.getByRole("button", { name: "Canvas", exact: true }).click();
  const composer = page.getByRole("textbox", { name: "Comment on this page", exact: true });
  await expect(composer).toBeVisible();
  await composer.fill("Please update the introduction");
  await page.getByRole("button", { name: "Send comment", exact: true }).click();
  await expect(composer).toHaveValue("");
  await expect(page.getByText("Please update the introduction", { exact: true }).first()).toBeVisible();

  const reply = page.getByRole("textbox", { name: /^Reply to / }).first();
  await reply.fill("The new introduction looks good");
  await reply.locator("xpath=ancestor::form[1]").getByRole("button", { name: "Send reply", exact: true }).click();
  await expect(reply).toHaveValue("");
  await expect(page.getByText("The new introduction looks good", { exact: true })).toBeVisible();
});

test("keeps drafts with their page and opens paths only when submitted", async ({ page }) => {
  await page.goto("/dev/rooms-preview?room=project-launch&site=site-launch");
  await page.getByRole("button", { name: "Canvas", exact: true }).click();
  const composer = page.getByRole("textbox", { name: "Comment on this page" });
  const path = page.getByRole("textbox", { name: "Page path" });
  await composer.fill("Home page feedback");
  await path.fill("/about.html");
  await expect(composer).toHaveValue("Home page feedback");
  await path.press("Enter");
  await expect(composer).toHaveValue("");
  await path.fill("/");
  await page.getByRole("button", { name: "Go", exact: true }).click();
  await expect(composer).toHaveValue("Home page feedback");
  await composer.press("Control+Enter");
  await expect(composer).toHaveValue("");
  await expect(page.getByText("Home page feedback", { exact: true })).toBeVisible();
});

test("shows both proposal versions on a phone before acceptance", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/dev/proposal-preview");
  const before = page.getByTitle("Before preview");
  const after = page.getByTitle("After preview");
  await expect(before.contentFrame().getByText("The only ask is to say hello")).toBeVisible();
  await expect(after.contentFrame().getByRole("heading", { name: "A place to belong." })).toBeVisible();
  await expect(after.contentFrame().getByText("The only ask is to say hello")).toHaveCount(0);
  const first = (await before.boundingBox())!;
  const second = (await after.boundingBox())!;
  expect(second.y).toBeGreaterThanOrEqual(first.y + first.height);
  expect(second.x + second.width).toBeLessThanOrEqual(390);
  await expect(page.getByRole("button", { name: "Accept change" })).toBeEnabled();
});

test("clears A's saved draft when the comment response arrives in room B", async ({ page }) => {
  const roomA = "project-launch";
  const roomB = "project-brand";
  const feedback = "Publish the revised introduction";
  const otherDraft = "Keep this brand review draft";
  const thread = {
    ID: "saved-thread", RoomID: roomA, SiteID: "site-launch", VersionID: "version-3",
    ContentHash: "hash-3", Page: "/", AnchorKind: "page", Anchor: {}, Status: "open",
    Replies: [{ ID: "saved-reply", AuthorID: "demo-owner", Body: feedback, CreatedAt: "" }],
  };
  let saves = 0;
  let persisted = false;
  let finishSave!: () => void;
  const pendingSave = new Promise<void>((resolve) => { finishSave = resolve; });

  await page.route("**/api/rooms/detail?*", (route) => {
    const id = new URL(route.request().url()).searchParams.get("roomId");
    const isA = id === roomA;
    return route.fulfill({ json: { ok: true, detail: {
      room: { ID: id, Name: isA ? "Studio launch" : "Brand refresh", Role: "owner", Unread: 0 },
      members: [], sites: [{ siteId: isA ? "site-launch" : "site-guide", createdBy: "demo-owner" }],
    } } });
  });
  await page.route("**/api/rooms/discussion?*", (route) => {
    const id = new URL(route.request().url()).searchParams.get("roomId");
    return route.fulfill({ json: { ok: true, members: [], candidates: [],
      threads: persisted && id === roomA ? [thread] : [], threadCursor: null, candidateCursor: null,
    } });
  });
  await page.route("**/api/rooms/revisions?*", (route) => route.fulfill({ json: { ok: true, versions: [] } }));
  await page.route("**/api/rooms/preview?*", (route) => route.fulfill({ json: { ok: false, message: "Page comments available." } }));
  await page.route("**/dev/rooms-preview?*", async (route) => {
    if (!route.request().headers()["next-action"]) return route.continue();
    const [input] = route.request().postDataJSON();
    let result;
    if (input?.body === feedback && input.roomId === roomA) {
      saves++;
      await pendingSave;
      persisted = true;
      result = { ok: true, thread };
    } else {
      result = { ok: true, items: [], nextCursor: null, unread: 0 };
    }
    // A Next server action response with a return value and no route updates.
    await route.fulfill({ contentType: "text/x-component", body: "0:" + JSON.stringify({ a: result, f: [] }) + "\n" });
  });

  const openRoom = async (room: string, site: string) => {
    await page.getByRole("button", { name: "Browse room contents" }).click();
    await page.getByRole("button", { name: "Open room " + room }).click();
    await page.getByRole("button", { name: "Open site " + site }).click();
  };
  const composer = page.getByRole("textbox", { name: "Comment on this page" });
  const send = page.getByRole("button", { name: "Send comment", exact: true });
  await page.goto("/dev/rooms-preview?session=1&room=" + roomB + "&site=site-guide");
  await composer.fill(otherDraft);
  await openRoom("Studio launch", "studio-launch");
  await composer.fill(feedback);
  await send.click();
  try {
    await expect.poll(() => saves).toBe(1);
    await openRoom("Brand refresh", "brand-guidelines");
    await expect(composer).toHaveValue(otherDraft);
  } finally {
    finishSave();
  }
  await expect(send).toBeEnabled();
  await expect(composer).toHaveValue(otherDraft);
  await expect(page.getByText("Page comment stored on the original version.")).toHaveCount(0);
  await expect(page.getByText(feedback, { exact: true })).toHaveCount(0);

  await openRoom("Studio launch", "studio-launch");
  await expect(page.getByRole("region", { name: "Comments", exact: true }).getByText(feedback, { exact: true })).toBeVisible();
  await expect(composer).toHaveValue("");
  await expect(send).toBeDisabled();
  expect(saves).toBe(1);
});
