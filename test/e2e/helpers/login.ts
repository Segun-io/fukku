import type { Page } from "@playwright/test";
import type { TestUser } from "./env";

/**
 * Sign the page's browser context in as one of the proxy's three users. The proxy answers with
 * the cookie that picks the user; the page itself is not navigated.
 */
export async function loginAs(page: Page, user: TestUser): Promise<void> {
  const res = await page.context().request.get(`/__test/login?user=${user}`);
  if (!res.ok()) throw new Error(`login as ${user}: ${res.status()} ${await res.text()}`);
}
