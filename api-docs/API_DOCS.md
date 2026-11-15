# API documentation

Jotist serves its REST API at `/api/v1`. The server also exposes `/health` and
`/ready` at the root. Use the [Swagger 2.0 specification](swagger.json) from the
Git tag that matches your installed version. The project site's API reference
uses the same specification.

Import the JSON into your API client and set the server URL to your Jotist
installation, including its scheme and port. Use the endpoint paths as written.
For example, to list recordings with an existing bearer token:

```sh
JOTIST_URL=http://localhost:8080
curl -H "Authorization: Bearer ${JOTIST_TOKEN}" "${JOTIST_URL}/api/v1/transcription/list"
```

The Docker image and standalone server serve the application UI and REST API.
For a browsable API reference, use the project site's `/api` page or import
the spec into a local Swagger viewer. There is no `/swagger/index.html` route
on the Jotist server.

## Regenerate the specification

Handler annotations in `internal/api/*.go` and server metadata in
`cmd/server/main.go` define the specification. Paths in `@Router` annotations
are relative to the server origin and include `/api/v1` for versioned routes.

From the repository root, run:

```bash
make docs
```

This uses the pinned Swag generator in `scripts/generate-api-docs.sh` to write
`api-docs/docs.go`, `api-docs/swagger.json`, and `api-docs/swagger.yaml`. It copies
that JSON to `web/project-site/public/api/swagger.json` and regenerates the
site's endpoint index, `undocumented.json`. Do not edit those outputs by hand.
Review and commit the generated changes with the handler changes.

Use `make website-dev` to inspect the reference locally after installing the
project site's dependencies (`cd web/project-site && npm ci`).
`make website-build` writes the website to `web/project-site/dist`; the root
`docs/` directory contains authored documentation and qualification records.

CI runs the same generator and rejects uncommitted generated changes. Keep
the API annotations current when changing routes, parameters, or responses.
