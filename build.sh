#!/usr/bin/env bash
set -euo pipefail

# Compatibility entrypoint; the build steps live in scripts/build.sh.
exec "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/scripts/build.sh" "$@"
