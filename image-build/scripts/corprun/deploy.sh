#!/usr/bin/env bash
# deploy.sh — deploy scion-hub-corprun + scion-ac to a go/corp-run
# managed Cloud Run service. Idempotent; re-runs cleanly to update.
#
# Pre-reqs:
#   - `corprun init` (or equivalent) has provisioned a Cloud Run
#     service for `scion` in $CORPRUN_PROJECT.
#   - The build SA / your user has run.developer on the service.
#   - Images live at $IMAGE_REGISTRY/scion-hub-corprun:<tag> and
#     $IMAGE_REGISTRY/scion-ac:<tag> (built via
#     image-build/scripts/build-images.sh --target corprun --push).
#   - The corp-run project's runtime SA has read on the image AR
#     (see grant-cross-project-pull.sh sibling).
#
# Env:
#   CORPRUN_PROJECT      GCP project hosting the corp-run service.
#                        Required.
#   CORPRUN_REGION       Cloud Run region. Default: us-central1.
#   IMAGE_REGISTRY       AR repo holding the images.
#                        Default: us-west1-docker.pkg.dev/mjk-agent-memory-service/scion
#   IMAGE_TAG            Image tag to deploy. Default: latest.
#   HUB_SERVICE_NAME     Cloud Run service name for the hub.
#                        Default: scion-hub.
#   AC_SERVICE_NAME      Cloud Run service name for AC.
#                        Default: scion-ac.
#   AC_VAULT_REMOTE_URL  Required for AC to persist data. Without
#                        it, AC runs ephemeral.
#   SCION_ADMIN_EMAILS   Optional; passed to hub.

set -euo pipefail

CORPRUN_PROJECT="${CORPRUN_PROJECT:?CORPRUN_PROJECT is required}"
CORPRUN_REGION="${CORPRUN_REGION:-us-central1}"
IMAGE_REGISTRY="${IMAGE_REGISTRY:-us-west1-docker.pkg.dev/mjk-agent-memory-service/scion}"
IMAGE_TAG="${IMAGE_TAG:-latest}"
HUB_SERVICE_NAME="${HUB_SERVICE_NAME:-scion-hub}"
AC_SERVICE_NAME="${AC_SERVICE_NAME:-scion-ac}"

c_dim()   { printf '\033[2m%s\033[0m' "$*"; }
c_green() { printf '\033[32m%s\033[0m' "$*"; }
log()     { printf '%s %s\n' "$(c_dim '==>')" "$*"; }
ok()      { printf '%s %s\n' "$(c_green '  ✓')" "$*"; }

HUB_IMAGE="${IMAGE_REGISTRY}/scion-hub-corprun:${IMAGE_TAG}"
AC_IMAGE="${IMAGE_REGISTRY}/scion-ac:${IMAGE_TAG}"

log "Project:  ${CORPRUN_PROJECT}"
log "Region:   ${CORPRUN_REGION}"
log "Hub img:  ${HUB_IMAGE}"
log "AC  img:  ${AC_IMAGE}"
log "Hub svc:  ${HUB_SERVICE_NAME}"
log "AC  svc:  ${AC_SERVICE_NAME}"

# Hub deploy.
log "Deploying ${HUB_SERVICE_NAME}..."
hub_env_args=(
  "--set-env-vars=PORT=8080"
)
if [[ -n "${SCION_ADMIN_EMAILS:-}" ]]; then
  hub_env_args+=("--update-env-vars=SCION_ADMIN_EMAILS=${SCION_ADMIN_EMAILS}")
fi

gcloud run deploy "${HUB_SERVICE_NAME}" \
  --project="${CORPRUN_PROJECT}" \
  --region="${CORPRUN_REGION}" \
  --image="${HUB_IMAGE}" \
  --port=8080 \
  --no-allow-unauthenticated \
  --ingress=internal-and-cloud-load-balancing \
  --min-instances=1 \
  --max-instances=1 \
  --cpu=1 \
  --memory=512Mi \
  --timeout=3600 \
  "${hub_env_args[@]}"

ok "${HUB_SERVICE_NAME} deployed"

# AC deploy.
log "Deploying ${AC_SERVICE_NAME}..."
ac_env_args=(
  "--set-env-vars=PORT=8080"
)
if [[ -n "${AC_VAULT_REMOTE_URL:-}" ]]; then
  ac_env_args+=("--update-env-vars=AC_VAULT_REMOTE_URL=${AC_VAULT_REMOTE_URL}")
else
  log "WARN: AC_VAULT_REMOTE_URL unset — AC will run ephemeral. Smoke-test only."
fi
if [[ -n "${AC_PUSH_ASYNC:-}" ]]; then
  ac_env_args+=("--update-env-vars=AC_PUSH_ASYNC=${AC_PUSH_ASYNC}")
fi

gcloud run deploy "${AC_SERVICE_NAME}" \
  --project="${CORPRUN_PROJECT}" \
  --region="${CORPRUN_REGION}" \
  --image="${AC_IMAGE}" \
  --port=8080 \
  --no-allow-unauthenticated \
  --ingress=internal-and-cloud-load-balancing \
  --min-instances=1 \
  --max-instances=1 \
  --cpu=1 \
  --memory=512Mi \
  --timeout=3600 \
  "${ac_env_args[@]}"

ok "${AC_SERVICE_NAME} deployed"

# Summary.
log "Service URLs:"
gcloud run services describe "${HUB_SERVICE_NAME}" \
  --project="${CORPRUN_PROJECT}" --region="${CORPRUN_REGION}" \
  --format='value(status.url)' | xargs -I{} printf '  hub: %s\n' {}
gcloud run services describe "${AC_SERVICE_NAME}" \
  --project="${CORPRUN_PROJECT}" --region="${CORPRUN_REGION}" \
  --format='value(status.url)' | xargs -I{} printf '  ac:  %s\n' {}

ok "Deploy complete."
log "NOTE: --no-allow-unauthenticated + --ingress=internal-and-cloud-load-balancing"
log "      means traffic must come from inside Google's network and authn'd."
log "      ACLaim/UberProxy at the corp-run edge handles user-level access."
