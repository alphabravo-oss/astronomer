#!/usr/bin/env bash
# FEATURES-051126 T23 — emit a sorted, deduplicated list of every
# container image the astronomer chart will pull at install time.
#
# Used by `make images.txt` to regenerate deploy/chart/images.txt. Release CI
# resolves this inventory to exact digests; air-gapped operators consume the
# signed release manifest through scripts/mirror-release.py.
#
# Usage:
#   ./scripts/extract-images.sh > deploy/chart/images.txt
#
# Output format: one image per line, sorted, comments stripped.
# Example line: postgres:16-alpine
#
# The script renders the chart twice:
#   1) default values (dev / first-touch)
#   2) production-like optional components (Dex, management backup with a
#      dummy S3 target + key wrap, management logging) so air-gapped prod
#      installs don't miss dex / pgdump-s3 / fluent-bit.
# Results are unioned. The agent image and digest-pinned downstream controller
# images are added explicitly because they run only in managed clusters and do
# not appear in a management-plane Deployment.

set -euo pipefail

CHART_DIR="${CHART_DIR:-deploy/chart}"
COMPATIBILITY_FILE="${COMPATIBILITY_FILE:-deploy/release/compatibility.yaml}"
BUNDLE_CATALOG="${BUNDLE_CATALOG:-deploy/bundles/catalog.json}"

if ! command -v helm >/dev/null 2>&1; then
    echo "extract-images: helm not on PATH" >&2
    exit 2
fi
if [[ ! -d "$CHART_DIR" ]]; then
    echo "extract-images: chart directory not found: $CHART_DIR" >&2
    exit 2
fi
if ! command -v jq >/dev/null 2>&1; then
    echo "extract-images: jq not on PATH" >&2
    exit 2
fi
if [[ ! -f "$COMPATIBILITY_FILE" ]]; then
    echo "extract-images: compatibility file not found: $COMPATIBILITY_FILE" >&2
    exit 2
fi
if [[ ! -f "$BUNDLE_CATALOG" ]]; then
    echo "extract-images: built-in bundle catalog not found: $BUNDLE_CATALOG" >&2
    exit 2
fi

# Pull every `image:` reference out of a helm template render. The chart ships
# no key material (secrets.secretKey / secrets.encryptionKey are empty and the
# render fails without them), so every call passes throwaway values — nothing
# here ever reaches a cluster, we only want the image refs.
extract_images() {
    # Pin kube-version so CI (no cluster) stays inside Chart.yaml kubeVersion.
    # Do not swallow helm errors: an empty render used to look like a valid,
    # just-shorter inventory.
    # shellcheck disable=SC2068
    helm template astronomer "$CHART_DIR" \
        --kube-version 1.35.0 \
        --set secrets.secretKey=extract-images-render-only \
        --set secrets.encryptionKey=I2oWSIt6LO68xR6lxhqBpQxhesPuii5R6ubog-Id-yo= \
        $@ \
        | grep -oE 'image: "?[^"]+"?' \
        | sed -E 's/^image: //; s/^"//; s/"$//'
}

# Default (dev) render — covers server/worker/migrate/frontend/postgres/
# redis/shell/busybox and anything else on by default.
dev_images="$(extract_images -f "$CHART_DIR/values.yaml")"

# Production-like optional components. These stay off in values.yaml so a
# laptop install doesn't pull them, but values-production.yaml (or an
# operator --set) turns them on. Use dummy S3/key-wrap/logging values so
# production preflight doesn't refuse the render; we only care about the
# image refs that appear when the CronJobs / DaemonSet / Dex Deployment
# are active.
prod_like_images="$(
    extract_images \
        -f "$CHART_DIR/values.yaml" \
        --set dex.enabled=true \
        --set managementBackup.enabled=true \
        --set managementBackup.s3.bucket=airgap-extract-dummy \
        --set managementBackup.s3.credentialsSecretRef.name=airgap-extract-dummy \
        --set managementBackup.encryptionKeyBackup.wrappingSecretRef.name=airgap-extract-dummy \
        --set managementRestoreDrill.enabled=true \
        --set managementRestoreDrill.decryptCheck.wrappingSecretRef.name=airgap-extract-dummy \
        --set managementLogging.enabled=true \
        --set managementLogging.endpoint=http://loki.observability.svc:3100
)"

images="$(printf '%s\n%s' "$dev_images" "$prod_like_images" | sed '/^$/d' | LC_ALL=C sort -u)"

# The agent image isn't in any Deployment — it's referenced when the
# server renders the install.yaml that operators apply in a new member
# cluster. Surface it explicitly so operators don't miss it. Grab the
# 6 lines after `  agent:` and parse registry + repository + tag out of them.
agent_block="$(grep -A6 '^  agent:' "$CHART_DIR/values.yaml")"
agent_reg="$(printf '%s' "$agent_block" | awk -F': ' '/^    registry:/ {gsub(/"/,"",$2); print $2; exit}')"
agent_repo="$(printf '%s' "$agent_block" | awk -F': ' '/^    repository:/ {gsub(/"/,"",$2); print $2; exit}')"
agent_tag="$(printf '%s' "$agent_block" | awk -F': ' '/^    tag:/ {gsub(/"/,"",$2); print $2; exit}')"
if [[ -n "$agent_repo" && -n "$agent_tag" ]]; then
    # Prepend the registry so the air-gap mirror list carries the full ref,
    # matching what the configmap renders into adopted-cluster manifests.
    agent_ref="$agent_repo:$agent_tag"
    [[ -n "$agent_reg" ]] && agent_ref="$agent_reg/$agent_ref"
    images="$(printf '%s\n%s' "$images" "$agent_ref" | LC_ALL=C sort -u)"
fi

# Flux runs downstream only, so Helm cannot discover these images. The release
# compatibility contract is authoritative and requires every component digest.
downstream_images="$(jq -er '
  .flux.components[] |
  select(.digest_pin_required == true) |
  if (.repository | test("^ghcr\\.io/fluxcd/[a-z0-9-]+$")) and
     (.digest | test("^sha256:[a-f0-9]{64}$"))
  then .repository + "@" + .digest
  else error("invalid downstream controller image contract")
  end
' "$COMPATIBILITY_FILE")"
[[ "$(printf '%s\n' "$downstream_images" | sed '/^$/d' | wc -l | tr -d ' ')" == 3 ]] || {
    echo "extract-images: expected exactly three downstream controller images" >&2
    exit 2
}
images="$(printf '%s\n%s' "$images" "$downstream_images" | sed '/^$/d' | LC_ALL=C sort -u)"

# Built-in bundles are normal delivery artifacts and their workload images must
# be mirrored even though they are absent from the management chart render.
builtin_images="$(jq -er '
  .components[].images[] |
  if test("^[a-z0-9][a-z0-9.:-]*/[a-z0-9][a-z0-9._/-]*@sha256:[a-f0-9]{64}$")
  then .
  else error("invalid built-in bundle image identity")
  end
' "$BUNDLE_CATALOG")"
images="$(printf '%s\n%s' "$images" "$builtin_images" | sed '/^$/d' | LC_ALL=C sort -u)"

# Emit a stable, comment-prefixed header so the file is self-describing.
cat <<EOF
# Astronomer Helm chart image list (T23 air-gapped install)
#
# Regenerated via: make images.txt
# Source:          $CHART_DIR/values.yaml + helm template, plus the
#                  digest-pinned managed-cluster controllers declared in
#                  $COMPATIBILITY_FILE and built-ins declared in
#                  $BUNDLE_CATALOG
#
# Air-gapped install procedure: verify the tagged release-manifest signature,
# then use scripts/mirror-release.py to resolve the complete immutable copy
# plan, verify destination digests, sign the mapping, and emit Helm overrides.
#
# See docs/airgapped-install.md for the full operator procedure.
EOF
printf '%s\n' "$images"
