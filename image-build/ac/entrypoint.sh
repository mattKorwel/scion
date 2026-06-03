#!/usr/bin/env bash
# ac-corprun-entrypoint — Cloud Run entrypoint for the AC HTTP server.
#
# Responsibilities:
#   1. Translate Cloud Run's $PORT env into the `ac server serve --listen`
#      flag.
#   2. Bootstrap the git-backed vault from $AC_VAULT_REMOTE_URL if set
#      (clone on first start; otherwise leave the existing local repo
#      alone).
#   3. Configure git remote + push behavior so writes persist.
#   4. exec into `ac server serve` so signals propagate cleanly to PID 1.
#
# Env contract: see image-build/ac/Dockerfile.

set -euo pipefail

VAULT_DIR="${AC_VAULT_DIR:-/home/scion/.altered-carbon/vault}"
PORT="${PORT:-8080}"
REMOTE_NAME="${AC_VAULT_REMOTE_NAME:-origin}"
PUSH_TIMEOUT="${AC_PUSH_TIMEOUT:-10s}"
PUSH_ASYNC="${AC_PUSH_ASYNC:-false}"
PET_LABEL="${AC_PET_LABEL:-ac-corprun}"

log() { printf '[ac-corprun-entrypoint] %s\n' "$*" >&2; }

# Bootstrap the vault.
if [[ ! -d "${VAULT_DIR}/.git" ]]; then
  if [[ -n "${AC_VAULT_REMOTE_URL:-}" ]]; then
    log "Cloning vault from ${AC_VAULT_REMOTE_URL} into ${VAULT_DIR}"
    mkdir -p "$(dirname "${VAULT_DIR}")"
    git clone --origin "${REMOTE_NAME}" "${AC_VAULT_REMOTE_URL}" "${VAULT_DIR}"
  else
    log "WARN: AC_VAULT_REMOTE_URL unset; initializing ephemeral local vault at ${VAULT_DIR}"
    log "WARN: data will be lost on container recycle. Use only for smoke tests."
    mkdir -p "${VAULT_DIR}"
    git -C "${VAULT_DIR}" init -q
    # AC requires a non-empty repo (at least one commit) to be a valid
    # vault. Drop a trivial seed.
    : > "${VAULT_DIR}/.gitkeep"
    git -C "${VAULT_DIR}" -c user.email="${PET_LABEL}@corprun" \
                          -c user.name="${PET_LABEL}" \
        add .gitkeep
    git -C "${VAULT_DIR}" -c user.email="${PET_LABEL}@corprun" \
                          -c user.name="${PET_LABEL}" \
        commit -q -m "init: ephemeral vault seed"
  fi
else
  log "Reusing existing vault at ${VAULT_DIR}"
  if [[ -n "${AC_VAULT_REMOTE_URL:-}" ]]; then
    # Make sure the remote URL matches what we want (covers the case of
    # the remote URL changing between deploys; idempotent otherwise).
    if git -C "${VAULT_DIR}" remote get-url "${REMOTE_NAME}" >/dev/null 2>&1; then
      git -C "${VAULT_DIR}" remote set-url "${REMOTE_NAME}" "${AC_VAULT_REMOTE_URL}"
    else
      git -C "${VAULT_DIR}" remote add "${REMOTE_NAME}" "${AC_VAULT_REMOTE_URL}"
    fi
    log "Pulling latest from ${REMOTE_NAME}"
    git -C "${VAULT_DIR}" pull --ff-only "${REMOTE_NAME}" || \
      log "WARN: pull failed (continuing with local state)"
  fi
fi

# Configure committer identity for ac-server's commits. AC defaults to
# its own author; we override committer so cloud-run-side commits are
# distinguishable.
git -C "${VAULT_DIR}" config user.email "${PET_LABEL}@corprun"
git -C "${VAULT_DIR}" config user.name  "${PET_LABEL}"

# Build the `ac server serve` argv.
args=(
  server serve
  --repo "${VAULT_DIR}"
  --listen "0.0.0.0:${PORT}"
  --pet-label "${PET_LABEL}"
)

if [[ -n "${AC_VAULT_REMOTE_URL:-}" ]]; then
  args+=(
    --remote "${REMOTE_NAME}"
    --push-timeout "${PUSH_TIMEOUT}"
  )
  if [[ "${PUSH_ASYNC}" == "true" ]]; then
    args+=(--push-async)
  fi
fi

# Pull-timeout is supported by ac (via gitstore Config.PullTimeout) but
# not exposed as a server-serve flag in the version we have. Skip for
# now; reads are local-only, which is fine for a single-instance Cloud
# Run service.

log "Starting: ac ${args[*]}"
exec ac "${args[@]}"
