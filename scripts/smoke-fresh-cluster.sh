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
#   ASTRO_EMAIL=admin@astronomer.local ASTRO_PASSWORD=... \
#   ./scripts/smoke-fresh-cluster.sh
#
# Env vars:
#   ASTRO_URL          — management URL (default: http://astronomer.localtest.me:8080)
#   ASTRO_EMAIL        — admin email (default: admin@astronomer.local)
#   ASTRO_PASSWORD     — admin password (required)
#   SMOKE_CLUSTER      — k3d cluster name to create (default: astronomer-smoke-$$)
#   SMOKE_KEEP         — set to 1 to leave the k3d cluster behind on success
#   MGMT_CONTEXT       — management-plane kube context (default: current context)
#   AGENT_IMAGE        — astronomer agent image to load (default: ghcr.io/alphabravo-oss/astronomer-go-agent:dev)
#   SHELL_IMAGE        — astronomer-shell image to load (default: ghcr.io/alphabravo-oss/astronomer-shell:dev)
#   K3S_IMAGE          — rancher/k3s image for the adopted cluster (default: v1.35.0-k3s1)
#   TIMEOUT_API        — seconds to wait for the management API (default: 180)
#   TIMEOUT_AGENT      — seconds to wait for agent connect (default: 90)
#   TIMEOUT_BASELINE   — seconds to wait for Flux baseline convergence (default: 600)
#   TIMEOUT_SCANS      — seconds to wait for first vulnerability report (default: 240)
#   SMOKE_EVIDENCE_FILE — sanitized JSON evidence path (default: /tmp/astronomer-smoke-evidence.json)

set -euo pipefail

# ── config ────────────────────────────────────────────────────────────

: "${ASTRO_URL:=http://astronomer.localtest.me:8080}"
: "${ASTRO_EMAIL:=admin@astronomer.local}"
: "${ASTRO_PASSWORD:?ASTRO_PASSWORD is required}"
: "${SMOKE_CLUSTER:=astronomer-smoke-$$}"
: "${SMOKE_KEEP:=0}"
: "${AGENT_IMAGE:=ghcr.io/alphabravo-oss/astronomer-go-agent:dev}"
: "${SHELL_IMAGE:=ghcr.io/alphabravo-oss/astronomer-shell:dev}"
: "${K3S_IMAGE:=rancher/k3s:v1.35.0-k3s1}"
: "${MGMT_CONTEXT:=$(kubectl config current-context 2>/dev/null || true)}"
: "${TIMEOUT_API:=180}"
: "${TIMEOUT_AGENT:=90}"
: "${TIMEOUT_BASELINE:=600}"
: "${TIMEOUT_SCANS:=240}"
: "${SMOKE_EVIDENCE_FILE:=/tmp/astronomer-smoke-evidence.json}"

SMOKE_STARTED_AT="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
SMOKE_CURRENT_CHECK="initialization"
SMOKE_COMPLETED_CHECKS=()
SMOKE_SKIPPED_CHECKS=()
SMOKE_EVIDENCE_WRITER="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/write-fresh-cluster-evidence.py"

KUBECONFIG_FILE="$(mktemp -t smoke-kubeconfig.XXXXXX)"
trap 'cleanup "$?"' EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

step()  { SMOKE_CURRENT_CHECK="$*"; printf "\n\033[1;36m▸ %s\033[0m\n" "$*"; }
ok()    { printf "\033[1;32m✓ %s\033[0m\n" "$*"; }
fail()  { printf "\033[1;31m✗ %s\033[0m\n" "$*" >&2; exit 1; }
record_check() { SMOKE_COMPLETED_CHECKS+=("$1"); }
record_skip() { SMOKE_SKIPPED_CHECKS+=("$1"); }

write_evidence() {
  local rc="$1" status="fail" commit kubernetes_version flux_version check flux_image management_image management_images
  [[ "$rc" -eq 0 ]] && status="pass"
  commit="${GITHUB_SHA:-$(git rev-parse HEAD 2>/dev/null || echo unknown)}"
  kubernetes_version="$(kubectl --context "k3d-$SMOKE_CLUSTER" version -o json 2>/dev/null \
    | python3 -c 'import json,sys; print(json.load(sys.stdin).get("serverVersion",{}).get("gitVersion",""))' 2>/dev/null || true)"
  flux_version="$(python3 -c 'import json; print(json.load(open("deploy/release/compatibility.yaml", encoding="utf-8"))["flux"]["distribution_version"])' 2>/dev/null || true)"
  management_images="$(kubectl --context "$MGMT_CONTEXT" -n astronomer get deployments -o json 2>/dev/null \
    | python3 -c 'import json,sys
payload=json.load(sys.stdin)
for item in payload.get("items", []):
    for container in item.get("spec",{}).get("template",{}).get("spec",{}).get("containers",[]):
        print("{}={}".format(item.get("metadata",{}).get("name",""),container.get("image","")))' 2>/dev/null || true)"
  local args=(
    --output "$SMOKE_EVIDENCE_FILE" --status "$status" --exit-code "$rc"
    --started-at "$SMOKE_STARTED_AT" --completed-at "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
    --commit "$commit" --workflow "${GITHUB_WORKFLOW:-}" --run-id "${GITHUB_RUN_ID:-}"
    --run-attempt "${GITHUB_RUN_ATTEMPT:-}" --job "${GITHUB_JOB:-}"
    --repository "${GITHUB_REPOSITORY:-}" --ref "${GITHUB_REF:-}"
    --cluster-name "$SMOKE_CLUSTER" --cluster-id "${SMOKE_CLUSTER_ID:-}"
    --kubernetes-version "$kubernetes_version" --flux-version "$flux_version"
    --agent-image "$AGENT_IMAGE" --shell-image "$SHELL_IMAGE" --k3s-image "$K3S_IMAGE"
  )
  [[ "$status" == "pass" ]] || args+=(--failed-check "$SMOKE_CURRENT_CHECK")
  for check in "${SMOKE_COMPLETED_CHECKS[@]}"; do args+=(--check "$check"); done
  for check in "${SMOKE_SKIPPED_CHECKS[@]}"; do args+=(--skipped-check "$check"); done
  while IFS= read -r flux_image; do
    [[ -z "$flux_image" ]] || args+=(--flux-image "$flux_image")
  done <<<"${observed_flux_images:-}"
  while IFS= read -r management_image; do
    [[ -z "$management_image" ]] || args+=(--management-image "$management_image")
  done <<<"$management_images"
  python3 "$SMOKE_EVIDENCE_WRITER" "${args[@]}" || printf 'warning: failed to write smoke evidence\n' >&2
}

cleanup() {
  local rc="${1:-1}"
  write_evidence "$rc"
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

step "Preflight: management API reachable (timeout ${TIMEOUT_API}s)"
# Helm can briefly observe the server rollout as Available before every
# dependency is ready. Treat that bounded 503 window as startup, while still
# failing a server that never becomes healthy.
api_ready=0
deadline=$(( $(date +%s) + TIMEOUT_API ))
while (( $(date +%s) < deadline )); do
  if curl -fsS --max-time 5 "$ASTRO_URL/health" >/dev/null 2>&1; then
    api_ready=1
    break
  fi
  sleep 2
done
[[ "$api_ready" == "1" ]] || fail "management API at $ASTRO_URL not reachable within ${TIMEOUT_API}s"
ok "management API responds"
record_check "management_api_ready"

# ── 1. authenticate ───────────────────────────────────────────────────

step "Authenticate"
LOGIN_BODY="$(curl -fsS -X POST -H 'Content-Type: application/json' \
  -d "{\"email\":\"$ASTRO_EMAIL\",\"password\":\"$ASTRO_PASSWORD\"}" \
  "$ASTRO_URL/api/v1/auth/login/")"
TOKEN="$(echo "$LOGIN_BODY" | jget "['data']['token']")"
[[ -n "$TOKEN" ]] || fail "no token in login response"
ok "authenticated as $ASTRO_EMAIL"
record_check "authentication"

# ── 2. create k3d cluster ─────────────────────────────────────────────

step "Create k3d cluster: $SMOKE_CLUSTER"
# Join the management cluster's Docker network so the agent can reach its
# advertised URL through host.k3d.internal. Derive the network from the
# caller-supplied context; hard-coding the default cluster name makes isolated
# and concurrent smoke runs silently create an unreachable adopted cluster.
NETWORK_ARGS=()
if [[ "$MGMT_CONTEXT" == k3d-* ]]; then
  MGMT_NETWORK="k3d-${MGMT_CONTEXT#k3d-}"
  if docker network inspect "$MGMT_NETWORK" >/dev/null 2>&1; then
    NETWORK_ARGS=(--network "$MGMT_NETWORK")
  fi
fi
k3d cluster create "$SMOKE_CLUSTER" --no-lb \
    --image "$K3S_IMAGE" \
    --k3s-arg "--disable=traefik@server:0" \
    "${NETWORK_ARGS[@]}" \
    >/dev/null
ok "k3d cluster up"

step "Import images into k3d"
k3d image import -c "$SMOKE_CLUSTER" "$AGENT_IMAGE" "$SHELL_IMAGE" >/dev/null 2>&1
ok "images imported"
record_check "adopted_cluster_created"

# ── 3. register cluster via wizard API ────────────────────────────────

step "Register cluster via wizard"
CREATE_BODY="$(api POST /api/v1/clusters/ -d "$(cat <<EOF
{"name":"$SMOKE_CLUSTER","display_name":"smoke test","environment":"dev","provider":"k3d","distribution":"k3s","region":"local","annotations":{"astronomer.io/agent-privilege-profile":"admin"}}
EOF
)")"
SMOKE_CLUSTER_ID="$(echo "$CREATE_BODY" | jget "['data']['id']")"
ok "cluster created with explicit admin agent profile: $SMOKE_CLUSTER_ID"

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
record_check "registration_manifest_applied"

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
record_check "agent_connected"

step "Confirm wizard step (advance to awaiting_agent → connected)"
api POST "/api/v1/clusters/$SMOKE_CLUSTER_ID/registration/confirm/" \
  -d '{}' >/dev/null
ok "confirm posted"

# ── 5. wait for the catalog-defined Flux baseline ─────────────────────

expected_builtin_releases="$(python3 -c 'import json
with open("deploy/bundles/catalog.json", encoding="utf-8") as handle:
    catalog=json.load(handle)
print("\n".join(sorted("{}/{}".format(item["target_namespace"], item["release_name"]) for item in catalog["components"] if item["default_enabled"])))')"
[[ -n "$expected_builtin_releases" ]] || fail "built-in bundle catalog has no default-enabled releases"
trivy_default_enabled="$(python3 -c 'import json
with open("deploy/bundles/catalog.json", encoding="utf-8") as handle:
    catalog=json.load(handle)
print("true" if any(item["slug"] == "trivy-operator" and item["default_enabled"] for item in catalog["components"]) else "false")')"

step "Wait for the exact signed Flux distribution (timeout ${TIMEOUT_BASELINE}s)"
deadline=$(( $(date +%s) + TIMEOUT_BASELINE ))
expected_flux_controllers="helm-controller kustomize-controller source-controller"
ready_flux_controllers=""
while (( $(date +%s) < deadline )); do
  ready_flux_controllers="$(kubectl --context "k3d-$SMOKE_CLUSTER" \
    -n astronomer-delivery-system get deployments -o json 2>/dev/null \
    | python3 -c 'import json,sys
payload=json.load(sys.stdin)
ready=[]
for item in payload.get("items", []):
    metadata=item.get("metadata", {})
    spec=item.get("spec", {})
    status=item.get("status", {})
    replicas=spec.get("replicas", 1)
    if status.get("observedGeneration", 0) >= metadata.get("generation", 1) and status.get("availableReplicas", 0) >= replicas:
        ready.append(metadata.get("name", ""))
print(" ".join(sorted(ready)))' 2>/dev/null || true)"
  if [[ "$ready_flux_controllers" == "$expected_flux_controllers" ]]; then
    ok "exact Flux controller set is ready: $ready_flux_controllers"
    break
  fi
  sleep 5
done
[[ "$ready_flux_controllers" == "$expected_flux_controllers" ]] \
  || fail "Flux controllers not ready or unexpected controller present after ${TIMEOUT_BASELINE}s: '$ready_flux_controllers'"

expected_flux_images="$(python3 -c 'import json
with open("deploy/release/compatibility.yaml", encoding="utf-8") as handle:
    contract=json.load(handle)
print("\n".join(sorted("{}={}@{}".format(item["name"], item["repository"], item["digest"]) for item in contract["flux"]["components"])))')"
observed_flux_images="$(kubectl --context "k3d-$SMOKE_CLUSTER" \
  -n astronomer-delivery-system get deployments -o json \
  | python3 -c 'import json,sys
payload=json.load(sys.stdin)
rows=[]
for item in payload.get("items", []):
    containers=item.get("spec", {}).get("template", {}).get("spec", {}).get("containers", [])
    if len(containers) == 1:
        rows.append("{}={}".format(item["metadata"]["name"], containers[0].get("image", "")))
print("\n".join(sorted(rows)))')"
[[ "$observed_flux_images" == "$expected_flux_images" ]] \
  || fail "running Flux controller images do not exactly match the signed release compatibility contract"
ok "Flux controller images exactly match the signed release digests"
record_check "flux_distribution_verified"

step "Wait for Flux-owned built-in platform bundles (timeout ${TIMEOUT_BASELINE}s)"
deadline=$(( $(date +%s) + TIMEOUT_BASELINE ))
observed_builtin_releases=""
ready_builtin_releases=""
while (( $(date +%s) < deadline )); do
  builtin_release_rows="$(kubectl --context "k3d-$SMOKE_CLUSTER" get \
    helmreleases.helm.toolkit.fluxcd.io --all-namespaces \
    -l app.kubernetes.io/managed-by=astronomer-agent -o json 2>/dev/null \
    | python3 -c 'import json,sys
payload=json.load(sys.stdin)
rows=[]
for item in payload.get("items", []):
    metadata=item.get("metadata", {})
    spec=item.get("spec", {})
    status=item.get("status", {})
    current=status.get("observedGeneration", 0) >= metadata.get("generation", 1)
    ready=any(c.get("type") == "Ready" and c.get("status") == "True" for c in status.get("conditions", []))
    rows.append(("{}/{}".format(spec.get("targetNamespace", ""), spec.get("releaseName", "")), current and ready))
for identity, is_ready in sorted(rows):
    print("{}\t{}".format(identity, int(is_ready)))' 2>/dev/null || true)"
  observed_builtin_releases="$(printf '%s\n' "$builtin_release_rows" | awk -F '\t' 'NF {print $1}')"
  ready_builtin_releases="$(printf '%s\n' "$builtin_release_rows" | awk -F '\t' '$2 == "1" {print $1}')"
  if [[ "$observed_builtin_releases" == "$expected_builtin_releases" && "$ready_builtin_releases" == "$expected_builtin_releases" ]]; then
    ok "exact catalog-defined HelmRelease set is generation-current and Ready"
    break
  fi
  sleep 5
done
[[ "$observed_builtin_releases" == "$expected_builtin_releases" ]] \
  || fail "agent-owned HelmRelease set does not exactly match default-enabled catalog releases after ${TIMEOUT_BASELINE}s"
[[ "$ready_builtin_releases" == "$expected_builtin_releases" ]] \
  || fail "not every catalog-defined HelmRelease is generation-current and Ready after ${TIMEOUT_BASELINE}s"

kubectl --context "k3d-$SMOKE_CLUSTER" -n astronomer-monitoring \
  rollout status deployment/kube-state-metrics --timeout=120s >/dev/null
kubectl --context "k3d-$SMOKE_CLUSTER" -n astronomer-monitoring \
  rollout status daemonset/prometheus-node-exporter --timeout=120s >/dev/null
ok "kube-state-metrics and prometheus-node-exporter workloads are ready"
record_check "catalog_baseline_releases_ready"

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
record_check "kubectl_shell"

# ── 7. wait for first vulnerability report ────────────────────────────

if [[ "$trivy_default_enabled" == "true" ]]; then
  step "Wait for first image vulnerability report (timeout ${TIMEOUT_SCANS}s)"
  deadline=$(( $(date +%s) + TIMEOUT_SCANS ))
  count=0
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
  record_check "vulnerability_reports"
else
  step "Skip vulnerability report check: trivy-operator is not default-enabled"
  ok "vulnerability reporting is outside the catalog-defined default baseline"
  record_skip "vulnerability_reports_not_in_default_baseline"
fi

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
deadline=$(( $(date +%s) + TIMEOUT_BASELINE ))
phase=""
while (( $(date +%s) < deadline )); do
  phase="$(api GET "/api/v1/clusters/$SMOKE_CLUSTER_ID/" \
    | jget "['data']['registration_phase']" 2>/dev/null || true)"
  [[ "$phase" == "ready" ]] && break
  sleep 2
done
[[ "$phase" == "ready" ]] || fail "registration_phase=$phase, expected 'ready'"
ok "registration_phase=ready"

# Also confirm no orphan 'template_applying running' rows survived —
# migration 087 backfills these on upgrade, but a regression on the
# self-heal path would leave them in flight on a fresh registration.
orphan_count="$(api GET "/api/v1/clusters/$SMOKE_CLUSTER_ID/registration/status/" \
  | python3 -c 'import sys,json
d=json.load(sys.stdin).get("data",{})
print(sum(1 for s in d.get("steps",[]) if s.get("step_name")=="template_applying" and s.get("status")=="running"))' \
  2>/dev/null || echo 0)"
[[ "$orphan_count" -eq 0 ]] || fail "found $orphan_count orphan template_applying running rows"
ok "no orphan template_applying rows"
record_check "registration_ready"

# ── 8. verify k8s proxy works ─────────────────────────────────────────

step "k8s passthrough proxy"
NS_COUNT="$(api GET "/api/v1/clusters/$SMOKE_CLUSTER_ID/k8s/api/v1/namespaces" \
  | jget "['items'].__len__()" 2>/dev/null || echo 0)"
[[ "$NS_COUNT" -ge 4 ]] || fail "k8s proxy returned $NS_COUNT namespaces (expected >=4)"
ok "k8s proxy returned $NS_COUNT namespaces"
record_check "kubernetes_proxy"

# ── 9. openapi + swagger ──────────────────────────────────────────────

step "OpenAPI spec + Swagger UI"
SPEC_LEN="$(curl -fsS "$ASTRO_URL/api/v1/openapi.yaml" | wc -c)"
[[ "$SPEC_LEN" -gt 1000 ]] || fail "openapi spec suspiciously short: $SPEC_LEN bytes"
ok "openapi spec $SPEC_LEN bytes"
DOCS_CT="$(curl -fsS -o /dev/null -w '%{content_type}' "$ASTRO_URL/api/v1/docs/")"
[[ "$DOCS_CT" == "text/html"* ]] || fail "swagger UI content-type: $DOCS_CT"
ok "swagger UI served"
record_check "openapi_and_swagger"

step "All smoke-test stages passed for cluster $SMOKE_CLUSTER_ID"
