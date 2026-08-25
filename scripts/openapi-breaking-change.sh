#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BASELINE="${1:-${OPENAPI_BASELINE:-HEAD:docs/openapi.yaml}}"
REVISION="${2:-docs/openapi.yaml}"
ARTIFACT_DIR="${VERIFY_ARTIFACT_DIR:-${TMPDIR:-/tmp}/astronomer-verify-enterprise}"
OASDIFF_VERSION="v1.28.0"
REVIEW_FILE="docs/api-breaking-change-review.md"

mkdir -p "$ARTIFACT_DIR"
cd "$ROOT_DIR"

go run "github.com/oasdiff/oasdiff@${OASDIFF_VERSION}" changelog \
  "$BASELINE" "$REVISION" \
  --format markdown --allow-external-refs=false \
  >"$ARTIFACT_DIR/openapi-changelog.md"

go run "github.com/oasdiff/oasdiff@${OASDIFF_VERSION}" breaking \
  "$BASELINE" "$REVISION" \
  --format markdown --fail-on WARN --allow-external-refs=false \
  --err-ignore "$REVIEW_FILE" --warn-ignore "$REVIEW_FILE" \
  >"$ARTIFACT_DIR/openapi-breaking.md"

printf 'OpenAPI compatibility gate passed against %s; reports: %s\n' "$BASELINE" "$ARTIFACT_DIR"
