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

# Scion Base Image - Adds sciontool and scion user on top of core-base
ARG BASE_IMAGE
FROM ${BASE_IMAGE} AS base

# Builder stage for sciontool compilation
FROM base AS builder
USER root
WORKDIR /src
ENV PATH="/usr/local/go/bin:${PATH}"
ENV CGO_ENABLED=1

# Build arguments for version info
ARG GIT_COMMIT
ARG BUILD_TIME

# Copy go.mod and go.sum first for better layer caching. The
# replace directive points at ./image-build/scion-base/_ac-src
# (alteredCarbon source mirror), so we need to seed the replace
# target before `go mod download` resolves it. The full AC source
# tree gets COPYed below alongside scion's own pkg/ — the early
# go.mod here is just enough to satisfy the replace.
COPY image-build/scion-base/_ac-src/go.mod image-build/scion-base/_ac-src/go.sum* ./image-build/scion-base/_ac-src/
COPY go.mod go.sum ./
RUN go mod download

# Copy source and build scion and sciontool. AC source lives under
# the build context as image-build/scion-base/_ac-src/; the replace
# directive in scion's go.mod resolves to ./image-build/scion-base/_ac-src
# both on the host and in this build.
COPY image-build/scion-base/_ac-src/ ./image-build/scion-base/_ac-src/
COPY cmd/ ./cmd/
COPY pkg/ ./pkg/
COPY web/*.go ./web/

# Build with version info injected via ldflags
# VCS stamping is disabled since .git is not in the build context;
# commit info is passed explicitly via the GIT_COMMIT build arg
RUN BUILD_TIME="${BUILD_TIME:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}" && \
    SCION_PKG="github.com/GoogleCloudPlatform/scion/pkg/version" && \
    SCIONTOOL_PKG="github.com/GoogleCloudPlatform/scion/cmd/sciontool/commands" && \
    SCION_LDFLAGS="-s -w -X ${SCION_PKG}.BuildTime=${BUILD_TIME}" && \
    SCIONTOOL_LDFLAGS="-s -w -X ${SCIONTOOL_PKG}.BuildTime=${BUILD_TIME}" && \
    if [ -n "${GIT_COMMIT}" ]; then \
        SCION_LDFLAGS="${SCION_LDFLAGS} -X ${SCION_PKG}.Commit=${GIT_COMMIT}"; \
        SCIONTOOL_LDFLAGS="${SCIONTOOL_LDFLAGS} -X ${SCIONTOOL_PKG}.Commit=${GIT_COMMIT}"; \
    fi && \
    go build -buildvcs=false -tags no_embed_web -ldflags="${SCION_LDFLAGS}" -o /usr/local/bin/scion ./cmd/scion/ && \
    go build -buildvcs=false -tags no_embed_web -ldflags="${SCIONTOOL_LDFLAGS}" -o /usr/local/bin/sciontool ./cmd/sciontool/

# Final stage
FROM base
USER root

# Copy scion and sciontool from builder
COPY --from=builder /usr/local/bin/scion /usr/local/bin/scion
COPY --from=builder /usr/local/bin/sciontool /usr/local/bin/sciontool

# alteredCarbon CLI + MCP server. Pre-built for the target platform
# by image-build/scripts/build-ac.sh; that script drops binaries
# named ac-<os>-<arch> under image-build/scion-base/_ac-bin/. The
# dir always exists (it's checked into the build context with a
# .gitkeep sentinel) so this COPY succeeds whether or not binaries
# were built. The downstream RUN picks the matching arch and
# installs it; absence of a matching binary is silent — the agent
# image just won't have `ac`, which is the correct behavior for
# the opt-in AC integration.
ARG TARGETOS
ARG TARGETARCH
COPY image-build/scion-base/_ac-bin /tmp/ac-bin
RUN if [ -f "/tmp/ac-bin/ac-${TARGETOS}-${TARGETARCH}" ]; then \
      install -m 0755 "/tmp/ac-bin/ac-${TARGETOS}-${TARGETARCH}" /usr/local/bin/ac; \
    fi && \
    rm -rf /tmp/ac-bin

# Create dev-override bin directory at highest PATH priority.
# When SCION_DEV_BINARIES is set, this directory is bind-mounted with
# locally-built binaries, allowing rapid iteration without image rebuilds.
RUN mkdir -p /opt/scion/bin
ENV PATH="/opt/scion/bin:${PATH}"

# Create scion user with passwordless sudo
RUN useradd -m -s /bin/zsh -u 1000 scion && \
    echo "scion ALL=(ALL) NOPASSWD:ALL" > /etc/sudoers.d/scion && \
    chmod 0440 /etc/sudoers.d/scion

# Precreate shared Scion home state so mounted file secrets and runtime
# config writes do not force root-owned parent directories at startup.
RUN mkdir -p /home/scion/.scion && \
    chown -R scion:scion /home/scion

# Set ownership for npm global directory
# TODO: Consider making npm-global world read-write (chmod) instead of chown
# to scion. This would decouple it from the scion UID and avoid any ownership
# concerns when the UID is remapped at container start.
RUN chown -R scion:scion /usr/local/share/npm-global

# Note: Container starts as root to allow sciontool init to adjust
# the scion user's UID/GID to match the host user for proper file
# permissions on mounted volumes. The child process drops privileges.

# Set sciontool as the container entrypoint (PID 1)
# The -- separator ensures everything after it is treated as the child command
ENTRYPOINT ["sciontool", "init", "--"]

# Default command (can be overridden at runtime)
CMD ["bash"]
