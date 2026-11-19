# Going-public checklist

The secrets, naming and history review of issue #64 (plan D4). The repository was already
public when it ran: it had been rebuilt from a rewritten history in which every commit uses a
GitHub noreply identity, and the private original was kept separately. The review was read-only. It
rewrote no history and changed no setting, issue or pull request. Findings that need the
maintainer are separate issues: #150 (settings), #151 (history residue), #152 (third-party
notices), #153 (community files), #154 (a pin).

Reviewed on 2026-11-17 at `main` = `6c4da54`, with 44 pull-request heads (`refs/pull/*/head`,
#70–#148). Status: **done** (checked and clean), **fixed** (fixed in the pull request for #64),
**open** (an issue tracks it), **accepted** (a known residue, accepted by the maintainer or proposed for acceptance in #151).

How to repeat it: fetch the pull-request heads with
`git fetch origin '+refs/pull/*/head:refs/remotes/origin/pr/*'`, then run the commands named in
each section. The text searches used one list of terms: the names of home-lab products and
storage, identity and tunnel vendors, common mail domains, `.home`, `.lan` and `.local` names,
home-directory paths, and the maintainer's name. The list stays out of the repository.

## 1. Secrets

| Item | Status | Evidence |
| --- | --- | --- |
| gitleaks, working tree | done | `make secrets`: no leaks |
| gitleaks, full history of `main` | done | `gitleaks git --log-opts=origin/main` (dev image, `.gitleaks.toml`): 87 commits, no leaks |
| gitleaks, every pull-request head | done | `--log-opts="--remotes=origin/pr/*"`: 169 commits, no leaks |
| Binary blobs gitleaks cannot read | accepted (#151) | Five core dumps were committed: two on `main` (added in `e499437`, #139; deleted in `a148688`, #148) and three more on the heads of #139 and #147. All are of `sh -c kill -SEGV $$` in the dev container. Their strings hold only that container's environment (container ids, `/src` and `/go` paths, the dev image's variables): no key, token, address, name or host path |
| Test API keys | done | fixed fakes only (`test/env`), documented in `docs/development.md` |
| Repository secrets and variables | done | none (`/actions/secrets`, `/actions/variables`) |
| CI logs and artifacts | done | a full CI log of `main` holds only the login and noreply addresses. The artifacts (build records, SBOMs, probe results, end-to-end traces) come from GitHub-hosted runners and the local test environment |
| Published images | done | no package on GHCR yet (`ghcr.io/segun-io/fukku`: name unknown), no tag or release |
| `make secrets` in a git worktree | fixed | the dev container saw only the worktree, not the repository's metadata, so the history scan was skipped without a word. The Makefile now mounts the git directory read-only, and the scan warns if the history is still not visible |

## 2. Identity and naming

| Item | Status | Evidence |
| --- | --- | --- |
| Author and committer of every commit on `main` and every pull-request head | done | every address is the maintainer's GitHub noreply address, `noreply@github.com` (web merges) or dependabot's noreply address |
| Trailers | accepted (#151) | Co-authored-by Claude (`noreply@anthropic.com`), dependabot. 40 commits have a malformed `Co-authored-by` trailer whose address is an API error message. It holds no personal data |
| Old commits still served by GitHub | open (#151) | three commits of the old history, unreachable from any ref, are still returned by the commits API with a non-noreply committer address. A workflow run (`43915379753`) has one of them as its head commit and links to it. Proposed: delete that run and ask GitHub Support for a purge |
| Personal names, e-mail addresses, home-lab terms, private hostnames: files on `main` | done | `git grep` with the term list: no hit. E-mail addresses are all `@example.com`, `@*.example.com` or `@evil.example` |
| The same in the full history (added lines and commit messages, `main` and pull-request heads) | done | no hit except the malformed trailer above. Every host name that was added and later removed is `*.test` |
| IP addresses | done | outside the documentation ranges, only version numbers and a netmask (`128.0.0.0`). Private ranges: `172.29.50.0/24` (deploy example), `172.30.77.0/24` (test environment), `10.0.0.0/8` and `172.16.0.0/12` (tests) |
| Issue and pull-request titles and bodies, issue comments, review comments, reviews | done | `gh api` dumps of all of them, searched with the term list: no hit. The only addresses are `user1@example.com` and noreply ones. Links to the old private repository (`Segun-io/fukku-private#<n>`) show only its name (accepted, #151) |
| Edit histories of issues, pull requests and comments | done / maintainer | GraphQL `userContentEdits` of every issue, pull request and comment (111 edited): no hit for the term list. #111 and #115 keep three earlier revisions each; the maintainer is deleting them by hand |
| Examples are generic | done | `example.com`, documentation and private address ranges, generic service names. The plan's "first adopter" stays generic (`docs/plan.md` §4) |
| Contract fixtures | done | `internal/arr/testdata/*.json` (10 KB) are hand-written API responses with open-movie titles and `/data/...` paths, no captured data |

## 3. Pins

| Item | Status | Evidence |
| --- | --- | --- |
| `FROM` in `Dockerfile` and `tools/dev/Dockerfile` | done | every base image pinned by version and digest through `ARG` |
| `image:` in `test/env/compose.yaml` | done | radarr, sonarr, bazarr, jellyfin, playwright: version and digest. The others are images built locally |
| `deploy/compose.example.yaml` | done | pinned to the released 0.1.0 image by digest (`ghcr.io/segun-io/fukku:0.1.0@sha256:1b2346a7…`) |
| `uses:` in the workflows and local actions | done | every action pinned by commit SHA with its version as a comment |
| QEMU and BuildKit images used by the image build | fixed | `setup-qemu-action` and `setup-buildx-action` used their moving default tags (`binfmt:latest`, `buildkit:buildx-stable-1`). Both are now pinned by version and digest in `.github/actions/build-image` |
| Tools in the Makefile and `tools/*.sh` | done | actionlint and Python images by version and digest. govulncheck by version (Go checksum database). syft by version through the pinned `anchore/sbom-action` |
| spdx-tools (`tools/sbom-validate.sh`) | open (#154) | pinned by version, but pip resolves its dependencies at run time without versions or hashes |
| Downloads | done | the ffmpeg source tarball is checked against its SHA-256. Debian packages come from a dated snapshot, by version (in the dev image, `sqlite3` and `zip` take the snapshot's version). npm and Go come from lockfiles |
| Runner image | accepted | `ubuntu-24.04` is a version label. GitHub offers no digest pin for hosted runners |

## 4. Repository settings (read only; changes in #150)

| Item | Status | Evidence |
| --- | --- | --- |
| Branch protection on `main` | open (#150) | none, and no ruleset |
| Secret scanning and push protection | open (#150) | both disabled |
| Dependabot alerts | done | enabled, 0 open. Automatic security updates are off (#150) |
| Private vulnerability reporting | done | enabled |
| Default `GITHUB_TOKEN` | done | read-only, cannot approve pull requests |
| Actions allowed, SHA pinning required | open (#150) | all actions allowed, SHA pinning not required (every action is pinned anyway) |
| Workflows from forks | open (#150) | approval required for first-time contributors |
| `prerelease` environment | open (#150) | `prerelease.yml` expects it, with the maintainer as required reviewer, but it does not exist |
| Description and topics | open (#150) | the description is fine. There are no topics |
| Organisation two-factor requirement | open (#150) | not required |
| LICENSE | done | AGPL-3.0 |
| SECURITY.md | done | from #63, private vulnerability reporting, no personal address |
| CONTRIBUTING.md | fixed | added |
| Code of conduct, issue and pull-request templates | open (#153) | none. The maintainer decides |

## 5. The old private repository

| Item | Status | Evidence |
| --- | --- | --- |
| Private | done | `visibility: private` |
| Archived | open (#151) | `archived: false`. It was expected to be archived |

## 6. Files

| Item | Status | Evidence |
| --- | --- | --- |
| `.gitignore` | fixed | it already covered caches, `node_modules`, `ui/dist`, `bin/`, `test/env/.run/`, Playwright output and core dumps. Added: `deploy/secrets/`, `deploy/fukku.yaml` and `.env` files, for a deployment tried from inside the checkout |
| Large or generated files | done | 841 tracked files, the largest 257 KB (design screenshots in `docs/design`). No core dump or ELF binary (`tools/check-tracked.sh` in `make check`) |
| Committed test media or archives | done | none. `test/fixtures` generates them at test time. The only binaries are the design screenshots and the UI fonts |
| Third-party licences | open (#152) | the fonts' OFL texts sit next to them, and `docs/image.md` has the GPL source offer for ffmpeg. Nothing collects the notices of the bundled npm packages, and nothing ships them in the image |
