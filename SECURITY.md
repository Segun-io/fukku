# Security policy

Fukku takes uploads from people you may not fully trust and drives Radarr and Sonarr, so it
can put a library at risk. If you have found a way to make it do something it should not,
please tell us privately first.

## Reporting a vulnerability

Report it through GitHub's private vulnerability reporting: open the repository's
**Security** tab and choose **Report a vulnerability**. The report is visible only to the
maintainers until an advisory is published.

Please do not open a public issue, pull request or discussion for a vulnerability, and do
not post it anywhere else until a fix is released.

## What to include

- The Fukku version (`fukku version`, or the image digest) and the deployment mode
  (`split` or `combined`), and the versions of Radarr, Sonarr, the subtitle manager and the
  media server if they matter.
- What an attacker needs: an upload account, an admin account, a position on the network,
  the reverse proxy configured in a particular way, a compromised worker, or nothing at all.
- The steps to reproduce it, as small as you can make them: the requests, a file or archive
  that triggers it (or a script that generates one), and the settings that differ from the
  defaults. Leave out API keys, cookies and anything else secret.
- What happens and what you expected, with the relevant log lines (`fukku` logs never
  contain API keys; check before you paste).
- What you think the impact is, and a fix if you have one in mind.

[docs/threat-model.md](docs/threat-model.md) describes what Fukku defends against, what it
accepts and what is out of scope; it helps to say which of its controls the report defeats.

## What to expect

- An acknowledgement within 7 days.
- An assessment (whether we can reproduce it, how severe we think it is, and what we plan to
  do) within 14 days of the acknowledgement.
- A fix in a new release as soon as we can make one; for a high-severity issue, the aim is
  30 days. We keep you informed until then and agree the publication date with you.
- Credit in the advisory and the release notes, if you want it.

Fukku is maintained by volunteers, so these are aims rather than guarantees.

## Supported versions

Only the latest release gets security fixes. Older releases are not patched; upgrade to the
latest one. Builds from `main` that are not a release are not supported.

## Out of scope

- Vulnerabilities in Radarr, Sonarr, Bazarr, Jellyfin, FFmpeg, MKVToolNix or the reverse
  proxy themselves: report those to their projects. A Fukku image that ships a known
  vulnerable FFmpeg or MKVToolNix is in scope.
- Problems that need `auth.mode: none` (development only), a reverse proxy that passes
  client-supplied identity headers through, or `auth.trustedProxies` covering untrusted
  addresses.
- Anything [docs/threat-model.md](docs/threat-model.md) lists as accepted.
