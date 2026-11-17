import { createHash, randomBytes } from "node:crypto";
import { open, readdir, readFile, rm, stat } from "node:fs/promises";
import path from "node:path";
import { expect, type Page, test } from "@playwright/test";
import { loginAs, stack } from "./helpers";
import { Api, type Job, type JobFile, type VersionRecord } from "./helpers/api";
import { indexer, jellyfin, type Movie, mediacheck, radarr } from "./helpers/apps";
import { dataDir, fixturesDir, online, simulateOutage, versionsRoot } from "./helpers/env";
import { needMetadata, outageOr } from "./helpers/online";

// The M3 exit criteria (plan section 15, issue #61), on the dev stack with the test Radarr,
// Jellyfin and the fake indexer (#24), with versions.enabled on in the test config only:
//   1. a matching dub ends as one file with correctly tagged tracks and the film unmonitored,
//      and neither RSS sync nor the Wanted search replaces it (decision B6 on #61: the film's
//      own Search still can, and the UI says so);
//   2. a dub whose audio is 800 ms late is caught by the preview: its clips are measurably out
//      of sync, and in sync with an offset of -800 ms;
//   3. a PAL copy becomes a second version, as decision #20 defines it: outside Radarr's root
//      folder, Radarr's file untouched, grouped with the film by Jellyfin;
//   4. a winning label is refused by the check #20 defines: a forbidden token, and a word
//      Radarr's parse reads a quality in;
//   5. a failed mux leaves the library untouched: the merge task killed (5a), and a corrupt
//      upload (5b); the library file's hash and Radarr's record are as before.
//
// Like the M2 tests they add a title through Radarr's metadata service, so they are tagged
// @online and skip without FUKKU_E2E_ONLINE=1 or when that service is down. They use one of
// the probes' free titles (Big Buck Bunny), which each test removes, with its files and its
// version, from Radarr and Jellyfin before and after. Each test puts the film in the library
// through flow 1 (the English file of make e2e-fixtures' m3/), then uploads the other
// language. 1 and 2 go through the pages (upload, match, the flow 2 page's sync check and
// merge, the result); 3 to 5 through the API.
//
// The clips are measured by mediacheck (test/env/mediacheck, in the dev image): the runner's
// image has no ffmpeg and its Chromium cannot decode them, and the #53 tests' measure
// (fixtures.SyncOffset, the flash against the beep) is Go and ffmpeg.

const m3 = path.join(fixturesDir, "m3");

interface Manifest {
  tmdbId: number;
  title: string;
  year: number;
  film: string;
  dub: string;
  shifted: string;
  pal: string;
}

let fx: Manifest;
const api = new Api("user1");
const admin = new Api("admin1");
/** The film's folder name in Radarr, versions.root and Jellyfin. */
const folderName = () => `${fx.title} (${fx.year})`;
/** Clips within this of each other are in sync; at least `apart` is measurably out (#53). */
const inSyncMs = 50;
const apartMs = 150;
/** A word of letters for this run: a version's name is never reused (its record stays). */
const runTag = Date.now()
  .toString(36)
  .replace(/[0-9]/g, (d) => "abcdefghij"[Number(d)] ?? "x");

/** Jobs a test started; cancelled at the end unless they finished. */
const started: string[] = [];

test.beforeAll(async () => {
  fx = JSON.parse(await readFile(path.join(m3, "manifest.json"), "utf8")) as Manifest;
});

/**
 * Remove the film from Radarr (with its files), its second versions from versions.root, and
 * their items from Jellyfin. The versions folder of the film is this test's own: only these
 * tests place films' versions in the dev stack.
 */
async function removeFilm() {
  const folders = await radarr.removeByTmdb(fx.tmdbId);
  const versions = path.join(versionsRoot, folderName());
  await rm(versions, { recursive: true, force: true });
  for (const folder of new Set([...folders, `/data/media/movies/${folderName()}`, versions])) {
    await jellyfin.forget(folder);
  }
}

async function emptyBlackhole() {
  const dir = path.join(dataDir, "blackhole", "radarr", "nzb");
  for (const n of await readdir(dir).catch(() => [] as string[])) {
    await rm(path.join(dir, n), { force: true });
  }
}

test.afterEach(async () => {
  const cancelled: string[] = [];
  for (const id of started.splice(0)) {
    const job = await api.job(id).catch(() => null);
    if (job && !["done", "failed", "cancelled"].includes(job.state)) {
      await api.cancel(id).catch(() => {});
      cancelled.push(id);
    }
  }
  // The runner removes a cancelled job's folder on a later pass: wait for it, so that the
  // next test (exit-stack.spec.ts lists the intake around its uploads) sees a quiet intake.
  for (const id of cancelled) {
    await expect
      .poll(
        () =>
          stat(jobDir(id)).then(
            () => true,
            () => false,
          ),
        { timeout: 60_000 },
      )
      .toBe(false)
      .catch(() => console.log(`the folder of the cancelled job ${id} is still there`));
  }
  if (!online || simulateOutage) return;
  await indexer.clear().catch(() => {});
  await emptyBlackhole();
  await removeFilm();
});

// ---- The film in the library, and its other language ----------------------------------

async function movieOf(): Promise<Movie> {
  const [m] = await radarr.movieByTmdb(fx.tmdbId);
  if (!m) throw new Error(`Radarr has no film ${fx.tmdbId}`);
  return m;
}

/** Flow 1 through the API: the English film is added to Radarr and imported. */
async function filmInLibrary(): Promise<Movie> {
  await removeFilm();
  const { id } = await api.createJob();
  started.push(id);
  await api.upload(id, path.join(m3, fx.film));
  await api.submit(id);
  const probed = await api.settled(id);
  expect(probed.state, JSON.stringify(probed.error)).toBe("probed");
  const m = await api.match(id, { app: "radarr", tmdbId: fx.tmdbId });
  if (m.status !== 200) await outageOr("radarr", fx.tmdbId, "match", m.status, m.body);
  await api.readyProposal(id);
  const c = await api.confirm(id, {});
  expect(c.status, JSON.stringify(c.body)).toBe(202);
  await api.until(id, "the film's import", (j) => j.state === "done", { timeoutMs: 240_000 });
  const movie = await movieOf();
  expect(movie.hasFile, "the film has its file").toBe(true);
  return movie;
}

/** A flow 2 job of an upload, compared with the film's file and awaiting the user's choice. */
async function otherLanguage(file: string): Promise<Job> {
  const { id } = await api.createJob();
  started.push(id);
  await api.upload(id, path.join(m3, file));
  await api.submit(id);
  const probed = await api.settled(id);
  expect(probed.state, JSON.stringify(probed.error)).toBe("probed");
  const m = await api.match(id, { app: "radarr", tmdbId: fx.tmdbId });
  if (m.status !== 200) await outageOr("radarr", fx.tmdbId, "match", m.status, m.body);
  return compared(id);
}

/** The job once the library file is probed and compared (flow 2 ready). */
function compared(id: string): Promise<Job> {
  return api.until(
    id,
    "the comparison",
    (j) => j.state === "awaiting-confirmation" && j.flow2?.state === "ready",
    { timeoutMs: 180_000 },
  );
}

function upload(job: Job): JobFile {
  const f = job.files.find((x) => x.kind === "video" && x.status === "ok");
  if (!f) throw new Error(`job ${job.id} has no video`);
  return f;
}

function jobDir(id: string): string {
  return path.join(dataDir, "intake", "jobs", id);
}

// ---- What "the library is untouched" means ---------------------------------------------

async function sha256(p: string): Promise<string> {
  return createHash("sha256")
    .update(await readFile(p))
    .digest("hex");
}

/**
 * The film's file and Radarr's record of it, and of the film: what a failed or refused
 * flow 2 must leave as it was.
 */
async function library(movie: Movie) {
  const m = await radarr.movie(movie.id);
  const file = m.movieFile;
  if (!file) throw new Error("the film has no file");
  const st = await stat(file.path, { bigint: true });
  return {
    hash: await sha256(file.path),
    inode: { dev: st.dev, ino: st.ino, size: st.size, mtimeNs: st.mtimeNs },
    folder: (await readdir(m.path)).sort(),
    movie: {
      monitored: m.monitored,
      hasFile: m.hasFile,
      path: m.path,
    },
    record: {
      id: file.id,
      path: file.path,
      relativePath: file.relativePath,
      size: file.size,
      dateAdded: file.dateAdded,
      quality: file.quality?.quality.name,
      languages: file.languages?.map((l) => l.name),
    },
    files: (await radarr.movieFiles(movie.id)).map((f) => f.id),
  };
}

// ---- The pages ---------------------------------------------------------------------------

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

/** The match page for a film that already has a file: "Add another language" leads to flow 2. */
async function matchExistingThroughUI(page: Page, id: string) {
  const start = page.getByRole("link", { name: "Choose the film or show" });
  await expect(start).toBeVisible({ timeout: 180_000 });
  await start.click();
  await expect(page).toHaveURL(/\/match$/);
  await page.getByRole("radio", { name: "A film", exact: true }).check();
  await page.getByRole("textbox", { name: "Title" }).fill(fx.title);
  const lookup = page.waitForResponse((r) => new URL(r.url()).pathname.startsWith("/api/lookup/"), {
    timeout: 120_000,
  });
  await page.getByRole("button", { name: "Search" }).click();
  const res = await lookup;
  if (res.status() !== 200) {
    await outageOr("radarr", fx.tmdbId, "lookup", res.status(), await res.text());
  }
  await page
    .getByRole("radio", { name: new RegExp(`^${fx.title} \\(${fx.year}\\)`) })
    .first()
    .check();
  const matched = page.waitForResponse(
    (r) => new URL(r.url()).pathname === `/api/jobs/${id}/match` && r.request().method() === "POST",
    { timeout: 120_000 },
  );
  await page.getByRole("button", { name: "Add another language" }).click();
  const m = await matched;
  if (m.status() !== 200) {
    await outageOr("radarr", fx.tmdbId, "match", m.status(), await m.text());
  }
  await expect(page).toHaveURL(new RegExp(`/jobs/${id}/flow2$`));
}

/**
 * Make the clips on the flow 2 page with this offset, wait until the page shows them, and
 * measure each one (the clips the page's players load, fetched as the user).
 */
async function clipsAt(page: Page, offsetMs: number): Promise<number[]> {
  const offset = page.getByRole("textbox", { name: "Offset in milliseconds" });
  await offset.fill(String(offsetMs));
  await page.getByRole("button", { name: /^Make the clips/ }).click();
  await expect(page.getByText(`at an offset of ${offsetMs} ms`)).toBeVisible({ timeout: 240_000 });
  const players = page.getByRole("list", { name: "Clips" }).locator("video");
  await expect(players.first()).toBeVisible();
  const offsets: number[] = [];
  for (const src of await players.evaluateAll((v) => v.map((x) => x.getAttribute("src") ?? ""))) {
    const rep = await mediacheck.sync(await api.bytes(src));
    expect(rep.flashes, `${src}: flashes to measure`).toBeGreaterThanOrEqual(5);
    offsets.push(rep.offsetMs);
  }
  expect(offsets.length, "the preview's clips").toBeGreaterThanOrEqual(1);
  return offsets;
}

// ---- The fake indexer (B6) ---------------------------------------------------------------

/** A size Radarr accepts for the quality and runtime: the middle of its MB-per-minute range. */
async function releaseSize(quality: string, minutes: number): Promise<number> {
  const def = (await radarr.qualityDefinitions()).find((d) => d.quality.name === quality);
  if (!def) throw new Error(`Radarr has no quality ${quality}`);
  const hi = def.maxSize ?? 0;
  const perMinute = hi > def.minSize ? (def.minSize + hi) / 2 : def.minSize + 50;
  return Math.round(perMinute * minutes * 1024 * 1024);
}

/**
 * Run a Radarr command that may grab from the fake indexer: what it asked the indexer, and
 * whether it grabbed (an NZB in the blackhole, or a new grabbed event in the film's history).
 */
async function tryGrab(movieId: number, command: string) {
  await emptyBlackhole();
  // A queued copy of the release would block a new grab: make Radarr look at its queue now.
  await radarr.run({ name: "RefreshMonitoredDownloads" });
  await indexer.forgetRequests();
  const grabbed = async () =>
    (await radarr.history(movieId)).filter((h) => h.eventType === "grabbed").length;
  const before = await grabbed();
  const cmd = await radarr.run({ name: command });
  const nzbDir = path.join(dataDir, "blackhole", "radarr", "nzb");
  let nzbs: string[] = [];
  // The NZB is written before the command ends; the wait covers a slow disk.
  for (let i = 0; i < 12 && nzbs.length === 0; i++) {
    nzbs = await readdir(nzbDir).catch(() => []);
    if (nzbs.length === 0) await new Promise((r) => setTimeout(r, 250));
  }
  // The indexer answers null while it has seen no request.
  const requests = ((await indexer.requests()) ?? []).filter((r) => r.agent.startsWith("Radarr"));
  const result = {
    command,
    status: `${cmd.status} ${cmd.message ?? ""}`.trim(),
    requests: requests.map((r) => `${r.t}: ${r.returned.join(", ") || "none"}`),
    nzbs,
    newGrabbedEvents: (await grabbed()) - before,
  };
  console.log(`B6: ${JSON.stringify(result)}`);
  return result;
}

// ---- The merge task's process ------------------------------------------------------------

/**
 * In the worker container, wait for the merge's mkvmerge (the one writing an output, not an
 * identification), let it write `bytes` of its output, and kill it (SIGKILL), as a crash of
 * the tool halfway would. The mux takes a fraction of a second on these files, so every
 * mkvmerge is stopped (SIGSTOP) as soon as it is seen; an identification is let go on, and
 * the mux is let run in slices of half a millisecond until its output (fd 5: the worker
 * hands it the library file, the upload and the output as fds 3, 4 and 5) is that large.
 * Only shell builtins run while it runs. Ends when it has killed one or at the deadline.
 */
function killMux(bytes: number, deadlineSeconds = 240) {
  const script = `
exec 9< <(sleep ${deadlineSeconds + 10})  # a pipe that stays empty: read -t on it is a short wait
waiter=$!
trap 'kill "$waiter" 2>/dev/null' EXIT
end=$((SECONDS+${deadlineSeconds}))
while [ $SECONDS -lt $end ]; do
  for d in /proc/[0-9]*; do
    { read -r c < "$d/comm"; } 2>/dev/null || continue
    [ "$c" = mkvmerge ] || continue
    pid=\${d#/proc/}
    kill -STOP "$pid" 2>/dev/null || continue
    # The mux has the argument --output (identifications have --output-charset only).
    mapfile -d '' args < "$d/cmdline" 2>/dev/null
    mux=0
    for a in "\${args[@]}"; do [ "$a" = --output ] && mux=1; done
    if [ $mux = 0 ]; then kill -CONT "$pid"; continue; fi
    size=0
    while [ -d "$d" ] && [ $SECONDS -lt $end ]; do
      size=$(stat -Lc %s "$d/fd/5" 2>/dev/null || echo 0)
      [ "$size" -ge ${bytes} ] && break
      kill -CONT "$pid"; read -t 0.0005 -u 9; kill -STOP "$pid"
    done
    kill -KILL "$pid" && echo "killed mkvmerge $pid with $size bytes of output written" && exit 0
  done
done
echo "no mkvmerge writing an output within ${deadlineSeconds} s"
exit 1`;
  return stack.exec("fukku-worker", ["bash", "-c", script]);
}

// ---- The criteria ------------------------------------------------------------------------

test.describe("M3 exit criteria", { tag: "@online" }, () => {
  test.use({ actionTimeout: 60_000 });

  test("1. a matching dub ends as one file with tagged tracks, the film unmonitored, and RSS sync and the Wanted search do not replace it", async ({
    page,
  }) => {
    test.setTimeout(20 * 60_000);
    await needMetadata("radarr", fx.tmdbId);
    const movie = await test.step("the English film is in the library (flow 1)", filmInLibrary);
    const before = await library(movie);
    await loginAs(page, "user1");

    const id = await test.step("upload the Spanish dub through the upload page", () =>
      uploadThroughUI(page, [path.join(m3, fx.dub)]));
    await test.step("choose the film on the match page: another language of it", () =>
      matchExistingThroughUI(page, id));

    await test.step("the flow 2 page: the same cut, merge offered first", async () => {
      await expect(page.getByText("Same cut")).toBeVisible({ timeout: 180_000 });
      const job = await compared(id);
      expect(job.flow2?.finding).toBe("match");
      expect(job.flow2?.offers).toEqual(["merge", "version"]);
    });

    await test.step("the sync check: the clips of a matching dub are in sync", async () => {
      const offsets = await clipsAt(page, 0);
      console.log(`1: clips of the matching dub, audio against picture: ${offsets.join(", ")} ms`);
      for (const o of offsets)
        expect(Math.abs(o), `in sync: ${offsets}`).toBeLessThanOrEqual(inSyncMs);
      await page.getByRole("button", { name: "They match, continue" }).click();
    });

    const job = await api.job(id);
    const spanish = job.flow2?.upload?.audio[0];
    if (!spanish) throw new Error("the upload has no audio track");
    await test.step("confirm the merge; the page warns that the film's own Search can still replace it", async () => {
      // Shown before the merge is confirmed (#59, decision B6 on #61).
      await expect(page.getByText(/own Search button in Radarr can still replace/)).toBeVisible();
      await page.getByRole("checkbox", { name: "Confirm that you have read this" }).check();
      const confirmed = page.waitForResponse(
        (r) => new URL(r.url()).pathname === `/api/jobs/${id}/confirm`,
      );
      await page.getByRole("button", { name: "Merge into the library file", exact: true }).click();
      const res = await confirmed;
      expect(res.status(), await res.text()).toBe(202);
      const sent = res.request().postDataJSON() as {
        flow2: string;
        merge: Record<string, unknown>;
      };
      expect(sent.flow2).toBe("merge");
      expect(sent.merge).toMatchObject({ audioTracks: [spanish.index], offsetMs: 0 });
      await expect(page).toHaveURL(new RegExp(`/jobs/${id}$`));
    });

    const done = await api.until(id, "the merge's import", (j) => j.state === "done", {
      timeoutMs: 300_000,
    });
    expect(done.import?.merge).toMatchObject({
      state: "merged",
      languageTag: "es-419",
      trackName: "Español (Latino)",
      keepOriginalDefault: true,
    });

    // One file, in Radarr and on disk: the merged one, with every track tagged.
    const after = await library(movie);
    expect(after.files, "one file for the film in Radarr").toHaveLength(1);
    expect(after.record.id, "Radarr's record is a new file").not.toBe(before.record.id);
    expect(after.hash, "the file is not the one before").not.toBe(before.hash);
    expect(
      after.folder.filter((n) => n.endsWith(".mkv")),
      "one video in the film's folder",
    ).toEqual([path.basename(after.record.path)]);
    expect(after.record.quality, "the existing file's quality").toBe(before.record.quality);
    expect(after.record.languages).toEqual(["English", "Spanish (Latino)"]);
    const tracks = await mediacheck.tracks(after.record.path);
    const shown = tracks.map(({ type, properties: p }) => ({
      type,
      language: p.language,
      ietf: p.language_ietf,
      name: p.track_name,
      default: p.default_track,
    }));
    console.log(`1: the merged file's tracks: ${JSON.stringify(shown)}`);
    expect(tracks.map((t) => t.type)).toEqual(["video", "audio", "audio"]);
    expect(tracks[1]?.properties).toMatchObject({
      language: "eng",
      track_name: "English",
      default_track: true,
    });
    expect(tracks[2]?.properties).toMatchObject({
      language: "spa",
      language_ietf: "es-419",
      track_name: "Español (Latino)",
      default_track: false,
    });

    // Unmonitored (merge.unmonitorAfterMerge), and the result says what can still replace the
    // file. Soft, so that without the protection the grab below shows too.
    expect.soft((await radarr.movie(movie.id)).monitored, "the film is unmonitored").toBe(false);
    expect.soft(done.result?.unmonitorReason).toBe("merged");
    expect.soft(done.result?.merge?.warnings.map((w) => w.code)).toEqual(["search_can_replace"]);

    await test.step("a Bluray-1080p release without the dub: neither RSS sync nor the Wanted search grabs it", async () => {
      const minutes = movie.runtime && movie.runtime > 0 ? movie.runtime : 9;
      const title = `${fx.title.replace(/ /g, ".")}.${fx.year}.1080p.BluRay.x264-FUKKU`;
      await indexer.offer([
        { title, category: 2040, size: await releaseSize("Bluray-1080p", minutes) },
      ]);
      const rss = await tryGrab(movie.id, "RssSync");
      // Radarr saw the release (so "not grabbed" means something), and left it.
      expect(
        rss.requests.some((r) => r.includes(title)),
        "RSS sync saw the release",
      ).toBe(true);
      expect.soft(rss.nzbs, "RSS sync grabbed nothing").toEqual([]);
      expect.soft(rss.newGrabbedEvents, "no grabbed event after RSS sync").toBe(0);
      const wanted = await tryGrab(movie.id, "CutoffUnmetMoviesSearch");
      expect.soft(wanted.nzbs, "the Wanted (cutoff unmet) search grabbed nothing").toEqual([]);
      expect.soft(wanted.newGrabbedEvents, "no grabbed event after the Wanted search").toBe(0);
      const now = await library(movie);
      expect(now.record, "Radarr's file is still the merged one").toEqual(after.record);
      expect(now.hash).toBe(after.hash);
    });

    await test.step("the result page says the film is unmonitored, and that its own Search can still replace the file", async () => {
      await expect
        .soft(page.getByText("This film is not monitored"))
        .toBeVisible({ timeout: 60_000 });
      await expect
        .soft(page.getByText(/own Search button in Radarr can still replace it/).first())
        .toBeVisible();
    });
  });

  test("2. a dub 800 ms late is caught by the preview: its clips are out of sync, and in sync at -800 ms", async ({
    page,
  }) => {
    test.setTimeout(15 * 60_000);
    await needMetadata("radarr", fx.tmdbId);
    await test.step("the English film is in the library (flow 1)", filmInLibrary);
    const job = await test.step("upload the late dub and choose the film", () =>
      otherLanguage(fx.shifted));
    // The video is the same cut (the container is 0.8 s longer, its picture is not).
    expect(job.flow2?.finding).toBe("match");
    expect(job.flow2?.offers).toContain("merge");

    await loginAs(page, "user1");
    await page.goto(`/jobs/${job.id}/flow2`);
    await expect(page.getByText("Same cut")).toBeVisible({ timeout: 60_000 });
    const late = await test.step("the clips at offset 0 are out of sync", () => clipsAt(page, 0));
    console.log(
      `2: offset 0: audio against picture ${late.join(", ")} ms (800 ms late reads as -200)`,
    );
    for (const o of late)
      expect(Math.abs(o), `out of sync: ${late}`).toBeGreaterThanOrEqual(apartMs);

    const fixed = await test.step("the clips at -800 ms are in sync", () => clipsAt(page, -800));
    console.log(`2: offset -800: audio against picture ${fixed.join(", ")} ms`);
    for (const o of fixed) expect(Math.abs(o), `in sync: ${fixed}`).toBeLessThanOrEqual(inSyncMs);
    expect((await api.preview(job.id)).offsetMs).toBe(-800);
  });

  test("3. a PAL copy becomes a second version: outside Radarr's folder, Radarr's file untouched, grouped by Jellyfin", async () => {
    test.setTimeout(15 * 60_000);
    await needMetadata("radarr", fx.tmdbId);
    const movie = await filmInLibrary();
    const job = await otherLanguage(fx.pal);
    const f2 = job.flow2;
    // Decision #20 and plan §8: a PAL speed-up is offered as a second version, never merged.
    expect(f2?.finding).toBe("pal");
    expect(f2?.ratio ?? 0).toBeGreaterThan(1.04);
    expect(f2?.offers).toEqual(["version"]);
    expect(f2?.notOffered).toContainEqual({ offer: "merge", reason: "runtime_differs" });
    expect(f2?.version?.label).toBe("Español");
    const refused = await api.requestPreview(job.id);
    expect(refused.status, "no sync check for a PAL copy").toBe(409);
    expect(refused.body.error?.code).toBe("merge_not_offered");

    const before = await library(movie);
    const up = upload(job);
    const src = await stat(path.join(jobDir(job.id), up.path), { bigint: true });
    const label = `${f2?.version?.label} ${runTag}`;
    const c = await api.confirmFlow2(job.id, { flow2: "version", version: { label } });
    expect(c.status, JSON.stringify(c.body)).toBe(202);
    const done = await api.until(job.id, "the version", (j) => j.state === "done", {
      timeoutMs: 300_000,
    });
    const name = `${folderName()} - ${label}.mkv`;
    expect(done.result?.version).toMatchObject({ name, label });

    // The file: in versions.root, outside Radarr's root folder, the upload itself (a hard link).
    const placed = path.join(versionsRoot, folderName(), name);
    const at = await stat(placed, { bigint: true });
    expect({ dev: at.dev, ino: at.ino }, "a hard link of the upload").toEqual({
      dev: src.dev,
      ino: src.ino,
    });
    expect(placed.startsWith("/data/media/movies/")).toBe(false);
    // Radarr: the same file, record and folder, and no record of the version.
    expect(await library(movie), "Radarr's file and record are unchanged").toEqual(before);
    // Jellyfin groups the version with the film's file (MergeVersions).
    await expect
      .poll(
        async () => {
          const item = await jellyfin.itemAt(before.record.path);
          return item ? await jellyfin.sources(item.Id) : [];
        },
        { message: "Jellyfin lists the version with the film", timeout: 180_000 },
      )
      .toContain(placed);
    const { versions } = await admin.expect<{ versions: VersionRecord[] }>(
      "GET",
      "/api/admin/versions",
      200,
    );
    expect(versions.find((v) => v.jobId === job.id)).toMatchObject({
      app: "radarr",
      titleId: movie.id,
      path: placed,
      label,
      status: "grouped",
    });
  });

  test("4. a winning label is refused by the check of decision #20 (a forbidden token, a quality Radarr reads)", async () => {
    test.setTimeout(10 * 60_000);
    await needMetadata("radarr", fx.tmdbId);
    const movie = await filmInLibrary();
    const job = await otherLanguage(fx.pal);
    expect(job.flow2?.offers).toEqual(["version"]);
    const before = await library(movie);
    // "Screener" is no forbidden token, but Radarr reads a quality in the version's name.
    const parsed = await radarr.parse(`${folderName()} - Screener`);
    expect(parsed.quality, "Radarr reads a quality in the name").not.toBe("Unknown");
    for (const label of ["1080p", "Screener"]) {
      const c = await api.confirmFlow2(job.id, { flow2: "version", version: { label } });
      console.log(`4: label ${label}: ${c.status} ${JSON.stringify(c.body)}`);
      expect(c.status, `label ${label}: ${JSON.stringify(c.body)}`).toBe(409);
      expect(c.body.error?.code).toBe("version_label");
    }
    expect((await api.job(job.id)).state).toBe("awaiting-confirmation");
    // Nothing placed, no record, Radarr as before.
    expect(await readdir(path.join(versionsRoot, folderName())).catch(() => [])).toEqual([]);
    const { versions } = await admin.expect<{ versions: VersionRecord[] }>(
      "GET",
      "/api/admin/versions",
      200,
    );
    expect(versions.filter((v) => v.jobId === job.id)).toEqual([]);
    expect(await library(movie)).toEqual(before);
  });

  /**
   * Once the merge has come to an end, nothing was written: the library file (hash, inode,
   * modification time), Radarr's records of the film and its file, and the film's folder are
   * as before, Radarr got no ManualImport of the job's folder, and the job's merged/ and work/
   * hold no file. Then the job is back at the confirmation with the refusal (#95).
   */
  async function untouched(movie: Movie, before: Awaited<ReturnType<typeof library>>, id: string) {
    const back = await api.until(
      id,
      "the merge's end",
      (j) =>
        (j.state === "awaiting-confirmation" && !!j.flow2?.mergeRefusal) ||
        ["done", "failed", "cancelled"].includes(j.state),
      { timeoutMs: 300_000, stop: [] },
    );
    console.log(
      `5: the job is ${back.state} (${JSON.stringify(back.error)}), refusal ${JSON.stringify(back.flow2?.mergeRefusal)}`,
    );
    expect(await library(movie), "the library file and Radarr's record are unchanged").toEqual(
      before,
    );
    expect(back.state, "back at the confirmation").toBe("awaiting-confirmation");
    expect(await radarr.manualImports(jobDir(id)), "no ManualImport of the job").toEqual([]);
    for (const sub of ["merged", "work"]) {
      const left = await readdir(path.join(jobDir(id), sub), { recursive: true }).catch(
        () => [] as string[],
      );
      expect(
        left.filter((n) => n.endsWith(".mkv")),
        `no file left in ${sub}/`,
      ).toEqual([]);
    }
    return back;
  }

  test("5a. the merge task killed: the library file and Radarr's record are unchanged", async () => {
    test.setTimeout(15 * 60_000);
    await needMetadata("radarr", fx.tmdbId);
    const movie = await filmInLibrary();
    const job = await otherLanguage(fx.dub);
    const p = await api.requestPreview(job.id);
    expect(p.status, JSON.stringify(p.body)).toBe(202);
    await api.readyPreview(job.id);
    const before = await library(movie);

    // Killed halfway: about half of the merged file written (the film and the dub are some
    // 6.5 MB each, the merged file about 7).
    const killer = killMux(3 << 20);
    const c = await api.confirmFlow2(job.id, { flow2: "merge", merge: {} });
    expect(c.status, JSON.stringify(c.body)).toBe(202);
    const killed = await killer;
    console.log(`5a: ${killed.stdout.trim()} ${killed.stderr.trim()}`);
    expect(killed.code, killed.stdout + killed.stderr).toBe(0);
    const back = await untouched(movie, before, job.id);
    expect(back.flow2?.mergeRefusal?.code).toBe("mux_failed");
  });

  test("5b. a corrupt upload: the merge is refused, the library file and Radarr's record are unchanged", async () => {
    test.setTimeout(15 * 60_000);
    await needMetadata("radarr", fx.tmdbId);
    const movie = await filmInLibrary();
    const job = await otherLanguage(fx.dub);
    const p = await api.requestPreview(job.id);
    expect(p.status, JSON.stringify(p.body)).toBe(202);
    await api.readyPreview(job.id);
    const before = await library(movie);

    // The upload's middle overwritten with noise (2 MiB, the size unchanged), after the
    // sync check: what a disk error or a tampered file would give the merge.
    const file = path.join(jobDir(job.id), upload(job).path);
    const fh = await open(file, "r+");
    try {
      const { size } = await fh.stat();
      await fh.write(randomBytes(2 << 20), 0, 2 << 20, Math.floor(size / 2));
    } finally {
      await fh.close();
    }
    const c = await api.confirmFlow2(job.id, { flow2: "merge", merge: {} });
    expect(c.status, JSON.stringify(c.body)).toBe(202);
    const back = await untouched(movie, before, job.id);
    expect(["mux_failed", "verify_failed"]).toContain(back.flow2?.mergeRefusal?.code);
  });
});
