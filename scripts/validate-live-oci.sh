#!/usr/bin/env bash
# Validate a real OCI Helm catalog path through Astronomer:
#   create OCI repo -> test connection -> sync -> install chart on remote cluster
#   -> verify live Helm release/workloads -> uninstall -> delete repo
#
# Usage:
#   ASTRO_USERNAME=admin ASTRO_PASSWORD=... ./scripts/validate-live-oci.sh
#   AUTH_TOKEN=... ./scripts/validate-live-oci.sh
#
# Optional env:
#   BASE_URL        default: http://astronomer.localtest.me:8080
#   REMOTE_CONTEXT  default: k3d-astronomer-remote
#   CLUSTER_ID      default: first active non-local cluster
#   EVENT_TIMEOUT   default: 180
#   OCI_REPO_URL    default: oci://ghcr.io/argoproj/argo-helm
#   OCI_CHART       default: argo-cd
#   OCI_VERSION     optional exact chart version; default: latest synced version

set -euo pipefail

BASE_URL="${BASE_URL:-http://astronomer.localtest.me:8080}"
API_BASE="${BASE_URL%/}/api/v1"
REMOTE_CONTEXT="${REMOTE_CONTEXT:-k3d-astronomer-remote}"
CLUSTER_ID="${CLUSTER_ID:-}"
EVENT_TIMEOUT="${EVENT_TIMEOUT:-180}"
AUTH_TOKEN="${AUTH_TOKEN:-}"
ASTRO_USERNAME="${ASTRO_USERNAME:-}"
ASTRO_PASSWORD="${ASTRO_PASSWORD:-}"
OCI_REPO_URL="${OCI_REPO_URL:-oci://ghcr.io/argoproj/argo-helm}"
OCI_CHART="${OCI_CHART:-argo-cd}"
OCI_VERSION="${OCI_VERSION:-}"

RUN_ID="$(date +%s)"
REPO_NAME="oci-live-${RUN_ID}"
RELEASE_NAME="${RELEASE_NAME:-oci-live-argocd}"
NAMESPACE="${NAMESPACE:-${RELEASE_NAME}}"
REPO_ID=""
CHART_ID=""
CHART_VERSION_ID=""
CHART_VERSION=""
INSTALLATION_ID=""

cleanup() {
  if [[ -n "${AUTH_TOKEN}" && -n "${INSTALLATION_ID}" ]]; then
    echo "cleanup: uninstalling ${INSTALLATION_ID}"
    delete_resp="$(
      curl -fsS \
        -H "Authorization: Bearer ${AUTH_TOKEN}" \
        -X DELETE \
        "${API_BASE}/catalog/installed/${INSTALLATION_ID}/" 2>/dev/null || true
    )"
    if [[ -n "${delete_resp}" ]]; then
      delete_op_id="$(echo "${delete_resp}" | jq -r '.data.id // empty')"
      if [[ -n "${delete_op_id}" ]]; then
        poll_operation "${delete_op_id}" >/dev/null 2>&1 || true
      fi
    fi
  fi
  if [[ -n "${AUTH_TOKEN}" && -n "${REPO_ID}" ]]; then
    echo "cleanup: deleting repo ${REPO_ID}"
    curl -fsS \
      -H "Authorization: Bearer ${AUTH_TOKEN}" \
      -X DELETE \
      "${API_BASE}/catalog/repositories/${REPO_ID}/" >/dev/null 2>&1 || true
  fi
  kubectl --context "${REMOTE_CONTEXT}" delete namespace "${NAMESPACE}" --ignore-not-found >/dev/null 2>&1 || true
}
trap cleanup EXIT

require() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "missing required tool: $1" >&2
    exit 1
  }
}

poll_operation() {
  local op_id="$1"
  local status=""
  for i in $(seq 1 "${EVENT_TIMEOUT}"); do
    resp="$(
      curl -fsS \
        -H "Authorization: Bearer ${AUTH_TOKEN}" \
        "${API_BASE}/catalog/operations/${op_id}/"
    )"
    status="$(echo "${resp}" | jq -r '.data.status')"
    echo "operation-poll=${i} op=${op_id} status=${status}"
    if [[ "${status}" == "completed" ]]; then
      echo "${resp}" | jq '{status: .data.status, events: .data.events}'
      return 0
    fi
    if [[ "${status}" == "failed" || "${status}" == "superseded" ]]; then
      echo "${resp}" | jq '{status: .data.status, error: .data.errorMessage, events: .data.events}' >&2
      return 1
    fi
    sleep 2
  done
  echo "timed out waiting for catalog operation ${op_id}" >&2
  return 1
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
echo "Creating OCI repo ${REPO_NAME} -> ${OCI_REPO_URL}"

create_repo_body="$(
  jq -n \
    --arg name "${REPO_NAME}" \
    --arg url "${OCI_REPO_URL}" \
    --arg chart "${OCI_CHART}" \
    '{name:$name,url:$url,repo_type:"oci",enabled:true,auth_config:{charts:[$chart]}}'
)"

REPO_ID="$(
  curl -fsS \
    -H "Authorization: Bearer ${AUTH_TOKEN}" \
    -H 'Content-Type: application/json' \
    -X POST \
    "${API_BASE}/catalog/repositories/" \
    -d "${create_repo_body}" |
    jq -r '.data.id'
)"

echo "Testing OCI repo reachability"
curl -fsS \
  -H "Authorization: Bearer ${AUTH_TOKEN}" \
  -X POST \
  "${API_BASE}/catalog/repositories/${REPO_ID}/test-connection/" |
  jq '.data'

echo "Syncing OCI repo"
curl -fsS \
  -H "Authorization: Bearer ${AUTH_TOKEN}" \
  -X POST \
  "${API_BASE}/catalog/repositories/${REPO_ID}/sync/" |
  jq '.data'

CHART_ID="$(
  curl -fsS \
    -H "Authorization: Bearer ${AUTH_TOKEN}" \
    "${API_BASE}/catalog/charts/?limit=500" |
    jq -r --arg repo_id "${REPO_ID}" --arg chart "${OCI_CHART}" '
      .data[]
      | select(.repository_id == $repo_id and .name == $chart)
      | .id
    ' |
    head -n1
)"
if [[ -z "${CHART_ID}" ]]; then
  echo "chart ${OCI_CHART} not found after OCI sync" >&2
  exit 1
fi

version_payload="$(
  curl -fsS \
    -H "Authorization: Bearer ${AUTH_TOKEN}" \
    "${API_BASE}/catalog/charts/${CHART_ID}/versions/"
)"
if [[ -n "${OCI_VERSION}" ]]; then
  CHART_VERSION_ID="$(echo "${version_payload}" | jq -r --arg version "${OCI_VERSION}" '.data[] | select(.version == $version) | .id' | head -n1)"
  CHART_VERSION="$(echo "${version_payload}" | jq -r --arg version "${OCI_VERSION}" '.data[] | select(.version == $version) | .version' | head -n1)"
fi
if [[ -z "${CHART_VERSION_ID}" ]]; then
  CHART_VERSION_ID="$(echo "${version_payload}" | jq -r '.data[0].id // empty')"
  CHART_VERSION="$(echo "${version_payload}" | jq -r '.data[0].version // empty')"
fi
if [[ -z "${CHART_VERSION_ID}" || -z "${CHART_VERSION}" ]]; then
  echo "no chart version found for ${OCI_CHART}" >&2
  exit 1
fi

echo "Installing chart ${OCI_CHART} version=${CHART_VERSION} release=${RELEASE_NAME}"
install_body="$(
  jq -n \
    --arg cluster_id "${CLUSTER_ID}" \
    --arg chart_version_id "${CHART_VERSION_ID}" \
    --arg release_name "${RELEASE_NAME}" \
    --arg namespace "${NAMESPACE}" \
    '{
      cluster_id:$cluster_id,
      chart_version_id:$chart_version_id,
      release_name:$release_name,
      namespace:$namespace
    }'
)"

install_resp="$(
  curl -fsS \
    -H "Authorization: Bearer ${AUTH_TOKEN}" \
    -H 'Content-Type: application/json' \
    -X POST \
    "${API_BASE}/catalog/installed/" \
    -d "${install_body}"
)"
INSTALLATION_ID="$(echo "${install_resp}" | jq -r '.data.installation.id')"
install_op_id="$(echo "${install_resp}" | jq -r '.data.operation.id')"
poll_operation "${install_op_id}"

curl -fsS \
  -H "Authorization: Bearer ${AUTH_TOKEN}" \
  "${API_BASE}/catalog/installed/?cluster_id=${CLUSTER_ID}&limit=200" |
  jq -r --arg id "${INSTALLATION_ID}" '
    .data[]
    | select(.id == $id)
    | "installed-chart-status=\(.status) release=\(.release_name) namespace=\(.namespace)"
  '

echo "Waiting for remote deployments to become ready"
for i in $(seq 1 "${EVENT_TIMEOUT}"); do
  deploys_json="$(kubectl --context "${REMOTE_CONTEXT}" -n "${NAMESPACE}" get deploy -o json 2>/dev/null || true)"
  deploys="$(echo "${deploys_json}" | jq -r '.items[]? | "\(.metadata.name)=\(.status.readyReplicas // 0)/\(.spec.replicas // 0)"')"
  echo "deploy-poll=${i}"
  echo "${deploys}"
  if [[ -n "${deploys}" ]] && echo "${deploys_json}" | jq -e '(.items | length) > 0 and all(.items[]; ((.status.readyReplicas // 0) == (.spec.replicas // 0)))' >/dev/null; then
    break
  fi
  sleep 2
done

echo "Verifying remote Helm release"
helm --kube-context "${REMOTE_CONTEXT}" -n "${NAMESPACE}" ls -a | grep "${RELEASE_NAME}"

echo "Validated OCI repo create -> sync -> install against a real remote cluster"
