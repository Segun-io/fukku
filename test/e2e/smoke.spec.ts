import type { Page } from "@playwright/test";
import { loginAs, network, session, stack } from "./helpers";
import { expect, test } from "./test";

test.afterEach(async () => {
  await network.reset();
  await session.restore();
});

// The UI does not print the version yet, so the page's own session request is where to read
// it: it answers for user1 with the version that /healthz reports.
async function loadsAsUser1(page: Page) {
  const [res] = await Promise.all([
    page.waitForResponse((r) => new URL(r.url()).pathname === "/api/session"),
    page.goto("/"),
  ]);
  expect(res.status()).toBe(200);
  const info = await res.json();
  expect(info.user.name).toBe("user1");
  expect(info.version).toBeTruthy();
  const health = await (await page.request.get("/healthz")).json();
  expect(info.version).toBe(health.version);

  await expect(page.getByRole("link", { name: "Fukku" })).toBeVisible();
  await expect(page.getByRole("main")).toBeVisible();
  await expect(page).toHaveURL(/\/jobs$/);
}

test("the home page loads for user1, and again after fukku-web restarts", async ({ page }) => {
  await loginAs(page, "user1");
  await loadsAsUser1(page);

  await stack.restart("fukku-web");
  await stack.waitReady();
  await loadsAsUser1(page);
});
