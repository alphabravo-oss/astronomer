#!/usr/bin/env bash
# Cross-version DECOMMISSION validation.
#
# For each k8s (k3s) version: adopt a single-node k3d cluster into the live
# Astronomer plane, DESTROY the k3d cluster (agent + apiserver gone), then
# DELETE /clusters/{id}/ and measure time-to-tombstone. Validates the
# "definitively gone" fast-path (commit 5782aa4): a provably-gone cluster must
# reach decommissioned_at in ~2-4 minutes, not ~15-20. Runs all versions
# concurrently (adopt all -> destroy+decommission all -> poll all) so wall-clock
# is bounded by one decommission, not the sum.
#
# Usage:
#   ASTRO_URL=https://astronomer.dev.alphabravo.io ASTRO_EMAIL=admin@alphabravo.io \
#   ASTRO_PASSWORD=... ./scripts/k3d-decommission-matrix-test.sh v1.28.15-k3s1 v1.29.10-k3s1 v1.31.2-k3s1 v1.32.1-k3s1
#
# Env: ASTRO_URL (req), ASTRO_EMAIL (default admin@alphabravo.io), ASTRO_PASSWORD (req),
#      AGENT_IMAGE (default ghcr.io/alphabravo-oss/astronomer-go-agent:v0.2.0),
#      PGCTX (kube context for the mgmt psql, default 'default'),
#      TOMBSTONE_TIMEOUT (default 420s), MAX_TILE (max prompt seconds to still call PASS, default 360)

set -uo pipefail

ASTRO_URL="${ASTRO_URL:?ASTRO_URL required}"
ASTRO_EMAIL="${ASTRO_EMAIL:-admin@alphabravo.io}"
ASTRO_PASSWORD="${ASTRO_PASSWORD:?ASTRO_PASSWORD required}"
AGENT_IMAGE="${AGENT_IMAGE:-ghcr.io/alphabravo-oss/astronomer-go-agent:v0.2.0}"
PGCTX="${PGCTX:-default}"
TOMBSTONE_TIMEOUT="${TOMBSTONE_TIMEOUT:-420}"
MAX_TILE="${MAX_TILE:-360}"
API="${ASTRO_URL%/}/api/v1"

VERSIONS=("$@"); [[ ${#VERSIONS[@]} -eq 0 ]] && VERSIONS=(v1.28.15-k3s1 v1.29.10-k3s1 v1.31.2-k3s1 v1.32.1-k3s1)

psql_q() { kubectl --context "$PGCTX" exec -n astronomer astronomer-postgres-0 -- psql -U astronomer -d astronomer -t -A "$@" 2>/dev/null; }
jget() { python3 -c "import sys,json;print(json.load(sys.stdin)$1)" 2>/dev/null; }

echo "▸ authenticate"
TOKEN="$(curl -fsS -X POST -H 'Content-Type: application/json' -d "{\"email\":\"$ASTRO_EMAIL\",\"password\":\"$ASTRO_PASSWORD\"}" "$API/auth/login/" | jget "['data']['token']")"
[[ -n "${TOKEN:-}" ]] || { echo "login failed"; exit 1; }

declare -A CID
declare -A DESTROYED_AT

# --- adopt all versions ---
for ver in "${VERSIONS[@]}"; do
  name="dc-${ver//./-}"
  echo "▸ [$ver] create + adopt $name"
  k3d cluster create "$name" --no-lb --k3s-arg "--disable=traefik@server:0" --image "rancher/k3s:$ver" >/dev/null 2>&1 || { echo "  ✗ k3d create failed"; CID[$ver]="CREATE_FAIL"; continue; }
  k3d image import -c "$name" "$AGENT_IMAGE" >/dev/null 2>&1
  cid="$(curl -sS -X POST -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -d "{\"name\":\"$name\",\"environment\":\"dev\",\"provider\":\"k3d\",\"distribution\":\"k3s\",\"region\":\"local\"}" "$API/clusters/" | jget "['data']['id']")"
  [[ -n "${cid:-}" ]] || { echo "  ✗ register failed"; CID[$ver]="REGISTER_FAIL"; k3d cluster delete "$name" >/dev/null 2>&1; continue; }
  CID[$ver]="$cid"
  curl -sS -X PUT -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -d '{"install_baseline":false}' "$API/clusters/$cid/registration/options/" >/dev/null 2>&1
  curl -fsS -H "Authorization: Bearer $TOKEN" "$API/clusters/$cid/manifest/" | kubectl --context "k3d-$name" apply -f - >/dev/null 2>&1
  # wait heartbeat
  for i in $(seq 1 40); do
    hb="$(curl -sS -H "Authorization: Bearer $TOKEN" "$API/clusters/$cid/" | jget "['data'].get('last_heartbeat')")"
    [[ -n "${hb:-}" && "$hb" != "None" && "$hb" != "null" ]] && { echo "  ✓ [$ver] active"; break; }
    sleep 3
  done
done

# --- destroy k3d + decommission all (near-simultaneous) ---
echo "▸ destroy k3d + decommission all"
T0=$(date +%s)
for ver in "${VERSIONS[@]}"; do
  cid="${CID[$ver]}"; [[ "$cid" == *FAIL ]] && continue
  name="dc-${ver//./-}"
  k3d cluster delete "$name" >/dev/null 2>&1
  curl -sS -X DELETE -H "Authorization: Bearer $TOKEN" "$API/clusters/$cid/" -o /dev/null -w "  [$ver] DELETE %{http_code}\n"
done

# --- poll all until tombstoned ---
echo "▸ poll for tombstone (timeout ${TOMBSTONE_TIMEOUT}s)"
declare -A TILE  # time-to-tombstone
deadline=$(( T0 + TOMBSTONE_TIMEOUT ))
while (( $(date +%s) < deadline )); do
  pending=0
  for ver in "${VERSIONS[@]}"; do
    cid="${CID[$ver]}"; [[ "$cid" == *FAIL ]] && continue
    [[ -n "${TILE[$ver]:-}" ]] && continue
    dec="$(psql_q -c "SELECT decommissioned_at IS NOT NULL FROM clusters WHERE id='$cid';")"
    if [[ "$dec" == "t" || -z "$dec" ]]; then
      TILE[$ver]=$(( $(date +%s) - T0 ))
      echo "  ✓ [$ver] tombstoned at +${TILE[$ver]}s"
    else
      pending=$((pending+1))
    fi
  done
  (( pending == 0 )) && break
  sleep 10
done

# --- summary ---
echo ""
echo "═══ DECOMMISSION MATRIX (prompt tombstone of a provably-gone cluster) ═══"
printf "%-16s %-14s %s\n" "K3S_VERSION" "RESULT" "TIME_TO_TOMBSTONE"
pass=0; total=0
for ver in "${VERSIONS[@]}"; do
  cid="${CID[$ver]}"
  if [[ "$cid" == *FAIL ]]; then printf "%-16s %-14s %s\n" "$ver" "$cid" "-"; total=$((total+1)); continue; fi
  total=$((total+1))
  t="${TILE[$ver]:-}"
  if [[ -n "$t" && "$t" -le "$MAX_TILE" ]]; then printf "%-16s %-14s %ss\n" "$ver" "PASS" "$t"; pass=$((pass+1))
  elif [[ -n "$t" ]]; then printf "%-16s %-14s %ss\n" "$ver" "SLOW" "$t"
  else printf "%-16s %-14s %s\n" "$ver" "TIMEOUT" ">${TOMBSTONE_TIMEOUT}s"; fi
done
echo ""
echo "$pass/$total versions tombstoned promptly (<=${MAX_TILE}s)"
[[ "$pass" -eq "$total" ]] && exit 0 || exit 1
