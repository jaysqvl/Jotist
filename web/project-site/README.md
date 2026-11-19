# Jotist project website

This React/Vite project contains the public documentation and API reference.
It is separate from the application frontend in `web/frontend`.

Install dependencies with `npm ci` in this directory, then run `make dev` or
`make build`. These commands export the OpenAPI document from the Go
route registrations before running Vite. From the repository root, the
equivalent commands are `make website-dev` and `make website-build`.

The generated website goes into `web/project-site/dist` and is ignored by Git.
The root `docs/` directory is reserved for authored engineering documentation
and qualification records. Website publication is a separate operation.

See [`api-docs/API_DOCS.md`](../../api-docs/API_DOCS.md) for the export
and validation commands. Run `npm run lint` before submitting website changes.
