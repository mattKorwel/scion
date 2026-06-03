# syntax=docker/dockerfile:1
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

# Scion AC (alteredCarbon) image — extends scion-base to run the
# alteredCarbon HTTP API server (`ac server serve`).
#
# scion-base already has the `ac` binary installed at /usr/local/bin/ac
# (built by image-build/scripts/build-ac.sh and COPYed in scion-base's
# Dockerfile). This layer just adds:
#   - a Cloud-Run-friendly entrypoint that honors $PORT and bootstraps
#     the git-backed vault from $AC_VAULT_REMOTE_URL on container start;
#   - default AC vault state under /home/scion/.altered-carbon/vault.
#
# Cloud Run env contract:
#   $PORT                   — listen port. Defaults to 8080. Required for
#                             Cloud Run.
#   $AC_VAULT_REMOTE_URL    — git URL to clone the vault from on first
#                             start, and push to on each write. If unset,
#                             the entrypoint creates a local-only ephemeral
#                             vault (data lost on container recycle —
#                             only useful for smoke tests).
#   $AC_VAULT_REMOTE_NAME   — git remote name. Defaults to "origin".
#   $AC_PUSH_TIMEOUT        — push timeout (Go duration). Defaults to "10s".
#   $AC_PUSH_ASYNC          — "true" to push in background. Defaults to
#                             "false" (sync push for write-after-read
#                             consistency on a single instance).
#   $AC_PET_LABEL           — identity stamped on records. Defaults to
#                             "ac-corprun".

ARG BASE_IMAGE
FROM ${BASE_IMAGE}

USER root

# AC vault lives under /home/scion/.altered-carbon. scion-base already
# created the scion user; ensure our subdir exists with correct ownership.
RUN mkdir -p /home/scion/.altered-carbon && \
    chown -R scion:scion /home/scion/.altered-carbon

# Drop the entrypoint wrapper. Kept readable to make ops debugging
# straightforward — it's <100 lines of bash.
COPY --chmod=0755 entrypoint.sh /usr/local/bin/ac-corprun-entrypoint

USER scion

# Cloud Run probes the container's $PORT for HTTP startup. We listen
# on 0.0.0.0 inside the container; ingress is gated by ACLaim/UberProxy
# at the corp-run edge.
ENV PORT=8080

# The scion-base ENTRYPOINT runs sciontool init. Override it for AC —
# we don't need sciontool init here, just the ac binary + our wrapper.
ENTRYPOINT ["/usr/local/bin/ac-corprun-entrypoint"]
