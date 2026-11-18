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
