# Jotist releases and deployment images

Jotist publishes server archives and Linux amd64 containers from [jaysqvl/Jotist](https://github.com/jaysqvl/Jotist). The first preview is `v1.7.0-rc.1`; the first stable Jotist version will be `v1.7.0`, continuing the inherited 1.6.1 version line.

## Image names

All variants share **one** container repository:

| Variant | Preview tag | Stable tag |
| --- | --- | --- |
| CPU | `ghcr.io/jaysqvl/jotist:1.7.0-rc.1` | `ghcr.io/jaysqvl/jotist:1.7.0` |
| CUDA | `ghcr.io/jaysqvl/jotist:1.7.0-rc.1-cuda` | `ghcr.io/jaysqvl/jotist:1.7.0-cuda` |
| Blackwell | `ghcr.io/jaysqvl/jotist:1.7.0-rc.1-blackwell` | `ghcr.io/jaysqvl/jotist:1.7.0-blackwell` |

Container tags omit the Git tag's leading `v`. Builds also publish `<version><variant-suffix>-<12-character-commit>` and report a digest in the Actions summary. Use `ghcr.io/jaysqvl/jotist@sha256:...` for reproducible deployment.

Stable aliases are `latest`, `latest-cuda`, and `latest-blackwell`. They update only when explicitly requested for a stable version tag pointing at the built source. Prereleases and preview channels never move them.

## First preview publication

After the reviewed source is on `main`, create and push the annotated `v1.7.0-rc.1` tag. Pushing a tag does not itself publish anything. In GitHub Actions, run **Release** with:

- `tag`: `v1.7.0-rc.1`
- `variants`: `all`, or one of `cpu`, `cuda`, `blackwell`
- `publish_latest`: `false`

The workflow resolves the tag to an immutable commit and requires that commit to belong to `main`. Frontend checks, backend checks, dependency scans, and Python adapter resolver checks run against that commit. GoReleaser then publishes archives with the Jotist server executable, checksums, license, and attribution. Container publication follows successful artifact publication and builds the same validated commit.

A GitHub release appearing is not proof that every image has finished publishing. Verify all selected jobs and each requested variant before deploying.

## Independently dispatch an image build

Use **Publish container images** when rebuilding a particular variant or publishing a preview channel. Inputs are `source_repository=jaysqvl/Jotist`, `upstream_ref` (branch, tag, or commit), `image_channel` (without `v`), `variants`, and `publish_latest`.

The workflow resolves the source once and validates it before building. It rejects other source repositories. Release workflow calls can reuse validation only when their supplied immutable commit exactly matches the image source; this option is not exposed in manual dispatch.

Each matrix job builds its own Dockerfile and prints the resulting digest and source commit. CPU uses `Dockerfile`, CUDA uses `Dockerfile.cuda`, and Blackwell uses `Dockerfile.cuda.13.0`.

## Stable releases

Release Please opens a version/changelog PR from conventional commits on `main`. Its manifest remains at `1.6.1` until a stable release advances it. Bootstrap history begins at `812adc5`, before the imported feature snapshot, so the first Jotist release includes the new features without replaying the entire upstream history.

After preview review, merge the reviewed stable release PR. Release Please calls the same validation/artifact/image pipeline with stable aliases enabled. The reusable workflow call is intentional: tags/releases created using `GITHUB_TOKEN` do not need to trigger another tag-push workflow.

Repository Actions must be allowed to create pull requests. Jobs request only the contents, pull-request, issue, or package permissions they need. No custom PAT or Docker Hub credentials are required for this publication flow.

## Verify a published release

Confirm that validation, artifact publication, and all selected image jobs completed successfully for the intended commit. Check the image's `org.opencontainers.image.revision` and `org.opencontainers.image.version`, then record the digest. Ensure the GHCR package is linked to Jotist, public if advertised publicly, and can be pulled without authentication.

Archives and images retain the MIT license and upstream attribution. Containers include `/app/LICENSE`, `/app/ATTRIBUTION.md`, and `/app/THIRD_PARTY_NOTICES.md`.

Deployment remains a separate step. Follow the [migration guide](jotist-migration.md), preserve the existing stack's data/configuration, and verify the actual running image and application behavior. Keep the old fork and pre-upgrade backup until the preview and cutover are accepted.
