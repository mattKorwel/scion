# corprun helpers

Scripts for deploying the scion hub + AC images to a `go/corp-run`
managed Cloud Run service. Sibling to `image-build/scripts/`'s other
helpers; placed here because they're corp-run-specific (will not
work outside Google's internal corp-run platform).

## Files

| File | Purpose |
|---|---|
| `deploy.sh` | Deploy `scion-hub` + `scion-ac` Cloud Run services. Idempotent. |
| `grant-ar-pull.sh` | Grant the corp-run runtime SA pull access on a cross-project Artifact Registry. |

## Quick Start

```sh
# 1. (One time) Run corprun init to provision the corp-run service:
ssh <cloudtop>
gcert
/google/bin/releases/corprun-cli/corprun init \
  --system_name=scion \
  --team_group=experimental-scion-team \
  --region=us-central1 \
  --buganizer_component=2084314

# 2. (One time) Grant the corp-run runtime SA AR pull access on the
#    image-source project (until images move to the corprun project's AR):
CORPRUN_PROJECT=<scion-corprun-project-from-step-1> \
  AR_PROJECT=mjk-agent-memory-service \
  AR_REPO=scion \
  AR_LOCATION=us-west1 \
  image-build/scripts/corprun/grant-ar-pull.sh

# 3. Deploy hub + AC:
CORPRUN_PROJECT=<scion-corprun-project> \
  AC_VAULT_REMOTE_URL=https://source.developers.google.com/p/<svc-project>/r/scion-vault \
  image-build/scripts/corprun/deploy.sh

# 4. Verify:
gcloud run services describe scion-hub \
  --project=$CORPRUN_PROJECT --region=us-central1 \
  --format='value(status.url)'
# Then from any corp device:
curl https://scion-hub-XXXXX-uc.a.run.app/healthz
```

## Image rebuild

The two corp-run images (`scion-hub-corprun`, `scion-ac`) are built
via the standard scion image build pipeline — they're full citizens
of `image-build/scripts/lib/targets.sh`:

```sh
# Rebuild the corp-run pair specifically:
image-build/scripts/build-images.sh \
  --target corprun --push \
  --registry us-west1-docker.pkg.dev/<project>/<repo>

# Or via Cloud Build:
GCLOUD_PROJECT=<project> \
  image-build/scripts/build-images.sh \
  --builder cloud-build --target corprun \
  --registry us-west1-docker.pkg.dev/<project>/<repo>
```

After a rebuild, re-run `deploy.sh` to roll the new image.
