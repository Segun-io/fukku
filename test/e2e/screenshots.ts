import { mkdir, readFile } from "node:fs/promises";
import path from "node:path";
import type { Page } from "@playwright/test";
import { loginAs } from "./helpers";
import { Api, type Job } from "./helpers/api";
import { jellyfin, radarr, sonarr } from "./helpers/apps";
import { fixturesDir } from "./helpers/env";
import { needMetadata, outageOr } from "./helpers/online";
import { expect, test } from "./test";

// The README's screenshots (issue #162), taken from the dev stack with the generated media of
// `make e2e-fixtures` and the test users: `make screenshots` runs this file with
// screenshots.config.ts and copies the PNGs to docs/images. The titles are the M2 exit tests'
// free ones (Big Buck Bunny, Pioneer One); each test removes what it added from Radarr, Sonarr
// and Jellyfin, before and after, as exit-m2.spec.ts does.

const m2 = path.join(fixturesDir, "m2");
const out = path.join(import.meta.dirname, "screenshots");

interface Manifest {
  films: { tmdbId: number; title: string; year: number; file: string; subtitle?: string }[];
  season: { tvdbId: number; title: string; file: string };
}

let manifest: Manifest;
const api = new Api("user1");
const started: string[] = [];

function bunny() {
  const f = manifest.films.find((x) => x.title === "Big Buck Bunny");
  if (!f?.subtitle) throw new Error("no fixture for Big Buck Bunny: make e2e-fixtures");
  return f;
}

async function removeFilm() {
  const f = bunny();
  const folders = await radarr.removeByTmdb(f.tmdbId);
  for (const folder of new Set([...folders, `/data/media/movies/${f.title} (${f.year})`])) {
    await jellyfin.forget(folder);
  }
}

async function removeShow() {
  const s = manifest.season;
  const folders = await sonarr.removeByTvdb(s.tvdbId);
  for (const folder of new Set([...folders, `/data/media/tv/${s.title}`])) {
    await jellyfin.forget(folder);
  }
}

/** The page at the configuration's viewport, or all of it; no caret, no animation. */
async function shoot(page: Page, name: string, fullPage = false) {
  await page.evaluate(() => document.fonts.ready);
  await page.screenshot({
    path: path.join(out, name),
    fullPage,
    animations: "disabled",
    caret: "hide",
  });
}

test.beforeAll(async () => {
  manifest = JSON.parse(await readFile(path.join(m2, "manifest.json"), "utf8")) as Manifest;
  await mkdir(out, { recursive: true });
});

test.afterEach(async () => {
  for (const id of started.splice(0)) {
    const job = await api.job(id).catch(() => null);
    if (job && !["done", "failed", "cancelled"].includes(job.state)) {
      await api.cancel(id).catch(() => {});
    }
  }
});

test.afterAll(async () => {
  await removeFilm();
  await removeShow();
});

test("a film: the upload, the confirmation and the finished job", async ({ page }) => {
  const f = bunny();
  await needMetadata("radarr", f.tmdbId);
  await removeFilm();
  await loginAs(page, "user1");

  // 1. The upload page with the film still on its way: the browser's upload is slowed down
  // until the picture is taken.
  await page.goto("/upload");
  await page
    .locator('input[type="file"]')
    .setInputFiles([path.join(m2, f.file), path.join(m2, f.subtitle ?? "")]);
  const cdp = await page.context().newCDPSession(page);
  await cdp.send("Network.enable");
  await cdp.send("Network.emulateNetworkConditions", {
    offline: false,
    latency: 0,
    downloadThroughput: -1,
    uploadThroughput: 256 * 1024,
  });
  const created = page.waitForResponse(
    (r) => new URL(r.url()).pathname === "/api/jobs" && r.request().method() === "POST",
  );
  await page.getByRole("button", { name: "Start upload" }).click();
  const id = ((await (await created).json()) as Job).id;
  started.push(id);
  // Wait until the film is between a quarter and three quarters on its way.
  await page.waitForFunction(
    () =>
      [...document.querySelectorAll('[role="progressbar"]')].some((bar) => {
        const v = Number(bar.getAttribute("aria-valuenow"));
        return v >= 25 && v <= 75;
      }),
    undefined,
    { timeout: 60_000, polling: 100 },
  );
  await shoot(page, "upload.png");
  await cdp.send("Network.emulateNetworkConditions", {
    offline: false,
    latency: 0,
    downloadThroughput: -1,
    uploadThroughput: -1,
  });
  await expect(page).toHaveURL(new RegExp(`/jobs/${id}$`), { timeout: 180_000 });

  // 2. The title, then what Radarr made of the file, then the confirmation.
  await page.getByRole("link", { name: "Choose the film or show" }).click({ timeout: 180_000 });
  await page.getByRole("radio", { name: "A film", exact: true }).check();
  await page.getByRole("textbox", { name: "Title" }).fill(f.title);
  const lookup = page.waitForResponse((r) => new URL(r.url()).pathname.startsWith("/api/lookup/"), {
    timeout: 120_000,
  });
  await page.getByRole("button", { name: "Search" }).click();
  const found = await lookup;
  if (found.status() !== 200) {
    await outageOr("radarr", f.tmdbId, "lookup", found.status(), await found.text());
  }
  await page
    .getByRole("radio", { name: new RegExp(`^${f.title} \\(${f.year}\\)`) })
    .first()
    .check();
  await page.getByRole("button", { name: "Use this title" }).click();
  await expect(page).toHaveURL(/\/mapping$/, { timeout: 120_000 });
  await expect(page.getByText("No problems found.")).toBeVisible({ timeout: 120_000 });
  await page.getByRole("link", { name: "Continue to the confirmation" }).click();
  await expect(page).toHaveURL(new RegExp(`/jobs/${id}/confirm$`));
  await expect(page.getByRole("button", { name: "Import", exact: true })).toBeVisible();
  await shoot(page, "confirm.png", true);

  // 3. The import, by Radarr, and the finished job with the link to the title.
  await page.getByRole("button", { name: "Import", exact: true }).click();
  await expect(page).toHaveURL(new RegExp(`/jobs/${id}$`));
  await api.until(id, "done", (j) => j.state === "done", { timeoutMs: 240_000 });
  await page.reload();
  await expect(page.getByRole("link", { name: "Open the title in the media server" })).toBeVisible({
    timeout: 60_000,
  });
  await shoot(page, "done.png");
});

test("a season: what Sonarr made of each file", async ({ page }) => {
  const s = manifest.season;
  await needMetadata("sonarr", s.tvdbId);
  await removeShow();
  await loginAs(page, "user1");

  const { id } = await api.createJob();
  started.push(id);
  await api.upload(id, path.join(m2, s.file));
  await api.submit(id);
  const probed = await api.settled(id, 180_000);
  expect(probed.state, JSON.stringify(probed.error)).toBe("probed");
  const m = await api.match(id, { app: "sonarr", tvdbId: s.tvdbId });
  if (m.status !== 200) await outageOr("sonarr", s.tvdbId, "match", m.status, m.body);
  await api.readyProposal(id);

  await page.goto(`/jobs/${id}/mapping`);
  await expect(
    page.getByText("This file is about twice as long as an episode. It may hold two."),
  ).toBeVisible({ timeout: 60_000 });
  await shoot(page, "review.png", true);
});

test("the admin page: health and settings", async ({ page }) => {
  await loginAs(page, "admin1");
  await page.goto("/admin");
  await expect(page.getByRole("heading", { name: "Settings" })).toBeVisible();
  await expect(page.getByText("Everything works")).toBeVisible();
  await expect(page.getByRole("button", { name: "Check again" })).toBeEnabled();
  await shoot(page, "admin.png");
});
