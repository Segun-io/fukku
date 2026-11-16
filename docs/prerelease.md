# Pre-release images

`.github/workflows/prerelease.yml` builds the product image and can publish it to GHCR as a
private pre-release, so a real host can pull it by digest (plan D5). Releases proper, with
signing and public images, are M4 (#66). CI builds and smoke-tests both architectures ([image.md](image.md)); this workflow still publishes `linux/amd64` only until the release workflow (#66) pushes a multi-arch manifest.

**Every actual publish needs the maintainer's explicit OK.** The workflow starts only by hand
(`workflow_dispatch`); nothing triggers it from a push, tag, schedule or pull request.

## Running it

Actions tab, workflow `prerelease`, "Run workflow" (or `gh workflow run prerelease.yml`):

| Input | Default | Meaning |
| --- | --- | --- |
| `ref` | `main` | branch, tag or commit to build |
| `publish` | `false` | `false` is a dry run; `true` pushes to GHCR |

- **Dry run (`publish: false`)** builds the image, runs the smoke test (`tools/image-smoke.sh`: version, ffmpeg,
  ffprobe and mkvmerge with no network, read-only, no capabilities), attaches the SBOM as an artifact and
  prints the tag it would use. Nothing is pushed.
- **Publish (`publish: true`)** does the same, then the `publish` job waits for approval of the
  `prerelease` environment and pushes the very image that passed the smoke test. It signs in
  with the workflow's own token; no other secret is used.

The tag is `ghcr.io/segun-io/fukku:0.0.0-pre.<UTC date>.<short commit>`. There is never a
`latest` tag or a plain version. The job summary prints the reference to copy, pinned by digest:

```
ghcr.io/segun-io/fukku:<tag>@sha256:<digest>
```

## Pulling it

Use a token that can read packages (a personal access token with `read:packages`, or a
fine-grained token with read access to the package) while the package is private:

```sh
echo "$TOKEN" | docker login ghcr.io -u <user> --password-stdin
docker pull ghcr.io/segun-io/fukku:<tag>@sha256:<digest>
```

Use the same reference as `image:` in your copy of `deploy/compose.example.yaml`.

## Visibility

While the repository is private, the package must stay private. A new package starts private,
but its visibility is a package setting (package page, "Package settings"): check it after the
first publish. Changing it is a maintainer step.

## Deleting a pre-release

Package page, "Versions", pick the version, "Delete version". Or:

```sh
gh api -X DELETE /orgs/Segun-io/packages/container/fukku/versions/<version-id>
```

List the ids with `gh api /orgs/Segun-io/packages/container/fukku/versions` (needs the
`delete:packages` scope).

## One-time setup (maintainer)

Create the GitHub environment `prerelease` with a required reviewer, so each publish needs an
approval. It is a repository setting and is not created by the workflow.
