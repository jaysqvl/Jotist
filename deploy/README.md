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

## Existing installations

`--project-directory .` is required for the alternate configurations. It keeps
the project name, build context, `.env` lookup, and relative bind mounts rooted
at this checkout, rather than at `deploy/compose`. If you previously supplied
`--project-name` or `COMPOSE_PROJECT_NAME`, keep that same value.

The existing `scriberr` service, named-volume keys, bind-directory names,
container names, ports, and environment defaults are preserved. CPU source
builds retain `./scriberr-data`; CUDA source builds retain `./scriberr_data`.
These are different existing directories, not interchangeable spellings.
Check your resolved configuration before an upgrade:

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
