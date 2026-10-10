#!/bin/bash
set -euo pipefail

# Default values
PUID=${PUID:-1000}
PGID=${PGID:-1000}

echo "=== Jotist Container Setup ==="
echo "Requested UID: $PUID, GID: $PGID"
# Linux reserves 4294967295 as an invalid UID/GID. Validate before passing an
# identity to account tools or changing ownership; never continue as another user.
validate_id() {
    local value=$1 name=$2
    if [[ ! "$value" =~ ^(0|[1-9][0-9]{0,9})$ ]] || (( value > 4294967294 )); then
        echo "Invalid $name: expected a decimal Linux user/group ID." >&2
        exit 1
    fi
}

setup_user() {
    local target_uid=$1
    local target_gid=$2
    validate_id "$target_uid" PUID
    validate_id "$target_gid" PGID

    if ! getent group "$target_gid" >/dev/null; then
        groupmod -g "$target_gid" appuser
    fi
    # CUDA base images can already contain UID 1000, and Unraid's group 100
    # already exists. Keep the application account but set both IDs explicitly.
    if [ "$(id -u appuser)" != "$target_uid" ] || [ "$(id -g appuser)" != "$target_gid" ]; then
        usermod -o -u "$target_uid" -g "$target_gid" appuser
    fi
    test "$(id -u appuser)" = "$target_uid"
    test "$(id -g appuser)" = "$target_gid"
}

# Setup the user (only if running as root)
if [ "$(id -u)" = "0" ]; then
    setup_user "$PUID" "$PGID"

    # Set up directories with proper ownership
    echo "Setting up data directories..."
    mkdir -p /app/data/uploads /app/data/transcripts /app/whisperx-env
    chown -R "$PUID:$PGID" /app/data /app/whisperx-env
    chown "$PUID:$PGID" /home/appuser

    echo "=== Setup Complete ==="
    echo "Switching to user appuser (UID=$PUID, GID=$PGID) and starting application..."

    # Switch to the appuser and execute the command
    exec gosu appuser "$@"
else
    echo "Running as non-root user UID=$(id -u), GID=$(id -g)"

    # Just ensure directories exist
    mkdir -p /app/data/uploads /app/data/transcripts /app/whisperx-env 2>/dev/null || true

    echo "=== Setup Complete ==="
    echo "Starting Jotist application..."

    # Execute directly
    exec "$@"
fi
