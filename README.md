# Fukku

**Status: pre-alpha.** Nothing here is ready to run against a real library yet.

Fukku (フック, "hook") is a small self-hosted web app for adding video that no indexer will
ever provide (a ripped disc, a home recording, another language of a film you already have,
a zipped season) to a Radarr and Sonarr library. You upload in the browser. Fukku checks the
upload, asks Radarr or Sonarr what they make of it, and lets you confirm. Then **Radarr or
Sonarr import it through their own Manual Import API**, so their naming, quality and upgrade
logic stay in charge, and the media server, the subtitle manager and request apps see it like
a normal download.

- **Radarr and Sonarr stay the only writers of the library.** Fukku never renames or files
  media itself. The one exception is opt-in second versions (a dub kept next to the
  original), placed as hard links that never replace a file.
- **Built for untrusted uploads.** A separate worker process with no network and no
  credentials opens the uploaded files. Archives are extracted with limits, and nothing is
  ever overwritten.
- **Every deployment-specific behaviour is a setting**, with a default
  ([docs/settings.md](docs/settings.md)).
- **Easy to run:** one OCI image, two containers, configuration by environment variables or
  one YAML file, behind any reverse proxy.

The plan, the three flows and the milestones are in [docs/plan.md](docs/plan.md). How the
pinned versions of Radarr, Sonarr, Bazarr and Jellyfin behave is in
[docs/compatibility.md](docs/compatibility.md). How the UI looks and sounds, and which
component or token to use, is in [docs/design.md](docs/design.md).

## Running it (preview)

[deploy/compose.example.yaml](deploy/compose.example.yaml) shows the two-container layout,
and [deploy/fukku.example.yaml](deploy/fukku.example.yaml) an example configuration. No image
has been published yet; pre-releases are described in [docs/prerelease.md](docs/prerelease.md).

```sh
fukku check-config --config fukku.yaml           # validate, print with secrets redacted
fukku web --config fukku.yaml                    # the web process
fukku worker --config fukku.yaml                 # the worker process
```

The web process keeps its state in one SQLite file in `server.dataDir` (local disk only);
how to back it up while Fukku runs is in [docs/backup.md](docs/backup.md).

## Metrics

Set `server.metrics: true` (`FUKKU_SERVER_METRICS=true`) and the web process serves Prometheus
metrics on `/metrics`: uploads in flight and their bytes, bytes received, jobs by state, worker
task durations by kind and outcome, imports by app and outcome (submitted, imported, failed), and readiness per check. No
label carries a user, file name or title. Like `/readyz`, it is open to a loopback address and
otherwise needs an admin through the reverse proxy, so a scraper on the same host needs no
credentials; with the setting off the path is a 404. See [docs/settings.md](docs/settings.md).

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
API for tests, is in [test/env/compose.yaml](test/env/compose.yaml).

Browser tests (Playwright, in a pinned image) run against this stack with `make e2e`; see
[test/e2e/README.md](test/e2e/README.md). They include a 5 GiB upload that survives a reload,
a restart and dropped connections; `make e2e-large` runs it at 60 GiB (by hand, it needs about
65 GiB free under `test/env/.run`). The M2 exit criteria (a film and a season imported through
the test Radarr and Sonarr, kills of `fukku-web` while importing) add titles through the apps'
metadata services, so they need the internet; `make e2e E2E_ONLINE=0` skips them.

## Development

The host needs Docker (with Compose), make and git. Every other tool is pinned in the
development image ([tools/dev/Dockerfile](tools/dev/Dockerfile)).

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
make dev-up            # run Fukku in the test environment (see "Try it locally")
make env-reset        # delete the test environment
```

The test environment uses fixed fake API keys and runs only on 127.0.0.1. The probes refuse
to talk to anything but its services. Test videos and hostile archives are generated at test
time and never committed. The indexer is a fake built from this repository
([test/env/indexer](test/env/indexer)): it offers only the generated releases a probe gives it,
and a grab ends as an NZB file in a blackhole folder under `test/env/.run`; nothing is
downloaded.

| Path | What |
| --- | --- |
| `cmd/fukku` | the binary: `web`, `worker`, `combined`, `check-config`, `healthcheck` |
| `internal/config` | the settings catalogue, YAML and `FUKKU_*` loading, validation |
| `internal/web`, `internal/worker` | the two processes |
| `internal/spool` | the task and result files between the two processes; `spooltest` has an in-memory queue |
| `internal/store` | the SQLite store: jobs and their state machine, files, uploads, audit log; `storetest` has the in-memory fake and the conformance suite |
| `internal/jobdir` | the layout of the intake root (`.incoming/`, `jobs/<id>/files`, `archives`, `work`) |
| `internal/runner` | drives jobs through their states (uploaded → probing → probed), restart-safe |
| `internal/extract`, `internal/safename` | safe zip extraction in the worker; the file-name cleaner |
| `ui/` | the React single-page app, embedded in the binary |
| `test/fixtures` | ffmpeg media and hostile-archive generators |
| `test/env`, `test/testenv`, `test/probes` | the local integration environment (including the dev stack, its test proxy and the fake indexer) and the behaviour probes |

## Licence

[AGPL-3.0](LICENSE).
