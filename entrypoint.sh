#!/bin/sh
set -e

# PUID/PGID support (linuxserver.io convention)
# Default: 1000/1000. Override with environment variables.
PUID=${PUID:-1000}
PGID=${PGID:-1000}

# Create/update the patchdeck group and user with the requested IDs.
# If the group/user already exist (from Dockerfile), modify them in place.
if getent group patchdeck >/dev/null 2>&1; then
  sed -i "s/^patchdeck:x:[0-9]*:/patchdeck:x:${PGID}:/" /etc/group
else
  addgroup -g "$PGID" patchdeck
fi

if getent passwd patchdeck >/dev/null 2>&1; then
  sed -i "s/^patchdeck:x:[0-9]*:[0-9]*:/patchdeck:x:${PUID}:${PGID}:/" /etc/passwd
else
  adduser -D -H -u "$PUID" -G patchdeck patchdeck
fi

# Ensure the data directory exists and is writable.
DATA_DIR="$(dirname "${PATCHDECK_DB_PATH:-/data/patchdeck.db}")"
mkdir -p "$DATA_DIR"
chown -R "$PUID:$PGID" "$DATA_DIR"

# Ensure the TLS directory exists and is writable.
TLS_DIR="$(dirname "${PATCHDECK_TLS_CERT:-/data/tls/cert.pem}")"
mkdir -p "$TLS_DIR"
chown -R "$PUID:$PGID" "$TLS_DIR"

echo "Starting Patchdeck with UID=$PUID GID=$PGID"
exec su-exec patchdeck /app/patchdeck "$@"
