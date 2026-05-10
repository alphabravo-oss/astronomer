#!/usr/bin/env bash
# Validate the live Dex path through Astronomer:
#   connector create -> apply -> Dex rollout -> mounted config update ->
#   Astronomer SSO redirect into the configured connector
#
# Usage:
#   ASTRO_USERNAME=admin ASTRO_PASSWORD=... ./scripts/validate-live-dex.sh
#   AUTH_TOKEN=... ./scripts/validate-live-dex.sh
#
# Optional env:
#   BASE_URL      default: http://astronomer.localtest.me:8080
#   MGMT_CONTEXT  default: k3d-astronomer-mgmt

set -euo pipefail

BASE_URL="${BASE_URL:-http://astronomer.localtest.me:8080}"
API_BASE="${BASE_URL%/}/api/v1"
MGMT_CONTEXT="${MGMT_CONTEXT:-k3d-astronomer-mgmt}"
AUTH_TOKEN="${AUTH_TOKEN:-}"
ASTRO_USERNAME="${ASTRO_USERNAME:-}"
ASTRO_PASSWORD="${ASTRO_PASSWORD:-}"
CONNECTOR_NAME="dex-verify-$(date +%s)"

cleanup() {
  if [[ -n "${AUTH_TOKEN}" ]]; then
    ids="$(
      curl -fsS -H "Authorization: Bearer ${AUTH_TOKEN}" "${API_BASE}/auth/dex/connectors/" |
        jq -r --arg name "${CONNECTOR_NAME}" '.data[] | select(.name == $name) | .id'
    )"
    for id in ${ids}; do
      curl -fsS -H "Authorization: Bearer ${AUTH_TOKEN}" -X DELETE \
        "${API_BASE}/auth/dex/connectors/${id}/" >/dev/null 2>&1 || true
    done
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

echo "Using connector=${CONNECTOR_NAME}"

# Keep the rendered Dex config deterministic for the validation by clearing any
# previously configured connectors first.
for id in $(
  curl -fsS -H "Authorization: Bearer ${AUTH_TOKEN}" "${API_BASE}/auth/dex/connectors/" |
    jq -r '.data[].id'
); do
  curl -fsS -H "Authorization: Bearer ${AUTH_TOKEN}" -X DELETE \
    "${API_BASE}/auth/dex/connectors/${id}/" >/dev/null
done

CREATE_BODY="$(
  jq -nc --arg name "${CONNECTOR_NAME}" '{
    type: "github",
    name: $name,
    display_name: $name,
    enabled: true,
    config: {
      clientID: "dummy-client",
      clientSecret: "dummy-secret"
    }
  }'
)"

curl -fsS \
  -H "Authorization: Bearer ${AUTH_TOKEN}" \
  -H 'Content-Type: application/json' \
  -X POST \
  "${API_BASE}/auth/dex/connectors/" \
  -d "${CREATE_BODY}" >/dev/null

APPLY_RESPONSE="$(
  curl -fsS \
    -H "Authorization: Bearer ${AUTH_TOKEN}" \
    -X POST \
    "${API_BASE}/auth/dex/apply/"
)"

kubectl --context "${MGMT_CONTEXT}" -n dex rollout status deploy/dex --timeout=120s >/dev/null

DEX_POD="$(
  kubectl --context "${MGMT_CONTEXT}" -n dex get pod -l app.kubernetes.io/name=dex -o json |
    jq -r '
      .items
      | map(select(
          .status.phase == "Running"
          and any(.status.conditions[]?; .type == "Ready" and .status == "True")
        ))
      | sort_by(.metadata.creationTimestamp)
      | last
      | .metadata.name // ""
    '
)"
if [[ -z "${DEX_POD}" ]]; then
  echo "no running Dex pod found after rollout" >&2
  exit 1
fi
MOUNTED_CFG="$(
  kubectl --context "${MGMT_CONTEXT}" -n dex exec "${DEX_POD}" -- cat /etc/astronomer-dex/config.yaml
)"
CM_CFG="$(
  kubectl --context "${MGMT_CONTEXT}" -n dex get configmap astronomer-dex-config \
    -o jsonpath='{.data.config\.yaml}'
)"

curl -fsS \
  -H "Authorization: Bearer ${AUTH_TOKEN}" \
  -H 'Content-Type: application/json' \
  -X POST \
  "${API_BASE}/auth/dex/register-as-sso/" \
  -d '{"client_id":"astronomer","client_secret":"astro-dex-shared-secret","display_name":"Sign in with Dex"}' >/dev/null

FIRST_LOCATION="$(
  curl -fsS -D - -o /dev/null "${API_BASE}/auth/login/dex/" |
    tr -d '\r' |
    awk '/^Location:/ {print $2}' |
    tail -n1
)"
SECOND_LOCATION="$(
  curl -fsS -D - -o /dev/null "${FIRST_LOCATION}" |
    tr -d '\r' |
    awk '/^Location:/ {print $2}' |
    tail -n1
)"

echo "apply_response=${APPLY_RESPONSE}"
echo "mounted_pod=${DEX_POD}"
echo "first_location=${FIRST_LOCATION}"
echo "second_location=${SECOND_LOCATION}"

grep -q "${CONNECTOR_NAME}" <<<"${CM_CFG}" || {
  echo "configmap did not contain ${CONNECTOR_NAME}" >&2
  exit 1
}
grep -q "${CONNECTOR_NAME}" <<<"${MOUNTED_CFG}" || {
  echo "mounted Dex config did not contain ${CONNECTOR_NAME}" >&2
  exit 1
}
if grep -q 'mockCallback' <<<"${MOUNTED_CFG}"; then
  echo "mounted Dex config still contains stale mock connector" >&2
  exit 1
fi
if [[ "${SECOND_LOCATION}" != *"/dex/auth/${CONNECTOR_NAME}"* ]]; then
  echo "unexpected connector redirect: ${SECOND_LOCATION}" >&2
  exit 1
fi
if [[ "${FIRST_LOCATION}" == *"/auth/auth/callback/dex"* ]]; then
  echo "unexpected duplicated auth callback path in redirect_uri: ${FIRST_LOCATION}" >&2
  exit 1
fi
if [[ "${FIRST_LOCATION}" != *"%2Fapi%2Fv1%2Fauth%2Fcallback%2Fdex"* ]]; then
  echo "unexpected callback path in redirect_uri: ${FIRST_LOCATION}" >&2
  exit 1
fi

echo "Validated Dex connector apply + rollout + SSO redirect against a live deployment"
