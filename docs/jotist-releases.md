# Jotist releases and deployment images

Jotist publishes server archives and Linux amd64 containers from [jaysqvl/Jotist](https://github.com/jaysqvl/Jotist). **v1.7.0** is the first stable Jotist release, continuing the inherited 1.6.1 version line.

The standard stable publication selects **CPU and CUDA 12.6** using `variants=cpu-cuda`. It publishes `latest` and `latest-cuda` only after the existing validation and artifact jobs succeed. Blackwell/CUDA 13 is excluded from that default: dependency-resolution and native-library checks exist, but actual Blackwell inference remains unqualified.

## Image names

CPU and CUDA share one container repository:

| Variant | Versioned image | Stable alias |
| --- | --- | --- |
| CPU | `ghcr.io/jaysqvl/jotist:1.7.0` | `ghcr.io/jaysqvl/jotist:latest` |
| CUDA 12.6 | `ghcr.io/jaysqvl/jotist:1.7.0-cuda` | `ghcr.io/jaysqvl/jotist:latest-cuda` |

Container tags omit the Git tag's leading `v`. Builds also publish `<version><variant-suffix>-<12-character-commit>` and report a digest in the Actions summary. Use `ghcr.io/jaysqvl/jotist@sha256:...` for reproducible deployment. Confirm every selected image job finished before pulling the release.

For Blackwell evaluation, build `Dockerfile.cuda.13.0` locally using `docker-compose.build.blackwell.yml`. The `docker-compose.blackwell.yml` entrypoint also builds from source while retaining its existing named-volume mappings. Both are explicitly unqualified for actual Blackwell model inference; neither recommends the old RC1 image. Automatic stable publication does not update `latest-blackwell`.

## Preview history and qualification

The final preview was [v1.7.0-rc.3](https://github.com/jaysqvl/Jotist/releases/tag/v1.7.0-rc.3). Its CUDA 12.6 image is `ghcr.io/jaysqvl/jotist:1.7.0-rc.3-cuda`, from commit `71d31c7c3becc91f217b7d5bca37f3a2db86bdd4`, with digest `sha256:a552e0d01345581aab17c9d31e1dc11303a79bc1964099c6956b106eac8b195f`. It was published with `variants=cuda` and `publish_latest=false`; those immutable references remain historical records.

RC2 failed staging against copied existing environments: retained `uv.lock` selections kept older dependencies, and inexact readiness checks left packages outside the resolved graph installed. RC3 corrected that upgrade path. Do not retag RC1, RC2, or RC3 with new bytes.

Selected CPU and CUDA 12.6 model pipelines passed the runtime qualification recorded with RC3, including checks against retained environments, installed versions, and native decoding. Those results are prior evidence; they do not replace validation of the final stable source or qualification against each deployment's retained data and runtime state. Blackwell inference remains unqualified.

## Rolling development image

Maintainer installations can track `ghcr.io/jaysqvl/jotist:dev-cuda`. **Publish
development CUDA image** runs on pushes to `main`, validates the exact pushed
commit through the existing release checks, and builds the CUDA 12.6 image. It
publishes `dev-cuda-<12-character-commit>` first, then copies that exact digest to
`dev-cuda` only if the built commit is still the head of `main`. Superseded runs
are canceled. Failed validation or builds leave the previous alias available.
Stable `latest` aliases and version tags are unaffected.

This channel deliberately includes unreleased development changes. The checks
include frontend/backend validation and Python runtime checks; they do not
replace model inference or an upgrade check against the NAS's existing runtime
directories. A floating tag enables the deployment manager to discover newer
images. Pulling and recreating the container is still a separate update step.

Use **Publish development CUDA image** with no inputs on `main` to request a
fresh development build. Set repository variable `JOTIST_DEV_AUTOBUILD_ENABLED`
to `false` to pause builds triggered by pushes during a coordinated migration;
set it to `true` (or remove it) to resume. Manual dispatch remains available.

To adopt the channel without rebuilding or changing the currently qualified
application, dispatch the same workflow on `main` with `qualified_cuda_digest`
set to the full `sha256:...` digest of the already published Jotist CUDA image.
This mode copies the existing manifest in the same registry and verifies that
the digest is unchanged. The maintainer must first verify its CUDA variant and
complete the required NAS qualification; promotion itself performs neither.

For a coordinated first cutover, pause automatic builds before merging this
workflow, qualify the selected versioned image, promote its digest, and verify
that `dev-cuda` resolves to it. Then update the existing NAS container and its
Unraid template together while retaining their data/configuration. Resume
automatic builds after cutover acceptance. Keep the deployed digest and backup
with the cutover record so rollback does not depend on the moving alias.

## Publishing a versioned preview

RC3 is already published; never replace its existing tag or image. For a future preview, create a new annotated version tag after the reviewed source is on `main`. Pushing a tag does not itself publish anything. In GitHub Actions, run **Release** with:

- `tag`: the new version tag
- `variants`: `cpu-cuda` (or one explicitly selected variant)
- `publish_latest`: `false`

The workflow resolves the tag to an immutable commit and requires that commit to belong to `main`. Frontend checks, backend checks, dependency scans, and Python adapter resolver checks run against that commit. GoReleaser then publishes archives with the Jotist server executable, checksums, license, and attribution. Container publication follows successful artifact publication and builds the same validated commit.

A GitHub release appearing is not proof that every image has finished publishing. Verify all selected jobs and each requested variant before deploying.

## Independently dispatch an image build

Use **Publish container images** when rebuilding a particular variant or publishing a preview channel. Inputs are `source_repository=jaysqvl/Jotist`, `upstream_ref` (branch, tag, or commit), `image_channel` (without `v`), `variants`, and `publish_latest`.

The workflow resolves the source once and validates it before building. It rejects other source repositories. Release workflow calls can reuse validation only when their supplied immutable commit exactly matches the image source; this option is not exposed in manual dispatch.

The `variants` choices are `cpu-cuda` (the default), `cpu`, `cuda`, `blackwell`, and `all`. `cpu-cuda` selects exactly the CPU and CUDA 12.6 jobs. `all` retains the explicit option to publish all three variants, including unqualified Blackwell; it is not used by the normal stable release call. If stable aliases are requested for an explicit `all` or `blackwell` build, the existing alias mechanism also publishes `latest-blackwell`. That is a publication choice, not evidence of hardware qualification.

Each matrix job builds its own Dockerfile and prints the resulting digest and source commit. CPU uses `Dockerfile`, CUDA uses `Dockerfile.cuda`, and Blackwell uses `Dockerfile.cuda.13.0`.

## Stable releases

Release Please opens a version/changelog PR from conventional commits on `main`. Its manifest records the stable release version. Bootstrap history begins at `812adc5`, before the imported feature snapshot, so the first Jotist release includes the new features without replaying the entire upstream history.

After the audit and preview review, merge the reviewed stable release PR for `v1.7.0`. Release Please calls the same validation/artifact/image pipeline with `variants=cpu-cuda` and stable aliases enabled. The normal call publishes CPU and CUDA 12.6; Blackwell is not selected. The reusable workflow call is intentional: tags/releases created using `GITHUB_TOKEN` do not need to trigger another tag-push workflow.

Repository Actions must be allowed to create pull requests. Jobs request only the contents, pull-request, issue, or package permissions they need. No custom PAT or Docker Hub credentials are required for this publication flow.

## Verify a published release

Confirm that validation, artifact publication, and all selected image jobs completed successfully for the intended commit. Check the image's `org.opencontainers.image.revision` and `org.opencontainers.image.version`, then record the digest. Ensure the GHCR package is linked to Jotist, public if advertised publicly, and can be pulled without authentication.

Archives and images retain the MIT license and upstream attribution. Containers include `/app/LICENSE`, `/app/ATTRIBUTION.md`, and `/app/THIRD_PARTY_NOTICES.md`.

Deployment remains a separate step. Qualify the released image against a private
copy of the existing runtime volumes, including their lockfiles and installed
packages. Compare the complete installed package/version graph with the reviewed
qualification graph, run native imports and the installed dependency audit, and
exercise the selected model pipeline. A fresh-environment check alone does not
validate migration.

After cutover, require the complete live installed graph to equal the qualified
staged graph and repeat the installed audit against the actual materialized
callers. An unexpected package, version, or caller difference blocks acceptance.
See [runtime maintenance](python-runtime-maintenance.md) for lock migration and
exact synchronization. Follow the [migration guide](jotist-migration.md), preserve
the existing stack's data/configuration, and verify the running image and
application behavior. Keep the old fork and pre-upgrade backup until the preview
and cutover are accepted.
