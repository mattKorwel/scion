#!/bin/bash
# Copyright 2026 Google LLC
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

# local-docker builder for the scion image build orchestrator.
#
# Implements the per-image builder contract on top of `docker buildx`.

BUILDER_MODE="per-image"
# Custom builder name for the docker-container driver path. The
# docker-container driver is required for multi-arch (--platform with
# multiple targets) but it isolates buildkit from the local docker daemon's
# image store, which breaks chained per-image builds (intermediate parent
# images written by step N can't be FROM-referenced in step N+1 without
# pushing to a real registry). Single-arch local builds use the default
# `docker` driver instead, which reads/writes the daemon image store
# directly and lets chained FROM references resolve from local tags.
BUILDX_MULTIARCH_INSTANCE="scion-builder"
BUILDX_INSTANCE="default"

builder_check() {
  if ! command -v docker >/dev/null 2>&1; then
    echo "Error: 'docker' not found in PATH."
    echo "Install Docker Desktop or the docker CLI before using --builder local-docker."
    return 1
  fi
  if ! docker buildx version >/dev/null 2>&1; then
    echo "Error: 'docker buildx' is not available."
    echo "Install the buildx plugin (Docker Desktop ships it; otherwise see https://docs.docker.com/buildx/)."
    return 1
  fi
}

builder_prepare() {
  # Pick docker-container driver only when multi-arch is requested (the docker
  # driver doesn't support multi-platform). For single-arch builds, use a
  # `docker` driver instance bound to the current docker context so each
  # step's output lands in the local docker image store and is visible to
  # the next step's FROM resolution.
  if [[ "${PLATFORMS:-}" == *","* ]]; then
    BUILDX_INSTANCE="${BUILDX_MULTIARCH_INSTANCE}"
  else
    # Find a docker-driver buildx instance whose endpoint matches the
    # current docker context. The literally-named "default" instance is
    # bound to the "default" docker context (unix:///var/run/docker.sock)
    # which may not be the active one (e.g. Docker Desktop uses
    # `desktop-linux`). Use the buildx instance whose endpoint is the
    # active context, or fall back to "default".
    local active_ctx
    active_ctx="$(docker context show 2>/dev/null || echo default)"
    if docker buildx inspect "${active_ctx}" >/dev/null 2>&1; then
      BUILDX_INSTANCE="${active_ctx}"
    else
      BUILDX_INSTANCE="default"
    fi
  fi

  if [[ "${DRY_RUN:-false}" == "true" ]]; then
    if [[ "${BUILDX_INSTANCE}" != "${BUILDX_MULTIARCH_INSTANCE}" ]]; then
      echo "[dry-run] docker buildx use ${BUILDX_INSTANCE}"
    else
      echo "[dry-run] docker buildx create --name ${BUILDX_INSTANCE} --use   # if missing"
      echo "[dry-run] docker buildx inspect --bootstrap"
    fi
    return 0
  fi

  if [[ "${BUILDX_INSTANCE}" != "${BUILDX_MULTIARCH_INSTANCE}" ]]; then
    docker buildx use "${BUILDX_INSTANCE}"
    return 0
  fi

  if ! docker buildx inspect "${BUILDX_INSTANCE}" >/dev/null 2>&1; then
    echo "Creating buildx builder '${BUILDX_INSTANCE}'..."
    docker buildx create --name "${BUILDX_INSTANCE}" --use
  else
    docker buildx use "${BUILDX_INSTANCE}"
  fi
  docker buildx inspect --bootstrap >/dev/null
}

# builder_build flag arguments:
#   --image-name <name>
#   --context-dir <abs path>
#   --dockerfile <abs path>
#   --tags <comma-separated full refs>
#   --platforms <comma-separated platforms or empty>
#   --build-arg KEY=VALUE   (repeatable)
#   --push <true|false>
#   --load <true|false>
builder_build() {
  local image_name="" context_dir="" dockerfile="" tags="" platforms="" push="false" load="false"
  local -a build_args=()

  while [[ $# -gt 0 ]]; do
    case "$1" in
      --image-name)  image_name="$2"; shift 2 ;;
      --context-dir) context_dir="$2"; shift 2 ;;
      --dockerfile)  dockerfile="$2"; shift 2 ;;
      --tags)        tags="$2"; shift 2 ;;
      --platforms)   platforms="$2"; shift 2 ;;
      --build-arg)   build_args+=("$2"); shift 2 ;;
      --push)        push="$2"; shift 2 ;;
      --load)        load="$2"; shift 2 ;;
      *) echo "local-docker: unknown builder_build flag: $1" >&2; return 1 ;;
    esac
  done

  local -a cmd=(docker buildx build)
  if [[ -n "${platforms}" ]]; then
    cmd+=(--platform "${platforms}")
  fi

  local IFS=','
  read -ra tag_list <<<"${tags}"
  unset IFS
  local t
  for t in "${tag_list[@]}"; do
    cmd+=(-t "${t}")
  done

  local arg
  for arg in ${build_args[@]+"${build_args[@]}"}; do
    cmd+=(--build-arg "${arg}")
  done

  cmd+=(-f "${dockerfile}")

  if [[ "${push}" == "true" ]]; then
    cmd+=(--push)
  elif [[ "${load}" == "true" && "${BUILDX_INSTANCE}" == "${BUILDX_MULTIARCH_INSTANCE}" ]]; then
    # The `docker` driver writes directly to the daemon image store, so
    # --load is redundant. Only pass --load when using the
    # docker-container driver (multi-arch path).
    cmd+=(--load)
  fi

  cmd+=("${context_dir}")

  echo "==> [local-docker] building ${image_name}..."
  if [[ "${DRY_RUN:-false}" == "true" ]]; then
    printf '[dry-run]'
    printf ' %q' "${cmd[@]}"
    printf '\n'
    return 0
  fi
  "${cmd[@]}"
  echo "    ${image_name} done."
}

builder_finalize() {
  :
}
