# Threat model

What Fukku defends, against whom, with which controls, and what it accepts. It expands plan
§12 (security model), §10 (upload and file safety) and §17 risk 1, and was written with the
security review of issue #63 before the first public release. Each finding of that review
is a GitHub issue labelled `security`; the table at the end lists them. How to report a
vulnerability: [SECURITY.md](../SECURITY.md).

Keep this document in step with the code: a change that moves a trust boundary, adds a
direct write into the library, a new subprocess or a new input from the network updates it.

## 1. The system

```text
                 ┌──────────────────────────── host ─────────────────────────────┐
 browser ──TLS──►│ reverse proxy ──HTTP──► fukku web ──HTTP+API key──► Radarr     │
 (user)          │ (signs users in,         │  ▲  │                    Sonarr     │
                 │  sets Remote-User…)      │  │  └─────────────────► Bazarr     │
                 │                          │  │                      Jellyfin   │
                 │              spool files ▼  │ result files                    │
                 │                       fukku worker (network_mode: none)       │
                 │                                                               │
                 │ /data: intake root: jobs/ (web rw, worker ro), worker/ (both  │
                 │        rw), .incoming/ (web only); library (web rw only with  │
                 │        versions.enabled, worker ro: paths.libraryRoots only), │
                 │        versions.root                                          │
                 └───────────────────────────────────────────────────────────────┘
```

- **web** (`fukku web`): the HTTP API, the embedded UI and the tus upload endpoint. It holds
  every API key, decides every state change of a job, and talks to the apps. It never
  parses a media file. It opens files the worker produced in two places: it streams the
  preview clips the worker rendered to the job's owner (`internal/web/preview.go`), and it
  passes an accepted subtitle file to the subtitle manager unparsed. It also writes the
  unfinished tus uploads in `.incoming/`.
- **worker** (`fukku worker`): extracts archives, runs ffprobe, renders preview clips with
  ffmpeg, merges with mkvmerge. No network, no secrets; refuses to start if its
  configuration holds an API key (`internal/config/validate.go`).
- **spool**: a directory both mount; the web process writes task files, the worker claims
  them and writes result files (`internal/spool`).
- **`mode: combined`** runs both in one process and one container. It gives up the
  separation in §3 (B4) and is documented as weaker; everything below assumes `split`
  unless it says otherwise.

## 2. Assets

| Asset | Where | Why it matters |
| --- | --- | --- |
| The media library | Radarr's and Sonarr's root folders, `versions.root` | Hard to replace (ripped discs, home recordings); Radarr and Sonarr run without a recycle bin in the target setup, so a replaced file is gone |
| API keys of Radarr, Sonarr, Bazarr, Jellyfin | Environment or `*_FILE` secrets of the web process | Full control of those apps, including deleting titles with their files, changing download clients and running their scripts |
| The apps' state | Radarr, Sonarr (titles, monitoring, profiles), Jellyfin (groupings) | A title unmonitored or added without the user meaning it changes what the library downloads |
| Users' uploads | `<intake>/.incoming/`, `<intake>/jobs/<job>/` | Private recordings; one user must not see or change another's |
| Identity and roles | Headers from the proxy; `auth.adminGroup` | Admins see everyone's jobs, error details with paths, the settings and the versions list |
| The database and audit log | `server.dataDir` (SQLite) | Who uploaded, confirmed, overrode, merged or replaced what |
| Host resources | Disk of the intake filesystem, CPU and memory of the worker | A full disk stops Radarr's and Sonarr's own imports too |
| The release artefacts | GHCR image, SBOM, workflow | Users run what the image contains |

## 3. Trust boundaries

| # | Boundary | Who is on the far side | What crosses it | Main controls |
| --- | --- | --- | --- | --- |
| B1 | Browser → reverse proxy | Any user of the site, any web page the user visits (CSRF), anyone on the internet if the proxy is public | HTTP requests, uploads, cookies of the proxy's sign-in | The proxy signs users in (out of Fukku's hands); Fukku's Origin check, same-origin API, CSP |
| B2 | Reverse proxy → web | The proxy, trusted to authenticate and to set and strip the identity headers | `Remote-User`, `Remote-Email`, `Remote-Groups` and the request | `auth.trustedProxies` matched against the connection's own address only; one usable user header required; repeated headers refused (`internal/auth/middleware.go`) |
| B3 | Network neighbours → web | Any container or host that can reach the web port | Requests that bypass the proxy | The same address check: everything not from `trustedProxies` gets 403, including `/readyz` and `/metrics` (loopback only, or an admin) |
| B4 | web ↔ worker (spool) | The worker, which parses hostile files and must be assumed compromised | Task files (web → worker), result files (worker → web), files the worker writes into its own folder of a job (`worker/<job>`) | The worker has no network and no secrets (it refuses to start with an API key, a `_FILE` secret or a URL with credentials); the web process validates results (size, ids, kinds), takes the files it accepts into the job's directory one checked regular file at a time (`jobdir.Seal`), and opens every job file it hands over or links without following a link (`jobdir.OpenFile`) |
| B5 | Worker → filesystem | The worker | Reads of uploads and library files, writes into its own folder of the job | Its container mounts only `intake/worker` writable, `intake/jobs` and each of `paths.libraryRoots` read-only, nothing else of `/data` (no `.incoming/`); it refuses to start when it can write more (`worker.requireSeparateMounts`); `os.Root` confinement to the job's folders; library reads only under `paths.libraryRoots` |
| B6 | web → Radarr, Sonarr, Bazarr, Jellyfin | The apps, trusted as the owners of the library | API calls with the keys; titles, paths and verdicts back | Keys only in headers, never logged; response bodies capped; paths from the apps are mapped (`internal/pathmap`) and checked against `paths.libraryRoots` before the web process writes |
| B7 | web → library (direct writes) | — | Second versions: films' into `versions.root`, episodes' into Sonarr's season folders | Only with `versions.enabled` (off by default; without it the web container mounts the library read-only); hard links with `linkat(2)` that never replace a file, made from the job's file as it was opened and checked; opened through an `os.Root` of the library root; trial link and overlap checks; Sonarr's "not an upgrade" verdict for episodes |
| B8 | Supply chain → image | Upstream projects and registries | Go modules, npm packages, Debian packages, FFmpeg source, base images, Actions | Pins by version and hash or digest, `go.sum` and the npm lockfile with integrity hashes, the FFmpeg tarball by SHA-256, Actions by commit SHA, an SBOM per image |

## 4. Attackers

- **A1, an upload user.** Signed in through the proxy, not an admin. Can create jobs, upload
  anything up to the limits, pick any title in Radarr or Sonarr, edit the proposal, override
  rejections and confirm imports. The reference deployment lets friends and family upload,
  so A1 is not assumed benign.
- **A2, a hostile web page** in the browser of a signed-in user: cross-site requests with
  the user's cookies.
- **A3, a network neighbour**: another container on the media network or a host on the LAN
  that can reach the web port directly.
- **A4, a compromised worker**: code execution inside the worker through a parser bug
  (FFmpeg, MKVToolNix, Go's zip reader), reached by an upload of A1.
- **A5, a hostile upstream**: a poisoned dependency, base image or Action.

Out of scope as attackers: the admin, the host, the reverse proxy, and Radarr, Sonarr,
Bazarr and Jellyfin themselves (see §7).

## 5. Threats and controls

### 5.1 Authentication and requests (B1–B3)

| Threat | Control | Where |
| --- | --- | --- |
| A3 sends `Remote-User: admin` straight to the web port | Only connections from `auth.trustedProxies` are accepted; `X-Forwarded-For` and similar never count; IPv4-mapped IPv6 is unmapped before the match | `internal/auth/middleware.go` (`remoteAddr`, `isTrusted`) |
| A1 adds a second `Remote-User` or `Remote-Groups` that a proxy appends instead of replacing | A repeated identity header is refused with 403 | `identityHeader` |
| A1 sends control characters, invalid UTF-8 or a huge user name to confuse logs or the owner check | Refused; user and email at most 256 bytes | `identityHeader` |
| A1 forges `Remote-Groups` through a proxy that sets only the user header | Not preventable by Fukku: the proxy must strip and set every header Fukku reads. Documented in `docs/settings.md` (`auth`); `auth.groupsHeader` is empty by default, `auth.adminGroup` needs it, and `fukku check-config` warns about an Origin check that trusts `Host` | proxy configuration |
| A2 makes the browser POST, PATCH or DELETE to the API | Every method but GET, HEAD and OPTIONS needs exactly one `Origin` equal to `server.publicBaseUrl`'s origin (or the request's `Host` when that is unset); `null` and missing are refused; tusd's `X-HTTP-Method-Override` is removed; no GET changes state; no CORS headers | `internal/auth/origin.go`, `internal/uploads/uploads.go` |
| A2 frames the UI (clickjacking) | `frame-ancestors 'none'` | `internal/web/server.go` (`securityHeaders`) |
| A1 or A2 injects script into the UI | React escapes text; the CSP allows scripts and styles only from the origin (`default-src 'self'`, no `unsafe-inline` or `unsafe-eval`, `object-src 'none'`), and `Cross-Origin-Resource-Policy: same-origin` keeps other sites from embedding a clip; `base-uri 'none'`, `form-action 'self'` | `securityHeaders`, `ui/src` |
| A1 reads `/readyz` or `/metrics` for paths and counts | Loopback without a user (the container's health check), anyone else must be an admin | `Middleware` |
| A1 reads the API key through a redirect | Not yet: the *arr and Bazarr clients follow redirects and resend their key header to another host (#119) | `internal/arr/client.go`, `internal/subtitles/bazarr.go` |
| A1 sends large JSON bodies | 1 MiB cap outside `/api/uploads/`, decided by the route the mux picks, not the raw path | `internal/web/respond.go` (`limitJSONBody`) |

### 5.2 Authorisation (A1)

| Threat | Control | Where |
| --- | --- | --- |
| A1 reads or changes another user's job | Every `/api/jobs/{id}…` handler loads the job through `ownJob`: malformed id, unknown id and someone else's job are all 404; admins see every job | `internal/web/jobs.go` (`ownJob`), `match.go`, `proposal.go`, `confirm.go`, `preview.go` |
| A1 resumes, reads or deletes another user's upload | The upload row's owner must be the user, else 404; uploads into another user's job are refused at creation | `internal/uploads/uploads.go` (`lookup`), `create.go` |
| A1 reaches admin pages | `adminOnly` on `/api/admin/*`; error details naming paths and services only for admins | `internal/web/admin.go`, `versions.go` |
| A1 replaces or changes library files of any title | `auth.libraryChangesRequireAdmin` (on by default, #116): only an admin (`auth.adminGroup`, or everyone with `auth.allUsersAreAdmins`) may confirm an override of a Radarr or Sonarr rejection or of `existing_file` (and a `shared_file` one), the season-dub policies `replace` and `version`, a flow 2 merge or a film's second version; others get 403 `admin_required`, and the proposal and the job list those options in `notOffered` with `admin_required`. Fukku's own warnings on a new file stay open, and so do new films and episodes. Each submission of an import, a retry, a resume after a stall or a crash included, checks again that it replaces no file the title got since the confirmation unless that confirmation chose `replace` for the row: before any submission the job goes back to `probed`, after one it fails with `library_has_file`. Every step is audited. With the default `auth.adminGroup` (empty) nobody is an admin: the web process logs `config_warning` and `fukku check-config` warns | `internal/flows/library_changes.go`, `confirm.go`, `seasondub.go`, `import.go` (`replacesUnchosen`), `internal/config/warnings.go` |

### 5.3 Uploads (tus) and limits

| Threat | Control | Where |
| --- | --- | --- |
| Path traversal in the tus `filename` | `safename.Clean`: NFC, control and bidi characters removed, `/`, `\`, drive letters, `.`/`..` refused, at most `extract.maxNameBytes`; leading dot refused; the stored name is the cleaned one | `internal/uploads/create.go`, `internal/safename` |
| Disk exhaustion | `upload.maxFileSize` per file, `upload.maxInFlight` over all unfinished uploads, `upload.maxParallelPerUser`, free space minus what unfinished uploads still need must exceed size + `upload.freeSpaceMargin`; deferred length and concatenation refused; a PATCH carries at most `upload.chunkSize` | `create.go`, `uploads.go` |
| Slow or stuck clients | `upload.requestIdleTimeout`; idle uploads expire after `upload.idleUploadExpiry`; idle jobs expire | `internal/retention` |
| An upload id used as a path | Ids are 32 random hex characters, checked by `jobdir.ValidID` before anything touches the disk | `internal/uploads/uploads.go`, `internal/jobdir` |
| Upload metadata under the intake root steers the endpoint's reads and writes (the worker can write the intake root, A4) | Fukku's own tus data store (`incomingStore`), not tusd's filestore: every path is computed from the upload id, never taken from an `.info` file; an `.info` that names any other place than `.incoming/<id>` and `.incoming/<id>.info`, or another id, is refused; every open goes through an `os.Root` of `.incoming/`, which must be a real directory of the intake root (a symbolic link there is refused, also when swapped in while running); upload files must be regular files and not links, checked before and after each open; upload data that is not a regular file is never moved into a job; an `.info` with a deferred or negative size, concatenation, or a size other than the upload row's is refused, and so is data past that size; the headers of the IETF resumable-upload drafts are removed, so the endpoint speaks tus 1.0 only. Fixed in #137. Only paths, file types and these fields are enforced: the upload's data and its other metadata stay writable by the worker; since #138 the worker has no view of `.incoming/` | `internal/uploads/incoming.go`, `finish.go` |
| Many jobs | Idle jobs expire; no per-user cap yet (#134) | `internal/web/jobs.go` |

### 5.4 Hostile files (worker, A1 → A4)

| Threat | Control | Where |
| --- | --- | --- |
| Zip-slip, absolute names, drive letters, backslashes | Refused on the raw name; each segment then goes through `safename.Clean`, and one that becomes empty, `.` or `..` counts as traversal; at most 200 bytes a segment, 32 segments, 1024 bytes; extraction writes through `os.Root` of the job directory into a scratch folder renamed into place, so a refusal leaves nothing | `internal/extract` |
| Duplicate and colliding names, overwrites | Compared after cleaning, NFC and case folding; files created with `O_EXCL`, so nothing is overwritten | `internal/extract` |
| Symbolic links, devices, FIFOs in archives | Only regular files and directories are extracted; the rest is skipped and listed | `internal/extract` |
| Zip bombs | The central directory is read under a byte budget and counted against `extract.maxEntries` before anything is written; bytes counted as written; stop at `extract.maxWrittenBytes`, the free-space margin (checked every 64 MiB), or a written/compressed ratio above `extract.maxRatio`; a declared size that lies, or overlapping entry data, refuses the archive; encrypted entries skipped; nested archives never opened | `internal/extract` |
| A parser bug in FFmpeg, MKVToolNix or Go | The worker has no network and no secrets; read-only root filesystem; no capabilities; `no-new-privileges`; `pids_limit`; per-file and per-task deadlines with the whole process group killed; tool output capped (ffprobe 4 MiB, mkvmerge identification 4 MiB, clips by size); no memory limit yet (#117) | `deploy/compose.example.yaml`, `internal/worker`, `internal/media`, `internal/preview`, `internal/merge` |
| A file that makes ffprobe or ffmpeg open other files or URLs (playlists, references) | Uploads are read with `-format_whitelist matroska,webm,mov,mp4,avi,mpegts` and `-protocol_whitelist fd` from an inherited descriptor, so an HLS or concat file named `.mkv` is refused and nothing else can be opened (checked in the review); MOV references (`enable_drefs`) are off; the product's FFmpeg is built with `--disable-network` and only the `file`, `pipe` and `fd` protocols ([image.md](image.md)); playlist demuxers are refused for library probes by configuration validation | `internal/media/probe.go`, `Dockerfile`, `internal/config/validate.go` |
| Arguments taken as options (`-…`, mkvmerge's `@options-file`) | No file name reaches an argument list: ffprobe reads `fd:`, mkvmerge `/proc/self/fd/N`; user values go to mkvmerge as `<track id>:<value>` (a track name starting with `@` stays a name, checked in the review); track names are printable UTF-8 of at most 256 bytes and language tags canonical BCP 47; the preview's filter graph holds only integers; the tools get a minimal environment | `internal/media/probe.go`, `internal/merge`, `internal/preview`, `internal/config/language.go` |
| A user value out of range (offset, clip count) | Preview requests are checked: offset within ±60 s, 1 to 10 clips of at most 2 minutes, request id `[0-9a-f]{16}`; clip files are named from the stored request, never from the worker's result | `internal/preview` |
| Huge metadata | Not yet bounded per string: a probe result can exceed the spool's 16 MiB cap and wedge its job (#115) | `internal/media/probe.go`, `internal/spool` |

### 5.5 The worker as an attacker (A4)

The split exists so that a compromised worker gains little. What it can still do, and what
stops more:

| Threat | Control | Where |
| --- | --- | --- |
| Reach Radarr, Sonarr or the internet | `network_mode: none` | compose |
| Read API keys | Not in its environment or configuration (it refuses to start with one); not in the spool | `internal/config/validate.go` |
| Write anything but its own scratch space | Only `intake/worker` is mounted writable. The jobs' files (`intake/jobs`) and the library roots are read-only mounts, so the names the web process made of a file it checked, handed to Radarr or Sonarr or linked as a version (the job's, a placed version's, an import made with hard links) cannot be opened for writing, whatever the file's owner or mode (#114); a name the worker makes for such a file in its own folder, through a descriptor it kept, is the next row. `.incoming/`, the downloads and the rest of `/data` are not mounted at all (#118, #138). `fukku worker` refuses to start when it can write `jobs/`, `.incoming/`, a library root or `versions.root`, or, on the host's filesystem, the intake root or the folders above it and above the library roots (`worker.requireSeparateMounts`, `internal/worker/mounts.go`). The two processes keep one user id, so imported files keep their owner: the mounts, not the uid, are the boundary | compose, `internal/worker/mounts.go` |
| Read other users' files | Not prevented: the worker processes every job, so it reads every job's files and the library roots, and could put what it reads into what it produces for its own job (preview clips, a merge, extracted files): accepted, §6 | compose |
| Forge results (another job, huge files, odd kinds) | Spool access through an `os.Root`, writes to a random `O_EXCL` name then fsync and rename, reads non-blocking, regular files only, at most 16 MiB; ids by regex and equal to the file name; the web process matches each result to what it asked (probe results by stored path, extracted paths under the destination, merge output, tracks and library echo, preview request echo and clip count, library-probe path) | `internal/spool/dir.go`, `internal/runner/steps.go`, `internal/flows` |
| Plant symbolic links or swap files under its folder of the job (or, where it can write it, the job directory) that the web process later opens, links or hands to an app | Every job directory and the worker's folder of it are opened with `jobdir.Open` and `jobdir.OpenWorker`, from an `os.Root` of the intake root, so a link at `jobs/`, `worker/` or below cannot lead out (#112); job directories and uploads are removed through an `os.Root` of the intake root (`jobdir.Remove`, `RemoveUpload`), so a link at `jobs/`, `worker/` or `.incoming/` that leads out of it is refused and a link at `jobs/<id>` is removed itself, never followed (#137); a test fails if non-test code outside `internal/jobdir` uses `jobdir.Dir` outside a reviewed allow-list of callers (paths handed to an app, the free-space check), or hands `jobdir.Dir(...)` directly to a package `os` function. An accepted subtitle was first opened through the job's root with an `Lstat` and same-file check (#111), as `os.Root` follows a link that stays inside it. Since #113 every job file the web process reads, hands over or links is opened one path element at a time with `O_NOFOLLOW` from the job's root (`jobdir.OpenFile`): a symbolic link anywhere on the way, also one that stays inside, is refused, and so is anything but a regular file; the accepted subtitle, the preview clips (in the worker's folder, of the size it reported) and the staging of episode versions go through it. What the worker extracted or merged is taken from its folder into the job's by `jobdir.Seal`: the file is opened without following a link and checked (a regular file of the reported size with no other hard link), that open file is hard-linked into the job (`linkat(2)` of its `/proc/self/fd` entry, never replacing a name), and the worker's name is removed when it is still that file. Whatever the worker swaps in under the name meanwhile (a link, another file, a directory it keeps open) is never moved: nothing but the checked regular file gets a name under the job's directory, and if the file still has another name afterwards the job's name is removed again and the job fails. Right before a path goes to Radarr or Sonarr it is checked again the same way, with the recorded size and exactly one name (`jobdir.CheckFile`); a version is hard-linked from the open, checked file (`linkat(2)` of its `/proc/self/fd` entry), and the new name is checked to be that file. The apps open the path themselves later; no name in the path can change in between, because the job's directory is read-only to the worker. The file's content still can, through a descriptor the worker kept to a file it wrote itself (next row) | `internal/jobdir`, `internal/subtitles/step.go`, `internal/web/preview.go`, `internal/runner/steps.go`, `internal/flows` |
| Keep writing a file it produced | A worker compromised before it wrote an extracted file or a merge may keep a descriptor to it. Through it the worker can write the file after it was taken into the job and imported, and can give it a new name in its own folder (`linkat` of its own `/proc/self/fd` entry), a writable name of the job's file, of a placed version or of an imported library file. The file is not copied at the hand-over (a film is tens of gigabytes). Checked against: right before each hand-over to Radarr or Sonarr and each version link the file must have exactly the names the web process made (the job's, a staged version's), else it is refused (`jobdir.WantLinks`); every retention sweep (`upload.sweepInterval`) and every start of the worker remove from `worker/` each regular file with a second name, each link or special file, every directory deeper than a job's folders go, and every entry that is no job's folder (`jobdir.WorkerSweeper`), and a job's folder there goes once the job is done. A sweep looks at no more than `worker.sweepMaxEntries` entries, reads each directory 1024 entries at a time and holds one descriptor per level, so a worker that fills its folder with millions of entries or a very deep tree slows the sweep down (the next one carries on) but cannot exhaust the web process's memory or descriptors; a job folder with more than half that many entries is removed as a whole. A name made after the hand-over can live until the next sweep, and the descriptor itself as long as the worker process does. Only files the worker wrote itself are reached, whose content it chose anyway: accepted, §6 | `internal/jobdir` (`Seal`, `WantLinks`, `WorkerSweeper`), `internal/retention` |
| Stay in control across jobs | The worker is one long-lived process for every job (`worker.concurrency` tasks at a time), and the tools it starts run as the same user in the same container. Code that a hostile upload got running there (for example a child process that left the tool's process group) can stay until the container restarts, and then chooses what every later job's tasks produce: the extracted files, merges and preview clips of other users' uploads, and their probe results. It still gets no network, no keys and no writable name in `jobs/`. Restarting the worker container ends it; nothing in Fukku restarts it per job: accepted, §6 | `internal/worker` |
| Write upload metadata that the web process acts on | See §5.3: nothing in `.incoming/` is taken as a path, symbolic links and files that are not regular are refused, and an `.info` whose shape the endpoint never creates (deferred or negative size, concatenation) or whose size differs from the upload's row is refused (#137). The upload's data and the other fields of its `.info` (its metadata) are not checked further; since #138 the worker has no view of `.incoming/` | `internal/uploads/incoming.go` |
| Produce a bad merge that replaces a film | The worker verifies its own merge, so a compromised worker can make Radarr import a file of its choosing for the job's film: accepted, §6 | `internal/merge` |

### 5.6 Library writes (B7)

| Threat | Control | Where |
| --- | --- | --- |
| A second version replaces a library file | `linkat(2)` never replaces; a repeat finds its own link by inode | `internal/flows/version_link_linux.go` |
| A label or a name from Radarr or Sonarr escapes the target folder | Labels go through `safename.Clean`, refuse `<>:"\|?*`, format characters, leading or trailing dots and forbidden tokens, at most 64 bytes (a label with fullwidth or other compatibility characters, which are not NFKC-normal, is refused); the film folder from Radarr must be one clean path element; the season folder must be inside `paths.libraryRoots` (a `..` in Sonarr's path is cleaned rather than refused, #120); both are opened through an `os.Root` and never created outside it | `internal/flows/version.go`, `episode_version.go` |
| Path-mapping prefix confusion (`/media/tv` against `/media/tv2`) | Mappings and library roots match whole path segments only | `internal/pathmap`, `internal/media/library.go` |
| Radarr adopts a film's version as the tracked file | Films' versions live outside Radarr's root folders (`versions.root`, checked not to overlap any, symlinks resolved) | `internal/flows/version.go` |
| Sonarr adopts an episode's version | Sonarr's Manual Import proposal must reject it as "not an upgrade" at the confirmation and right before the link; the keeper re-checks and reports `adoptable`/`adopted` | `internal/flows/episode_version.go`, `internal/mediaserver/episode_recheck.go` |
| A missing mount makes writes land on the host disk | Sentinel files must exist before any write; trial hard link from the intake root | `internal/fscheck` |

### 5.7 Secrets

| Threat | Control | Where |
| --- | --- | --- |
| Keys in logs, errors or the admin page | `config.Secret` prints `[redacted]` in every format (`String`, `GoString`, `LogValue`, `MarshalText`); the *arr client scrubs the key from error text; keys go only in headers | `internal/config/types.go`, `internal/arr/client.go` |
| Keys in the repository | Only from the environment or `*_FILE`; gitleaks in `make check` and the pre-commit hook | `.gitleaks.toml` |
| Keys in the worker | Refused at start-up; not in the spool | `internal/config/validate.go` |

### 5.8 Supply chain and release (A5)

| Threat | Control | Where |
| --- | --- | --- |
| A moved tag or a poisoned release of a dependency | Every `uses:` pinned to a full commit SHA; the Go, Node and runtime base images by digest; Debian packages by version from a dated snapshot (signatures still checked); FFmpeg by the SHA-256 of its tarball; Go modules by `go.sum`; npm packages by exact version and the lockfile's integrity hashes, with `ignore-scripts=true` | `Dockerfile`, `tools/dev/Dockerfile`, `.github/`, `go.sum`, `ui/package-lock.json`, `ui/.npmrc` |
| A workflow that runs untrusted input with write access | `permissions: contents: read` at the top of every workflow; `packages: write` only in the publish job behind a protected environment; no `pull_request_target` or `workflow_run`; inputs passed through `env`, never interpolated into `run:` | `.github/workflows/` |
| A known-vulnerable dependency ships | Not yet scanned (#128); setuid programs left in the image (#126); checkouts persist the token (#131) | |
| Users cannot tell what the image contains | An SBOM per CI image build (Debian packages and Go modules; npm packages missing, not attached to the published image yet, #127) | `.github/actions/build-image` |

## 6. Accepted risks

- **The web process holds API keys that can delete library files.** That is inherent to
  driving Radarr and Sonarr, and it is why the web process never parses uploads. Whoever
  takes over the web process owns the apps.
- **The web process writes into the library when `versions.enabled` is on**: films' versions
  into `versions.root`, next to Radarr's root folders, and, since #58, episodes' versions
  into Sonarr's own season folders. Its mount of the library must then be writable (one
  mount with the intake root, for the hard link); with versions off the compose example
  mounts it read-only. The writes are hard links that never replace a file, but a bug in the
  path checks would be a write into the library with the web process's rights.
- **Any signed-in user can add titles to Radarr and Sonarr and import new files into them.**
  The steps that replace or change a library file (an override of a rejection or of
  `existing_file`, the season-dub policies `replace` and `version`, a merge into a film's
  file, second versions) are admin-only while `auth.libraryChangesRequireAdmin` is on (the
  default, #116). A deployment that turns it off (a single household) accepts that every
  user can replace library files; the audit log records who did it.
- **A retry repeats an earlier confirmation.** The admin check is made when a job is
  confirmed; a retry or a resume submits what was confirmed then, so the owner can retry an
  admin's confirmation of their upload (a merge, `replace`), or one made before an upgrade
  to #116 or while the setting was off. Each submission checks that it replaces no file the
  title got since, other than the file a `replace` row had at the confirmation (a merge's
  film file is checked against the match's); a grab that lands between that check and the
  app running the ManualImport (a command still queued or running is not checked again)
  can still be replaced.
- **`subtitlesOnly` and the subtitle hand-over are open to every user.** They import no
  video; Bazarr writes the job's subtitle file beside the episode's or film's file, and may
  overwrite an earlier subtitle of the same language and flags (as its own upgrades do). A
  subtitle is cheap to get again, and Bazarr's history records the upload.
- **A compromised worker can choose what Radarr imports for its own job** (a merge it
  "verified", files it "extracted"). It cannot reach the network or the keys, and it cannot
  change an upload or any other file it did not write itself: those are on its read-only
  mount (#113, #114, #138). Files it wrote itself: the next bullets.
- **A compromised worker reads every job's files and the library roots.** It processes every
  job, so its read-only mounts hold every user's uploads; it could put what it reads into
  what it renders for its own job (a preview clip, a merge) and so show it to that job's
  owner. Static mounts cannot narrow this to one job at a time (#118).
- **A file the worker produced is not copied when it is handed over.** A worker that was
  compromised before it wrote an extracted file or a merge can keep a descriptor to it and
  change the file later, even after Radarr or Sonarr imported it (by a move or a hard link,
  the library file is that inode). Through that descriptor it can also give the file a new,
  writable name in its own folder after the hand-over. A second name is refused right before
  every hand-over and version link, and removed from `worker/` at every retention sweep and
  every start of the worker, but one made after the hand-over lives until the next sweep.
  It reaches only files it wrote itself, whose content it chose anyway; a copy would close
  this at the cost of reading and writing a whole film (tens of gigabytes) at each hand-over,
  on top of the merge's own copy (#114).
- **A compromised worker stays compromised for later jobs.** It is one long-lived process for
  every job, so code a hostile upload got running in it can choose what it produces for
  every later job, of every user, until the worker container is restarted. The worker has no
  network, no keys and no writable name in `jobs/`, so that reaches the outputs of later
  jobs (and what Radarr imports from them), not the library or the apps directly.
- **`mode: combined`** puts the parser next to the keys and the network. It is for small,
  trusted installs and is documented as weaker.
- **Same-host proxy on loopback.** If the proxy connects from a loopback address, `/readyz`
  and `/metrics` are open to whoever the proxy lets through (documented under
  `auth.trustedProxies`).
- **Sync is judged by a person** (plan §17 risk 5): a drifting dub can be merged.

## 7. Out of scope

- The reverse proxy and the identity provider: signing users in, session lifetime, TLS,
  HSTS, rate limiting, and stripping client-supplied identity headers.
- Radarr, Sonarr, Bazarr and Jellyfin: their own vulnerabilities, and what they do with a
  file once they own it.
- The host: the container runtime, the kernel, other containers with access to the same
  volumes, backups of the database.
- An admin acting in bad faith, and anyone who can edit the configuration or the compose
  file.
- Physical access, and denial of service by an unauthenticated client against the proxy.
- `auth.mode: none` (refuses to start without `--insecure-dev`).

## 8. Review of issue #63

The review read the code against this model, area by area, and attacked the running dev
stack (`make dev-up`, the test proxy of `test/env`) for header spoofing, cross-site requests
and upload abuse.

What held up is the "Control" column of §5: no way was found for an upload user, a
cross-site page or a network neighbour to get past authentication, the Origin check, the
ownership checks, the tus limits, the extraction rules or the subprocess argument
handling. The live checks against the dev stack: identity headers sent straight to the web
process (also with `X-Forwarded-For` naming the proxy) and `/readyz` and `/metrics` from
another container all got 403; a user's own `Remote-User` and `Remote-Groups` were replaced
by the proxy; `POST /api/jobs` without an `Origin`, with a foreign one, `null`, two of them,
or one with a path got `origin_mismatch`, and a `POST` with `X-HTTP-Method-Override: DELETE`
too; another user's job and upload answered 404 to `GET`, `HEAD`, `PATCH`, `DELETE` and
cancel; tus names with `..`, backslashes, absolute paths, drive letters, a leading dot or
more than 200 bytes were refused, control and bidi characters removed; an oversized or
negative `Upload-Length`, a deferred length, a fourth parallel upload, a `PATCH` past the
upload's size and one at a wrong offset were refused.

The weak spots are where the web process trusts what the worker leaves in the intake root,
which matters only after a parser exploit, and a few limits and defaults. An independent
review of the fixes found a second high-severity finding of that kind, in the tus upload
store (#137). Both high findings are fixed in the same pull request; nothing of high
severity is open.

| Issue | Severity | Area | Status |
| --- | --- | --- | --- |
| #111 | high | files left by a compromised worker could steer the subtitle hand-over's reads | fixed in the #63 pull request |
| #137 | high | worker-controlled upload metadata could steer the web process's writes | fixed in the #63 pull request |
| #112 | medium | a symlink at `jobs/<id>` redirects the web process's job-directory operations (the staging of episode versions is left to #113) | fixed in the #63 pull request |
| #113 | medium | worker-written paths with symlinked parts handed to Radarr, Sonarr and `linkat` | fixed: no-follow opens, moves into the job, links from the open file |
| #114 | medium | hard links and a shared uid let a compromised worker change placed files | fixed: the job directory is read-only to the worker; second names refused at hand-over and swept from `worker/`; a descriptor kept to its own output is accepted (§6) |
| #115 | medium | huge track titles wedge a job in probing | open |
| #116 | medium | any user can replace library files (decided: admin-only by default, behind a setting) | fixed: `auth.libraryChangesRequireAdmin`, and a check before each submission |
| #117 | medium | no memory limits | open |
| #118 | medium | worker sees all of `/data` and every job; web has the library writable | fixed: library roots and jobs read-only, nothing else; web read-only without versions; reading every job accepted (§6) |
| #138 | medium | worker should write only `intake/jobs` (follow-up of #137, part of #118) | fixed: it writes only `intake/worker`, reads `intake/jobs`, has no `.incoming/` |
| #119 | medium | API keys follow redirects to other hosts | open |
| #120 | low | Sonarr's season folder cleaned instead of refused | open |
| #121 | low | fullwidth characters pass the label checks | fixed: a label that is not NFKC-normal is refused (`version_label`) |
| #122 | low | extracted names may start with a dot; finished uploads not re-cleaned | open |
| #123 | low | merge free-space check trusts the upload's size tags | fixed: the merge counts the upload's fstat size |
| #124 | low | process-group kill after reaping can hit a reused PID | fixed: `internal/procgroup` runs probe, merge and preview |
| #125 | low | worker's no-secrets check covers only API-key settings | fixed: the worker also refuses `_FILE` secrets and URLs with credentials |
| #126 | low | setuid binaries in the image | open |
| #127 | low | SBOM incomplete and not attached | open |
| #128 | low | no dependency vulnerability scanning | open |
| #129 | low | CSP tightening and `Cross-Origin-Resource-Policy` | fixed: `style-src 'self'`, `object-src 'none'`, `Cross-Origin-Resource-Policy`; e2e fails on a violation |
| #130 | low | `mediaServer.titleLinkTemplate` not validated | fixed: validated as an http(s) URL with `{itemId}`; the id is escaped |
| #131 | low | CI checkouts persist the token | open |
| #132 | low | `/assets/` listing and source maps | fixed: directories and `.map` files are 404; no source maps in the build |
| #133 | low | auth configuration foot-guns | fixed: no groups header by default, wide `trustedProxies` refused, Origin-from-Host warning |
| #134 | low | no per-user job limit | open |
