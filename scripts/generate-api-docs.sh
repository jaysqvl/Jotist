#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

# Handler annotations are the source of truth. Pin the generator for local/CI parity.
go run github.com/swaggo/swag/cmd/swag@v1.16.6 init \
  -g server/main.go --dir cmd,internal -o api-docs
mkdir -p web/project-site/public/api
cp api-docs/swagger.json web/project-site/public/api/swagger.json
(cd web/project-site && node scripts/gen-endpoints.mjs)
