# Container deployment

Run these commands from the repository root. The default CPU deployment stays
at `docker-compose.yml`, so `docker compose up -d` continues to work. Alternate
configurations and all Docker build sources live here.

| Use | Command |
| --- | --- |
| Published CPU image | `docker compose up -d` |
| Published CUDA 12.6 image | `docker compose --project-directory . -f deploy/compose/cuda.yaml up -d` |
| Local CPU build | `docker compose --project-directory . -f deploy/compose/build.yaml up -d --build` |
| Local CUDA 12.6 build | `docker compose --project-directory . -f deploy/compose/build.cuda.yaml up -d --build` |
| Local Blackwell build with named volumes | `docker compose --project-directory . -f deploy/compose/blackwell.yaml up -d --build` |
| Local Blackwell build with bind mounts | `docker compose --project-directory . -f deploy/compose/build.blackwell.yaml up -d --build` |

Blackwell/CUDA 13 source builds remain unqualified for actual Blackwell model
inference. They are not part of normal stable image publication.

## Installation names and existing data

`--project-directory .` is required for the alternate configurations. It keeps
the project name, build context, `.env` lookup, and relative bind mounts rooted
at this checkout, rather than at `deploy/compose`. If you previously supplied
`--project-name` or `COMPOSE_PROJECT_NAME`, keep that same value.

New installations use the `jotist` service, `jotist_data` named volume, and
`jotist.db` database. Source builds use `./jotist-data` for application data.
The container runs `/app/jotist-server` and serves Jotist CLI downloads.
The image templates follow stable `latest` and `latest-cuda` releases; pin a
released version or digest when you need a reproducible deployment.

For an existing installation, carry forward the actual storage mapping before
starting these templates. Configure `DATABASE_PATH` for the existing database
or rename the database while the server is stopped. Follow the
[migration guide](../docs/jotist-migration.md); changing a service or volume key
alone does not transfer its data. Check your resolved configuration:

```bash
docker compose --project-directory . -f deploy/compose/cuda.yaml config
```

Back up and qualify an existing installation using the
[migration guide](../docs/jotist-migration.md). File relocation does not migrate
data or change a running container.

| Previous path | Current path |
| --- | --- |
| `Dockerfile` | `deploy/docker/Dockerfile.cpu` |
| `Dockerfile.cuda` | `deploy/docker/Dockerfile.cuda` |
| `Dockerfile.cuda.13.0` | `deploy/docker/Dockerfile.blackwell` |
| `docker-entrypoint.sh` | `deploy/docker/entrypoint.sh` |
| `docker-compose.cuda.yml` | `deploy/compose/cuda.yaml` |
| `docker-compose.blackwell.yml` | `deploy/compose/blackwell.yaml` |
| `docker-compose.build.yml` | `deploy/compose/build.yaml` |
| `docker-compose.build.cuda.yml` | `deploy/compose/build.cuda.yaml` |
| `docker-compose.build.blackwell.yml` | `deploy/compose/build.blackwell.yaml` |

## Building directly

Docker build context is always the repository root:

```bash
docker build -f deploy/docker/Dockerfile.cpu -t jotist:local .
docker build -f deploy/docker/Dockerfile.cuda -t jotist:local-cuda .
```

The container's entrypoint path stays `/usr/local/bin/docker-entrypoint.sh`.
Release automation uses these same Dockerfiles and supplies the release version
and source commit. See [release maintenance](../docs/jotist-releases.md).

## Health and startup

`GET /health` is a lightweight liveness and release-identity response. `GET /ready` returns HTTP 200 only when the database is reachable and the processing queue has started; it returns 503 with generic component states otherwise. A busy worker or waiting jobs are normal ready states. `/ready` does not load models, download weights or prove GPU inference.

Docker health checks use `/ready`. Models prepare on first use under the run's model-file lease, so starting the server does not restore intentionally deleted weights. The first run after a model removal may take longer while downloading it again.

The root entrypoint applies both `PUID` and `PGID`, including an existing host group, and refuses to launch with a mismatched identity. When using Docker's explicit `--user`, that identity takes precedence. Keep persistent mount permissions compatible with the selected user. Configure restart/autostart behavior in your deployment manager; a Docker health check reports health and does not itself restart an unhealthy container.

## Required cleanup after an accepted deployment

Agents and maintainers must complete this step for manual Compose, Portainer and
native Docker deployments. Image publication alone does not run NAS cleanup.
The helper requires Python 3.9+ on the client and Docker access locally or through
an existing SSH alias; the Docker host does not need Python.

Before pulling/replacing the service, capture its immutable identity in a new,
private journal. Replace `nas`, `jotist` and the unique journal name with the
actual SSH alias, existing container name and deployment ID:

```sh
python3 scripts/deployment_cleanup.py --ssh-host nas capture \
  --service jotist --state tmp/deployment-UNIQUE.json
```

Keep the manager's configuration and a consistent data/key backup separately.
Record the previous registry digest or preserve its build artifact for recovery;
do not add a permanent local rollback tag. Track only disposable resources you
created, using full IDs returned by Docker, for example:

```sh
python3 scripts/deployment_cleanup.py --ssh-host nas track \
  --state tmp/deployment-UNIQUE.json --container-id FULL_64_CHARACTER_CANARY_ID
python3 scripts/deployment_cleanup.py --ssh-host nas track \
  --state tmp/deployment-UNIQUE.json --image-id sha256:FULL_64_CHARACTER_IMAGE_ID
```

After recreation, record the replacement's full container ID and image ID before
running acceptance checks. Verify the intended image/revision, `/ready`, `/health`, the UI, existing database/audio, model-cache mounts and the qualified installed Python runtime graph. Exercise the selected model pipeline when the update affects inference. A ready endpoint alone does not qualify model inference.
Keep the old container stopped and available until acceptance. Stop any owned
qualification containers by their recorded IDs. Then run:

```sh
python3 scripts/deployment_cleanup.py --ssh-host nas finish \
  --state tmp/deployment-UNIQUE.json --container-id ACCEPTED_FULL_CONTAINER_ID \
  --image-id sha256:ACCEPTED_FULL_IMAGE_ID --verified --dry-run
# Review the preview, then repeat without --dry-run to perform cleanup.
python3 scripts/deployment_cleanup.py --ssh-host nas finish \
  --state tmp/deployment-UNIQUE.json --container-id ACCEPTED_FULL_CONTAINER_ID \
  --image-id sha256:ACCEPTED_FULL_IMAGE_ID --verified
```

`--verified` declares that the project-specific checks above passed; it does not
run them. The helper independently requires the exact accepted service to remain
running (and healthy when Docker has a health check), and rechecks it before
removal. It removes only recorded stopped/Created containers and unreferenced
images by immutable ID. Every container state counts as an image reference;
namespace, volume and writable-root dependencies are protected, including Unraid
Btrfs bind aliases. Shared images stay. Mutable tags are never deletion targets.
Native Docker may refuse a multi-tag image; report that blocker rather than force
removal. Avoid creating extra rollback tags in the first place.

If acceptance fails, preserve recovery resources and follow the rollback
procedure. If cleanup fails after acceptance, keep the verified service running,
report the blocker and retry the same journal after resolving it. A retry is
idempotent. Include actual removed/protected IDs and cleanup status in the
receipt. A dry run is a preview, not completed cleanup.

This does not delete persistent data, keys, model caches, volumes, backups, VMs,
ISOs or build caches. It never runs a host-wide prune. Do not delete Docker's
internal storage paths manually. Existing unrecorded leftovers need a separate
ownership/reference audit; this workflow prevents new untracked leftovers.
