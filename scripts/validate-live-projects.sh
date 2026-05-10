#!/usr/bin/env bash
# Validate live project enforcement through Astronomer:
#   project create -> namespace attach -> reconcile ResourceQuota / LimitRange /
#   NetworkPolicy / PSA labels / registry secret -> namespace detach cleanup
#   -> audit rows
#
# Usage:
#   ASTRO_USERNAME=admin ASTRO_PASSWORD=... ./scripts/validate-live-projects.sh
#   AUTH_TOKEN=... ./scripts/validate-live-projects.sh
#
# Optional env:
#   BASE_URL        default: http://astronomer.localtest.me:8080
#   REMOTE_CONTEXT  default: k3d-astronomer-remote
#   CLUSTER_ID      default: first active non-local cluster
#   EVENT_TIMEOUT   default: 60

set -euo pipefail

BASE_URL="${BASE_URL:-http://astronomer.localtest.me:8080}"
API_BASE="${BASE_URL%/}/api/v1"
REMOTE_CONTEXT="${REMOTE_CONTEXT:-k3d-astronomer-remote}"
EVENT_TIMEOUT="${EVENT_TIMEOUT:-60}"
AUTH_TOKEN="${AUTH_TOKEN:-}"
ASTRO_USERNAME="${ASTRO_USERNAME:-}"
ASTRO_PASSWORD="${ASTRO_PASSWORD:-}"
CLUSTER_ID="${CLUSTER_ID:-}"

PROJECT_NAME="project-verify-$(date +%s)"
PROJECT_DISPLAY_NAME="Project Verify"
NAMESPACE="project-verify-$(date +%s)"
REGISTRY_URL="https://registry.example.invalid"
REGISTRY_HOST="registry.example.invalid"
REGISTRY_USERNAME="project-validator"
REGISTRY_PASSWORD="validator-pass-123"

PROJECT_ID=""
CREATED_TEMPLATE_ID=""
ORIGINAL_REGISTRY_JSON=""
ORIGINAL_REGISTRY_PRESENT=0
EXPECT_REGISTRY_DELETE_AUDIT=0
REMOVE_COMPLETED=0
PROJECT_DELETED=0
NAMESPACE_CREATED=0
AUDIT_CURSOR=""

cleanup() {
  if [[ -n "${AUTH_TOKEN}" && -n "${PROJECT_ID}" && "${PROJECT_DELETED}" -ne 1 ]]; then
    curl -fsS \
      -H "Authorization: Bearer ${AUTH_TOKEN}" \
      -X DELETE \
      "${API_BASE}/projects/${PROJECT_ID}/" >/dev/null 2>&1 || true
  fi
  restore_registry_config >/dev/null 2>&1 || true
  delete_created_template >/dev/null 2>&1 || true
  if [[ "${NAMESPACE_CREATED}" -eq 1 ]]; then
    kubectl --context "${REMOTE_CONTEXT}" delete namespace "${NAMESPACE}" --ignore-not-found >/dev/null 2>&1 || true
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

api() {
  curl -fsS -H "Authorization: Bearer ${AUTH_TOKEN}" "$@"
}

restore_registry_config() {
  if [[ -z "${AUTH_TOKEN}" || -z "${CLUSTER_ID}" ]]; then
    return 0
  fi
  if [[ "${ORIGINAL_REGISTRY_PRESENT}" -eq 1 ]]; then
    curl -fsS \
      -H "Authorization: Bearer ${AUTH_TOKEN}" \
      -H 'Content-Type: application/json' \
      -X PUT \
      "${API_BASE}/clusters/${CLUSTER_ID}/registry/" \
      -d "${ORIGINAL_REGISTRY_JSON}" >/dev/null
  else
    curl -fsS \
      -H "Authorization: Bearer ${AUTH_TOKEN}" \
      -X DELETE \
      "${API_BASE}/clusters/${CLUSTER_ID}/registry/" >/dev/null
  fi
}

delete_created_template() {
  if [[ -z "${AUTH_TOKEN}" || -z "${CREATED_TEMPLATE_ID}" ]]; then
    return 0
  fi
  curl -fsS \
    -H "Authorization: Bearer ${AUTH_TOKEN}" \
    -X DELETE \
    "${API_BASE}/security/templates/${CREATED_TEMPLATE_ID}/" >/dev/null
}

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
    api "${API_BASE}/clusters/" |
      jq -r '.data[] | select(.status == "active" and (.is_local | not)) | .id' |
      head -n1
  )"
fi

if [[ -z "${CLUSTER_ID}" ]]; then
  echo "no active non-local cluster found; set CLUSTER_ID explicitly" >&2
  exit 1
fi

AUDIT_CURSOR="$(
  api "${API_BASE}/audit/?limit=1" |
    jq -r '.data[0].id // empty'
)"

echo "Using cluster_id=${CLUSTER_ID} context=${REMOTE_CONTEXT} namespace=${NAMESPACE}"

TEMPLATES_JSON="$(api "${API_BASE}/security/templates/?limit=1000")"
DEFAULT_TEMPLATE_JSON="$(
  jq -c '.data[] | select(.is_default == true)' <<<"${TEMPLATES_JSON}" | head -n1
)"
if [[ -z "${DEFAULT_TEMPLATE_JSON}" ]]; then
  echo "Creating default pod-security template for validator"
  DEFAULT_TEMPLATE_JSON="$(
    api \
      -H 'Content-Type: application/json' \
      -X POST \
      "${API_BASE}/security/templates/" \
      -d "$(jq -n \
        --arg name "validator-default-psa-$(date +%s)" \
        '{
          name: $name,
          description: "validator-created default PSA template",
          is_default: true,
          enforce_level: "baseline",
          enforce_version: "latest",
          audit_level: "restricted",
          audit_version: "latest",
          warn_level: "restricted",
          warn_version: "latest",
          exempt_usernames: [],
          exempt_runtime_classes: [],
          exempt_namespaces: []
        }')" |
      jq -c '.data'
  )"
  CREATED_TEMPLATE_ID="$(jq -r '.id' <<<"${DEFAULT_TEMPLATE_JSON}")"
fi

PSA_ENFORCE_LEVEL="$(jq -r '.enforce_level // empty' <<<"${DEFAULT_TEMPLATE_JSON}")"
PSA_ENFORCE_VERSION="$(jq -r '.enforce_version // empty' <<<"${DEFAULT_TEMPLATE_JSON}")"
PSA_AUDIT_LEVEL="$(jq -r '.audit_level // empty' <<<"${DEFAULT_TEMPLATE_JSON}")"
PSA_AUDIT_VERSION="$(jq -r '.audit_version // empty' <<<"${DEFAULT_TEMPLATE_JSON}")"
PSA_WARN_LEVEL="$(jq -r '.warn_level // empty' <<<"${DEFAULT_TEMPLATE_JSON}")"
PSA_WARN_VERSION="$(jq -r '.warn_version // empty' <<<"${DEFAULT_TEMPLATE_JSON}")"

REGISTRY_TMP="$(mktemp)"
REGISTRY_STATUS="$(
  curl -sS -o "${REGISTRY_TMP}" -w '%{http_code}' \
    -H "Authorization: Bearer ${AUTH_TOKEN}" \
    "${API_BASE}/clusters/${CLUSTER_ID}/registry/"
)"
if [[ "${REGISTRY_STATUS}" == "200" ]]; then
  ORIGINAL_REGISTRY_PRESENT=1
  ORIGINAL_REGISTRY_JSON="$(jq -c '.data' < "${REGISTRY_TMP}")"
elif [[ "${REGISTRY_STATUS}" != "404" ]]; then
  echo "failed to read existing registry config: status=${REGISTRY_STATUS}" >&2
  cat "${REGISTRY_TMP}" >&2
  rm -f "${REGISTRY_TMP}"
  exit 1
fi
rm -f "${REGISTRY_TMP}"
if [[ "${ORIGINAL_REGISTRY_PRESENT}" -eq 0 ]]; then
  EXPECT_REGISTRY_DELETE_AUDIT=1
fi

echo "Setting cluster registry config"
api \
  -H 'Content-Type: application/json' \
  -X PUT \
  "${API_BASE}/clusters/${CLUSTER_ID}/registry/" \
  -d "$(jq -n \
    --arg url "${REGISTRY_URL}" \
    --arg username "${REGISTRY_USERNAME}" \
    --arg password "${REGISTRY_PASSWORD}" \
    '{
      private_registry_url: $url,
      registry_username: $username,
      registry_password: $password,
      insecure: false,
      ca_bundle: ""
    }')" >/dev/null

echo "Creating remote namespace ${NAMESPACE}"
kubectl --context "${REMOTE_CONTEXT}" create namespace "${NAMESPACE}" >/dev/null
NAMESPACE_CREATED=1

echo "Creating project ${PROJECT_NAME}"
PROJECT_BODY="$(
  jq -n \
    --arg name "${PROJECT_NAME}" \
    --arg display "${PROJECT_DISPLAY_NAME}" \
    --arg cluster_id "${CLUSTER_ID}" \
    '{
      name: $name,
      display_name: $display,
      description: "live validation project",
      cluster_id: $cluster_id,
      namespaces: [],
      resource_quota: {
        cpu: "2",
        memory: "1Gi",
        pods: "10"
      },
      limit_range: {
        default: {
          cpu: "500m",
          memory: "256Mi"
        },
        default_request: {
          cpu: "250m",
          memory: "128Mi"
        }
      },
      network_policy_mode: "isolated"
    }'
)"
PROJECT_JSON="$(
  api \
    -H 'Content-Type: application/json' \
    -X POST \
    "${API_BASE}/projects/" \
    -d "${PROJECT_BODY}"
)"
PROJECT_ID="$(jq -r '.data.id' <<<"${PROJECT_JSON}")"
if [[ -z "${PROJECT_ID}" || "${PROJECT_ID}" == "null" ]]; then
  echo "failed to create project" >&2
  echo "${PROJECT_JSON}" >&2
  exit 1
fi

echo "Attaching namespace to project"
api \
  -H 'Content-Type: application/json' \
  -X POST \
  "${API_BASE}/projects/${PROJECT_ID}/add-namespace/" \
  -d "{\"namespace\":\"${NAMESPACE}\"}" >/dev/null

echo "Waiting for reconcile objects"
for i in $(seq 1 "${EVENT_TIMEOUT}"); do
  if kubectl --context "${REMOTE_CONTEXT}" get resourcequota astronomer-quota -n "${NAMESPACE}" >/dev/null 2>&1 &&
    kubectl --context "${REMOTE_CONTEXT}" get limitrange astronomer-limits -n "${NAMESPACE}" >/dev/null 2>&1 &&
    kubectl --context "${REMOTE_CONTEXT}" get networkpolicy astronomer-isolation -n "${NAMESPACE}" >/dev/null 2>&1 &&
    kubectl --context "${REMOTE_CONTEXT}" get secret astronomer-registry -n "${NAMESPACE}" >/dev/null 2>&1; then
    break
  fi
  echo "reconcile-poll=${i}"
  sleep 1
done

NS_JSON="$(kubectl --context "${REMOTE_CONTEXT}" get namespace "${NAMESPACE}" -o json)"
RQ_JSON="$(kubectl --context "${REMOTE_CONTEXT}" get resourcequota astronomer-quota -n "${NAMESPACE}" -o json)"
LR_JSON="$(kubectl --context "${REMOTE_CONTEXT}" get limitrange astronomer-limits -n "${NAMESPACE}" -o json)"
NP_JSON="$(kubectl --context "${REMOTE_CONTEXT}" get networkpolicy astronomer-isolation -n "${NAMESPACE}" -o json)"
SECRET_JSON="$(kubectl --context "${REMOTE_CONTEXT}" get secret astronomer-registry -n "${NAMESPACE}" -o json)"
SA_JSON="$(kubectl --context "${REMOTE_CONTEXT}" get serviceaccount default -n "${NAMESPACE}" -o json)"

jq -e --arg project_id "${PROJECT_ID}" '
  .metadata.labels["astronomer.io/project-id"] == $project_id
' <<<"${NS_JSON}" >/dev/null

if [[ -n "${PSA_ENFORCE_LEVEL}" ]]; then
  jq -e --arg value "${PSA_ENFORCE_LEVEL}" '.metadata.labels["pod-security.kubernetes.io/enforce"] == $value' <<<"${NS_JSON}" >/dev/null
fi
if [[ -n "${PSA_ENFORCE_VERSION}" ]]; then
  jq -e --arg value "${PSA_ENFORCE_VERSION}" '.metadata.labels["pod-security.kubernetes.io/enforce-version"] == $value' <<<"${NS_JSON}" >/dev/null
fi
if [[ -n "${PSA_AUDIT_LEVEL}" ]]; then
  jq -e --arg value "${PSA_AUDIT_LEVEL}" '.metadata.labels["pod-security.kubernetes.io/audit"] == $value' <<<"${NS_JSON}" >/dev/null
fi
if [[ -n "${PSA_AUDIT_VERSION}" ]]; then
  jq -e --arg value "${PSA_AUDIT_VERSION}" '.metadata.labels["pod-security.kubernetes.io/audit-version"] == $value' <<<"${NS_JSON}" >/dev/null
fi
if [[ -n "${PSA_WARN_LEVEL}" ]]; then
  jq -e --arg value "${PSA_WARN_LEVEL}" '.metadata.labels["pod-security.kubernetes.io/warn"] == $value' <<<"${NS_JSON}" >/dev/null
fi
if [[ -n "${PSA_WARN_VERSION}" ]]; then
  jq -e --arg value "${PSA_WARN_VERSION}" '.metadata.labels["pod-security.kubernetes.io/warn-version"] == $value' <<<"${NS_JSON}" >/dev/null
fi

jq -e '
  .spec.hard.cpu == "2" and
  .spec.hard.memory == "1Gi" and
  .spec.hard.pods == "10"
' <<<"${RQ_JSON}" >/dev/null

jq -e '
  .spec.limits[0].default.cpu == "500m" and
  .spec.limits[0].default.memory == "256Mi" and
  .spec.limits[0].defaultRequest.cpu == "250m" and
  .spec.limits[0].defaultRequest.memory == "128Mi"
' <<<"${LR_JSON}" >/dev/null

jq -e '.metadata.name == "astronomer-isolation"' <<<"${NP_JSON}" >/dev/null

jq -e --arg host "${REGISTRY_HOST}" --arg user "${REGISTRY_USERNAME}" --arg pass "${REGISTRY_PASSWORD}" '
  .type == "kubernetes.io/dockerconfigjson" and
  (.data[".dockerconfigjson"] | @base64d | fromjson | .auths[$host].username == $user) and
  (.data[".dockerconfigjson"] | @base64d | fromjson | .auths[$host].password == $pass)
' <<<"${SECRET_JSON}" >/dev/null

jq -e '
  any(.imagePullSecrets[]?; .name == "astronomer-registry")
' <<<"${SA_JSON}" >/dev/null

echo "Removing namespace from project and waiting for cleanup"
api \
  -H 'Content-Type: application/json' \
  -X POST \
  "${API_BASE}/projects/${PROJECT_ID}/remove-namespace/" \
  -d "{\"namespace\":\"${NAMESPACE}\"}" >/dev/null
REMOVE_COMPLETED=1

for i in $(seq 1 "${EVENT_TIMEOUT}"); do
  label_value="$(kubectl --context "${REMOTE_CONTEXT}" get namespace "${NAMESPACE}" -o jsonpath='{.metadata.labels.astronomer\.io/project-id}' 2>/dev/null || true)"
  sa_has_secret="$(
    kubectl --context "${REMOTE_CONTEXT}" get serviceaccount default -n "${NAMESPACE}" -o json 2>/dev/null |
      jq -r 'any(.imagePullSecrets[]?; .name == "astronomer-registry")'
  )"
  if [[ -z "${label_value}" ]] &&
    ! kubectl --context "${REMOTE_CONTEXT}" get resourcequota astronomer-quota -n "${NAMESPACE}" >/dev/null 2>&1 &&
    ! kubectl --context "${REMOTE_CONTEXT}" get limitrange astronomer-limits -n "${NAMESPACE}" >/dev/null 2>&1 &&
    ! kubectl --context "${REMOTE_CONTEXT}" get networkpolicy astronomer-isolation -n "${NAMESPACE}" >/dev/null 2>&1 &&
    ! kubectl --context "${REMOTE_CONTEXT}" get secret astronomer-registry -n "${NAMESPACE}" >/dev/null 2>&1 &&
    [[ "${sa_has_secret}" == "false" ]]; then
    break
  fi
  echo "cleanup-poll=${i} label=${label_value:-<empty>} sa_has_secret=${sa_has_secret:-<unknown>}"
  sleep 1
done

if kubectl --context "${REMOTE_CONTEXT}" get resourcequota astronomer-quota -n "${NAMESPACE}" >/dev/null 2>&1; then
  echo "resourcequota cleanup failed" >&2
  exit 1
fi
if kubectl --context "${REMOTE_CONTEXT}" get limitrange astronomer-limits -n "${NAMESPACE}" >/dev/null 2>&1; then
  echo "limitrange cleanup failed" >&2
  exit 1
fi
if kubectl --context "${REMOTE_CONTEXT}" get networkpolicy astronomer-isolation -n "${NAMESPACE}" >/dev/null 2>&1; then
  echo "networkpolicy cleanup failed" >&2
  exit 1
fi
if kubectl --context "${REMOTE_CONTEXT}" get secret astronomer-registry -n "${NAMESPACE}" >/dev/null 2>&1; then
  echo "registry secret cleanup failed" >&2
  exit 1
fi
if [[ -n "$(kubectl --context "${REMOTE_CONTEXT}" get namespace "${NAMESPACE}" -o jsonpath='{.metadata.labels.astronomer\.io/project-id}' 2>/dev/null || true)" ]]; then
  echo "project label cleanup failed" >&2
  exit 1
fi
kubectl --context "${REMOTE_CONTEXT}" get serviceaccount default -n "${NAMESPACE}" -o json |
  jq -e 'all(.imagePullSecrets[]?; .name != "astronomer-registry")' >/dev/null

echo "Deleting project"
api \
  -X DELETE \
  "${API_BASE}/projects/${PROJECT_ID}/" >/dev/null
PROJECT_DELETED=1

echo "Restoring validator-created config"
restore_registry_config

if [[ -n "${CREATED_TEMPLATE_ID}" ]]; then
  delete_created_template
  CREATED_TEMPLATE_ID=""
fi

fetch_audit_json() {
  if [[ -n "${AUDIT_CURSOR}" ]]; then
    api "${API_BASE}/audit/?since=${AUDIT_CURSOR}&limit=200"
  else
    api "${API_BASE}/audit/?limit=200"
  fi
}

audit_filter='
  [ .data[] | select(.resource_id == $project_id) | .action ] as $actions
  | ($actions | index("project.create"))
  and ($actions | index("project.add_namespace"))
  and ($actions | index("project.remove_namespace"))
  and ($actions | index("project.delete"))
  and any(.data[]; .resource_id == $project_id and .action == "project.add_namespace" and .detail.namespace == $ns)
  and any(.data[]; .resource_id == $project_id and .action == "project.remove_namespace" and .detail.namespace == $ns)
  and any(.data[]; .resource_type == "cluster" and .action == "cluster.update")
'
if [[ "${EXPECT_REGISTRY_DELETE_AUDIT}" -eq 1 ]]; then
  audit_filter+=' and any(.data[]; .resource_type == "cluster" and .action == "cluster.registry.delete")'
fi

echo "Waiting for audit rows"
for i in $(seq 1 "${EVENT_TIMEOUT}"); do
  AUDIT_JSON="$(fetch_audit_json)"
  if jq -e --arg project_id "${PROJECT_ID}" --arg ns "${NAMESPACE}" "${audit_filter}" <<<"${AUDIT_JSON}" >/dev/null; then
    break
  fi
  echo "audit-poll=${i}"
  sleep 1
done

AUDIT_JSON="$(fetch_audit_json)"
jq -e --arg project_id "${PROJECT_ID}" --arg ns "${NAMESPACE}" "${audit_filter}" <<<"${AUDIT_JSON}" >/dev/null

echo "Validated live project enforcement:"
echo "- ResourceQuota, LimitRange, NetworkPolicy, and PSA labels applied"
echo "- Registry pull secret propagated into namespace and default ServiceAccount"
echo "- Managed objects, project label, and pull secret cleaned up on remove-namespace"
echo "- Audit rows recorded for project lifecycle and registry mutations"
