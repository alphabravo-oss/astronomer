#!/usr/bin/env bash
# Run the Plan 007/W11-04 delivery acceptance matrix using public HTTPS API,
# CLI, and UI-route surfaces only.
#
# The matrix is deliberately external: source credentials, trust roots, mirror
# endpoints, failure injection, backup/restore, and browser automation belong
# to the acceptance environment, not this repository. Secret values are
# referenced as {"$env":"VARIABLE"}; they must never be placed in the matrix.
#
# Usage:
#   ./scripts/validate-live-delivery.sh --check --matrix matrix.json
#   BASE_URL=https://acceptance.example.test \
#   LIVE_DELIVERY_ENVIRONMENT=acceptance \
#   LIVE_DELIVERY_CONFIRM=acceptance.example.test \
#   AUTH_TOKEN=... \
#     ./scripts/validate-live-delivery.sh --matrix matrix.json
#
# A mutating step must either create an owned fixture and register cleanup:
#   "creates_owned":"source_id", "capture":{"source_id":".data.id"},
#   "cleanup":{"method":"DELETE","path":"/api/v1/delivery/sources/{{source_id}}/?project_id={{project_id}}","owned_ref":"source_id","expect_status":[204]}
# or name a previously captured owned fixture with "owned_ref":"source_id".
# Cleanup requests are registered only after successful ownership capture and
# are replayed in reverse order on success, failure, or interruption.

set -euo pipefail

readonly SCRIPT_NAME="$(basename "$0")"
readonly MAX_RESPONSE_BYTES=$((4 * 1024 * 1024))
readonly DEFAULT_STEP_TIMEOUT=30
readonly REQUIRED_COVERAGE_CSV='source.git,source.oci_artifact,source.helm_http,source.helm_oci,renderer.kustomize,renderer.helm,auth.none,auth.basic,auth.bearer,auth.ssh,auth.workload_identity,trust.unsigned,trust.cosign_key,trust.cosign_keyless,trust.git_signature,trust.verification_failure,path.online,path.air_gap,placement.cluster,placement.group,placement.label,placement.change,strategy.all_at_once,strategy.rolling,strategy.canary,strategy.partitioned,gate.approval,gate.maintenance_window,control.pause,control.resume,control.abort,control.retry,control.rollback,resilience.drift_detect,resilience.drift_repair,resilience.disconnect,resilience.reconnect,resilience.controller_restart,resilience.controller_upgrade,resilience.controller_rollback,resilience.credential_rotation,resilience.credential_revocation,lifecycle.delete,lifecycle.orphan,authorization.project_denial,backup.restore,surface.ui,surface.cli,surface.sse,surface.metrics,surface.alerts,surface.runbook'

MATRIX_FILE="${DELIVERY_MATRIX_FILE:-}"
EVIDENCE_ROOT="${EVIDENCE_ROOT:-${PWD}/artifacts/live-delivery}"
CHECK_ONLY=0
LIST_REQUIREMENTS=0
BASE_URL="${BASE_URL:-}"
AUTH_TOKEN="${AUTH_TOKEN:-}"
ASTRO_USERNAME="${ASTRO_USERNAME:-}"
ASTRO_PASSWORD="${ASTRO_PASSWORD:-}"
ASTRO_BIN="${ASTRO_BIN:-astro}"
LIVE_DELIVERY_ENVIRONMENT="${LIVE_DELIVERY_ENVIRONMENT:-}"
LIVE_DELIVERY_CONFIRM="${LIVE_DELIVERY_CONFIRM:-}"
ALLOW_INSECURE_LOCALHOST="${ALLOW_INSECURE_LOCALHOST:-0}"
RUN_PREFIX="${RUN_PREFIX:-}"

STATE_DIR=""
VARS_FILE=""
EVENTS_FILE=""
EVIDENCE_FILE=""
RUN_STARTED=""
OVERALL_STATUS="not_started"
CURRENT_SCENARIO=""
CURRENT_STEP=""
LAST_METHOD=""
LAST_PATH=""
LAST_HTTP_STATUS=""
LAST_ATTEMPT=0
declare -a CLEANUPS=()
declare -A OWNED=()

usage() {
  cat <<EOF
Usage: ${SCRIPT_NAME} --matrix FILE [--check] [--evidence-dir DIR]
       ${SCRIPT_NAME} --list-requirements

--check              Validate the matrix and local prerequisites; no network calls.
--evidence-dir DIR   Parent directory for the private, run-specific evidence directory.
--list-requirements  Print the exact W11-04 coverage identifiers as JSON.
EOF
}

die() {
  printf '%s\n' "${SCRIPT_NAME}: $*" >&2
  exit 1
}

require_tool() {
  command -v "$1" >/dev/null 2>&1 || die "missing required tool: $1"
}

json_event() {
  local kind="$1" status="$2" code="$3" detail="${4:-}"
  [[ -n "${EVENTS_FILE}" ]] || return 0
  jq -cn \
    --arg time "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    --arg kind "${kind}" --arg status "${status}" --arg code "${code}" \
    --arg scenario "${CURRENT_SCENARIO}" --arg step "${CURRENT_STEP}" \
    --arg detail "${detail}" \
    '{time:$time,kind:$kind,status:$status,code:$code}
     + (if $scenario == "" then {} else {scenario:$scenario} end)
     + (if $step == "" then {} else {step:$step} end)
     + (if $detail == "" then {} else {detail:$detail} end)' >>"${EVENTS_FILE}"
}

json_step_event() {
  local status="$1" code="$2" transport="$3" proofs="$4"
  jq -cn \
    --arg time "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    --arg status "${status}" --arg code "${code}" --arg scenario "${CURRENT_SCENARIO}" \
    --arg step "${CURRENT_STEP}" --arg transport "${transport}" \
    --arg method "${LAST_METHOD}" --arg route "${LAST_PATH}" \
    --arg http_status "${LAST_HTTP_STATUS}" --argjson attempt "${LAST_ATTEMPT}" \
    --argjson proves "${proofs}" \
    '{time:$time,kind:"step",status:$status,code:$code,scenario:$scenario,step:$step,
      transport:$transport,proves:$proves}
     + (if $transport == "http" then
          {method:$method,route:$route,attempt:$attempt}
          + (if ($http_status|test("^[0-9]{3}$")) then {http_status:($http_status|tonumber)} else {} end)
        else {} end)' >>"${EVENTS_FILE}"
}

finalize_evidence() {
  local rc="$1" finished status
  [[ -n "${EVIDENCE_FILE}" && -n "${EVENTS_FILE}" && -f "${EVENTS_FILE}" ]] || return 0
  finished="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  status="${OVERALL_STATUS}"
  if [[ "${status}" == "running" || "${status}" == "not_started" ]]; then
    status="failed"
  fi
  jq -n \
    --arg schema_version "1" --arg run_prefix "${RUN_PREFIX}" \
    --arg started_at "${RUN_STARTED}" --arg finished_at "${finished}" \
    --arg status "${status}" --argjson exit_code "${rc}" \
    --arg candidate_version "$(jq -r '.candidate.version // ""' "${MATRIX_FILE}" 2>/dev/null || true)" \
    --arg candidate_commit "$(jq -r '.candidate.commit // ""' "${MATRIX_FILE}" 2>/dev/null || true)" \
    --arg release_digest "$(jq -r '.candidate.release_manifest_digest // ""' "${MATRIX_FILE}" 2>/dev/null || true)" \
    --arg suite_id "$(jq -r '.run.suite_id // ""' "${MATRIX_FILE}" 2>/dev/null || true)" \
    --argjson run_sequence "$(jq -r '.run.sequence // 0' "${MATRIX_FILE}" 2>/dev/null || printf '0')" \
    --argjson run_total "$(jq -r '.run.total // 0' "${MATRIX_FILE}" 2>/dev/null || printf '0')" \
    --argjson clusters "$(jq -c '[.clusters[]|{id,mode,fresh,owned}]' "${MATRIX_FILE}" 2>/dev/null || printf '[]')" \
    --arg required_coverage "${REQUIRED_COVERAGE_CSV}" \
    --argjson declared_coverage "$(jq -c '[.scenarios[].steps[].proves[]] | unique | sort' "${MATRIX_FILE}" 2>/dev/null || printf '[]')" \
    --slurpfile events "${EVENTS_FILE}" \
    '{schema_version:($schema_version|tonumber),run_prefix:$run_prefix,
      started_at:$started_at,finished_at:$finished_at,status:$status,exit_code:$exit_code,
      run:{suite_id:$suite_id,sequence:$run_sequence,total:$run_total},clusters:$clusters,
      candidate:{version:$candidate_version,commit:$candidate_commit,release_manifest_digest:$release_digest},
      coverage:{required:($required_coverage|split(",")|sort),declared:$declared_coverage},
      events:$events,
      summary:{passed:([$events[]|select(.status=="passed")]|length),
               failed:([$events[]|select(.status=="failed")]|length),
               blocked:([$events[]|select(.status=="blocked")]|length)}}' >"${EVIDENCE_FILE}.tmp" &&
    mv "${EVIDENCE_FILE}.tmp" "${EVIDENCE_FILE}"
  chmod 0600 "${EVIDENCE_FILE}" 2>/dev/null || true
}

render_text() {
  local value="$1" token key replacement
  [[ "${value}" != *'{{env:'* ]] || return 1
  while [[ "${value}" =~ \{\{([a-zA-Z][a-zA-Z0-9_]*)\}\} ]]; do
    token="${BASH_REMATCH[0]}"
    key="${BASH_REMATCH[1]}"
    replacement="$(jq -er --arg key "${key}" '.[$key] | strings' "${VARS_FILE}")" || return 1
    [[ "${replacement}" =~ ^[-._~A-Za-z0-9]+$ ]] || return 1
    value="${value//${token}/${replacement}}"
  done
  [[ "${value}" != *'{{'* && "${value}" != *'}}'* ]] || return 1
  printf '%s' "${value}"
}

render_json() {
  local compact="$1"
  jq -c --argjson vars "$(<"${VARS_FILE}")" --arg run_prefix "${RUN_PREFIX}" '
    def render:
      if type == "object" and (keys == ["$env"]) then
        .["$env"] as $name | if (env[$name] // "") == "" then error("missing secret environment reference") else env[$name] end
      elif type == "object" and (keys == ["$var"]) then
        .["$var"] as $name | if $vars[$name] == null then error("missing captured variable") else $vars[$name] end
      elif type == "array" then map(render)
      elif type == "object" then with_entries(.value |= render)
      elif type == "string" then gsub("\\{\\{run_prefix\\}\\}"; $run_prefix)
      else . end;
    render' <<<"${compact}"
}

capture_values() {
  local response_file="$1" captures="$2" key selector value
  [[ "${captures}" != "null" ]] || return 0
  while IFS= read -r key; do
    [[ "${key}" =~ ^[a-zA-Z][a-zA-Z0-9_]*$ ]] || return 1
    [[ ! "${key}" =~ (token|password|credential|secret|private|manifest|content|body|url)$ ]] || return 1
    selector="$(jq -r --arg key "${key}" '.[$key]' <<<"${captures}")"
    [[ "${selector}" =~ ^\.([a-zA-Z][a-zA-Z0-9_]*)(\.[a-zA-Z][a-zA-Z0-9_]*)*$ ]] || return 1
    value="$(jq -er "${selector} | scalars" "${response_file}")" || return 1
    [[ "${value}" != *$'\n'* && ${#value} -le 2048 ]] || return 1
    jq -c --arg key "${key}" --arg value "${value}" '. + {($key):$value}' "${VARS_FILE}" >"${VARS_FILE}.tmp"
    mv "${VARS_FILE}.tmp" "${VARS_FILE}"
  done < <(jq -r 'keys[]' <<<"${captures}")
}

validate_path() {
  local path="$1"
  [[ "${path}" == /* ]] || return 1
  [[ "${path}" != *'://'* && "${path}" != *'..'* && "${path}" != *$'\n'* ]] || return 1
  [[ ! "${path}" =~ (^|[?&])(token|access_token|password|credential|secret|private_key)= ]] || return 1
}

http_request() {
  local method="$1" path="$2" body="$3" timeout_seconds="$4" idempotency_key="$5" if_match="${6:-}"
  local response_file="$7" headers_file="$8" status_file="$9"
  local curl_rc=0
  local -a args
  validate_path "${path}" || return 90
  args=(--silent --show-error --location --max-redirs 3 --connect-timeout 10
        --max-time "${timeout_seconds}" --max-filesize "${MAX_RESPONSE_BYTES}"
        --output "${response_file}" --dump-header "${headers_file}" --write-out '%{http_code}'
        --request "${method}" --header "Authorization: Bearer ${AUTH_TOKEN}"
        --header 'Accept: application/json')
  if [[ "${method}" != "GET" && "${method}" != "HEAD" && "${method}" != "OPTIONS" ]]; then
    args+=(--header "Idempotency-Key: ${idempotency_key}")
  fi
  [[ -z "${if_match}" ]] || args+=(--header "If-Match: ${if_match}")
  if [[ "${body}" != "null" ]]; then
    args+=(--header 'Content-Type: application/json' --data-binary @-)
    printf '%s' "${body}" | curl "${args[@]}" "${BASE_URL%/}${path}" >"${status_file}" || curl_rc=$?
  else
    curl "${args[@]}" "${BASE_URL%/}${path}" >"${status_file}" || curl_rc=$?
  fi
  [[ "$(wc -c <"${response_file}")" -le "${MAX_RESPONSE_BYTES}" ]] || return 91
  return "${curl_rc}"
}

status_expected() {
  local status="$1" expected="$2"
  jq -e --argjson status "${status}" 'index($status) != null' <<<"${expected}" >/dev/null
}

run_http_step() {
  local scenario_id="$1" step_json="$2"
  local step_id method path body response_kind assertion expected attempts interval timeout_seconds
  local if_match_from if_match idempotency_key response_file headers_file status_file status attempt=1 curl_rc=0
  step_id="$(jq -r '.id' <<<"${step_json}")"
  method="$(jq -r '.method // "GET" | ascii_upcase' <<<"${step_json}")"
  path="$(render_text "$(jq -r '.path' <<<"${step_json}")")" || return 92
  LAST_METHOD="${method}"
  LAST_PATH="${path%%\?*}"
  body="$(render_json "$(jq -c '.body // null' <<<"${step_json}")")" || return 93
  response_kind="$(jq -r '.response // "json"' <<<"${step_json}")"
  assertion="$(jq -r '.assert // "true"' <<<"${step_json}")"
  expected="$(jq -c '.expect_status // [200]' <<<"${step_json}")"
  attempts="$(jq -r '.poll.attempts // 1' <<<"${step_json}")"
  interval="$(jq -r '.poll.interval_seconds // 1' <<<"${step_json}")"
  timeout_seconds="$(jq -r ".timeout_seconds // ${DEFAULT_STEP_TIMEOUT}" <<<"${step_json}")"
  if_match_from="$(jq -r '.if_match_from // ""' <<<"${step_json}")"
  if_match=""
  if [[ -n "${if_match_from}" ]]; then
    if_match="$(jq -er --arg key "${if_match_from}" '.[$key] | strings' "${VARS_FILE}")" || return 94
  fi
  response_file="${STATE_DIR}/response.json"
  headers_file="${STATE_DIR}/headers"
  status_file="${STATE_DIR}/status"
  idempotency_key="$(tr -cd 'a-zA-Z0-9._-' <<<"${RUN_PREFIX}-${scenario_id}-${step_id}" | cut -c1-120)"

  while (( attempt <= attempts )); do
    : >"${response_file}"; : >"${headers_file}"; : >"${status_file}"
    curl_rc=0
    http_request "${method}" "${path}" "${body}" "${timeout_seconds}" "${idempotency_key}" "${if_match}" \
      "${response_file}" "${headers_file}" "${status_file}" || curl_rc=$?
    status="$(<"${status_file}")"
    LAST_HTTP_STATUS="${status}"
    LAST_ATTEMPT="${attempt}"
    if [[ ( "${curl_rc}" -eq 0 || ( "${curl_rc}" -eq 28 && "$(jq -r '.allow_timeout // false' <<<"${step_json}")" == "true" ) ) && "${status}" =~ ^[0-9]{3}$ ]] && status_expected "${status}" "${expected}"; then
      if [[ "${response_kind}" == "json" ]] && jq -e "${assertion}" "${response_file}" >/dev/null 2>&1; then
        capture_values "${response_file}" "$(jq -c '.capture // null' <<<"${step_json}")" || return 95
        return 0
      elif [[ "${response_kind}" == "text" ]]; then
        local text_ok=1 needle
        while IFS= read -r needle; do
          grep -Fq -- "$(render_text "${needle}")" "${response_file}" || text_ok=0
        done < <(jq -r '.expect_contains[]?' <<<"${step_json}")
        [[ "${text_ok}" -eq 1 ]] && return 0
      fi
    fi
    (( attempt == attempts )) && break
    sleep "${interval}"
    attempt=$((attempt + 1))
  done
  return 96
}

run_cli_step() {
  local step_json="$1" timeout_seconds output_file
  local -a argv
  timeout_seconds="$(jq -r ".timeout_seconds // ${DEFAULT_STEP_TIMEOUT}" <<<"${step_json}")"
  mapfile -t argv < <(jq -r '.argv[]' <<<"${step_json}")
  [[ "${#argv[@]}" -gt 1 && "${argv[0]}" == "delivery" ]] || return 97
  local i
  for i in "${!argv[@]}"; do
    argv[$i]="$(render_text "${argv[$i]}")" || return 97
  done
  output_file="${STATE_DIR}/cli.json"
  : >"${output_file}"
  ASTRO_API_TOKEN="${AUTH_TOKEN}" timeout "${timeout_seconds}" "${ASTRO_BIN}" \
    --server "${BASE_URL}" --output json "${argv[@]}" >"${output_file}" 2>/dev/null || return 98
  [[ "$(wc -c <"${output_file}")" -le "${MAX_RESPONSE_BYTES}" ]] || return 98
  jq -e "$(jq -r '.assert // "true"' <<<"${step_json}")" "${output_file}" >/dev/null 2>&1 || return 98
  capture_values "${output_file}" "$(jq -c '.capture // null' <<<"${step_json}")" || return 98
}

run_cleanup() {
  local cleanup_json="$1" method path body timeout_seconds if_match_from if_match response headers status_file status expected
  method="$(jq -r '.method | ascii_upcase' <<<"${cleanup_json}")"
  path="$(render_text "$(jq -r '.path' <<<"${cleanup_json}")")" || return 1
  body="$(render_json "$(jq -c '.body // null' <<<"${cleanup_json}")")" || return 1
  timeout_seconds="$(jq -r ".timeout_seconds // ${DEFAULT_STEP_TIMEOUT}" <<<"${cleanup_json}")"
  if_match_from="$(jq -r '.if_match_from // ""' <<<"${cleanup_json}")"
  if_match=""
  [[ -z "${if_match_from}" ]] || if_match="$(jq -r --arg key "${if_match_from}" '.[$key] // ""' "${VARS_FILE}")"
  response="${STATE_DIR}/cleanup-response"; headers="${STATE_DIR}/cleanup-headers"; status_file="${STATE_DIR}/cleanup-status"
  : >"${response}"; : >"${headers}"; : >"${status_file}"
  http_request "${method}" "${path}" "${body}" "${timeout_seconds}" "${RUN_PREFIX}-cleanup" "${if_match}" \
    "${response}" "${headers}" "${status_file}" >/dev/null 2>&1 || return 1
  status="$(<"${status_file}")"
  expected="$(jq -c '.expect_status // [200,202,204,404]' <<<"${cleanup_json}")"
  status_expected "${status}" "${expected}"
}

cleanup_and_exit() {
  local rc="$?" i cleanup_fail=0
  trap - EXIT INT TERM HUP
  set +e
  for ((i=${#CLEANUPS[@]}-1; i>=0; i--)); do
    CURRENT_SCENARIO="cleanup"; CURRENT_STEP="fixture-$((i+1))"
    if run_cleanup "${CLEANUPS[$i]}"; then
      json_event cleanup passed cleanup_succeeded
    else
      cleanup_fail=1
      json_event cleanup failed cleanup_failed
    fi
  done
  if [[ "${cleanup_fail}" -ne 0 ]]; then
    OVERALL_STATUS="failed"
    [[ "${rc}" -ne 0 ]] || rc=1
  fi
  finalize_evidence "${rc}"
  [[ -z "${STATE_DIR}" ]] || rm -rf -- "${STATE_DIR}"
  exit "${rc}"
}

on_signal() {
  local rc="$1"
  OVERALL_STATUS="failed"
  json_event run failed interrupted
  exit "${rc}"
}

validate_matrix() {
  local missing unknown secret_env
  jq -e --arg required "${REQUIRED_COVERAGE_CSV}" '
    def sensitive: test("(?i)(password|token|credential|secret|private|manifest|content|body|url)$");
    def method($s): ($s.method // "GET" | ascii_upcase);
    def has($s;$fragment): (($s.path // "") | contains($fragment));
    def proof_ok($s;$proof):
      if $proof == "source.git" then method($s)=="POST" and has($s;"/delivery/sources") and $s.body.type=="git"
      elif $proof == "source.oci_artifact" then method($s)=="POST" and has($s;"/delivery/sources") and $s.body.type=="oci_artifact"
      elif $proof == "source.helm_http" then method($s)=="POST" and has($s;"/delivery/sources") and $s.body.type=="helm_http"
      elif $proof == "source.helm_oci" then method($s)=="POST" and has($s;"/delivery/sources") and $s.body.type=="helm_oci"
      elif ($proof|startswith("auth.")) then method($s)=="POST" and has($s;"/delivery/sources") and $s.body.auth_mode==($proof|split(".")[1])
      elif $proof == "trust.unsigned" then method($s)=="POST" and has($s;"/delivery/sources") and $s.body.trust_policy.allow_unsigned==true
      elif $proof == "trust.cosign_key" then method($s)=="POST" and has($s;"/delivery/sources") and $s.body.trust_policy.provider=="cosign_key"
      elif $proof == "trust.cosign_keyless" then method($s)=="POST" and has($s;"/delivery/sources") and $s.body.trust_policy.provider=="cosign_keyless"
      elif $proof == "trust.git_signature" then method($s)=="POST" and has($s;"/delivery/sources") and $s.body.trust_policy.provider=="git"
      elif $proof == "trust.verification_failure" then has($s;"/delivery/") and has($s;"/versions/") and (($s.assert // "")|contains("failed"))
      elif $proof == "renderer.kustomize" then method($s)=="POST" and has($s;"/versions/") and $s.body.spec.renderer.kind=="kustomize"
      elif $proof == "renderer.helm" then method($s)=="POST" and has($s;"/versions/") and $s.body.spec.renderer.kind=="helm"
      elif ($proof|startswith("strategy.")) then method($s)=="POST" and has($s;"/rollouts/") and $s.body.strategy.type==($proof|split(".")[1])
      elif $proof == "placement.cluster" then ($s.body.placement.cluster_ids|length)>0
      elif $proof == "placement.group" then ($s.body.placement.cluster_group_ids|length)>0
      elif $proof == "placement.label" then (($s.body.placement.match_labels|length)>0 or ($s.body.placement.match_expressions|length)>0)
      elif $proof == "placement.change" then method($s)=="PATCH" and has($s;"/delivery/targets/") and ($s.body.placement|type)=="object"
      elif $proof == "gate.approval" then method($s)=="POST" and has($s;"/approve/")
      elif $proof == "gate.maintenance_window" then ($s.body.maintenance_window_policy|type)=="object" or $s.body.strategy.respect_maintenance_windows==true
      elif ($proof|startswith("control.")) then method($s)=="POST" and has($s;("/"+($proof|split(".")[1])+"/"))
      elif $proof == "resilience.credential_rotation" then method($s)=="POST" and has($s;"/rotate-credential/")
      elif $proof == "resilience.credential_revocation" then method($s)=="POST" and has($s;"/revoke")
      elif $proof == "lifecycle.delete" then method($s)=="DELETE" and has($s;"/delivery/")
      elif $proof == "lifecycle.orphan" then method($s)=="POST" and has($s;"/orphan/")
      elif $proof == "authorization.project_denial" then (($s.expect_status // []) | index(403) != null or index(404) != null)
      elif $proof == "backup.restore" then method($s)=="POST" and has($s;"/restore")
      elif $proof == "surface.cli" then ($s.kind // "http")=="cli"
      elif $proof == "surface.ui" then ($s.kind // "http")=="http" and method($s)=="GET" and ($s.response // "json")=="text" and (has($s;"/api/v1/")|not)
      elif $proof == "surface.sse" then has($s;"/api/v1/events/stream/") and ($s.response // "json")=="text" and $s.allow_timeout==true
      elif $proof == "surface.metrics" then method($s)=="GET" and has($s;"metrics")
      elif $proof == "surface.alerts" then method($s)=="GET" and has($s;"alert")
      elif $proof == "surface.runbook" then method($s)=="GET" and has($s;"runbook")
      elif $proof == "resilience.drift_detect" or $proof == "resilience.drift_repair" then has($s;"drift")
      elif $proof == "resilience.disconnect" then has($s;"disconnect")
      elif $proof == "resilience.reconnect" then has($s;"reconnect")
      elif $proof == "resilience.controller_restart" then has($s;"restart")
      elif $proof == "resilience.controller_upgrade" then has($s;"/delivery/system/rollouts")
      elif $proof == "resilience.controller_rollback" then has($s;"/delivery/system/rollouts") and has($s;"/rollback/")
      else true end;
    . as $matrix |
    .schema_version == 1 and
    (.candidate.version | type == "string" and test("^[a-zA-Z0-9._+-]{1,128}$")) and
    (.candidate.commit | type == "string" and test("^[a-fA-F0-9]{7,64}$")) and
    (.candidate.release_manifest_digest | test("^sha256:[a-f0-9]{64}$")) and
    (.run.suite_id | type=="string" and test("^[a-zA-Z0-9._-]{8,128}$")) and
    (.run.sequence | type=="number" and .>=1 and .<=3) and
    (.run.total == 3) and
    (.clusters | type == "array" and length == 2) and
    (([.clusters[].mode] | sort) == ["air_gap","online"]) and
    (all(.clusters[]; .fresh == true and .owned == true and (.id | test("^[0-9a-fA-F-]{36}$")))) and
    (.variables | type == "object" and all(to_entries[];
      (.key|test("^[a-zA-Z][a-zA-Z0-9_]*$") and (test("(?i)(token|password|credential|secret|private|key)$")|not)) and
      (.value|type=="string" and test("^[-._~A-Za-z0-9]{1,2048}$")))) and
    (.secret_env | type == "array" and all(.; length == (unique|length)) and
      all(.[]; test("^[A-Z][A-Z0-9_]*$") and
        (. != "AUTH_TOKEN" and . != "ASTRO_PASSWORD" and . != "ASTRO_USERNAME"))) and
    (.scenarios | type == "array" and length > 0) and
    (all(.scenarios[];
      (.id | test("^[a-z0-9][a-z0-9._-]*$")) and
      (.covers | type == "array" and length > 0) and
      (.steps | type == "array" and length > 0) and
      ((.covers|unique|sort) == ([.steps[].proves[]]|unique|sort)) and
      all(.steps[];
        (.id | test("^[a-z0-9][a-z0-9._-]*$")) and
        ((.kind // "http") == "http" or .kind == "cli") and
        (.proves | type == "array" and length > 0 and all(.[]; type=="string")) and
        (. as $step | all(.proves[]; proof_ok($step;.))) and
        ((.response // "json") == "json" or (.response // "json") == "text") and
        (if (.response // "json") == "text" then
           (.expect_contains | type=="array" and length>0 and all(.[]; type=="string" and length>0))
         else true end) and
        ((.timeout_seconds // 30) >= 1 and (.timeout_seconds // 30) <= 300) and
        ((.poll.attempts // 1) >= 1 and (.poll.attempts // 1) <= 600) and
        ((.poll.interval_seconds // 1) >= 0 and (.poll.interval_seconds // 1) <= 60) and
        ((.expect_status // [200]) | type=="array" and length>0 and all(.[]; type=="number" and .>=100 and .<=599)) and
        ((.assert // "true") | type=="string" and length>0) and
        (if (.kind // "http") == "http" then
          (.method // "GET" | ascii_upcase) as $method |
          (.path | type == "string" and startswith("/") and (contains("://")|not) and (contains("..")|not)) and
          (if ($method == "GET" or $method == "HEAD" or $method == "OPTIONS") then true
           elif .creates_owned then
             (.creates_owned | type=="string" and test("^[a-zA-Z][a-zA-Z0-9_]*$")) and
             (.capture[.creates_owned] | type=="string" and length>0) and
             ([.body.name?,.body.version?] | map(select(type=="string" and contains("{{run_prefix}}"))) | length>0) and
             (.cleanup | type=="object") and
             (.cleanup.method | type=="string" and test("^(DELETE|POST|PATCH)$";"i")) and
             (.cleanup.path | type=="string" and startswith("/")) and
             (.cleanup.owned_ref | type=="string" and length>0)
           else (.owned_ref | type == "string" and length > 0) end)
         else (. as $cli | ($cli.argv | type == "array" and length > 2) and $cli.argv[0] == "delivery" and
               (($cli.argv[1] == "source" and ($cli.argv[2] == "list" or $cli.argv[2] == "get")) or
                ($cli.argv[1] == "bundle" and ($cli.argv[2] == "list" or $cli.argv[2] == "get" or $cli.argv[2] == "version-list" or $cli.argv[2] == "version-get")))) end) and
        (all((.capture // {}) | keys[]; sensitive | not)) and
        (all((.capture // {})[]; type=="string" and test("^\\.([a-zA-Z][a-zA-Z0-9_]*)(\\.[a-zA-Z][a-zA-Z0-9_]*)*$")))
      )
    )) and
    (([.scenarios[].steps[].creates_owned? // empty] | length) == ([.scenarios[].steps[].creates_owned? // empty] | unique | length)) and
    (all(.scenarios[].steps[]; . as $step | all(.proves[];
      if . == "path.online" then
        $step.cluster_mode=="online" and ($step.cluster_id == ($matrix.clusters[]|select(.mode=="online")|.id)) and has($step;$step.cluster_id)
      elif . == "path.air_gap" then
        $step.cluster_mode=="air_gap" and ($step.cluster_id == ($matrix.clusters[]|select(.mode=="air_gap")|.id)) and has($step;$step.cluster_id)
      else true end)))
    ' "${MATRIX_FILE}" >/dev/null || die "matrix schema, ownership, or safety validation failed"

  missing="$(jq -nr --arg required "${REQUIRED_COVERAGE_CSV}" --slurpfile matrix "${MATRIX_FILE}" '
    ($required|split(",")) - ([$matrix[0].scenarios[].covers[]]|unique) | join(",")')"
  unknown="$(jq -nr --arg required "${REQUIRED_COVERAGE_CSV}" --slurpfile matrix "${MATRIX_FILE}" '
    ([$matrix[0].scenarios[].covers[]]|unique) - ($required|split(",")) | join(",")')"
  [[ -z "${missing}" ]] || die "matrix is missing required coverage: ${missing}"
  [[ -z "${unknown}" ]] || die "matrix contains unknown coverage identifiers: ${unknown}"

  jq -e '
    ([.. | objects | select(keys == ["$env"]) | .["$env"]] | unique) as $refs |
    (.secret_env | unique) as $declared |
    ($refs == $declared)' "${MATRIX_FILE}" >/dev/null ||
    die "secret_env must exactly list all {\"\u0024env\":\"VARIABLE\"} references"

  while IFS= read -r secret_env; do
    [[ -n "${!secret_env:-}" ]] || { [[ "${CHECK_ONLY}" -eq 1 ]] || die "required secret environment variable is unset: ${secret_env}"; }
  done < <(jq -r '.secret_env[]' "${MATRIX_FILE}")

  # Sensitive request fields must be environment references, never literals.
  jq -e '
    [paths(objects) as $p |
      (getpath($p)) as $o |
      $o | to_entries[]? |
      select(.key | test("(?i)(password|token|secret|private_key|passphrase|ca_bundle)$")) |
      .value] |
    all(.[]; type == "object" and keys == ["$env"])' "${MATRIX_FILE}" >/dev/null ||
    die "sensitive matrix fields must contain only {\"\u0024env\":\"VARIABLE\"} references"
}

while [[ "$#" -gt 0 ]]; do
  case "$1" in
    --matrix) [[ "$#" -ge 2 ]] || die "--matrix requires a file"; MATRIX_FILE="$2"; shift 2 ;;
    --evidence-dir) [[ "$#" -ge 2 ]] || die "--evidence-dir requires a directory"; EVIDENCE_ROOT="$2"; shift 2 ;;
    --check) CHECK_ONLY=1; shift ;;
    --list-requirements) LIST_REQUIREMENTS=1; shift ;;
    --help|-h) usage; exit 0 ;;
    *) die "unknown argument: $1" ;;
  esac
done

require_tool jq
if [[ "${LIST_REQUIREMENTS}" -eq 1 ]]; then
  jq -cn --arg required "${REQUIRED_COVERAGE_CSV}" '$required | split(",")'
  exit 0
fi

[[ -n "${MATRIX_FILE}" && -f "${MATRIX_FILE}" ]] || die "--matrix must name a readable JSON file"
jq -e . "${MATRIX_FILE}" >/dev/null || die "matrix is not valid JSON"
validate_matrix

if [[ "${CHECK_ONLY}" -eq 1 ]]; then
  printf '%s\n' "delivery matrix preflight passed (network was not contacted)"
  exit 0
fi

require_tool curl
require_tool timeout
command -v "${ASTRO_BIN}" >/dev/null 2>&1 || die "ASTRO_BIN is not executable: ${ASTRO_BIN}"
[[ "${LIVE_DELIVERY_ENVIRONMENT}" =~ ^(development|test|staging|acceptance)$ ]] ||
  die "LIVE_DELIVERY_ENVIRONMENT must be development, test, staging, or acceptance"
[[ -n "${BASE_URL}" ]] || die "BASE_URL is required"
base_host="$(sed -E 's#^[a-zA-Z]+://([^/:]+).*#\1#' <<<"${BASE_URL}")"
[[ -n "${base_host}" && "${base_host}" == "${LIVE_DELIVERY_CONFIRM}" ]] ||
  die "LIVE_DELIVERY_CONFIRM must exactly match the BASE_URL hostname"
[[ ! "${base_host}" =~ (^|[.-])(prod|production)([.-]|$) ]] || die "production-like hostnames are refused"
if [[ "${BASE_URL}" != https://* ]]; then
  [[ "${ALLOW_INSECURE_LOCALHOST}" == "1" && "${base_host}" =~ ^(localhost|127\.0\.0\.1|[^.]+\.localtest\.me)$ ]] ||
    die "BASE_URL must use HTTPS (insecure mode is limited to explicit localhost use)"
fi

if [[ -z "${AUTH_TOKEN}" ]]; then
  [[ -n "${ASTRO_USERNAME}" && -n "${ASTRO_PASSWORD}" ]] || die "set AUTH_TOKEN or ASTRO_USERNAME and ASTRO_PASSWORD"
  login_response="$(mktemp)"
  chmod 0600 "${login_response}"
  jq -cn --arg username "${ASTRO_USERNAME}" --arg password "${ASTRO_PASSWORD}" \
    '{username:$username,password:$password}' |
    curl --fail --silent --show-error --max-time 20 --header 'Content-Type: application/json' \
      --data-binary @- "${BASE_URL%/}/api/v1/auth/login/" >"${login_response}" || { rm -f -- "${login_response}"; die "authentication failed"; }
  AUTH_TOKEN="$(jq -er '.data.token | strings | select(length > 0)' "${login_response}")" || die "authentication response carried no token"
  rm -f -- "${login_response}"
fi

if [[ -z "${RUN_PREFIX}" ]]; then
  random_suffix="$(od -An -N4 -tx1 /dev/urandom | tr -d ' \n')"
  RUN_PREFIX="delivery-live-$(date -u +%Y%m%dT%H%M%SZ)-${random_suffix}"
fi
[[ "${RUN_PREFIX}" =~ ^delivery-live-[a-zA-Z0-9-]{8,100}$ ]] || die "RUN_PREFIX must be a unique delivery-live-* identifier"

case "${EVIDENCE_ROOT}" in ""|/|"${PWD}") die "unsafe EVIDENCE_ROOT" ;; esac
umask 077
mkdir -p -- "${EVIDENCE_ROOT}"
evidence_dir="${EVIDENCE_ROOT%/}/${RUN_PREFIX}"
[[ ! -e "${evidence_dir}" ]] || die "run evidence directory already exists: ${evidence_dir}"
mkdir -- "${evidence_dir}"
chmod 0700 "${evidence_dir}"
STATE_DIR="$(mktemp -d "${TMPDIR:-/tmp}/delivery-live.XXXXXXXX")"
chmod 0700 "${STATE_DIR}"
VARS_FILE="${STATE_DIR}/vars.json"
EVENTS_FILE="${evidence_dir}/events.ndjson"
EVIDENCE_FILE="${evidence_dir}/evidence.json"
jq -c --arg run_prefix "${RUN_PREFIX}" '.variables + {run_prefix:$run_prefix}' "${MATRIX_FILE}" >"${VARS_FILE}"
: >"${EVENTS_FILE}"
RUN_STARTED="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
OVERALL_STATUS="running"
trap cleanup_and_exit EXIT
trap 'on_signal 130' INT
trap 'on_signal 143' TERM
trap 'on_signal 129' HUP

json_event run passed preflight_complete
printf '%s\n' "starting delivery acceptance run ${RUN_PREFIX}"

while IFS= read -r scenario; do
  CURRENT_SCENARIO="$(jq -r '.id' <<<"${scenario}")"
  while IFS= read -r step; do
    CURRENT_STEP="$(jq -r '.id' <<<"${step}")"
    kind="$(jq -r '.kind // "http"' <<<"${step}")"
    owned_ref="$(jq -r '.owned_ref // ""' <<<"${step}")"
    if [[ -n "${owned_ref}" && -z "${OWNED[${owned_ref}]:-}" ]]; then
      json_event step blocked owned_fixture_not_created
      die "${CURRENT_SCENARIO}/${CURRENT_STEP}: owned fixture ${owned_ref} was not created by this run"
    fi
    if [[ -n "${owned_ref}" && "$(jq -r '.path // ""' <<<"${step}")" != *"{{${owned_ref}}}"* ]]; then
      json_event step blocked owned_fixture_not_in_path
      die "${CURRENT_SCENARIO}/${CURRENT_STEP}: mutation path is not bound to owned fixture ${owned_ref}"
    fi
    printf '%s\n' "running ${CURRENT_SCENARIO}/${CURRENT_STEP}"
    if [[ "${kind}" == "http" ]]; then
      if ! run_http_step "${CURRENT_SCENARIO}" "${step}"; then
        json_event step failed public_probe_failed
        die "${CURRENT_SCENARIO}/${CURRENT_STEP}: public API assertion failed"
      fi
    else
      if ! run_cli_step "${step}"; then
        json_event step failed cli_probe_failed
        die "${CURRENT_SCENARIO}/${CURRENT_STEP}: CLI assertion failed"
      fi
    fi
    creates_owned="$(jq -r '.creates_owned // ""' <<<"${step}")"
    if [[ -n "${creates_owned}" ]]; then
      owned_value="$(jq -er --arg key "${creates_owned}" '.[$key] | strings | select(length > 0)' "${VARS_FILE}")" ||
        die "${CURRENT_SCENARIO}/${CURRENT_STEP}: ownership capture ${creates_owned} is empty"
      OWNED["${creates_owned}"]="${owned_value}"
      cleanup_ref="$(jq -r '.cleanup.owned_ref // ""' <<<"${step}")"
      [[ -n "${cleanup_ref}" && -n "${OWNED[${cleanup_ref}]:-}" && "$(jq -r '.cleanup.path' <<<"${step}")" == *"{{${cleanup_ref}}}"* ]] ||
        die "${CURRENT_SCENARIO}/${CURRENT_STEP}: cleanup is not bound to a fixture owned by this run"
      CLEANUPS+=("$(jq -c '.cleanup' <<<"${step}")")
    fi
    proof_json="$(jq -c '.proves' <<<"${step}")"
    if [[ "${kind}" == "http" ]]; then
      json_step_event passed assertion_passed http "${proof_json}"
    else
      LAST_METHOD=""; LAST_PATH=""; LAST_HTTP_STATUS=""; LAST_ATTEMPT=0
      json_step_event passed assertion_passed cli "${proof_json}"
    fi
  done < <(jq -c '.steps[]' <<<"${scenario}")
  json_event scenario passed scenario_complete
done < <(jq -c '.scenarios[]' "${MATRIX_FILE}")

CURRENT_SCENARIO=""; CURRENT_STEP=""
OVERALL_STATUS="passed"
json_event run passed matrix_complete
printf '%s\n' "delivery matrix assertions passed; cleanup and evidence finalization will now run"
