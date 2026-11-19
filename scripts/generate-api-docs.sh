#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

# Handler annotations are the source of truth. Pin the generator for local/CI parity.
go run github.com/swaggo/swag/cmd/swag@v1.16.6 init \
  -g server/main.go --dir cmd,internal -o api-docs
openapi_output=$(mktemp api-docs/openapi.XXXXXX)
trap 'rm -f "$openapi_output"' EXIT
(cd scripts/openapi && go run -mod=readonly . ../../api-docs/swagger.json) > "$openapi_output"
mv "$openapi_output" api-docs/openapi.json
mkdir -p web/project-site/public/api
cp api-docs/swagger.json web/project-site/public/api/swagger.json
(cd web/project-site && node scripts/gen-endpoints.mjs)
