import { test, expect } from "@playwright/test";

test.use({ hasTouch: true });

test.skip(process.env.ROOMS_PREVIEW_E2E !== "1", "Requires the development rooms preview");

test("custom workspace selectors support pointer and keyboard selection", async ({ page }) => {
  await page.goto("/dev/rooms-preview?room=project-launch&site=site-launch");
  await page.getByRole("button", { name: "Canvas", exact: true }).click();
  const site = page.getByRole("combobox", { name: "Site or image", exact: true });
  await site.click();
  await expect(page.getByRole("listbox")).toBeVisible();
  await page.getByRole("option", { name: "brand-guidelines", exact: true }).click();
  await expect(site).toHaveText("brand-guidelines");
  await expect(page.getByRole("listbox")).toHaveCount(0);
  await site.press("s");
  await site.press("Enter");
  await expect(site).toHaveText("Summer campaign.png");
  await site.click();
  await site.press("Home");
  await site.press("Enter");
  await expect(site).toHaveText("studio-launch");
  const revision = page.getByRole("combobox", { name: "Viewing version", exact: true });
  await revision.press("ArrowDown");
  await revision.press("End");
  await revision.press("Enter");
  await expect(revision).toHaveText("Revision 1");
  const filter = page.getByRole("combobox", { name: "Comment version filter", exact: true });
  await filter.click();
  await page.getByRole("option", { name: "Viewing version", exact: true }).click();
  await expect(filter).toHaveText("Viewing version");
  await filter.click();
  await filter.press("Escape");
  await expect(filter).toBeFocused();
  await expect(page.getByRole("listbox")).toHaveCount(0);
});

test("dropdowns remain inside a mobile viewport and support touch selection", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/dev/rooms-preview?room=project-launch&site=site-launch");
  const site = page.getByRole("combobox", { name: "Site or image", exact: true });
  await site.tap();
  const menu = page.getByRole("listbox");
  const bounds = await menu.boundingBox();
  expect(bounds).not.toBeNull();
  expect(bounds!.x).toBeGreaterThanOrEqual(0);
  expect(bounds!.x + bounds!.width).toBeLessThanOrEqual(390);
  await page.getByRole("option", { name: "Summer campaign.png", exact: true }).tap();
  await expect(site).toHaveText("Summer campaign.png");
});
