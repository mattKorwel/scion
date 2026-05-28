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

# Cloudcode harness image.
#
# Cloudcode is distributed internally as a static prebuilt binary
# (no public npm/apt package), so we bake the linux/amd64 binary into
# the image from image-build/cloudcode/_bin/cloudcode. That file must
# be present at build time — populated by hand from
# ~/dev/bin/cloudcode-linux-amd64 or via fleet-apply's
# `cloudcode_linux` distribution path on operator workstations.
#
# Build context: image-build/cloudcode/ (passed by build-images.sh
# via step_context_dir).

ARG BASE_IMAGE
FROM ${BASE_IMAGE}

ARG TZ
ENV TZ="$TZ"

ARG USERNAME=scion

USER root

# Persist bash history across container restarts, matching the
# convention used by the claude/gemini/codex images.
RUN SNIPPET="export PROMPT_COMMAND='history -a' && export HISTFILE=/commandhistory/.bash_history" \
  && mkdir /commandhistory \
  && touch /commandhistory/.bash_history \
  && chown -R $USERNAME /commandhistory

# Mark this as a devcontainer-like environment for orientation.
ENV DEVCONTAINER=true

# Cloudcode stores its config + caches under ~/.config/cloudcode and
# ~/.local/share/cloudcode. Pre-create with the right ownership so
# the first run doesn't bail on permission errors.
RUN mkdir -p /home/${USERNAME}/.config/cloudcode \
             /home/${USERNAME}/.local/share/cloudcode \
  && chown -R ${USERNAME}:${USERNAME} /home/${USERNAME}/.config/cloudcode \
                                      /home/${USERNAME}/.local/share/cloudcode

# Install the cloudcode binary from the build context. Two-stage move
# (COPY then install with mode) keeps the layer cacheable and avoids
# baking COPY-time owner of root into the final binary.
COPY _bin/cloudcode /tmp/cloudcode
RUN install -m 0755 /tmp/cloudcode /usr/local/bin/cloudcode \
  && rm -f /tmp/cloudcode \
  && /usr/local/bin/cloudcode --version 2>/dev/null || true

# Default to running as the scion user. Harness command construction
# lives in the harness-config (~/.scion/harness-configs/cloudcode/);
# this image doesn't set ENTRYPOINT / CMD so the runtime can pass its
# own `sh -c tmux new-session ... cloudcode run ...` line.
USER ${USERNAME}
WORKDIR /workspace
