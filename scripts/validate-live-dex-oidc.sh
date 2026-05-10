#!/usr/bin/env bash
# Validate the full live Dex -> external OIDC callback path through Astronomer:
#   configure Dex OIDC connector -> apply -> boot disposable Keycloak ->
#   start Astronomer SSO login -> complete IdP login -> verify final JWT redirect
#
# Usage:
#   ASTRO_USERNAME=admin ASTRO_PASSWORD=... ./scripts/validate-live-dex-oidc.sh
#   AUTH_TOKEN=... ./scripts/validate-live-dex-oidc.sh
#
# Optional env:
#   BASE_URL       default: http://astronomer.localtest.me:8080
#   MGMT_CONTEXT   default: k3d-astronomer-mgmt
#   KEYCLOAK_PORT  default: 8180

set -euo pipefail

BASE_URL="${BASE_URL:-http://astronomer.localtest.me:8080}"
API_BASE="${BASE_URL%/}/api/v1"
MGMT_CONTEXT="${MGMT_CONTEXT:-k3d-astronomer-mgmt}"
KEYCLOAK_PORT="${KEYCLOAK_PORT:-8180}"
AUTH_TOKEN="${AUTH_TOKEN:-}"
ASTRO_USERNAME="${ASTRO_USERNAME:-}"
ASTRO_PASSWORD="${ASTRO_PASSWORD:-}"
RUN_ID="$(date +%s)"
CONNECTOR_NAME="keycloak-live-${RUN_ID}"
KEYCLOAK_CONTAINER="astro-keycloak-verify-${RUN_ID}"
KEYCLOAK_USER="alice"
KEYCLOAK_PASSWORD="TempPass123456!"
PLATFORM_BASE=""
DEX_ISSUER=""
TMPDIR=""

cleanup() {
  docker rm -f "${KEYCLOAK_CONTAINER}" >/dev/null 2>&1 || true
  if [[ -n "${TMPDIR}" && -d "${TMPDIR}" ]]; then
    rm -rf "${TMPDIR}"
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
require docker

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

PLATFORM_BASE="$(
  curl -fsS "${API_BASE}/bootstrap/" |
    jq -r '.server_url // empty'
)"
if [[ -z "${PLATFORM_BASE}" ]]; then
  PLATFORM_BASE="${BASE_URL%/}"
fi

DEX_ISSUER="$(
  curl -fsS -H "Authorization: Bearer ${AUTH_TOKEN}" "${API_BASE}/auth/dex/settings/" |
    jq -r '.data.issuer_url'
)"
if [[ -z "${DEX_ISSUER}" || "${DEX_ISSUER}" == "null" ]]; then
  echo "Dex settings are not configured" >&2
  exit 1
fi
DEX_CALLBACK="${DEX_ISSUER%/}/callback"

echo "Using platform_base=${PLATFORM_BASE}"
echo "Using dex_issuer=${DEX_ISSUER}"

TMPDIR="$(mktemp -d)"
cat > "${TMPDIR}/realm.json" <<JSON
{
  "realm": "astronomer",
  "enabled": true,
  "sslRequired": "none",
  "registrationAllowed": false,
  "clients": [
    {
      "clientId": "dex-oidc",
      "name": "Dex OIDC",
      "enabled": true,
      "protocol": "openid-connect",
      "publicClient": false,
      "secret": "dex-secret",
      "redirectUris": ["${DEX_CALLBACK}"],
      "standardFlowEnabled": true,
      "directAccessGrantsEnabled": true,
      "fullScopeAllowed": true,
      "protocolMappers": [
        {
          "name": "email",
          "protocol": "openid-connect",
          "protocolMapper": "oidc-usermodel-property-mapper",
          "consentRequired": false,
          "config": {
            "userinfo.token.claim": "true",
            "user.attribute": "email",
            "id.token.claim": "true",
            "access.token.claim": "true",
            "claim.name": "email",
            "jsonType.label": "String"
          }
        }
      ]
    }
  ],
  "users": [
    {
      "username": "${KEYCLOAK_USER}",
      "enabled": true,
      "email": "alice@example.com",
      "firstName": "Alice",
      "lastName": "Astronomer",
      "emailVerified": true,
      "credentials": [
        {
          "type": "password",
          "value": "${KEYCLOAK_PASSWORD}",
          "temporary": false
        }
      ]
    }
  ]
}
JSON

docker rm -f "${KEYCLOAK_CONTAINER}" >/dev/null 2>&1 || true
docker run -d --name "${KEYCLOAK_CONTAINER}" -p "${KEYCLOAK_PORT}:8080" \
  -e KEYCLOAK_ADMIN=admin \
  -e KEYCLOAK_ADMIN_PASSWORD=adminpass \
  -v "${TMPDIR}/realm.json:/opt/keycloak/data/import/realm.json:ro" \
  quay.io/keycloak/keycloak:26.1.2 \
  start-dev --http-port=8080 --hostname="http://host.k3d.internal:${KEYCLOAK_PORT}" --import-realm >/dev/null

for i in $(seq 1 60); do
  if curl -fsS --resolve "host.k3d.internal:${KEYCLOAK_PORT}:127.0.0.1" \
    "http://host.k3d.internal:${KEYCLOAK_PORT}/realms/astronomer/.well-known/openid-configuration" >/dev/null 2>&1; then
    break
  fi
  sleep 2
done

# Keep the Dex connector set deterministic for this validation.
for id in $(
  curl -fsS -H "Authorization: Bearer ${AUTH_TOKEN}" "${API_BASE}/auth/dex/connectors/" |
    jq -r '.data[].id'
); do
  curl -fsS -H "Authorization: Bearer ${AUTH_TOKEN}" -X DELETE \
    "${API_BASE}/auth/dex/connectors/${id}/" >/dev/null
done

CREATE_BODY="$(
  jq -nc --arg name "${CONNECTOR_NAME}" --arg port "${KEYCLOAK_PORT}" '{
    type: "oidc",
    name: $name,
    display_name: $name,
    enabled: true,
    config: {
      issuer: ("http://host.k3d.internal:" + $port + "/realms/astronomer"),
      clientID: "dex-oidc",
      clientSecret: "dex-secret",
      scopes: ["openid", "profile", "email"]
    }
  }'
)"

curl -fsS \
  -H "Authorization: Bearer ${AUTH_TOKEN}" \
  -H 'Content-Type: application/json' \
  -X POST \
  "${API_BASE}/auth/dex/connectors/" \
  -d "${CREATE_BODY}" >/dev/null

curl -fsS \
  -H "Authorization: Bearer ${AUTH_TOKEN}" \
  -H 'Content-Type: application/json' \
  -X POST \
  "${API_BASE}/auth/dex/register-as-sso/" \
  -d '{"client_id":"astronomer","client_secret":"astro-dex-shared-secret","display_name":"Sign in with Dex"}' >/dev/null

curl -fsS \
  -H "Authorization: Bearer ${AUTH_TOKEN}" \
  -X POST \
  "${API_BASE}/auth/dex/apply/" >/dev/null

kubectl --context "${MGMT_CONTEXT}" -n dex rollout status deploy/dex --timeout=120s >/dev/null

COOKIE_JAR="$(mktemp)"
HDR1="$(mktemp)"
curl -sS -D "${HDR1}" -o /dev/null -c "${COOKIE_JAR}" "${PLATFORM_BASE%/}/api/v1/auth/login/dex/"
L1="$(awk '/^Location:/ {print $2}' "${HDR1}" | tr -d '\r' | tail -n1)"

HDR2="$(mktemp)"
curl -sS -D "${HDR2}" -o /dev/null -b "${COOKIE_JAR}" "${L1}"
L2="$(awk '/^Location:/ {print $2}' "${HDR2}" | tr -d '\r' | tail -n1)"
if [[ "${L2}" == /* ]]; then
  dex_base="$(python3 - <<'PY' "${L1}"
import sys, urllib.parse
u = urllib.parse.urlparse(sys.argv[1])
print(f"{u.scheme}://{u.netloc}")
PY
)"
  L2="${dex_base}${L2}"
fi

HDR3="$(mktemp)"
curl -sS -D "${HDR3}" -o /dev/null -b "${COOKIE_JAR}" "${L2}"
L3="$(awk '/^Location:/ {print $2}' "${HDR3}" | tr -d '\r' | tail -n1)"

KEYCLOAK_LOGIN_HTML="$(mktemp)"
curl -sS --resolve "host.k3d.internal:${KEYCLOAK_PORT}:127.0.0.1" \
  -o "${KEYCLOAK_LOGIN_HTML}" -b "${COOKIE_JAR}" -c "${COOKIE_JAR}" "${L3}" >/dev/null
FORM_ACTION="$(
  grep -o 'action="[^"]*"' "${KEYCLOAK_LOGIN_HTML}" | head -n1 | sed 's/^action="//; s/"$//'
)"
FORM_ACTION="$(printf '%s' "${FORM_ACTION}" | sed 's/&amp;/\&/g')"
if [[ "${FORM_ACTION}" != http* ]]; then
  FORM_ACTION="http://host.k3d.internal:${KEYCLOAK_PORT}${FORM_ACTION}"
fi

HDR5="$(mktemp)"
curl -sS --resolve "host.k3d.internal:${KEYCLOAK_PORT}:127.0.0.1" \
  -D "${HDR5}" -o /dev/null -b "${COOKIE_JAR}" -c "${COOKIE_JAR}" \
  -e "${L3}" -X POST "${FORM_ACTION}" \
  --data-urlencode "username=${KEYCLOAK_USER}" \
  --data-urlencode "password=${KEYCLOAK_PASSWORD}" \
  --data 'credentialId='
L5="$(awk '/^Location:/ {print $2}' "${HDR5}" | tr -d '\r' | tail -n1)"

HDR6="$(mktemp)"
curl -sS -D "${HDR6}" -o /dev/null -b "${COOKIE_JAR}" -c "${COOKIE_JAR}" "${L5}"
L6="$(awk '/^Location:/ {print $2}' "${HDR6}" | tr -d '\r' | tail -n1)"

HDR7="$(mktemp)"
curl -sS -D "${HDR7}" -o /dev/null -b "${COOKIE_JAR}" -c "${COOKIE_JAR}" "${L6}"
FINAL_LOCATION="$(awk '/^Location:/ {print $2}' "${HDR7}" | tr -d '\r' | tail -n1)"

echo "platform_base=${PLATFORM_BASE}"
echo "dex_issuer=${DEX_ISSUER}"
echo "keycloak_callback=${DEX_CALLBACK}"
echo "final_location=${FINAL_LOCATION}"

if [[ "${FINAL_LOCATION}" != *"provider=dex"* ]]; then
  echo "final redirect missing provider=dex: ${FINAL_LOCATION}" >&2
  exit 1
fi
if [[ "${FINAL_LOCATION}" != *"token="* ]]; then
  echo "final redirect missing access token: ${FINAL_LOCATION}" >&2
  exit 1
fi
if [[ "${FINAL_LOCATION}" != *"refresh="* ]]; then
  echo "final redirect missing refresh token: ${FINAL_LOCATION}" >&2
  exit 1
fi

echo "Validated full Dex -> external OIDC callback through Astronomer"
