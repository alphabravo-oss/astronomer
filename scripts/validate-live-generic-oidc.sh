#!/usr/bin/env bash
# Validate the full live Astronomer -> external OIDC callback path without Dex:
#   create generic OIDC provider via /settings/sso -> boot disposable Keycloak ->
#   start Astronomer SSO login -> complete IdP login -> verify final JWT redirect
#
# Usage:
#   ASTRO_USERNAME=admin ASTRO_PASSWORD=... ./scripts/validate-live-generic-oidc.sh
#   AUTH_TOKEN=... ./scripts/validate-live-generic-oidc.sh
#
# Optional env:
#   BASE_URL       default: http://astronomer.localtest.me:8080
#   KEYCLOAK_PORT  default: 8181

set -euo pipefail

BASE_URL="${BASE_URL:-http://astronomer.localtest.me:8080}"
API_BASE="${BASE_URL%/}/api/v1"
KEYCLOAK_PORT="${KEYCLOAK_PORT:-8181}"
AUTH_TOKEN="${AUTH_TOKEN:-}"
ASTRO_USERNAME="${ASTRO_USERNAME:-}"
ASTRO_PASSWORD="${ASTRO_PASSWORD:-}"
RUN_ID="$(date +%s)"
PROVIDER_NAME="Corporate OIDC ${RUN_ID}"
PROVIDER_ID=""
PROVIDER_KEY=""
KEYCLOAK_CONTAINER="astro-generic-oidc-${RUN_ID}"
KEYCLOAK_USER="alice"
KEYCLOAK_PASSWORD="TempPass123456!"
PLATFORM_BASE=""
TMPDIR=""
COOKIE_JAR=""

cleanup() {
  if [[ -n "${PROVIDER_ID}" && -n "${AUTH_TOKEN}" ]]; then
    curl -fsS \
      -H "Authorization: Bearer ${AUTH_TOKEN}" \
      -X DELETE \
      "${API_BASE}/settings/sso/${PROVIDER_ID}/" >/dev/null 2>&1 || true
  fi
  docker rm -f "${KEYCLOAK_CONTAINER}" >/dev/null 2>&1 || true
  if [[ -n "${COOKIE_JAR}" && -f "${COOKIE_JAR}" ]]; then
    rm -f "${COOKIE_JAR}"
  fi
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
require docker
require python3

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
if [[ -z "${PLATFORM_BASE}" || "${PLATFORM_BASE}" == "null" ]]; then
  PLATFORM_BASE="${BASE_URL%/}"
fi

TMPDIR="$(mktemp -d)"
cat > "${TMPDIR}/realm.json" <<JSON
{
  "realm": "astronomer",
  "enabled": true,
  "sslRequired": "none",
  "registrationAllowed": false,
  "clients": [
    {
      "clientId": "astronomer-oidc",
      "name": "Astronomer Direct OIDC",
      "enabled": true,
      "protocol": "openid-connect",
      "publicClient": false,
      "secret": "astronomer-oidc-secret",
      "redirectUris": ["${PLATFORM_BASE%/}/api/v1/auth/callback/*"],
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

CREATE_BODY="$(
  jq -nc --arg name "${PROVIDER_NAME}" --arg port "${KEYCLOAK_PORT}" '{
    type: "oidc",
    name: $name,
    enabled: true,
    config: {
      client_id: "astronomer-oidc",
      client_secret: "astronomer-oidc-secret",
      metadata_url: ("http://host.k3d.internal:" + $port + "/realms/astronomer/.well-known/openid-configuration"),
      auto_create_users: true
    }
  }'
)"

CREATE_RESP="$(
  curl -fsS \
    -H "Authorization: Bearer ${AUTH_TOKEN}" \
    -H 'Content-Type: application/json' \
    -X POST \
    "${API_BASE}/settings/sso/" \
    -d "${CREATE_BODY}"
)"

PROVIDER_ID="$(printf '%s' "${CREATE_RESP}" | jq -r '.data.id')"
PROVIDER_KEY="$(printf '%s' "${CREATE_RESP}" | jq -r '.data.provider')"
if [[ -z "${PROVIDER_ID}" || "${PROVIDER_ID}" == "null" || -z "${PROVIDER_KEY}" || "${PROVIDER_KEY}" == "null" ]]; then
  echo "failed to create generic OIDC provider: ${CREATE_RESP}" >&2
  exit 1
fi

PUBLIC_LIST="$(
  curl -fsS "${API_BASE}/settings/sso/"
)"
if ! printf '%s' "${PUBLIC_LIST}" | jq -e --arg key "${PROVIDER_KEY}" '.data[] | select(.provider == $key)' >/dev/null; then
  echo "public SSO provider list did not include ${PROVIDER_KEY}" >&2
  exit 1
fi

COOKIE_JAR="$(mktemp)"
HDR1="$(mktemp)"
curl -sS -D "${HDR1}" -o /dev/null -c "${COOKIE_JAR}" "${PLATFORM_BASE%/}/api/v1/auth/login/${PROVIDER_KEY}/"
L1="$(awk '/^Location:/ {print $2}' "${HDR1}" | tr -d '\r' | tail -n1)"

HDR2="$(mktemp)"
curl -sS --resolve "host.k3d.internal:${KEYCLOAK_PORT}:127.0.0.1" \
  -D "${HDR2}" -o /dev/null -b "${COOKIE_JAR}" -c "${COOKIE_JAR}" "${L1}"
L2="$(awk '/^Location:/ {print $2}' "${HDR2}" | tr -d '\r' | tail -n1)"

KEYCLOAK_LOGIN_HTML="$(mktemp)"
curl -sS --resolve "host.k3d.internal:${KEYCLOAK_PORT}:127.0.0.1" \
  -o "${KEYCLOAK_LOGIN_HTML}" -b "${COOKIE_JAR}" -c "${COOKIE_JAR}" "${L2}" >/dev/null
FORM_ACTION="$(
  grep -o 'action="[^"]*"' "${KEYCLOAK_LOGIN_HTML}" | head -n1 | sed 's/^action="//; s/"$//'
)"
FORM_ACTION="$(printf '%s' "${FORM_ACTION}" | sed 's/&amp;/\&/g')"
if [[ "${FORM_ACTION}" != http* ]]; then
  FORM_ACTION="http://host.k3d.internal:${KEYCLOAK_PORT}${FORM_ACTION}"
fi

HDR3="$(mktemp)"
curl -sS --resolve "host.k3d.internal:${KEYCLOAK_PORT}:127.0.0.1" \
  -D "${HDR3}" -o /dev/null -b "${COOKIE_JAR}" -c "${COOKIE_JAR}" \
  -e "${L2}" -X POST "${FORM_ACTION}" \
  --data-urlencode "username=${KEYCLOAK_USER}" \
  --data-urlencode "password=${KEYCLOAK_PASSWORD}" \
  --data 'credentialId='
L3="$(awk '/^Location:/ {print $2}' "${HDR3}" | tr -d '\r' | tail -n1)"

HDR4="$(mktemp)"
curl -sS -D "${HDR4}" -o /dev/null -b "${COOKIE_JAR}" -c "${COOKIE_JAR}" "${L3}"
FINAL_LOCATION="$(awk '/^Location:/ {print $2}' "${HDR4}" | tr -d '\r' | tail -n1)"

echo "platform_base=${PLATFORM_BASE}"
echo "provider_key=${PROVIDER_KEY}"
echo "final_location=${FINAL_LOCATION}"

if [[ "${FINAL_LOCATION}" != *"provider=${PROVIDER_KEY}"* ]]; then
  echo "final redirect missing provider=${PROVIDER_KEY}: ${FINAL_LOCATION}" >&2
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

echo "Validated full direct generic OIDC callback through Astronomer"
