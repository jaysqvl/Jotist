# Source ownership

Jotist is a Go server with an embedded React frontend and Python model adapters.
The project website is a separate build. Prefer the owners below when changing
behavior instead of introducing another implementation or generated copy.

| Area | Source owner | Responsibility |
| --- | --- | --- |
| Server entrypoint | `cmd/server` | Application setup and dependency wiring |
| HTTP API | `internal/api` | Request validation, authentication boundaries, and API annotations |
| Persistence | `internal/models`, `internal/repository`, `internal/database` | Stored models, queries, and schema changes |
| Scheduling and execution | `internal/queue`, `internal/execution`, `internal/transcription` | Queue lifecycle, recovery, model selection, and transcription orchestration |
| Model adapters | `internal/transcription/adapters` | Go-to-Python contracts and per-model execution |
| Python runtimes | `internal/transcription/adapters/py` | Dependency recipes, embedded scripts, and vendored compatibility code |
| Application frontend | `web/frontend/src` | User workflows and browser state |
| Embedded frontend | `internal/web/static.go` | Serving the build generated from `web/frontend` |
| Compatible CLI | `cmd/scriberr-cli`, `internal/cli` | Folder watching and uploads; retain existing command/configuration names |
| Project website | `web/project-site` | Public documentation and API reference |
| Engineering documentation | `docs/*.md`, `docs/design`, qualification JSON | Decisions, operator procedures, and recorded validation evidence |

## Build and generated files

`Makefile` provides the supported local commands. `scripts/build.sh` owns the
application frontend build, embedding, and local server output (`bin/jotist`).
The root `build.sh` forwards to it. `make embed` only copies an already built
frontend and is shared with CI and archive builds. Docker retains its separate
frontend stage so its dependency cache works independently of Go.

`internal/web/dist` is generated and ignored; no other copied frontend bundle
belongs in the Go tree. The website builds into its own ignored `dist/`; the
root `docs/` is not a website build output.

API handler annotations generate `api-docs/docs.go`, JSON, and YAML via
`scripts/generate-api-docs.sh`. The JSON is copied to
`web/project-site/public/api/swagger.json`. These generated specifications are
committed together for reproducibility and are not independent source files.

## Behavior boundaries

- `AuthProvider` owns browser session initialization and its one renewal timer.
  `useAuth` reads the shared store; adding a consumer must not start another
  lifecycle. The fetch interceptor adds credentials only to the same-origin API.
- A queue has a fixed worker count for its lifetime. `QUEUE_WORKERS` overrides
  the server default of two; the durable pending records survive a full queue.
- Upload conversion finishes before a short database transaction publishes the
  recording and binds its upload session. Dispatch follows that commit, so a
  repeated completion request returns the same result. Quick results remain
  temporary and are bound before their worker starts.
- Recording deletion reserves queue ownership, commits the database aggregate,
  then removes original media. A database rollback retains the recording;
  filesystem cleanup failures are reported separately because files cannot
  participate in a SQLite transaction.
- Each Python environment is owned by its adapter recipe and the shared
  preparation lock/cache. Virtual environments resolve dependency conflicts;
  they are not security sandboxes. Keep reviewed vendor sources and audit
  evidence intact when changing runtime code.

## Validation boundaries

Go tests live beside their packages and in `tests/`. Frontend tests exercise
browser-independent logic; rendered UI changes also need browser inspection.
Python resolver, import, and audit checks are run through `scripts/` and the
workflows described in [runtime maintenance](python-runtime-maintenance.md).

A passing unit test, dependency resolution, installed import, real model
inference, retained-runtime migration, and live deployment verification answer
different questions. Record which checks actually ran. Release automation
builds immutable reviewed source; deployment follows the separate
[release](jotist-releases.md) and [migration](jotist-migration.md) procedures.
