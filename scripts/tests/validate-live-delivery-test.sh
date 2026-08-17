#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
SCRIPT="${ROOT_DIR}/scripts/validate-live-delivery.sh"
TEST_DIR="$(mktemp -d)"
cleanup_test_dir() {
  if [[ "${KEEP_TEST_ARTIFACTS:-0}" == "1" ]]; then
    printf 'test artifacts retained at %s\n' "${TEST_DIR}" >&2
  else
    rm -rf -- "${TEST_DIR}"
  fi
}
trap cleanup_test_dir EXIT

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

expect_failure() {
  local pattern="$1"
  shift
  if "$@" >"${TEST_DIR}/stdout" 2>"${TEST_DIR}/stderr"; then
    fail "command unexpectedly succeeded: $*"
  fi
  grep -Fq -- "${pattern}" "${TEST_DIR}/stderr" || {
    sed -n '1,120p' "${TEST_DIR}/stderr" >&2
    fail "failure did not contain: ${pattern}"
  }
}

requirements="$(${SCRIPT} --list-requirements)"
[[ "$(jq 'length' <<<"${requirements}")" -eq 52 ]] || fail "unexpected requirement count"
jq -e 'index("source.git") != null and index("backup.restore") != null and index("surface.sse") != null' \
  <<<"${requirements}" >/dev/null || fail "required coverage identifiers are incomplete"

# Produce a schema-complete matrix without embedding credentials. The URLs for
# disruptive cases intentionally describe public test-control routes; --check
# validates the plan without contacting or mutating any environment.
jq -n --argjson requirements "${requirements}" '
  def common($p): {id:($p|gsub("\\.";"-")),proves:[$p],kind:"http",method:"GET",path:"/api/v1/delivery/system/compatibility/",expect_status:[200],assert:"true"};
  def owned_name($p): "fixture_"+($p|gsub("\\.";"_"));
  def owned_create($p;$path;$body): (owned_name($p)) as $owned | common($p) + {
    method:"POST",path:$path,
    body:($body + (if ($path|contains("/versions/")) then {version:"{{run_prefix}}"} else {name:"{{run_prefix}}"} end)),
    expect_status:[201],capture:{($owned):".data.id"},creates_owned:$owned,
    cleanup:{method:"DELETE",path:("/api/v1/delivery/fixtures/{{"+$owned+"}}/"),owned_ref:$owned,expect_status:[204,404]}
  };
  def owned_action($p;$path;$body): common($p) + {method:"POST",path:$path,body:$body,owned_ref:"fixture_source_git",expect_status:[202]};
  def step($p):
    if ($p|startswith("source.")) then owned_create($p;"/api/v1/delivery/sources/";{type:($p|split(".")[1]),auth_mode:"none",trust_policy:{allow_unsigned:true}})
    elif ($p|startswith("auth.")) then owned_create($p;"/api/v1/delivery/sources/";{type:"git",auth_mode:($p|split(".")[1]),trust_policy:{allow_unsigned:true}})
    elif $p=="trust.unsigned" then owned_create($p;"/api/v1/delivery/sources/";{type:"git",auth_mode:"none",trust_policy:{allow_unsigned:true}})
    elif $p=="trust.cosign_key" then owned_create($p;"/api/v1/delivery/sources/";{type:"oci_artifact",auth_mode:"none",trust_policy:{allow_unsigned:false,provider:"cosign_key",key_ref:"acceptance-key"}})
    elif $p=="trust.cosign_keyless" then owned_create($p;"/api/v1/delivery/sources/";{type:"oci_artifact",auth_mode:"none",trust_policy:{allow_unsigned:false,provider:"cosign_keyless",identity:"acceptance",issuer:"https://issuer.example.test"}})
    elif $p=="trust.git_signature" then owned_create($p;"/api/v1/delivery/sources/";{type:"git",auth_mode:"none",trust_policy:{allow_unsigned:false,provider:"git",key_ref:"acceptance-key"}})
    elif $p=="trust.verification_failure" then common($p)+{path:"/api/v1/delivery/bundles/{{bundle_id}}/versions/failed/",assert:".data.state == \"failed\""}
    elif $p=="renderer.kustomize" then owned_create($p;"/api/v1/delivery/bundles/{{bundle_id}}/versions/";{spec:{renderer:{kind:"kustomize"}}})
    elif $p=="renderer.helm" then owned_create($p;"/api/v1/delivery/bundles/{{bundle_id}}/versions/";{spec:{renderer:{kind:"helm"}}})
    elif ($p|startswith("strategy.")) then owned_action($p;"/api/v1/delivery/targets/{{fixture_source_git}}/rollouts/";{strategy:{type:($p|split(".")[1])}})
    elif $p=="path.online" then common($p)+{path:"/api/v1/delivery/clusters/00000000-0000-4000-8000-000000000001/inventory/",cluster_mode:"online",cluster_id:"00000000-0000-4000-8000-000000000001"}
    elif $p=="path.air_gap" then common($p)+{path:"/api/v1/delivery/clusters/00000000-0000-4000-8000-000000000002/inventory/",cluster_mode:"air_gap",cluster_id:"00000000-0000-4000-8000-000000000002"}
    elif $p=="placement.cluster" then common($p)+{body:{placement:{cluster_ids:["00000000-0000-4000-8000-000000000001"]}}}
    elif $p=="placement.group" then common($p)+{body:{placement:{cluster_group_ids:["00000000-0000-4000-8000-000000000001"]}}}
    elif $p=="placement.label" then common($p)+{body:{placement:{match_labels:{environment:"acceptance"}}}}
    elif $p=="placement.change" then common($p)+{method:"PATCH",path:"/api/v1/delivery/targets/{{fixture_source_git}}/",body:{placement:{cluster_ids:["00000000-0000-4000-8000-000000000001"]}},owned_ref:"fixture_source_git",expect_status:[200]}
    elif $p=="gate.approval" then owned_action($p;"/api/v1/delivery/rollouts/{{fixture_source_git}}/approve/";{})
    elif $p=="gate.maintenance_window" then common($p)+{body:{maintenance_window_policy:{timezone:"UTC"}}}
    elif ($p|startswith("control.")) then owned_action($p;("/api/v1/delivery/rollouts/{{fixture_source_git}}/"+($p|split(".")[1])+"/");{})
    elif $p=="resilience.credential_rotation" then owned_action($p;"/api/v1/delivery/sources/{{fixture_source_git}}/rotate-credential/";{})
    elif $p=="resilience.credential_revocation" then owned_action($p;"/api/v1/delivery/sources/{{fixture_source_git}}/revoke/";{})
    elif $p=="lifecycle.delete" then common($p)+{method:"DELETE",path:"/api/v1/delivery/targets/{{fixture_source_git}}/",owned_ref:"fixture_source_git",expect_status:[202]}
    elif $p=="lifecycle.orphan" then owned_action($p;"/api/v1/delivery/targets/{{fixture_source_git}}/orphan/";{})
    elif $p=="authorization.project_denial" then common($p)+{path:"/api/v1/delivery/denied/",expect_status:[403]}
    elif $p=="backup.restore" then owned_action($p;"/api/v1/backups/{{fixture_source_git}}/restore/";{})
    elif $p=="surface.cli" then common($p)+{kind:"cli",argv:["delivery","source","list","--project","{{project_id}}"]}|del(.method,.path)
    elif $p=="surface.ui" then common($p)+{path:"/delivery",response:"text",expect_contains:["Astronomer"]}
    elif $p=="surface.sse" then common($p)+{path:"/api/v1/events/stream/",response:"text",expect_contains:["delivery"],allow_timeout:true}
    elif $p=="surface.metrics" then common($p)+{path:"/api/v1/metrics"}
    elif $p=="surface.alerts" then common($p)+{path:"/api/v1/alerts/"}
    elif $p=="surface.runbook" then common($p)+{path:"/api/v1/runbooks/delivery/"}
    elif $p=="resilience.drift_detect" or $p=="resilience.drift_repair" then common($p)+{path:"/api/v1/delivery/test-controls/drift/"}
    elif $p=="resilience.disconnect" then common($p)+{path:"/api/v1/delivery/test-controls/disconnect/"}
    elif $p=="resilience.reconnect" then common($p)+{path:"/api/v1/delivery/test-controls/reconnect/"}
    elif $p=="resilience.controller_restart" then common($p)+{path:"/api/v1/delivery/test-controls/restart/"}
    elif $p=="resilience.controller_upgrade" then owned_action($p;"/api/v1/delivery/system/rollouts/{{fixture_source_git}}/";{})
    elif $p=="resilience.controller_rollback" then owned_action($p;"/api/v1/delivery/system/rollouts/{{fixture_source_git}}/rollback/";{})
    else common($p) end;
  {schema_version:1,
   candidate:{version:"v1.0.0",commit:"0123456789abcdef",release_manifest_digest:("sha256:"+("a"*64))},
   run:{suite_id:"acceptance-suite-001",sequence:1,total:3},
   clusters:[
     {id:"00000000-0000-4000-8000-000000000001",mode:"online",fresh:true,owned:true},
     {id:"00000000-0000-4000-8000-000000000002",mode:"air_gap",fresh:true,owned:true}],
   variables:{project_id:"00000000-0000-4000-8000-000000000003",bundle_id:"00000000-0000-4000-8000-000000000004"},
   secret_env:[],scenarios:[$requirements[] as $p|{id:($p|gsub("\\.";"-")),covers:[$p],steps:[step($p)]}]}' >"${TEST_DIR}/matrix.json"

"${SCRIPT}" --check --matrix "${TEST_DIR}/matrix.json" >"${TEST_DIR}/check.out"
grep -Fq 'network was not contacted' "${TEST_DIR}/check.out" || fail "check mode did not confirm offline operation"

jq 'del(.scenarios[0])' "${TEST_DIR}/matrix.json" >"${TEST_DIR}/missing.json"
expect_failure 'matrix is missing required coverage' "${SCRIPT}" --check --matrix "${TEST_DIR}/missing.json"

jq '.scenarios[0].steps[0].body.password="literal-secret"' "${TEST_DIR}/matrix.json" >"${TEST_DIR}/secret.json"
expect_failure 'sensitive matrix fields' "${SCRIPT}" --check --matrix "${TEST_DIR}/secret.json"

expect_failure 'production-like hostnames are refused' env \
  AUTH_TOKEN=fake ASTRO_BIN=/bin/true BASE_URL=https://prod.example.test \
  LIVE_DELIVERY_ENVIRONMENT=acceptance LIVE_DELIVERY_CONFIRM=prod.example.test \
  "${SCRIPT}" --matrix "${TEST_DIR}/matrix.json"

# Exercise the runner, reverse cleanup stack, and evidence writer against
# process-local public-surface fakes. No socket is opened.
mkdir "${TEST_DIR}/bin"
cat >"${TEST_DIR}/bin/curl" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
output="" headers="" method="GET" url="" consume_stdin=0
while [[ "$#" -gt 0 ]]; do
  case "$1" in
    --output) output="$2"; shift 2 ;;
    --dump-header) headers="$2"; shift 2 ;;
    --request) method="$2"; shift 2 ;;
    --data-binary) [[ "$2" == "@-" ]] && consume_stdin=1; shift 2 ;;
    --max-redirs|--connect-timeout|--max-time|--max-filesize|--write-out|--header) shift 2 ;;
    --silent|--show-error|--location) shift ;;
    *) url="$1"; shift ;;
  esac
done
[[ "${consume_stdin}" -eq 0 ]] || payload="$(</dev/stdin)"
status=200
if [[ "${url}" == *'/delivery/denied/'* ]]; then
  status=403
elif [[ "${method}" == "DELETE" && "${url}" == *'/delivery/fixtures/'* ]]; then
  status=204
elif [[ "${method}" == "DELETE" ]]; then
  status=202
elif [[ "${method}" == "POST" && ( "${url}" == *'/delivery/sources/' || "${url}" == *'/versions/' ) ]]; then
  status=201
elif [[ "${method}" == "POST" ]]; then
  status=202
fi
if [[ "${url}" == */delivery ]]; then
  printf 'Astronomer delivery UI' >"${output}"
elif [[ "${url}" == *'/events/stream/'* ]]; then
  printf 'event: delivery\ndata: {}\n\n' >"${output}"
elif [[ "${status}" == "204" ]]; then
  : >"${output}"
else
  printf '{"data":{"id":"00000000-0000-4000-8000-000000000005","state":"failed"}}' >"${output}"
fi
printf 'HTTP/1.1 %s Test\r\nETag: "1"\r\n\r\n' "${status}" >"${headers}"
printf '%s' "${status}"
EOF
cat >"${TEST_DIR}/bin/astro" <<'EOF'
#!/usr/bin/env bash
printf '{"data":[]}'
EOF
chmod +x "${TEST_DIR}/bin/curl" "${TEST_DIR}/bin/astro"

env PATH="${TEST_DIR}/bin:${PATH}" AUTH_TOKEN=fake ASTRO_BIN="${TEST_DIR}/bin/astro" \
  BASE_URL=http://localhost:8080 LIVE_DELIVERY_ENVIRONMENT=acceptance LIVE_DELIVERY_CONFIRM=localhost \
  ALLOW_INSECURE_LOCALHOST=1 RUN_PREFIX=delivery-live-test-00000001 EVIDENCE_ROOT="${TEST_DIR}/evidence" \
  "${SCRIPT}" --matrix "${TEST_DIR}/matrix.json" >"${TEST_DIR}/run.out"

evidence="${TEST_DIR}/evidence/delivery-live-test-00000001/evidence.json"
jq -e '
  .status=="passed" and .exit_code==0 and .run.sequence==1 and .run.total==3 and
  (.clusters|length)==2 and (.coverage.required==.coverage.declared) and
  ([.events[]|select(.kind=="step" and .transport=="http")]|length)>0 and
  ([.events[]|select(.kind=="cleanup" and .status=="passed")]|length)>0' \
  "${evidence}" >/dev/null || fail "machine-readable evidence is incomplete"
grep -Fq 'fake' "${evidence}" && fail "authentication material leaked into evidence"

printf 'validate-live-delivery tests passed\n'
