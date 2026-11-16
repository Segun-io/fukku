# End-to-end tests (Playwright)

Browser tests of the UI against the dev stack (`make dev-up`: Fukku web and worker behind the
test auth proxy, next to the test Radarr, Sonarr, Bazarr and Jellyfin). Plan §14.6.

## How it runs

`make e2e` runs `make image env-up dev-up`, installs this project's dependencies in the dev
image (`npm ci`, no install scripts), and runs the tests in the `e2e` service of
`test/env/compose.yaml`. That service uses the pinned Playwright image
(`mcr.microsoft.com/playwright`, tag and digest), so no browser is downloaded at test time. It
is on the stack's network only: it reaches the proxy and nothing else.

- The image tag and `@playwright/test` in `package.json` must be the same version;
  `run.sh` checks it. Bump the tag, the digest and the package together.
- The proxy's addresses are the stack contract (`http://127.0.0.1:18080` and `:18081`).
  `global-setup.ts` forwards those two ports inside the runner to `fukku-proxy`, so the
  public base URL and the sign-in redirect work as in a person's browser.
- **The runner gets the container runtime's socket** (and no docker client: `helpers/stack.ts`
  speaks the Engine API) so `stack.restart` can restart `fukku-web` and `fukku-worker`. It is
  in this test profile only.

## Run one test

```sh
make e2e ARGS='smoke.spec.ts'
make e2e ARGS='helpers.spec.ts -g session'
```

`ARGS` goes to `playwright test` (split on spaces). With the stack already up and
dependencies installed, `test/e2e/run.sh smoke.spec.ts` skips the setup.

## See a trace

A failed test leaves `test/e2e/test-results/<test>/trace.zip` and a screenshot (CI uploads
them, with the containers' logs, as the artifact `e2e-traces-and-logs`). Open a trace with
`npx playwright show-trace test-results/<test>/trace.zip` (in this directory after
`make e2e-deps`, or drag the file into <https://trace.playwright.dev>, which runs in your
browser). `playwright-report/index.html` is the HTML report.

## Helpers (`helpers/`)

| Helper | What it does |
| --- | --- |
| `loginAs(page, "user1" \| "user2" \| "admin1")` | sets the proxy's user cookie in the page's context |
| `session.expire("401" \| "302")`, `session.restore()` | the proxy answers every request 401 or redirects to sign-in; back to normal |
| `network.cutUploads({afterBytes, times})`, `network.reset()` | the next upload PATCH requests are cut after that many body bytes; clear all |
| `stack.restart("fukku-web" \| "fukku-worker")`, `stack.waitReady()` | restart a container; wait until both are healthy and `/healthz` answers |
| `files.sparse(name, bytes)` | a file that is one big hole, in the runner's `/files`; returns its path |

`helpers.spec.ts` tests each helper. Tests share one proxy, so they run one at a time, and
each one that changes proxy state should call `network.reset()` and `session.restore()` in
`afterEach`.

## Add a scenario

1. Create `<name>.spec.ts` here. Import from `./helpers`.
2. Start with `await loginAs(page, "user1")`; use paths relative to the base URL.
3. Wait on observable state (a response, an element, an offset), never on a fixed sleep.
4. `make e2e ARGS='<name>.spec.ts'` until green, then `make check` (lints and type-checks this
   project with Biome and `tsc`).
