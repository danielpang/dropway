import { expect, test } from "@playwright/test";

test("invitation survives a new-tab authentication callback without putting its token in URLs or JS cookies", async ({
  context,
  page,
}) => {
  const token = "A".repeat(43);
  const requests: string[] = [];
  context.on("request", (request) => requests.push(request.url()));
  await context.route(/https:\/\/[^/]*posthog\.com\//, (route) =>
    route.abort(),
  );
  await page.goto(`/shared-projects/accept#invite=${token}`);
  await expect(page).toHaveURL(/\/shared-projects\/accept$/);
  await expect
    .poll(
      async () =>
        (await context.cookies()).find(
          (cookie) => cookie.name === "dropway-project-invitation",
        )?.value,
    )
    .toBe(token);
  const cookie = (await context.cookies()).find(
    (item) => item.name === "dropway-project-invitation",
  )!;
  expect(cookie.httpOnly).toBe(true);
  expect(cookie.sameSite).toBe("Lax");
  expect(cookie.path).toBe("/shared-projects");
  expect(cookie.expires * 1000 - Date.now()).toBeLessThanOrEqual(1800000);
  expect(await page.evaluate(() => document.cookie)).not.toContain(token);
  const callback = await context.newPage();
  await callback.goto("/shared-projects/accept");
  await expect(
    callback.getByRole("heading", { name: "Accept a project invitation" }),
  ).toBeVisible();
  await expect(
    callback.getByText("Open the original invitation email in this browser.", {
      exact: false,
    }),
  ).toHaveCount(0);
  expect(requests.some((url) => url.includes(token))).toBe(false);
  expect(requests.some((url) => /posthog\.com/.test(url))).toBe(false);
  expect(
    requests.some((url) => url.includes("/v1/shared-projects/accept")),
  ).toBe(false);
});
