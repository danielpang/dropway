import { test, expect } from "@playwright/test";

// The development preview uses the real workspace and local, deterministic data.
// Run with ROOMS_PREVIEW_E2E=1 against `next dev`; unavailable in production.
test.skip(process.env.ROOMS_PREVIEW_E2E !== "1", "Requires the development rooms preview");
test("clears posted room comments and replies", async ({ page }) => {
  await page.goto("/dev/rooms-preview");
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
