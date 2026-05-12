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
# The script renders the chart with the agent included so the agent
# image (used by member-cluster install) is captured too — it doesn't
# appear in the management-plane Deployments, only in the install
# template the server hands out at registration time.

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

# Render with defaults — production-mode renders depend on operator
# secrets so they'd 502 here; the image set is identical between modes
# because no production-only image is conditionally rendered.
images="$(
    helm template astronomer "$CHART_DIR" -f "$CHART_DIR/values.yaml" 2>/dev/null \
    | grep -oE 'image: "?[^"]+"?' \
    | sed -E 's/^image: //; s/^"//; s/"$//' \
    | sort -u
)"

# The agent image isn't in any Deployment — it's referenced when the
# server renders the install.yaml that operators apply in a new member
# cluster. Surface it explicitly so operators don't miss it. Grab the
# 5 lines after `  agent:` and parse repository + tag out of them.
agent_block="$(grep -A5 '^  agent:' "$CHART_DIR/values.yaml")"
agent_repo="$(printf '%s' "$agent_block" | awk -F': ' '/^    repository:/ {gsub(/"/,"",$2); print $2; exit}')"
agent_tag="$(printf '%s' "$agent_block" | awk -F': ' '/^    tag:/ {gsub(/"/,"",$2); print $2; exit}')"
if [[ -n "$agent_repo" && -n "$agent_tag" ]]; then
    images="$(printf '%s\n%s:%s' "$images" "$agent_repo" "$agent_tag" | sort -u)"
fi

# Emit a stable, comment-prefixed header so the file is self-describing
# but `grep -v '^#'` cleans it up for `xargs skopeo copy`.
cat <<EOF
# Astronomer Helm chart image list (T23 air-gapped install)
#
# Regenerated via: make images.txt
# Source:          $CHART_DIR/values.yaml + helm template
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
