#!/usr/bin/env bash
# Validate the live B6 watch path end to end:
#   remote-cluster Pod create -> agent informer -> tunnel -> SSE
#
# Usage:
#   ASTRO_USERNAME=admin ASTRO_PASSWORD=... ./scripts/validate-live-b6.sh
#   AUTH_TOKEN=... ./scripts/validate-live-b6.sh
#
# Optional env:
#   BASE_URL        default: http://astronomer.localtest.me:8080
#   REMOTE_CONTEXT  default: k3d-astronomer-remote
#   CLUSTER_ID      default: first active non-local cluster from /api/v1/clusters/
#   NAMESPACE       default: default
#   IMAGE           default: nginx:1.27
#   EVENT_TIMEOUT   default: 20

set -euo pipefail

BASE_URL="${BASE_URL:-http://astronomer.localtest.me:8080}"
API_BASE="${BASE_URL%/}/api/v1"
REMOTE_CONTEXT="${REMOTE_CONTEXT:-k3d-astronomer-remote}"
NAMESPACE="${NAMESPACE:-default}"
IMAGE="${IMAGE:-nginx:1.27}"
EVENT_TIMEOUT="${EVENT_TIMEOUT:-20}"
AUTH_TOKEN="${AUTH_TOKEN:-}"
ASTRO_USERNAME="${ASTRO_USERNAME:-}"
ASTRO_PASSWORD="${ASTRO_PASSWORD:-}"
CLUSTER_ID="${CLUSTER_ID:-}"
POD_NAME="b6-probe-$(date +%s)"
SSE_LOG="$(mktemp)"

cleanup() {
  kubectl --context "${REMOTE_CONTEXT}" -n "${NAMESPACE}" delete pod "${POD_NAME}" --wait=false >/dev/null 2>&1 || true
  rm -f "${SSE_LOG}"
}
trap cleanup EXIT

require() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "missing required tool: $1" >&2
    exit 1
  }
}

require curl
require jq
require kubectl
require timeout

  if [[ -z "${AUTH_TOKEN}" ]]; then
  if [[ -z "${ASTRO_USERNAME}" || -z "${ASTRO_PASSWORD}" ]]; then
    echo "set AUTH_TOKEN or ASTRO_USERNAME and ASTRO_PASSWORD" >&2
    exit 1
  fi
  AUTH_TOKEN="$(
    curl -fsS \
      -H 'Content-Type: application/json' \
      -X POST "${API_BASE}/auth/login/" \
      -d "{\"username\":\"${ASTRO_USERNAME}\",\"password\":\"${ASTRO_PASSWORD}\"}" |
      jq -r '.data.token'
  )"
fi

if [[ -z "${CLUSTER_ID}" ]]; then
  CLUSTER_ID="$(
    curl -fsS -H "Authorization: Bearer ${AUTH_TOKEN}" "${API_BASE}/clusters/" |
      jq -r '.data[] | select(.status == "active" and (.is_local | not)) | .id' |
      head -n1
  )"
fi

if [[ -z "${CLUSTER_ID}" ]]; then
  echo "no active non-local cluster found; set CLUSTER_ID explicitly" >&2
  exit 1
fi

echo "Using cluster_id=${CLUSTER_ID} context=${REMOTE_CONTEXT} pod=${POD_NAME}"

timeout "${EVENT_TIMEOUT}"s \
  curl -fsS -N "${API_BASE}/events/stream/?token=${AUTH_TOKEN}" > "${SSE_LOG}" &
SSE_PID=$!

sleep 2
kubectl --context "${REMOTE_CONTEXT}" -n "${NAMESPACE}" run "${POD_NAME}" --image="${IMAGE}" --restart=Never >/dev/null

found=0
for _ in $(seq 1 "${EVENT_TIMEOUT}"); do
  if grep -q "\"cluster_id\":\"${CLUSTER_ID}\"" "${SSE_LOG}" && grep -q "\"kind\":\"Pod\"" "${SSE_LOG}" && grep -q "\"name\":\"${POD_NAME}\"" "${SSE_LOG}"; then
    found=1
    break
  fi
  sleep 1
done

wait "${SSE_PID}" || true

if [[ "${found}" -ne 1 ]]; then
  echo "did not observe cluster.k8s_changed for pod ${POD_NAME}" >&2
  echo "--- SSE LOG ---" >&2
  sed -n '1,240p' "${SSE_LOG}" >&2
  exit 1
fi

echo "Observed live cluster.k8s_changed event:"
grep -n "\"name\":\"${POD_NAME}\"" "${SSE_LOG}" | head -n1
