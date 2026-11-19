# API documentation

Jotist serves its REST API at `/api/v1`. The server also exposes `/health` and
`/ready` at the root. Use the [OpenAPI 3 specification](openapi.json) from the
Git tag that matches your installed version. The project site uses the
[Swagger 2.0 export](swagger.json) generated from the same annotations.

Import the JSON into your API client and set the server URL to your Jotist
installation, including its scheme and port. Use the endpoint paths as written.
For example, to list recordings with an existing bearer token:

```sh
JOTIST_URL=http://localhost:8080
curl -H "Authorization: Bearer ${JOTIST_TOKEN}" "${JOTIST_URL}/api/v1/transcription/list"
```

Open `/swagger/` on your Jotist server or follow **API reference** from
**Settings → API Keys**. Sign in to browse the specification packaged with
that build. Open an operation in the reference, choose **Try it out** to
edit its fields, then select **Build cURL** and use the snippet's copy button.
Swagger UI generates the forms and Bash cURL snippets from OpenAPI 3.
For file uploads, the command uses the selected names. Adjust the paths to
match the machine running cURL. Commands can use placeholders for
credentials and values that need cURL-specific quoting.

Open **Quick checks** above the reference to run one of four reviewed
reads using your Jotist sign-in. You can list recordings, check a
recording's status, get its transcript, or get the model catalog. Run
**List recordings** to fill the recording picker, or enter a recording ID.

The transcript example includes `read_only=true`, which returns the saved
transcript without creating or updating legacy run snapshots.

## Import into Postman

1. Open `api-docs/openapi.json` at the Git tag for your installed
   [Jotist release](https://github.com/jaysqvl/Jotist/releases) and select
   **Raw** to get the JSON file or its URL.
2. In Postman, choose **Import**, select the file or paste its URL, and import
   it as a **Collection**.
3. Set the server address to your Jotist URL, including the scheme and port.
   For protected requests, add an `X-API-Key` header with a key from
   **Settings → API Keys**.

You can also paste the cURL command from **Copy request** into **Import** to
bring in one request. Replace the credential placeholder in Postman.

## Follow live recording updates

Use **List recordings** to find the recording's `id`. Pass it as the
required `job_id` query parameter to `GET /api/v1/events/`.
The `200` response is a stream with content type `text/event-stream`.
The first message confirms the subscription.

```text
data: {"type":"connected","job_id":"RECORDING_ID"}
```

Subsequent messages arrive as the recording is processed. Use
`GET /api/v1/transcription/RECORDING_ID/status` for a single JSON response.

## About the embedded reference

The bundled Swagger UI renders the spec in a sandboxed frame with network
access blocked. Its request preview generates snippets in memory. The
four authenticated reads run through Jotist outside the frame, and their
responses stay out of the service-worker cache. The project site's `/api`
reference also supports browsing without a running instance.

The frontend lockfile pins `swagger-ui-dist`. When updating it, review
advisories for the upstream lockfile and verify the browser assets to be
shipped. An npm audit checks package metadata, which omits dependencies
compiled into the distribution's prebuilt JavaScript.

## Regenerate the specification from source

Regeneration is for contributors working in a Git checkout of Jotist. The
repository root is the checkout folder containing `Makefile` and `go.mod`.
For a Docker installation, use the spec from the Git tag matching your
installed release.

Handler annotations in `internal/api/*.go` and server metadata in
`cmd/server/main.go` define the specification. Paths in `@Router` annotations
are relative to the server origin and include `/api/v1` for versioned routes.

From the repository root, run:

```bash
make docs
```

This uses the pinned Swag generator in `scripts/generate-api-docs.sh` to write
`api-docs/docs.go`, `api-docs/swagger.json`, and `api-docs/swagger.yaml`. A Go
build tool uses kin-openapi to convert and validate `api-docs/openapi.json`
for the embedded reference, including the file schemas for multipart and
binary uploads. The script copies the Swagger 2.0 JSON to the project site
and regenerates its endpoint index, `undocumented.json`.
Review and commit the generated changes with the handler changes.

Use `make website-dev` to inspect the reference locally after installing the
project site's dependencies (`cd web/project-site && npm ci`).
`make website-build` writes the website to `web/project-site/dist`; the root
`docs/` directory contains authored documentation and qualification records.

CI regenerates the spec and rejects stale output. A Go test also compares
the documented methods and paths to the registered router in both
directions. It accounts for the reference bootstrap and CLI installer
routes explicitly. Keep the API annotations current when changing
parameters or responses.
