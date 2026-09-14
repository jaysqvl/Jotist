#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

case "${1:-}" in
  "")
    (cd web/frontend && npm run build)
    ;;
  --embed-only) ;;
  *)
    echo "Usage: $0 [--embed-only]" >&2
    exit 2
    ;;
esac

if [ ! -f web/frontend/dist/index.html ]; then
  echo "Frontend build missing. Run make frontend before make embed." >&2
  exit 1
fi

mkdir -p internal/web
rm -rf internal/web/dist
cp -R web/frontend/dist internal/web/dist

if [ "${1:-}" != --embed-only ]; then
  mkdir -p bin
  go build -o bin/jotist ./cmd/server
  echo "Built bin/jotist. Run ./bin/jotist to start the server."
fi
