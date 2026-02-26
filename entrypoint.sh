#!/bin/sh
set -e

# Ensure the data directory exists and is writable by the patchdeck user.
# Docker may create bind-mount directories as root, so we fix ownership
# before dropping privileges. This is the standard container pattern used
# by Postgres, Redis, Gitea, etc.
DATA_DIR="$(dirname "${PATCHDECK_DB_PATH:-/data/patchdeck.db}")"
mkdir -p "$DATA_DIR"
chown -R patchdeck:patchdeck "$DATA_DIR"

exec su-exec patchdeck /app/patchdeck "$@"
