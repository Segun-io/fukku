# Releasing Fukku

The checklist for a release, v0.1.0 first (issue #66, plan §15 M4, D4 and D5). The release
workflow is `.github/workflows/release.yml`; the notes come from [CHANGELOG.md](../CHANGELOG.md).

**Nothing here happens without the maintainer's explicit OK, each time:** pushing the tag,
approving the `release` environment (which pushes the image to GHCR, signs it and attaches
provenance), making the package public and publishing the draft release. The workflow never
creates a tag and only ever creates a **draft** release.

## One-time setup (maintainer)

1. **The `release` environment.** Settings, Environments, New environment, named exactly
   `release`. Add the maintainer as a required reviewer. Leave "Prevent self-review" off when the
   maintainer is the only reviewer (the maintainer who pushes the tag is the one who approves).
   Under "Deployment branches and tags" choose selected tags and add `v*`. It needs no secrets.
   The workflow refuses to publish when the environment is missing, has no required reviewer or
   is not limited to `v*` tags: GitHub creates a missing environment on first use *without* a
   reviewer, so the check runs in a job before the publish job (`environment-check`).
2. **Tags.** Recommended (the maintainer decides; this work creates nothing): a repository ruleset
   for the tag pattern `v*` that restricts who can create, update and delete such tags to the
   maintainer. The workflow's own checks (the tag is `vX.Y.Z`, matches the compose version, has a
   dated changelog section, and its commit is on `main`) run from the workflow file *at the tag*, so
   whoever can push a tag controls them: they are advisory. The real controls are the tag
   ruleset and the required reviewer of the `release` environment, who sees the tag and commit
   before approving.
3. **Package visibility.** The first push creates `ghcr.io/segun-io/fukku` as a private package.
   After the first publish, open the package's settings and make it public (and check that it is
   linked to the repository), or `docker pull` needs a token. Changing visibility is a
   maintainer step and is not undone lightly.
4. **Attestations and signing** need nothing stored: keyless signing uses the run's OIDC token
   (the repository is public), and the attestations go to GitHub and the registry.

## The checklist

Do these in order. Tick each in the release pull request or the release issue.

### 1. Before the version bump

- [ ] **All milestone exit tests are green on `main`.** The `ci` workflow's last run on `main`
  passes every job, including `e2e` with the online tests (M1, M2 and M3 exit specs:
  `test/e2e/exit-m1-large-upload.spec.ts`, `exit-stack.spec.ts`, `exit-m2.spec.ts`, `exit-m3.spec.ts`):

  ```sh
  gh run list -R Segun-io/fukku --workflow ci --branch main --limit 1
  ```

  A push to `main` runs the e2e tests with `E2E_ONLINE=1`. To run them by hand: `make e2e`.
- [ ] **The probes pass on the pinned versions, and the table is current:**

  ```sh
  make env-up
  make probes-online
  make compat          # writes docs/compatibility.md
  ```

  Commit `docs/compatibility.md` if it changed (the release attaches it as of the tag). Any new
  `differs` or `error` verdict is either fixed or recorded in `test/probes/accepted.json` with
  its issue. The weekly run opens (or updates) one issue, "Online probes: a verdict changed or
  the gate failed", when a verdict differs from the committed table or the gate fails: it must
  be closed or explained.
- [ ] **No open high-severity security finding:**

  ```sh
  gh issue list -R Segun-io/fukku --label severity:high --state open
  ```

  The output must be empty. Read the open `severity:medium` list too and decide, in writing,
  which ones may ship in 0.1.0 (the threat model, docs/threat-model.md, says what each leaves).
- [ ] **The going-public checklist** ([going-public.md](going-public.md), #64) has no open item
  that the maintainer has not accepted.
- [ ] **The clean-room install passes with a locally built image** (first of two runs: this one
  before publishing, the second with the released digest in step 4) (#67, README, "The clean-room
  install test"). It needs Docker-in-Docker and the internet (Radarr and Sonarr metadata):

  ```sh
  make image                              # builds fukku:dev
  make install-test FUKKU_IMAGE=fukku:dev
  ```

### 2. The version bump (one pull request)

A **release candidate** (`vX.Y.Z-rc.N`) goes through the same steps, with `X.Y.Z-rc.N` as the
version in both places: the `x-image` line and the CHANGELOG heading, which takes the version's
notes. The workflow marks its draft as a pre-release. Skip step 5's digest pin and the closing of
issues for a candidate; the final version's bump renames the heading to `## [X.Y.Z] - <date>`
and leaves a one-line `## [X.Y.Z-rc.N]` section that points to it.

The version is written in **one place**: the `x-image` line of `deploy/compose.example.yaml`
(`ghcr.io/segun-io/fukku:0.1.0`). The binary's version (the `-X ...internal/version.Version`
linker flag in the `Dockerfile` and the `Makefile`) comes from the `VERSION` build argument, which
the workflow sets from the tag; there is nothing to edit for it. The workflow refuses a tag whose
version differs from that line.

- [ ] Set the `x-image` tag to the new version (without the `v`), and **drop the `@sha256:...`
  digest of the previous release** from that line: the new digest does not exist yet (it is added
  in step 5). The line reads `ghcr.io/segun-io/fukku:0.1.0` until then.
- [ ] In `CHANGELOG.md`, turn `## [0.1.0] - Unreleased` into `## [0.1.0] - <date>`, keep an empty
  `## [Unreleased]` above it, and keep the link lines at the end right.
  The workflow's notes are this version's section: `tools/release-notes.sh 0.1.0`.
- [ ] Merge the pull request (`make check` and CI green).
- [ ] **Dry run of the workflow on `main`**, which builds both architectures, smoke-tests them,
  writes the SBOMs, generates the notes and checks cosign, and publishes nothing:

  ```sh
  gh workflow run release.yml -R Segun-io/fukku --ref main -f publish=false
  gh run list -R Segun-io/fukku --workflow release.yml --limit 1
  ```

  Every job must pass. The job summary of `release environment check` says whether the `release`
  environment would be accepted (a warning if not). The job summary of `signing-dry-check` shows
  the draft release's body. The arm64 build runs under emulation and takes about an hour.

### 3. The tag (maintainer's explicit OK)

- [ ] Tag the merged commit and push **only that tag**:

  ```sh
  git fetch origin
  git tag -s v0.1.0 origin/main -m "Fukku 0.1.0"    # or -a when you do not sign tags
  git push origin v0.1.0
  ```

  The tag must be on `main`, match the `x-image` version and have a dated CHANGELOG section (not
  "Unreleased"); the workflow refuses it otherwise (advisory: see the tag ruleset in the setup).
  Pushing the tag starts the `release` workflow: it builds, tests and waits.
- [ ] **Approve the environment** (maintainer's explicit OK): Actions, the run, "Review
  deployments", `release`. Only now does the `publish` job push
  the two images the build jobs tested (it does not build again) by digest under a neutral
  `staging-<run id>` tag, assemble the multi-arch index, smoke-test each pushed digest, sign the
  index and each image with cosign, attach build provenance and the two SBOMs as attestations,
  verify all of them, and **only then create the tag `ghcr.io/segun-io/fukku:0.1.0`**. The
  `staging-*` tags are permanent, harmless aliases of the released digests: **never delete one
  after a successful run** (on GHCR, deleting a tag deletes the package version, that is the
  digest, and `staging-<run id>` is the very version `:0.1.0` points to). The
  `release` job then creates a **draft** GitHub release (notes, the digest-pinned reference, the
  SBOMs and `compatibility.md`).

  To stop, reject the deployment before approving: nothing was pushed. After approving, a pushed
  image is not taken back: do not reuse the version; fix forward with the next patch version.

### 4. Verify what was published

Take the digest from the `publish` job's summary (`ghcr.io/segun-io/fukku:0.1.0@sha256:...`).
With `cosign` and `gh` installed:

```sh
IMAGE=ghcr.io/segun-io/fukku
DIGEST=sha256:<digest from the summary>

# The signature: made by this repository's release workflow, run for exactly this version's
# tag (replace X.Y.Z). Do not loosen the identity to a pattern.
cosign verify \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate-identity 'https://github.com/Segun-io/fukku/.github/workflows/release.yml@refs/tags/vX.Y.Z' \
  "$IMAGE@$DIGEST"

# The build provenance (SLSA), checked against this repository.
gh attestation verify "oci://$IMAGE@$DIGEST" --repo Segun-io/fukku

# The SBOM attestation of one architecture's image: take its digest from the manifest list.
docker buildx imagetools inspect "$IMAGE@$DIGEST" --raw | jq '.manifests[] | {platform, digest}'
gh attestation verify "oci://$IMAGE@<amd64 digest>" --repo Segun-io/fukku \
  --predicate-type https://spdx.dev/Document/v2.3
```

(The last command's predicate type is the SPDX document version of the SBOM; if it says no
attestation matches, list them with `gh attestation download` or try `https://spdx.dev/Document`.)

- [ ] Signature verified, provenance verified, SBOM attestation verified.
- [ ] **The clean-room install with the released image**, now by digest:

  ```sh
  make install-test FUKKU_IMAGE="ghcr.io/segun-io/fukku:0.1.0@$DIGEST"
  ```

  While the package is private the clean host cannot use your login, so give the test a token
  that can read the package (a personal access token with `read:packages`) in a file, mode 0600;
  the host logs in with it over stdin, the token is never printed, and the host logs out after
  the pull (a host kept with `FUKKU_INSTALL_KEEP=1` holds no token):

  ```sh
  make install-test FUKKU_IMAGE="ghcr.io/segun-io/fukku:0.1.0@$DIGEST" \
    GHCR_TOKEN_FILE=$HOME/.config/fukku-release/ghcr-token GHCR_USER=<your GitHub login>
  ```

  (Once the package is public no token is needed, and the test then also proves an anonymous pull.)
- [ ] Make the package public (see the setup) and pull it without logging in:
  `docker pull "$IMAGE@$DIGEST"`.

### 5. After publishing

- [ ] **Update `deploy/compose.example.yaml` to the released digest** (pull request): the
  `x-image` line becomes `ghcr.io/segun-io/fukku:0.1.0@sha256:<digest>`. The version stays the
  one place to edit; the workflow's check reads the part before the `@`.
- [ ] **Publish the draft release** (maintainer's explicit OK): Releases, the draft for `v0.1.0`,
  read the notes, the reference and the attached files, then Publish. Or
  `gh release edit v0.1.0 -R Segun-io/fukku --draft=false`.
- [ ] Close #66 and #28 (the milestone's tracking issue) and the M4 milestone.

## What the workflow does

| Job | Runs | Needs | Permissions |
| --- | --- | --- | --- |
| `prepare` | always | | read |
| `build` (amd64, arm64) | always | `prepare` | read. The shared `build-image` action: build, smoke test, merged and validated SBOM |
| `environment-check` | always | `prepare` | read, `actions: read`. Refuses to publish unless the `release` environment exists, has a required reviewer and deploys only from `v*` tags; a dry run warns |
| `handoff-dryrun` (x2) | dry run | `build` | read. The saved images reload with the id they were tested with |
| `signing-dry-check` | dry run | `build` | read. Pinned cosign signs and verifies the SBOMs with a throwaway key (no log, no registry); the draft release body and its exact signer identity |
| `publish` | tag | `build`, `environment-check` | **environment `release`**; `packages: write`, `id-token: write`, `attestations: write`. Push the tested images by digest, index, smoke-test by digest, cosign sign (recursive), provenance, SBOM attestations, verification, then the version tag |
| `release` | tag | `publish` | `contents: write`. The draft release only |

A run is a dry run when the ref is a branch, or when it is started by hand on a tag with
`publish` left false. A tag push publishes (after approval); a manual run with `publish=true`
works only on a version tag. `publish` does not build: it pushes the images the build jobs saved
after testing them, so the SBOMs describe the bytes that are published. Dry runs and tag runs use
separate concurrency groups, so a dry run never cancels a waiting release.

Pinned: every action by commit SHA; BuildKit and the QEMU emulator image by version and digest;
cosign by version and the SHA-256 of its binary (`.github/actions/install-cosign`: bump the
version and the digest from the release's `cosign_checksums.txt` together).

## When something fails

- **`prepare` refuses the tag:** the tag is not `v<major>.<minor>.<patch>[-pre]`, differs from the
  `x-image` version, is not on `main`, or CHANGELOG.md has no dated section for it. Delete the tag
  (`git push origin :refs/tags/v0.1.0`, a maintainer step), fix, and tag again. Nothing was
  pushed.
- **`release environment check` refuses:** create or fix the environment as in the setup, then
  re-run the failed jobs.
- **`publish` fails after the push:** the images exist on GHCR under their digests and
  `staging-*` tags, but the version tag was not created and nothing is released. Do not publish a
  release for them. Fix forward with the next patch version. Leave the images alone unless you
  are sure no version tag points to a digest: before deleting a package version, run
  `docker buildx imagetools inspect ghcr.io/segun-io/fukku:<version>` (and for any other tag
  that might alias it) and compare its digest with the version you are about to delete; delete
  only a digest that no version tag points to. A re-run of a successful run is refused by the
  `publish` job when `:<version>` already exists.
