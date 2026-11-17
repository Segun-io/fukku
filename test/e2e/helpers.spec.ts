import { stat } from "node:fs/promises";
import { files, loginAs, network, session, stack, type TestUser } from "./helpers";
import { radarr } from "./helpers/apps";
import { baseURL } from "./helpers/env";
import { expect, test } from "./test";

// Self-test of the helpers: each one is used here, so a broken helper fails under its own
// name before a scenario trips over it. Scenarios live in their own *.spec.ts files.

test.afterEach(async () => {
  await network.reset();
  await session.restore();
});

test.describe("loginAs", () => {
  const expected: [TestUser, boolean][] = [
    ["user1", false],
    ["user2", false],
    ["admin1", true],
  ];
  for (const [user, admin] of expected) {
    test(user, async ({ page }) => {
      await loginAs(page, user);
      const res = await page.request.get("/api/session");
      expect(res.status()).toBe(200);
      const info = await res.json();
      expect(info.user).toMatchObject({ name: user, email: `${user}@example.com`, admin });
    });
  }
});

test("session.expire answers 401 or redirects to sign-in, and restore brings it back", async ({
  page,
}) => {
  await loginAs(page, "user1");

  await session.expire("401");
  expect((await page.request.get("/api/session")).status()).toBe(401);

  await session.expire("302");
  const redirected = await page.request.get("/api/session", { maxRedirects: 0 });
  expect(redirected.status()).toBe(302);
  expect(redirected.headers().location).toContain("/__test/signin");

  await session.restore();
  expect((await page.request.get("/api/session")).status()).toBe(200);
});

test("network.cutUploads drops the next upload requests, and reset clears it", async ({ page }) => {
  await loginAs(page, "user1");
  const patch = () =>
    page.request.patch("/api/uploads/does-not-exist", {
      headers: { "Content-Type": "application/offset+octet-stream" },
      data: Buffer.alloc(64 * 1024),
      timeout: 10_000,
    });

  await network.cutUploads({ afterBytes: 1024, times: 2 });
  await expect(patch()).rejects.toThrow();
  await expect(patch()).rejects.toThrow();
  // The third is not cut: whatever Fukku answers for an unknown upload, the connection holds.
  expect((await patch()).status()).toBeGreaterThanOrEqual(400);

  await network.cutUploads({ afterBytes: 1024, times: 5 });
  await network.reset();
  expect((await patch()).status()).toBeGreaterThanOrEqual(400);
});

test("files.sparse makes a large file that takes no disk", async () => {
  const size = 3 * 1024 ** 3;
  const file = await files.sparse("helpers-self-test.bin", size, Buffer.from("head"));
  const info = await stat(file);
  expect(info.size).toBe(size);
  expect(info.blocks * 512).toBeLessThan(size / 100);
  await expect(files.sparse("../escape", 1)).rejects.toThrow();
});

test("stack.restart restarts a container and waitReady waits for it", async () => {
  await stack.restart("fukku-worker");
  await stack.waitReady();
  await stack.restart("fukku-web");
  await stack.waitReady();
});

test("stack.kill kills fukku-web with SIGKILL; start brings it back, and logs reads it", async () => {
  const before = Date.now();
  const { at, start } = await stack.kill("fukku-web");
  expect(at).toBeGreaterThanOrEqual(before);
  const down = await fetch(`${baseURL}/healthz`, { signal: AbortSignal.timeout(3000) }).catch(
    () => null,
  );
  expect(down?.ok ?? false, "/healthz while fukku-web is down").toBe(false);
  await start();
  await stack.waitReady();
  // A new process logs its start again.
  expect(await stack.logs("fukku-web", at)).toContain("runner_started");
});

test("stack.freezeApp makes the test Radarr answer nothing until it is thawed", async () => {
  const thaw = await stack.freezeApp("radarr");
  try {
    await expect(radarr.ping()).rejects.toThrow();
  } finally {
    await thaw();
  }
  await expect(radarr.ping()).resolves.toBeTruthy();
});
