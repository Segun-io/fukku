# Contributing to Fukku

Thank you for helping. Fukku is pre-alpha: open an issue before a large change, so we can
agree on the approach first.

**Security problems are not issues.** Report them privately, as [SECURITY.md](SECURITY.md)
describes.

## Setting up

The host needs Docker (with Compose), make and git. Every other tool is pinned in the
development image ([tools/dev/Dockerfile](tools/dev/Dockerfile)).
[docs/development.md](docs/development.md) lists the make targets and describes the local test
environment, the end-to-end tests, the README's screenshots, the source layout and the
clean-room install test. The plan, the flows and the milestones are in
[docs/plan.md](docs/plan.md).

```sh
make hooks    # the pre-commit hook: gitleaks on staged changes
make check    # what CI runs on each commit; it must pass before you open a pull request
```

## Rules for every change

- **Independent of any deployment.** No real hostnames, IP addresses, network, identity
  provider, storage vendor or person's name in code, tests, documentation, commit messages,
  issues or pull requests. Use `example.com`, the documentation address ranges
  (192.0.2.0/24, 198.51.100.0/24, 203.0.113.0/24) or 127.0.0.1, and the generic service names
  radarr, sonarr, jellyfin and bazarr. Behaviour that depends on a deployment is a setting with a
  default, in `internal/config` and [docs/settings.md](docs/settings.md).
- **No secrets.** Test API keys are fixed fakes. Never paste a real key, cookie or log line that
  holds one.
- **Test files are generated, never committed.** Media and archives come from `test/fixtures`
  at test time. Core dumps and compiled binaries fail `make check` (`tools/check-tracked.sh`);
  stage files by name, not with `git add -A`.
- **Only the local test environment.** Tests and probes talk to the Radarr, Sonarr, Bazarr and
  Jellyfin of `make env-up`, never to a real installation.
- **Everything pinned.** Images by version and digest, actions by commit SHA, tools by version
  (and digest or checksum where there is one).
- **Commit with your GitHub noreply address** (GitHub settings, Emails). A personal address in a
  public history cannot be taken back.

## Pull requests

Name the issue a pull request resolves with `Fixes #<n>` in its description, and keep the change
to what the issue asks. CI runs `make check`, the image build and smoke test, the behaviour probes and the
end-to-end tests; a first-time contributor's workflows run after a maintainer approves them.

Contributions are made under the project's licence, [AGPL-3.0](LICENSE).
