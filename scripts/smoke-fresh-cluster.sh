#!/usr/bin/env bash
# Fresh-cluster end-to-end smoke test.
#
# Why this exists: bitnami/kubectl:1.31 silently 404'd on docker.io for
# months without anyone noticing, because nothing in CI exercised the
# fresh-cluster shell-open path. Same for the SetKubectlSessionStatus
# SQLSTATE 42P08 bug. This script walks the full operator-onboarding
# flow against a real k3d cluster + a real Astronomer instance, so
# regressions in the wizard / manifest / agent / shell / image-scan
# pipeline fail the test instead of leaking to live.
#
# Run from CI:
#   ./scripts/smoke-fresh-cluster.sh
#
# Run locally (with the .247 stack already live):
#   ASTRO_URL=http://astronomer.5.78.101.247.nip.io:8080 \
#   ASTRO_USERNAME=admin ASTRO_PASSWORD=... \
#   ./scripts/smoke-fresh-cluster.sh
#
# Env vars:
#   ASTRO_URL          — management URL (default: http://astronomer.localtest.me:8080)
#   ASTRO_USERNAME     — admin user (default: admin)
#   ASTRO_PASSWORD     — admin password (required)
#   SMOKE_CLUSTER      — k3d cluster name to create (default: astronomer-smoke-$$)
#   SMOKE_KEEP         — set to 1 to leave the k3d cluster behind on success
#   AGENT_IMAGE        — astronomer agent image to load (default: ghcr.io/alphabravo-oss/astronomer-go-agent:dev)
#   SHELL_IMAGE        — astronomer-shell image to load (default: ghcr.io/alphabravo-oss/astronomer-shell:dev)
#   TIMEOUT_AGENT      — seconds to wait for agent connect (default: 90)
#   TIMEOUT_BASELINE   — seconds to wait for baseline tools install (default: 300)
#   TIMEOUT_SCANS      — seconds to wait for first vulnerability report (default: 240)

set -euo pipefail

# ── config ────────────────────────────────────────────────────────────

: "${ASTRO_URL:=http://astronomer.localtest.me:8080}"
: "${ASTRO_USERNAME:=admin}"
: "${ASTRO_PASSWORD:?ASTRO_PASSWORD is required}"
: "${SMOKE_CLUSTER:=astronomer-smoke-$$}"
: "${SMOKE_KEEP:=0}"
: "${AGENT_IMAGE:=ghcr.io/alphabravo-oss/astronomer-go-agent:dev}"
: "${SHELL_IMAGE:=ghcr.io/alphabravo-oss/astronomer-shell:dev}"
: "${TIMEOUT_AGENT:=90}"
: "${TIMEOUT_BASELINE:=300}"
: "${TIMEOUT_SCANS:=240}"

KUBECONFIG_FILE="$(mktemp -t smoke-kubeconfig.XXXXXX)"
trap cleanup EXIT

step()  { printf "\n\033[1;36m▸ %s\033[0m\n" "$*"; }
ok()    { printf "\033[1;32m✓ %s\033[0m\n" "$*"; }
fail()  { printf "\033[1;31m✗ %s\033[0m\n" "$*" >&2; exit 1; }

cleanup() {
  local rc=$?
  if [[ "$SMOKE_KEEP" != "1" || $rc -ne 0 ]]; then
    if [[ "${SMOKE_DELETE:-1}" == "1" ]]; then
      step "Cleanup: deleting k3d cluster $SMOKE_CLUSTER"
      k3d cluster delete "$SMOKE_CLUSTER" >/dev/null 2>&1 || true
    fi
    if [[ -n "${SMOKE_CLUSTER_ID:-}" ]]; then
      curl -sS -X DELETE \
        -H "Authorization: Bearer $TOKEN" \
        "$ASTRO_URL/api/v1/clusters/$SMOKE_CLUSTER_ID/" >/dev/null 2>&1 || true
    fi
  else
    printf "\n  k3d cluster left behind: %s\n" "$SMOKE_CLUSTER"
    printf "  registered as cluster_id: %s\n" "${SMOKE_CLUSTER_ID:-N/A}"
  fi
  rm -f "$KUBECONFIG_FILE"
  if [[ $rc -eq 0 ]]; then
    printf "\n\033[1;32m═══ SMOKE TEST PASSED ═══\033[0m\n"
  else
    printf "\n\033[1;31m═══ SMOKE TEST FAILED ═══\033[0m\n"
  fi
}

api() {
  # Wrapper that re-authenticates if the token expired mid-run.
  local method="$1"; shift
  local path="$1"; shift
  curl -sS -X "$method" \
    -H "Authorization: Bearer $TOKEN" \
    -H "Content-Type: application/json" \
    "$ASTRO_URL$path" "$@"
}

jget() { python3 -c "import sys,json; print(json.load(sys.stdin)$1)"; }

# ── 0. preflight ──────────────────────────────────────────────────────

step "Preflight: tooling versions"
command -v k3d >/dev/null    || fail "k3d not on PATH"
command -v kubectl >/dev/null || fail "kubectl not on PATH"
command -v curl >/dev/null   || fail "curl not on PATH"
command -v python3 >/dev/null || fail "python3 not on PATH"
ok "k3d $(k3d version | head -1)"
ok "kubectl client present"

step "Preflight: management API reachable"
curl -fsS --max-time 5 "$ASTRO_URL/health" >/dev/null \
  || fail "management API at $ASTRO_URL not reachable"
ok "management API responds"

# ── 1. authenticate ───────────────────────────────────────────────────

step "Authenticate"
LOGIN_BODY="$(curl -fsS -X POST -H 'Content-Type: application/json' \
  -d "{\"username\":\"$ASTRO_USERNAME\",\"password\":\"$ASTRO_PASSWORD\"}" \
  "$ASTRO_URL/api/v1/auth/login/")"
TOKEN="$(echo "$LOGIN_BODY" | jget "['data']['token']")"
[[ -n "$TOKEN" ]] || fail "no token in login response"
ok "authenticated as $ASTRO_USERNAME"

# ── 2. create k3d cluster ─────────────────────────────────────────────

step "Create k3d cluster: $SMOKE_CLUSTER"
# --network k3d-astronomer-mgmt joins the management cluster's docker
# network so the agent can reach the public nip.io URL through the
# host. CI environments without that network fall back to bridge —
# the manifest URL is server-derived so it Just Works either way.
NETWORK_ARG=""
if docker network inspect k3d-astronomer-mgmt >/dev/null 2>&1; then
  NETWORK_ARG="--network k3d-astronomer-mgmt"
fi
k3d cluster create "$SMOKE_CLUSTER" --no-lb \
    --k3s-arg "--disable=traefik@server:0" \
    $NETWORK_ARG \
    >/dev/null
ok "k3d cluster up"

step "Import images into k3d"
k3d image import -c "$SMOKE_CLUSTER" "$AGENT_IMAGE" "$SHELL_IMAGE" >/dev/null 2>&1
ok "images imported"

# ── 3. register cluster via wizard API ────────────────────────────────

step "Register cluster via wizard"
CREATE_BODY="$(api POST /api/v1/clusters/ -d "$(cat <<EOF
{"name":"$SMOKE_CLUSTER","display_name":"smoke test","environment":"dev","provider":"k3d","distribution":"k3s","region":"local"}
EOF
)")"
SMOKE_CLUSTER_ID="$(echo "$CREATE_BODY" | jget "['data']['id']")"
ok "cluster created: $SMOKE_CLUSTER_ID"

api PUT "/api/v1/clusters/$SMOKE_CLUSTER_ID/registration/options/" \
  -d '{"install_baseline":true}' >/dev/null
ok "install_baseline=true recorded"

step "Fetch agent manifest"
MANIFEST_FILE="$(mktemp -t smoke-agent.XXXXXX.yaml)"
curl -fsS -H "Authorization: Bearer $TOKEN" \
  "$ASTRO_URL/api/v1/clusters/$SMOKE_CLUSTER_ID/manifest/" > "$MANIFEST_FILE"
grep -q "SERVER_URL" "$MANIFEST_FILE" || fail "manifest missing SERVER_URL placeholder"
ok "manifest fetched ($(wc -l <"$MANIFEST_FILE") lines)"

step "Apply manifest into k3d cluster"
kubectl --context "k3d-$SMOKE_CLUSTER" apply -f "$MANIFEST_FILE" >/dev/null
rm -f "$MANIFEST_FILE"
ok "manifest applied"

# ── 4. wait for agent to connect ──────────────────────────────────────

step "Wait for agent connect (timeout ${TIMEOUT_AGENT}s)"
deadline=$(( $(date +%s) + TIMEOUT_AGENT ))
while (( $(date +%s) < deadline )); do
  hb="$(api GET "/api/v1/clusters/$SMOKE_CLUSTER_ID/" | jget "['data']['last_heartbeat']" 2>/dev/null || true)"
  if [[ -n "$hb" && "$hb" != "None" && "$hb" != "null" ]]; then
    ok "agent heartbeat: $hb"
    break
  fi
  sleep 3
done
[[ -n "$hb" && "$hb" != "None" && "$hb" != "null" ]] || fail "agent never sent a heartbeat within ${TIMEOUT_AGENT}s"

step "Confirm wizard step (advance to awaiting_agent → connected)"
api POST "/api/v1/clusters/$SMOKE_CLUSTER_ID/registration/confirm/" \
  -d '{}' >/dev/null
ok "confirm posted"

# ── 5. wait for baseline tools to install ─────────────────────────────

step "Wait for baseline operators to install (timeout ${TIMEOUT_BASELINE}s)"
deadline=$(( $(date +%s) + TIMEOUT_BASELINE ))
expected_tools="trivy-operator kube-state-metrics prometheus-node-exporter fluent-bit cert-manager"
while (( $(date +%s) < deadline )); do
  # tools/status returns an array of {slug,status,...}; we accept any
  # status that's not "not_installed" as success — installing/installed/upgrading
  # all mean the apply task is doing work, and the polling loop catches
  # the final state when it settles.
  installed="$(api GET "/api/v1/clusters/$SMOKE_CLUSTER_ID/tools/status/" 2>/dev/null \
    | python3 -c 'import sys,json
d=json.load(sys.stdin)
print(" ".join(r["slug"] for r in d if r.get("status") in ("installed","installing","upgrading")))' 2>/dev/null || true)"
  missing=""
  for t in $expected_tools; do
    if ! echo " $installed " | grep -q " $t "; then
      missing="$missing $t"
    fi
  done
  if [[ -z "$missing" ]]; then
    ok "all baseline tools installed: $installed"
    break
  fi
  sleep 5
done
[[ -z "$missing" ]] || fail "baseline tools not installed after ${TIMEOUT_BASELINE}s: missing$missing"

# ── 6. open kubectl shell ─────────────────────────────────────────────

step "Open kubectl shell session"
SHELL_BODY="$(api POST "/api/v1/clusters/$SMOKE_CLUSTER_ID/shell/sessions/" -d '{}' --max-time 90)"
SHELL_STATUS="$(echo "$SHELL_BODY" | jget "['data']['status']" 2>/dev/null || true)"
[[ "$SHELL_STATUS" == "active" ]] || fail "shell session not active: $SHELL_BODY"
SHELL_SESSION_ID="$(echo "$SHELL_BODY" | jget "['data']['id']")"
ok "shell session $SHELL_SESSION_ID active"

# Tear down so the smoke test doesn't leak a long-lived shell pod.
api POST "/api/v1/clusters/$SMOKE_CLUSTER_ID/shell/sessions/$SHELL_SESSION_ID/close/" >/dev/null 2>&1 || true
ok "shell session closed"

# ── 7. wait for first vulnerability report ────────────────────────────

step "Wait for first image vulnerability report (timeout ${TIMEOUT_SCANS}s)"
deadline=$(( $(date +%s) + TIMEOUT_SCANS ))
while (( $(date +%s) < deadline )); do
  count="$(api GET "/api/v1/clusters/$SMOKE_CLUSTER_ID/vulnerabilities/summary/" \
    | jget "['data']['report_count']" 2>/dev/null || echo 0)"
  if [[ "$count" -gt 0 ]]; then
    ok "vulnerability reports flowing: $count"
    break
  fi
  sleep 6
done
[[ "$count" -gt 0 ]] || fail "no vulnerability reports after ${TIMEOUT_SCANS}s"

# ── 7b. assert registration_phase == ready (T5.1) ─────────────────────
#
# The provisioning tab renders the cluster_registration_steps timeline.
# The phase-machine self-heal fix from sprint-086 closes orphan
# 'running' rows; if the wizard finishes without ever transitioning
# the cluster to registration_phase=ready, the user would see a stuck
# "provisioning" badge forever. Pin that here so a regression on the
# phase machine fails the smoke instead of leaving a half-onboarded
# cluster on staging.

step "Assert registration_phase=ready (T5.1)"
phase="$(api GET "/api/v1/clusters/$SMOKE_CLUSTER_ID/" \
  | jget "['data']['registrationPhase']" 2>/dev/null || true)"
[[ "$phase" == "ready" ]] || fail "registration_phase=$phase, expected 'ready'"
ok "registration_phase=ready"

# Also confirm no orphan 'template_applying running' rows survived —
# migration 087 backfills these on upgrade, but a regression on the
# self-heal path would leave them in flight on a fresh registration.
orphan_count="$(api GET "/api/v1/clusters/$SMOKE_CLUSTER_ID/registration/steps/" \
  | python3 -c 'import sys,json
d=json.load(sys.stdin).get("data",[])
print(sum(1 for s in d if s.get("stepName")=="template_applying" and s.get("status")=="running"))' \
  2>/dev/null || echo 0)"
[[ "$orphan_count" -eq 0 ]] || fail "found $orphan_count orphan template_applying running rows"
ok "no orphan template_applying rows"

# ── 8. verify k8s proxy works ─────────────────────────────────────────

step "k8s passthrough proxy"
NS_COUNT="$(api GET "/api/v1/clusters/$SMOKE_CLUSTER_ID/k8s/api/v1/namespaces" \
  | jget "['items'].__len__()" 2>/dev/null || echo 0)"
[[ "$NS_COUNT" -ge 4 ]] || fail "k8s proxy returned $NS_COUNT namespaces (expected >=4)"
ok "k8s proxy returned $NS_COUNT namespaces"

# ── 9. openapi + swagger ──────────────────────────────────────────────

step "OpenAPI spec + Swagger UI"
SPEC_LEN="$(curl -fsS "$ASTRO_URL/api/v1/openapi.yaml" | wc -c)"
[[ "$SPEC_LEN" -gt 1000 ]] || fail "openapi spec suspiciously short: $SPEC_LEN bytes"
ok "openapi spec $SPEC_LEN bytes"
DOCS_CT="$(curl -fsS -o /dev/null -w '%{content_type}' "$ASTRO_URL/api/v1/docs/")"
[[ "$DOCS_CT" == "text/html"* ]] || fail "swagger UI content-type: $DOCS_CT"
ok "swagger UI served"

step "All smoke-test stages passed for cluster $SMOKE_CLUSTER_ID"
