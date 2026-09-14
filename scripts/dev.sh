#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

if [ ! -f web/frontend/node_modules/vite/bin/vite.js ]; then
  echo "Frontend dependencies missing. Run: cd web/frontend && npm ci" >&2
  exit 1
fi
command -v node >/dev/null
command -v go >/dev/null

# Go embed needs a file even when Vite serves the UI separately.
mkdir -p internal/web/dist tmp
if [ ! -f internal/web/dist/index.html ]; then
  printf '%s\n' '<!-- Development placeholder: use the Vite URL. -->' > internal/web/dist/index.html
fi

backend_pid=
frontend_pid=
cleanup() {
  trap - EXIT INT TERM
  for pid in "$backend_pid" "$frontend_pid"; do
    if [ -n "$pid" ]; then
      kill "$pid" 2>/dev/null || true
    fi
  done
  for pid in "$backend_pid" "$frontend_pid"; do
    if [ -n "$pid" ]; then
      wait "$pid" 2>/dev/null || true
    fi
  done
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

if command -v air >/dev/null 2>&1; then
  air &
else
  echo "Air is not installed; the backend will run without live reload."
  go build -o tmp/jotist-dev ./cmd/server
  ./tmp/jotist-dev &
fi
backend_pid=$!

# Run Vite directly so the tracked PID is the server, not an npm wrapper.
(cd web/frontend && exec node node_modules/vite/bin/vite.js) &
frontend_pid=$!

# Bash 3.2 (macOS) has no wait -n. Stop the sibling if either server exits.
while kill -0 "$backend_pid" 2>/dev/null && kill -0 "$frontend_pid" 2>/dev/null; do
  sleep 1
done
status=0
if ! kill -0 "$backend_pid" 2>/dev/null; then
  wait "$backend_pid" || status=$?
  backend_pid=
else
  wait "$frontend_pid" || status=$?
  frontend_pid=
fi
exit "$status"
