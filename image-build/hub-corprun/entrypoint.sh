#!/usr/bin/env bash
# hub-corprun-entrypoint — Cloud Run entrypoint for the scion hub.
#
# Responsibilities:
#   1. Translate Cloud Run's $PORT env into `scion server start --port`.
#   2. Translate corp-run env vars into the scion flag namespace.
#   3. exec into `scion server start --foreground ...` so signals
#      propagate cleanly to PID 1.
#
# Env contract: see image-build/hub-corprun/Dockerfile.

set -euo pipefail

PORT="${PORT:-8080}"

log() { printf '[hub-corprun-entrypoint] %s\n' "$*" >&2; }

args=(
  server start
  --foreground
  --production
  --enable-hub
  --host 0.0.0.0
  --port "${PORT}"
)

if [[ -n "${SCION_HUB_DB_URL:-}" ]]; then
  args+=(--db "${SCION_HUB_DB_URL}")
fi

if [[ -n "${SCION_STORAGE_BUCKET:-}" ]]; then
  args+=(--storage-bucket "${SCION_STORAGE_BUCKET}")
elif [[ -n "${SCION_STORAGE_DIR:-}" ]]; then
  args+=(--storage-dir "${SCION_STORAGE_DIR}")
fi

if [[ -n "${SCION_ADMIN_EMAILS:-}" ]]; then
  args+=(--admin-emails "${SCION_ADMIN_EMAILS}")
fi

if [[ "${SCION_ENABLE_DEBUG:-false}" == "true" ]]; then
  args+=(--debug)
fi

if [[ "${SCION_ENABLE_DEV_AUTH:-false}" == "true" ]]; then
  args+=(--dev-auth)
fi

log "Starting: scion ${args[*]}"
exec scion "${args[@]}"
