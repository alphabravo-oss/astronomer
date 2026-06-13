#!/usr/bin/env bash
# Validate ArgoCD auto-adoption for an already registered downstream cluster.
#
# Usage:
#   ASTRO_USERNAME=admin ASTRO_PASSWORD=... ./scripts/validate-live-argocd-auto-adoption.sh
#   AUTH_TOKEN=... CLUSTER_ID=... ./scripts/validate-live-argocd-auto-adoption.sh
#
# Optional env:
#   BASE_URL       default: http://astronomer.localtest.me:8080
#   INSTANCE_ID    default: first listed ArgoCD instance
#   CLUSTER_ID     default: first active non-local cluster
#   MGMT_CONTEXT   default: k3d-astronomer-mgmt
#   EVENT_TIMEOUT  default: 120
#   VALIDATE_PRUNING default: false; when true, temporarily removes the
#                   selected cluster Secret from the built-in ApplicationSet
#                   selector and verifies generated baseline Applications prune

set -euo pipefail

BASE_URL="${BASE_URL:-http://astronomer.localtest.me:8080}"
API_BASE="${BASE_URL%/}/api/v1"
MGMT_CONTEXT="${MGMT_CONTEXT:-k3d-astronomer-mgmt}"
EVENT_TIMEOUT="${EVENT_TIMEOUT:-120}"
AUTH_TOKEN="${AUTH_TOKEN:-}"
ASTRO_USERNAME="${ASTRO_USERNAME:-}"
ASTRO_PASSWORD="${ASTRO_PASSWORD:-}"
INSTANCE_ID="${INSTANCE_ID:-}"
CLUSTER_ID="${CLUSTER_ID:-}"
CLUSTER_NAME="${CLUSTER_NAME:-}"
VALIDATE_PRUNING="${VALIDATE_PRUNING:-false}"
VALIDATION_VALUE="run-$(date +%s)"
BASELINE_APPSETS=(
  astronomer-baseline-trivy
  astronomer-baseline-kube-state-metrics
  astronomer-baseline-node-exporter
  astronomer-baseline-fluent-bit
  astronomer-baseline-cert-manager
)

require() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "missing required tool: $1" >&2
    exit 1
  }
}

api() {
  local method="$1"
  local path="$2"
  shift 2
  curl -fsS \
    -H "Authorization: Bearer ${AUTH_TOKEN}" \
    -H 'Content-Type: application/json' \
    -X "${method}" \
    "${API_BASE}${path}" \
    "$@"
}

wait_for() {
  local label="$1"
  shift
  for i in $(seq 1 "${EVENT_TIMEOUT}"); do
    if "$@"; then
      echo "ok ${label}"
      return 0
    fi
    echo "wait ${label} ${i}/${EVENT_TIMEOUT}"
    sleep 1
  done
  echo "timed out waiting for ${label}" >&2
  return 1
}

require curl
require jq
require kubectl

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

if [[ -z "${INSTANCE_ID}" ]]; then
  INSTANCE_ID="$(api GET /argocd/instances/ | jq -r '.data[0].id // empty')"
fi
if [[ -z "${INSTANCE_ID}" ]]; then
  echo "no ArgoCD instance found; set INSTANCE_ID explicitly" >&2
  exit 1
fi

if [[ -z "${CLUSTER_ID}" ]]; then
  CLUSTER_ID="$(
    api GET /clusters/ |
      jq -r '.data[] | select(.status == "active" and (.is_local | not)) | .id' |
      head -n1
  )"
fi
if [[ -z "${CLUSTER_ID}" ]]; then
  echo "no active non-local cluster found; register a downstream cluster or set CLUSTER_ID" >&2
  exit 1
fi

CLUSTER_JSON="$(api GET "/clusters/${CLUSTER_ID}/" | jq -c '.data')"
CLUSTER_NAME="${CLUSTER_NAME:-$(jq -r '.name' <<<"${CLUSTER_JSON}")}"
if [[ -z "${CLUSTER_NAME}" || "${CLUSTER_NAME}" == "null" ]]; then
  echo "unable to resolve cluster name for ${CLUSTER_ID}" >&2
  exit 1
fi

echo "Using instance_id=${INSTANCE_ID} cluster_id=${CLUSTER_ID} cluster_name=${CLUSTER_NAME}"

managed_cluster_present() {
  api GET "/argocd/instances/${INSTANCE_ID}/clusters/" |
    jq -e --arg id "${CLUSTER_ID}" '.data[] | select(.cluster_id == $id)' >/dev/null
}

wait_for "argocd managed-cluster row" managed_cluster_present

ARGO_CLUSTER_SERVER="$(
  api GET "/argocd/instances/${INSTANCE_ID}/clusters/" |
    jq -r --arg id "${CLUSTER_ID}" '.data[] | select(.cluster_id == $id) | (.server // .server_url // empty)' |
    head -n1
)"

cluster_secret_json() {
  kubectl --context "${MGMT_CONTEXT}" -n argocd get secret \
    -l "argocd.argoproj.io/secret-type=cluster,astronomer.io/cluster-id=${CLUSTER_ID}" \
    -o json
}

cluster_secret_labeled() {
  cluster_secret_json |
    jq -e '.items[0].metadata.labels["astronomer.io/managed-by"] == "astronomer"' >/dev/null
}

wait_for "argocd cluster Secret labels" cluster_secret_labeled

ARGO_CLUSTER_SECRET="$(
  cluster_secret_json |
    jq -r '.items[0].metadata.name // empty'
)"
if [[ -z "${ARGO_CLUSTER_SECRET}" ]]; then
  echo "unable to resolve ArgoCD cluster Secret for ${CLUSTER_ID}" >&2
  exit 1
fi

for appset in "${BASELINE_APPSETS[@]}"; do
  kubectl --context "${MGMT_CONTEXT}" -n argocd get applicationset.argoproj.io "${appset}" >/dev/null
done
echo "ok built-in baseline ApplicationSets exist"

baseline_app_generated() {
  kubectl --context "${MGMT_CONTEXT}" -n argocd get applications.argoproj.io -o json |
    jq -e --arg server "${ARGO_CLUSTER_SERVER}" --arg cluster_id "${CLUSTER_ID}" '
      [.items[]
        | select((.spec.destination.server == $server) or (.spec.destination.server | contains($cluster_id)))
        | select([.metadata.ownerReferences[]?.name] | any(startswith("astronomer-baseline-")))
      ] | length > 0
    ' >/dev/null
}

wait_for "baseline ApplicationSet fan-out" baseline_app_generated

baseline_app_count() {
  kubectl --context "${MGMT_CONTEXT}" -n argocd get applications.argoproj.io -o json |
    jq --arg server "${ARGO_CLUSTER_SERVER}" --arg cluster_id "${CLUSTER_ID}" '
      [.items[]
        | select((.spec.destination.server == $server) or (.spec.destination.server | contains($cluster_id)))
        | select([.metadata.ownerReferences[]?.name] | any(startswith("astronomer-baseline-")))
      ] | length
    '
}

baseline_app_synced() {
  kubectl --context "${MGMT_CONTEXT}" -n argocd get applications.argoproj.io -o json |
    jq -e --arg server "${ARGO_CLUSTER_SERVER}" --arg cluster_id "${CLUSTER_ID}" '
      [.items[]
        | select((.spec.destination.server == $server) or (.spec.destination.server | contains($cluster_id)))
        | select([.metadata.ownerReferences[]?.name] | any(startswith("astronomer-baseline-")))
        | select(.status.sync.status == "Synced" and .status.health.status == "Healthy")
      ] | length > 0
    ' >/dev/null
}

wait_for "at least one baseline app Healthy/Synced" baseline_app_synced

echo "Patching cluster labels to validate ArgoCD Secret refresh"
UPDATE_BODY="$(
  jq -n \
    --arg display_name "$(jq -r '.display_name // ""' <<<"${CLUSTER_JSON}")" \
    --arg description "$(jq -r '.description // ""' <<<"${CLUSTER_JSON}")" \
    --arg environment "$(jq -r '.environment // "development"' <<<"${CLUSTER_JSON}")" \
    --arg region "$(jq -r '.region // ""' <<<"${CLUSTER_JSON}")" \
    --arg value "${VALIDATION_VALUE}" \
    --argjson labels "$(jq -c '.labels // {}' <<<"${CLUSTER_JSON}")" \
    --argjson annotations "$(jq -c '.annotations // {}' <<<"${CLUSTER_JSON}")" \
    '{
      display_name: $display_name,
      description: $description,
      environment: $environment,
      region: $region,
      labels: ($labels + {"validation-run": $value}),
      annotations: $annotations
    }'
)"
api PATCH "/clusters/${CLUSTER_ID}/" -d "${UPDATE_BODY}" >/dev/null

refreshed_label_present() {
  cluster_secret_json |
    jq -e --arg value "${VALIDATION_VALUE}" '.items[0].metadata.labels["astronomer.io/label-validation-run"] == $value' >/dev/null
}

wait_for "argocd cluster Secret label refresh" refreshed_label_present

if [[ "${VALIDATE_PRUNING}" == "true" ]]; then
  echo "Temporarily changing ArgoCD cluster Secret selector labels to validate ApplicationSet pruning"
  kubectl --context "${MGMT_CONTEXT}" -n argocd label secret "${ARGO_CLUSTER_SECRET}" \
    astronomer.io/managed-by=validation-disabled \
    --overwrite >/dev/null

  baseline_apps_pruned() {
    [[ "$(baseline_app_count)" == "0" ]]
  }

  restore_selector_labels() {
    kubectl --context "${MGMT_CONTEXT}" -n argocd label secret "${ARGO_CLUSTER_SECRET}" \
      astronomer.io/managed-by=astronomer \
      astronomer.io/is-local=false \
      --overwrite >/dev/null
  }
  trap restore_selector_labels EXIT

  wait_for "baseline ApplicationSet pruning after selector removal" baseline_apps_pruned
  restore_selector_labels
  trap - EXIT
  wait_for "baseline ApplicationSet fan-out after selector restore" baseline_app_generated
fi

echo "Validated ArgoCD auto-adoption, baseline fan-out, and label refresh"
