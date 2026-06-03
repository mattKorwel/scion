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

# Scion Hub for go/corp-run image — extends scion-base to run the
# centralized hub server in a Cloud-Run-shaped container. Sibling to
# image-build/hub/, which targets the cloudtop+systemd deployment with
# its own port + filesystem assumptions.
#
# Differences from image-build/hub/:
#   - Honors Cloud Run's $PORT env (defaults to 8080).
#   - Uses an entrypoint wrapper to construct the right
#     `scion server start ...` argv from corp-run env vars.
#   - Does NOT enable --enable-runtime-broker by default; this image is
#     hub-only. Brokers run elsewhere (cloudtop fleet hosts).
#   - Does NOT enable --enable-web by default; the web frontend is a
#     separate concern not in this image's scope.
#
# Cloud Run env contract:
#   $PORT                   — listen port. Defaults to 8080. Required for
#                             Cloud Run.
#   $SCION_HUB_DB_URL       — optional. Database URL/path. Defaults to
#                             sqlite at /home/scion/.scion/hub.db inside
#                             the container (ephemeral; data lost on
#                             recycle). For real persistence point this
#                             at a Cloud SQL connection string or a
#                             GCS-backed sqlite path.
#   $SCION_STORAGE_BUCKET   — GCS bucket for template storage.
#   $SCION_STORAGE_DIR      — alternative: local dir for template storage.
#   $SCION_ADMIN_EMAILS     — comma-separated admin bootstrap list.
#   $SCION_ENABLE_DEBUG     — "true" for verbose logs.
#   $SCION_ENABLE_DEV_AUTH  — "true" to enable dev auth (insecure; ACLaim
#                             at the corp-run edge already authn'd).

ARG BASE_IMAGE
FROM ${BASE_IMAGE}

USER root

# Hub state lives under /home/scion/.scion (sqlite, templates, etc.).
# scion-base already created the user; ensure the dir survives any
# image flattening and is writable.
RUN mkdir -p /home/scion/.scion && \
    chown -R scion:scion /home/scion/.scion

# Drop the entrypoint wrapper.
COPY --chmod=0755 entrypoint.sh /usr/local/bin/hub-corprun-entrypoint

USER scion

ENV PORT=8080

# scion-base's ENTRYPOINT runs sciontool init -- <cmd>. Override it: we
# don't need init in this image, just a clean exec into the server.
ENTRYPOINT ["/usr/local/bin/hub-corprun-entrypoint"]
