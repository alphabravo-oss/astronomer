#!/usr/bin/env bash
# Validate a real CIS scan path through Astronomer:
#   ensure cis-operator -> trigger scan via Astronomer -> wait for completion ->
#   verify the generated ClusterScanReport is ingested -> verify findings export
#
# Usage:
#   ASTRO_USERNAME=admin ASTRO_PASSWORD=... ./scripts/validate-live-cis.sh
#   AUTH_TOKEN=... ./scripts/validate-live-cis.sh
#
# Optional env:
#   BASE_URL        default: http://astronomer.localtest.me:8080
#   REMOTE_CONTEXT  default: k3d-astronomer-remote
#   CLUSTER_ID      default: first active non-local cluster
#   EVENT_TIMEOUT   default: 180

set -euo pipefail

BASE_URL="${BASE_URL:-http://astronomer.localtest.me:8080}"
API_BASE="${BASE_URL%/}/api/v1"
REMOTE_CONTEXT="${REMOTE_CONTEXT:-k3d-astronomer-remote}"
CLUSTER_ID="${CLUSTER_ID:-}"
EVENT_TIMEOUT="${EVENT_TIMEOUT:-180}"
AUTH_TOKEN="${AUTH_TOKEN:-}"
ASTRO_USERNAME="${ASTRO_USERNAME:-}"
ASTRO_PASSWORD="${ASTRO_PASSWORD:-}"

require() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "missing required tool: $1" >&2
    exit 1
  }
}

require curl
require jq
require kubectl
require helm

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

echo "Using cluster_id=${CLUSTER_ID} remote_context=${REMOTE_CONTEXT}"

helm repo add rancher-charts https://charts.rancher.io >/dev/null 2>&1 || true
helm repo update >/dev/null 2>&1
helm upgrade --install cis-operator-crd rancher-charts/rancher-cis-benchmark-crd \
  --kube-context "${REMOTE_CONTEXT}" \
  --namespace cis-operator-system \
  --create-namespace >/dev/null
helm upgrade --install cis-operator rancher-charts/rancher-cis-benchmark \
  --kube-context "${REMOTE_CONTEXT}" \
  --namespace cis-operator-system \
  --create-namespace >/dev/null
kubectl --context "${REMOTE_CONTEXT}" -n cis-operator-system rollout status deploy/cis-operator --timeout=180s >/dev/null

profiles_json="$(kubectl --context "${REMOTE_CONTEXT}" get clusterscanprofiles.cis.cattle.io -o json)"
echo "${profiles_json}" | jq -e '.items | length > 0' >/dev/null

scan_response="$(
  curl -fsS \
    -H "Authorization: Bearer ${AUTH_TOKEN}" \
    -H 'Content-Type: application/json' \
    -X POST \
    "${API_BASE}/security/scans/" \
    -d "{\"cluster_id\":\"${CLUSTER_ID}\"}"
)"
scan_id="$(echo "${scan_response}" | jq -r '.data.id')"
scan_type="$(echo "${scan_response}" | jq -r '.data.scan_type')"
cluster_scan_name="$(echo "${scan_response}" | jq -r '.data.cluster_scan_name')"
echo "created scan_id=${scan_id} cluster_scan_name=${cluster_scan_name} scan_type=${scan_type}"

if [[ "${scan_type}" == "k3s-cis-1.8-profile-permissive" ]]; then
  echo "scan selected stale k3s 1.8 profile" >&2
  exit 1
fi

for i in $(seq 1 "${EVENT_TIMEOUT}"); do
  row="$(
    curl -fsS -H "Authorization: Bearer ${AUTH_TOKEN}" "${API_BASE}/security/scans/" |
      jq -c --arg id "${scan_id}" --arg name "${cluster_scan_name}" '.data // .items // .results // . | map(select(.id == $id or .cluster_scan_name == $name)) | .[0]'
  )"
  status="$(echo "${row}" | jq -r '.status // ""')"
  echo "scan-poll=${i} status=[${status}]"
  if [[ "${status}" == "completed" ]]; then
    break
  fi
  if [[ "${status}" == "failed" || "${status}" == "error" ]]; then
    echo "scan failed: ${row}" >&2
    exit 1
  fi
  sleep 1
done

row="$(
  curl -fsS -H "Authorization: Bearer ${AUTH_TOKEN}" "${API_BASE}/security/scans/" |
    jq -c --arg id "${scan_id}" --arg name "${cluster_scan_name}" '.data // .items // .results // . | map(select(.id == $id or .cluster_scan_name == $name)) | .[0]'
)"
status="$(echo "${row}" | jq -r '.status // ""')"
if [[ "${status}" != "completed" ]]; then
  echo "scan did not complete in time: ${row}" >&2
  exit 1
fi

report_name="$(
  kubectl --context "${REMOTE_CONTEXT}" get clusterscanreports.cis.cattle.io -o json |
    jq -r --arg owner "${cluster_scan_name}" '.items[] | select((.metadata.ownerReferences[0].name // "") == $owner) | .metadata.name' |
    head -n1
)"
if [[ -z "${report_name}" ]]; then
  echo "no ClusterScanReport found for ${cluster_scan_name}" >&2
  exit 1
fi

echo "resolved report_name=${report_name}"
kubectl --context "${REMOTE_CONTEXT}" get clusterscanreports.cis.cattle.io "${report_name}" >/dev/null

full_scan="$(
  curl -fsS -H "Authorization: Bearer ${AUTH_TOKEN}" "${API_BASE}/security/scans/${scan_id}/"
)"
findings_count="$(echo "${full_scan}" | jq -r '.data.findings | length')"
passed="$(echo "${row}" | jq -r '.passed // 0')"
failed="$(echo "${row}" | jq -r '.failed // 0')"
echo "completed counts: passed=${passed} failed=${failed} findings=${findings_count}"

if [[ "${findings_count}" == "0" ]]; then
  echo "expected findings in full scan response" >&2
  exit 1
fi

csv_preview="$(
  curl -fsS -H "Authorization: Bearer ${AUTH_TOKEN}" "${API_BASE}/security/scans/${scan_id}/report.csv" | sed -n '1,3p'
)"
echo "csv preview:"
echo "${csv_preview}"
echo "${csv_preview}" | grep -q 'test_id,severity,status,description,remediation'

echo "live CIS validation succeeded"
