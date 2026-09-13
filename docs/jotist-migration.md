# Migrating from Scriberr to Jotist

Jotist is an independent repository at [jaysqvl/Jotist](https://github.com/jaysqvl/Jotist). Its first preview release is `v1.7.0-rc.1`; stable `v1.7.0` follows review. The preview changes the product identity, indigo/cyan styling, release ownership, and includes the preserved local-model and recoverable-execution work. It does not intentionally relocate existing data or replace API contracts.

## Repository transplant

The transplant retains Scriberr's commit ancestry and authorship. Commit `9262941` captures the completed local speech/model and recovery work on top of `812adc5`, before the Jotist rebrand. Keep the previous fork available while reviewing the preview.

Create an empty independent repository and push the reviewed branch with its history. Do not initialize a fresh unrelated history, use GitHub's fork action, or force-push over an existing project. Keep the new checkout's `origin` on Jotist and the old fork/upstream as separate fetch remotes if wanted.

**Historical tag import:** disable GitHub Actions before pushing the old Scriberr tags. Those tags contain historical workflow files that can publish old images even though Jotist's current workflows no longer publish on tag pushes. Reenable Actions after the import. New Jotist versions are published explicitly through the Release workflow or Release Please.

Git commits and tags do not transfer GitHub issues, pull requests, release attachments, stars, repository settings, or package settings. Retain the old fork until anything worth preserving there has been reviewed. Deleting the old fork is a separate later decision.

## Preserve these identities

| Existing interface or storage | Migration behavior |
| --- | --- |
| Database `scriberr.db` and `DATABASE_PATH` | Keep the same file and configured path |
| `/app/data` | Keep database, uploads, transcripts, and `jwt_secret` together |
| `/app/whisperx-env` | Retain the configured model/runtime mount |
| Compose project/service, Unraid template, and physical volumes | Retain their mapping; update the image in the existing deployment |
| `JWT_SECRET` / `JWT_SECRET_FILE` | Keep the same secret to preserve sessions |
| `scriberr_access_token`, `scriberr_refresh_token` cookies | Retained |
| Existing browser upload/session storage and PWA id | Retained on the same application origin |
| CLI `scriberr`, `~/.scriberr.yaml`, `SCRIBERR_*`, watcher service | Retained |
| API routes, webhook identity and idempotency keys | Retained |
| Container executable `/app/scriberr` | Compatibility symlink to `/app/jotist` |

Keeping an existing URL preserves browser-origin storage. Moving to a different hostname or port may require signing in again and does not transfer in-progress browser upload state. A later domain rename can be handled separately.

## Preview before replacing the live stack

1. Inspect the actual deployment manager: Unraid DockerMan template, Compose/Portainer stack, or another runtime. Record image and digest, ports, environment names, bind mounts or named volumes, UID/GID, GPU access, proxy routing, health checks, restart behavior, and autostart settings without publishing secrets. For Unraid, preserve the user template, WebUI/icon settings, extra parameters, and autostart ordering so updates and host reboots retain the configuration.
2. Back up the database, uploads, transcripts, and signing secret using a consistent database backup or a stopped application. Keep the old image digest and deployment definition/template with the backup.
3. Create a preview with a **separate writable copy** of application data and a separate port. Never run two server versions against the same SQLite file or writable model environment. Large model caches may be copied/reflinked as supported by the host, while writable runtime state remains isolated.
4. Pull the selected `ghcr.io/jaysqvl/jotist:1.7.0-rc.1` variant, record its digest and OCI revision, and configure the preview with that digest.
5. Verify startup and health, sign-in, recording list, existing transcript playback, profile settings, uploads, and the workflow you actually use. Inspect desktop/mobile and light/dark appearance. Model inference must be checked separately on the target hardware.

A healthy container alone does not verify audio playback, migration data, or transcription execution. The preview is also not the production cutover.

## Cutover after preview review

Update the existing deployment's image to the reviewed Jotist digest while retaining its data, environment, network, proxy mappings, restart policy, and autostart behavior. On Unraid, update the DockerMan user template as well as the running container; a runtime-only image change can otherwise be lost on the next template update. If the container is renamed, migrate its template name and autostart entry together and verify any proxy/container-name references. Do not run `docker compose down -v`, rename physical volumes, or reset runtime directories as part of the rebrand.

Verify the running container's configured image, image ID/digest, source revision, health endpoint, application version, and the same user workflows checked in preview. Keep the prior image and backup until the migration is accepted.

The imported feature work includes database migrations. If rollback is necessary, stop the new server and restore the matching pre-upgrade application-data backup before starting the old image. Do not assume an older binary can safely use a database already migrated by a newer one.

## Remaining optional setup

The independent GitHub repository and GHCR publication use the repository's `GITHUB_TOKEN`; no Docker Hub credentials are needed. Check package visibility separately and verify anonymous pulling for public installation instructions.

A custom domain, DNS/proxy changes, Homebrew tap, profile pinning, and eventual old-fork deletion are separate from this initial transplant. The inherited `scriberr.app` CNAME and donation configuration are removed because they belong to the upstream project.
