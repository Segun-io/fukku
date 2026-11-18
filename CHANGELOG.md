# Changelog

All notable changes to Fukku are written here, in the
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) format. Versions follow
[Semantic Versioning](https://semver.org/). The release workflow
(`.github/workflows/release.yml`) takes the notes of a release from the section of its version;
how a release is made is in [docs/release.md](docs/release.md).

## [Unreleased]

## [0.1.0-rc.1] - 2026-11-18

The release candidate of 0.1.0, the first release. It runs the whole release path (publish,
sign, attest) once before 0.1.0 itself, which follows with these same changes.

### Upgrade notes

There is no earlier release to upgrade from. These defaults changed while 0.1.0 was being
prepared, and matter to anyone who ran a pre-release image (`0.0.0-pre.*`) or the test stack:

- **The worker has its own folder, and the compose layout changed.** The worker now writes only
  under `<intake>/worker/<job>/`; the job folders under `<intake>/jobs/` are written by the web
  process alone and mounted read-only in the worker. Take the mounts from
  `deploy/compose.example.yaml` and docs/install.md; a copy of an older layout will not start:
  - `worker.requireSeparateMounts` is on by default, so `fukku worker` refuses to start when it
    can write the job folders, the library or `versions.root` (log `worker_mounts`).
  - The intake mounts use `create_host_path: false`, so a folder that does not exist stops the
    container instead of being created as root: create the folders first (install step 1).
  - That needs Docker Compose v2.12 or later.
  - The web container mounts `/data` read-only unless `versions.enabled` is on (second versions
    are hard links from the intake root and need the data folder writable).
- **Changes to files the library already has are admin-only.** The new setting
  `auth.libraryChangesRequireAdmin` is on by default. Only an admin (`auth.adminGroup`, or
  everyone with `auth.allUsersAreAdmins`) can confirm "Import anyway" over a rejection, the
  season-dub choices `replace` and `version`, a merge, or a film's second version. Everyone can
  still upload and import new films and episodes. **With the defaults nobody is an admin:** set
  `auth.adminGroup` (with `auth.groupsHeader`) or `auth.allUsersAreAdmins`, or turn the setting
  off for a household where everyone may change the library. `fukku check-config` and the log
  warn about it.
- **`auth.groupsHeader` is empty by default** (it was `Remote-Groups`). Set it to
  `Remote-Groups` only where your proxy sets that header on every request and drops a copy sent
  by the client. In `proxyHeaders` mode, `auth.adminGroup` without `auth.groupsHeader` is now
  refused at start-up (it used to make nobody an admin).
- **Wide trusted-proxy ranges are refused.** An `auth.trustedProxies` prefix shorter than /8
  (IPv4) or /32 (IPv6), such as `0.0.0.0/0`, stops the start unless
  `auth.allowWideTrustedProxies` is on. `fukku check-config` also warns when
  `server.publicBaseUrl` is empty in `proxyHeaders` mode, because the Origin check then trusts
  the `Host` header.
- **Stricter settings checks.** `mediaServer.titleLinkTemplate` must be an http(s) URL without
  credentials that contains `{itemId}` and no other placeholder. The worker refuses any
  `<NAME>_FILE` secret and any URL with credentials.
- **New limits can refuse uploads that used to work.** `upload.maxOpenJobsPerUser` (default 20)
  answers a user with 20 open jobs `429 too_many_jobs`; `worker.maxStreamsPerFile` (256) refuses
  a file with more tracks (`too_many_streams`); `worker.toolMemoryLimit` (6GiB) caps each
  ffprobe, ffmpeg and mkvmerge process. The compose example limits memory (`mem_limit` 4g for
  the worker, 1g for the web process) and processes (`pids_limit` 256); a very large file or
  merge may need higher values.
- **Stricter archive and folder names.** Archive entries whose names start with a dot are
  skipped, and a season folder that Sonarr reports with `..` or another unclean path is refused
  instead of cleaned.
- **The content security policy no longer allows inline styles**, and `/assets/` is not listed.
  A proxy that rewrites responses must not add inline styles.

### Added

- **Upload (M1).** Resumable uploads of large files (tus) that survive a reload, a restart and a
  dropped connection; sign-in through a trusted reverse proxy (`proxyHeaders`), with an Origin
  check; a job list and a job detail page; retention of idle uploads and stale jobs; readiness
  checks (`/readyz`) for the shared filesystem and the mount sentinels.
- **Safe extraction.** Archives and media files are opened by a worker that has no network and
  no secrets, with limits on size, entries, depth and names. A content check with `ffprobe`
  rejects what is not media.
- **Flow 1, a new film, and flow 3, a season archive (M2).** Search and pick the title in Radarr
  or Sonarr, review the proposal (a mapping editor for seasons, with a per-row re-check, bulk
  edits and a language warning), confirm, and follow the import to the result. Titles are
  added with search off. An import resumes after a crash and is checked against what the app
  reports. Subtitles are handed over to Bazarr, with confirmation and retry, and the media
  server (Jellyfin) is told to refresh.
- **Flow 2, another language of a film already in the library (M3).** The new file is probed
  against the library file and compared by runtime. A sync preview with an offset (short
  clips) lets you hear the result before you choose to merge the audio and subtitles into the
  library file with `mkvmerge` (verified, then imported over the old file and unmonitored so
  that an upgrade does not replace it), or keep it as a second version of the film. For a
  season of episodes already in the library, the season-dub policies are `merge`, `version`,
  `replace`, `subtitlesOnly` and `ask`. Second versions (`versions.enabled`, off by default)
  are placed with a hard link and re-checked periodically.
- **Operations (M4).** A read-only admin page with the health and effective settings,
  Prometheus metrics on `/metrics`, and `fukku check-config`. The interface is in English and
  Spanish. Documentation: the install guide, reverse-proxy examples, the settings reference,
  backup, troubleshooting and the threat model.
- **One multi-architecture image** (`linux/amd64` and `linux/arm64`), slim, running as a
  non-root user, with an SBOM that lists the image's and the interface's packages, and an
  `ffmpeg` built from a pinned upstream source. Releases are pushed to GHCR, signed with cosign
  (keyless) and carry build provenance; verify them as written in docs/release.md.
- **A compatibility table** (`docs/compatibility.md`) of what Fukku relies on in Radarr, Sonarr,
  Bazarr and Jellyfin, produced by behaviour probes against the pinned versions, and published
  with each release.

### Security

- A review of the threat model with the findings fixed before this release, including: a
  compromised worker steering the web process's reads and writes, through the subtitle
  hand-over, upload metadata or a symbolic link in a job folder (the worker is now confined to
  its own folder; every hand-over checks the file without following links); no setuid files
  left in the image; API keys no longer follow redirects to other hosts; dependency scanning
  in CI; fullwidth and other compatibility characters refused in version labels; a free-space
  check that does not trust the upload's own size tags; process-group kills that cannot hit a
  reused process id; a stricter content security policy and `Cross-Origin-Resource-Policy`;
  source maps and directory listings removed.
- Report vulnerabilities privately, as `SECURITY.md` describes.

[Unreleased]: https://github.com/Segun-io/fukku/compare/v0.1.0-rc.1...HEAD
[0.1.0-rc.1]: https://github.com/Segun-io/fukku/releases/tag/v0.1.0-rc.1
