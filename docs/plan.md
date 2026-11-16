# Fukku product plan: self-hosted media intake for Radarr and Sonarr

**Fukku** (フック, "hook"). Written 2026-11-15 as a handoff for the session that creates the repository; decisions D1–D6 settled and M0 run on 2026-11-15 (§16, §18). Facts that M0's behaviour probes contradicted are corrected in place and marked *(corrected in M0)*; the design questions they raise are open in §18. Probe results: [compatibility.md](compatibility.md).

This document describes an independent, self-hostable product. It must not depend on, or mention, any particular home lab, network, hostname, identity provider or storage vendor. One real deployment (the "first adopter", described in generic terms in §4) drives the defaults and the first test cases.

---

## 1. Problem

People who run Radarr and Sonarr sometimes have video that no indexer or download client will ever provide: a home recording of a broadcast, a film bought on disc and ripped, an original-language or dubbed copy of a film already in the library, a zipped season from a friend. Today the only way in is a shell or a file manager: copy the file somewhere the *arr app can see, add the title with search off, open Manual Import, fix the mapping, and hope the media server notices.

Fukku is a small web app that does exactly that, safely, for non-technical users. It handles the upload, checks it, asks Radarr or Sonarr what they make of it, and lets the user confirm. Then Radarr or Sonarr imports it, so everything downstream (the media server, the subtitle manager, the request app) sees it like a normal download.

## 2. Goals and non-goals

**Goals**

1. A non-technical user can add a film or a season from a browser, on a phone or a laptop, including files of 20–80 GB over a slow or unreliable connection.
2. **Radarr and Sonarr stay the only writers of the library.** Fukku never renames or files media itself; it uses their Manual Import API, so their naming, quality, history and upgrade logic stay in charge.
3. Three use cases (§7): a new film; an extra-language copy of a film already in the library (merge the audio or add a second version); a season archive with an editable episode mapping.
4. Safe by construction with untrusted uploads: no path traversal, no archive bombs, no overwriting, no code execution, and the parsing process holds no credentials and has no network.
5. Every behaviour that one deployment might want differently is a **setting** with a sensible default (§11). New behaviours are added as settings, not forks.
6. Easy to run: one OCI image, a compose example, configuration by environment variables or one YAML file, works behind any reverse proxy.

**Non-goals (v1)**

- Not a file manager: no browsing, renaming, deleting or sharing of library files.
- Not a downloader or indexer, and not a replacement for the *arr apps' own UIs (they stay the fallback at every step).
- No user management of its own beyond what the chosen auth mode provides (§9).
- No music, books or other *arr apps in v1 (Lidarr and others are backlog).
- No transcoding of the library. ffmpeg is used only for probing and for short preview clips.

## 3. Glossary

| Term | Meaning |
| --- | --- |
| *arr | Radarr (films) and Sonarr (shows), v3 API |
| Job | One user submission: one or more uploaded files plus the decisions made about them, tracked as a state machine |
| Proposal | What Radarr or Sonarr's Manual Import endpoint suggests for the files of a job (title, episode, quality, languages, rejections) |
| Merge | Muxing extra audio (and subtitle) tracks from an upload into the existing library file, producing a new file that replaces it through Manual Import |
| Second version | Placing an upload next to the existing film or episode file so the media server shows a version picker, while the *arr app keeps tracking its own file |
| Media server | Jellyfin first; Plex and Emby later (backlog) |
| Subtitle manager | Bazarr (optional) |

## 4. Users and the first adopter

**Roles**

- *Uploader*: anyone allowed in. Uploads, follows their jobs, confirms imports.
- *Admin* (optional, by auth group or header value): sees all jobs, can retry or delete them, sees settings and health.
- A setting `auth.allUsersAreAdmins` covers small, trusted households where everyone already administers the *arr apps.

**First adopter profile (generic description of the deployment that drives the defaults)**

- A family household: everyone allowed in is trusted equally and already has admin access to Radarr and Sonarr.
- The app runs in Docker on the same host as Radarr, Sonarr, Bazarr and Jellyfin, and sits behind a reverse proxy with forward authentication. The proxy passes the user name, email and groups as headers and connects from one fixed, known address.
- The app has **no internet access** and should not need any: title lookups go through Radarr and Sonarr.
- Media lives on network storage (NFS) mounted on the host. Downloads, the library and the upload area are **one filesystem**, so moves are renames and imports are hardlinks.
- Radarr and Sonarr use TRaSH-style quality profiles with **upgrades allowed**. The film profile prefers the original language; the show profile scores "not original language" very negatively.
- The *arr apps have `importExtraFiles` off, no media-server connection and no recycle bin. The media server's real-time monitoring is off, and it scans the library every hour.
- Typical extra language: **Latin American Spanish** (`es-419`). The UI needs English and Spanish.
- Uploads come from home (fast LAN) and sometimes from outside through a tunnel with a 60 s request timeout at the proxy.

## 5. How it works

```text
Browser ──tus chunks──► web process ──spool task──► worker process (no network, no secrets)
   ▲                       │   ▲                        │ unzip · ffprobe · mkvmerge · preview clips
   │                       │   └──────result file───────┘
   │                       ├──► Radarr / Sonarr API (lookup, add, Manual Import proposal, import)
   │                       ├──► Bazarr API (subtitle upload, optional)
   └──── job status ◄──────└──► Media server API (refresh the changed path, optional)

Storage:  <intake root>/.incoming/<upload>   (tus, in progress)
          <intake root>/jobs/<job>/          (validated or extracted files, merge output)
          Radarr/Sonarr move files from jobs/ into the library themselves.
```

The web process holds the API keys and never opens media files. The worker opens untrusted files and holds nothing. They talk through task and result files in a local spool directory, so neither needs a network path to the other.

## 6. Integrations and their contracts

Versions checked so far: Radarr 6.4.4, Sonarr 4.0.20, Bazarr 1.6.2, Jellyfin 12.4.0 (image tag `12.4.20261026-211555`; there is no `12.4.0` tag). "Live" means a call made against a running instance; "spec" means it was read from the app's published OpenAPI or swagger, not called; "probe Cn/Jn" means an M0 probe in the local test environment exercised it ([compatibility.md](compatibility.md)).

| Step | Call | Evidence |
| --- | --- | --- |
| Find a film / show | `GET /api/v3/movie/lookup?term=` (also `tmdb:<id>`), `GET /api/v3/series/lookup?term=` (also `tvdb:<id>`) | live. Sonarr may miss original-language titles that Radarr finds; the UI must offer English-title and id search |
| Add without searching | `POST /api/v3/movie` with `addOptions.searchForMovie=false`; `POST /api/v3/series` with `addOptions.searchForMissingEpisodes=false`, `addOptions.monitor` | probe setup (every online probe adds its titles this way) |
| Proposal | `GET /api/v3/manualimport?folder=<job dir>&filterExistingFiles=false`, **without** a title id; then set the title per row with the re-check call below. *(Corrected in M0: with `movieId`, or `seriesId` + `seasonNumber`, both apps ignore `folder` and scan the title's own folder instead, probes C6 and C6-sonarr. `?movieId=` alone is useful for B3: it gives Radarr's verdict on files already in the film folder.)* | live on both; probe C6. Items carry `path`, `quality`, `languages`, `releaseGroup`, `customFormats`, `customFormatScore`, `indexerFlags`, `rejections`; Sonarr adds `episodes`, `seasonNumber` |
| Re-check edited rows | `POST /api/v3/manualimport` (reprocess) with edited items, including `movieId` (Sonarr: `seriesId`, `seasonNumber`, `episodeIds`: one id per row, or two for a double) | probe C2 (Radarr), C2-sonarr: a row moved by one episode and a row given two episodes come back with fresh decisions; a double imports as one file for both episodes (`S01E05-E06`) |
| Pickers in the mapping table | `GET /api/v3/qualitydefinition` (qualities), `GET /api/v3/language`, `GET /api/v3/episode?seriesId=` (episodes, expected runtime, `episodeFileId`) | spec (qualities, languages); the episode list is live. A Sonarr proposal item it could not match to a series carries no season or episodes, so Fukku reads `SxxEyy` / `NxNN` from the file name for those rows |
| Import | `POST /api/v3/command` `{name: "ManualImport", importMode: "move", files: [...]}`, poll `GET /api/v3/command/<id>` | probe C1: imports by rename (same inode) on one filesystem |
| Confirm | `GET /api/v3/movie/<id>` → `movieFile` (the tracked file), `GET /api/v3/episode?seriesId=` → `episodeFileId` | live. *(Corrected in M0: `/moviefile?movieId=` can list several records, B3, so its first entry is not the tracked file.)* `moviefile.mediaInfo.runTime` helps the runtime check |
| Busy check | `GET /api/v3/queue/details?movieId=` / `seriesId=` | live |
| Monitoring | `PUT /api/v3/movie/editor` `{movieIds, monitored}`; `PUT /api/v3/episode/monitor` `{episodeIds, monitored}`; season flag via `PUT /api/v3/series/<id>` | probe C3 (movie editor), C3-sonarr: `PUT /episode/monitor` changes exactly the listed episodes; setting a season's flag through `PUT /series/<id>` monitors every episode of that season |
| Name check | `GET /api/v3/parse?title=` returns quality, languages, custom formats and score for a file name | live (Radarr); probe C4 |
| Naming config | `GET /api/v3/config/naming` | live; probe C4 |
| Subtitles | Bazarr `POST /api/movies/subtitles?radarrid=&language=&forced=&hi=`, `POST /api/episodes/subtitles?seriesid=&episodeid=&language=&forced=&hi=`, multipart field `file`; all four query parameters required (400 otherwise); 204 with no body. *(Corrected in M2, #42: `forced` and `hi` are sent as lower-case `true`/`false`; the endpoint sets a flag only on the lower-case string, so `True` silently means false. A subtitle whose cues are in brackets is marked hearing-impaired by Bazarr itself, whatever `hi` says. Bazarr stores a film or episode once it has a file and knows it right after Radarr's or Sonarr's import event; an upload for a title it does not know is refused with 404. The write is a short queue job that can end "completed" without a file, so only the title's history proves it: `GET /api/movies/history?radarrid=` or `/api/episodes/history?episodeid=` gains an entry (`action` 4, `subtitles_path`) per upload. A repeated upload is safe: it rewrites the same file and adds another entry.)* **Hand-over (decision #23, `internal/subtitles`):** a post-processing step `subtitles`, run after the import when `subtitles.handOverUploads` is on. For each accepted subtitle file it finds the video with the same base name, reads the language and the forced / hearing-impaired flags from the name (`.es.srt`, `.es.forced.srt`, `.en.hi.srt`; the UI's choice wins), uploads, and waits up to `subtitles.confirmTimeout` for one more history entry for that language and forced state. If it does not show, it uploads again after `subtitles.retryBackoff`, doubling each time, up to `subtitles.attempts` uploads; a refused upload (an unknown title included) counts as an attempt, which covers the wait for Bazarr to learn the title, so no wait on its sync task is needed. The written file's name is not predicted (it carries `.hi` or `.forced` by Bazarr's rules). A file that was not written, or that has no video or no language, fails the step with its name; the job still ends `done`, because the import is not undone. `subtitles.provider: none` skips the step. The web process reads a subtitle file only here, only one the worker accepted, up to `subtitles.maxFileSize`, and passes the bytes on without parsing them. | probes C5, C5a-C5f (confirmed in #50 and #42) |
| Media server refresh | Jellyfin `POST /Library/Media/Updated` `{Updates: [{Path, UpdateType: "Created"}]}` with the path as the media server sees it; fallback `POST /Library/Refresh` | probe J1: works, but the item appears only after the server's `LibraryMonitorDelay` (60 s by default) |
| Version grouping (alt) | Jellyfin `POST /Videos/MergeVersions?ids=` | probe J2: the merge survives a full library scan and a full metadata refresh |

All integration code sits behind small interfaces (`MovieManager`, `SeriesManager`, `SubtitleManager`, `MediaServer`), so Plex, Emby or later API versions can be added without touching the flows.

## 7. Third-party behaviours that shape the design

These were found by reading the upstream source and checking a live instance. Each one becomes an automated **behaviour probe** in the test environment (§14), because an upgrade of the other app can change it.

| # | Behaviour | Consequence for Fukku |
| --- | --- | --- |
| B1 | **Jellyfin groups film versions** only when every video in the folder starts with the folder name and, after that prefix and Jellyfin's clean-string pass, the rest is empty, starts with `-`, `_` or `.`, or is a resolution such as `1080p`; all files must parse to the same year (`VideoListResolver.IsEligibleForMultiVersion`) | Radarr's default-style `{Movie Title} ({Release Year}) {Quality Full}` never groups. Fukku reads Radarr's naming config, explains the problem, and offers the fix (`… ({Release Year}) - {Quality Full}`) as an admin action; or uses `MergeVersions` if the setting chooses it |
| B2 | **Jellyfin 12.4 groups episode versions** in a Shows library by the strictly parsed `SxxEyy`, whatever follows it (`GetEpisodesGroupedByVersion`, with unit tests) | Second versions are possible for episodes too, with no naming change. Older Jellyfin versions do not do this: detect the server version and hide the option below 12.4 |
| B3 | **Radarr re-judges every untracked video in a film folder on each rescan** (`DiskScanService` → import decision with `newDownload=false`). If the upgrade check accepts it (better quality, or equal quality and a custom-format score at least as high), Radarr **adopts it as the film's file**, without moving or deleting anything | A second version must always lose to the tracked file. Rules: the version label never contains a quality or source token; before linking, Fukku asks Radarr for its verdict on the file (Manual Import proposal or `parse`) and refuses unless it is rejected as "not an upgrade". Sonarr is assumed to behave the same until a probe shows otherwise *(Corrected in M0, probe B3: Radarr 6.4.4 does not switch its tracked file on rescan. It registers every video in the film folder as an extra movie-file record, renames those records on a rename, and keeps them through a later upgrade import. Its proposal for a label file shows quality Unknown and no rejection, so the "not an upgrade" rule would refuse every Radarr version. Sonarr 4.0.20 behaves as described: it re-points the episode to an upgrade and rejects a label file as "not an upgrade", probe B3-sonarr. See §18.)* |
| B4 | Radarr's own filters skip `-other`, `-scene`, `-trailer`… files and `extras/`-style folders | Not usable for versions: Jellyfin treats those as extras, not versions |
| B5 | Radarr and Sonarr track existing subtitle files as extra files even with `importExtraFiles` off, and move them on rename | A naming change keeps the subtitles beside the video. Probes B5 and B5-sonarr: both apps rename the `.srt` with the video (Sonarr 4.0.20 has no `/extrafile` endpoint, so there the rename is the evidence) |
| B6 | With upgrades allowed, a merged file (original + dub) or a dub-only file can be replaced by a better release that lacks the dub | After a merge, and after any dub-only import, Fukku unmonitors the title by default (setting) and says why in the UI *(Corrected by probes B6 and B6-sonarr, with a fake indexer: unmonitoring stops the automatic paths only. An unmonitored film or episode is skipped by RSS sync, by the Wanted "cutoff unmet" search and, on Sonarr, by season and series searches, but the title's own search (Radarr `MoviesSearch`, Sonarr `EpisodeSearch`) still grabs the better release without the dub. A quality profile with upgrades not allowed stops that search too. See §18 item 6.)* |
| B7 | Radarr's profile language is used when choosing releases, not on import; custom-format scores (e.g. "not original language") are applied on import | Language rejections show up in proposals for Sonarr-style profiles; Fukku shows them and asks before overriding *(Corrected in M0, probe B7-sonarr: a −10000 "not original language" score below a minimum of 0 gives **no** rejection in a Sonarr Manual Import proposal. Fukku must read `customFormats` and `customFormatScore` itself and warn. Radarr behaves as described, probe B7-radarr, and detects Spanish from the audio tag alone.)* |
| B8 | Manual Import with an explicit title or episode id is expected to import even when the proposal lists rejections | **Unverified**. If false, the override flow changes |
| B9 | Many reverse proxies cap a request at 60 s (Traefik v3's default `readTimeout` covers the body), and some CDNs cap a body at 100 MB | Chunked uploads with a configurable chunk size (default 16 MiB), so each request is small and short |

## 8. The three flows

### Flow 1: a film the library does not have

1. Upload. The worker probes it: container, duration, resolution, video codec, audio and subtitle tracks with languages.
2. The user searches by title (or a TMDB id) and picks the film from Radarr's lookup. If the film is already in Radarr **with a file**, switch to flow 2.
3. Options: quality (pre-filled from resolution; source default from a setting, e.g. WEBDL), quality profile (default from a setting), **"replace when a better copy appears"** (monitored; default from a setting).
4. Add the film with search off (tag from a setting, e.g. `fukku`), get the proposal, show rejections, confirm, `ManualImport` (move), poll, confirm the file record.
5. Hand uploaded subtitles to the subtitle manager, refresh the media server path, show a link to the title (URL template setting).

### Flow 2: another language of a film already in the library

The worker compares the upload's duration with the library file's:

| Finding | Default action (thresholds are settings) |
| --- | --- |
| Within ±0.5 s | Offer **2a merge**, after the sync check |
| Ratio ≈ 25/23.976 (1.0427 ± 0.002) | PAL speed-up: **2b second version** (tempo correction is backlog) |
| Anything else | Different cut: **2b second version** |

**2a, merge.** Equal length does not prove sync. The worker renders N preview clips (default 3 × 20 s at start, middle and end: existing video at low resolution with the new audio) for the user to check, with an optional offset in ms (mkvmerge `--sync`). mkvmerge writes the existing file plus the chosen new tracks to `jobs/<job>/.partial.mkv`. Language tag and track name come from a setting (default `es-419`, "Español (Latino)"), and the original audio stays the default track (setting). Verification before import: exit code 0, same video stream and duration, track count = original + added. Then `ManualImport` with the film id, the **existing file's quality**, and both languages; Radarr replaces its file. Then unmonitor (B6).

**2b, second version.** Only if the media server supports film versions with the current naming (B1) or the merge-API strategy is on. The file is linked into the film folder as `<folder name> - <label>.<ext>`. The label comes from a setting (default the language's display name, e.g. `Español`) and is checked against B3's rules, including Radarr's verdict, before linking. Link, then rename into place, never overwriting (`link()` fails if the name exists). This is the one direct write into the library; it is off unless `versions.enabled` is set.

### Flow 3: a season archive (or loose episode files)

1. Upload a `.zip` (RAR and 7z are backlog) or several files. The worker extracts with the limits in §10 and probes each file.
2. Pick the show from Sonarr's lookup (if new: added with search off and nothing monitored; series type standard, anime or daily).
3. **Editable mapping table**: one row per file with Sonarr's proposal (season, episode or episodes, quality, language, rejections), the episode title and Sonarr's expected runtime next to the real duration. Per row: change season and episode, pick two episodes for a double, skip. Bulk: shift by N, set the season. Every edit is re-checked through Sonarr.
4. Hints: a duration about twice the expected one suggests a double episode (Sonarr says nothing about it: its proposal for a file of twice the runtime has no rejection and no runtime field, probe C7-sonarr, so the hint comes from Fukku's own duration); many mismatches suggest DVD order (Sonarr knows aired order only).
5. `ManualImport` (move) for the accepted rows, subtitles to the subtitle manager, media server refresh.


**A dub of a season already in the library.** `seasonDub.policy` setting, default `ask`:

| Policy | Result |
| --- | --- |
| `merge` | Per-episode merge as 2a, only where every duration matches (later milestone) |
| `version` | Per-episode second versions (B2; Jellyfin ≥ 12.4 and the B3 rules) |
| `replace` | Import the dub over the existing files and unmonitor the season |
| `subtitlesOnly` | Import nothing; point the user at the subtitle manager |
| `ask` | Show the options that apply, in that order of preference |

## 9. Architecture

| Part | Choice (recommendation; decision D3) | Why |
| --- | --- | --- |
| Language | **Go**, one static binary; the UI is a React single-page app (decision D6) built into the binary | `tusd` embeds as a library; the standard library covers HTTP, zip, JSON and `os.Root` (confined file access); small memory and patch surface. TypeScript on Node with `@tus/server` is the alternative |
| State | SQLite (pure-Go driver) on local disk, never on network storage | Jobs, uploads, audit log; one file to back up |
| Image | Debian slim base pinned by digest, with `mkvtoolnix` and `ffmpeg` from the distribution; non-root; multi-arch (amd64, arm64) | Hosts don't need the tools |
| Processes | `fukku web` and `fukku worker` from the same image. The reference compose file runs them as two containers, the worker with `network_mode: none`, read-only root, all capabilities dropped. A `mode: combined` setting runs both in one container for simple installs (documented as weaker) | Untrusted parsing has no network and no secrets |
| Web ↔ worker | Task and result files in a shared local spool directory; one task at a time (setting) | No network between them, restart-safe, easy to inspect |
| Jobs | Persisted state machine: `uploading → uploaded → probing → probed → matched → awaiting-confirmation → importing → imported → post-processing → done`, plus `failed`, `stalled` and `cancelled`. Every step can be re-run and is idempotent (the command id is stored; "imported" is decided by asking the *arr app, never assumed) | Crashes and restarts end in `done` without duplicates |
| Paths | **Path mapping** settings: how Fukku, Radarr, Sonarr, the subtitle manager and the media server each see the intake root and the library roots (like the *arr "remote path mappings") | The apps rarely see the same paths |
| Filesystem checks | At start-up and before each import: the intake root and each library root exist, are writable or readable as needed, and **share a filesystem** (same device id) when `importMode=move`; optional sentinel file per mount (setting) that must exist, so an unmounted network share is detected | Moves stay renames; nothing is written onto a host disk under a missing mount |

## 10. Upload and file safety

| Topic | Default (all are settings) |
| --- | --- |
| Protocol | tus 1.0 with creation, termination and expiration; client retry and resume after a reload |
| Chunk size | 16 MiB per `PATCH`, sequential |
| Auth expiry mid-upload | A `302`/`401` on a chunk pauses and shows "sign in again"; resume from the stored offset |
| Limits | 80 GiB per file; 300 GiB in flight in total; 3 parallel uploads per user; refuse when free space < size + 100 GiB |
| Accepted types | Video `.mkv .mp4 .m4v .avi .mov .ts .webm`; subtitles `.srt .ass .ssa`; archives `.zip` |
| Retention | Idle upload deleted after 48 h; job awaiting a decision deleted after 14 days (date shown from day one); startup sweep of unknown `.incoming` files, repeated every 15 min; the records of `done` and `cancelled` jobs are deleted after 30 days, audit entries are kept |
| Zip-slip | Reject `..`, absolute paths, drive letters and backslashes; extract through `os.Root` under the job directory; NFC-normalise names, strip control characters, max 200 bytes |
| Entry types | Regular files and directories only; everything else is skipped and listed |
| Bombs | ≤ 500 entries; bytes counted **as written**; stop at 200 GiB, at the free-space margin, or at a written/compressed ratio > 20; never open nested archives |
| Content check | Extension allow-list, then ffprobe must find a video stream with a duration (worker, 60 s deadline) |
| Overwrites | Never. Library changes go through the *arr apps; the one direct write (2b) is link-then-rename with no replace; merge output is renamed only after verification |
| Concurrency | One active job per (app, title, season); refuse while the *arr queue has an active download for the title |

## 11. Settings catalogue (initial)

Configuration comes from a YAML file plus environment overrides (`FUKKU_…`, with `_FILE` variants for secrets). The full catalogue, with the settings M0 added (data directory, `mode`, name length, stuck-job threshold, heartbeat, double-episode ratio, large-file hint, request timeouts), is [settings.md](settings.md), kept in step with the code by a test. Settings that are safe to change at runtime can later get an admin page; v1 can be file-only. Each setting has a default suited to the first adopter, and the README documents all of them.

| Group | Settings (default) |
| --- | --- |
| `server` | listen address; public base URL; UI languages (`en`, `es`); log format (`logfmt` \| `json`); metrics on/off |
| `auth` | `mode`: `proxyHeaders` (default) \| `oidc` \| `none` (dev only, refuses to start unless `--insecure-dev`); `trustedProxies` (CIDRs, required for `proxyHeaders`); header names for user, email, groups; admin group; `allUsersAreAdmins` (false); Origin check (on) |
| `radarr` / `sonarr` | URL; API key; default quality profile; default root folder; tag for added titles (`fukku`); default "monitor after import" (true for flow 1); `importMode` (`move` \| `copy`) |
| `subtitles` | provider (`bazarr` \| `none`); URL; API key; hand uploaded subtitles over (true) |
| `mediaServer` | provider (`jellyfin` \| `none`; later `plex`, `emby`); URL; API key; refresh method (`pathUpdate` \| `libraryScan` \| `none`); title link template |
| `paths` | intake root; path mappings per app; sentinel files; required same-filesystem check (on when `importMode=move`) |
| `upload` | chunk size; per-file limit; total in-flight limit; per-user parallel uploads; free-space margin; accepted extensions; idle-upload expiry; job retention |
| `extract` | max entries; max written bytes; max ratio; allowed archive types |
| `match` | runtime tolerance (0.5 s); PAL ratio and tolerance; default source when the name has none (`WEBDL`) |
| `merge` | enabled (true); preview clip count and length; default language tag and track name; keep the original audio as default (true); unmonitor after merge (true) |
| `versions` | enabled (false); strategy (`naming` \| `mergeApi`); label template (`{languageName}`); forbidden label tokens (quality and source words); require the *arr verdict "not an upgrade" (true) |
| `seasonDub` | policy (`ask`) and the order of options |
| `worker` | concurrency (1); CPU and time limits per task; spool directory |

## 12. Security model

- **Threats:** a hostile or malformed upload; a stolen user session; a neighbouring service on the same network calling the app; a cross-site POST from the user's browser.
- **Controls:**
  - Only the worker parses media, with no network and no secrets.
  - In `proxyHeaders` mode, requests are accepted only from `trustedProxies` and only with a user header; everything else gets 403.
  - State-changing requests check `Origin`, and no endpoint uses GET to change state.
  - No file browsing and no arbitrary paths in the API: jobs are addressed by id.
  - Secrets only from the environment or files, never logged.
  - Container runs as non-root with a read-only root filesystem.
- **Audit log:** who uploaded, confirmed, overrode a rejection, merged, replaced or deleted. Written to the database and to stdout as structured lines.
- **Accepted:** the web process holds *arr API keys that can delete library files. That is inherent to driving them, and it is why it never parses uploads.

## 13. Observability and operations

- `/healthz` (process up), `/readyz` (database, spool writable, sentinels present, intake root writable, same filesystem, worker heartbeat younger than 2 min: 503 when one fails; Radarr and Sonarr reachable: reported, and a failure only makes the status `degraded` with 200). The container `HEALTHCHECK` uses local checks only (`/readyz?local=1`, which leaves the *arr checks out), so a reverse proxy that stops routing to unhealthy containers keeps serving the UI while Radarr or Sonarr is down.
- Structured log events with stable names (`upload_completed`, `job_state`, `import_submitted`, `job_stuck` once a minute while a job has been failed or stalled for more than 1 h).
- Optional Prometheus `/metrics`: uploads in flight, bytes, job states, worker task durations.
- Backup: the SQLite file (documented online-copy command). Uploads in progress are not backed up.

## 14. Testing strategy and test environment

1. **Unit tests**: extraction limits, name normalisation, the state machine, duration matching, label rules.
2. **Hostile-archive corpus, generated at test time** (never committed as files): path traversal, absolute paths, symlinks, device entries, 1000:1 bomb, 501 entries, nested zip, renamed non-video.
3. **Media fixtures generated with ffmpeg at test time**: short test-pattern videos with tagged audio tracks (eng, spa), a PAL-speed copy, a copy with the audio shifted by 800 ms, a different cut (trimmed), a broken file.
4. **Integration environment** (`docker compose` under `test/env/`): Radarr, Sonarr, Bazarr and Jellyfin at pinned versions, pre-seeded configs with known API keys and root folders, one shared volume as the "library filesystem", and a fake Newznab indexer built from the repository with blackhole download clients, so a probe can offer generated releases and see what is grabbed (nothing is downloaded). Lookups and adds need the *arr metadata servers, so tests that add titles are tagged `online`; the rest run offline.
5. **Behaviour probes** (one test per row of §7, runnable against any version): B1 film grouping with old and new naming, B2 episode grouping, B3 Radarr (and Sonarr) adoption with a losing name and a winning name, B4 extras in Radarr and Jellyfin, B5 subtitles following a rename, B6 the upgrade decision over a merged file (and, through a fake indexer and a blackhole download client, RSS and searches with the title monitored and not), B7 language handling on import, B8 Manual Import with rejections, the §6 contracts (C1–C6), plus `Library/Media/Updated` (J1) and `MergeVersions` persistence across rescans (J2). B9 concerns reverse proxies and is covered by M1's upload tests. Their output is a compatibility table published with each release ([compatibility.md](compatibility.md)).
6. **End-to-end**: Playwright through the UI for the three flows, including a reload mid-upload, a container restart mid-import, and a session expiry mid-upload.
7. CI: lint, unit, offline integration, image build, SBOM; `online` tests on a schedule.

## 15. Milestones (each ends in exit criteria)

| Milestone | Scope | Exit criteria |
| --- | --- | --- |
| **M0 Foundations** | Repo, licence, CI, image build, config loading and validation, `/healthz`, test environment, fixture generators, behaviour probes B1–B8 | Probes run green or red in CI against the pinned versions and their results are written down; every "unverified" row in §6 is confirmed or the plan is corrected |
| **M1 Upload and extract** | tus upload, worker, probing, safe extraction, job list, retention, audit log, `proxyHeaders` auth, Origin check. A finished job shows its files and a link to the *arr Manual Import screen (manual fallback) | 60 GB upload survives a reload, a container restart and a dropped connection (simulated); the hostile corpus is refused with nothing written outside the job directory; the worker cannot reach the network; requests from an untrusted address get 403; missing sentinel → not ready |
| **M2 Flow 1 and flow 3** | Lookup, add, proposal, mapping editor with re-check, import, confirmation, subtitles, media server refresh | A new film arrives by rename (same inode) and is visible in the media server within a minute; a season zip with a double episode and a misnumbered file imports correctly after editing; a kill during `importing` ends in `done` without duplicates |
| **M3 Flow 2** | Runtime comparison, preview clips, merge, unmonitor, second versions (behind `versions.enabled`), season dub policies `replace`, `subtitlesOnly`, `version` | A matching dub ends as one file with correctly tagged tracks and the title unmonitored, and a forced search does not replace it; a shifted dub is caught by the preview; a PAL copy becomes a second version; a winning label is refused by the B3 check; a failed mux leaves the library untouched |
| **M4 Release** | README, configuration reference, reverse-proxy examples (generic, plus forward-auth header setup), compose example, threat model, compatibility table, Spanish UI strings, v0.1.0 tag | A clean install from the README works on a fresh host |

Backlog (each as a setting when built): `oidc` auth mode, per-episode merge, RAR/7z, automatic offset detection by audio cross-correlation, PAL tempo correction, Plex and Emby, Lidarr, a settings page, notifications (webhook) when a job needs a decision, multiple Radarr/Sonarr instances (e.g. 4K), a separate-library strategy for dubbed shows.

## 16. Decisions for the first session

Settled on 2026-11-15.

| # | Decision | Recommendation | Decided |
| --- | --- | --- | --- |
| D1 | Product name and repository name | Short, unclaimed on GitHub and Docker Hub/GHCR, does not use "arr" (trademark-adjacent) | **Fukku**, repository `segun-io/fukku` (the `fukku` names on GitHub and Docker Hub are held by inactive accounts; Fukku publishes under its organisation) |
| D2 | Licence | AGPL-3.0 (same family as most of the ecosystem) or MIT/Apache-2.0 if wider reuse matters more | **AGPL-3.0** |
| D3 | Language | Go (§9); TypeScript only if the maintainers prefer it | **Go** (1.27), tools in a pinned development image |
| D4 | Repository host and visibility | Private GitHub repository to start; public at M4 at the earliest, after a secrets and naming review | **GitHub, private** (not created yet) |
| D5 | Image registry | GHCR, images pulled by digest; signed with cosign (keyless) once public | **GHCR** (`ghcr.io/segun-io/fukku`) |
| D6 | UI approach | Server-rendered HTML + small vanilla JS (recommended), or a small SPA if the flows need it | **React single-page app**: Vite, TypeScript, shadcn/ui, TanStack Router, Query and Table, `tus-js-client`; built into the Go binary, no Node at runtime. The flows (resumable uploads, the mapping editor with per-edit re-checks) need it |

## 17. Risks

1. **New code handling untrusted files, exposed to a network.** Mitigated by the process split, limits, probes and a narrow API. A security review is due before the first public release.
2. **Behaviours, not contracts** (§7). Mitigated by probes run against every new upstream version and a published compatibility table.
3. **Merges delete the previous file** when the *arr recycle bin is off. Mitigated by verification and by keeping the upload until the job is `done`; document enabling the recycle bin on the same filesystem.
4. **Unmonitored titles stop improving.** The tag added by Fukku makes them easy to list; the UI says so.
5. **Sync is judged by a person.** A drifting dub can slip through; the fix is to redo it as a second version.
6. **Large uploads through tunnels and relays** cost transfer and time; the UI can show a "large files: upload from home" hint (setting).

## 18. M0 findings and open questions (2026-11-15)

M0's behaviour probes ran against Radarr 6.4.4, Sonarr 4.0.20, Bazarr 1.6.2 and Jellyfin 12.4.0 in the local test environment. Full results are in [compatibility.md](compatibility.md). Facts are corrected in place above; the design questions below need a decision before the milestone named.

1. **Second versions of films (flow 2b, B3), before M3.** Radarr 6.4.4 takes every video in a film folder as an extra movie-file record. It does not adopt them as the film's file, but it owns them: it renames them on a rename, and its proposal for a label file shows no "not an upgrade" rejection. The B3 rule as written ("refuse unless Radarr rejects it as not an upgrade") would therefore refuse every Radarr version. Still to probe before M3: what Radarr does with the extra records when the film is deleted, re-searched or renamed when no name collides, and whether a label file keeps its name. Options to weigh: rely on Jellyfin's `MergeVersions` (J2: it survives rescans) with the version kept outside the film folder, or accept Radarr owning the file. `versions.enabled` stays off by default meanwhile.
2. **Language warning for shows (B7), before M2.** Sonarr does not reject a not-original-language episode, even at −10000 against a minimum of 0. The mapping table must flag the "not original language" custom format and the score itself.
3. **Proposal scoping (§6), M2.** Proposals are fetched for the job folder without a title id, then each row is re-checked with the title chosen in the UI. This is how the apps' own interactive import works.
4. **Media server refresh (J1), M2 exit criterion.** `Library/Media/Updated` works, but the item appears only after the server's `LibraryMonitorDelay` (60 s by default), so "visible within a minute" holds only if that delay is lowered. Options: document lowering the delay, refresh the parent folder item directly, or relax the criterion to "within the server's monitor delay plus a minute".
5. **Subtitle hand-over (C5), M2. Decided (#23), built in #42.** Bazarr writes uploads from a short background job. The delay seen in M0 did not reproduce (8 of 8 uploads written within a second; the cause was the probe's bracketed cues, which Bazarr names `.hi.srt`). Fukku uploads with lower-case `true`/`false`, confirms through the title's history (a finished queue job is not proof), retries with backoff, and fails the step visibly. See §6, Subtitles.
6. **B6 (differs, #24), before M3's exit tests (#61).** Manual Import accepts a better release without the dub over a merged file, so the risk is real. With a fake indexer and a blackhole download client (probes B6, B6-sonarr): an unmonitored film or episode is **not** upgraded by RSS sync or the Wanted "cutoff unmet" search (nor, on Sonarr, by a season or series search), so unmonitoring stays the default. But the title's own search (the film's or the episode's Search button) **does** grab the release without the dub, so M3's exit criterion "a forced search does not replace it" does not hold with unmonitoring alone. What stopped that search in the probes is a quality profile with upgrades not allowed. Recommendation: keep unmonitoring as the default, reword the criterion to what it guarantees (RSS and the bulk searches), and have the UI say that the title's own search still upgrades it. Switching the title to a no-upgrades profile (an opt-in setting) is the only measure found that also stops that search, but it changes the user's profiles. The maintainer decides in #61.
7. **Merge with mkvmerge, M3.** The fixtures carry a once-a-second flash and beep, so the preview clips can be judged by eye and ear. The merge itself is not exercised until M3.

