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
