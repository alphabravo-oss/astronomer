#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BASELINE="${1:-${OPENAPI_BASELINE:-HEAD:docs/openapi.yaml}}"
REVISION="${2:-docs/openapi.yaml}"
ARTIFACT_DIR="${VERIFY_ARTIFACT_DIR:-${TMPDIR:-/tmp}/astronomer-verify-enterprise}"
OASDIFF_VERSION="v1.33.0"
REVIEW_FILE="$ARTIFACT_DIR/openapi-effective-review.md"

mkdir -p "$ARTIFACT_DIR"
cd "$ROOT_DIR"

go run "github.com/oasdiff/oasdiff@${OASDIFF_VERSION}" changelog \
  "$BASELINE" "$REVISION" \
  --format markdown --allow-external-refs=false \
  >"$ARTIFACT_DIR/openapi-changelog.md"

# Retain the unignored machine report. Only exact, source-bound, unexpired
# current reviews may join the unchanged historical exception bytes.
go run "github.com/oasdiff/oasdiff@${OASDIFF_VERSION}" breaking \
  "$BASELINE" "$REVISION" --format json --allow-external-refs=false \
  >"$ARTIFACT_DIR/openapi-breaking-raw.json"
python3 scripts/openapi-current-reviews.py --baseline "$BASELINE" --revision "$REVISION" \
  --report "$ARTIFACT_DIR/openapi-breaking-raw.json" --output "$REVIEW_FILE"

go run "github.com/oasdiff/oasdiff@${OASDIFF_VERSION}" breaking \
  "$BASELINE" "$REVISION" \
  --format markdown --fail-on WARN --allow-external-refs=false \
  --err-ignore "$REVIEW_FILE" --warn-ignore "$REVIEW_FILE" \
  >"$ARTIFACT_DIR/openapi-breaking.md"

printf 'OpenAPI compatibility gate passed against %s; reports: %s\n' "$BASELINE" "$ARTIFACT_DIR"
