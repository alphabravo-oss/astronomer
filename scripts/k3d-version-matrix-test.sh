#!/usr/bin/env bash
# Qualify owned fresh candidate stacks; historical release receipts stay unchanged.
set -Eeuo pipefail
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
if [[ "${1:-}" == --validate-only && $# == 1 ]]; then
  echo 'k3d-version-matrix: declared owned lanes1.36/1.37; no external endpoint'
  exit 0
fi
if [[ -n "${ASTRO_URL:-}" || -n "${ASTRO_PASSWORD:-}" || -n "${ASTRO_EMAIL:-}" || -n "${AGENT_IMAGE:-}" || -n "${K3S_IMAGE:-}" ]]; then
  echo 'k3d-version-matrix: external endpoints, credentials and image overrides are prohibited' >&2
  exit 2
fi
minors=("$@")
if [[ ${#minors[@]} == 0 ]]; then minors=(1.36 1.37); fi
for minor in "${minors[@]}"; do
  case "$minor" in
    1.36|1.37) ;;
    *) echo 'k3d-version-matrix: select declared minor1.36 or1.37; older candidate lanes are not qualified' >&2; exit 2 ;;
  esac
done
if [[ "${minors[*]}" == *'1.36 1.36'* || "${minors[*]}" == *'1.37 1.37'* || ${#minors[@]} -gt 2 ]]; then
  echo 'k3d-version-matrix: duplicate qualification lanes are not allowed' >&2
  exit 2
fi
if [[ -n "${LIVE_BROWSER_ARTIFACT_DIR:-}" ]]; then
  artifact="$LIVE_BROWSER_ARTIFACT_DIR"
  mkdir -m 700 -- "$artifact"
else
  artifact="$(mktemp -d "${TMPDIR:-/tmp}/astronomer-k3d-matrix.XXXXXXXX")"
fi
for minor in "${minors[@]}"; do
  KUBERNETES_QUALIFICATION_MINOR="$minor" LIVE_BROWSER_ARTIFACT_DIR="$artifact/$minor" \
    "$root/scripts/test-live-browser.sh"
done
