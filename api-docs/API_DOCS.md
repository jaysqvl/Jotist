# API documentation

Jotist serves its REST API at `/api/v1`. The server also exposes `/health` and
`/ready` at the root. Use the [OpenAPI specification](openapi.json) from the
Git tag that matches your installed version. The server and the project site
use the same OpenAPI 3.1 contract. The last Swagger 2 exports are available
under the [v3.2.1 tag](https://github.com/jaysqvl/Jotist/tree/v3.2.1/api-docs).

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

The route declarations in `internal/api/routes*.go` register the Gin
handlers and the corresponding Huma operations together. Authentication
requirements come from the same groups that install the middleware. Huma
derives JSON schemas from the Go request and response types. Multipart
fields, binary bodies and event streams have explicit media types so clients
can generate the right requests.

From the repository root, run:

```bash
make docs
```

This exports the native OpenAPI document to `api-docs/openapi.json` and
copies it to the project site's `public/api/openapi.json`. The running server
serves the contract produced by its own registered routes. Review and commit
the generated files with changes to the API.

Use `make website-dev` to inspect the reference locally after installing the
project site's dependencies (`cd web/project-site && npm ci`).
`make website-build` writes the website to `web/project-site/dist`; the root
`docs/` directory contains authored documentation and qualification records.

CI regenerates the exports and rejects stale output. Go tests validate the
OpenAPI document, compare methods and exact paths with the router, and check
the upload, streaming and authentication contracts. When changing an API,
update its route declaration and the Go types it uses.
