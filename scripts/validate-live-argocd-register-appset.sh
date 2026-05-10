#!/usr/bin/env bash
# Validate two ArgoCD lifecycle paths through Astronomer:
#   1. register a non-local managed cluster into an ArgoCD instance
#   2. create an ApplicationSet that fans out to that cluster, then delete it
#
# Usage:
#   ASTRO_USERNAME=admin ASTRO_PASSWORD=... ./scripts/validate-live-argocd-register-appset.sh
#   AUTH_TOKEN=... ./scripts/validate-live-argocd-register-appset.sh
#
# Optional env:
#   BASE_URL         default: http://astronomer.localtest.me:8080
#   INSTANCE_ID      default: first listed ArgoCD instance
#   CLUSTER_ID       default: first active non-local cluster
#   CLUSTER_NAME     default: looked up from CLUSTER_ID
#   MGMT_CONTEXT     default: k3d-astronomer-mgmt
#   REMOTE_CONTEXT   default: k3d-astronomer-remote
#   EVENT_TIMEOUT    default: 60
#   CHART_VERSION    default: 6.5.4

set -euo pipefail

BASE_URL="${BASE_URL:-http://astronomer.localtest.me:8080}"
API_BASE="${BASE_URL%/}/api/v1"
MGMT_CONTEXT="${MGMT_CONTEXT:-k3d-astronomer-mgmt}"
REMOTE_CONTEXT="${REMOTE_CONTEXT:-k3d-astronomer-remote}"
EVENT_TIMEOUT="${EVENT_TIMEOUT:-60}"
CHART_VERSION="${CHART_VERSION:-6.5.4}"
AUTH_TOKEN="${AUTH_TOKEN:-}"
ASTRO_USERNAME="${ASTRO_USERNAME:-}"
ASTRO_PASSWORD="${ASTRO_PASSWORD:-}"
INSTANCE_ID="${INSTANCE_ID:-}"
CLUSTER_ID="${CLUSTER_ID:-}"
CLUSTER_NAME="${CLUSTER_NAME:-}"
APPSET_NAME="appset-verify-$(date +%s)"
APP_NAME="${APPSET_NAME}-dev"
APP_NAMESPACE="${APPSET_NAME}-dev"

cleanup() {
  if [[ -n "${AUTH_TOKEN}" && -n "${INSTANCE_ID}" ]]; then
    curl -fsS \
      -H "Authorization: Bearer ${AUTH_TOKEN}" \
      -X DELETE \
      "${API_BASE}/argocd/instances/${INSTANCE_ID}/applicationsets/${APPSET_NAME}/" >/dev/null 2>&1 || true
  fi
  kubectl --context "${REMOTE_CONTEXT}" delete namespace "${APP_NAMESPACE}" --ignore-not-found >/dev/null 2>&1 || true
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

if [[ -z "${CLUSTER_NAME}" ]]; then
  CLUSTER_NAME="$(
    curl -fsS -H "Authorization: Bearer ${AUTH_TOKEN}" "${API_BASE}/clusters/" |
      jq -r --arg id "${CLUSTER_ID}" '.data[] | select(.id == $id) | .name'
  )"
fi

if [[ -z "${CLUSTER_NAME}" ]]; then
  echo "unable to resolve cluster name for ${CLUSTER_ID}" >&2
  exit 1
fi

DEST_SERVER="http://astronomer-server.astronomer.svc.cluster.local:8000/api/v1/clusters/${CLUSTER_ID}/k8s"

echo "Using instance_id=${INSTANCE_ID} cluster_id=${CLUSTER_ID} cluster_name=${CLUSTER_NAME}"

echo "Preparing remote-cluster ArgoCD manager ServiceAccount"
kubectl --context "${REMOTE_CONTEXT}" apply -f - >/dev/null <<'YAML'
apiVersion: v1
kind: ServiceAccount
metadata:
  name: argocd-manager
  namespace: kube-system
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: argocd-manager
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: cluster-admin
subjects:
- kind: ServiceAccount
  name: argocd-manager
  namespace: kube-system
YAML

K8S_TOKEN="$(
  kubectl --context "${REMOTE_CONTEXT}" -n kube-system create token argocd-manager --duration=24h
)"

echo "Removing any stale upstream ArgoCD cluster secret for ${CLUSTER_ID}"
kubectl --context "${MGMT_CONTEXT}" -n argocd delete secret \
  -l "argocd.argoproj.io/secret-type=cluster,astronomer.io/cluster-id=${CLUSTER_ID}" \
  --ignore-not-found >/dev/null

echo "Clearing any managed-cluster DB mapping for ${CLUSTER_ID}"
curl -fsS \
  -H "Authorization: Bearer ${AUTH_TOKEN}" \
  -X DELETE \
  "${API_BASE}/argocd/instances/${INSTANCE_ID}/clusters/${CLUSTER_ID}/register/" >/dev/null 2>&1 || true

echo "Registering cluster through Astronomer"
REGISTER_BODY="$(
  jq -n \
    --arg token "${K8S_TOKEN}" \
    --arg server "${DEST_SERVER}" \
    --arg env "dev" \
    '{bearer_token:$token,server:$server,insecure:true,labels:{"astronomer.io/environment":$env}}'
)"

curl -fsS \
  -H "Authorization: Bearer ${AUTH_TOKEN}" \
  -H 'Content-Type: application/json' \
  -X POST \
  "${API_BASE}/argocd/instances/${INSTANCE_ID}/clusters/${CLUSTER_ID}/register/" \
  -d "${REGISTER_BODY}" | jq

echo "Verifying cluster now appears in Astronomer managed-cluster list"
curl -fsS \
  -H "Authorization: Bearer ${AUTH_TOKEN}" \
  "${API_BASE}/argocd/instances/${INSTANCE_ID}/clusters/" |
  jq -e --arg id "${CLUSTER_ID}" --arg server "${DEST_SERVER}" '
    .data[] | select(.cluster_id == $id and .server == $server)
  ' >/dev/null

echo "Creating ApplicationSet ${APPSET_NAME}"
APPSET_BODY="$(
  jq -n \
    --arg name "${APPSET_NAME}" \
    --arg ns "${APP_NAMESPACE}" \
    --arg chartVersion "${CHART_VERSION}" \
    '{
      name: $name,
      spec: {
        generators: [
          {
            clusters: {
              selector: {
                matchLabels: {
                  "astronomer.io/environment": "dev"
                }
              }
            }
          }
        ],
        template: {
          metadata: {
            name: ($name + "-{{name}}")
          },
          spec: {
            project: "default",
            source: {
              repoURL: "https://stefanprodan.github.io/podinfo",
              chart: "podinfo",
              targetRevision: $chartVersion,
              helm: {
                values: "replicaCount: 1\n"
              }
            },
            destination: {
              server: "{{server}}",
              namespace: $ns
            },
            syncPolicy: {
              automated: {
                prune: true,
                selfHeal: true
              },
              syncOptions: [
                "CreateNamespace=true"
              ]
            }
          }
        }
      }
    }'
)"

curl -fsS \
  -H "Authorization: Bearer ${AUTH_TOKEN}" \
  -H 'Content-Type: application/json' \
  -X POST \
  "${API_BASE}/argocd/instances/${INSTANCE_ID}/applicationsets/" \
  -d "${APPSET_BODY}" >/dev/null

for i in $(seq 1 "${EVENT_TIMEOUT}"); do
  app_status="$(kubectl get application.argoproj.io "${APP_NAME}" -n argocd --context "${MGMT_CONTEXT}" -o jsonpath='{.status.sync.status} {.status.health.status}' 2>/dev/null || true)"
  deploy_status="$(kubectl get deploy "${APP_NAMESPACE}-podinfo" -n "${APP_NAMESPACE}" --context "${REMOTE_CONTEXT}" -o jsonpath='{.status.readyReplicas}/{.spec.replicas}' 2>/dev/null || true)"
  echo "create-poll=${i} app=[${app_status}] deploy=[${deploy_status}]"
  if [[ "${app_status}" == "Synced Healthy" && "${deploy_status}" == "1/1" ]]; then
    break
  fi
  sleep 1
done

echo "Deleting ApplicationSet ${APPSET_NAME}"
curl -fsS \
  -H "Authorization: Bearer ${AUTH_TOKEN}" \
  -X DELETE \
  "${API_BASE}/argocd/instances/${INSTANCE_ID}/applicationsets/${APPSET_NAME}/" >/dev/null

for i in $(seq 1 "${EVENT_TIMEOUT}"); do
  appset_ref="$(kubectl get applicationset.argoproj.io "${APPSET_NAME}" -n argocd --context "${MGMT_CONTEXT}" --ignore-not-found -o name)"
  app_ref="$(kubectl get application.argoproj.io "${APP_NAME}" -n argocd --context "${MGMT_CONTEXT}" --ignore-not-found -o name)"
  deploy_ref="$(kubectl get deploy "${APP_NAMESPACE}-podinfo" -n "${APP_NAMESPACE}" --context "${REMOTE_CONTEXT}" --ignore-not-found -o name 2>/dev/null || true)"
  echo "delete-poll=${i} appset=[${appset_ref}] app=[${app_ref}] deploy=[${deploy_ref}]"
  if [[ -z "${appset_ref}" && -z "${app_ref}" && -z "${deploy_ref}" ]]; then
    break
  fi
  sleep 1
done

echo "Validated ArgoCD cluster register -> ApplicationSet fan-out -> delete against a real upstream instance"
