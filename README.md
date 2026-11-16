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
  media itself.
- **Built for untrusted uploads.** A separate worker process with no network and no
  credentials opens the uploaded files. Archives are extracted with limits, and nothing is
  ever overwritten.
- **Every deployment-specific behaviour is a setting**, with a default
  ([docs/settings.md](docs/settings.md)).
- **Easy to run:** one OCI image, two containers, configuration by environment variables or
  one YAML file, behind any reverse proxy.

The plan, the three flows and the milestones are in [docs/plan.md](docs/plan.md). How the
pinned versions of Radarr, Sonarr, Bazarr and Jellyfin behave is in
[docs/compatibility.md](docs/compatibility.md).

## Running it (preview)

[deploy/compose.example.yaml](deploy/compose.example.yaml) shows the two-container layout,
and [deploy/fukku.example.yaml](deploy/fukku.example.yaml) an example configuration. No image
has been published yet; pre-releases are described in [docs/prerelease.md](docs/prerelease.md).

```sh
fukku check-config --config fukku.yaml           # validate, print with secrets redacted
fukku web --config fukku.yaml                    # the web process
fukku worker --config fukku.yaml                 # the worker process
```

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

## Development

The host needs Docker (with Compose), make and git. Every other tool is pinned in the
development image ([tools/dev/Dockerfile](tools/dev/Dockerfile)).

```sh
make hooks            # install the pre-commit hook (gitleaks on staged changes)
make check            # format, lint, unit tests, UI checks, workflow lint, secret scan
make build            # bin/fukku, with the UI built in
make image            # the product image, as fukku:dev
make fixtures         # look at the generated test media and hostile archives

make env-up           # local Radarr, Sonarr, Bazarr and Jellyfin, seeded with fake keys
make probes           # behaviour probes that need no internet
make probes-online    # all probes (adding titles needs the *arr metadata services)
make compat           # write probe results into docs/compatibility.md
make dev-up            # run Fukku in the test environment (see "Try it locally")
make env-reset        # delete the test environment
```

The test environment uses fixed fake API keys and runs only on 127.0.0.1. The probes refuse
to talk to anything but its services. Test videos and hostile archives are generated at test
time and never committed.

| Path | What |
| --- | --- |
| `cmd/fukku` | the binary: `web`, `worker`, `combined`, `check-config`, `healthcheck` |
| `internal/config` | the settings catalogue, YAML and `FUKKU_*` loading, validation |
| `internal/web`, `internal/worker` | the two processes |
| `ui/` | the React single-page app, embedded in the binary |
| `test/fixtures` | ffmpeg media and hostile-archive generators |
| `test/env`, `test/testenv`, `test/probes` | the local integration environment (including the dev stack and its test proxy) and the behaviour probes |

## Licence

[AGPL-3.0](LICENSE).
