#!/usr/bin/env bash
# sync-ac-src.sh — mirror alteredCarbon source into the scion build
# context so go.mod's `replace` directive resolves both on the host
# and inside the Docker build.
#
# scion's go.mod has:
#   replace github.com/mattkorwel/alteredCarbon => ./image-build/scion-base/_ac-src
#
# That path needs to:
#   1. Exist on the host (so `go build`, `go vet`, `go test` and
#      Go-based IDE tooling resolve the replace).
#   2. Exist inside the Docker build context (so the scion-base
#      Dockerfile's `RUN go mod download` resolves the same replace
#      against the in-container path).
#
# This script keeps (1) and (2) in sync by rsync'ing AC source into
# image-build/scion-base/_ac-src/. It's a no-op when AC source isn't
# available — host builds will then fail with a clearer error than
# Docker builds would. Run it before `image-build/scripts/build-images.sh`
# (which already invokes it via the build-ac.sh hook).
#
# Env overrides:
#   AC_SOURCE_DIR     path to alteredCarbon source
#                     (default: ~/dev/altered-carbon)
#   AC_SRC_DEST       sync destination
#                     (default: <scion-repo>/image-build/scion-base/_ac-src)

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SCION_REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"

AC_SOURCE_DIR="${AC_SOURCE_DIR:-${HOME}/dev/altered-carbon}"
AC_SRC_DEST="${AC_SRC_DEST:-${SCION_REPO_ROOT}/image-build/scion-base/_ac-src}"

c_dim()   { printf '\033[2m%s\033[0m' "$*"; }
c_green() { printf '\033[32m%s\033[0m' "$*"; }
c_yellow(){ printf '\033[33m%s\033[0m' "$*"; }
log()     { printf '%s %s\n' "$(c_dim '==>')" "$*"; }
ok()      { printf '%s %s\n' "$(c_green '  ✓')" "$*"; }
warn()    { printf '%s %s\n' "$(c_yellow ' !!')" "$*" >&2; }

if [[ ! -d "${AC_SOURCE_DIR}" ]]; then
  warn "AC_SOURCE_DIR=${AC_SOURCE_DIR} does not exist; nothing to sync."
  warn "Host builds will fail with 'replace target not found' until AC is available."
  exit 0
fi

if [[ ! -f "${AC_SOURCE_DIR}/go.mod" ]]; then
  warn "AC_SOURCE_DIR=${AC_SOURCE_DIR} is not a Go module (no go.mod); skipping sync."
  exit 0
fi

mkdir -p "${AC_SRC_DEST}"

log "Syncing AC source: ${AC_SOURCE_DIR} -> ${AC_SRC_DEST}"

# rsync excludes:
#   .git/             — large, unneeded; we don't run git in-container
#   _ac-bin/          — AC's own binary outputs (irrelevant; AC builds
#                       happen elsewhere via build-ac.sh)
#   *_test.go         — keep tests for vet/build; drop is optional
#                       (uncomment the line below to drop)
#   internal/cli/     — only pkg/api + pkg/brain are imported by scion;
#                       keeping all of internal/ keeps the module
#                       compileable in case test files reference it
#                       transitively. Cheap (a few hundred KB).
rsync -a --delete \
  --exclude '.git/' \
  --exclude '_ac-bin/' \
  --exclude 'cmd/ac/' \
  "${AC_SOURCE_DIR}/" "${AC_SRC_DEST}/"

# Sanity: confirm pkg/api and pkg/brain landed (these are what scion
# actually imports). Failure here means AC's layout has changed since
# this script was written.
for d in pkg/api pkg/brain; do
  if [[ ! -d "${AC_SRC_DEST}/${d}" ]]; then
    warn "AC source synced but missing ${d}/ — AC layout may have changed."
    exit 1
  fi
done

ok "AC source synced: $(du -sh "${AC_SRC_DEST}" | cut -f1)"
