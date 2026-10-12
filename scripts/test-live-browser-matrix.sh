#!/usr/bin/env bash
# Destructive journeys need a fresh owned stack for each browser engine.
set -Eeuo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
artifact="${LIVE_BROWSER_ARTIFACT_DIR:-${TMPDIR:-/tmp}/astronomer-live-browser-matrix-$$-$(date +%s)}"
for engine in chromium firefox webkit; do
  LIVE_BROWSER_ENGINE="$engine" LIVE_BROWSER_ARTIFACT_DIR="$artifact/$engine" \
    "$root/scripts/test-live-browser.sh"
done
