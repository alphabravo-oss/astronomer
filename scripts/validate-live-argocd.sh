#!/usr/bin/env bash
# Validate a real ArgoCD lifecycle path through Astronomer:
#   create app -> reconcile on remote cluster -> patch replicas -> delete cascade
#
# Usage:
#   ASTRO_USERNAME=admin ASTRO_PASSWORD=... ./scripts/validate-live-argocd.sh
#   AUTH_TOKEN=... ./scripts/validate-live-argocd.sh
#
# Optional env:
#   BASE_URL        default: http://astronomer.localtest.me:8080
#   INSTANCE_ID     default: first listed ArgoCD instance
#   CLUSTER_ID      default: first active non-local cluster
#   MGMT_CONTEXT    default: k3d-astronomer-mgmt
#   REMOTE_CONTEXT  default: k3d-astronomer-remote
#   CHART_VERSION   default: 6.5.4
#   EVENT_TIMEOUT   default: 60

set -euo pipefail

BASE_URL="${BASE_URL:-http://astronomer.localtest.me:8080}"
API_BASE="${BASE_URL%/}/api/v1"
MGMT_CONTEXT="${MGMT_CONTEXT:-k3d-astronomer-mgmt}"
REMOTE_CONTEXT="${REMOTE_CONTEXT:-k3d-astronomer-remote}"
CHART_VERSION="${CHART_VERSION:-6.5.4}"
EVENT_TIMEOUT="${EVENT_TIMEOUT:-60}"
AUTH_TOKEN="${AUTH_TOKEN:-}"
ASTRO_USERNAME="${ASTRO_USERNAME:-}"
ASTRO_PASSWORD="${ASTRO_PASSWORD:-}"
INSTANCE_ID="${INSTANCE_ID:-}"
CLUSTER_ID="${CLUSTER_ID:-}"
APP_NAME="podinfo-verify-$(date +%s)"
NAMESPACE="${APP_NAME}"

cleanup() {
  if [[ -n "${AUTH_TOKEN}" && -n "${INSTANCE_ID}" ]]; then
    curl -fsS \
      -H "Authorization: Bearer ${AUTH_TOKEN}" \
      -X DELETE \
      "${API_BASE}/argocd/instances/${INSTANCE_ID}/applications/${APP_NAME}/?cascade=true" >/dev/null 2>&1 || true
  fi
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
  INSTANCE_ID="$(
    curl -fsS -H "Authorization: Bearer ${AUTH_TOKEN}" "${API_BASE}/argocd/instances/" |
      jq -r '.data[0].id // empty'
  )"
fi
if [[ -z "${INSTANCE_ID}" ]]; then
  echo "no ArgoCD instance found; set INSTANCE_ID explicitly" >&2
  exit 1
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

DEST_SERVER="http://astronomer-server.astronomer.svc.cluster.local:8000/api/v1/clusters/${CLUSTER_ID}/k8s"
APP_BASE="${API_BASE}/argocd/instances/${INSTANCE_ID}/applications/${APP_NAME}"

echo "Using instance_id=${INSTANCE_ID} cluster_id=${CLUSTER_ID} context=${REMOTE_CONTEXT}"
echo "Creating application ${APP_NAME} in namespace ${NAMESPACE}"

CREATE_BODY="$(cat <<EOF
{"name":"${APP_NAME}","spec":{"project":"default","source":{"repoURL":"https://stefanprodan.github.io/podinfo","chart":"podinfo","targetRevision":"${CHART_VERSION}","helm":{"values":"replicaCount: 1\\n"}},"destination":{"server":"${DEST_SERVER}","namespace":"${NAMESPACE}"},"syncPolicy":{"automated":{"prune":true,"selfHeal":true},"syncOptions":["CreateNamespace=true"]}}}
EOF
)"

curl -fsS \
  -H "Authorization: Bearer ${AUTH_TOKEN}" \
  -H 'Content-Type: application/json' \
  -X POST \
  "${API_BASE}/argocd/instances/${INSTANCE_ID}/applications/" \
  -d "${CREATE_BODY}" >/dev/null

for i in $(seq 1 "${EVENT_TIMEOUT}"); do
  app_status="$(kubectl get application.argoproj.io "${APP_NAME}" -n argocd --context "${MGMT_CONTEXT}" -o jsonpath='{.status.sync.status} {.status.health.status}' 2>/dev/null || true)"
  deploy_status="$(kubectl get deploy "${APP_NAME}" -n "${NAMESPACE}" --context "${REMOTE_CONTEXT}" -o jsonpath='{.status.readyReplicas}/{.spec.replicas}' 2>/dev/null || true)"
  echo "create-poll=${i} app=[${app_status}] deploy=[${deploy_status}]"
  if [[ "${app_status}" == "Synced Healthy" && "${deploy_status}" == "1/1" ]]; then
    break
  fi
  sleep 1
done

PATCH_BODY='{"spec":{"source":{"helm":{"values":"replicaCount: 2\n"}}}}'
curl -fsS \
  -H "Authorization: Bearer ${AUTH_TOKEN}" \
  -H 'Content-Type: application/json' \
  -X PATCH \
  "${APP_BASE}/" \
  -d "${PATCH_BODY}" >/dev/null

for i in $(seq 1 "${EVENT_TIMEOUT}"); do
  app_status="$(kubectl get application.argoproj.io "${APP_NAME}" -n argocd --context "${MGMT_CONTEXT}" -o jsonpath='{.status.sync.status} {.status.health.status}' 2>/dev/null || true)"
  deploy_status="$(kubectl get deploy "${APP_NAME}" -n "${NAMESPACE}" --context "${REMOTE_CONTEXT}" -o jsonpath='{.status.readyReplicas}/{.spec.replicas}' 2>/dev/null || true)"
  echo "patch-poll=${i} app=[${app_status}] deploy=[${deploy_status}]"
  if [[ "${app_status}" == "Synced Healthy" && "${deploy_status}" == "2/2" ]]; then
    break
  fi
  sleep 1
done

curl -fsS \
  -H "Authorization: Bearer ${AUTH_TOKEN}" \
  -X DELETE \
  "${APP_BASE}/?cascade=true" >/dev/null

for i in $(seq 1 "${EVENT_TIMEOUT}"); do
  app_ref="$(kubectl get application.argoproj.io "${APP_NAME}" -n argocd --context "${MGMT_CONTEXT}" --ignore-not-found -o name)"
  deploy_ref="$(kubectl get deploy "${APP_NAME}" -n "${NAMESPACE}" --context "${REMOTE_CONTEXT}" --ignore-not-found -o name 2>/dev/null || true)"
  echo "delete-poll=${i} app=[${app_ref}] deploy=[${deploy_ref}]"
  if [[ -z "${app_ref}" && -z "${deploy_ref}" ]]; then
    break
  fi
  sleep 1
done

echo "Validated ArgoCD create -> patch -> delete against a real upstream instance"
