#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
RUNNER="$ROOT/scripts/test-live-browser.sh"
WORK_DIR="$(mktemp -d)"
trap 'rm -rf "$WORK_DIR"' EXIT

bash -n "$RUNNER"

# Validation mode must be side-effect free, including when every external
# dependency is replaced with a command that would fail if invoked.
for command in base64 curl docker git go helm k3d kubectl npm openssl python3 setsid sha256sum; do
  printf '#!/usr/bin/env bash\nexit 97\n' >"$WORK_DIR/$command"
  chmod +x "$WORK_DIR/$command"
done
PATH="$WORK_DIR:/usr/bin:/bin" "$RUNNER" --validate-only >/dev/null

# These are literal source-code fixtures; dollar expressions must not expand in
# this static runner test.
# shellcheck disable=SC2016
required_patterns=(
  'postgres:16-alpine'
  'redis:7-alpine'
  'MANAGEMENT_BACKUP_ENABLED=false'
  'DELIVERY_ENABLED=true'
	'./scripts/testdata/live-browser-fixture'
	'"$artifact_dir/bin/live-browser-fixture" api-token'
	'admin-api.curl'
	'LIVE_FIXTURE_AGENT_TOKEN="$agent_token"'
	'agent_connections WHERE cluster_id='
  'npx playwright test --project=live'
  'assert_stack before-playwright'
  'assert_stack after-playwright'
	'PLAYWRIGHT_REUSE_EXISTING_SERVER=1'
	'LIVE_BROWSER_ARTIFACT_DIR'
	'SERVER_METRICS_ADDR=127.0.0.1:$server_metrics_port'
	'WORKER_METRICS_ADDR=127.0.0.1:$worker_metrics_port'
	'http://127.0.0.1:$worker_metrics_port/healthz'
	'--kubeconfig-update-default=false'
	'--kubeconfig-switch-context=false'
	'KUBECONFIG="$flux_kubeconfig"'
	'kubectl apply -f deploy/flux/install.yaml'
	'app.kubernetes.io/managed-by=astronomer-agent'
	'flux-kustomize-managed'
	'vmware-tanzu/velero'
	'--version 11.4.0'
	'--set upgradeCRDs=false'
	'LIVE_FIXTURE_BACKUP_NAMESPACE="$backup_namespace"'
	'app.kubernetes.io/managed-by=astronomer-go'
	'mc stat --json'
	'minio_server_commit="0d7408fc9969caf07de6a8c3a84f9fbb10a6739e"'
	'minio_client_commit="b00526b153a31b36767991a4f5ce2cced435ee8e"'
	'github.com/minio/minio@$minio_server_commit'
	'github.com/minio/mc@$minio_client_commit'
	'k3d image import "$minio_fixture_image"'
	'--image-pull-policy=Never'
	'backups/$velero_backup_name/$velero_backup_name.tar.gz'
	'velero-durable-state.log'
	'LIVE_FIXTURE_DIRECT_ENDPOINT="$direct_api_endpoint"'
	'LIVE_FIXTURE_DIRECT_CA_SHA256="$direct_ca_sha256"'
	'LIVE_FIXTURE_DIRECT_KUBECONFIG_PATH="$direct_kubeconfig"'
	'astronomer-direct-reader'
	'direct-access-validation.log'
	'direct-token-leak-paths.log'
	'LIVE_BROWSER_TRIVY_ENABLED'
	'LIVE_BROWSER_DIRECT_API_PORT'
	'direct API port must be 443 or 6443'
	'neither supported direct API port (443, 6443) is available'
	'--api-port "$direct_api_host:$direct_api_port"'
	'LIVE_FIXTURE_TRIVY_ENABLED="$trivy_enabled"'
	'--version "$trivy_chart_version"'
	'trivy_chart_digest="sha256:'
	'LIVE_FIXTURE_TRIVY_CHART_DIGEST="$trivy_chart_digest"'
	'succeeded:ready:true:true'
	'desired_generation=d.observed_generation'
	'desired_spec_digest=d.observed_spec_digest'
	'vulnerabilityreports.aquasecurity.github.io'
	'image_vulnerability_reports'
	'trivy-flux-objects.yaml'
	'trivy-ingestion-state.log'
	'trivy-database-evidence.log'
  'refusing to contact an existing endpoint'
  'docker rm -f "$postgres_container" "$redis_container"'
)
for pattern in "${required_patterns[@]}"; do
  grep -Fq -- "$pattern" "$RUNNER" || {
    echo "live-browser runner is missing contract: $pattern" >&2
    exit 1
  }
done

if grep -Eq 'localhost:(5433|6380)|k3d cluster delete \$|kubectl config use-context|member-a|member-b' "$RUNNER"; then
  echo "live-browser runner must not use existing compose endpoints or mutable Kubernetes context" >&2
  exit 1
fi
if grep -Fq 'auth/login/" | json_field token' "$RUNNER"; then
	echo "live-browser runner must not expect bearer material from browser login" >&2
	exit 1
fi
grep -Fq 'flux_cluster="test-run-live-' "$RUNNER"
# Literal assertion against the runner source, not an expression in this test.
# shellcheck disable=SC2016
grep -Fq '[[ "$flux_created" == 1 && "$flux_cluster" == test-run-live-* ]]' "$RUNNER"
grep -Fq 'agent.NewMirrorSubscriber(proxy.Client(), deliveryDynamic, client, log)' \
	"$ROOT/scripts/testdata/live-browser-fixture/main.go"
grep -Fq 'FeatureDeliveryRendererHelm' "$ROOT/scripts/testdata/live-browser-fixture/main.go"
grep -Fq 'mirror.gcr.io/library/alpine:3.18.0@sha256:' \
	"$ROOT/scripts/testdata/live-browser-fixture/trivy-scan-target.yaml"
grep -Fq 'FROM alpine@sha256:' \
	"$ROOT/scripts/testdata/live-browser-fixture/Dockerfile.minio-source"
grep -Fq 'image: __MINIO_FIXTURE_IMAGE__' \
	"$ROOT/scripts/testdata/live-browser-fixture/velero-minio.yaml.tmpl"
if grep -Rq 'quay.io/minio/' "$ROOT/scripts/test-live-browser.sh" "$ROOT/scripts/testdata/live-browser-fixture"; then
	echo "live-browser runner must build its pinned MinIO fixture from source" >&2
	exit 1
fi

grep -Fq 'test-live-browser:' "$ROOT/Makefile"
grep -Fq 'make test-live-browser' "$ROOT/.github/workflows/pr-validation.yaml"
grep -Fq 'retries: 0' "$ROOT/frontend/playwright.config.ts"
grep -Fq 'trace: "retain-on-failure"' "$ROOT/frontend/playwright.config.ts"
grep -Fq 'video: "retain-on-failure"' "$ROOT/frontend/playwright.config.ts"

# Count declarations at any TypeScript indentation level. The retained Trivy
# journey is intentionally nested under `if (trivyEnabled)` and CI enables it
# through the runner environment handoff asserted above.
live_test_count="$(grep -RhE '^[[:space:]]*test\(' "$ROOT/frontend/tests/e2e-live" --include='*.spec.ts' | wc -l | tr -d ' ')"
[[ "$live_test_count" == "16" ]] || {
  echo "live-browser runner expects 16 explicit retry-free journeys, found $live_test_count" >&2
  exit 1
}

echo "live-browser-runner-test: OK"
