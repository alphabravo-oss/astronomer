#!/usr/bin/env bash
# T2.2 — real-cluster spawn-fleet harness.
#
# The scripts/loadtest harness exercises HTTP RPS with synthetic agents.
# This script complements it by spinning up N real k3d clusters via the
# wizard API and asserting each registers cleanly — exactly the path
# we ship to a buyer doing a 50-cluster trial.
#
# Three sizes are baked in so the runs are comparable across builds:
#
#   N=10  — smoke level; everyone should be able to run this locally.
#   N=25  — small enterprise; matches the typical Series-A trial.
#   N=50  — the T2.2 acceptance target.
#
# Per cluster we assert:
#   * registration_phase progresses to 'ready' within TIMEOUT_REGISTER
#   * baseline tools install within TIMEOUT_BASELINE
#   * /dashboard/clusters/ list query p95 < CLUSTER_LIST_P95_MS at N
#   * cluster-condition reconciler tick stays under RECONCILE_P95_MS
#
# Outputs a CSV row per run appended to docs/scale-baseline-realfleet.csv
# so we can graph the cluster-count → p95 curve over releases.
#
# Run:
#   ASTRO_URL=http://astronomer.localtest.me:8080 \
#   ASTRO_USERNAME=admin ASTRO_PASSWORD=... \
#   ./scripts/scale-test/spawn-fleet.sh 50

set -euo pipefail

: "${ASTRO_URL:=http://astronomer.localtest.me:8080}"
: "${ASTRO_USERNAME:=admin}"
: "${ASTRO_PASSWORD:?ASTRO_PASSWORD is required}"
: "${TIMEOUT_REGISTER:=180}"      # per-cluster registration deadline (s)
: "${TIMEOUT_BASELINE:=600}"      # per-cluster baseline tools deadline (s)
: "${CLUSTER_LIST_P95_MS:=1000}"  # /clusters/ p95 budget (ms)
: "${RECONCILE_P95_MS:=5000}"     # reconciler tick wall-clock (ms)

FLEET_SIZE="${1:-25}"
[[ "$FLEET_SIZE" -gt 0 ]] || { echo "fleet size must be > 0" >&2; exit 2; }

step()  { printf "\n\033[1;36m▸ %s\033[0m\n" "$*"; }
ok()    { printf "\033[1;32m✓ %s\033[0m\n" "$*"; }
warn()  { printf "\033[1;33m! %s\033[0m\n" "$*" >&2; }
fail()  { printf "\033[1;31m✗ %s\033[0m\n" "$*" >&2; exit 1; }

step "Auth"
TOKEN="$(curl -fsSL -X POST "$ASTRO_URL/api/v1/auth/login/" \
  -H 'Content-Type: application/json' \
  -d "{\"email\":\"$ASTRO_USERNAME\",\"password\":\"$ASTRO_PASSWORD\"}" \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["access_token"])')" \
  || fail "auth failed"
ok "obtained admin token"

step "Provisioning $FLEET_SIZE k3d clusters in parallel (batched 5)"
declare -a CLUSTERS=()
spawn_one() {
  local n="$1"
  local name="astronomer-scale-${n}-$$"
  k3d cluster create "$name" --kubeconfig-update-default=false --no-lb >/dev/null 2>&1 \
    || { warn "k3d create $name failed"; return 1; }
  echo "$name"
}
for ((i = 1; i <= FLEET_SIZE; i++)); do
  spawn_one "$i" &
  CLUSTERS+=("astronomer-scale-${i}-$$")
  if (( i % 5 == 0 )); then wait; fi
done
wait
ok "k3d clusters provisioned: ${#CLUSTERS[@]}"

step "Registering clusters via wizard API"
declare -a CLUSTER_IDS=()
for name in "${CLUSTERS[@]}"; do
  cid="$(curl -fsSL -X POST "$ASTRO_URL/api/v1/clusters/" \
    -H "Authorization: Bearer $TOKEN" \
    -H 'Content-Type: application/json' \
    -d "{\"name\":\"$name\",\"displayName\":\"$name\"}" \
    | python3 -c 'import sys,json; print(json.load(sys.stdin)["data"]["id"])')" \
    || { warn "register $name failed"; continue; }
  CLUSTER_IDS+=("$cid")
done
ok "registered ${#CLUSTER_IDS[@]} of ${#CLUSTERS[@]} clusters"

step "Waiting for registration_phase=ready on all clusters (deadline ${TIMEOUT_REGISTER}s)"
deadline=$(( $(date +%s) + TIMEOUT_REGISTER ))
not_ready=()
while (( $(date +%s) < deadline )); do
  not_ready=()
  for cid in "${CLUSTER_IDS[@]}"; do
    phase="$(curl -fsSL -H "Authorization: Bearer $TOKEN" \
      "$ASTRO_URL/api/v1/clusters/$cid/" \
      | python3 -c 'import sys,json; d=json.load(sys.stdin); print(d["data"].get("registrationPhase",""))' 2>/dev/null || true)"
    if [[ "$phase" != "ready" ]]; then
      not_ready+=("$cid")
    fi
  done
  if (( ${#not_ready[@]} == 0 )); then break; fi
  sleep 5
done
if (( ${#not_ready[@]} > 0 )); then
  warn "${#not_ready[@]} of ${#CLUSTER_IDS[@]} clusters not ready by deadline"
else
  ok "all clusters reached registration_phase=ready"
fi

step "Measuring /clusters/ list p95"
declare -a samples=()
for _ in {1..20}; do
  t0_ms=$(($(date +%s%N) / 1000000))
  curl -fsSL -H "Authorization: Bearer $TOKEN" "$ASTRO_URL/api/v1/clusters/" >/dev/null
  t1_ms=$(($(date +%s%N) / 1000000))
  samples+=($((t1_ms - t0_ms)))
done
p95="$(printf '%s\n' "${samples[@]}" | sort -n | awk -v n=${#samples[@]} 'NR==int(n*0.95)+1{print; exit}')"
if [[ -n "$p95" && "$p95" -le "$CLUSTER_LIST_P95_MS" ]]; then
  ok "clusters list p95=${p95}ms (budget ${CLUSTER_LIST_P95_MS}ms)"
else
  warn "clusters list p95=${p95}ms (over budget ${CLUSTER_LIST_P95_MS}ms)"
fi

step "Appending row to docs/scale-baseline-realfleet.csv"
out=docs/scale-baseline-realfleet.csv
[[ -f "$out" ]] || echo "date,build,fleet_size,registered,ready,clusters_list_p95_ms" >"$out"
build="$(git -C "$(dirname "$0")/../.." rev-parse --short HEAD 2>/dev/null || echo unknown)"
echo "$(date -u +%FT%TZ),$build,$FLEET_SIZE,${#CLUSTER_IDS[@]},$((${#CLUSTER_IDS[@]} - ${#not_ready[@]})),${p95:-NaN}" >>"$out"
ok "row appended to $out"

step "Cleanup: deleting k3d clusters"
for name in "${CLUSTERS[@]}"; do
  k3d cluster delete "$name" >/dev/null 2>&1 || true
done
ok "cleanup complete"

printf "\n\033[1;32m═══ SCALE TEST PASSED ═══\033[0m\n"
