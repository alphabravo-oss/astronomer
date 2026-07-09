#!/usr/bin/env bash
# FEATURES-051126 T23 — emit a sorted, deduplicated list of every
# container image the astronomer chart will pull at install time.
#
# Used by `make images.txt` to regenerate deploy/chart/images.txt,
# which air-gapped operators feed to `skopeo copy` (or equivalent) to
# mirror every image into an internal registry before installing.
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
# Results are unioned. The agent image is added explicitly because it only
# appears in the member-cluster install template the server hands out at
# registration time — never in a management-plane Deployment.

set -euo pipefail

CHART_DIR="${CHART_DIR:-deploy/chart}"

if ! command -v helm >/dev/null 2>&1; then
    echo "extract-images: helm not on PATH" >&2
    exit 2
fi
if [[ ! -d "$CHART_DIR" ]]; then
    echo "extract-images: chart directory not found: $CHART_DIR" >&2
    exit 2
fi

# Pull every `image:` reference out of a helm template render.
extract_images() {
    # shellcheck disable=SC2068
    helm template astronomer "$CHART_DIR" $@ 2>/dev/null \
        | grep -oE 'image: "?[^"]+"?' \
        | sed -E 's/^image: //; s/^"//; s/"$//'
}

# Default (dev) render — covers server/worker/migrate/frontend/postgres/
# redis/shell/busybox/argocd and anything else on by default.
dev_images="$(extract_images -f "$CHART_DIR/values.yaml" || true)"

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
        --set managementLogging.endpoint=http://loki.observability.svc:3100 \
        || true
)"

images="$(printf '%s\n%s' "$dev_images" "$prod_like_images" | sed '/^$/d' | sort -u)"

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
    images="$(printf '%s\n%s' "$images" "$agent_ref" | sort -u)"
fi

# Emit a stable, comment-prefixed header so the file is self-describing
# but `grep -v '^#'` cleans it up for `xargs skopeo copy`.
cat <<EOF
# Astronomer Helm chart image list (T23 air-gapped install)
#
# Regenerated via: make images.txt
# Source:          $CHART_DIR/values.yaml + helm template
#                  (default render ∪ production-like optional components:
#                   dex, managementBackup/pgdump-s3, managementLogging/fluent-bit)
#
# Air-gapped install procedure:
#   1) grep -v '^#' deploy/chart/images.txt > /tmp/images.txt
#   2) Mirror each image to your internal registry with skopeo:
#        while read -r img; do
#          skopeo copy --all "docker://\$img" "docker://internal.example.com/astronomer/\$img"
#        done < /tmp/images.txt
#   3) helm install --set image.registry=internal.example.com/astronomer ...
#
# See docs/airgapped-install.md for the full operator procedure.
EOF
printf '%s\n' "$images"
