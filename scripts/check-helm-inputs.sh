#!/usr/bin/env bash
set -Eeuo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"

if rg -n '^dependencies:[[:space:]]*$' deploy/chart/Chart.yaml >/dev/null; then
  [[ -f deploy/chart/Chart.lock ]] || {
    printf 'FAIL: chart dependencies require a committed Chart.lock\n' >&2
    exit 1
  }
  helm dependency build deploy/chart
  git diff --exit-code -- deploy/chart/Chart.lock
else
  [[ ! -e deploy/chart/Chart.lock ]] || {
    printf 'FAIL: dependency-free chart must not retain a stale Chart.lock\n' >&2
    exit 1
  }
fi

helm show chart deploy/chart >/dev/null
printf 'Helm chart input and dependency-lock contract passed\n'
