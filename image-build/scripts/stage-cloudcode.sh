#!/usr/bin/env bash
# stage-cloudcode.sh — copy the cloudcode linux/amd64 binary into the
# scion-cloudcode image build context so docker buildx can pick it up.
#
# Cloudcode is shipped internally as a static prebuilt binary, not via
# a public npm/apt package. fleet-apply distributes the binary to
# operator workstations at $HOME/dev/bin/cloudcode-linux-amd64 (path
# configurable via fleet.toml: cloudcode_linux). This script copies
# that binary to image-build/cloudcode/_bin/cloudcode where the
# Dockerfile's COPY directive expects it.
#
# Run before `build-images.sh --target {harnesses,common,all}` or any
# cloudbuild yaml that builds scion-cloudcode. Idempotent: re-copies
# every time so the image always reflects the latest local binary
# (cheap; the COPY layer hashes the bytes anyway).
#
# Env overrides:
#   CLOUDCODE_BIN   path to the source binary (default:
#                   $HOME/dev/bin/cloudcode-linux-amd64)
#   CLOUDCODE_OUT   destination (default: alongside the dockerfile)
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SCION_REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"

SRC="${CLOUDCODE_BIN:-${HOME}/dev/bin/cloudcode-linux-amd64}"
DST_DIR="${CLOUDCODE_OUT:-${SCION_REPO_ROOT}/image-build/cloudcode/_bin}"
DST="${DST_DIR}/cloudcode"

c_dim()  { printf '\033[2m%s\033[0m' "$*"; }
c_ok()   { printf '\033[32m  ✓\033[0m %s\n' "$*"; }
c_warn() { printf '\033[33mWARN:\033[0m %s\n' "$*"; }
c_die()  { printf '\033[31mERROR:\033[0m %s\n' "$*"; exit 1; }

if [[ ! -f "${SRC}" ]]; then
  c_die "cloudcode binary not found at ${SRC}.
  Set \$CLOUDCODE_BIN or run fleet-apply to populate \$HOME/dev/bin/cloudcode-linux-amd64."
fi

mkdir -p "${DST_DIR}"
cp -f "${SRC}" "${DST}"
chmod 0755 "${DST}"

c_ok "staged $(du -h "${DST}" | awk '{print $1}') cloudcode -> ${DST}"
