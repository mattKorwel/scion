#!/usr/bin/env bash
# build-ac.sh — cross-build the alteredCarbon `ac` binary for every
# target arch a scion harness image might run on, dropping artifacts
# under image-build/scion-base/_ac-bin/ where scion-base's Dockerfile
# COPYs them into the final image.
#
# Run this once before `build-images.sh` (or `scion-apply`) so the
# scion-base layer has fresh `ac` binaries to bake in. Idempotent;
# re-runs cleanly. Skipped when AC_SOURCE_DIR doesn't exist (alteredCarbon
# isn't checked out locally), in which case the scion-base Dockerfile
# falls back to its no-AC path (no `ac` binary in the image).
#
# Env overrides:
#   AC_SOURCE_DIR     path to alteredCarbon checkout
#                     (default: ~/dev/altered-carbon)
#   AC_OUT_DIR        artifact destination
#                     (default: <scion-repo>/image-build/scion-base/_ac-bin)
#   AC_PLATFORMS      space-separated GOOS/GOARCH pairs
#                     (default: "linux/amd64 linux/arm64 darwin/arm64")

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SCION_REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"

AC_SOURCE_DIR="${AC_SOURCE_DIR:-${HOME}/dev/altered-carbon}"
AC_OUT_DIR="${AC_OUT_DIR:-${SCION_REPO_ROOT}/image-build/scion-base/_ac-bin}"
AC_PLATFORMS="${AC_PLATFORMS:-linux/amd64 linux/arm64 darwin/arm64}"

c_dim()   { printf '\033[2m%s\033[0m' "$*"; }
c_green() { printf '\033[32m%s\033[0m' "$*"; }
c_yellow(){ printf '\033[33m%s\033[0m' "$*"; }
log()     { printf '%s %s\n' "$(c_dim '==>')" "$*"; }
ok()      { printf '%s %s\n' "$(c_green '  ✓')" "$*"; }
warn()    { printf '%s %s\n' "$(c_yellow ' !!')" "$*" >&2; }

if [[ ! -d "${AC_SOURCE_DIR}" ]]; then
  warn "AC_SOURCE_DIR=${AC_SOURCE_DIR} does not exist; skipping ac binary build."
  warn "scion-base will be built without the ac binary baked in."
  exit 0
fi

if [[ ! -f "${AC_SOURCE_DIR}/cmd/ac/main.go" ]]; then
  warn "AC_SOURCE_DIR=${AC_SOURCE_DIR} is not an alteredCarbon checkout (no cmd/ac/main.go)."
  warn "scion-base will be built without the ac binary baked in."
  exit 0
fi

mkdir -p "${AC_OUT_DIR}"

# Resolve git commit for ldflags stamping. Best-effort; bare clone or
# missing git silently leaves it empty.
ac_commit=""
if command -v git >/dev/null 2>&1 && [[ -d "${AC_SOURCE_DIR}/.git" ]]; then
  ac_commit="$(git -C "${AC_SOURCE_DIR}" rev-parse --short=12 HEAD 2>/dev/null || true)"
fi

ldflags="-s -w"
if [[ -n "${ac_commit}" ]]; then
  ldflags="${ldflags} -X main.buildSHA=${ac_commit}"
fi

log "Building ac binaries from ${AC_SOURCE_DIR}"
log "Commit: ${ac_commit:-(unknown)}"
log "Out:    ${AC_OUT_DIR}"

cd "${AC_SOURCE_DIR}"

for platform in ${AC_PLATFORMS}; do
  goos="${platform%/*}"
  goarch="${platform#*/}"
  out="${AC_OUT_DIR}/ac-${goos}-${goarch}"
  log "  ${platform} -> ${out}"
  GOOS="${goos}" GOARCH="${goarch}" CGO_ENABLED=0 \
    go build -trimpath -ldflags="${ldflags}" -o "${out}" ./cmd/ac/
  ok "$(printf '%-12s %s' "${platform}" "$(stat -f%z "${out}" 2>/dev/null || stat -c%s "${out}" 2>/dev/null) bytes")"
done

ok "ac binaries ready in ${AC_OUT_DIR}"
