#!/usr/bin/env bash
# Upgrade an existing Astronomer v1 Helm release to one exact signed tag.
#
# The management-plane chart is dependency-free. Helm is the sole owner of the
# release lifecycle; downstream Flux controllers remain agent-managed and are
# never installed, paused, or modified by this script.

set -euo pipefail

usage() {
  cat <<'EOF'
Usage: scripts/upgrade-release.sh [--yes] [--dry-run-only] <version>

  version         Exact v1 stable release, for example v1.0.1 or 1.0.1
  --yes           Perform the upgrade after backup and server-side dry run
  --dry-run-only  Stop after backup and server-side Helm dry run

Environment:
  RELEASE             Helm release name (default: astronomer)
  NAMESPACE           Helm namespace (default: astronomer)
  CHART_REF           OCI chart reference
  RELEASE_REPO        GitHub release repository
  BACKUP_ROOT         Private backup parent (default: ./astronomer-upgrade-backups)
  TIMEOUT             Helm/rollout timeout (default: 15m)
  HEALTH_PORT         Temporary localhost readiness port (default: 18080)
  MIN_READY_NODES     Minimum schedulable Ready nodes (default: 1)
  MIN_BACKUP_FREE_KIB Free space required before backup (default: 1048576)
  EXTERNAL_DB_BACKUP_CONFIRMED=1
                      Required when bundled PostgreSQL is not present
  EXTERNAL_DB_V1_SCHEMA_CONFIRMED=1
                      Required for an external database; the chart hook still
                      independently verifies the exact clean v1 schema
EOF
}

die() {
  printf 'upgrade-release: %s\n' "$*" >&2
  exit 1
}

approve=0
dry_run_only=0
version=""
while (($#)); do
  case "$1" in
    --yes) approve=1 ;;
    --dry-run-only) dry_run_only=1 ;;
    -h|--help) usage; exit 0 ;;
    -*) printf 'unknown option: %s\n' "$1" >&2; usage >&2; exit 2 ;;
    *)
      [[ -z "$version" ]] || { printf 'only one version may be supplied\n' >&2; exit 2; }
      version="$1"
      ;;
  esac
  shift
done

[[ -n "$version" ]] || { usage >&2; exit 2; }
image_tag="$version"
[[ "$image_tag" == v* ]] || image_tag="v${image_tag}"
chart_version="${image_tag#v}"
[[ "$chart_version" =~ ^1\.[0-9]+\.[0-9]+$ ]] || {
  printf 'target must be an exact stable v1 semantic version, got %q\n' "$version" >&2
  exit 2
}

release="${RELEASE:-astronomer}"
namespace="${NAMESPACE:-astronomer}"
chart_ref="${CHART_REF:-oci://ghcr.io/alphabravo-oss/charts/astronomer}"
release_repo="${RELEASE_REPO:-alphabravo-oss/astronomer}"
backup_root="${BACKUP_ROOT:-./astronomer-upgrade-backups}"
timeout="${TIMEOUT:-15m}"
health_port="${HEALTH_PORT:-18080}"
min_ready_nodes="${MIN_READY_NODES:-1}"
min_backup_free_kib="${MIN_BACKUP_FREE_KIB:-1048576}"

[[ "$health_port" =~ ^[1-9][0-9]{0,4}$ ]] && ((health_port <= 65535)) || die "HEALTH_PORT must be 1-65535"
[[ "$min_ready_nodes" =~ ^[1-9][0-9]*$ ]] || die "MIN_READY_NODES must be a positive integer"
[[ "$min_backup_free_kib" =~ ^[1-9][0-9]*$ ]] || die "MIN_BACKUP_FREE_KIB must be a positive integer"

for tool in helm kubectl curl gh cosign sha256sum cmp jq awk sort df grep; do
  command -v "$tool" >/dev/null 2>&1 || die "missing required tool: $tool"
done
helm upgrade --help | grep -q -- '--reset-then-reuse-values' || \
  die "this upgrade requires Helm with --reset-then-reuse-values support"

status_json="$(helm status "$release" --namespace "$namespace" --output json)"
[[ "$(jq -r '.info.status // empty' <<<"$status_json")" == "deployed" ]] || \
  die "Helm release ${namespace}/${release} is not deployed"
kubectl auth can-i get secrets --namespace "$namespace" | grep -qx yes || \
  die "current Kubernetes identity cannot back up Secrets in namespace $namespace"

current_metadata="$(helm get metadata "$release" --namespace "$namespace" --output json)"
current_chart_name="$(jq -r '.chart // empty' <<<"$current_metadata")"
current_chart_version="$(jq -r '.version // empty' <<<"$current_metadata")"
[[ "$current_chart_name" == "astronomer" ]] || \
  die "installed release chart is '$current_chart_name', expected astronomer"
[[ "$current_chart_version" =~ ^1\.[0-9]+\.[0-9]+$ ]] || \
  die "v1 is fresh-install-only; refusing to upgrade installed chart version '$current_chart_version'"
[[ "$chart_version" != "$current_chart_version" ]] || \
  die "target $chart_version is already installed"
newest="$(printf '%s\n%s\n' "$current_chart_version" "$chart_version" | sort -V | tail -1)"
[[ "$newest" == "$chart_version" ]] || \
  die "target $chart_version is older than installed $current_chart_version; use helm rollback with a verified backup"

printf 'Running management-plane readiness and capacity gates\n'
ready_nodes="$(kubectl get nodes -o json | jq '[.items[] | select(.spec.unschedulable != true) | select(any(.status.conditions[]?; .type == "Ready" and .status == "True"))] | length')"
((ready_nodes >= min_ready_nodes)) || \
  die "only $ready_nodes schedulable Ready nodes; require at least $min_ready_nodes"

deployments_json="$(kubectl get deployments --namespace "$namespace" \
  -l "app.kubernetes.io/instance=${release},app.kubernetes.io/part-of=astronomer" -o json)"
jq -e '(.items | length) > 0 and all(.items[];
  (.metadata.generation <= (.status.observedGeneration // 0)) and
  ((.status.availableReplicas // 0) >= (.spec.replicas // 0)) and
  ((.status.updatedReplicas // 0) >= (.spec.replicas // 0)))' \
  <<<"$deployments_json" >/dev/null || \
  die "one or more Astronomer deployments are not fully observed, updated, and available"

pdb_json="$(kubectl get poddisruptionbudgets --namespace "$namespace" \
  -l "app.kubernetes.io/instance=${release},app.kubernetes.io/part-of=astronomer" -o json)"
jq -e 'all(.items[]; (.status.disruptionsAllowed // 0) >= 1)' <<<"$pdb_json" >/dev/null || \
  die "one or more Astronomer PodDisruptionBudgets allow no voluntary disruption"

mkdir -p "$backup_root"
backup_free_kib="$(df -Pk "$backup_root" | awk 'NR == 2 {print $4}')"
[[ "$backup_free_kib" =~ ^[0-9]+$ ]] || die "could not determine backup filesystem free space"
((backup_free_kib >= min_backup_free_kib)) || \
  die "backup filesystem has ${backup_free_kib} KiB free; require ${min_backup_free_kib} KiB"

umask 077
timestamp="$(date -u +%Y%m%dT%H%M%SZ)"
backup_dir="${backup_root%/}/${release}-${timestamp}"
mkdir -p "$backup_dir/release-assets" "$backup_dir/oci-chart"

printf 'Downloading and verifying published release %s from %s\n' "$image_tag" "$release_repo"
gh release download "$image_tag" --repo "$release_repo" \
  --dir "$backup_dir/release-assets" \
  --pattern RELEASE_IMAGES --pattern SHA256SUMS \
  --pattern "astronomer-${chart_version}.tgz"

verify_release_asset() {
  local name="$1" expected actual count
  count="$(awk -v name="$name" '$2 == name {count++} END {print count + 0}' "$backup_dir/release-assets/SHA256SUMS")"
  [[ "$count" == 1 ]] || die "SHA256SUMS must have exactly one entry for $name"
  expected="$(awk -v name="$name" '$2 == name {print $1}' "$backup_dir/release-assets/SHA256SUMS")"
  [[ "$expected" =~ ^[a-f0-9]{64}$ ]] || die "SHA256SUMS has an invalid digest for $name"
  actual="$(sha256sum "$backup_dir/release-assets/$name" | awk '{print $1}')"
  [[ "$actual" == "$expected" ]] || die "release checksum mismatch for $name"
}
verify_release_asset RELEASE_IMAGES
verify_release_asset "astronomer-${chart_version}.tgz"
gh attestation verify "$backup_dir/release-assets/astronomer-${chart_version}.tgz" \
  --repo "$release_repo" >/dev/null

release_image_ref() {
  local repository="$1" ref count
  count="$(grep -Ec "^ghcr\\.io/alphabravo-oss/${repository}@sha256:[a-f0-9]{64}$" "$backup_dir/release-assets/RELEASE_IMAGES" || true)"
  [[ "$count" == 1 ]] || die "RELEASE_IMAGES must contain exactly one immutable reference for $repository"
  ref="$(grep -E "^ghcr\\.io/alphabravo-oss/${repository}@sha256:[a-f0-9]{64}$" "$backup_dir/release-assets/RELEASE_IMAGES")"
  printf '%s' "$ref"
}

server_ref="$(release_image_ref astronomer-go-server)"
worker_ref="$(release_image_ref astronomer-go-worker)"
agent_ref="$(release_image_ref astronomer-go-agent)"
migrate_ref="$(release_image_ref astronomer-go-migrate)"
frontend_ref="$(release_image_ref astronomer-frontend)"
shell_ref="$(release_image_ref astronomer-shell)"
[[ "$(grep -Ec '^[^#[:space:]]' "$backup_dir/release-assets/RELEASE_IMAGES")" == 6 ]] || \
  die "RELEASE_IMAGES must contain exactly the six first-party images"

release_identity="https://github.com/alphabravo-oss/astronomer/.github/workflows/release.yaml@refs/tags/${image_tag}"
for ref in "$server_ref" "$worker_ref" "$agent_ref" "$migrate_ref" "$frontend_ref" "$shell_ref"; do
  cosign verify \
    --certificate-identity "$release_identity" \
    --certificate-oidc-issuer https://token.actions.githubusercontent.com \
    "$ref" >/dev/null
done

printf 'Resolving exact chart %s:%s\n' "$chart_ref" "$chart_version"
helm show chart "$chart_ref" --version "$chart_version" >"$backup_dir/target-chart.yaml"
grep -qx "version: ${chart_version}" "$backup_dir/target-chart.yaml" || \
  die "registry returned a chart whose version does not match $chart_version"
helm pull "$chart_ref" --version "$chart_version" --destination "$backup_dir/oci-chart"
cmp --silent \
  "$backup_dir/release-assets/astronomer-${chart_version}.tgz" \
  "$backup_dir/oci-chart/astronomer-${chart_version}.tgz" || \
  die "published OCI chart does not match the checksum-verified release chart"

printf 'Capturing Helm state, manifests, recovery Secrets, and history in %s\n' "$backup_dir"
helm get values "$release" --namespace "$namespace" --all --output yaml >"$backup_dir/values-all.yaml"
helm get values "$release" --namespace "$namespace" --output yaml >"$backup_dir/values-user.yaml"
helm get manifest "$release" --namespace "$namespace" >"$backup_dir/manifest-before.yaml"
helm history "$release" --namespace "$namespace" --output json >"$backup_dir/history.json"
kubectl get secrets --namespace "$namespace" \
  -l "app.kubernetes.io/instance=${release}" -o yaml >"$backup_dir/release-secrets.yaml"
previous_revision="$(jq -r 'map(.revision | tonumber) | max' "$backup_dir/history.json")"
[[ "$previous_revision" =~ ^[1-9][0-9]*$ ]] || die "could not determine current Helm revision"

postgres_pod="$(kubectl get pods --namespace "$namespace" \
  -l "app.kubernetes.io/instance=${release},app.kubernetes.io/component=postgres" \
  -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)"
if [[ -n "$postgres_pod" ]]; then
  printf 'Verifying and backing up bundled PostgreSQL from pod %s\n' "$postgres_pod"
  migration_state="$(kubectl exec --namespace "$namespace" "$postgres_pod" -- sh -ec \
    'psql -X -v ON_ERROR_STOP=1 --username="$POSTGRES_USER" --dbname="$POSTGRES_DB" -AtF "|" -c "SELECT count(*), COALESCE(max(version),0), COALESCE(bool_or(dirty),false) FROM schema_migrations"')"
  [[ "$migration_state" == "1|1|f" ]] || \
    die "bundled PostgreSQL is not the exact clean v1 schema (found $migration_state)"
  database_bytes="$(kubectl exec --namespace "$namespace" "$postgres_pod" -- sh -ec \
    'psql -X -v ON_ERROR_STOP=1 --username="$POSTGRES_USER" --dbname="$POSTGRES_DB" -Atqc "SELECT pg_database_size(current_database())"')"
  [[ "$database_bytes" =~ ^[1-9][0-9]*$ ]] || die "could not determine bundled PostgreSQL size"
  database_kib=$(((database_bytes + 1023) / 1024))
  backup_free_kib="$(df -Pk "$backup_root" | awk 'NR == 2 {print $4}')"
  required_backup_kib=$((database_kib + min_backup_free_kib))
  ((backup_free_kib >= required_backup_kib)) || \
    die "backup filesystem has ${backup_free_kib} KiB free; bundled database plus reserve requires ${required_backup_kib} KiB"
  kubectl exec --namespace "$namespace" "$postgres_pod" -- sh -ec \
    'pg_dump --format=custom --username="$POSTGRES_USER" --dbname="$POSTGRES_DB"' \
    >"$backup_dir/astronomer.pgcustom"
else
  [[ "${EXTERNAL_DB_BACKUP_CONFIRMED:-0}" == 1 ]] || \
    die "confirm a current external database backup with EXTERNAL_DB_BACKUP_CONFIRMED=1"
  [[ "${EXTERNAL_DB_V1_SCHEMA_CONFIRMED:-0}" == 1 ]] || \
    die "confirm one clean schema_migrations row at version 1 with EXTERNAL_DB_V1_SCHEMA_CONFIRMED=1"
fi

release_args=(
  "$release" "$chart_ref"
  --namespace "$namespace"
  --version "$chart_version"
  --reset-then-reuse-values
  --set-string "image.registry="
  --set-string "image.server.registry=ghcr.io/alphabravo-oss"
  --set-string "image.worker.registry=ghcr.io/alphabravo-oss"
  --set-string "image.agent.registry=ghcr.io/alphabravo-oss"
  --set-string "image.migrate.registry=ghcr.io/alphabravo-oss"
  --set-string "frontend.image.registry=ghcr.io/alphabravo-oss"
  --set-string "preflight.image.registry=ghcr.io/alphabravo-oss"
  --set-string "image.server.repository=astronomer-go-server"
  --set-string "image.worker.repository=astronomer-go-worker"
  --set-string "image.agent.repository=astronomer-go-agent"
  --set-string "image.migrate.repository=astronomer-go-migrate"
  --set-string "frontend.image.repository=astronomer-frontend"
  --set-string "preflight.image.repository=astronomer-shell"
  --set-string "image.server.tag=${image_tag}"
  --set-string "image.worker.tag=${image_tag}"
  --set-string "image.agent.tag=${image_tag}"
  --set-string "image.migrate.tag=${image_tag}"
  --set-string "frontend.image.tag=${image_tag}"
  --set-string "preflight.image.tag=${image_tag}"
  --set-string "image.server.digest=${server_ref##*@}"
  --set-string "image.worker.digest=${worker_ref##*@}"
  --set-string "image.agent.digest=${agent_ref##*@}"
  --set-string "image.migrate.digest=${migrate_ref##*@}"
  --set-string "frontend.image.digest=${frontend_ref##*@}"
  --set-string "preflight.image.digest=${shell_ref##*@}"
  --set-string "config.agentImageRepository=${agent_ref}"
  --set-string "config.agentImageTag=${image_tag}"
  --set-string "kubectlShell.image=${shell_ref}"
  --timeout "$timeout"
)

printf 'Running server-side dry run with preserved values and immutable release images\n'
helm upgrade "${release_args[@]}" --dry-run=server --hide-secret >"$backup_dir/dry-run.yaml"

if ((dry_run_only)); then
  printf 'Dry run succeeded; no cluster changes were made. Backup: %s\n' "$backup_dir"
  exit 0
fi
if ((!approve)); then
  printf 'Dry run succeeded. Review %s and re-run with --yes.\n' "$backup_dir"
  exit 0
fi

port_forward_pid=""
target_upgrade_applied=0
rollback_attempted=0
restore_previous_release() {
  rollback_attempted=1
  printf 'Rolling back %s/%s to Helm revision %s\n' "$namespace" "$release" "$previous_revision" >&2
  helm rollback "$release" "$previous_revision" --namespace "$namespace" \
    --wait --cleanup-on-fail --timeout "$timeout"
}
cleanup() {
  local status=$?
  if [[ -n "$port_forward_pid" ]]; then
    kill "$port_forward_pid" >/dev/null 2>&1 || true
  fi
  if ((status != 0 && target_upgrade_applied && !rollback_attempted)); then
    restore_previous_release || \
      printf 'ROLLBACK FAILED; recover from %s\n' "$backup_dir" >&2
  fi
  return "$status"
}
trap cleanup EXIT

printf 'Upgrading %s/%s to %s with atomic Helm ownership\n' "$namespace" "$release" "$image_tag"
helm upgrade "${release_args[@]}" --atomic --cleanup-on-fail --wait --wait-for-jobs
target_upgrade_applied=1

helm get manifest "$release" --namespace "$namespace" >"$backup_dir/manifest-after.yaml"
for ref in "$server_ref" "$worker_ref" "$migrate_ref" "$frontend_ref" "$shell_ref"; do
  grep -Fq "$ref" "$backup_dir/manifest-after.yaml" || \
    die "post-upgrade manifest does not pin $ref"
done
grep -Fq "AGENT_IMAGE_REPOSITORY: \"${agent_ref}\"" "$backup_dir/manifest-after.yaml" || \
  die "post-upgrade manifest does not pin the managed-cluster agent repository"
grep -Fq "AGENT_IMAGE_TAG: \"${image_tag}\"" "$backup_dir/manifest-after.yaml" || \
  die "post-upgrade manifest does not pin the managed-cluster agent tag"

mapfile -t workload_deployments < <(jq -r '.items[].metadata.name' <<<"$deployments_json")
for deployment in "${workload_deployments[@]}"; do
  kubectl rollout status --namespace "$namespace" "deployment/${deployment}" --timeout "$timeout"
done

mapfile -t server_services < <(kubectl get service --namespace "$namespace" \
  -l "app.kubernetes.io/instance=${release},app.kubernetes.io/name=astronomer,app.kubernetes.io/component=server" \
  -o go-template='{{range .items}}{{$name := .metadata.name}}{{range .spec.ports}}{{if eq .port 8000}}{{$name}}{{"\n"}}{{end}}{{end}}{{end}}')
((${#server_services[@]} == 1)) || \
  die "expected one Astronomer server Service on port 8000, found ${#server_services[@]}"
kubectl port-forward --namespace "$namespace" "service/${server_services[0]}" "${health_port}:8000" \
  >"$backup_dir/port-forward.log" 2>&1 &
port_forward_pid=$!

ready=0
for _ in $(seq 1 60); do
  kill -0 "$port_forward_pid" >/dev/null 2>&1 || \
    die "server port-forward exited; see $backup_dir/port-forward.log"
  if curl --fail --silent --show-error "http://127.0.0.1:${health_port}/readyz" \
    >"$backup_dir/readyz.json" 2>"$backup_dir/readyz.stderr"; then
    ready=1
    break
  fi
  sleep 2
done
((ready == 1)) || die "readiness verification failed"

helm status "$release" --namespace "$namespace" >"$backup_dir/status-after.txt"
printf 'Upgrade complete: %s/%s is running exact release %s. Backup: %s\n' \
  "$namespace" "$release" "$image_tag" "$backup_dir"
