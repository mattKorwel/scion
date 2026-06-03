#!/usr/bin/env bash
# grant-ar-pull.sh — grant a corp-run service's runtime SA the
# `roles/artifactregistry.reader` role on the AR repo holding the
# scion-hub-corprun + scion-ac images, when the AR is in a different
# GCP project than the corp-run service.
#
# Usage:
#   CORPRUN_PROJECT=<svc-project> AR_PROJECT=<ar-project> AR_REPO=<repo> \
#     [AR_LOCATION=us-west1] grant-ar-pull.sh
#
# What it does:
#   1. Resolves the corp-run project's compute SA / Cloud Run runtime SA.
#   2. Grants it artifactregistry.reader on the target AR.
# Idempotent.

set -euo pipefail

CORPRUN_PROJECT="${CORPRUN_PROJECT:?CORPRUN_PROJECT is required}"
AR_PROJECT="${AR_PROJECT:?AR_PROJECT is required}"
AR_REPO="${AR_REPO:?AR_REPO is required}"
AR_LOCATION="${AR_LOCATION:-us-west1}"

c_dim()   { printf '\033[2m%s\033[0m' "$*"; }
c_green() { printf '\033[32m%s\033[0m' "$*"; }
log()     { printf '%s %s\n' "$(c_dim '==>')" "$*"; }
ok()      { printf '%s %s\n' "$(c_green '  ✓')" "$*"; }

# Resolve the project number for the corp-run project (compute SA
# uses the project number).
project_number="$(gcloud projects describe "${CORPRUN_PROJECT}" \
  --format='value(projectNumber)')"
runtime_sa="${project_number}-compute@developer.gserviceaccount.com"

log "Corp-run project: ${CORPRUN_PROJECT} (#${project_number})"
log "Runtime SA:       ${runtime_sa}"
log "AR target:        ${AR_LOCATION}-docker.pkg.dev/${AR_PROJECT}/${AR_REPO}"

log "Granting roles/artifactregistry.reader..."
gcloud artifacts repositories add-iam-policy-binding "${AR_REPO}" \
  --project="${AR_PROJECT}" \
  --location="${AR_LOCATION}" \
  --member="serviceAccount:${runtime_sa}" \
  --role="roles/artifactregistry.reader" \
  --condition=None >/dev/null

ok "Done. ${runtime_sa} can now pull from ${AR_LOCATION}-docker.pkg.dev/${AR_PROJECT}/${AR_REPO}"
