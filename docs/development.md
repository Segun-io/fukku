# Development

How to build, run and test Fukku from this repository. The rules for a change and for a pull
request are in [CONTRIBUTING.md](../CONTRIBUTING.md); how to run Fukku for real is in the
[README](../README.md) and [install.md](install.md).

The plan, the three flows and the milestones are in [plan.md](plan.md). How the UI looks and
sounds, and which component or token to use, is in [design.md](design.md). How the pinned
versions of Radarr, Sonarr, Bazarr and Jellyfin behave is in [compatibility.md](compatibility.md).
Releases follow [release.md](release.md).

## Tools

The host needs Docker (with Compose), make and git. Every other tool is pinned in the
development image ([tools/dev/Dockerfile](../tools/dev/Dockerfile)).

```sh
make hooks            # install the pre-commit hook (gitleaks on staged changes)
make check            # format, lint, unit tests, UI checks, workflow lint, secret scan
make build            # bin/fukku, with the UI built in
make image            # the product image, as fukku:dev
make fixtures         # look at the generated test media and hostile archives

make env-up           # local Radarr, Sonarr, Bazarr and Jellyfin, seeded with fake keys, a fake
                      # indexer and blackhole download clients
make probes           # behaviour probes that need no internet
make probes-online    # all probes (adding titles needs the *arr metadata services)
make compat           # write probe results into docs/compatibility.md
make probes-gate      # CI rule on the results: fail on differs or error, warn on upstream skips
make dev-up           # run Fukku in the test environment (see "Try it locally")
make env-reset        # delete the test environment
make install-test FUKKU_IMAGE=fukku:dev   # clean-room install from these documents (below)
make screenshots      # the README's screenshots (below)
```

`make help` lists every target.

The test environment uses fixed fake API keys and runs only on 127.0.0.1. The probes refuse
to talk to anything but its services. Test videos and hostile archives are generated at test
time and never committed. The indexer is a fake built from this repository
([test/env/indexer](../test/env/indexer)): it offers only the generated releases a probe gives
it, and a grab ends as an NZB file in a blackhole folder under `test/env/.run`; nothing is
downloaded.

## The version

The release version is written in one place: the image tag in the `x-image` line of
[deploy/compose.example.yaml](../deploy/compose.example.yaml). The release workflow checks the
tag against it, and [release.md](release.md) says how to change it.

## Try it locally

Fukku runs next to a local Radarr, Sonarr, Bazarr and Jellyfin (fake API keys, 127.0.0.1 only)
behind a small test reverse proxy that signs you in:

```sh
make image env-up dev-up      # build the image, start and seed the apps, start Fukku
# open http://127.0.0.1:18080
make dev-logs                 # follow the logs
make dev-down                 # stop Fukku; the test environment keeps running
```

The proxy offers three users, chosen with a cookie: open
`http://127.0.0.1:18080/__test/login?user=admin1` (or `user1`, `user2`) first. `user1` is the
default, `user2` a second ordinary user, `admin1` is in the admin group. Their addresses are
`<name>@example.com`. If `172.30.77.0/24` is taken on your host, set `FUKKU_DEV_SUBNET` to
another /24 before `make dev-up`. The full contract of the stack, including the proxy's control
API for tests, is in [test/env/compose.yaml](../test/env/compose.yaml).

Browser tests (Playwright, in a pinned image) run against this stack with `make e2e`; see
[test/e2e/README.md](../test/e2e/README.md). They include a 5 GiB upload that survives a reload,
a restart and dropped connections; `make e2e-large` runs it at 60 GiB (by hand, it needs about
65 GiB free under `test/env/.run`). The M2 exit criteria (a film and a season imported through
the test Radarr and Sonarr, kills of `fukku-web` while importing) add titles through the apps'
metadata services, so they need the internet; `make e2e E2E_ONLINE=0` skips them.

## The screenshots

The README's screenshots in [images/](images) are taken from this stack, with the generated
media and the test users, by [test/e2e/screenshots.ts](../test/e2e/screenshots.ts) in the
pinned Playwright image:

```sh
make screenshots              # writes docs/images/*.png
```

It uploads Big Buck Bunny and the Pioneer One season zip of `make e2e-fixtures`, adds the two
titles to the test Radarr and Sonarr through their metadata services (so it needs the internet),
and removes them again at the end. The viewport is 1200 by 800 in English; the target fails if a
picture is larger than 300 KB. Look at the pictures before you commit them: they must show no
hostname but 127.0.0.1 or a service name of the stack, and no real name or library.

## Layout

| Path | What |
| --- | --- |
| `cmd/fukku` | the binary: `web`, `worker`, `combined`, `check-config`, `healthcheck` |
| `internal/config` | the settings catalogue, YAML and `FUKKU_*` loading, validation |
| `internal/web`, `internal/worker` | the two processes |
| `internal/spool` | the task and result files between the two processes; `spooltest` has an in-memory queue |
| `internal/store` | the SQLite store: jobs and their state machine, files, uploads, audit log; `storetest` has the in-memory fake and the conformance suite |
| `internal/jobdir` | the layout of the intake root (`.incoming/`, `jobs/<id>/files`, `archives`, `worker/<id>`), and the checks of a job's file before it is handed over (no symbolic link on the way, regular, moved in from the worker's folder) |
| `internal/runner` | drives jobs through their states (uploaded → probing → probed), restart-safe |
| `internal/extract`, `internal/safename` | safe zip extraction in the worker; the file-name cleaner |
| `ui/` | the React single-page app, embedded in the binary |
| `test/fixtures` | ffmpeg media and hostile-archive generators |
| `test/install` | the clean-room install test: the steps of the README and [install.md](install.md), followed literally on a clean Docker-in-Docker host |
| `test/env`, `test/testenv`, `test/probes` | the local integration environment (including the dev stack, its test proxy and the fake indexer) and the behaviour probes |
| `test/e2e` | the Playwright tests and the README's screenshots |

## The clean-room install test

`make install-test FUKKU_IMAGE=<image>` checks that the documents are enough. It starts a clean
host (Docker-in-Docker, `docker:29.9.3-dind` pinned by digest: a container runtime and Compose,
nothing else), runs the pinned test Radarr, Sonarr and Jellyfin and a forward-authentication stub
in it, and then runs the install exactly as README.md ("Install in five steps") and
[install.md](install.md) say ([test/install/operator.sh](../test/install/operator.sh)), with Caddy
as in [reverse-proxy.md](reverse-proxy.md). It then checks `/readyz`, sign-in through the
proxy, and one film (flow 1) and one season archive (flow 3) imported through the HTTP API. The
media are generated at test time. Seeding the apps is scaffolding, kept apart from the install in
[test/install/run.sh](../test/install/run.sh).

`FUKKU_IMAGE` is a registry reference by digest (the clean host pulls it, as install step 2 says)
or an image of this machine such as `fukku:dev`, which is copied in with `docker save | docker
load`. While the package is private, the clean host cannot use the outer machine's login: give
it a token that can read the package with `GHCR_TOKEN_FILE=<file>` (or `GHCR_TOKEN`, and
`GHCR_USER`); it logs in with it over stdin and the token is never printed, and it logs out again
after the pull, so a host kept with `FUKKU_INSTALL_KEEP=1` holds no token. It needs the internet
for the apps' metadata services only (they add the free titles the probes use) and for image
pulls that are not cached, and takes about two minutes once the image exists. **Run it by hand
before a release**; it is not scheduled, because it needs a privileged container host and a full
image build ([prerelease.md](prerelease.md)). A step that the documents get wrong or leave out
makes it fail, and the fix goes into the documents.

When you change the README's install steps or [install.md](install.md), change
[test/install/operator.sh](../test/install/operator.sh) with them: it follows them literally,
and its comments name the step of each document it runs.
