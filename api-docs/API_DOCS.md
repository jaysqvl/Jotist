# API documentation

Handler annotations in `internal/api/*.go` and the server metadata in
`cmd/server/main.go` are the source of truth. Generated files are committed so
the Go application and project site use the same specification.

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
