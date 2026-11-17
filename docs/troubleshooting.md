# Troubleshooting

Start with the two places that tell you most:

```sh
docker compose ps                     # both containers should be "healthy"
docker compose logs --tail 100 web    # events have stable names: auth_refused, job_stuck, ...
```

and, signed in as an admin through your proxy, `GET /readyz` (every readiness check by name)
and the **Admin** page (the effective settings and the server's health). Set
`server.logFormat: json` to feed the logs to a tool.

## "not ready": which check

A container that is `unhealthy`, or a `/readyz` with `"status":"not_ready"`, names a failing
check. The container's own health check uses the local ones only; `radarr` and `sonarr` only
make the status `degraded` (HTTP 200).

| Check | The detail says | Fix |
| --- | --- | --- |
| `database` | the database does not answer | `state/data` must be writable by the container user, on local disk, and used by one Fukku only. Read the log lines before it |
| `spool` | the spool folder is not writable | `state/spool` must belong to the container user |
| `sentinels` | `/data/.mounted is missing` (or the sentinel you set) | the share is not mounted, or the file was never created: `touch` it once on the mounted share. Nothing is written while it is missing, by design |
| `intakeRoot` | the folder is not writable | the container user must be able to write `paths.intakeRoot`; the data mount of the web container must not be read-only |
| `sameFilesystem` | `/data/media/movies is on a different filesystem than /data/intake` | see below |
| `workerHeartbeat` | there is no heartbeat, or it is older than `worker.heartbeatMaxAge` | the worker container is down, or does not share `state/spool` with the web container. `docker compose logs worker` |
| `radarr`, `sonarr` | the app answered HTTP 401, or did not answer | wrong `url`, wrong key file, or the container is not on the shared network. This only makes the status `degraded`; uploads still work and jobs that need the app wait (`arr_unavailable`) and resume by themselves |

`fukku healthcheck` itself only prints the HTTP status it got (`readyz returned 503`): use
`/readyz` for the name.

### "On a different filesystem"

Imports that move (`radarr.importMode` and `sonarr.importMode` default to `move`) are only a
rename when the intake root and every library root are on one filesystem: otherwise the app
copies the whole file and deletes the original, and Fukku refuses (`paths.requireSameFilesystem`
`auto`). Mount **one** host folder as `/data` in Fukku, Radarr and Sonarr, with intake and
media below it, not the intake and the media as two mounts, even of the same disk. If you really
want a copy, set the import mode to `copy`, or `paths.requireSameFilesystem: never`.

## 403

Every refusal is logged as `auth_refused` with a `reason` and the `remote` address:

| `reason` | Cause | Fix |
| --- | --- | --- |
| `untrusted_address` | the connection did not come from `auth.trustedProxies` | put the address in `remote=` into `auth.trustedProxies`, or fix the proxy's network ([reverse-proxy.md](reverse-proxy.md), "a fixed address for the proxy"). With the host's proxy, it is the Docker network's gateway |
| `user_header_missing` | no `Remote-User` (or `auth.userHeader`) | the proxy did not set it: check its forward-auth block, and that the auth service's answer carries it |
| `user_header_unusable` | the user header is empty, repeated, longer than 256 bytes or has control characters | the proxy sends it twice (a header you set and one copied), or an empty value |
| `email_header_unusable`, `groups_header_unusable` | same for those headers | send them once, or set `auth.emailHeader` / `auth.groupsHeader` empty |
| `origin_mismatch` | a change (`POST`, `PATCH`, `DELETE`) without an `Origin` that equals `server.publicBaseUrl` | `server.publicBaseUrl` must be exactly the address in the browser, with `https://`; the proxy must not rewrite `Origin` or `Host` |
| `not_admin` | `/readyz`, `/metrics` or the Admin page, as a non-admin | put the user in `auth.adminGroup`, or `auth.allUsersAreAdmins` |

If the Admin page is not there, you are not an admin: it answers 404 to others. If you are signed
out in the middle of an upload, the page waits and continues once you sign in.

## Uploads that stall or fail

- **Stops after the same amount every time, or after a round number of seconds**: the proxy's
  timeout. One request carries up to `upload.chunkSize` (16 MiB); set the proxy's body and
  upstream timeouts above the time it takes on the slowest link, or lower the chunk size. nginx
  defaults to 60 s, Traefik's `readTimeout` to 60 s ([reverse-proxy.md](reverse-proxy.md)). The
  upload continues from where it stopped when the person chooses the same file again.
- **`413` at once**: the proxy's body limit is below one chunk (nginx `client_max_body_size`; a
  content-delivery network's cap), or a file is above `upload.maxFileSize` (`file_too_large`).
- **`507 insufficient_space`**: free space on the intake filesystem is less than the file plus
  `upload.freeSpaceMargin` (100 GiB by default): lower the margin on a small disk, or make room.
  **`in_flight_limit`** is `upload.maxInFlight`; **`429 too_many_uploads`** is
  `upload.maxParallelPerUser`.
- **`415 extension_not_allowed`**: the file type is not in `upload.videoExtensions`,
  `upload.subtitleExtensions` or `upload.archiveExtensions`.
- **`upload_busy`, `offset_mismatch`**: two tabs on one upload; the page recovers by itself.
- **An upload that has not moved for `upload.idleUploadExpiry`** (2 days) is deleted.
- A large file over a tunnel or relay is slow and costs transfer: the page hints at it above
  `upload.largeFileHintThreshold`.

## Jobs

- **Stuck or failed**: the job says why and has the actions that apply (retry, cancel). A job
  that has been failed or stalled for `server.jobStuckAfter` (1 hour) logs `job_stuck` once a
  minute. A job `stalled` with `arr_unavailable` continues when the app answers again.
- **An import that never ends** (Radarr or Sonarr keeps the Manual Import command queued): after
  `server.jobStuckAfter` an admin can cancel it, which is audited as `import.abandoned`. The app
  may still finish it later, so look in its Activity and History first.
- **Not offered**: a second version (`versions_disabled`, `versions_unsupported`,
  `media_server_too_old`, `season_folder_unavailable`, ...) or a merge. The page says why;
  [settings.md](settings.md) (`versions`, `seasonDub`, `merge`) explains each. A second version
  needs the web container to write the library: a hard link failing means `versions.root` or the
  season folder is on another mount, or read-only.
- **`library_unreadable`** (flow 2): the film's file is not under `paths.libraryRoots` (as
  `paths.mappings.radarr` maps Radarr's path), or its container is not in
  `worker.libraryFormats`.
- **A new title is not in Jellyfin**: after `mediaServer.itemWait` the job still ends `done`,
  with the step marked. See "Settings worth changing in the other apps" in [install.md](install.md).
- **A post step failed (subtitles, media server)**: the job is `done`; the step is retried
  `server.postStepAttempts` times and then recorded as failed. Use the app's own button.
- **Jobs waiting for a decision are deleted** after `upload.jobRetention` (14 days); finished
  jobs' records after `upload.finishedJobRetention` (30 days).

## The manual fallback

Radarr and Sonarr stay usable without Fukku. A job's files are in
`<paths.intakeRoot>/jobs/<job id>/files`. Open the app's Manual
Import page on that folder (**Wanted**, **Missing**, **Manual Import**); a finished job links to it when `radarr.publicUrl` or `sonarr.publicUrl` is set.
Adding the title with search off first, as Fukku does, avoids a download.

## Still stuck

Collect the effective settings (`docker compose run --rm --no-deps web check-config`, which
redacts the keys), the output of `docker compose ps` and `/readyz`, and the log lines around
the problem, and open an issue at the project's repository. Look for secrets before you paste:
Fukku does not log them, your proxy might.
