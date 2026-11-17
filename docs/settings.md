# Settings

Every behaviour that one deployment might want differently is a setting with a default
(plan §11). Settings come from, in increasing order of precedence:

1. the defaults below;
2. a YAML file, given with `--config <path>` or `FUKKU_CONFIG`; keys are the dotted names
   below, nested (`server.listenAddress` is `server:` → `listenAddress:`); unknown keys are
   an error;
3. environment variables, one per setting, named in the second column. Unknown `FUKKU_*`
   variables are an error, so typos are caught.

Value formats:

- **Lists** of words: comma separated in the environment (`en,es`), YAML lists in the file.
- **Path mappings**: a YAML list of `{from, to}` in the file, or the same as JSON in the
  environment: `[{"from":"/data/intake","to":"/downloads/intake"}]`. `from` is the path as
  Fukku sees it, `to` the path as that app sees it.
- **Sizes**: bytes or a suffix, `16MiB`, `80GiB`, `500MB`.
- **Durations**: `500ms`, `60s`, `2m`, `1h`, `14d`.
- **Secrets** (API keys): never printed or logged. Each also accepts `<NAME>_FILE`, the path
  of a file holding the value (for container secrets). Give secrets to the web process
  only; the worker refuses to start if it is given any.

`fukku check-config` loads and validates a configuration and prints it with secrets
redacted (`--role worker` validates it as the worker will: it must hold no secret). A test
keeps this table and the code in step.

What most installs set, and the rest left at its default ([install.md](install.md) has the
steps): `server.publicBaseUrl`, `auth.trustedProxies`, `auth.adminGroup` (or
`auth.allUsersAreAdmins`), `radarr.*` and `sonarr.*` (`url`, `apiKey`, `rootFolder`,
`qualityProfile`), `paths.libraryRoots`, `paths.sentinels`, and, when the apps see the data under
other paths, `paths.mappings.*`. Add `subtitles.*` and `mediaServer.*` for Bazarr and Jellyfin.
Turn on `versions.enabled` (with `versions.root`) only when you want second versions: it is the
one setting that makes the web container write into the library, so its mount of the library
must be writable (films' versions go to `versions.root`; episodes' to Sonarr's season folders,
which must be on the intake root's mount and writable by the web user).

Administrators see the same effective settings, each marked default or set, on the `/admin`
page (`GET /api/admin/settings`), next to the server's health (`GET /api/admin/status`).
The page is read-only: nothing changes a setting at runtime, and secrets are never sent.
Others get 404 on both endpoints.

## Process

| Setting | Environment | Default | Description |
| --- | --- | --- | --- |
| `mode` | `FUKKU_MODE` | `split` | `split`: web and worker run as two processes (`fukku web`, `fukku worker`), the worker with no network. `combined`: one process (`fukku combined`) for simple installs; weaker, because the parser then shares the web process's network and secrets. The setting and the command must agree: `web` and `worker` refuse `combined`, and `fukku combined` refuses `split` |

## server

| Setting | Environment | Default | Description |
| --- | --- | --- | --- |
| `server.listenAddress` | `FUKKU_SERVER_LISTEN_ADDRESS` | `:8080` | Address the web process listens on |
| `server.publicBaseUrl` | `FUKKU_SERVER_PUBLIC_BASE_URL` | (empty) | External URL users open, e.g. `https://fukku.example.com`. Used for the Origin check (only this origin, scheme included, may change state) and links; empty means the request's own `Host` (then the proxy must pass the original `Host` through) |
| `server.uiLanguages` | `FUKKU_SERVER_UI_LANGUAGES` | `en, es` | UI languages offered; the first is the fallback |
| `server.logFormat` | `FUKKU_SERVER_LOG_FORMAT` | `logfmt` | `logfmt` or `json` |
| `server.metrics` | `FUKKU_SERVER_METRICS` | `false` | Serve Prometheus metrics on `/metrics` (web process; 404 when off). Same access rule as `/readyz`: a loopback address needs no user, anyone else must be an admin through the proxy. Series, with no user, file or title in any label: `fukku_uploads_in_flight`, `fukku_uploads_in_flight_bytes`, `fukku_upload_received_bytes_total`, `fukku_jobs{state}`, `fukku_task_duration_seconds{kind,outcome}` (histogram), `fukku_imports_total{app,outcome}` (outcome `submitted`, `imported` or `failed`), `fukku_ready{check}`. Counters and the histogram restart at 0 with the process |
| `server.dataDir` | `FUKKU_SERVER_DATA_DIR` | `/var/lib/fukku` | Database (`fukku.db`) and local state. Must be local disk, never network storage (SQLite). The web process refuses to start when it is not writable, when another process has it open (lock file `fukku.lock`), or when it is on a network filesystem. That check is best effort, by filesystem type, on Linux: NFS, SMB/CIFS, AFS, Coda, NCP, Ceph, 9p (also WSL's Windows drives), GPFS and Lustre are refused; FUSE is allowed, since it is mostly local. Backup: [backup.md](backup.md) |
| `server.jobStuckAfter` | `FUKKU_SERVER_JOB_STUCK_AFTER` | `1h` | A failed or stalled job older than this logs `job_stuck` once a minute. An importing job whose Manual Import command has not ended after this long (the app keeps it queued or started) can be cancelled by an admin; before that nobody can cancel it. That cancellation is audited as `import.abandoned` with the app and the command id, because the app may still finish the import afterwards. This applies once the import is submitted. A merge job (flow 2a) whose import is not submitted yet (the merge is still running, stalled or failed) is cancelled by its owner or an admin at any time, as `job.cancelled`: nothing was sent to the app, so the merge task, `merged/` and the partial file are removed and the library is untouched. |
| `server.readinessCacheTtl` | `FUKKU_SERVER_READINESS_CACHE_TTL` | `15s` | How long `/readyz` reuses the answer of the Radarr and Sonarr checks, so polling does not hit them every time. `0` turns the cache off |
| `server.arrStallBackoff` | `FUKKU_SERVER_ARR_STALL_BACKOFF` | `30s` | A job stalled with `arr_unavailable` (Radarr or Sonarr did not answer: no connection, a timeout, a 5xx, 401, 403 or 429) tries again once the app answers its status call and this long has passed; the wait doubles with each further stall, up to 16 times |
| `server.arrStallLimit` | `FUKKU_SERVER_ARR_STALL_LIMIT` | `5` | After this many stalls in a row in one state (the app answers its status call but keeps failing the real one, e.g. a proposal scan that times out), the job fails instead, so it does not cycle for ever; the user can retry it. While an import may be running, an app that does not answer only stalls the job (it is not given up on halfway), and only refusals count (an answer such as 404 after the title was deleted): this many in a row fail it, saying so. A poll the app answers between two refusals starts the count again, so occasional refusals while the command runs never add up. An admin can cancel a job stalled while importing (`import.abandoned`) |
| `server.importPollInterval` | `FUKKU_SERVER_IMPORT_POLL_INTERVAL` | `2s` | How often an import is looked at while it runs: Fukku asks Radarr or Sonarr whether the files arrived and polls the Manual Import command |
| `server.postStepAttempts` | `FUKKU_SERVER_POST_STEP_ATTEMPTS` | `5` | Runs of a post-processing step (media server refresh, subtitle hand-over) before it is recorded as failed; the job still ends `done`. A step that retries by itself (the subtitle hand-over, `subtitles.attempts`) gives up after one run, so its retries are not multiplied |
| `server.postStepBackoff` | `FUKKU_SERVER_POST_STEP_BACKOFF` | `30s` | Wait after a post-processing step's first failed run; doubles for each further one, up to 16 times |

## auth

In `proxyHeaders` mode the reverse proxy signs users in and passes who they are in headers.
Fukku accepts a request only when the connection comes from `trustedProxies` and carries the
user header; everything else gets 403 and is logged as `auth_refused` with the reason and the
remote address. The proxy must remove these headers from what clients send and set them
itself. Two paths need no user: `GET /healthz` from anywhere and `GET /readyz` from a loopback
address (the container's health check); `/readyz` from elsewhere needs an admin. `GET /metrics`
(when `server.metrics` is on) follows the same rule as `/readyz`. With `none`
every request is the admin user `dev`, and the UI shows a warning banner.

| Setting | Environment | Default | Description |
| --- | --- | --- | --- |
| `auth.mode` | `FUKKU_AUTH_MODE` | `proxyHeaders` | `proxyHeaders`: a reverse proxy authenticates users and passes headers. `oidc`: backlog. `none`: development only, refuses to start without `--insecure-dev` |
| `auth.trustedProxies` | `FUKKU_AUTH_TRUSTED_PROXIES` | (empty) | Addresses or CIDRs the reverse proxy connects from. Required for `proxyHeaders`; requests from anywhere else get 403. Only the connection's own address counts, never `X-Forwarded-For` or similar; an IPv4-mapped IPv6 address matches its IPv4 entry. `GET /readyz` is open to loopback addresses without a user, so if the proxy runs on the same host and connects from loopback, anyone it lets through can read `/readyz` and `/metrics` |
| `auth.userHeader` | `FUKKU_AUTH_USER_HEADER` | `Remote-User` | Header with the user name. Requests without a usable one get 403: missing, empty, repeated, longer than 256 bytes, not UTF-8 or with control characters |
| `auth.emailHeader` | `FUKKU_AUTH_EMAIL_HEADER` | `Remote-Email` | Header with the user's email (optional, shown in the audit log). When sent, the same rules as the user header apply; set it empty to read no such header |
| `auth.groupsHeader` | `FUKKU_AUTH_GROUPS_HEADER` | `Remote-Groups` | Header with the user's groups (optional). Repeated or with control characters gets 403; set it empty to read no such header |
| `auth.groupsSeparator` | `FUKKU_AUTH_GROUPS_SEPARATOR` | `,` | Separator between groups in the groups header |
| `auth.adminGroup` | `FUKKU_AUTH_ADMIN_GROUP` | (empty) | Members of this group are admins. Empty means no admins unless `allUsersAreAdmins` |
| `auth.allUsersAreAdmins` | `FUKKU_AUTH_ALL_USERS_ARE_ADMINS` | `false` | Every signed-in user is an admin (small, trusted households) |
| `auth.originCheck` | `FUKKU_AUTH_ORIGIN_CHECK` | `true` | Every method but GET, HEAD and OPTIONS must carry an `Origin` equal to the origin of `server.publicBaseUrl`, or, when that is empty, whose host equals the request's `Host`. Missing or `null` gets 403 `origin_mismatch` |

## radarr and sonarr

Each app is enabled when its `url` is set; at least one must be. The two groups have the
same settings.

| Setting | Environment | Default | Description |
| --- | --- | --- | --- |
| `radarr.url` | `FUKKU_RADARR_URL` | (empty) | Base URL, e.g. `http://radarr:7878` |
| `radarr.publicUrl` | `FUKKU_RADARR_PUBLIC_URL` | (empty) | Address of Radarr that a browser can open, e.g. `https://radarr.example.com`; `radarr.url` is for the server and may not open in a browser. A finished job links to its Manual Import page (`/wanted/missing`). Empty means no link, only the folder to copy |
| `radarr.apiKey` | `FUKKU_RADARR_API_KEY` | (empty) | API key (secret) |
| `radarr.qualityProfile` | `FUKKU_RADARR_QUALITY_PROFILE` | (empty) | Quality profile name for added films; empty means the user picks |
| `radarr.rootFolder` | `FUKKU_RADARR_ROOT_FOLDER` | (empty) | Root folder for added films, as Radarr sees it; empty means the first one |
| `radarr.tag` | `FUKKU_RADARR_TAG` | `fukku` | Tag put on titles Fukku adds, so they are easy to list |
| `radarr.monitorAfterImport` | `FUKKU_RADARR_MONITOR_AFTER_IMPORT` | `true` | Default of "replace when a better copy appears" for a new film (flow 1) |
| `radarr.importMode` | `FUKKU_RADARR_IMPORT_MODE` | `move` | `move` or `copy` for Manual Import. A row counts as imported when the app tracks a new file for it of the job's file's size; with `move` the job's file must also be gone (a size the app reports as 0 is then accepted), with `copy` a size of 0 is not enough and an import without a recorded baseline (submitted by an earlier version) counts nothing as imported |
| `radarr.minimumAvailability` | `FUKKU_RADARR_MINIMUM_AVAILABILITY` | `released` | When a film Fukku adds counts as available for searches: `announced`, `inCinemas` or `released` |
| `radarr.requestTimeout` | `FUKKU_RADARR_REQUEST_TIMEOUT` | `30s` | Timeout of one API request |
| `sonarr.url` | `FUKKU_SONARR_URL` | (empty) | Base URL, e.g. `http://sonarr:8989` |
| `sonarr.publicUrl` | `FUKKU_SONARR_PUBLIC_URL` | (empty) | Address of Sonarr that a browser can open, e.g. `https://sonarr.example.com`; `sonarr.url` is for the server and may not open in a browser. A finished job links to its Manual Import page (`/wanted/missing`). Empty means no link, only the folder to copy |
| `sonarr.apiKey` | `FUKKU_SONARR_API_KEY` | (empty) | API key (secret) |
| `sonarr.qualityProfile` | `FUKKU_SONARR_QUALITY_PROFILE` | (empty) | Quality profile name for added shows; empty means the user picks |
| `sonarr.rootFolder` | `FUKKU_SONARR_ROOT_FOLDER` | (empty) | Root folder for added shows, as Sonarr sees it; empty means the first one |
| `sonarr.tag` | `FUKKU_SONARR_TAG` | `fukku` | Tag put on shows Fukku adds |
| `sonarr.monitorAfterImport` | `FUKKU_SONARR_MONITOR_AFTER_IMPORT` | `false` | New shows are added with nothing monitored (flow 3) |
| `sonarr.importMode` | `FUKKU_SONARR_IMPORT_MODE` | `move` | `move` or `copy` for Manual Import. A row counts as imported when the app tracks a new file for it of the job's file's size; with `move` the job's file must also be gone (a size the app reports as 0 is then accepted), with `copy` a size of 0 is not enough and an import without a recorded baseline (submitted by an earlier version) counts nothing as imported |
| `sonarr.minimumAvailability` | `FUKKU_SONARR_MINIMUM_AVAILABILITY` | `released` | Not used: Sonarr has no equivalent. Kept because both apps share one settings type |
| `sonarr.requestTimeout` | `FUKKU_SONARR_REQUEST_TIMEOUT` | `30s` | Timeout of one API request |

## subtitles

| Setting | Environment | Default | Description |
| --- | --- | --- | --- |
| `subtitles.provider` | `FUKKU_SUBTITLES_PROVIDER` | `none` | `bazarr` or `none` |
| `subtitles.url` | `FUKKU_SUBTITLES_URL` | (empty) | Base URL, e.g. `http://bazarr:6767` |
| `subtitles.apiKey` | `FUKKU_SUBTITLES_API_KEY` | (empty) | API key (secret) |
| `subtitles.handOverUploads` | `FUKKU_SUBTITLES_HAND_OVER_UPLOADS` | `true` | Give uploaded subtitle files to the subtitle manager after import |
| `subtitles.requestTimeout` | `FUKKU_SUBTITLES_REQUEST_TIMEOUT` | `30s` | Timeout of one API request |
| `subtitles.attempts` | `FUKKU_SUBTITLES_ATTEMPTS` | `4` | Uploads of one subtitle file before the hand-over gives up and the step fails |
| `subtitles.confirmTimeout` | `FUKKU_SUBTITLES_CONFIRM_TIMEOUT` | `20s` | How long to wait after an upload for the subtitle manager's history to show it written |
| `subtitles.pollInterval` | `FUKKU_SUBTITLES_POLL_INTERVAL` | `1s` | How often the history is read while waiting |
| `subtitles.retryBackoff` | `FUKKU_SUBTITLES_RETRY_BACKOFF` | `5s` | Wait before the second upload of a file; doubles for each further one |
| `subtitles.maxFileSize` | `FUKKU_SUBTITLES_MAX_FILE_SIZE` | `2MiB` | Largest subtitle file the hand-over reads |

## mediaServer

| Setting | Environment | Default | Description |
| --- | --- | --- | --- |
| `mediaServer.provider` | `FUKKU_MEDIA_SERVER_PROVIDER` | `none` | `jellyfin` or `none` (Plex and Emby are backlog) |
| `mediaServer.url` | `FUKKU_MEDIA_SERVER_URL` | (empty) | Base URL, e.g. `http://jellyfin:8096` |
| `mediaServer.apiKey` | `FUKKU_MEDIA_SERVER_API_KEY` | (empty) | API key (secret) |
| `mediaServer.refreshMethod` | `FUKKU_MEDIA_SERVER_REFRESH_METHOD` | `libraryRefresh` | `libraryRefresh` (refresh the library item that holds the path; the new title is visible in under a second, decision #22), `pathUpdate` (report the path to `/Library/Media/Updated`; the server shows it only after its `LibraryMonitorDelay`, 60 s by default, so lower that delay on the server or raise `mediaServer.itemWait`), `libraryScan` (full scan) or `none` |
| `mediaServer.refreshFallback` | `FUKKU_MEDIA_SERVER_REFRESH_FALLBACK` | `true` | When the title is still not visible after `mediaServer.itemWait`, ask the server to scan every library on it (`POST /Library/Refresh`, not only the title's) and wait another `mediaServer.itemWait`. Needed for a library the server has never scanned with content in it (a brand-new Shows library), where `libraryRefresh` and `pathUpdate` find nothing new. It runs once per attempt of the step (the step is retried, `postStepAttempts` times), and is skipped while the server's scan task is already running; if the scan request fails, the step fails with the not-visible error and the reason. Logged as the event `media_server_fallback_scan` and named in the job's step detail. Not used with `libraryScan` (already a scan) or `none` |
| `mediaServer.titleLinkTemplate` | `FUKKU_MEDIA_SERVER_TITLE_LINK_TEMPLATE` | (empty) | Link shown after import, e.g. `https://media.example.com/web/#/details?id={itemId}`; empty means no link |
| `mediaServer.requestTimeout` | `FUKKU_MEDIA_SERVER_REQUEST_TIMEOUT` | `30s` | Timeout of one API request |
| `mediaServer.itemWait` | `FUKKU_MEDIA_SERVER_ITEM_WAIT` | `2m` | How long the `mediaServerRefresh` step waits for the new title to appear before it gives up on the link (the job still ends `done`). With `mediaServer.refreshFallback` the step can take up to twice this |
| `mediaServer.itemPollInterval` | `FUKKU_MEDIA_SERVER_ITEM_POLL_INTERVAL` | `2s` | How often the step asks the server whether the title is there |

## paths

| Setting | Environment | Default | Description |
| --- | --- | --- | --- |
| `paths.intakeRoot` | `FUKKU_PATHS_INTAKE_ROOT` | `/data/intake` | Where uploads (`.incoming/`) and jobs (`jobs/`) live. Must be on the library filesystem when `importMode=move` |
| `paths.libraryRoots` | `FUKKU_PATHS_LIBRARY_ROOTS` | (empty) | Library root folders as Fukku sees them; used for the same-filesystem check and by the worker to read a film's library file in flow 2 (the runtime comparison): the worker reads a library file only when its path, as `paths.mappings.radarr` maps Radarr's, is a clean path inside one of these roots, and only through a read-only handle confined to that root (no `..`, no link out of it). A merge (flow 2a) reads the library file the same way and never writes it: its output is a new file in the job directory. With none set, a flow 2 job fails with `library_unreadable`. Second versions of episodes (#58) are the one write the web process makes under these roots: a hard link into Sonarr's season folder, through a handle confined to the root |
| `paths.mappings.radarr` | `FUKKU_PATHS_MAPPINGS_RADARR` | (empty) | How Radarr sees Fukku's paths; empty means the same paths |
| `paths.mappings.sonarr` | `FUKKU_PATHS_MAPPINGS_SONARR` | (empty) | How Sonarr sees Fukku's paths |
| `paths.mappings.subtitles` | `FUKKU_PATHS_MAPPINGS_SUBTITLES` | (empty) | How the subtitle manager sees Fukku's paths |
| `paths.mappings.mediaServer` | `FUKKU_PATHS_MAPPINGS_MEDIA_SERVER` | (empty) | How the media server sees Fukku's paths |
| `paths.sentinels` | `FUKKU_PATHS_SENTINELS` | (empty) | Files that must exist (one per mount), so an unmounted network share is detected and nothing is written under it |
| `paths.requireSameFilesystem` | `FUKKU_PATHS_REQUIRE_SAME_FILESYSTEM` | `auto` | `auto` (required when an import mode is `move`), `always` or `never`: the intake root and library roots must share a device id |

## upload

| Setting | Environment | Default | Description |
| --- | --- | --- | --- |
| `upload.chunkSize` | `FUKKU_UPLOAD_CHUNK_SIZE` | `16MiB` | Size of one upload request. Keep each request well under the proxy's timeout and body limit |
| `upload.maxFileSize` | `FUKKU_UPLOAD_MAX_FILE_SIZE` | `80GiB` | Largest single file |
| `upload.maxInFlight` | `FUKKU_UPLOAD_MAX_IN_FLIGHT` | `300GiB` | Total size of all unfinished uploads |
| `upload.maxParallelPerUser` | `FUKKU_UPLOAD_MAX_PARALLEL_PER_USER` | `3` | Uploads one user may run at once |
| `upload.freeSpaceMargin` | `FUKKU_UPLOAD_FREE_SPACE_MARGIN` | `100GiB` | Refuse an upload when free space is below its size plus this; an extraction stops when free space falls below this, and a merge does not start unless the job's filesystem has room for about the library file plus the added tracks, plus this (measured on Linux, where the worker runs; elsewhere the extraction and merge checks always pass) |
| `upload.videoExtensions` | `FUKKU_UPLOAD_VIDEO_EXTENSIONS` | `.mkv, .mp4, .m4v, .avi, .mov, .ts, .webm` | Accepted video files |
| `upload.subtitleExtensions` | `FUKKU_UPLOAD_SUBTITLE_EXTENSIONS` | `.srt, .ass, .ssa` | Accepted subtitle files |
| `upload.archiveExtensions` | `FUKKU_UPLOAD_ARCHIVE_EXTENSIONS` | `.zip` | Accepted archives |
| `upload.idleUploadExpiry` | `FUKKU_UPLOAD_IDLE_UPLOAD_EXPIRY` | `2d` | An unfinished upload with no progress for this long is deleted |
| `upload.requestIdleTimeout` | `FUKKU_UPLOAD_REQUEST_IDLE_TIMEOUT` | `30s` | An upload request that sends no data for this long is closed, so a slow or vanished client cannot hold the server; the bytes received so far are kept and the upload continues from there |
| `upload.jobRetention` | `FUKKU_UPLOAD_JOB_RETENTION` | `14d` | A job waiting for a decision is deleted after this; the date is shown from day one. A merge refused for a reason that will fail again (`verify_failed`, `mux_failed`, `track_not_found`) puts the job back to waiting for a decision, and its date then becomes the later of the old one and this long from now, so that it is not deleted at once |
| `upload.finishedJobRetention` | `FUKKU_UPLOAD_FINISHED_JOB_RETENTION` | `30d` | The record of a `done` or `cancelled` job is deleted after this (its audit entries are kept) |
| `upload.sweepInterval` | `FUKKU_UPLOAD_SWEEP_INTERVAL` | `15m` | How often the retention sweep runs (it also runs once at start-up) |
| `upload.largeFileHintThreshold` | `FUKKU_UPLOAD_LARGE_FILE_HINT_THRESHOLD` | `20GiB` | Files above this show the hint "large files: upload from home if you can"; `0` turns it off |

## extract

| Setting | Environment | Default | Description |
| --- | --- | --- | --- |
| `extract.maxEntries` | `FUKKU_EXTRACT_MAX_ENTRIES` | `500` | Most entries in one archive, and most files and directories one extraction may create |
| `extract.maxWrittenBytes` | `FUKKU_EXTRACT_MAX_WRITTEN_BYTES` | `200GiB` | Stop when this much has been written (counted as written, not as declared; each directory created counts as 4 KiB) |
| `extract.maxRatio` | `FUKKU_EXTRACT_MAX_RATIO` | `20` | Stop when written bytes exceed compressed bytes by this factor |
| `extract.ratioFloor` | `FUKKU_EXTRACT_RATIO_FLOOR` | `1MiB` | The ratio limit applies only once this much has been written, so a small archive of text, which compresses well, is not refused; `0` applies it from the first byte |
| `extract.maxNameBytes` | `FUKKU_EXTRACT_MAX_NAME_BYTES` | `200` | Longest file name, in bytes after normalisation |
| `extract.maxPathSegments` | `FUKKU_EXTRACT_MAX_PATH_SEGMENTS` | `32` | Most directory levels plus the file name in one entry's path; deeper entries are skipped as `name_too_long` |
| `extract.maxPathBytes` | `FUKKU_EXTRACT_MAX_PATH_BYTES` | `1024` | Longest path of one entry, in bytes after normalisation; longer entries are skipped as `name_too_long` |
| `extract.allowedTypes` | `FUKKU_EXTRACT_ALLOWED_TYPES` | `zip` | Archive formats (RAR and 7z are backlog). Nested archives are never opened |

## match

| Setting | Environment | Default | Description |
| --- | --- | --- | --- |
| `match.runtimeTolerance` | `FUKKU_MATCH_RUNTIME_TOLERANCE` | `500ms` | Flow 2: an upload whose duration is within this of the library file's (inclusive) is the same cut, and a merge is offered. Both durations are the worker's ffprobe of the video stream (of the container when a file does not give one), never the app's rounded runtime |
| `match.palRatio` | `FUKKU_MATCH_PAL_RATIO` | `1.0427` | Flow 2: duration ratio (longer over shorter) of a PAL speed-up (25 / 23.976); such an upload is offered as a second version, and anything that is neither the same cut nor a PAL speed-up as a different cut |
| `match.palTolerance` | `FUKKU_MATCH_PAL_TOLERANCE` | `0.002` | Tolerance around the PAL ratio |
| `match.defaultSource` | `FUKKU_MATCH_DEFAULT_SOURCE` | `WEBDL` | Source assumed when the file name has none: `WEBDL`, `WEBRIP`, `BLURAY`, `HDTV` or `DVD` |
| `match.doubleEpisodeRatio` | `FUKKU_MATCH_DOUBLE_EPISODE_RATIO` | `2` | A file this many times the expected runtime is hinted as a double episode |
| `match.doubleEpisodeTolerance` | `FUKKU_MATCH_DOUBLE_EPISODE_TOLERANCE` | `0.15` | Relative tolerance around that ratio |
| `match.lookupCacheTtl` | `FUKKU_MATCH_LOOKUP_CACHE_TTL` | `5m` | How long the title search (`/api/lookup/…`) reuses an answer of Radarr or Sonarr for the same search term. `0` turns the cache off |
| `match.runtimeMismatchTolerance` | `FUKKU_MATCH_RUNTIME_MISMATCH_TOLERANCE` | `0.25` | A file whose duration is further than this, relative, from its episodes' expected runtime gets the warning `runtime_mismatch` (unless it looks like a double episode). Sonarr's runtimes are whole minutes and often rounded, so keep some room |
| `match.languageWarning` | `FUKKU_MATCH_LANGUAGE_WARNING` | `true` | A proposal row whose custom-format score is below its quality profile's `minFormatScore` gets the warning `language`, shown with the matching custom formats and the score, and needs an explicit override to be imported (audit entry `rejection.overridden`). Sonarr and Radarr give no rejection for a dub, even at −10000 against a minimum of 0, so this is the only signal. `false` turns the check off |
| `match.dvdOrderShare` | `FUKKU_MATCH_DVD_ORDER_SHARE` | `0.5` | When more than this share of a season's comparable rows (at least two) are runtime mismatches, the proposal hints that the files may be in DVD order (Sonarr knows aired order only) |

## merge

| Setting | Environment | Default | Description |
| --- | --- | --- | --- |
| `merge.enabled` | `FUKKU_MERGE_ENABLED` | `true` | Offer merging a new language into the existing file (flow 2a) |
| `merge.previewClips` | `FUKKU_MERGE_PREVIEW_CLIPS` | `3` | Sync-check clips (start, middle, end; 1 to 10): the library file's video with the upload's audio, spread evenly from its start to its end (one clip: the middle) |
| `merge.previewClipLength` | `FUKKU_MERGE_PREVIEW_CLIP_LENGTH` | `20s` | Length of each clip, at most `2m`; a film shorter than one clip gets one clip of its whole length |
| `merge.previewHeight` | `FUKKU_MERGE_PREVIEW_HEIGHT` | `360` | Height of the clips in pixels (even, 144 to 2160); smaller videos are not scaled up. Higher costs the worker more CPU per clip |
| `merge.previewVideoBitrate` | `FUKKU_MERGE_PREVIEW_VIDEO_BITRATE` | `800` | Cap of the clips' video bit rate, in kbit/s (100 to 20000); the audio is AAC at 96 kbit/s. A clip that grows past twice what the rates allow, plus 1 MiB, is stopped and refused (`too_large`) |
| `merge.previewTimeout` | `FUKKU_MERGE_PREVIEW_TIMEOUT` | `5m` | Deadline of rendering one clip (the worker's ffmpeg run, at `worker.nice`); ffmpeg is killed at it and the preview fails with `timeout`. Decoding a large library file (4K HEVC, say) on a small CPU is the slow part |
| `merge.languageTag` | `FUKKU_MERGE_LANGUAGE_TAG` | `es-419` | Default language tag of the tracks a merge adds; a merge task may name another. It must be BCP 47 in canonical form, the one mkvmerge keeps: `es-419`, `pt-BR`, `he` (not `iw`), `yue` (not `zh-yue`), never `en_US`; the error names the canonical form. The merge is verified only when every added track carries it |
| `merge.trackName` | `FUKKU_MERGE_TRACK_NAME` | `Español (Latino)` | Default name of the tracks a merge adds (audio and subtitles); a merge task may name another. At most 256 bytes, no control characters; empty adds them unnamed |
| `merge.keepOriginalDefault` | `FUKKU_MERGE_KEEP_ORIGINAL_DEFAULT` | `true` | The original audio stays the default track: the library file's default flags are kept (a file in another container than Matroska states none, so its first video and audio track are made the default unless ffprobe reports others) and no added track is default. `false` makes the first added audio track the default instead, and the library file's audio tracks lose the flag. A merge task may choose otherwise |
| `merge.coverageTolerance` | `FUKKU_MERGE_COVERAGE_TOLERANCE` | `3s` | How far each audio track a merge adds may fall short of the film, by the merged file's own timestamps (more than 0, at most `1m`): it must start no later than its start in the upload (after the offset) and end no earlier than the film's end (the video's end time or, when it ends sooner, the original audio's; a negative offset moves that end earlier by the offset) and than the end its DURATION tag in the upload states (shifted by the offset), each within this. A dub cut short, such as a truncated file whose header still states the full length, is refused (`verify_failed`) |
| `merge.unmonitorAfterMerge` | `FUKKU_MERGE_UNMONITOR_AFTER_MERGE` | `true` | Unmonitor the film once Radarr has imported the merged file, so that RSS sync and the Wanted searches do not replace it with a release without the dub (B6). The film's own Search in Radarr still can; the job's result says so (`search_can_replace`). Off: the film's monitoring is left as it was, and the result warns that any upgrade can replace the file (`upgrade_can_replace`) |
| `merge.unmonitorAfterDubOnlyImport` | `FUKKU_MERGE_UNMONITOR_AFTER_DUB_ONLY_IMPORT` | `true` | Same after importing a file that only has the dub |

When the merge tool refuses a merge for a reason that will fail again with the same choice
(`verify_failed`, `mux_failed`, `track_not_found`), the job goes back to waiting for a
confirmation instead of failing: the page shows why next to the sync check, and the user
picks another offset or track and confirms again. Refusals that may pass (free space, an
unreadable file, a busy destination) still fail the job, to be retried.

A merge (flow 2a) never writes the library file: the worker writes the merged file into the
job's directory (`merged/<attempt>/`, under the library file's name with `.mkv`), and Radarr
replaces its file with it through Manual Import (with `radarr.importMode` `move` it takes
the merged file away; the upload stays in the job until the job is removed). Each merged
file is a full copy of the film: one that is not imported (a refused or failed merge, a job
that goes back to the match, an earlier attempt's) is removed as soon as its task has ended,
and whatever is left in `merged/` (with `copy`, the imported copy) once the job is done. One
already verified stays while the job fails after it, for the retry to import. Radarr
deletes the file it replaces unless its recycle bin is on: set Radarr's recycle bin
(Settings, Media Management) to a folder on the library's filesystem to keep the previous
file for a while. At the confirmation, before the merge and again before the import,
Radarr's file for the film must still be the one of the match (its movie-file id, size and
path); if it is not, the job goes back to the match and nothing is written. The merged file
gets the existing file's quality and its languages plus Radarr's language for the tag:
`es-419` and the Spanish of any Latin American country (`es-MX`, `es-AR`, …, UN M.49 region
419) are Spanish (Latino), `pt-BR` is Portuguese (Brazil), `nl-BE` Flemish, and any other tag
its base language's English name (`es`, `es-ES`, `es-US`: Spanish; `pt-PT`: Portuguese). A
tag Radarr has no language for leaves the existing languages only, and the job's result
says so (`language_unknown_to_radarr`).

## versions

A second version of a film (flow 2b, decision #20) is the upload kept **outside Radarr's
root folders**, in `versions.root`, as `<versions.root>/<film folder>/<film folder> - <label>.<ext>`
(the film folder is the name of Radarr's folder for the film). Radarr never sees it, so it never
renames, upgrades or deletes it (probes B3e, B3g: a version inside the film folder can become
the film's file on an upgrade). Jellyfin groups it with the film's file through its merge-versions
call, so second versions of films need `mediaServer.provider: jellyfin`; with `none` they are not
offered (`versions_unsupported`). Jellyfin loses that grouping when Radarr renames or upgrades
the film's file, so Fukku checks every version on a schedule and after each of its own imports,
and groups it again with the film's current file. These checks run whatever
`versions.enabled` says, so versions placed before it was turned off keep their grouping. A film
deleted in Radarr leaves its version on disk: Fukku marks it `film_deleted`, and a version whose
file is gone `missing`; both are checked again every six `versions.regroupInterval` (at least
hourly) and come back by themselves when the film or the file does. Neither status is decided
from a passing fault: while a `paths.sentinels` file or the version's folder is missing, a check
decides nothing (the record keeps its status, its detail says why), and a 404 for the film counts
only when Radarr's own status call answers, asked again for each decision. Each version keeps its
film's TMDB id: when Radarr's movie id now answers with another film's (a restored or rebuilt
Radarr, or another instance), the version is never grouped with it and is treated like a deleted
film. A film deleted and added back to Radarr gets a new id: the version follows it by its TMDB
id (audit `version.reattached`). A version whose placement was cut short by a crash, and whose
job then ended, is settled by the next check: linked and grouped when the file is the job's
upload, otherwise its record is removed (so the name is not refused for ever).

**Episodes** (#58, the season-dub policy `version`) follow the naming plan instead: the dub of an
episode that already has a file is placed **next to that file**, in Sonarr's season folder, as
`<series folder> - S01E02 - <label>.<ext>` (the series folder is the name of Sonarr's folder for the
show). Jellyfin 12.4 groups the two as versions of one episode by the `SxxEyy` (probe B2), so no
merge call is needed and Sonarr's renames and upgrades of the episode's own file do not undo it
(probe M3-episode-versions). Sonarr does not track the
version: it never renames it, an upgrade replaces only Sonarr's own file, and deleting the series
with its files deletes the version too. On each rescan Sonarr judges the version against the
episode's file and would adopt it if it were an upgrade (probe B3-sonarr), so before each version
is linked Sonarr is asked for its verdict on the file under its version name
(`versions.requireNotUpgradeVerdict`), and if the episode's own file is deleted, Sonarr takes the
version for the episode's file on its next rescan. Episodes' versions are offered when
`versions.enabled` is on and the media server is Jellyfin 12.4 or later (Fukku reads its version);
`versions.root` is not used for them. They need the season folders **writable by the web process
and on the same mount as the intake root** (each confirmation checks every season folder by the
same trial link), and inside `paths.libraryRoots` as `paths.mappings.sonarr` maps Sonarr's paths.
The job's subtitle files for those episodes are not handed over (the subtitle manager knows only
Sonarr's file), and Sonarr's monitoring of them is left as it is.

Sonarr can still adopt a placed version later: when the episode's own file is deleted, when a
worse file replaces it, or when a change of the quality profile or its custom formats makes the
version win. So every `versions.regroupInterval` the keeper re-checks each episode's version
against Sonarr (#96), whatever `versions.enabled` says, and records what it finds as the version's
status, with an audit entry and a log event when it changes; admins see them in
`GET /api/admin/versions`. In that order:

- The series first: Sonarr's series id must still be the version's show. Each record keeps the
  show's TVDB id (from the confirmation, or learnt on the first check). A 404 for the series, or
  a series with another TVDB id (a restored or rebuilt Sonarr, another instance), is looked for
  again by the TVDB id (`GET /series?tvdbId=`): a show deleted and added back under a new id is
  followed (audit `version.reattached`). The episode is found by its id, else by its season and
  number (Sonarr gives an episode a new id when its metadata is rebuilt; `version.reattached`
  too). The job's result still finds a record that followed its version or its episode.
- `series_deleted`: none of that finds the show or the episode. The version, if still on disk,
  is left there (remove it by hand).
- `series_moved`: Sonarr's folder for the series is elsewhere and the version is not in it.
  Sonarr moves the whole folder when it moves a series with its files, the version with it: then
  Fukku's record follows the version to its new path (audit `version.followed`) and the check
  goes on there. A version left at the old path (the series moved without its files) is
  `series_moved`.
- `missing`: the version file is gone.
- `adopted`: Sonarr tracks the version as an episode's file. Sonarr now renames, upgrades and
  deletes it as its own; the detail says whether the episode's earlier file is still on disk.
- `episode_file_missing`: Sonarr lists no file for the episode, or its file is not on disk.
  Sonarr's next rescan takes the version for the episode's file: import the episode's file
  through Sonarr first to keep the version a second version.
- `adoptable`: with `versions.requireNotUpgradeVerdict`, Sonarr's verdict on the version, asked
  of the file where it is (Manual Import's proposal of the season folder with
  `filterExistingFiles=true`, re-checked for the episode, the check made before it was placed),
  no longer rejects it as not an upgrade: its next rescan takes the version for the episode's
  file. The verdict is asked when the episode's file changed, every six intervals (at least
  hourly), and on every pass while the version is `adoptable`; once per season folder and pass.
  A version stays `adoptable` until a verdict rejects it again: Sonarr giving no verdict, or the
  call failing, changes nothing. A verdict call that fails does not stop the check (the version
  is grouped or waiting as usual, its detail says the verdict was not asked) and backs off by
  season folder: 1, 2, 4… passes, up to the hourly recheck.

Otherwise the version is `grouped` once Jellyfin lists both files as one episode, or `waiting`,
with the same refresh and backoff as films' versions (Jellyfin 12.4.0 was seen to leave a version
removed and placed again at the same path unlisted for minutes). Fukku never deletes, moves,
renames or places a library file for any of these, and never asks Sonarr to rescan. A status
that ends with the problem solved goes back to `grouped` (audit `version.recovered`); `adopted`,
`series_deleted` and `missing` are checked again every six intervals (at least hourly), the
others on every pass (`series_moved` too: a move in Sonarr may be under way). As for films,
nothing is decided from a passing fault: while a `paths.sentinels` file, the library root
holding the version (`paths.libraryRoots`) or Sonarr's folder for the series (as
`paths.mappings.sonarr` maps it) is missing, or Sonarr does not answer, the version keeps its
status and its detail says why, and `series_deleted`, `series_moved` and `adopted` are decided
only when Sonarr's own status call answers, asked again for each decision. While Sonarr does not
answer, `version_check_failed` is logged once (and `version_check_recovered` when it answers
again); other failures are logged once per version until they change. Sonarr's slow calls are
made before the version's lock is taken, and checks of films nudged after Fukku's imports run
between the checks of a long pass. A version whose job is still importing or post-processing,
or stalled there, is left to the job's step, which writes a version only under its lock and
never over a status of the re-check.

The file is placed by a hard link from the job's folder, which never replaces an existing file,
so `versions.root` must be on **the same mount** as the intake root and writable by the web
process. Each confirmation checks this by trial: it links an empty file from the intake root into
`versions.root` and removes both; trial files named `.fukku-linkcheck-…` that a crash left behind
are removed by the next check once they are five minutes old. (Two bind mounts of one host folder report the same device id,
yet a link between them fails, so comparing device ids is not enough. The move-import check of
`paths.requireSameFilesystem` stays a device comparison: those moves are Radarr's and Sonarr's,
through their own mounts.) `versions.root` must also be a folder of a Jellyfin Movies library (as
`paths.mappings.mediaServer` maps it).
In probe M3-versions the first version in a folder just added to the library was not found
after the library refresh, only after the fallback scan (`mediaServer.refreshFallback`), so that
first job waits `mediaServer.itemWait`; scanning the library once after adding the folder avoids it.

`versions.strategy` (`naming` or `mergeApi`) was removed in #56: films always use the merge
outside the film folder, and episodes naming. A configuration that still sets it fails to load
(unknown key, or unknown variable `FUKKU_VERSIONS_STRATEGY`).

| Setting | Environment | Default | Description |
| --- | --- | --- | --- |
| `versions.enabled` | `FUKKU_VERSIONS_ENABLED` | `false` | Offer second versions (flow 2b, and the season-dub policy `version` for episodes), the one direct write into the library |
| `versions.root` | `FUKKU_VERSIONS_ROOT` | (empty) | Folder for films' second versions, as Fukku sees it. Required when `versions.enabled` is on. Must not overlap `paths.libraryRoots` or `paths.intakeRoot`, and before each version Fukku also checks it against Radarr's root folders (as `paths.mappings.radarr` maps them). Fukku creates one folder per film in it, never the root itself |
| `versions.labelTemplate` | `FUKKU_VERSIONS_LABEL_TEMPLATE` | `{languageName}` | Label in `<film folder> - <label>.<ext>` (and `<series folder> - S01E02 - <label>.<ext>` for episodes), proposed to the user, who may change it. `{languageName}` is the upload's new audio language in that language (`Español` for a Spanish track the library file does not have; for episodes, the first tagged audio language that is not the series' original one), `{languageCode}` its ISO 639-1 code (`es`). Without a tagged audio language the user must type a label. An episode's label must not hold a season and episode number (`S02E05`, `2x05`) |
| `versions.forbiddenLabelTokens` | `FUKKU_VERSIONS_FORBIDDEN_LABEL_TOKENS` | `2160p, 1080p, 720p, 576p, 480p, 4k, uhd, hd, sd, bluray, blu-ray, bdrip, brrip, remux, web, webdl, web-dl, webrip, hdtv, dvd, dvdrip, hdr, dv, proper, repack` | Words a label must not contain (whole words, any case; a token with `-` also matches with a space or `.`), so the version's name never reads as a quality or source (B3): such a label is refused with `version_label` |
| `versions.requireNotUpgradeVerdict` | `FUKKU_VERSIONS_REQUIRE_NOT_UPGRADE_VERDICT` | `true` | Before a version is placed, ask the app what it reads in the version's name. Films: Radarr's `GET /parse` of `<film folder> - <label>` (no extension: with `.mkv` Radarr reads WEBDL-720p in any name) must read no quality, source, resolution, proper, real or repack beyond what it reads in `<film folder>` alone (so a film whose own title reads as a quality, such as `Cam (2018)`, can still have versions), else the label is refused (`version_label`). A name Radarr cannot parse at all gives no verdict, and is refused too: try another label, or turn this off. Episodes (#58): the upload is hard-linked under the version's name into the job's folder (`jobs/<job>/versions/<row>/`), and Sonarr's Manual Import proposal for it, re-checked for the episode, must reject it as not an upgrade for the episode's file (the check Sonarr's rescan runs: quality from the name and the file's resolution, then the custom-format score), or because the quality profile allows no upgrade (Sonarr then never takes another file for the episode's). Else the rows are refused with `version_upgrade`: replace those episodes instead, or skip them. A file Sonarr does not offer, or reads as another episode, gives no verdict: `version_unchecked`, with the reason. Asked at the confirmation and again right before each link; a version refused there fails its row only (result `refused`, audit `version.refused`), and the other versions and episodes go on; only when every version is refused and nothing else is imported does the job fail. The re-check of placed episodes' versions (#96, `versions.regroupInterval`) asks the same verdict of the file where it is, and marks a version Sonarr would now take `adoptable`. Off, nothing stops a version Sonarr would take for the episode's file, and the re-check does not ask |
| `versions.regroupInterval` | `FUKKU_VERSIONS_REGROUP_INTERVAL` | `10m` | How often every film version's grouping is checked: Fukku asks Radarr for the film's current file and merges the version with it again when the grouping was lost (audit `version.regrouped`, which names the old and the new file). Files the media server does not list yet get one refresh per pass for all of them (with `mediaServer.refreshMethod: libraryScan`, one scan, skipped while a scan runs), and one wait of up to `mediaServer.itemWait` for all of them together; a file in no library of the media server is skipped (logged) and the others go on. A version still not listed after that backs off: the gap between its attempts doubles (1, 2, 4… passes), up to the hourly recheck (a nudge after an import tries at once; a restart starts over). Fukku also checks a film's versions right after each of its own imports for that film. `film_deleted` and `missing` versions are checked every six intervals (at least hourly). The same passes re-check every episode's version against Sonarr (#96, see the episodes above: `adopted`, `adoptable`, `episode_file_missing`, `series_moved`, `series_deleted`), reading each series once per pass and asking Sonarr's verdict once per season folder, when the episode's file changed and every six intervals; a nudge after an import does not check them. `0` turns the schedule off (then only the checks of films after imports run, and episodes' versions are re-checked only once, at start-up) |

## seasonDub

| Setting | Environment | Default | Description |
| --- | --- | --- | --- |
| `seasonDub.policy` | `FUKKU_SEASON_DUB_POLICY` | `ask` | A dub of a season already in the library: `merge`, `version`, `replace`, `subtitlesOnly` or `ask`. A fixed policy skips the question; one that does not apply to the job (or is not available yet: `merge`) leaves the rows that already have a file skipped. `version` keeps them as second versions of their episodes (see versions above) |
| `seasonDub.order` | `FUKKU_SEASON_DUB_ORDER` | `merge, version, replace, subtitlesOnly` | Order of the options offered by `ask`. Only those that apply are shown: `version` when `versions.enabled` is on, the media server is Jellyfin 12.4 or later, Sonarr's folder name for the series can be part of a file name, and every season folder of the rows takes a hard link from the intake root (tried, and reused for a minute; else the proposal's `seasonDub.notOffered` says why: `versions_disabled`, `versions_unsupported`, `media_server_too_old`, `media_server_unknown`, `series_folder_unusable`, `season_folder_unavailable`); `replace` always; `subtitlesOnly` when the job has subtitle files and `subtitles.provider` and `subtitles.handOverUploads` allow the hand-over |

## worker

| Setting | Environment | Default | Description |
| --- | --- | --- | --- |
| `worker.concurrency` | `FUKKU_WORKER_CONCURRENCY` | `1` | Tasks the worker runs at once |
| `worker.spoolDir` | `FUKKU_WORKER_SPOOL_DIR` | `/spool` | Local directory shared by web and worker for task and result files |
| `worker.pollInterval` | `FUKKU_WORKER_POLL_INTERVAL` | `1s` | How often the worker looks for new tasks and cancel requests, and the web process for results |
| `worker.probeTimeout` | `FUKKU_WORKER_PROBE_TIMEOUT` | `1m` | Deadline of one ffprobe run, and of one mkvmerge identification of a file in a merge |
| `worker.libraryFormats` | `FUKKU_WORKER_LIBRARY_FORMATS` | `matroska, webm, mov, mp4, avi, mpegts, mpeg, asf, flv, ogg` | Demuxers the worker may use on a film's library file in flow 2: the library probe (ffprobe) and the sync-check clips (ffmpeg, `merge.preview*`) (it is a file Radarr already imported, so it may be in an older container such as `.mpg`, `.wmv` or `.flv`; uploads always use the narrower fixed list of the `upload.videoExtensions` containers). Names are ffprobe demuxer names (`ffprobe -demuxers`): lower-case letters, digits, `_`, `+`. Playlist and streaming demuxers (`hls`, `dash`, `concat`, `rtsp`, `sdp`, ...) are refused, and the worker opens no network protocol whatever is listed. A library file in a format not listed fails the job with `library_unreadable` |
| `worker.taskTimeout` | `FUKKU_WORKER_TASK_TIMEOUT` | `6h` | Deadline of one task (extraction, merge) |
| `worker.nice` | `FUKKU_WORKER_NICE` | `10` | CPU priority of the tools the worker runs (0–19) |
| `worker.heartbeatInterval` | `FUKKU_WORKER_HEARTBEAT_INTERVAL` | `15s` | How often the worker writes its heartbeat |
| `worker.heartbeatMaxAge` | `FUKKU_WORKER_HEARTBEAT_MAX_AGE` | `2m` | An older heartbeat makes `/readyz` fail |
