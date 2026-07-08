#!/usr/bin/env bash
# Cross-version adoption matrix test.
#
# Spins up N single-node k3d clusters, each on a DIFFERENT Kubernetes (k3s)
# version, adopts each into the live Astronomer management plane via the real
# agent-manifest flow, and asserts the version-sensitive core works on every
# version: agent connects (heartbeat), and the k8s passthrough proxy can list
# namespaces / nodes / pods and read /version. Baseline tools, image scans, and
# the kubectl shell are intentionally skipped — this is a fast parity check of
# agent connectivity + proxy across k8s versions, not the full onboarding smoke
# (see smoke-fresh-cluster.sh for that).
#
# Usage:
#   ASTRO_URL=http://astronomer.5.78.101.247.nip.io:8080 \
#   ASTRO_USERNAME=admin ASTRO_PASSWORD=... \
#   AGENT_IMAGE=astronomer-go-agent:dev \
#   ./scripts/k3d-version-matrix-test.sh v1.28.15-k3s1 v1.29.10-k3s1 v1.30.6-k3s1 v1.31.2-k3s1
#
# Env:
#   ASTRO_URL        management URL (required)
#   ASTRO_USERNAME   admin user (default: admin)
#   ASTRO_PASSWORD   admin password (required)
#   AGENT_IMAGE      agent image to import into each k3d cluster (default: astronomer-go-agent:dev)
#   MGMT_NETWORK     docker network the mgmt plane is on, joined so the agent can
#                    reach ASTRO_URL (default: none; server-derived URL usually works)
#   KEEP             set to 1 to leave clusters behind on success
#   AGENT_TIMEOUT    seconds to wait for agent heartbeat (default: 120)

set -uo pipefail

ASTRO_URL="${ASTRO_URL:?ASTRO_URL is required}"
ASTRO_EMAIL="${ASTRO_EMAIL:-admin@alphabravo.io}"
ASTRO_PASSWORD="${ASTRO_PASSWORD:?ASTRO_PASSWORD is required}"
AGENT_IMAGE="${AGENT_IMAGE:-ghcr.io/alphabravo-oss/astronomer-go-agent:v0.2.0}"
MGMT_NETWORK="${MGMT_NETWORK:-}"
KEEP="${KEEP:-0}"
AGENT_TIMEOUT="${AGENT_TIMEOUT:-120}"

VERSIONS=("$@")
if [[ ${#VERSIONS[@]} -eq 0 ]]; then
  VERSIONS=(v1.28.15-k3s1 v1.29.10-k3s1 v1.30.6-k3s1 v1.31.2-k3s1)
fi

step() { printf "\n\033[1;36m▸ %s\033[0m\n" "$*"; }
ok()   { printf "\033[1;32m  ✓ %s\033[0m\n" "$*"; }
bad()  { printf "\033[1;31m  ✗ %s\033[0m\n" "$*"; }

jget() { python3 -c "import sys,json; print(json.load(sys.stdin)$1)" 2>/dev/null; }

api() {
  local method="$1"; shift; local path="$1"; shift
  curl -sS -X "$method" -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" "$ASTRO_URL$path" "$@"
}

# --- auth ---
step "Authenticate to $ASTRO_URL"
TOKEN="$(curl -fsS -X POST -H 'Content-Type: application/json' \
  -d "{\"email\":\"$ASTRO_EMAIL\",\"password\":\"$ASTRO_PASSWORD\"}" \
  "$ASTRO_URL/api/v1/auth/login/" | jget "['data']['token']")"
[[ -n "${TOKEN:-}" ]] || { bad "login failed"; exit 1; }
ok "authenticated as $ASTRO_EMAIL"

NET_ARG=""
if [[ -n "$MGMT_NETWORK" ]] && docker network inspect "$MGMT_NETWORK" >/dev/null 2>&1; then
  NET_ARG="--network $MGMT_NETWORK"
fi

declare -a RESULTS

test_version() {
  local ver="$1"
  local name="astro-ver-${ver//./-}"
  local cid="" hb="" phase="" ns=0 nodes=0 pods=0 kver=""
  local result="FAIL"

  step "[$ver] create single-node k3d cluster $name"
  if ! k3d cluster create "$name" --no-lb --k3s-arg "--disable=traefik@server:0" \
        $NET_ARG --image "rancher/k3s:$ver" >/dev/null 2>&1; then
    bad "[$ver] k3d create failed (image rancher/k3s:$ver may not exist)"
    RESULTS+=("$ver|CREATE_FAIL|-|-|-|-|-")
    return
  fi
  ok "[$ver] cluster up"

  step "[$ver] import agent image"
  k3d image import -c "$name" "$AGENT_IMAGE" >/dev/null 2>&1 && ok "imported $AGENT_IMAGE" || bad "image import failed"

  step "[$ver] register via wizard API"
  cid="$(api POST /api/v1/clusters/ -d "{\"name\":\"$name\",\"display_name\":\"ver $ver\",\"environment\":\"dev\",\"provider\":\"k3d\",\"distribution\":\"k3s\",\"region\":\"local\"}" | jget "['data']['id']")"
  if [[ -z "${cid:-}" ]]; then bad "[$ver] cluster create API failed"; RESULTS+=("$ver|REGISTER_FAIL|-|-|-|-|-"); teardown "$name" ""; return; fi
  ok "[$ver] cluster_id=$cid"
  # No baseline: keep the test fast + version-focused.
  api PUT "/api/v1/clusters/$cid/registration/options/" -d '{"install_baseline":false}' >/dev/null 2>&1

  step "[$ver] fetch + apply agent manifest"
  local mf; mf="$(mktemp)"
  curl -fsS -H "Authorization: Bearer $TOKEN" "$ASTRO_URL/api/v1/clusters/$cid/manifest/" > "$mf" 2>/dev/null
  if ! grep -q . "$mf"; then bad "[$ver] manifest empty"; rm -f "$mf"; RESULTS+=("$ver|MANIFEST_FAIL|$cid|-|-|-|-"); teardown "$name" "$cid"; return; fi
  kubectl --context "k3d-$name" apply -f "$mf" >/dev/null 2>&1 && ok "manifest applied ($(wc -l <"$mf") lines)" || bad "apply failed"
  rm -f "$mf"

  step "[$ver] wait for agent heartbeat (<=${AGENT_TIMEOUT}s)"
  local deadline=$(( $(date +%s) + AGENT_TIMEOUT ))
  while (( $(date +%s) < deadline )); do
    hb="$(api GET "/api/v1/clusters/$cid/" | jget "['data']['last_heartbeat']")"
    [[ -n "${hb:-}" && "$hb" != "None" && "$hb" != "null" ]] && break
    sleep 4
  done
  if [[ -z "${hb:-}" || "$hb" == "None" || "$hb" == "null" ]]; then
    bad "[$ver] agent never connected"; RESULTS+=("$ver|NO_HEARTBEAT|$cid|-|-|-|-"); teardown "$name" "$cid"; return
  fi
  ok "[$ver] agent heartbeat: $hb"
  api POST "/api/v1/clusters/$cid/registration/confirm/" -d '{}' >/dev/null 2>&1

  step "[$ver] exercise k8s passthrough proxy"
  ns="$(api GET "/api/v1/clusters/$cid/k8s/api/v1/namespaces" | jget "['items'].__len__()")"; ns="${ns:-0}"
  nodes="$(api GET "/api/v1/clusters/$cid/k8s/api/v1/nodes" | jget "['items'].__len__()")"; nodes="${nodes:-0}"
  pods="$(api GET "/api/v1/clusters/$cid/k8s/api/v1/namespaces/kube-system/pods" | jget "['items'].__len__()")"; pods="${pods:-0}"
  kver="$(api GET "/api/v1/clusters/$cid/k8s/version" | jget "['gitVersion']")"
  echo "  namespaces=$ns nodes=$nodes kube-system-pods=$pods reported-version=${kver:-?}"
  if [[ "$ns" -ge 4 && "$nodes" -ge 1 && "$pods" -ge 1 ]]; then
    result="PASS"; ok "[$ver] proxy OK"
  else
    bad "[$ver] proxy returned too few objects"
  fi
  RESULTS+=("$ver|$result|$cid|$ns|$nodes|$pods|${kver:-?}")
  teardown "$name" "$cid"
}

teardown() {
  local name="$1" cid="$2"
  if [[ "$KEEP" == "1" ]]; then echo "  (KEEP=1) leaving $name / $cid"; return; fi
  [[ -n "$cid" ]] && api DELETE "/api/v1/clusters/$cid/" >/dev/null 2>&1
  k3d cluster delete "$name" >/dev/null 2>&1 || true
}

for v in "${VERSIONS[@]}"; do test_version "$v"; done

# --- summary ---
printf "\n\033[1;36m═══ CROSS-VERSION ADOPTION MATRIX ═══\033[0m\n"
printf "%-16s %-14s %-4s %-5s %-5s %s\n" "K3S_VERSION" "RESULT" "NS" "NODE" "PODS" "REPORTED"
pass=0; total=0
for r in "${RESULTS[@]}"; do
  IFS='|' read -r ver res cid ns nodes pods kver <<<"$r"
  printf "%-16s %-14s %-4s %-5s %-5s %s\n" "$ver" "$res" "$ns" "$nodes" "$pods" "$kver"
  total=$((total+1)); [[ "$res" == "PASS" ]] && pass=$((pass+1))
done
printf "\n%d/%d versions PASSED\n" "$pass" "$total"
[[ "$pass" -eq "$total" ]] && exit 0 || exit 1
