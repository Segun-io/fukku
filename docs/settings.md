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
redacted. A test keeps this table and the code in step.

## Process

| Setting | Environment | Default | Description |
| --- | --- | --- | --- |
| `mode` | `FUKKU_MODE` | `split` | `split`: web and worker run as two processes (`fukku web`, `fukku worker`), the worker with no network. `combined`: one process (`fukku combined`) for simple installs; weaker, because the parser then shares the web process's network and secrets |

## server

| Setting | Environment | Default | Description |
| --- | --- | --- | --- |
| `server.listenAddress` | `FUKKU_SERVER_LISTEN_ADDRESS` | `:8080` | Address the web process listens on |
| `server.publicBaseUrl` | `FUKKU_SERVER_PUBLIC_BASE_URL` | (empty) | External URL users open, e.g. `https://fukku.example.com`. Used for the Origin check and links; empty means the request's own host |
| `server.uiLanguages` | `FUKKU_SERVER_UI_LANGUAGES` | `en, es` | UI languages offered; the first is the fallback |
| `server.logFormat` | `FUKKU_SERVER_LOG_FORMAT` | `logfmt` | `logfmt` or `json` |
| `server.metrics` | `FUKKU_SERVER_METRICS` | `false` | Serve Prometheus metrics on `/metrics` |
| `server.dataDir` | `FUKKU_SERVER_DATA_DIR` | `/var/lib/fukku` | Database and local state. Must be local disk, never network storage (SQLite) |
| `server.jobStuckAfter` | `FUKKU_SERVER_JOB_STUCK_AFTER` | `1h` | A failed or stalled job older than this logs `job_stuck` once a minute |

## auth

| Setting | Environment | Default | Description |
| --- | --- | --- | --- |
| `auth.mode` | `FUKKU_AUTH_MODE` | `proxyHeaders` | `proxyHeaders`: a reverse proxy authenticates users and passes headers. `oidc`: backlog. `none`: development only, refuses to start without `--insecure-dev` |
| `auth.trustedProxies` | `FUKKU_AUTH_TRUSTED_PROXIES` | (empty) | Addresses or CIDRs the reverse proxy connects from. Required for `proxyHeaders`; requests from anywhere else get 403 |
| `auth.userHeader` | `FUKKU_AUTH_USER_HEADER` | `Remote-User` | Header with the user name. Requests without it get 403 |
| `auth.emailHeader` | `FUKKU_AUTH_EMAIL_HEADER` | `Remote-Email` | Header with the user's email (optional, shown in the audit log) |
| `auth.groupsHeader` | `FUKKU_AUTH_GROUPS_HEADER` | `Remote-Groups` | Header with the user's groups |
| `auth.groupsSeparator` | `FUKKU_AUTH_GROUPS_SEPARATOR` | `,` | Separator between groups in the groups header |
| `auth.adminGroup` | `FUKKU_AUTH_ADMIN_GROUP` | (empty) | Members of this group are admins. Empty means no admins unless `allUsersAreAdmins` |
| `auth.allUsersAreAdmins` | `FUKKU_AUTH_ALL_USERS_ARE_ADMINS` | `false` | Every signed-in user is an admin (small, trusted households) |
| `auth.originCheck` | `FUKKU_AUTH_ORIGIN_CHECK` | `true` | State-changing requests must carry a matching `Origin` header |

## radarr and sonarr

Each app is enabled when its `url` is set; at least one must be. The two groups have the
same settings.

| Setting | Environment | Default | Description |
| --- | --- | --- | --- |
| `radarr.url` | `FUKKU_RADARR_URL` | (empty) | Base URL, e.g. `http://radarr:7878` |
| `radarr.apiKey` | `FUKKU_RADARR_API_KEY` | (empty) | API key (secret) |
| `radarr.qualityProfile` | `FUKKU_RADARR_QUALITY_PROFILE` | (empty) | Quality profile name for added films; empty means the user picks |
| `radarr.rootFolder` | `FUKKU_RADARR_ROOT_FOLDER` | (empty) | Root folder for added films, as Radarr sees it; empty means the first one |
| `radarr.tag` | `FUKKU_RADARR_TAG` | `fukku` | Tag put on titles Fukku adds, so they are easy to list |
| `radarr.monitorAfterImport` | `FUKKU_RADARR_MONITOR_AFTER_IMPORT` | `true` | Default of "replace when a better copy appears" for a new film (flow 1) |
| `radarr.importMode` | `FUKKU_RADARR_IMPORT_MODE` | `move` | `move` or `copy` for Manual Import |
| `radarr.requestTimeout` | `FUKKU_RADARR_REQUEST_TIMEOUT` | `30s` | Timeout of one API request |
| `sonarr.url` | `FUKKU_SONARR_URL` | (empty) | Base URL, e.g. `http://sonarr:8989` |
| `sonarr.apiKey` | `FUKKU_SONARR_API_KEY` | (empty) | API key (secret) |
| `sonarr.qualityProfile` | `FUKKU_SONARR_QUALITY_PROFILE` | (empty) | Quality profile name for added shows; empty means the user picks |
| `sonarr.rootFolder` | `FUKKU_SONARR_ROOT_FOLDER` | (empty) | Root folder for added shows, as Sonarr sees it; empty means the first one |
| `sonarr.tag` | `FUKKU_SONARR_TAG` | `fukku` | Tag put on shows Fukku adds |
| `sonarr.monitorAfterImport` | `FUKKU_SONARR_MONITOR_AFTER_IMPORT` | `false` | New shows are added with nothing monitored (flow 3) |
| `sonarr.importMode` | `FUKKU_SONARR_IMPORT_MODE` | `move` | `move` or `copy` for Manual Import |
| `sonarr.requestTimeout` | `FUKKU_SONARR_REQUEST_TIMEOUT` | `30s` | Timeout of one API request |

## subtitles

| Setting | Environment | Default | Description |
| --- | --- | --- | --- |
| `subtitles.provider` | `FUKKU_SUBTITLES_PROVIDER` | `none` | `bazarr` or `none` |
| `subtitles.url` | `FUKKU_SUBTITLES_URL` | (empty) | Base URL, e.g. `http://bazarr:6767` |
| `subtitles.apiKey` | `FUKKU_SUBTITLES_API_KEY` | (empty) | API key (secret) |
| `subtitles.handOverUploads` | `FUKKU_SUBTITLES_HAND_OVER_UPLOADS` | `true` | Give uploaded subtitle files to the subtitle manager after import |
| `subtitles.requestTimeout` | `FUKKU_SUBTITLES_REQUEST_TIMEOUT` | `30s` | Timeout of one API request |

## mediaServer

| Setting | Environment | Default | Description |
| --- | --- | --- | --- |
| `mediaServer.provider` | `FUKKU_MEDIA_SERVER_PROVIDER` | `none` | `jellyfin` or `none` (Plex and Emby are backlog) |
| `mediaServer.url` | `FUKKU_MEDIA_SERVER_URL` | (empty) | Base URL, e.g. `http://jellyfin:8096` |
| `mediaServer.apiKey` | `FUKKU_MEDIA_SERVER_API_KEY` | (empty) | API key (secret) |
| `mediaServer.refreshMethod` | `FUKKU_MEDIA_SERVER_REFRESH_METHOD` | `pathUpdate` | `pathUpdate` (tell the server which path changed), `libraryScan` (full scan) or `none` |
| `mediaServer.titleLinkTemplate` | `FUKKU_MEDIA_SERVER_TITLE_LINK_TEMPLATE` | (empty) | Link shown after import, e.g. `https://media.example.com/web/#/details?id={itemId}`; empty means no link |
| `mediaServer.requestTimeout` | `FUKKU_MEDIA_SERVER_REQUEST_TIMEOUT` | `30s` | Timeout of one API request |

## paths

| Setting | Environment | Default | Description |
| --- | --- | --- | --- |
| `paths.intakeRoot` | `FUKKU_PATHS_INTAKE_ROOT` | `/data/intake` | Where uploads (`.incoming/`) and jobs (`jobs/`) live. Must be on the library filesystem when `importMode=move` |
| `paths.libraryRoots` | `FUKKU_PATHS_LIBRARY_ROOTS` | (empty) | Library root folders as Fukku sees them; used for the same-filesystem check and for second versions |
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
| `upload.freeSpaceMargin` | `FUKKU_UPLOAD_FREE_SPACE_MARGIN` | `100GiB` | Refuse an upload when free space is below its size plus this; an extraction stops when free space falls below this (measured on Linux, where the worker runs; elsewhere the extraction check always passes) |
| `upload.videoExtensions` | `FUKKU_UPLOAD_VIDEO_EXTENSIONS` | `.mkv, .mp4, .m4v, .avi, .mov, .ts, .webm` | Accepted video files |
| `upload.subtitleExtensions` | `FUKKU_UPLOAD_SUBTITLE_EXTENSIONS` | `.srt, .ass, .ssa` | Accepted subtitle files |
| `upload.archiveExtensions` | `FUKKU_UPLOAD_ARCHIVE_EXTENSIONS` | `.zip` | Accepted archives |
| `upload.idleUploadExpiry` | `FUKKU_UPLOAD_IDLE_UPLOAD_EXPIRY` | `2d` | An unfinished upload with no progress for this long is deleted |
| `upload.jobRetention` | `FUKKU_UPLOAD_JOB_RETENTION` | `14d` | A job waiting for a decision is deleted after this; the date is shown from day one |
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
| `match.runtimeTolerance` | `FUKKU_MATCH_RUNTIME_TOLERANCE` | `500ms` | Durations within this are the same cut (merge is offered) |
| `match.palRatio` | `FUKKU_MATCH_PAL_RATIO` | `1.0427` | Duration ratio of a PAL speed-up (25 / 23.976) |
| `match.palTolerance` | `FUKKU_MATCH_PAL_TOLERANCE` | `0.002` | Tolerance around the PAL ratio |
| `match.defaultSource` | `FUKKU_MATCH_DEFAULT_SOURCE` | `WEBDL` | Source assumed when the file name has none: `WEBDL`, `WEBRIP`, `BLURAY`, `HDTV` or `DVD` |
| `match.doubleEpisodeRatio` | `FUKKU_MATCH_DOUBLE_EPISODE_RATIO` | `2` | A file this many times the expected runtime is hinted as a double episode |
| `match.doubleEpisodeTolerance` | `FUKKU_MATCH_DOUBLE_EPISODE_TOLERANCE` | `0.15` | Relative tolerance around that ratio |

## merge

| Setting | Environment | Default | Description |
| --- | --- | --- | --- |
| `merge.enabled` | `FUKKU_MERGE_ENABLED` | `true` | Offer merging a new language into the existing file (flow 2a) |
| `merge.previewClips` | `FUKKU_MERGE_PREVIEW_CLIPS` | `3` | Sync-check clips (start, middle, end) |
| `merge.previewClipLength` | `FUKKU_MERGE_PREVIEW_CLIP_LENGTH` | `20s` | Length of each clip |
| `merge.languageTag` | `FUKKU_MERGE_LANGUAGE_TAG` | `es-419` | Default language tag (BCP 47) of added tracks |
| `merge.trackName` | `FUKKU_MERGE_TRACK_NAME` | `Español (Latino)` | Default name of added audio tracks |
| `merge.keepOriginalDefault` | `FUKKU_MERGE_KEEP_ORIGINAL_DEFAULT` | `true` | The original audio stays the default track |
| `merge.unmonitorAfterMerge` | `FUKKU_MERGE_UNMONITOR_AFTER_MERGE` | `true` | Unmonitor the title after a merge, so an upgrade without the dub cannot replace it (B6) |
| `merge.unmonitorAfterDubOnlyImport` | `FUKKU_MERGE_UNMONITOR_AFTER_DUB_ONLY_IMPORT` | `true` | Same after importing a file that only has the dub |

## versions

| Setting | Environment | Default | Description |
| --- | --- | --- | --- |
| `versions.enabled` | `FUKKU_VERSIONS_ENABLED` | `false` | Allow second versions (flow 2b), the one direct write into the library |
| `versions.strategy` | `FUKKU_VERSIONS_STRATEGY` | `naming` | `naming` (file names the media server groups, B1/B2) or `mergeApi` (the media server's merge-versions call) |
| `versions.labelTemplate` | `FUKKU_VERSIONS_LABEL_TEMPLATE` | `{languageName}` | Label in `<folder name> - <label>.<ext>` |
| `versions.forbiddenLabelTokens` | `FUKKU_VERSIONS_FORBIDDEN_LABEL_TOKENS` | `2160p, 1080p, 720p, 576p, 480p, 4k, uhd, hd, sd, bluray, blu-ray, bdrip, brrip, remux, web, webdl, web-dl, webrip, hdtv, dvd, dvdrip, hdr, dv, proper, repack` | Words a label must not contain, so the *arr app never adopts the version as an upgrade (B3) |
| `versions.requireNotUpgradeVerdict` | `FUKKU_VERSIONS_REQUIRE_NOT_UPGRADE_VERDICT` | `true` | Before linking, the *arr app must reject the file as "not an upgrade" |

## seasonDub

| Setting | Environment | Default | Description |
| --- | --- | --- | --- |
| `seasonDub.policy` | `FUKKU_SEASON_DUB_POLICY` | `ask` | A dub of a season already in the library: `merge`, `version`, `replace`, `subtitlesOnly` or `ask` |
| `seasonDub.order` | `FUKKU_SEASON_DUB_ORDER` | `merge, version, replace, subtitlesOnly` | Order of the options offered by `ask` |

## worker

| Setting | Environment | Default | Description |
| --- | --- | --- | --- |
| `worker.concurrency` | `FUKKU_WORKER_CONCURRENCY` | `1` | Tasks the worker runs at once |
| `worker.spoolDir` | `FUKKU_WORKER_SPOOL_DIR` | `/spool` | Local directory shared by web and worker for task and result files |
| `worker.pollInterval` | `FUKKU_WORKER_POLL_INTERVAL` | `1s` | How often the worker looks for new tasks and cancel requests, and the web process for results |
| `worker.probeTimeout` | `FUKKU_WORKER_PROBE_TIMEOUT` | `1m` | Deadline of one ffprobe run |
| `worker.taskTimeout` | `FUKKU_WORKER_TASK_TIMEOUT` | `6h` | Deadline of one task (extraction, merge) |
| `worker.nice` | `FUKKU_WORKER_NICE` | `10` | CPU priority of the tools the worker runs (0–19) |
| `worker.heartbeatInterval` | `FUKKU_WORKER_HEARTBEAT_INTERVAL` | `15s` | How often the worker writes its heartbeat |
| `worker.heartbeatMaxAge` | `FUKKU_WORKER_HEARTBEAT_MAX_AGE` | `2m` | An older heartbeat makes `/readyz` fail |
