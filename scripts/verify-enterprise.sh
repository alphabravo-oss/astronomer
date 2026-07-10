#!/usr/bin/env bash
# Authoritative enterprise verification entry point.
#
# Dependency installation is intentionally not performed here. Run `npm ci`
# in frontend/ before frontend, api-contract, or all verification. Helm
# dependencies are rebuilt from the committed Chart.lock in helm/all modes.

set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ARTIFACT_DIR="${VERIFY_ARTIFACT_DIR:-${TMPDIR:-/tmp}/astronomer-verify-enterprise}"
AUDIT_LEVEL="${NPM_AUDIT_LEVEL:-moderate}"

mkdir -p "$ARTIFACT_DIR"
cd "$ROOT_DIR"

usage() {
  cat <<'EOF'
Usage: scripts/verify-enterprise.sh [all|backend|frontend|helm|api-contract]

Scopes:
  backend      migrations, sqlc drift, Go build/vet/tests/race, API contracts
  frontend     code health, lint, types, units, production build, npm audit
  helm         locked dependencies, lint, dev/prod renders, chart contracts
  api-contract focused API/OpenAPI/generated/embed/route/error-code contracts
  all          backend, frontend, and helm (default)

Prerequisites:
  - Run `npm ci` in frontend/ before frontend, api-contract, or all.
  - Install the Go, Node.js, npm, Helm, Python 3, and sqlc prerequisites
    documented in .github/workflows/README.md.

Logs and rendered manifests are written beneath VERIFY_ARTIFACT_DIR (default:
${TMPDIR:-/tmp}/astronomer-verify-enterprise).
EOF
}

step() {
  printf '\n==> %s\n' "$1"
}

run_logged() {
  local log_name="$1"
  shift
  "$@" 2>&1 | tee "$ARTIFACT_DIR/$log_name.log"
}

require_frontend_dependencies() {
  if [[ ! -d frontend/node_modules ]]; then
    printf 'ERROR: frontend/node_modules is missing; run `cd frontend && npm ci` first.\n' >&2
    return 1
  fi
}

verify_contract_artifacts() {
  step "OpenAPI route coverage"
  run_logged openapi-coverage node scripts/openapi-coverage.mjs --check

  step "Generated frontend OpenAPI types"
  run_logged openapi-generated-types node scripts/generate-openapi-types.mjs --check

  step "Embedded OpenAPI asset"
  if ! cmp -s docs/openapi.yaml internal/handler/assets/openapi.yaml; then
    printf 'FAIL: internal/handler/assets/openapi.yaml is stale; run `make openapi-embed`.\n' \
      | tee "$ARTIFACT_DIR/openapi-embed.log" >&2
    return 1
  fi
  printf 'embedded OpenAPI asset is current\n' | tee "$ARTIFACT_DIR/openapi-embed.log"

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
}

verify_api_contract() {
  require_frontend_dependencies

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
    'Test(AdminRouteRegistrationsAreAuthProtected|HighRiskRoutesDenyUnauthenticatedRequests|MutatingRoutesHaveSecurityClassification|BrowserCookieMutatingRoutesRequireCSRF|RouteInventoryCanBeGenerated|ForwardingRoutesAreDocumentedInProxyInventory|K8sProxy|ServiceProxy|RegistrationEvents|ArgoCDInternalK8sProxy)' \
    -count=1
}

verify_backend() {
  require_frontend_dependencies

  step "Migration safety"
  run_logged migration-safety ./scripts/check-migrations.sh

  step "sqlc generated-code drift"
  run_logged sqlc-generated ./scripts/check-sqlc-generated.sh

  step "Go build"
  run_logged go-build go build ./...

  step "Go vet"
  run_logged go-vet go vet ./...

  step "Full Go test suite"
  run_logged go-test go test ./... -count=1

  step "Full Go race suite"
  run_logged go-race go test -race -count=1 ./...

  # The full suites above already execute the Go route/error-code tests. Keep
  # generated and static contract checks here without pointlessly rerunning
  # those exact test cases. The api-contract scope retains focused diagnostics.
  verify_contract_artifacts

  step "Agent identity live API-server acceptance (requires explicit AGENT_IDENTITY_TEST_CONTEXT)"
  run_logged agent-identity-live ./scripts/verify-agent-identity-rbac.sh --if-available
}

verify_frontend() {
  require_frontend_dependencies
  pushd frontend >/dev/null

  step "Frontend architecture and generated-code health"
  run_logged frontend-code-health npm run code-health

  step "Frontend lint (zero warnings)"
  run_logged frontend-lint npm run lint -- --max-warnings=0

  step "Frontend type-check"
  run_logged frontend-type-check npm run type-check

  step "Frontend unit tests"
  run_logged frontend-unit-tests npm test -- --runInBand

  step "Frontend production build"
  run_logged frontend-build npm run build

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
    return 1
  fi
  cat "$stderr_log" >&2
  test -s "$output"
}

verify_helm() {
  step "Helm dependencies from Chart.lock"
  run_logged helm-dependency-build helm dependency build deploy/chart

  step "Helm lint"
  run_logged helm-lint helm lint deploy/chart

  step "Development Helm render"
  render_helm helm-development helm template astronomer deploy/chart \
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

printf '\nEnterprise verification passed: %s\nArtifacts: %s\n' "$scope" "$ARTIFACT_DIR"
