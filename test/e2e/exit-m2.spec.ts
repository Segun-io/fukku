import { readdir, readFile, stat } from "node:fs/promises";
import path from "node:path";
import { expect, type Page, test } from "@playwright/test";
import { loginAs, stack } from "./helpers";
import { Api, type Job, type JobFile, type Proposal } from "./helpers/api";
import {
  bazarr,
  type Episode,
  jellyfin,
  type Movie,
  radarr,
  type SubtitleHistory,
  sonarr,
} from "./helpers/apps";
import { dataDir, fixturesDir, online, simulateOutage, titleLinkTemplate } from "./helpers/env";
import { needMetadata, outageOr } from "./helpers/online";

// The M2 exit criteria (plan section 15, issue #47), on the dev stack with the test Radarr,
// Sonarr, Bazarr and Jellyfin:
//   1. a new film arrives by rename (same inode) and the media server shows it within a minute;
//   2. a season zip with a double episode and a misnumbered file imports correctly after
//      editing, and the dubbed episode carries the language warning;
//   3. a kill of fukku-web during `importing` ends in `done` without duplicates;
//   4. an uploaded subtitle is written by the subtitle manager (decision #23).
// And what the import engine's review (#40, PR #73) left to check against the real apps:
// Radarr and Sonarr list a ManualImport with its file paths (the lost-id recovery reads
// them); what a command id answers after the app restarts (#73 expected 404; see 3e); and
// the size the apps report for an imported file is its size on disk (the arrival check
// compares them).
//
// The tests add titles through the apps' public metadata services, so they are tagged
// @online: they run with FUKKU_E2E_ONLINE=1 (make e2e; CI where the online probes run) and
// skip, not fail, when that service is down. The titles are the probes' free ones; each test
// removes what it added, with its files, from the app and from Jellyfin.
//
// 1 and 2 confirm on the confirmation page (#44); the kills of 3 confirm through the API.

const m2 = path.join(fixturesDir, "m2");
/** Decision #22: visible in the media server within a minute of the import. */
const visibleWithinMs = 60_000;

interface FilmFixture {
  tmdbId: number;
  title: string;
  year: number;
  file: string;
  subtitle?: string;
}
interface Manifest {
  films: FilmFixture[];
  season: {
    tvdbId: number;
    title: string;
    file: string;
    want: { file: string; season: number; episodes: number[]; dubbed: boolean }[];
  };
}

let manifest: Manifest;
const api = new Api("user1");

function film(title: string): FilmFixture {
  const f = manifest.films.find((x) => x.title === title);
  if (!f) throw new Error(`no fixture for ${title}: make e2e-fixtures`);
  return f;
}

/** The job's own copy of a file, as the runner (and the apps) see it. */
function jobPath(jobId: string, f: { path: string }): string {
  return path.join(dataDir, "intake", "jobs", jobId, f.path);
}

async function inode(p: string): Promise<{ dev: bigint; ino: bigint; size: bigint }> {
  const s = await stat(p, { bigint: true });
  return { dev: s.dev, ino: s.ino, size: s.size };
}

async function exists(p: string): Promise<boolean> {
  return stat(p).then(
    () => true,
    () => false,
  );
}

// The first line of every test here is needMetadata (helpers/online.ts): it skips without
// FUKKU_E2E_ONLINE=1, and when the app cannot reach its metadata service.

// ---- Clean up ----------------------------------------------------------------------------

/** Jobs a test started; cancelled at the end unless they finished, so no title stays in use. */
const started: string[] = [];

// Jellyfin keeps the item of a removed file: a scan does not drop it while the library's
// folder is empty (it takes that for a missing disk), and item ids follow the path, so a
// later import at the same path would find the old item at once. The items under the
// title's folder are removed with the title (the apps' default folder names, and the folder
// the app had).

/** Remove the film from Radarr (with its files) and from Jellyfin. */
async function removeFilm(f: FilmFixture) {
  const folders = await radarr.removeByTmdb(f.tmdbId);
  for (const folder of new Set([...folders, `/data/media/movies/${f.title} (${f.year})`])) {
    await jellyfin.forget(folder);
  }
}

async function removeShow(s: Manifest["season"]) {
  const folders = await sonarr.removeByTvdb(s.tvdbId);
  for (const folder of new Set([...folders, `/data/media/tv/${s.title}`])) {
    await jellyfin.forget(folder);
  }
}

test.beforeAll(async () => {
  manifest = JSON.parse(await readFile(path.join(m2, "manifest.json"), "utf8")) as Manifest;
});

test.afterEach(async () => {
  for (const id of started.splice(0)) {
    const job = await api.job(id).catch(() => null);
    if (job && !["done", "failed", "cancelled"].includes(job.state)) {
      await api.cancel(id).catch(() => {});
    }
  }
  if (!online || simulateOutage) return;
  for (const f of manifest.films) await removeFilm(f);
  await removeShow(manifest.season);
});

/** Through the API: upload, probe, match (the film is added), proposal ready. */
async function filmAwaitingConfirmation(f: FilmFixture): Promise<{ job: Job; proposal: Proposal }> {
  const files = [path.join(m2, f.file), ...(f.subtitle ? [path.join(m2, f.subtitle)] : [])];
  const { id } = await api.createJob();
  started.push(id);
  for (const file of files) await api.upload(id, file);
  await api.submit(id);
  const probed = await api.settled(id);
  expect(probed.state, JSON.stringify(probed.error)).toBe("probed");
  const m = await api.match(id, { app: "radarr", tmdbId: f.tmdbId });
  if (m.status !== 200) await outageOr("radarr", f.tmdbId, "match", m.status, m.body);
  const proposal = await api.readyProposal(id);
  expect(proposal.rows).toHaveLength(1);
  expect(proposal.rows[0]?.importable, JSON.stringify(proposal.rows[0])).toBe(true);
  return { job: await api.job(id), proposal };
}

function video(job: Job): JobFile {
  const v = job.files.find((f) => f.kind === "video" && f.status === "ok");
  if (!v) throw new Error(`job ${job.id} has no video`);
  return v as JobFile;
}

async function movieOf(tmdbId: number): Promise<Movie> {
  const [m] = await radarr.movieByTmdb(tmdbId);
  if (!m) throw new Error(`Radarr has no film ${tmdbId}`);
  return m;
}

/**
 * The film's import left exactly one file in Radarr and on disk, the job's own file moved
 * there (same inode), and exactly one ManualImport of the job's folder in Radarr's command
 * list, which lists the file's path (#73: the lost-id recovery reads it).
 */
async function oneFilmFile(
  f: FilmFixture,
  job: Job,
  src: { path: string; ino: bigint; dev: bigint; size: bigint },
  manualImports = 1,
) {
  const movie = await movieOf(f.tmdbId);
  expect(movie.hasFile, "Radarr tracks a file for the film").toBe(true);
  const file = movie.movieFile;
  if (!file) throw new Error("movie.movieFile is missing");
  expect(await radarr.movieFiles(movie.id), "one file for the film in Radarr").toHaveLength(1);
  const videos = (await readdir(movie.path)).filter((n) => n.endsWith(".mkv"));
  expect(videos, "one video in the film's folder").toHaveLength(1);
  expect(path.join(movie.path, videos[0] ?? "")).toBe(file.path);

  const now = await inode(file.path);
  expect({ dev: now.dev, ino: now.ino }, "the same inode: a rename").toEqual({
    dev: src.dev,
    ino: src.ino,
  });
  expect(await exists(src.path), "the job's file is gone (move)").toBe(false);
  // #73: Radarr's movieFile.size is the file's size on disk.
  expect(BigInt(file.size), "Radarr's movieFile.size is the size on disk").toBe(now.size);
  expect(BigInt(file.size)).toBe(src.size);

  const folder = path.join(dataDir, "intake", "jobs", job.id);
  const cmds = await radarr.manualImports(folder);
  expect(cmds.length, "ManualImport commands of the job's folder").toBe(manualImports);
  for (const c of cmds) {
    expect(c.body?.files?.map((x) => x.path)).toEqual([src.path]);
  }
  return { movie, file, commands: cmds };
}

async function untilDone(id: string, timeoutMs = 240_000): Promise<Job> {
  const job = await api.until(id, "done", (j) => j.state === "done", { timeoutMs });
  expect(job.import?.status).toBe("done");
  expect(job.import?.rows.every((r) => r.result === "imported" || r.result === "skipped")).toBe(
    true,
  );
  return job;
}

/** The step's view in the job's result. */
function step(job: Job, name: string) {
  return job.result?.postProcessing.find((s) => s.step === name);
}

/** Bazarr's history shows the hand-over (decision #23): a manual upload, the file on disk. */
function manualUpload(history: SubtitleHistory[], code2: string, folder: string) {
  const entry = history.find(
    (h) => h.action === 4 && h.provider === "manual" && h.language?.code2 === code2,
  );
  expect(
    entry,
    `a manual upload of ${code2} in Bazarr's history: ${JSON.stringify(history)}`,
  ).toBeTruthy();
  expect(path.dirname(entry?.subtitles_path ?? "")).toBe(folder);
  return entry as SubtitleHistory;
}

// ---- The UI steps ---------------------------------------------------------------------

/** Upload through the upload page; returns the job id once the page shows the job. */
async function uploadThroughUI(page: Page, files: string[]): Promise<string> {
  await page.goto("/upload");
  await page.locator('input[type="file"]').setInputFiles(files);
  const created = page.waitForResponse(
    (r) => new URL(r.url()).pathname === "/api/jobs" && r.request().method() === "POST",
  );
  await page.getByRole("button", { name: "Start upload" }).click();
  const res = await created;
  expect(res.status()).toBe(201);
  const id = ((await res.json()) as Job).id;
  started.push(id);
  await expect(page).toHaveURL(new RegExp(`/jobs/${id}$`), { timeout: 180_000 });
  return id;
}

/** On the job page, once the files are checked: the match page, the title, its options. */
async function matchThroughUI(
  page: Page,
  kind: "A film" | "A show",
  term: string,
  hit: RegExp,
  outage: { app: "radarr" | "sonarr"; id: number },
) {
  const start = page.getByRole("link", { name: "Choose the film or show" });
  await expect(start).toBeVisible({ timeout: 180_000 });
  await start.click();
  await expect(page).toHaveURL(/\/match$/);
  const title = page.getByRole("textbox", { name: "Title" });
  await expect(title).toBeVisible();
  // The kind is offered only when both apps are configured (they are, in the dev stack).
  await page.getByRole("radio", { name: kind, exact: true }).check();
  await title.fill(term);
  const lookup = page.waitForResponse((r) => new URL(r.url()).pathname.startsWith("/api/lookup/"), {
    timeout: 120_000,
  });
  await page.getByRole("button", { name: "Search" }).click();
  const res = await lookup;
  if (res.status() !== 200) {
    await outageOr(outage.app, outage.id, "lookup", res.status(), await res.text());
  }
  await page.getByRole("radio", { name: hit }).first().check();
  const matched = page.waitForResponse(
    (r) =>
      /\/api\/jobs\/[^/]+\/match$/.test(new URL(r.url()).pathname) &&
      r.request().method() === "POST",
    { timeout: 120_000 },
  );
  await page.getByRole("button", { name: "Use this title" }).click();
  const m = await matched;
  if (m.status() !== 200)
    await outageOr(outage.app, outage.id, "match", m.status(), await m.text());
  await expect(page).toHaveURL(/\/mapping$/);
}

// ---- 1 and 4: a new film ---------------------------------------------------------------

test.describe("M2 exit criteria", { tag: "@online" }, () => {
  // A control that never appears fails the step in a minute, not at the test's timeout.
  test.use({ actionTimeout: 60_000 });

  test("1+4. a new film arrives by rename, the media server shows it within a minute, its subtitle is written by Bazarr", async ({
    page,
  }) => {
    test.setTimeout(10 * 60_000);
    const f = film("Big Buck Bunny");
    await needMetadata("radarr", f.tmdbId);
    await removeFilm(f);
    await loginAs(page, "user1");

    const id = await test.step("upload the film and its subtitle through the upload page", () =>
      uploadThroughUI(page, [path.join(m2, f.file), path.join(m2, f.subtitle ?? "")]));
    await test.step("choose the title on the match page", () =>
      matchThroughUI(page, "A film", f.title, new RegExp(`^${f.title} \\(${f.year}\\)`), {
        app: "radarr",
        id: f.tmdbId,
      }));
    await test.step("the mapping page shows the one file, with nothing to fix", async () => {
      await expect(page.getByText(path.basename(f.file))).toBeVisible({ timeout: 120_000 });
      await expect(page.getByText("No problems found.")).toBeVisible();
      await page.getByRole("link", { name: "Continue to the confirmation" }).click();
      await expect(page).toHaveURL(new RegExp(`/jobs/${id}/confirm$`));
    });

    const job = await api.job(id);
    expect(job.state).toBe("awaiting-confirmation");
    const v = video(job);
    const sub = job.files.find((x) => x.kind === "subtitle") as JobFile;
    const src = { path: jobPath(id, v), ...(await inode(jobPath(id, v))) };
    const movie = await movieOf(f.tmdbId);
    expect(await jellyfin.itemsUnder(movie.path), "Jellyfin knows nothing of the film yet").toEqual(
      [],
    );

    // Watch Radarr and Jellyfin from the confirmation on: when the file is tracked, and when
    // Jellyfin lists it.
    let tracked = 0;
    let visible = 0;
    let filePath = "";
    let watching = true;
    const watch = (async () => {
      while (watching && (!tracked || !visible)) {
        if (!tracked) {
          const m = await radarr.movie(movie.id).catch(() => null);
          if (m?.movieFile) {
            tracked = Date.now();
            filePath = m.movieFile.path;
          }
        }
        if (tracked && !visible && (await jellyfin.itemAt(filePath).catch(() => null))) {
          visible = Date.now();
        }
        await new Promise((r) => setTimeout(r, 200));
      }
    })();

    await test.step("confirm on the confirmation page: the subtitle's language is guessed from its name", async () => {
      await expect(
        page.getByRole("textbox", {
          name: new RegExp(`^Language of ${sub.name.replace(/\./g, "\\.")}`),
        }),
      ).toHaveValue("es");
      await expect(page.getByRole("checkbox", { name: `${sub.name} is forced` })).not.toBeChecked();
      await expect(
        page.getByRole("checkbox", { name: `${sub.name} is for the hard of hearing` }),
      ).not.toBeChecked();
      const confirmed = page.waitForResponse(
        (r) => new URL(r.url()).pathname === `/api/jobs/${id}/confirm`,
      );
      await page.getByRole("button", { name: "Import", exact: true }).click();
      const res = await confirmed;
      expect(res.status(), await res.text()).toBe(202);
      expect(res.request().postDataJSON()).toEqual({
        overrides: [],
        subtitles: [{ fileId: sub.id, language: "es", forced: false, hearingImpaired: false }],
      });
      await expect(page).toHaveURL(new RegExp(`/jobs/${id}$`));
    });
    try {
      await expect
        .poll(() => tracked, { message: "Radarr tracks the film's file", timeout: 120_000 })
        .toBeGreaterThan(0);
      await expect
        .poll(() => visible, {
          message: "Jellyfin lists the file within a minute of the import",
          timeout: visibleWithinMs + 5_000,
        })
        .toBeGreaterThan(0);
    } finally {
      watching = false;
      await watch;
    }
    const done = await untilDone(id);

    const { file } = await oneFilmFile(f, done, src);
    const item = await jellyfin.itemAt(file.path);
    expect(item?.Type).toBe("Movie");
    const latency = visible - tracked;
    console.log(
      `media server: visible ${(latency / 1000).toFixed(1)} s after Radarr tracked the file`,
    );
    expect(latency, "visible in Jellyfin within a minute of the import").toBeLessThanOrEqual(
      visibleWithinMs,
    );
    expect(step(done, "mediaServerRefresh")).toEqual({
      step: "mediaServerRefresh",
      status: "ok",
      detail: `item ${item?.Id}`,
    });
    expect(done.result?.titleLink).toBe(titleLinkTemplate.replace("{itemId}", item?.Id ?? ""));

    // 4. The subtitle: handed over, and written next to the film by Bazarr, with the
    // language and flags chosen at the confirmation (decision #23: the history proves it).
    expect(step(done, "subtitles")?.status).toBe("ok");
    const entry = manualUpload(await bazarr.movieHistory(movie.id), "es", movie.path);
    expect(entry.language).toMatchObject({ code2: "es", forced: false, hi: false });
    const written = await readFile(entry.subtitles_path, "utf8");
    const uploaded = await readFile(path.join(m2, f.subtitle ?? ""), "utf8");
    expect(written.replace(/\r\n/g, "\n").trim()).toBe(uploaded.replace(/\r\n/g, "\n").trim());
  });

  // ---- 2 and 4: the awkward season ------------------------------------------------------

  test("2+4. a season zip with a double episode and a misnumbered file imports correctly after editing", async ({
    page,
  }) => {
    test.setTimeout(15 * 60_000);
    const s = manifest.season;
    await needMetadata("sonarr", s.tvdbId);
    await removeShow(s);
    await loginAs(page, "user1");

    const id = await test.step("upload the zip through the upload page", () =>
      uploadThroughUI(page, [path.join(m2, s.file)]));
    await test.step("choose the show on the match page", () =>
      matchThroughUI(page, "A show", s.title, new RegExp(`^${s.title} \\(`), {
        app: "sonarr",
        id: s.tvdbId,
      }));

    const name = (want: { file: string }) => path.basename(want.file);
    const [double, , misnumbered, dub, beyond] = s.want;
    if (!double || !misnumbered || !dub || !beyond)
      throw new Error("the manifest's season is not the awkward one");
    const before = await api.readyProposal(id);
    const series = (await sonarr.seriesByTvdb(s.tvdbId))[0];
    if (!series) throw new Error("Sonarr has no series after the match");
    const episodes = await sonarr.episodes(series.id);
    const ep = (n: number) => {
      const e = episodes.find((x) => x.seasonNumber === 1 && x.episodeNumber === n);
      if (!e) throw new Error(`Sonarr has no S01E${n}`);
      return e;
    };
    // Pioneer One's first season has six episodes, so the fixture's seventh file has none.
    expect(episodes.filter((e) => e.seasonNumber === 1).map((e) => e.episodeNumber)).toEqual([
      1, 2, 3, 4, 5, 6,
    ]);

    await test.step("the editor shows what Sonarr and Fukku found", async () => {
      await expect(page.getByText(name(double))).toBeVisible({ timeout: 120_000 });
      // The double: Sonarr proposes episode 1 only; Fukku's own duration check hints at two.
      await expect(
        page.getByText("This file is about twice as long as an episode. It may hold two."),
      ).toBeVisible();
      // The dub: Sonarr gives no rejection; Fukku warns (decision #21).
      await expect(
        page.getByText(/The audio may not be in the original language\. Languages: Spanish\./),
      ).toBeVisible();
      await expect(page.getByText("Rules that matched: Not Original Language.")).toBeVisible();
      const row = (n: string) => before.rows.find((r) => r.name === n);
      expect(row(name(double))?.episodes?.map((e) => e.number)).toEqual([1]);
      expect(row(name(double))?.warnings.map((w) => w.code)).toEqual(["double_episode"]);
      expect(row(name(misnumbered))?.episodes?.map((e) => e.number)).toEqual([5]);
      expect(row(name(dub))?.warnings).toEqual([
        {
          code: "language",
          customFormats: ["Not Original Language"],
          score: -10000,
          minFormatScore: 0,
        },
      ]);
      expect(row(name(beyond))?.episodes).toEqual([]);
    });

    await test.step("fix the two rows, and skip the file that has no episode", async () => {
      await page.getByRole("button", { name: "Make it episodes 1 and 2" }).click();
      await expect(
        page.getByRole("combobox", { name: `Second episode of ${name(double)}` }),
      ).toHaveValue(String(ep(2).id), { timeout: 60_000 });
      await page
        .getByRole("combobox", { name: `Episode of ${name(misnumbered)}`, exact: true })
        .selectOption({ label: `4. ${ep(4).title}` });
      await expect(
        page.getByRole("combobox", { name: `Episode of ${name(misnumbered)}`, exact: true }),
      ).toHaveValue(String(ep(4).id), { timeout: 60_000 });
      await page.getByRole("checkbox", { name: `Skip ${name(beyond)}`, exact: true }).check();
      await expect(page.getByText("Checking…")).toHaveCount(0, { timeout: 60_000 });
      await expect(
        page.getByText("This file is about twice as long as an episode. It may hold two."),
      ).toHaveCount(0);
      await page.getByRole("link", { name: "Continue to the confirmation" }).click();
      await expect(page).toHaveURL(new RegExp(`/jobs/${id}/confirm$`));
    });

    // What the edits stored, re-checked through Sonarr.
    const edited = await api.proposal(id);
    const row = (n: string) => {
      const r = edited.rows.find((x) => x.name === n);
      if (!r) throw new Error(`no row for ${n}`);
      return r;
    };
    for (const w of s.want) {
      const r = row(name(w));
      if (w === beyond) {
        expect(r.skip).toBe(true);
        continue;
      }
      expect(r.skip).toBe(false);
      expect(r.seasonNumber).toBe(w.season);
      expect(
        r.episodes?.map((e) => e.number),
        name(w),
      ).toEqual(w.episodes);
      expect(r.rejections, name(w)).toEqual([]);
      expect(r.importable, name(w)).toBe(!w.dubbed);
    }

    // The job's files, before the import moves them.
    const job = await api.job(id);
    const fileOf = (n: string) => {
      const f = job.files.find((x) => x.name === n && x.kind === "video");
      if (!f) throw new Error(`no job file ${n}`);
      return f;
    };
    const src = new Map<string, { path: string; ino: bigint; dev: bigint; size: bigint }>();
    for (const w of s.want) {
      const p = jobPath(id, fileOf(name(w)));
      src.set(name(w), { path: p, ...(await inode(p)) });
    }
    const srt = job.files.find((x) => x.kind === "subtitle") as JobFile;

    await test.step("the dub needs its warning confirmed: needs_override without it", async () => {
      const refused = await api.confirm(id, {});
      expect(refused.status).toBe(409);
      expect(refused.body.error).toMatchObject({
        code: "needs_override",
        rows: [row(name(dub)).id],
      });
    });
    await test.step("confirm on the confirmation page, ticking the dub's warning", async () => {
      // The page shows the warning with its facts, and refuses to send without the tick.
      await page.reload();
      await expect(page.getByText(/Not Original Language/).first()).toBeVisible({
        timeout: 60_000,
      });
      const tick = page.getByRole("checkbox", { name: "Import anyway" });
      await expect(tick).toHaveCount(1);
      await page.getByRole("button", { name: "Import", exact: true }).click();
      await expect(
        page.getByText("Tick “Import anyway” for each file with a problem, or leave it out."),
      ).toBeVisible();
      await expect(
        page.getByRole("textbox", {
          name: new RegExp(`^Language of ${srt.name.replace(/\./g, "\\.")}`),
        }),
      ).toHaveValue("en");
      await tick.check();
      const confirmed = page.waitForResponse(
        (r) => new URL(r.url()).pathname === `/api/jobs/${id}/confirm`,
      );
      await page.getByRole("button", { name: "Import", exact: true }).click();
      const res = await confirmed;
      expect(res.status(), await res.text()).toBe(202);
      expect(res.request().postDataJSON()).toEqual({
        overrides: [row(name(dub)).id],
        subtitles: [{ fileId: srt.id, language: "en", forced: false, hearingImpaired: false }],
      });
      await expect(page).toHaveURL(new RegExp(`/jobs/${id}$`));
    });
    const done = await untilDone(id);
    expect(done.import?.rows.find((r) => r.name === name(beyond))?.result).toBe("skipped");

    // In Sonarr: every episode the archive holds has its file, the double covers two
    // episodes, nothing is on the wrong episode, episode 5 (not in the archive) has none.
    const now = await sonarr.episodes(series.id);
    const files = await sonarr.episodeFiles(series.id);
    const fileFor = (e: Episode) => files.find((x) => x.id === e.episodeFileId);
    for (const w of s.want.filter((x) => x !== beyond)) {
      const own = src.get(name(w));
      for (const n of w.episodes) {
        const e = now.find((x) => x.seasonNumber === 1 && x.episodeNumber === n);
        expect(e?.hasFile, `S01E0${n} has a file`).toBe(true);
        const ef = e && fileFor(e);
        if (!ef || !own) throw new Error(`no episode file for S01E0${n}`);
        const at = await inode(ef.path);
        expect({ dev: at.dev, ino: at.ino }, `S01E0${n} holds ${name(w)} (same inode)`).toEqual({
          dev: own.dev,
          ino: own.ino,
        });
        // #73: Sonarr's /episodefile size is the file's size on disk.
        expect(BigInt(ef.size), `S01E0${n}: /episodefile size is the size on disk`).toBe(at.size);
        expect(BigInt(ef.size)).toBe(own.size);
      }
    }
    const e1 = now.find((x) => x.episodeNumber === 1);
    const e2 = now.find((x) => x.episodeNumber === 2);
    expect(e1?.episodeFileId, "the double is one file for two episodes").toBe(e2?.episodeFileId);
    expect(now.find((x) => x.episodeNumber === 5)?.hasFile, "S01E05 is not in the archive").toBe(
      false,
    );
    expect(files, "four files: the double, 3, 4 and the dub").toHaveLength(4);
    expect(
      await exists(src.get(name(beyond))?.path ?? ""),
      "the skipped file stays in the job",
    ).toBe(true);

    // #73: Sonarr lists the ManualImport with its files; exactly one for the job.
    const folder = path.join(dataDir, "intake", "jobs", id);
    const cmds = await sonarr.manualImports(folder);
    expect(cmds).toHaveLength(1);
    expect(cmds[0]?.body?.files?.map((x) => x.path).sort()).toEqual(
      s.want
        .filter((x) => x !== beyond)
        .map((x) => src.get(name(x))?.path)
        .sort(),
    );

    // 4. The episode's subtitle, written by Bazarr next to S01E03.
    expect(step(done, "subtitles")?.status).toBe("ok");
    const e3 = now.find((x) => x.episodeNumber === 3);
    const e3file = e3 && fileFor(e3);
    if (!e3 || !e3file) throw new Error("no S01E03");
    const entry = manualUpload(await bazarr.episodeHistory(e3.id), "en", path.dirname(e3file.path));
    expect(await exists(entry.subtitles_path)).toBe(true);

    // The media server shows the show; the link goes to it.
    const link = step(done, "mediaServerRefresh");
    expect(link?.status).toBe("ok");
    const show = await jellyfin.itemAt(series.path, "Series");
    expect(show, "Jellyfin lists the show's folder").toBeTruthy();
    expect(done.result?.titleLink).toBe(titleLinkTemplate.replace("{itemId}", show?.Id ?? ""));

    // #73: what Sonarr says of the command after it restarts (see 3e for Radarr): the id
    // still answers from its database, and the list starts empty.
    const cmd = cmds[0];
    if (!cmd) throw new Error("no command");
    await stack.restartApp("sonarr", () => sonarr.ping());
    expect((await sonarr.command(cmd.id)).status, "the command by id after Sonarr restarted").toBe(
      "completed",
    );
    expect(await sonarr.manualImports(folder), "the command list after the restart").toEqual([]);
  });

  // ---- 3: a kill during importing --------------------------------------------------------

  // Each point is reached by watching what Fukku and the app say, never by a fixed sleep.
  // After the kill, fukku-web starts again and the job must end done with one file and one
  // ManualImport of the job's folder.

  /** Kill fukku-web (SIGKILL), run `meanwhile` while it is down, start it again. */
  async function crash(meanwhile: () => Promise<void> = async () => {}): Promise<number> {
    const { at, start } = await stack.kill("fukku-web");
    try {
      await meanwhile();
    } finally {
      await start();
    }
    await stack.waitReady();
    return at;
  }

  test("3a. killed before the command is submitted (Radarr hangs on Fukku's first call)", async () => {
    test.setTimeout(10 * 60_000);
    const f = film("Sintel");
    await needMetadata("radarr", f.tmdbId);
    await removeFilm(f);
    const { job } = await filmAwaitingConfirmation(f);
    const v = video(job);
    const src = { path: jobPath(job.id, v), ...(await inode(jobPath(job.id, v))) };

    const confirmed = await api.confirm(job.id, {});
    expect(confirmed.status).toBe(202);
    // Radarr stops answering at once: the importing step's first call (the busy check)
    // waits, so nothing can be submitted. A hung call shows nowhere, so this waits for
    // two runner passes (worker.pollInterval is 1s): the step has started and is stuck.
    // Then the job must still say pending (else the submission beat the freeze).
    const thaw = await stack.freezeApp("radarr");
    let frozen = true;
    try {
      await new Promise((r) => setTimeout(r, 2500));
      const seen = await api.job(job.id);
      expect(seen.state).toBe("importing");
      expect(seen.import?.status, "nothing submitted before the kill").toBe("pending");
      await crash(async () => {
        await thaw();
        frozen = false;
      });
    } finally {
      if (frozen) await thaw();
    }
    const done = await untilDone(job.id);
    await oneFilmFile(f, done, src);
  });

  test("3b. killed as soon as the submission has started (the command id may not be stored)", async () => {
    test.setTimeout(10 * 60_000);
    const f = film("Tears of Steel");
    await needMetadata("radarr", f.tmdbId);
    await removeFilm(f);
    const { job } = await filmAwaitingConfirmation(f);
    const v = video(job);
    const src = { path: jobPath(job.id, v), ...(await inode(jobPath(job.id, v))) };

    const confirmed = await api.confirm(job.id, {});
    expect(confirmed.status).toBe(202);
    // "running" is set when Fukku stores "submitting", just before it calls Radarr.
    await api.until(job.id, "the submission", (j) => j.import?.status === "running", {
      timeoutMs: 60_000,
    });
    const since = Date.now() - 1000;
    await crash();
    const done = await untilDone(job.id);
    await oneFilmFile(f, done, src);
    // Which side of the call the kill landed on, for the record: the stored id was polled,
    // or the command was found again in Radarr's list (import_command_found).
    const found = (await stack.logs("fukku-web", since)).includes("import_command_found");
    console.log(
      `3b: ${found ? "the lost command id was found again in Radarr's command list" : "the stored command id was polled"}`,
    );
  });

  test("3c. killed after the command finished, before Fukku saw it", async () => {
    test.setTimeout(10 * 60_000);
    const f = film("Elephants Dream");
    await needMetadata("radarr", f.tmdbId);
    await removeFilm(f);
    const { job } = await filmAwaitingConfirmation(f);
    const v = video(job);
    const src = { path: jobPath(job.id, v), ...(await inode(jobPath(job.id, v))) };
    const folder = path.join(dataDir, "intake", "jobs", job.id);

    const confirmed = await api.confirm(job.id, {});
    expect(confirmed.status).toBe(202);
    await expect
      .poll(async () => (await radarr.manualImports(folder))[0]?.status, {
        timeout: 60_000,
        intervals: [100],
      })
      .toBe("completed");
    const seen = await api.job(job.id);
    const killedAt = await crash();
    const done = await untilDone(job.id);
    await oneFilmFile(f, done, src);
    const imported = done.history.find((h) => h.from === "importing" && h.to === "imported");
    console.log(
      `3c: Fukku was ${seen.state} when it was killed; it recorded the import ${((Date.parse(imported?.at ?? "") - killedAt) / 1000).toFixed(1)} s after the kill`,
    );
  });

  test("3d. killed during post-processing: the import is not repeated", async () => {
    test.setTimeout(10 * 60_000);
    const f = film("Big Buck Bunny");
    await needMetadata("radarr", f.tmdbId);
    await removeFilm(f);
    const { job } = await filmAwaitingConfirmation(f);
    const v = video(job);
    const src = { path: jobPath(job.id, v), ...(await inode(jobPath(job.id, v))) };

    const confirmed = await api.confirm(job.id, {});
    expect(confirmed.status).toBe(202);
    await api.until(job.id, "post-processing", (j) => j.state === "post-processing", {
      timeoutMs: 120_000,
    });
    await crash();
    const done = await untilDone(job.id);
    await oneFilmFile(f, done, src);
    expect(step(done, "mediaServerRefresh")?.status).toBe("ok");
  });

  // #73 expected a command id to answer 404 after the app restarts. Radarr 6.4 and Sonarr 4.0
  // do not: GET /command/<id> still answers from their database, with the status the command
  // ended with; only the list (GET /command) starts empty. This test records that, and that
  // Fukku, killed meanwhile, then ends done without importing again.
  test("3e. Radarr restarts while fukku-web is down: the command id still answers, the list forgets it, the job ends done", async () => {
    test.setTimeout(10 * 60_000);
    const f = film("Sintel");
    await needMetadata("radarr", f.tmdbId);
    await removeFilm(f);
    const { job } = await filmAwaitingConfirmation(f);
    const v = video(job);
    const src = { path: jobPath(job.id, v), ...(await inode(jobPath(job.id, v))) };
    const folder = path.join(dataDir, "intake", "jobs", job.id);

    const confirmed = await api.confirm(job.id, {});
    expect(confirmed.status).toBe(202);
    await expect
      .poll(async () => (await radarr.manualImports(folder))[0]?.status, {
        timeout: 60_000,
        intervals: [100],
      })
      .toBe("completed");
    const [cmd] = await radarr.manualImports(folder);
    if (!cmd) throw new Error("no command");
    await crash(async () => {
      await stack.restartApp("radarr", () => radarr.ping());
      const after = await radarr.command(cmd.id);
      expect(after.status, "a finished command, asked by id after Radarr restarted").toBe(
        "completed",
      );
      expect(await radarr.manualImports(folder), "the command list after the restart").toEqual([]);
    });
    const done = await untilDone(job.id);
    // The list started empty with the restart: no ManualImport may have been added since.
    await oneFilmFile(f, done, src, 0);
  });
});
