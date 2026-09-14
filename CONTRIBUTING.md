# Contributing to Jotist

Use the Go version in `go.mod` and Node.js 24. Model execution additionally
needs Python, uv, FFmpeg, and the dependencies of the selected adapter. Normal
frontend and Go checks do not require downloading model weights.

## Local development

```bash
cd web/frontend && npm ci && cd ../..
make dev
```

`make dev` runs Vite and the backend, stopping both when either exits or the
command is interrupted. If Air is already on `PATH`, it reloads the backend;
otherwise the backend is built once. Install Air explicitly if live reload is
needed; this command does not install or upgrade development tools.

For a server with the frontend embedded:

```bash
make build
./bin/jotist
```

Build outputs belong in `bin/`, `tmp/`, and the ignored frontend `dist/`
directories. Keep binaries, local logs, databases, runtime environments, and
downloaded weights out of commits. `make clean` removes local build outputs
without clearing Go's shared build cache or application data.

## Checks

Run the checks relevant to the code you changed:

```bash
cd web/frontend
npm test && npm run lint && npm run type-check
cd ../..
make build
make vet
make test
```

For API handler changes, run `make docs` and commit its generated changes with
the source. For website changes, install dependencies in `web/project-site`,
then run `make website-build` and `npm --prefix web/project-site run lint`.
The generator version is pinned in `scripts/generate-api-docs.sh`.

For Python runtime changes, follow [runtime maintenance](docs/python-runtime-maintenance.md).
Passing a resolver or import check does not establish model inference quality
or validate migration of an existing installation.

The optional `lefthook.yml` runs the same Go vet and frontend checks using
installed tools. The obsolete golangci-lint v1 configuration is removed; CI's
Go vet, tests, and vulnerability scan remain the supported Go checks. Frontend
formatter settings live in `web/frontend`; editor and agent project state is
personal and ignored.

Container build sources and alternate configurations are described in
[container deployment](deploy/README.md). The repository root remains the
Docker build context. Use `make build` instead of the former root `build.sh`.

## Review and release

Keep changes scoped, include the behavior and checks in the PR description,
and preserve existing CLI configuration and data paths. The command and Go
module name `scriberr` remain compatibility contracts even though the product
is Jotist. See [source ownership](docs/architecture.md) before adding another
implementation or generated copy.

Publishing uses the reviewed source and the existing
[release workflow](docs/jotist-releases.md). Publishing and deployment are
separate steps; qualify an image against retained runtime/data state before
cutover. Preserve the MIT notice, attribution, and third-party notices.
