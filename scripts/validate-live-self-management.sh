#!/usr/bin/env bash
# Live acceptance harness for the self-managed Argo Application (Plan 003).
#
# Proves, against a DISPOSABLE cluster, that one operator sync request:
#   - produces exactly one durable operation with attempt_count=1 and exactly
#     one upstream ArgoCD sync call,
#   - runs exactly one successful Argo preflight Job lifecycle,
#   - is never raced by an Astronomer full-object Application write while the
#     operation is Running/Terminating (single-writer interval),
#   - terminates Succeeded/Synced/Healthy with exact source/destination
#     binding, and activates only through exact-hash approval.
#
# Redaction contract: this script never prints or stores Secret values, Helm
# values, tokens, passwords, login responses, or raw Application YAML. It
# records names, UIDs, key NAMES, whole-object digests, phases, counts, and
# bounded timestamps only. Credential temp files are 0600 and deleted as soon
# as the sync request has been created.
#
# Usage:
#   DISPOSABLE_CLUSTER_ACK=i-know KUBE_CONTEXT=k3d-... \
#     ASTRO_USERNAME=admin ASTRO_PASSWORD=... \
#     ./scripts/validate-live-self-management.sh
#
# Required env:
#   KUBE_CONTEXT            explicit kubeconfig context (never "current")
#   DISPOSABLE_CLUSTER_ACK  must be exactly "i-know" to acknowledge the target
#                           cluster is disposable
#   ASTRO_USERNAME + ASTRO_PASSWORD (or ASTRO_PASSWORD_FILE)
#
# Optional env:
#   BASE_URL          default http://astronomer.localtest.me:18080
#   HOST_HEADER       default host.k3d.internal (Gateway hostname)
#   NAMESPACE         default astronomer
#   EXPECTED_REVISION default 0.3.0 (exact staged chart targetRevision)
#   SYNC_TIMEOUT      default 900 seconds (operation SLO)
#   READY_TIMEOUT     default 300 seconds (controller/cache readiness)
#   OUT_DIR           default private mktemp dir (0700)
#   SKIP_APPROVAL=1   stop after terminal acceptance evidence (no activation)

set -Eeuo pipefail
umask 0077

KUBE_CONTEXT="${KUBE_CONTEXT:-}"
DISPOSABLE_CLUSTER_ACK="${DISPOSABLE_CLUSTER_ACK:-}"
BASE_URL="${BASE_URL:-http://astronomer.localtest.me:18080}"
HOST_HEADER="${HOST_HEADER:-host.k3d.internal}"
NAMESPACE="${NAMESPACE:-astronomer}"
EXPECTED_REVISION="${EXPECTED_REVISION:-0.3.0}"
SYNC_TIMEOUT="${SYNC_TIMEOUT:-900}"
READY_TIMEOUT="${READY_TIMEOUT:-300}"
SKIP_APPROVAL="${SKIP_APPROVAL:-0}"
API_BASE="${BASE_URL%/}/api/v1"
APP_NAME="astronomer-self-manage"
CONTROLLER="astro-argocd-application-controller"
PREFLIGHT_JOB="astronomer-preflight-argocd"
PHASE_ANNOTATION="astronomer.io/self-manage-phase"
HASH_ANNOTATION="astronomer.io/self-manage-spec-hash"
APPROVE_ANNOTATION="astronomer.io/self-manage-approved-hash"

for tool in kubectl curl jq; do
  command -v "$tool" >/dev/null 2>&1 || { echo "missing required tool: $tool" >&2; exit 1; }
done
if [[ -z "$KUBE_CONTEXT" ]]; then
  echo "KUBE_CONTEXT is required; this harness never uses the ambient current-context" >&2
  exit 1
fi
if [[ "$DISPOSABLE_CLUSTER_ACK" != "i-know" ]]; then
  echo "refusing to run: set DISPOSABLE_CLUSTER_ACK=i-know to confirm ${KUBE_CONTEXT} is a disposable acceptance cluster" >&2
  exit 1
fi

OUT_DIR="${OUT_DIR:-$(mktemp -d)}"
mkdir -p "$OUT_DIR"
chmod 0700 "$OUT_DIR"
RESULT_FILE="$OUT_DIR/RESULT.txt"
LOG_FILE="$OUT_DIR/run.log"
touch "$RESULT_FILE" "$LOG_FILE"
chmod 0600 "$RESULT_FILE" "$LOG_FILE"

K="kubectl --context $KUBE_CONTEXT -n $NAMESPACE"
PSQL=($K exec astronomer-postgres-0 -- psql -U astronomer -d astronomer -tA -c)

log() { printf '%s %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*" | tee -a "$LOG_FILE" >&2; }
sql() { "${PSQL[@]}" "$1"; }

GO_STATE=NO-GO
OP_ID=""
FINISHED=0

# Failure containment: stop the Application controller, wait for its Pods to
# terminate, and report (never mutate) any still-running durable operation.
cleanup() {
  local status=$?
  rm -f "$OUT_DIR"/cred.* 2>/dev/null || true
  if [[ "$FINISHED" != 1 || $status -ne 0 ]]; then
    log "FAILURE containment: scaling $CONTROLLER to zero"
    $K scale statefulset "$CONTROLLER" --replicas=0 >/dev/null 2>&1 || true
    for _ in $(seq 1 60); do
      remaining="$($K get pods -l app.kubernetes.io/name=argocd-application-controller -o name 2>/dev/null | wc -l)"
      [[ "$remaining" == 0 ]] && break
      sleep 5
    done
    running="$(sql "select count(*) from argocd_operations where status in ('running','pending')" 2>/dev/null || echo unknown)"
    log "durable operations still running/pending (left untouched): $running"
    [[ -n "$OP_ID" ]] && log "operation under test: $OP_ID (reported, not modified)"
    echo "NO-GO" > "$RESULT_FILE"
  fi
  log "artifacts (private): $OUT_DIR"
}
trap cleanup EXIT

fail() { log "FAIL: $*"; exit 1; }

app_json() {
  # Safe metadata projection only: annotations of interest, phases, statuses,
  # source/destination binding, managed-field manager/time evidence.
  # Exact source/destination binding is computed INSIDE jq as booleans so the
  # Helm values embedded in .spec.source / comparedTo are never emitted.
  $K get application "$APP_NAME" -o json | jq '{
    phase: .metadata.annotations["astronomer.io/self-manage-phase"],
    hash: .metadata.annotations["astronomer.io/self-manage-spec-hash"],
    approved: .metadata.annotations["astronomer.io/self-manage-approved-hash"],
    targetRevision: .spec.source.targetRevision,
    syncPolicy: .spec.syncPolicy,
    syncStatus: .status.sync.status,
    healthStatus: .status.health.status,
    opPhase: .status.operationState.phase,
    sourceExactMatch: (.spec.source == .status.sync.comparedTo.source),
    destinationExactMatch: (.spec.destination == .status.sync.comparedTo.destination),
    resultSourceExactMatch: (.spec.source == .status.operationState.syncResult.source),
    hasTopLevelOperation: (has("operation")),
    managedFields: [.metadata.managedFields[]? | {manager, operation, time}]
  }'
}

secret_evidence() {
  # Names, UIDs, key NAMES, and a whole-object digest. Never values.
  $K get secrets -l astronomer.io/self-manage-credential -o json | jq -c '
    [.items[] | {name: .metadata.name, uid: .metadata.uid, keys: (.data | keys)}]'
  for name in $($K get secrets -l astronomer.io/self-manage-credential -o jsonpath='{.items[*].metadata.name}'); do
    printf '%s digest=%s\n' "$name" "$($K get secret "$name" -o json | sha256sum | cut -d' ' -f1)"
  done
}

pvc_evidence() {
  $K get pvc -o json | jq -c '[.items[] | {name: .metadata.name, uid: .metadata.uid,
    size: .spec.resources.requests.storage, storageClass: .spec.storageClassName, phase: .status.phase}]'
}

workloads_ready() {
  local not_ready
  not_ready="$($K get deploy -o json | jq -r '.items[] | select((.spec.replicas // 1) != (.status.readyReplicas // 0)) | .metadata.name' | grep -v "^$CONTROLLER$" || true)"
  [[ -z "$not_ready" ]] || { log "deployments not ready: $not_ready"; return 1; }
  not_ready="$($K get statefulset -o json | jq -r '.items[] | select(.metadata.name != "'"$CONTROLLER"'") | select((.spec.replicas // 1) != (.status.readyReplicas // 0)) | .metadata.name' || true)"
  [[ -z "$not_ready" ]] || { log "statefulsets not ready: $not_ready"; return 1; }
}

# ---------------------------------------------------------------- preflight
log "=== Phase A: baseline verification (context=$KUBE_CONTEXT) ==="
kubectl --context "$KUBE_CONTEXT" version >/dev/null || fail "context $KUBE_CONTEXT unreachable"

replicas="$($K get statefulset "$CONTROLLER" -o jsonpath='{.spec.replicas}')"
[[ "$replicas" == 0 ]] || fail "controller desired replicas = $replicas, want 0 at baseline"
pods="$($K get pods -l app.kubernetes.io/name=argocd-application-controller -o name | wc -l)"
[[ "$pods" == 0 ]] || fail "controller pods still present at baseline"

running_ops="$(sql "select count(*) from argocd_operations where status in ('running','pending')")"
[[ "$running_ops" == 0 ]] || fail "argocd_operations has $running_ops running/pending rows at baseline"

dirty="$(sql 'select dirty from schema_migrations')"
schema_version="$(sql 'select version from schema_migrations')"
[[ "$dirty" == f ]] || fail "database schema is dirty (version $schema_version)"
log "schema version=$schema_version dirty=$dirty"

baseline="$(app_json)"
phase="$(jq -r '.phase' <<<"$baseline")"
hash="$(jq -r '.hash' <<<"$baseline")"
approved="$(jq -r '.approved' <<<"$baseline")"
target="$(jq -r '.targetRevision' <<<"$baseline")"
[[ "$phase" == awaiting-approval ]] || fail "application phase = $phase, want awaiting-approval"
[[ -n "$hash" && "$hash" != null ]] || fail "application has no spec-hash annotation"
[[ "$approved" == null ]] || fail "approval annotation already present at baseline"
[[ "$target" == "$EXPECTED_REVISION" ]] || fail "targetRevision = $target, want $EXPECTED_REVISION"
[[ "$(jq -r '.hasTopLevelOperation' <<<"$baseline")" == false ]] || fail "top-level operation already queued at baseline"

workloads_ready || fail "management workloads are not Ready at baseline"

secret_evidence > "$OUT_DIR/secrets.before"
pvc_evidence > "$OUT_DIR/pvc.before"
chmod 0600 "$OUT_DIR/secrets.before" "$OUT_DIR/pvc.before"
log "protected Secret/PVC evidence captured"

# ------------------------------------------------------------ authentication
log "=== Phase B: authenticate through the public API ==="
CURL=(curl -fsS --max-time 30 -H "Host: $HOST_HEADER")
PASS_FILE="$OUT_DIR/cred.password"
TOKEN_FILE="$OUT_DIR/cred.token"
LOGIN_FILE="$OUT_DIR/cred.login"
if [[ -n "${ASTRO_PASSWORD_FILE:-}" ]]; then
  install -m 0600 "$ASTRO_PASSWORD_FILE" "$PASS_FILE"
elif [[ -n "${ASTRO_PASSWORD:-}" ]]; then
  (umask 0177; printf '%s' "$ASTRO_PASSWORD" > "$PASS_FILE")
else
  fail "set ASTRO_PASSWORD or ASTRO_PASSWORD_FILE"
fi
[[ -n "${ASTRO_USERNAME:-}" ]] || fail "set ASTRO_USERNAME"
(umask 0177; jq -n --arg u "$ASTRO_USERNAME" --rawfile p "$PASS_FILE" '{username:$u,password:$p}' \
  | "${CURL[@]}" -H 'Content-Type: application/json' -X POST "$API_BASE/auth/login/" -d @- > "$LOGIN_FILE")
(umask 0177; jq -r '.data.token' "$LOGIN_FILE" > "$TOKEN_FILE")
[[ -s "$TOKEN_FILE" && "$(cat "$TOKEN_FILE")" != null ]] || fail "login returned no token"
AUTH=(-H "Authorization: Bearer $(cat "$TOKEN_FILE")")

INSTANCE_ID="$("${CURL[@]}" "${AUTH[@]}" "$API_BASE/argocd/instances/" | jq -r '.data[] | select(.name=="local") | .id')"
[[ -n "$INSTANCE_ID" && "$INSTANCE_ID" != null ]] || fail "local ArgoCD instance not found via API"
log "local instance id: $INSTANCE_ID"

# ------------------------------------------------------- controller start-up
log "=== Phase C: scale controller to one and wait for cache readiness ==="
$K scale statefulset "$CONTROLLER" --replicas=1 >/dev/null
$K rollout status statefulset "$CONTROLLER" --timeout="${READY_TIMEOUT}s" >/dev/null || fail "controller did not become Ready"
controller_pod="$($K get pods -l app.kubernetes.io/name=argocd-application-controller -o jsonpath='{.items[0].metadata.name}')"
# Observable readiness: the controller serves argocd_app_info metrics only
# after its cluster cache and app informers are loaded.
ready=0
deadline=$((SECONDS + READY_TIMEOUT))
while (( SECONDS < deadline )); do
  if $K exec "$controller_pod" -- sh -c 'command -v curl >/dev/null && curl -fsS localhost:8082/metrics || wget -qO- localhost:8082/metrics' 2>/dev/null | grep -q '^argocd_app_info'; then
    ready=1; break
  fi
  sleep 5
done
[[ "$ready" == 1 ]] || fail "controller metrics never reported app cache readiness"
log "controller cache ready (argocd_app_info present)"

# ----------------------------------------------------------------- sync call
log "=== Phase D: submit exactly one non-pruning sync ==="
SUBMIT_TS="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
sync_response="$("${CURL[@]}" "${AUTH[@]}" -H 'Content-Type: application/json' \
  -X POST "$API_BASE/argocd/instances/$INSTANCE_ID/applications/$APP_NAME/sync/" \
  -d "{\"revision\":\"$EXPECTED_REVISION\",\"reason\":\"plan-003 live acceptance run\"}")"
OP_ID="$(jq -r '.id // .data.id // empty' <<<"$sync_response")"
[[ -n "$OP_ID" ]] || fail "sync submission returned no operation id"
log "durable operation created: $OP_ID at $SUBMIT_TS"
# Credentials are no longer needed; delete them immediately.
rm -f "$PASS_FILE" "$TOKEN_FILE" "$LOGIN_FILE"
unset AUTH ASTRO_PASSWORD

# ------------------------------------------------- single-writer observation
log "=== Phase E: poll durable row; assert the single-writer interval ==="
POLLS=0
JOB_UIDS_FILE="$OUT_DIR/preflight-job-uids"
: > "$JOB_UIDS_FILE"
op_status=""
attempt_count=""
deadline=$((SECONDS + SYNC_TIMEOUT))
while (( SECONDS < deadline )); do
  POLLS=$((POLLS + 1))
  row="$(sql "select status||'|'||attempt_count from argocd_operations where id='$OP_ID'")"
  op_status="${row%%|*}"
  attempt_count="${row##*|}"

  sample="$(app_json)"
  op_phase="$(jq -r '.opPhase // empty' <<<"$sample")"
  if [[ "$op_phase" == Running || "$op_phase" == Terminating || "$(jq -r '.hasTopLevelOperation' <<<"$sample")" == true ]]; then
    # The single-writer invariant: no Astronomer server manager may touch the
    # Application while Argo owns the active operation.
    violation="$(jq -r --arg since "$SUBMIT_TS" '
      [.managedFields[] | select(.manager == "server" or .manager == "astronomer-server")
        | select(.time > $since)] | length' <<<"$sample")"
    [[ "$violation" == 0 ]] || fail "manager 'server' wrote the Application during the active interval (evidence: $(jq -c '.managedFields' <<<"$sample"))"
  fi

  job_uid="$($K get job "$PREFLIGHT_JOB" -o jsonpath='{.metadata.uid}' 2>/dev/null || true)"
  if [[ -n "$job_uid" ]] && ! grep -q "$job_uid" "$JOB_UIDS_FILE"; then
    echo "$job_uid" >> "$JOB_UIDS_FILE"
    log "observed preflight Job UID $job_uid"
  fi

  if [[ "$op_status" == completed || "$op_status" == failed ]]; then
    break
  fi
  sleep 5
done
COMPLETE_TS="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
log "operation $OP_ID status=$op_status attempt_count=$attempt_count polls=$POLLS window=$SUBMIT_TS..$COMPLETE_TS"
[[ "$op_status" == completed ]] || fail "operation did not complete inside the SLO (status=$op_status)"
[[ "$attempt_count" == 1 ]] || fail "attempt_count=$attempt_count, want exactly 1"

upstream_calls="$(sql "select count(*) from argocd_operation_events where operation_id='$OP_ID' and message='calling upstream ArgoCD sync'")"
[[ "$upstream_calls" == 1 ]] || fail "upstream sync call events = $upstream_calls, want exactly 1"

replays="$(sql "select count(*) from argocd_operations where operation_type='sync' and created_at >= '$SUBMIT_TS' ")"
[[ "$replays" == 1 ]] || fail "sync operations created during the run = $replays, want exactly 1 (replay detected)"

job_lifecycles="$(wc -l < "$JOB_UIDS_FILE")"
job_completions="$($K get events --field-selector "involvedObject.name=$PREFLIGHT_JOB,involvedObject.kind=Job,reason=Completed" -o json | jq --arg since "$SUBMIT_TS" '[.items[] | select(.lastTimestamp >= $since)] | length')"
[[ "$job_lifecycles" == 1 ]] || fail "observed $job_lifecycles preflight Job UIDs, want exactly 1"
[[ "$job_completions" == 1 ]] || fail "preflight Job Completed events = $job_completions, want exactly 1"
log "preflight Job lifecycle: 1 UID, 1 completion"

# -------------------------------------------------------- terminal acceptance
log "=== Phase F: terminal acceptance evidence ==="
final="$(app_json)"
for check in \
  '.opPhase == "Succeeded"' \
  '.syncStatus == "Synced"' \
  '.healthStatus == "Healthy"' \
  '.hasTopLevelOperation == false' \
  '.phase == "awaiting-approval"' \
  '.targetRevision == "'"$EXPECTED_REVISION"'"' \
  '.sourceExactMatch' \
  '.destinationExactMatch' \
  '.resultSourceExactMatch'; do
  [[ "$(jq -r "$check" <<<"$final")" == true ]] || fail "terminal acceptance check failed: $check ($(jq -c '{phase,syncStatus,healthStatus,opPhase,sourceExactMatch,destinationExactMatch,resultSourceExactMatch}' <<<"$final"))"
done
final_hash="$(jq -r '.hash' <<<"$final")"
[[ "$final_hash" == "$hash" ]] || fail "spec hash changed during acceptance ($hash -> $final_hash)"

secret_evidence > "$OUT_DIR/secrets.after"
pvc_evidence > "$OUT_DIR/pvc.after"
chmod 0600 "$OUT_DIR/secrets.after" "$OUT_DIR/pvc.after"
cmp -s "$OUT_DIR/secrets.before" "$OUT_DIR/secrets.after" || fail "protected Secret evidence changed"
cmp -s "$OUT_DIR/pvc.before" "$OUT_DIR/pvc.after" || fail "PVC evidence changed"
log "protected Secret and PVC evidence byte-for-byte unchanged"

# ------------------------------------------------------------------ approval
if [[ "$SKIP_APPROVAL" == 1 ]]; then
  log "SKIP_APPROVAL=1: stopping after terminal acceptance evidence"
else
  log "=== Phase G: exact-hash approval and activation ==="
  $K annotate application "$APP_NAME" --overwrite "$APPROVE_ANNOTATION=$final_hash" >/dev/null
  activated=0
  deadline=$((SECONDS + READY_TIMEOUT))
  while (( SECONDS < deadline )); do
    state="$(app_json)"
    if [[ "$(jq -r '.phase' <<<"$state")" == active ]]; then
      automated="$(jq -c '.syncPolicy.automated' <<<"$state")"
      [[ "$automated" == '{"prune":true,"selfHeal":true}' ]] || fail "activation armed unexpected sync policy: $automated"
      activated=1
      break
    fi
    sleep 5
  done
  [[ "$activated" == 1 ]] || fail "application did not reach active phase after exact-hash approval"
  log "application active with reviewed automated prune/self-heal"
fi

# ----------------------------------------------------------------- final gate
log "=== Phase H: final containment state ==="
workloads_ready || fail "management workloads not Ready at end of run"
final_running="$(sql "select count(*) from argocd_operations where status in ('running','pending')")"
[[ "$final_running" == 0 ]] || fail "$final_running durable operations still running/pending"
final_dirty="$(sql 'select dirty from schema_migrations')"
[[ "$final_dirty" == f ]] || fail "schema dirty at end of run"
final_replicas="$($K get statefulset "$CONTROLLER" -o jsonpath='{.spec.replicas}')"
[[ "$final_replicas" == 1 ]] || fail "controller desired replicas = $final_replicas at GO, want 1"

GO_STATE=GO
FINISHED=1
{
  echo "$GO_STATE"
  echo "operation_id=$OP_ID"
  echo "operation_status=$op_status"
  echo "attempt_count=$attempt_count"
  echo "poll_count=$POLLS"
  echo "upstream_sync_calls=$upstream_calls"
  echo "preflight_job_lifecycles=$job_lifecycles"
  echo "window=$SUBMIT_TS..$COMPLETE_TS"
  echo "schema_version=$schema_version"
  echo "controller_replicas=$final_replicas"
  echo "approval=$([[ "$SKIP_APPROVAL" == 1 ]] && echo skipped || echo applied)"
} > "$RESULT_FILE"
log "RESULT: $GO_STATE (details in $RESULT_FILE)"
