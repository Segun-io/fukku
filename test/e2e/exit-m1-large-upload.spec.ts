import { rm, stat, writeFile } from "node:fs/promises";
import path from "node:path";
import { expect, type Page, test } from "@playwright/test";
import { loginAs, network, session, stack } from "./helpers";
import { Api, type Job } from "./helpers/api";
import { dataDir, fixturesDir } from "./helpers/env";
import {
  formatSize,
  freeBytes,
  GiB,
  largeFile,
  MiB,
  parseSize,
  sha256,
} from "./helpers/large-file";

// M1 exit criterion (plan §15, issue #15): a large upload survives a reload, a container
// restart, dropped connections and an expired session, through the real upload page, and
// arrives byte for byte. FUKKU_E2E_LARGE_SIZE sets the size: 5GiB in make e2e and CI (above
// 4 GiB, so a 32-bit offset would show), 60GiB in make e2e-large (by hand).
//
// Everything waits on observable state: the offsets the server answers, what the page shows.

const size = parseSize(process.env.FUKKU_E2E_LARGE_SIZE ?? "5GiB");
const name = "large-upload.mkv";
const video = path.join(fixturesDir, "media", "film.mkv");
// The data arrives in .incoming and is linked into the job: one copy on disk, plus room.
const diskNeeded = size + GiB;
// The slowest rate the test accepts on average, and the time for everything else.
const minRate = 8 * MiB;
const budgetMs = Math.ceil((size / minRate) * 1000) + 10 * 60_000;

/** What the page's tus client sent and what the server answered, in order. */
type Event =
  | { kind: "create"; status: number; step: string }
  | { kind: "patch"; offset: number; step: string }
  | { kind: "answer"; method: string; status: number; offset: number | null; step: string }
  | { kind: "failed"; method: string; offset: number; error: string; step: string };

class Tracker {
  events: Event[] = [];
  /** The highest offset the server has confirmed (a PATCH's 204 or a HEAD). */
  server = 0;
  step = "start";
  uploadPath: string | null = null;

  constructor(page: Page) {
    const isUpload = (url: string) => new URL(url).pathname.startsWith("/api/uploads/");
    page.on("request", (req) => {
      if (!isUpload(req.url())) return;
      if (req.method() === "PATCH") {
        this.events.push({
          kind: "patch",
          offset: Number(req.headers()["upload-offset"]),
          step: this.step,
        });
      }
    });
    page.on("response", (res) => {
      const req = res.request();
      if (!isUpload(req.url())) return;
      if (req.method() === "POST") {
        this.events.push({ kind: "create", status: res.status(), step: this.step });
        const loc = res.headers().location;
        if (res.status() === 201 && loc) this.uploadPath = new URL(loc, req.url()).pathname;
        return;
      }
      if (req.method() !== "PATCH" && req.method() !== "HEAD") return;
      const raw = res.headers()["upload-offset"];
      const offset = raw === undefined ? null : Number(raw);
      this.events.push({
        kind: "answer",
        method: req.method(),
        status: res.status(),
        offset,
        step: this.step,
      });
      if (offset !== null && (res.status() === 204 || res.status() === 200)) {
        this.server = Math.max(this.server, offset);
      }
    });
    page.on("requestfailed", (req) => {
      if (!isUpload(req.url()) || req.method() === "OPTIONS") return;
      this.events.push({
        kind: "failed",
        method: req.method(),
        offset: Number(req.headers()["upload-offset"] ?? -1),
        error: req.failure()?.errorText ?? "",
        step: this.step,
      });
    });
  }

  /** Start an interruption; the upload must still be running, or it would prove nothing. */
  begin(step: string) {
    expect(this.server, `the upload is still running when "${step}" begins`).toBeLessThan(size);
    this.step = step;
  }

  patches(step?: string) {
    return this.events.filter(
      (e): e is Extract<Event, { kind: "patch" }> =>
        e.kind === "patch" && (!step || e.step === step),
    );
  }

  /** PATCH and HEAD requests of a step that failed or got an error answer. */
  troubles(step?: string) {
    return this.events.filter(
      (e) =>
        (!step || e.step === step) &&
        (e.kind === "failed" || (e.kind === "answer" && e.status >= 400)),
    ).length;
  }

  /** Wait until the server has confirmed at least `fraction` of the file. */
  async reach(fraction: number, timeout = budgetMs) {
    const target = Math.floor(size * fraction);
    await expect
      .poll(() => this.server, {
        message: `the server has ${Math.round(fraction * 100)} % (${formatSize(target)})`,
        timeout,
        intervals: [250, 500, 1000],
      })
      .toBeGreaterThanOrEqual(target);
  }

  /**
   * Every PATCH starts exactly where the server's last answer before it said it is, or later
   * (the server keeps what a cut request delivered): no confirmed byte is ever sent again.
   */
  checkNothingSentTwice() {
    let known = 0;
    const late: string[] = [];
    for (const e of this.events) {
      if (e.kind === "answer" && e.offset !== null && (e.status === 200 || e.status === 204)) {
        known = Math.max(known, e.offset);
      }
      if (e.kind === "patch" && e.offset < known) {
        late.push(`PATCH at ${e.offset} after the server confirmed ${known} (${e.step})`);
      }
    }
    expect(late, "PATCH requests that sent confirmed bytes again").toEqual([]);
  }
}

/** The 16 MiB around a position, clipped to the file. */
function around(at: number): [number, number] {
  const start = Math.max(0, Math.min(at - 8 * MiB, size - 16 * MiB));
  return [start, Math.min(size, start + 16 * MiB)];
}

let file = "";
let jobId: string | null = null;
let tracker: Tracker | null = null;
const api = new Api("user1");

test.beforeAll(async () => {
  test.setTimeout(10 * 60_000);
  const free = await freeBytes(dataDir);
  if (free < diskNeeded) {
    throw new Error(
      `the library filesystem has ${formatSize(free)} free, the ${formatSize(size)} upload needs ${formatSize(diskNeeded)}`,
    );
  }
  const limits = (await api.json<{ upload: { maxFileSize: number } }>("GET", "/api/session", 200))
    .upload;
  if (limits.maxFileSize < size) {
    throw new Error(
      `the stack takes files up to ${formatSize(limits.maxFileSize)}: run this test with make e2e (or make e2e-large), which raises the limit for ${formatSize(size)}`,
    );
  }
  file = await largeFile(name, size, video);
});

test.afterAll(async () => {
  if (file) await rm(file, { force: true });
});

// biome-ignore lint/correctness/noEmptyPattern: Playwright needs a pattern before testInfo
test.afterEach(async ({}, testInfo) => {
  // What the page sent and heard, for the report of a failed run.
  if (tracker && testInfo.status !== testInfo.expectedStatus) {
    const out = testInfo.outputPath("upload-events.json");
    await writeFile(out, JSON.stringify(tracker.events, null, 1));
    await testInfo.attach("upload-events.json", { path: out, contentType: "application/json" });
  }
  tracker = null;
  await network.reset();
  await session.restore();
  // The job holds the whole file: cancel it, and the runner removes its directory.
  if (jobId) {
    const id = jobId;
    jobId = null;
    await api.cancel(id).catch(() => {});
    await expect
      .poll(
        () =>
          stat(path.join(dataDir, "intake", "jobs", id)).then(
            () => true,
            () => false,
          ),
        {
          message: `the cancelled job ${id} is removed`,
          timeout: 120_000,
        },
      )
      .toBe(false);
  }
});

test(`a ${formatSize(size)} upload survives a reload, a restart, cut connections and an expired session`, async ({
  page,
  context,
}) => {
  test.setTimeout(budgetMs);
  const started = Date.now();
  const track = new Tracker(page);
  tracker = track;
  // Leaving the page while it uploads asks first (beforeunload): the reload says yes.
  page.on("dialog", (d) => void d.accept());
  const picker = page.locator('input[type="file"]');
  const marks: Record<string, number> = {};
  const log = (what: string) =>
    console.log(
      `${((Date.now() - started) / 1000).toFixed(0).padStart(5)} s  ${formatSize(track.server).padStart(11)}  ${what}`,
    );

  await loginAs(page, "user1");
  await page.goto("/upload");

  await test.step("start uploading one large file", async () => {
    await picker.setInputFiles(file);
    await expect(page.getByText(name)).toBeVisible();
    const created = page.waitForResponse(
      (r) => new URL(r.url()).pathname === "/api/jobs" && r.request().method() === "POST",
    );
    await page.getByRole("button", { name: "Start upload" }).click();
    const res = await created;
    expect(res.status()).toBe(201);
    jobId = ((await res.json()) as Job).id;
    log(`job ${jobId}`);
    await track.reach(0.2);
  });

  await test.step("at 20 %: reload, choose the same file again, it continues from the server's offset", async () => {
    track.begin("reload");
    marks.reload = track.server;
    log("reload");
    await page.reload();
    await expect(page.getByText("Your upload was interrupted")).toBeVisible();
    // The old page is gone: everything it had confirmed is in, nothing new is sent.
    const before = track.server;
    const mark = track.events.length;
    // The first request after choosing the file again asks the server where it is.
    const head = page.waitForResponse(
      (r) => r.request().method() === "HEAD" && new URL(r.url()).pathname === track.uploadPath,
    );
    await picker.setInputFiles(file);
    const offset = Number((await head).headers()["upload-offset"]);
    expect(offset, "the server's offset after the reload").toBeGreaterThanOrEqual(before);
    const firstPatch = () => track.events.slice(mark).find((e) => e.kind === "patch");
    await expect.poll(firstPatch).toBeTruthy();
    const first = firstPatch();
    expect(first?.kind === "patch" && first.offset, "the first chunk after the reload").toBe(
      offset,
    );
    expect(offset).toBeGreaterThan(0);
    await expect(page.getByText(/^Continued from/)).toBeVisible();
    log(`continued from ${offset}`);
    await track.reach(0.4);
  });

  await test.step("at 40 %: fukku-web restarts, the page waits and continues by itself", async () => {
    track.begin("restart");
    marks.restart = track.server;
    log("restart fukku-web");
    await stack.restart("fukku-web");
    log("fukku-web restarted");
    await track.reach(0.6);
    expect(
      track.troubles("restart"),
      "requests that failed while fukku-web was down",
    ).toBeGreaterThan(0);
  });

  await test.step("at 60 %: the connection is cut three times in a row, it continues each time", async () => {
    track.begin("cut");
    marks.cut = track.server;
    for (let round = 1; round <= 3; round++) {
      const seen = track.troubles("cut");
      log(`cut the connection (${round} of 3)`);
      await network.cutUploads({ afterBytes: 256 * 1024, times: 1 });
      // The page sees the PATCH fail, or, when the browser sent it again by itself on a new
      // connection, the server's 409: it has the bytes the cut request delivered.
      await expect
        .poll(() => track.troubles("cut"), {
          message: `cut ${round}: the page saw the PATCH fail`,
          timeout: 60_000,
        })
        .toBeGreaterThan(seen);
      const at = track.server;
      await expect
        .poll(() => track.server, { message: `cut ${round}: it continues`, timeout: 120_000 })
        .toBeGreaterThan(at + 4 * MiB);
    }
    await network.reset();
    await track.reach(0.8);
  });

  await test.step('at 80 %: the session expires ("302"), the page asks to sign in again, then continues', async () => {
    track.begin("expired-302");
    marks.expired302 = track.server;
    log("session expired (302)");
    await session.expire("302");
    const banner = page.getByRole("alert").filter({ hasText: "Sign in again" });
    await expect(banner).toBeVisible({ timeout: 60_000 });
    const stalled = track.server;
    // Signing in happens in a new tab: the proxy's sign-in page, then back to this tab.
    const [signin] = await Promise.all([
      context.waitForEvent("page"),
      banner.getByRole("link", { name: "Sign in again" }).click(),
    ]);
    await signin.getByRole("button", { name: "sign in" }).click();
    await signin.waitForURL((u) => !u.pathname.startsWith("/__test/"));
    await signin.close();
    await page.bringToFront();
    await page.evaluate(() => window.dispatchEvent(new Event("focus")));
    await expect(banner).toBeHidden();
    await expect.poll(() => track.server, { timeout: 120_000 }).toBeGreaterThan(stalled);
    log("signed in again");
  });

  await test.step('then the session expires once more ("401"), and comes back', async () => {
    track.begin("expired-401");
    marks.expired401 = track.server;
    log("session expired (401)");
    await session.expire("401");
    const banner = page.getByRole("alert").filter({ hasText: "Sign in again" });
    await expect(banner).toBeVisible({ timeout: 60_000 });
    const stalled = track.server;
    await session.restore();
    await page.evaluate(() => window.dispatchEvent(new Event("focus")));
    await expect(banner).toBeHidden();
    await expect.poll(() => track.server, { timeout: 120_000 }).toBeGreaterThan(stalled);
    log("signed in again");
  });

  await test.step("it finishes, the job is submitted and reaches probed", async () => {
    track.step = "finish";
    await page.waitForURL(`**/jobs/${jobId}`, { timeout: budgetMs });
    log("uploaded and submitted");
    const job = await api.settled(jobId ?? "", 5 * 60_000);
    expect(job.state, JSON.stringify(job.error)).toBe("probed");
    expect(job.files.map((f) => [f.name, f.size, f.status])).toEqual([[name, size, "ok"]]);
    log("probed");
  });

  await test.step("the file in the job is the same, byte for byte", async () => {
    const stored = path.join(dataDir, "intake", "jobs", jobId ?? "", "files", name);
    expect((await stat(stored)).size).toBe(size);
    const ranges: [string, [number, number]][] = [
      ["first 16 MiB", [0, Math.min(size, 16 * MiB)]],
      ["last 16 MiB", [Math.max(0, size - 16 * MiB), size]],
      ...Object.entries(marks).map(([k, at]): [string, [number, number]] => [
        `around ${k} (${at})`,
        around(at),
      ]),
    ];
    for (const [what, [start, end]] of ranges) {
      expect(await sha256(stored, start, end), what).toBe(await sha256(file, start, end));
    }
    expect(await sha256(stored), "SHA-256 of the whole file").toBe(await sha256(file));
    log("byte for byte the same");
  });

  // What was sent: one upload, never created again, nothing confirmed sent twice.
  expect(
    track.events.filter((e) => e.kind === "create").map((e) => e.kind === "create" && e.status),
  ).toEqual([201]);
  track.checkNothingSentTwice();
  log(`${track.patches().length} PATCH requests, ${track.troubles()} failed or refused`);
});
