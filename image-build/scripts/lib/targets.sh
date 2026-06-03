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

# Target DAG and step descriptors for the scion image build orchestrator.
#
# This file is sourced by build-images.sh. It is the single source of truth
# for which images exist, which target names expand to which ordered step
# lists, and which dockerfile / context dir / build-args each step uses.
#
# Builders never read this file. The orchestrator translates step descriptors
# into the uniform builder_build call.
#
# Harness images shipped by the fleet (2026-05-28):
#   - scion-gemini      gemini-cli, npm-installed from public registry
#   - scion-cloudcode   cloudcode, internal prebuilt linux binary baked in
#                       from image-build/cloudcode/_bin/cloudcode
#   - scion-jetski      INTENTIONALLY ABSENT. Jetski runs via the host's
#                       /usr/bin/jetski bind-mounted into a scion-base
#                       container — see harness-configs/jetski/config.yaml.
#                       Building it as a self-contained image would require
#                       corp apt access from inside the build context,
#                       which Cloud Build's default network can't provide.
#
# Previously-built harness images (scion-claude, scion-codex, scion-opencode)
# were removed in this CL — operator-side allowed set is gemini/cloudcode/jetski.

# All known step IDs. The step ID is also the published image name
# (without registry prefix).
ALL_STEP_IDS=(
  core-base
  scion-base
  scion-gemini
  scion-cloudcode
  scion-hub
  scion-hub-corprun
  scion-ac
)

# All known target names. Used by the orchestrator's --help and --target
# validation.
ALL_TARGETS=(
  core-base
  scion-base
  harnesses
  hub
  hub-corprun
  ac
  corprun
  common
  all
)

# resolve_targets <target>
#
# Echoes one step ID per line, in build order, for the given target. Returns
# nonzero (and prints nothing on stdout) for an unknown target.
resolve_targets() {
  case "$1" in
    core-base)
      echo core-base
      ;;
    scion-base)
      echo scion-base
      ;;
    harnesses)
      printf '%s\n' scion-gemini scion-cloudcode
      ;;
    hub)
      echo scion-hub
      ;;
    hub-corprun)
      echo scion-hub-corprun
      ;;
    ac)
      echo scion-ac
      ;;
    corprun)
      # The go/corp-run pair: Cloud-Run-shaped hub + AC server. Mirrors
      # cloudbuild-corprun.yaml.
      printf '%s\n' scion-hub-corprun scion-ac
      ;;
    common)
      printf '%s\n' scion-base scion-gemini scion-cloudcode scion-hub
      ;;
    all)
      printf '%s\n' core-base scion-base scion-gemini scion-cloudcode scion-hub
      ;;
    *)
      return 1
      ;;
  esac
}

# step_image_name <step_id>
step_image_name() {
  echo "$1"
}

# step_dockerfile <step_id>
#
# Echoes the absolute path to the dockerfile for the step. Requires
# IMAGE_BUILD_DIR to be set in the environment.
step_dockerfile() {
  case "$1" in
    core-base)       echo "${IMAGE_BUILD_DIR}/core-base/Dockerfile" ;;
    scion-base)      echo "${IMAGE_BUILD_DIR}/scion-base/Dockerfile" ;;
    scion-gemini)    echo "${IMAGE_BUILD_DIR}/gemini/Dockerfile" ;;
    scion-cloudcode) echo "${IMAGE_BUILD_DIR}/cloudcode/Dockerfile" ;;
    scion-hub)       echo "${IMAGE_BUILD_DIR}/hub/Dockerfile" ;;
    scion-hub-corprun) echo "${IMAGE_BUILD_DIR}/hub-corprun/Dockerfile" ;;
    scion-ac)        echo "${IMAGE_BUILD_DIR}/ac/Dockerfile" ;;
    *) return 1 ;;
  esac
}

# step_context_dir <step_id>
#
# Echoes the absolute path to the build context for the step. scion-base
# uses the repo root because it copies go source; everything else uses its
# own image-build subdirectory.
step_context_dir() {
  case "$1" in
    core-base)       echo "${IMAGE_BUILD_DIR}/core-base" ;;
    scion-base)      echo "${REPO_ROOT}" ;;
    scion-gemini)    echo "${IMAGE_BUILD_DIR}/gemini" ;;
    scion-cloudcode) echo "${IMAGE_BUILD_DIR}/cloudcode" ;;
    scion-hub)       echo "${IMAGE_BUILD_DIR}/hub" ;;
    scion-hub-corprun) echo "${IMAGE_BUILD_DIR}/hub-corprun" ;;
    scion-ac)        echo "${IMAGE_BUILD_DIR}/ac" ;;
    *) return 1 ;;
  esac
}

# step_build_args <step_id>
#
# Emits one KEY=VALUE line per build-arg on stdout. Reads orchestrator
# state from environment: REGISTRY, TAG, SHORT_SHA, COMMIT_SHA, BASE_TAG.
# BASE_TAG is the tag (sha or mutable) the orchestrator chose for this
# step's parent image. When REGISTRY is empty (local-only build), BASE_IMAGE
# is emitted with a bare image name (e.g. core-base:latest) so it matches
# the tag the previous step actually wrote into the local image store.
step_build_args() {
  local prefix=""
  if [[ -n "${REGISTRY:-}" ]]; then
    prefix="${REGISTRY}/"
  fi
  case "$1" in
    core-base)
      # No build-args.
      ;;
    scion-base)
      echo "BASE_IMAGE=${prefix}core-base:${BASE_TAG}"
      if [[ -n "${COMMIT_SHA:-}" ]]; then
        echo "GIT_COMMIT=${COMMIT_SHA}"
      fi
      ;;
    scion-gemini|scion-cloudcode|scion-hub|scion-hub-corprun|scion-ac)
      echo "BASE_IMAGE=${prefix}scion-base:${BASE_TAG}"
      ;;
    *) return 1 ;;
  esac
}

# step_parent <step_id>
#
# Echoes the step ID of the parent image, or empty for root images. Used by
# the orchestrator to thread :short-sha through chained builds and pick the
# right :tag fallback for standalone targets.
step_parent() {
  case "$1" in
    core-base)       echo "" ;;
    scion-base)      echo "core-base" ;;
    scion-gemini|scion-cloudcode|scion-hub|scion-hub-corprun|scion-ac)
      echo "scion-base"
      ;;
    *) return 1 ;;
  esac
}
