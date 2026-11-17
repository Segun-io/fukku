# Install

This guide takes a host that has Docker and a working Radarr and/or Sonarr to a running Fukku
with a first upload, using only what is in this repository. It assumes you have not seen the
code. Plan about half an hour.

Fukku is two containers from one image: **web** (the UI, the API, uploads; it holds the API keys
and talks to your apps) and **worker** (opens the uploaded files; no network, no secrets). A
reverse proxy that signs your users in sits in front of **web**. Nothing else is needed: the
state is one SQLite file.

## What you need

- **Docker with Compose v2** (`docker compose version`) on a Linux host, amd64 or arm64.
- **Radarr or Sonarr** (or both), the v3 API. The versions the project tests against, with
  Bazarr and Jellyfin, are in [compatibility.md](compatibility.md). Their API keys.
- **One filesystem for intake and library.** The folder uploads land in (the intake root) must
  be on the same filesystem as the library when Radarr and Sonarr import by moving (the
  default), so a move is a rename and a 60 GB upload is not copied again. In practice: one
  `/path/to/data` on the host with `downloads`/`intake` and `media` below it, mounted into every
  container (any layout works if it is one filesystem).
- **A reverse proxy that authenticates your users** and tells Fukku who they are in headers:
  Caddy, Traefik, nginx and others ([reverse-proxy.md](reverse-proxy.md)). Fukku has no log-in
  page of its own, and refuses requests that did not come through the proxy.
- Optional: **Bazarr** (subtitles of an upload are handed to it) and **Jellyfin** (a refresh
  after each import, and a link to the title; second versions need it).
- The **user id** your media files belong to (`id -u`, `id -g`: usually the `PUID` and `PGID`
  of your Radarr and Sonarr containers).

## 1. Make the folder

Everything lives in one folder, for example `~/fukku`. As the user from the list above (the
one that owns your media), not root:

```sh
git clone https://github.com/segun-io/fukku.git ~/fukku-src   # for the example files
mkdir -p ~/fukku && cd ~/fukku
cp ~/fukku-src/deploy/compose.example.yaml compose.yaml
cp ~/fukku-src/deploy/fukku.example.yaml fukku.yaml
install -d -m 0750 state/data state/spool
install -d -m 0700 secrets
# The intake folders the worker mounts on their own (replace /path/to/data as in step 4):
install -d /path/to/data/intake/jobs /path/to/data/intake/worker /path/to/data/intake/.incoming
```

The intake folders must exist, belonging to that same user, before the first start: the
compose file refuses to start a container on a missing one rather than let Docker create it as
root, which neither container could then write. The web process also creates them when it
starts, if the share is mounted.

`state/data` holds the database and `state/spool` the files the two containers pass each other.
They must belong to the user the containers run as (the `user:` line of the compose file,
`1000:1000` by default). If your media user is another one, change that line and, if you are
not that user, `chown -R <uid>:<gid> state`. They must be on **local disk**, not a network
share (SQLite; Fukku refuses to start on one).

## 2. Get the image

The compose example names the image once, in its `x-image` line, with the version in the tag.
Read it from your `compose.yaml`, so the version is written in one place:

```sh
image=$(sed -n 's/^x-image: &image //p' compose.yaml)
docker pull "$image"
```

If the registry does not have that tag yet (the release is still being prepared), build the
image from this repository, at the tag that matches, and give it that name:

```sh
git -C ~/fukku-src checkout <the release tag>
docker build -t "$image" ~/fukku-src
```

The build takes several minutes (it compiles ffmpeg from a pinned source, [image.md](image.md)).
Check what you have:

```sh
docker run --rm "$image" version
```

(An image you built yourself prints `fukku dev`; a release image prints its version.)

## 3. Secrets, as files

Fukku never takes API keys from the configuration file, because the worker reads that file and
refuses to start if it holds a secret. The web container reads each key from a file (Docker
secrets). Create one file per app you use. Each app shows its key under Settings, General (Radarr,
Sonarr, Bazarr) or Dashboard, API Keys (Jellyfin):

```sh
read -rs key; printf '%s' "$key" > secrets/radarr_api_key; unset key       # paste, Enter
read -rs key; printf '%s' "$key" > secrets/sonarr_api_key; unset key
read -rs key; printf '%s' "$key" > secrets/bazarr_api_key; unset key
read -rs key; printf '%s' "$key" > secrets/jellyfin_api_key; unset key
chmod 0400 secrets/*
```

Leave out an app by deleting its `FUKKU_..._API_KEY_FILE` line, its name in `secrets: [...]`,
its entry under `secrets:` at the end of the compose file and its section in `fukku.yaml`.
(Keep at least one of Radarr and Sonarr.) The file must be readable by the container user.

## 4. Edit the two files

`compose.yaml`:

| Replace | With |
| --- | --- |
| `image:` tag (in the `x-image` line) | the version you pulled |
| `user: "1000:1000"` | your media user's `uid:gid` |
| every `/path/to/data` | the host folder that holds intake and media (one filesystem) |
| `/path/to/data/intake` (and its `jobs`, `worker`) | the intake folder inside it |
| `/path/to/data/media/movies`, `/path/to/data/media/tv` (worker) | each of `paths.libraryRoots`, one line each, at the same path inside as in `fukku.yaml` |
| `name: media` under `networks` | the Docker network your Radarr, Sonarr, Bazarr and Jellyfin containers share. (They must be on one: `docker network ls`) |

Mount the data at the **same path inside Fukku as Radarr and Sonarr see it** (`/data` in most
setups): then no path mappings are needed. If an app sees it elsewhere, say so with
`paths.mappings.*` ([settings.md](settings.md)); each app has its own list.

`fukku.yaml` is read by both processes. The settings you almost always change:

| Setting | What |
| --- | --- |
| `server.publicBaseUrl` | the address users type, with `https://`, exactly as the browser shows it: it is checked against the `Origin` of every change |
| `auth.trustedProxies` | the address the reverse proxy connects from (below) |
| `auth.adminGroup` (with `auth.groupsHeader`, usually `Remote-Groups`) or `auth.allUsersAreAdmins` | who may see the admin page, cancel stuck imports and, while `auth.libraryChangesRequireAdmin` is on (the default), replace or change files the library already has. Set one of them (the groups header is empty by default, and Fukku refuses to start with an admin group and no groups header: the proxy must set that header and drop the client's own, as the examples in [reverse-proxy.md](reverse-proxy.md) do): with the defaults nobody is an admin, so nobody can override a rejection, replace episodes, merge or keep a second version (`fukku check-config` and the web process's `config_warning` log line say so) |
| `radarr.url`, `sonarr.url` | `http://<container name>:<port>` on the shared network |
| `radarr.rootFolder`, `sonarr.rootFolder` | the root folder **as Radarr and Sonarr list it** |
| `radarr.qualityProfile`, `sonarr.qualityProfile` | the exact name of a quality profile |
| `paths.libraryRoots` | the same root folders, as Fukku sees them |
| `paths.sentinels` | a file that exists only while the share is mounted: `touch /path/to/data/.mounted` |

Every other setting, with its default, is in [settings.md](settings.md); only change what you
need. Nothing in the file is a secret, and every setting can also be an environment variable
(`FUKKU_*`).

## 5. Check the configuration

```sh
docker compose run --rm --no-deps web check-config
```

It loads the file the way the web process will, prints the effective settings with the keys
redacted, and says `valid for role web` on the first line. On a mistake it lists what is wrong
and exits with 1: no such key, a missing `trustedProxies`, a worker that was given a secret.
The worker has the same check: `docker compose run --rm --no-deps worker check-config --role worker`.
It cannot check the apps: that is what the next step shows.

## 6. Start it

```sh
docker compose up -d
docker compose ps
```

Both containers should say `healthy` within a minute. Their health checks are `fukku
healthcheck` (the web one asks the process's own `/readyz` for the database, the spool, the
sentinels, the intake root, the shared filesystem and the worker's heartbeat) and `fukku
healthcheck --worker` (the heartbeat). They do not depend on Radarr or Sonarr being up, so your
proxy keeps routing to the UI while they are down. To see the individual checks, ask `/readyz`
as an admin through the proxy (next step), or read the logs:

```sh
docker compose logs web
```

The line `web_listening` means it is up. A line `auth_refused` with `reason=untrusted_address`
and the address the request came from tells you what `auth.trustedProxies` must be.

## 7. Put the reverse proxy in front

Read [reverse-proxy.md](reverse-proxy.md): what Fukku needs from any proxy, and worked
configurations for Caddy, Traefik and nginx. The compose file already creates a network just for
the proxy, `fukku-frontend`, with a fixed subnet, and `auth.trustedProxies` names the address
(`172.29.50.10`) the proxy takes in it. The proxy container joins that network at that address
and sends everything to `http://fukku-web:8080`.

Do not publish port 8080 of Fukku on the host: anything that reaches it directly is refused
(403) but should not be able to try.

Then open `https://fukku.example.com` (your address). `GET /readyz` through the proxy, signed in
as an admin, shows every check, including whether Radarr and Sonarr answer:

```json
{"status":"ready","checks":[{"name":"database","ok":true},{"name":"spool","ok":true},
{"name":"sentinels","ok":true},{"name":"intakeRoot","ok":true},{"name":"sameFilesystem","ok":true},
{"name":"workerHeartbeat","ok":true},{"name":"radarr","ok":true},{"name":"sonarr","ok":true}]}
```

`ready` means everything works. `degraded` means only Radarr or Sonarr does not answer (the URL,
the key or the network). `not_ready` names the failing check; see
[troubleshooting.md](troubleshooting.md).

## 8. Your first upload

1. Open Fukku and **Upload**. Choose a video (`.mkv`, `.mp4`, ...), several files, or one `.zip`
   (a season). Press **Start upload**. A dropped connection or a reload continues where it
   stopped: choose the same file again.
2. Fukku checks the files, and asks Radarr or Sonarr to look at them. **Choose the film or
   show** it is (search by title; one it does not have yet is added).
3. **Confirm the import.** Radarr or Sonarr says what it read from each file; fix what is wrong,
   then **Import**.
4. The job ends with *is in the library*, and a link to the title in your media server. Radarr or
   Sonarr did the import: it shows in their History as a normal import. Under **Your uploads**
   you find every job and its state; one that needs attention says why.

If something goes wrong, the files are kept and the job says what happened and what to do; the
manual fallback is always Radarr's or Sonarr's own Manual Import page on the job's folder
(shown on the job). [troubleshooting.md](troubleshooting.md) lists the usual causes.

## 9. Settings worth changing in the other apps

- **Radarr: the recycle bin.** A merged dub (flow 2a) replaces the film's file, and Radarr
  deletes the file it replaces unless a recycle bin is set: Settings, Media Management, Recycle
  Bin, a folder **on the library's filesystem**. Set it before you merge anything you cannot
  fetch again.
- **Sonarr and Radarr: Manual Import needs write access** to the intake folder: they run as
  the same user as Fukku, or the folder is group-writable by it.
- **Jellyfin: a Movies and a Shows library** covering the root folders, as Jellyfin sees them
  (`paths.mappings.mediaServer` if its paths differ from Fukku's). With the default
  `mediaServer.refreshMethod: libraryRefresh` a new title shows up in about a second. With
  `pathUpdate` Jellyfin waits its `LibraryMonitorDelay` (60 seconds by default): lower it in
  Jellyfin's settings, or raise `mediaServer.itemWait`. A library that has never held a file is
  scanned once by `mediaServer.refreshFallback`; scanning it once yourself avoids the wait.
- **Bazarr** must be connected to Radarr and Sonarr as usual; Fukku only hands it the subtitle
  files that came with an upload, once Radarr or Sonarr has imported the video.

For second versions (`versions.enabled`): the web container needs the whole data folder as one
writable mount, `/path/to/data:/data`, in place of the read-only one and the intake one (the
compose example says where), `versions.root` on that mount and inside a
Jellyfin Movies library, and, for episodes, Sonarr's season folders writable by the Fukku user.
[settings.md](settings.md) (`versions`) has the full list; confirming a version checks it and
says what is wrong.

## The volumes, and why

| Mount | Why |
| --- | --- |
| `./fukku.yaml:/config/fukku.yaml:ro` | the configuration, read by both containers |
| `./state/data:/var/lib/fukku` (web) | the database, `fukku.db`. Local disk only, and one running process at a time (it has a lock) |
| `./state/spool:/spool` (both) | task and result files between web and worker; also local disk |
| `/path/to/data:/data:ro` and `/path/to/data/intake:/data/intake` (web) | the web process reads the sentinels and checks the library, and writes only the intake folder: uploads, jobs, and the files it takes over from the worker |
| `/path/to/data:/data` (web, with `versions.enabled` instead of the two above) | a second version is a hard link from the intake folder into the library, so both must be one writable mount. Without versions the web process never writes the library |
| `/path/to/data/intake/worker:/data/intake/worker` (worker) | the one folder the worker writes: its own folder of each job, for extracted files, preview clips and merges. The web process moves what it accepts out of it, one regular file at a time, and refuses links |
| `/path/to/data/intake/jobs:/data/intake/jobs`, read-only (worker) | the jobs' files: the uploads, and what the web process accepted. Read-only, so the worker cannot change a file after the web process checked it and handed it to Radarr or Sonarr, or linked it as a version |
| each of `paths.libraryRoots`, read-only (worker) | the worker reads a film's file to compare a dub with it and to merge (flow 2), only under these. Nothing else of `/path/to/data`: not the downloads, not `.incoming/` (the unfinished uploads) |

The worker checks this when it starts and refuses to start if it can write `jobs/`,
`.incoming/` or a library root, or cannot write `worker/` (`worker.requireSeparateMounts`,
[settings.md](settings.md)); the log line `worker_mounts` names the paths. The intake folders
are mounted with `create_host_path: false`: a missing one stops the container instead of being
created as root.

The root filesystems are read-only, all capabilities are dropped and `/tmp` is a tmpfs, for both
containers. Do not give the worker a network. Why the worker gets so little:
[threat-model.md](threat-model.md).

## The user id

Both containers run as the `user:` of the compose file. It must be the user that owns the
media files, because the web process creates the intake folders and links versions, and Radarr
and Sonarr, running as the same user, must be able to move the files out of them. The worker
runs as that user too: what keeps it from changing those files is its read-only mounts, not
its user id, so the files Radarr and Sonarr import keep their usual owner. The image
itself runs as 10001 if you give no user; then every mounted folder must be writable by it.

## Upgrading

```sh
cd ~/fukku
# change the tag in compose.yaml (the x-image line), then
docker compose pull
docker compose up -d
```

The database is migrated when the web process starts; a database written by a newer Fukku is
refused, so back up first ([backup.md](backup.md)). Jobs in progress carry on. Read the release
notes for settings that changed: `docker compose run --rm --no-deps web check-config` names any
that were removed.

## Backup

Back up `state/data/fukku.db` with SQLite's online backup, never with a file copy
([backup.md](backup.md)). Uploads in progress and the intake folder are not part of it.

## `mode: combined`, and why it is weaker

For a small host you can run one container instead of two: set `mode: combined` (or
`FUKKU_MODE=combined`) and start it with the command `combined`. The worker then runs inside the
web process, so the code that opens uploaded files shares the web process's network and the API
keys. A flaw in a parser then reaches your apps. Use split mode unless you cannot.

```yaml
services:
  fukku:
    image: *image          # the same x-image line as in the two-container file
    command: ["combined"]
    user: "1000:1000"
    read_only: true
    cap_drop: [ALL]
    security_opt: ["no-new-privileges:true"]
    tmpfs: ["/tmp:size=64m"]
    environment:
      FUKKU_CONFIG: /config/fukku.yaml
      FUKKU_MODE: combined
      FUKKU_RADARR_API_KEY_FILE: /run/secrets/radarr_api_key
      FUKKU_SONARR_API_KEY_FILE: /run/secrets/sonarr_api_key
      FUKKU_SUBTITLES_API_KEY_FILE: /run/secrets/bazarr_api_key
      FUKKU_MEDIA_SERVER_API_KEY_FILE: /run/secrets/jellyfin_api_key
    secrets: [radarr_api_key, sonarr_api_key, bazarr_api_key, jellyfin_api_key]
    volumes:
      - ./fukku.yaml:/config/fukku.yaml:ro
      - ./state/data:/var/lib/fukku
      - ./state/spool:/spool
      - /path/to/data:/data
    networks: [media, frontend]
```

(with the same `networks:` and `secrets:` sections as the two-container file). `web` and `worker`
refuse to start with `mode: combined`, and `combined` refuses without it, so a half-change is
caught.
