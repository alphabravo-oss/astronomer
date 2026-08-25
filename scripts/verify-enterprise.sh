#!/usr/bin/env bash
# Authoritative enterprise verification entry point.
#
# Dependency installation is intentionally not performed here. Run `npm ci`
# in frontend/ before frontend, api-contract, or all verification. Helm
# dependencies are rebuilt from the committed Chart.lock in helm/all modes.

set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ARTIFACT_ROOT="${VERIFY_ARTIFACT_DIR:-${TMPDIR:-/tmp}/astronomer-verify-enterprise}"
AUDIT_LEVEL="${NPM_AUDIT_LEVEL:-moderate}"
cd "$ROOT_DIR"

ARTIFACT_DIR=""
RESULTS_FILE=""
TOOLS_FILE=""
STARTED_AT=""
RUN_ID=""
SOURCE_COMMIT=""
SOURCE_DIRTY="false"
SOURCE_TREE_SHA256=""
SOURCE_FILE_COUNT="0"

usage() {
  cat <<'EOF'
Usage: scripts/verify-enterprise.sh [all|backend|frontend|helm|api-contract]

Scopes:
  backend      migrations, sqlc drift, Go build/vet/lint/tests/race, API contracts
  frontend     code health, lint, types, units, production build, npm audit
  helm         locked dependencies, lint, dev/prod renders, chart contracts
  api-contract focused API/OpenAPI/generated/embed/route/error-code contracts
  all          complete cluster-safe static gate: backend, frontend, and helm (default)

Prerequisites:
  - Run `npm ci` in frontend/ before frontend, api-contract, or all.
  - Install the Go, Node.js, npm, Helm, and Python 3 prerequisites
    documented in .github/workflows/README.md. golangci-lint needs no install:
    scripts/check-go-lint.sh `go run`s a pinned version. sqlc is likewise run
    through Go at the version pinned by scripts/check-sqlc-generated.sh.

Each run gets a fresh directory beneath VERIFY_ARTIFACT_DIR (default:
${TMPDIR:-/tmp}/astronomer-verify-enterprise) containing logs, rendered
manifests, and a commit-bound evidence manifest. Docker/browser/HA and live
cluster qualifications remain required parallel CI lanes and are never hidden
as successful static subgates.
EOF
}

step() {
  printf '\n==> %s\n' "$1"
}

run_logged() {
  local log_name="$1"
  shift
  local status=0
  set +e
  "$@" 2>&1 | tee "$ARTIFACT_DIR/$log_name.log"
  status=${PIPESTATUS[0]}
  set -e
  if (( status == 0 )); then
    printf '%s\tpassed\t0\n' "$log_name" >>"$RESULTS_FILE"
  else
    printf '%s\tfailed\t%d\n' "$log_name" "$status" >>"$RESULTS_FILE"
  fi
  return "$status"
}

initialize_evidence() {
  local stamp
  stamp="$(date -u +%Y%m%dT%H%M%SZ)"
  SOURCE_COMMIT="$(git rev-parse HEAD 2>/dev/null || printf unknown)"
  if [[ -n "$(git status --short 2>/dev/null)" ]]; then
    SOURCE_DIRTY="true"
  fi
  RUN_ID="${stamp}-$$-${SOURCE_COMMIT:0:12}"
  ARTIFACT_DIR="$ARTIFACT_ROOT/$RUN_ID"
  mkdir -p "$ARTIFACT_DIR"
  RESULTS_FILE="$ARTIFACT_DIR/subgates.tsv"
  TOOLS_FILE="$ARTIFACT_DIR/tools.tsv"
  : >"$RESULTS_FILE"
  : >"$TOOLS_FILE"
  STARTED_AT="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  local tree_json
  tree_json="$(python3 scripts/hash-source-tree.py --root "$ROOT_DIR" --exclude "$ARTIFACT_DIR")"
  read -r SOURCE_TREE_SHA256 SOURCE_FILE_COUNT < <(
    python3 -c 'import json,sys; value=json.load(sys.stdin); print(value["source_tree_sha256"], value["source_file_count"])' <<<"$tree_json"
  )
  capture_tool go go version
  capture_tool node node --version
  capture_tool npm npm --version
  capture_tool helm helm version --short
  capture_tool python python3 --version
  capture_sqlc_generator_contract
  capture_tool shellcheck shellcheck --version
}

capture_tool() {
  local name="$1" binary="$2" version
  shift 2
  if ! command -v "$binary" >/dev/null 2>&1; then
    printf '%s\tunavailable\t\n' "$name" >>"$TOOLS_FILE"
    return
  fi
  version="$($binary "$@" 2>&1 | tr '\n\t' '  ' | sed -E 's/[[:space:]]+/ /g; s/^ //; s/ $//')"
  if [[ -z "$version" ]]; then
    printf '%s\tunavailable\t\n' "$name" >>"$TOOLS_FILE"
  else
    printf '%s\tavailable\t%s\n' "$name" "$version" >>"$TOOLS_FILE"
  fi
}

capture_sqlc_generator_contract() {
  local version
  # The dollar expression is literal input syntax matched by this sed pattern.
  # shellcheck disable=SC2016
  version="$(sed -n 's/^SQLC_VERSION="${SQLC_VERSION:-\([^"]*\)}"$/\1/p' scripts/check-sqlc-generated.sh)"
  if ! command -v go >/dev/null 2>&1 || [[ -z "$version" ]]; then
    printf 'sqlc\tunavailable\t\n' >>"$TOOLS_FILE"
    return
  fi
  printf 'sqlc\tavailable\tgo run github.com/sqlc-dev/sqlc/cmd/sqlc@%s\n' "$version" >>"$TOOLS_FILE"
}

require_tools_for_scope() {
  local required=()
  case "$scope" in
    backend|all) required=(go node npm python shellcheck) ;;
    frontend) required=(node npm python) ;;
    helm) required=(go helm python) ;;
    api-contract) required=(go node npm python) ;;
  esac
  local name status
  for name in "${required[@]}"; do
    status="$(awk -F '\t' -v name="$name" '$1 == name { print $2 }' "$TOOLS_FILE")"
    if [[ "$status" != "available" ]]; then
      printf 'ERROR: required %s tool is unavailable for %s scope\n' "$name" "$scope" >&2
      return 2
    fi
  done
}

finalize_evidence() {
  local exit_code="$1"
  trap - EXIT
  set +e
  local status="passed"
  (( exit_code == 0 )) || status="failed"
  local finished_tree_json finished_tree_sha256 finished_file_count tree_stable="true"
  finished_tree_json="$(python3 scripts/hash-source-tree.py --root "$ROOT_DIR" --exclude "$ARTIFACT_DIR")"
  read -r finished_tree_sha256 finished_file_count < <(
    python3 -c 'import json,sys; value=json.load(sys.stdin); print(value["source_tree_sha256"], value["source_file_count"])' <<<"$finished_tree_json"
  )
  if [[ "$finished_tree_sha256" != "$SOURCE_TREE_SHA256" || "$finished_file_count" != "$SOURCE_FILE_COUNT" ]]; then
    tree_stable="false"
    status="failed"
    exit_code=1
  fi
  python3 - "$ARTIFACT_DIR" "$RESULTS_FILE" "$RUN_ID" "$scope" \
    "$SOURCE_COMMIT" "$SOURCE_DIRTY" "$STARTED_AT" "$status" "$TOOLS_FILE" \
    "$SOURCE_TREE_SHA256" "$SOURCE_FILE_COUNT" "$finished_tree_sha256" "$finished_file_count" "$tree_stable" <<'PY'
import datetime as dt
import hashlib
import json
import pathlib
import sys

directory = pathlib.Path(sys.argv[1])
results_path = pathlib.Path(sys.argv[2])
run_id, scope, commit, dirty, started, status, tools_path, tree_sha256, file_count, finished_tree_sha256, finished_file_count, tree_stable = sys.argv[3:]
subgates = []
for line in results_path.read_text(encoding="utf-8").splitlines():
    name, result, exit_code = line.split("\t")
    subgates.append({"name": name, "status": result, "exit_code": int(exit_code)})
artifacts = []
for path in sorted(directory.iterdir()):
    if not path.is_file() or path.name in {"evidence.json", "subgates.tsv"}:
        continue
    artifacts.append({
        "path": path.name,
        "sha256": hashlib.sha256(path.read_bytes()).hexdigest(),
    })
tools = {}
for line in pathlib.Path(tools_path).read_text(encoding="utf-8").splitlines():
    name, tool_status, version = line.split("\t", 2)
    tools[name] = {"status": tool_status, "version": version}
manifest = {
    "schema_version": "astronomer-enterprise-verification/v1",
    "run_id": run_id,
    "scope": scope,
    "qualification_class": "cluster-safe-static",
    "source_commit": commit,
    "source_dirty": dirty == "true",
    "source_tree_sha256": tree_sha256,
    "source_file_count": int(file_count),
    "finished_source_tree_sha256": finished_tree_sha256,
    "finished_source_file_count": int(finished_file_count),
    "source_tree_stable": tree_stable == "true",
    "tools": tools,
    "started_at": started,
    "finished_at": dt.datetime.now(dt.timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z"),
    "status": status,
    "subgates": subgates,
    "artifacts": artifacts,
    "required_parallel_qualifications": [
        "same-commit-pr-validation",
        "stateful-ha-and-outage",
        "playwright-desktop-tablet-mobile",
        "live-browser-trivy",
        "container-supply-chain",
        "fresh-adopted-cluster",
        "postgresql-failover",
        "release-candidate-rehearsal",
        "cloud-acceptance",
        "day2-drill-execution",
        "day2-drill-qualification",
        "scale-certification-rungs",
        "scale-audit-sizing-aggregate",
        "rancher-benchmark-automated-human",
        "assistive-technology-accessibility",
        "release-approval-promotion",
        "resumed-release-approval-promotion",
    ],
}
(directory / "evidence.json").write_text(json.dumps(manifest, indent=2, sort_keys=True) + "\n", encoding="utf-8")
PY
  if [[ -n "${GITHUB_STEP_SUMMARY:-}" ]]; then
    {
      printf '## Enterprise verification: %s\n\n' "$status"
      # Backticks are intentional Markdown literals, not shell substitutions.
      # shellcheck disable=SC2016
      printf -- '- Scope: `%s`\n- Commit: `%s`\n- Evidence: `%s/evidence.json`\n\n' \
        "$scope" "$SOURCE_COMMIT" "$ARTIFACT_DIR"
      printf '| Subgate | Result |\n| --- | --- |\n'
      awk -F '\t' '{ printf "| `%s` | %s |\n", $1, $2 }' "$RESULTS_FILE"
    } >>"$GITHUB_STEP_SUMMARY"
  fi
  exit "$exit_code"
}

require_frontend_dependencies() {
  if [[ ! -d frontend/node_modules ]]; then
    # Backticks are intentional diagnostic literals.
    # shellcheck disable=SC2016
    printf 'ERROR: frontend/node_modules is missing; run `cd frontend && npm ci` first.\n' >&2
    return 1
  fi
}

verify_contract_artifacts() {
  step "Documentation links, lifecycle classification, and terminology"
  run_logged documentation-contract node scripts/check-docs.mjs

  step "Changed-code and legacy-hotspot complexity budgets"
  run_logged complexity-budget node scripts/check-complexity-budget.mjs

  step "Go and frontend dependency-direction boundaries"
  run_logged dependency-boundaries node scripts/check-dependency-boundaries.mjs

  step "Release compatibility contract and generated documentation"
  run_logged compatibility-contract ./scripts/compatibility-contract.py check

  step "OpenAPI route coverage"
  run_logged openapi-coverage node scripts/openapi-coverage.mjs --check

  step "OpenAPI route synchronization and operation quality"
  run_logged openapi-route-sync node scripts/sync-openapi-routes.mjs --check
  run_logged openapi-quality node scripts/openapi-quality.mjs

  step "OpenAPI Spectral schema validation"
  run_logged openapi-spectral node scripts/openapi-spectral.mjs

  step "OpenAPI breaking-change review"
  run_logged openapi-breaking ./scripts/openapi-breaking-change.sh

  step "OpenAPI request-schema fields vs Go request structs"
  run_logged openapi-request-fields node scripts/openapi-request-fields.mjs --check

  step "Generated frontend OpenAPI types"
  run_logged openapi-generated-types node scripts/generate-openapi-types.mjs --check
  run_logged openapi-generated-client node scripts/generate-openapi-client.mjs --check
  run_logged openapi-generated-go-sdk ./scripts/check-go-sdk-generated.sh

  step "Embedded OpenAPI asset"
  if ! cmp -s docs/openapi.yaml internal/handler/assets/openapi.yaml; then
    # Backticks are intentional diagnostic literals.
    # shellcheck disable=SC2016
    printf 'FAIL: internal/handler/assets/openapi.yaml is stale; run `make openapi-embed`.\n' \
      | tee "$ARTIFACT_DIR/openapi-embed.log" >&2
    printf 'openapi-embed\tfailed\t1\n' >>"$RESULTS_FILE"
    return 1
  fi
  printf 'embedded OpenAPI asset is current\n' | tee "$ARTIFACT_DIR/openapi-embed.log"
  printf 'openapi-embed\tpassed\t0\n' >>"$RESULTS_FILE"

  step "Error-code documentation"
  run_logged error-code-docs node scripts/error-code-docs.mjs --check

  step "Route metadata JSON"
  run_logged route-metadata-json python3 -c \
    'import json,sys; json.load(open(sys.argv[1], encoding="utf-8"))' \
    docs/generated-route-inventory.json
  run_logged security-sensitive-routes-json python3 -c \
    'import json,sys; json.load(open(sys.argv[1], encoding="utf-8"))' \
    docs/security-sensitive-routes.json
  run_logged route-risk-classifications-json python3 -c \
    'import json,sys; json.load(open(sys.argv[1], encoding="utf-8"))' \
    docs/route-risk-classifications.json
  run_logged security-wave-review node scripts/security-wave-review.mjs

  step "Browser flake/quarantine ownership policy"
  run_logged browser-flake-policy node scripts/test-flake-report.mjs --validate
}

verify_api_contract() {
  require_frontend_dependencies

  step "Pinned Charlie Product Bridge contract"
  run_logged charlie-contract make charlie-contract-check

  step "API contract Go build"
  run_logged api-go-build go build ./...

  step "API contract Go vet"
  run_logged api-go-vet go vet ./...

  step "API package tests"
  run_logged api-package-tests go test \
    ./internal/handler/ ./internal/server/ ./internal/auth/ ./internal/server/middleware/ -count=1

  verify_contract_artifacts

  step "Route table golden contract"
  run_logged route-table-contract go test ./internal/server/ -run RouteTable -count=1

  step "API error catalog coverage"
  run_logged apierror-catalog go test ./internal/handler/ -run TestApierrorCatalogCoverage -count=1

  step "Security-sensitive route contracts"
  run_logged route-security-contracts go test ./internal/server/ -run \
    'Test(AdminRouteRegistrationsAreAuthProtected|HighRiskRoutesDenyUnauthenticatedRequests|MutatingRoutesHaveSecurityClassification|BrowserCookieMutatingRoutesRequireCSRF|RouteInventoryCanBeGenerated|ForwardingRoutesAreDocumentedInProxyInventory|K8sProxy|ServiceProxy|RegistrationEvents)' \
    -count=1
}

verify_backend() {
  require_frontend_dependencies

  step "Formatting and patch whitespace"
  run_logged formatting ./scripts/check-formatting.sh

  step "Shell syntax and ShellCheck"
  run_logged shellcheck ./scripts/check-shell.sh

  step "Migration safety"
  run_logged migration-safety ./scripts/check-migrations.sh
  run_logged migration-policy-self-test ./scripts/check-migrations-test.sh

  step "sqlc generated-code drift"
  run_logged sqlc-generated ./scripts/check-sqlc-generated.sh

  step "Go build"
  run_logged go-build go build ./...

  step "Go vet"
  run_logged go-vet go vet ./...

  step "Go lint (pinned golangci-lint against .golangci.yml)"
  run_logged go-lint ./scripts/check-go-lint.sh

  step "Pinned Charlie Product Bridge generated contract"
  run_logged charlie-contract ./scripts/check-charlie-contract-generated.sh

  step "Full Go test suite"
  run_logged go-test go test ./... -count=1

  step "Full Go race suite"
  run_logged go-race go test -race -count=1 ./...

  # The full suites above already execute the Go route/error-code tests. Keep
  # generated and static contract checks here without pointlessly rerunning
  # those exact test cases. The api-contract scope retains focused diagnostics.
  verify_contract_artifacts

  step "Release, image inventory, air-gap, and evidence-writer contracts"
  run_logged release-contract make release-contract-check

  step "Protected enterprise qualification producer and release-aggregator contract"
  run_logged enterprise-qualification-producers ./scripts/check-enterprise-qualification-producers.py

  step "Agent identity live API-server acceptance (requires explicit AGENT_IDENTITY_TEST_CONTEXT)"
  run_logged agent-identity-live ./scripts/verify-agent-identity-rbac.sh --if-available
}

verify_frontend() {
  require_frontend_dependencies
  pushd frontend >/dev/null

  # Compare generation against the caller's current working tree, not HEAD.
  # This keeps the gate valid while a legitimate route change and its generated
  # route tree are being reviewed together in an otherwise dirty worktree.
  local route_tree_snapshot="$ARTIFACT_DIR/frontend-routeTree.gen.ts.before"
  cp src/routeTree.gen.ts "$route_tree_snapshot"

  step "Frontend architecture and generated-code health"
  run_logged frontend-code-health npm run code-health

  step "Frontend lint (zero errors; warnings reported)"
  run_logged frontend-lint npm run lint

  step "Frontend type-check"
  run_logged frontend-type-check npm run type-check

  step "Frontend unit tests"
  run_logged frontend-unit-tests npm test

  step "Frontend production build"
  run_logged frontend-build npm run build
  run_logged frontend-bundle-budget npm run bundle:check

  step "Frontend routeTree drift check (build regenerates src/routeTree.gen.ts)"
  run_logged frontend-routetree-drift diff -u "$route_tree_snapshot" src/routeTree.gen.ts

  step "Frontend dependency audit (threshold: $AUDIT_LEVEL)"
  run_logged frontend-npm-audit npm audit --audit-level="$AUDIT_LEVEL"

  popd >/dev/null
}

render_helm() {
  local name="$1"
  shift
  local output="$ARTIFACT_DIR/$name.yaml"
  local stderr_log="$ARTIFACT_DIR/$name.stderr.log"

  if ! "$@" >"$output" 2>"$stderr_log"; then
    cat "$stderr_log" >&2
    printf '%s\tfailed\t1\n' "$name" >>"$RESULTS_FILE"
    return 1
  fi
  cat "$stderr_log" >&2
  if ! test -s "$output"; then
    printf '%s\tfailed\t1\n' "$name" >>"$RESULTS_FILE"
    return 1
  fi
  printf '%s\tpassed\t0\n' "$name" >>"$RESULTS_FILE"
}

verify_helm() {
  step "Helm chart input contract"
  run_logged helm-input-contract ./scripts/check-helm-inputs.sh

  # The chart ships no key material: secrets.secretKey / secrets.encryptionKey
  # are empty by default and every render must supply its own (the chart used to
  # default to a JWT signing key and Fernet key published in this repository).
  # These are throwaway render-only values.
  local render_keys=(
    --set secrets.secretKey=verify-enterprise-render-signing-key
    --set secrets.encryptionKey=I2oWSIt6LO68xR6lxhqBpQxhesPuii5R6ubog-Id-yo=
  )

  step "Helm lint"
  run_logged helm-lint helm lint deploy/chart "${render_keys[@]}"

  step "Development Helm render"
  render_helm helm-development helm template astronomer deploy/chart \
    "${render_keys[@]}" \
    --set frontend.enabled=true \
    --set dex.enabled=true \
    --set config.env=development

  step "Fully wired production Helm render"
  render_helm helm-production helm template astronomer deploy/chart \
    -f deploy/chart/values-production.yaml \
    --set config.serverURL=https://astronomer.example.com \
    --set 'gateway.hosts={astronomer.example.com}' \
    --set tls.source=secret \
    --set tls.secretName=astronomer-tls \
    --set postgres.external.dsnSecretRef.name=astronomer-postgres-dsn \
    --set redis.external.address=redis.example.com:6379 \
    --set secrets.secretKey=prod-secret-key \
    --set secrets.encryptionKey=prod-encryption-key-prod-encryption-key12 \
    --set bootstrap.email=admin@example.com \
    --set bootstrap.password=prod-admin-initial \
    --set 'networkPolicy.externalPostgresEgressCIDRs={10.20.0.0/16}' \
    --set 'networkPolicy.externalRedisEgressCIDRs={10.30.0.0/16}' \
    --set 'networkPolicy.kubernetesAPIEgressCIDRs={10.40.0.0/14}' \
    --set delivery.artifacts.fluxDistribution.ociRepository=ghcr.io/example/astronomer/flux-distribution \
    --set delivery.artifacts.fluxDistribution.digest=sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef \
    --set delivery.artifacts.fluxDistribution.trustPolicy.certificateIdentity=https://github.com/example/repo/.github/workflows/release.yaml@refs/tags/v1.0.0 \
    --set delivery.artifacts.builtInBundles.ociRepository=ghcr.io/example/astronomer/bundles \
    --set delivery.artifacts.builtInBundles.digest=sha256:abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789 \
    --set delivery.artifacts.builtInBundles.trustPolicy.certificateIdentity=https://github.com/example/repo/.github/workflows/release.yaml@refs/tags/v1.0.0 \
    --set managementBackup.s3.bucket=astronomer-backups \
    --set managementBackup.s3.credentialsSecretRef.name=astronomer-backup-aws \
    --set managementBackup.encryptionKeyBackup.wrappingSecretRef.name=astronomer-key-wrap

  step "Helm chart contract tests"
  run_logged helm-contract-tests go test ./deploy/ -count=1
}

scope="${1:-all}"
if [[ $# -gt 1 ]]; then
  usage >&2
  exit 2
fi

initialize_evidence
trap 'finalize_evidence $?' EXIT

step "Tool prerequisites and exact versions"
run_logged tool-prerequisites require_tools_for_scope

case "$scope" in
  backend)
    verify_backend
    ;;
  frontend)
    verify_frontend
    ;;
  helm)
    verify_helm
    ;;
  api-contract)
    verify_api_contract
    ;;
  all)
    verify_backend
    verify_frontend
    verify_helm
    ;;
  -h|--help|help)
    usage
    exit 0
    ;;
  *)
    printf 'ERROR: unknown verification scope %q\n' "$scope" >&2
    usage >&2
    exit 2
    ;;
esac

printf '\nEnterprise static verification passed: %s\nEvidence: %s/evidence.json\n' "$scope" "$ARTIFACT_DIR"
