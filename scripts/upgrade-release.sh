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
  KUBE_CONTEXT        Required explicit kubeconfig context
  EXPECTED_KUBE_API_SERVER
                      Required exact API server URL for KUBE_CONTEXT
  RELEASE_ARTIFACT_DIR
                      Optional pre-promotion artifact directory (no network download)
  SANITIZED_EVIDENCE_DIR
                      Sanitized report directory (default: ./astronomer-upgrade-evidence)
  EXTERNAL_DB_BACKUP_CONFIRMED=1
                      Required when bundled PostgreSQL is not present
  EXTERNAL_DB_SCHEMA_VERSION=<n>
                      Required for an external database; must be a clean schema
                      inside the target release's declared upgrade range. The
                      chart hook independently verifies the live database.
  RC_DECRYPT_PROOF_WEBHOOK_ID
                      Optional disposable webhook UUID whose encrypted row must
                      exist in the clean restored database
  RC_REPLACE_DATABASE_FROM_BACKUP=1
                      Owned-disposable RC only: replace the live database with
                      the verified backup restore before final target startup
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
kube_context="${KUBE_CONTEXT:-}"
expected_kube_api_server="${EXPECTED_KUBE_API_SERVER:-}"
release_artifact_dir="${RELEASE_ARTIFACT_DIR:-}"
sanitized_evidence_dir="${SANITIZED_EVIDENCE_DIR:-./astronomer-upgrade-evidence}"

[[ -n "$kube_context" ]] || die "KUBE_CONTEXT is required; ambient Kubernetes contexts are forbidden"
[[ "$kube_context" != *[$'\t\r\n']* ]] || die "KUBE_CONTEXT contains unsafe whitespace"
[[ "$expected_kube_api_server" =~ ^https://[^[:space:]]+$ ]] || die "EXPECTED_KUBE_API_SERVER must be an explicit https URL"

if [[ ! "$health_port" =~ ^[1-9][0-9]{0,4}$ ]] || ((health_port > 65535)); then
  die "HEALTH_PORT must be 1-65535"
fi
[[ "$min_ready_nodes" =~ ^[1-9][0-9]*$ ]] || die "MIN_READY_NODES must be a positive integer"
[[ "$min_backup_free_kib" =~ ^[1-9][0-9]*$ ]] || die "MIN_BACKUP_FREE_KIB must be a positive integer"

for tool in helm kubectl curl gh cosign sha256sum cmp jq awk sort df grep tar; do
  command -v "$tool" >/dev/null 2>&1 || die "missing required tool: $tool"
done
helm upgrade --help | grep -q -- '--reset-then-reuse-values' || \
  die "this upgrade requires Helm with --reset-then-reuse-values support"

helm_cmd=(helm --kube-context "$kube_context")
kubectl_cmd=(kubectl --context "$kube_context")
actual_kube_api_server="$("${kubectl_cmd[@]}" config view --minify --raw -o jsonpath='{.clusters[0].cluster.server}')"
[[ -n "$actual_kube_api_server" && "$actual_kube_api_server" == "$expected_kube_api_server" ]] || \
  die "Kubernetes API identity mismatch for explicit context $kube_context"

status_json="$("${helm_cmd[@]}" status "$release" --namespace "$namespace" --output json)"
[[ "$(jq -r '.info.status // empty' <<<"$status_json")" == "deployed" ]] || \
  die "Helm release ${namespace}/${release} is not deployed"
"${kubectl_cmd[@]}" auth can-i get secrets --namespace "$namespace" | grep -qx yes || \
  die "current Kubernetes identity cannot back up Secrets in namespace $namespace"

current_metadata="$("${helm_cmd[@]}" get metadata "$release" --namespace "$namespace" --output json)"
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
ready_nodes="$("${kubectl_cmd[@]}" get nodes -o json | jq '[.items[] | select(.spec.unschedulable != true) | select(any(.status.conditions[]?; .type == "Ready" and .status == "True"))] | length')"
((ready_nodes >= min_ready_nodes)) || \
  die "only $ready_nodes schedulable Ready nodes; require at least $min_ready_nodes"

deployments_json="$("${kubectl_cmd[@]}" get deployments --namespace "$namespace" \
  -l "app.kubernetes.io/instance=${release},app.kubernetes.io/part-of=astronomer" -o json)"
jq -e '(.items | length) > 0 and all(.items[];
  (.metadata.generation <= (.status.observedGeneration // 0)) and
  ((.status.availableReplicas // 0) >= (.spec.replicas // 0)) and
  ((.status.updatedReplicas // 0) >= (.spec.replicas // 0)))' \
  <<<"$deployments_json" >/dev/null || \
  die "one or more Astronomer deployments are not fully observed, updated, and available"

pdb_json="$("${kubectl_cmd[@]}" get poddisruptionbudgets --namespace "$namespace" \
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
if [[ -n "$release_artifact_dir" ]]; then
  [[ -d "$release_artifact_dir" ]] || die "RELEASE_ARTIFACT_DIR is not a directory"
  for asset in "astronomer-${chart_version}.tgz" release-manifest.json release-manifest.sigstore.json; do
    [[ -f "$release_artifact_dir/$asset" && ! -L "$release_artifact_dir/$asset" ]] || die "pre-promotion artifact is missing regular file $asset"
    cp -- "$release_artifact_dir/$asset" "$backup_dir/release-assets/$asset"
  done
  if [[ -f "$release_artifact_dir/RELEASE_IMAGES" && ! -L "$release_artifact_dir/RELEASE_IMAGES" ]]; then
    cp -- "$release_artifact_dir/RELEASE_IMAGES" "$backup_dir/release-assets/RELEASE_IMAGES"
  else
    for component in server worker agent migrate frontend shell; do
      [[ -f "$release_artifact_dir/${component}.digest" && ! -L "$release_artifact_dir/${component}.digest" ]] || die "pre-promotion artifact is missing regular file ${component}.digest"
      sed -n '1p' "$release_artifact_dir/${component}.digest"
    done | sort --unique >"$backup_dir/release-assets/RELEASE_IMAGES"
  fi
  # SHA256SUMS is created only during final GitHub Release promotion. Before
  # promotion, Actions artifact integrity plus the signed manifest's exact
  # chart/image content binding is the trust root; this local inventory exists
  # only so the common verification path can reject truncation/duplication.
  (
    cd "$backup_dir/release-assets"
    sha256sum RELEASE_IMAGES "astronomer-${chart_version}.tgz" release-manifest.json release-manifest.sigstore.json >SHA256SUMS
  )
else
  gh release download "$image_tag" --repo "$release_repo" \
    --dir "$backup_dir/release-assets" \
    --pattern RELEASE_IMAGES --pattern SHA256SUMS \
    --pattern release-manifest.json --pattern release-manifest.sigstore.json \
    --pattern "astronomer-${chart_version}.tgz"
fi

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
verify_release_asset release-manifest.json
verify_release_asset release-manifest.sigstore.json
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

runtime_digest() {
  local source="$1"
  jq -er --arg source "$source" '.astronomer.runtime_images[] | select(.source_reference==$source) | .reference | split("@")[1] | select(test("^sha256:[a-f0-9]{64}$"))' "$backup_dir/release-assets/release-manifest.json"
}

release_identity="https://github.com/alphabravo-oss/astronomer/.github/workflows/release.yaml@refs/tags/${image_tag}"
cosign verify-blob \
  --bundle "$backup_dir/release-assets/release-manifest.sigstore.json" \
  --certificate-identity "$release_identity" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  "$backup_dir/release-assets/release-manifest.json" >/dev/null
jq -e --arg version "$image_tag" --arg chart_digest "sha256:$(sha256sum "$backup_dir/release-assets/astronomer-${chart_version}.tgz" | awk '{print $1}')" '
  .schema_version == 1 and .release.version == $version and
  .astronomer.chart.kind == "helm_chart" and .astronomer.chart.content_digest == $chart_digest
' "$backup_dir/release-assets/release-manifest.json" >/dev/null || \
  die "signed release manifest does not bind the exact target tag and chart"
jq -e --rawfile refs "$backup_dir/release-assets/RELEASE_IMAGES" '
  ([.astronomer.images[].reference] | sort) == ($refs | split("\n") | map(select(length > 0)) | sort)
' "$backup_dir/release-assets/release-manifest.json" >/dev/null || \
  die "signed release manifest does not bind the exact first-party image set"
for ref in "$server_ref" "$worker_ref" "$agent_ref" "$migrate_ref" "$frontend_ref" "$shell_ref"; do
  cosign verify \
    --certificate-identity "$release_identity" \
    --certificate-oidc-issuer https://token.actions.githubusercontent.com \
    "$ref" >/dev/null
done

printf 'Resolving exact chart %s:%s\n' "$chart_ref" "$chart_version"
"${helm_cmd[@]}" show chart "$chart_ref" --version "$chart_version" >"$backup_dir/target-chart.yaml"
grep -qx "version: ${chart_version}" "$backup_dir/target-chart.yaml" || \
  die "registry returned a chart whose version does not match $chart_version"
"${helm_cmd[@]}" pull "$chart_ref" --version "$chart_version" --destination "$backup_dir/oci-chart"
cmp --silent \
  "$backup_dir/release-assets/astronomer-${chart_version}.tgz" \
  "$backup_dir/oci-chart/astronomer-${chart_version}.tgz" || \
  die "published OCI chart does not match the checksum-verified release chart"

target_compatibility="$(tar -xOf "$backup_dir/oci-chart/astronomer-${chart_version}.tgz" astronomer/files/release-compatibility.json)" || \
  die "target chart does not contain release-compatibility.json"
minimum_upgrade_schema="$(jq -er '.postgresql.minimum_upgrade_schema | numbers' <<<"$target_compatibility")" || \
  die "target chart has no valid minimum_upgrade_schema"
target_schema="$(jq -er '.postgresql.target_schema | numbers' <<<"$target_compatibility")" || \
  die "target chart has no valid target_schema"
[[ "$minimum_upgrade_schema" =~ ^[1-9][0-9]*$ && "$target_schema" =~ ^[1-9][0-9]*$ ]] || \
  die "target chart schema bounds must be positive integers"
((minimum_upgrade_schema <= target_schema)) || \
  die "target chart schema range is invalid: ${minimum_upgrade_schema}-${target_schema}"
printf '%s\n' "$target_compatibility" >"$backup_dir/target-release-compatibility.json"

printf 'Capturing Helm state, manifests, recovery Secrets, and history in %s\n' "$backup_dir"
"${helm_cmd[@]}" get values "$release" --namespace "$namespace" --all --output yaml >"$backup_dir/values-all.yaml"
"${helm_cmd[@]}" get values "$release" --namespace "$namespace" --output yaml >"$backup_dir/values-user.yaml"
"${helm_cmd[@]}" get manifest "$release" --namespace "$namespace" >"$backup_dir/manifest-before.yaml"
"${helm_cmd[@]}" history "$release" --namespace "$namespace" --output json >"$backup_dir/history.json"
"${kubectl_cmd[@]}" get secrets --namespace "$namespace" \
  -l "app.kubernetes.io/instance=${release}" -o yaml >"$backup_dir/release-secrets.yaml"
previous_revision="$(jq -r 'map(.revision | tonumber) | max' "$backup_dir/history.json")"
[[ "$previous_revision" =~ ^[1-9][0-9]*$ ]] || die "could not determine current Helm revision"

postgres_pod="$("${kubectl_cmd[@]}" get pods --namespace "$namespace" \
  -l "app.kubernetes.io/instance=${release},app.kubernetes.io/component=postgres" \
  -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)"
if [[ -n "$postgres_pod" ]]; then
  command -v pg_restore >/dev/null 2>&1 || die "missing required tool for bundled-database restore verification: pg_restore"
  printf 'Verifying and backing up bundled PostgreSQL from pod %s\n' "$postgres_pod"
  # POSTGRES_* is intentionally expanded inside the remote pod shell.
  # shellcheck disable=SC2016
  migration_state="$("${kubectl_cmd[@]}" exec --namespace "$namespace" "$postgres_pod" -- sh -ec \
    'psql -X -v ON_ERROR_STOP=1 --username="$POSTGRES_USER" --dbname="$POSTGRES_DB" -AtF "|" -c "SELECT count(*), COALESCE(max(version),0), COALESCE(bool_or(dirty),false) FROM schema_migrations"')"
  IFS='|' read -r migration_rows migration_version migration_dirty <<<"$migration_state"
  [[ "$migration_rows" == 1 && "$migration_dirty" == f && "$migration_version" =~ ^[1-9][0-9]*$ ]] || \
    die "bundled PostgreSQL schema_migrations is not one clean row (found $migration_state)"
  ((migration_version >= minimum_upgrade_schema && migration_version <= target_schema)) || \
    die "bundled PostgreSQL schema ${migration_version} is outside target release range ${minimum_upgrade_schema}-${target_schema}"
  # POSTGRES_* is intentionally expanded inside the remote pod shell.
  # shellcheck disable=SC2016
  database_bytes="$("${kubectl_cmd[@]}" exec --namespace "$namespace" "$postgres_pod" -- sh -ec \
    'psql -X -v ON_ERROR_STOP=1 --username="$POSTGRES_USER" --dbname="$POSTGRES_DB" -Atqc "SELECT pg_database_size(current_database())"')"
  [[ "$database_bytes" =~ ^[1-9][0-9]*$ ]] || die "could not determine bundled PostgreSQL size"
  database_kib=$(((database_bytes + 1023) / 1024))
  backup_free_kib="$(df -Pk "$backup_root" | awk 'NR == 2 {print $4}')"
  required_backup_kib=$((database_kib + min_backup_free_kib))
  ((backup_free_kib >= required_backup_kib)) || \
    die "backup filesystem has ${backup_free_kib} KiB free; bundled database plus reserve requires ${required_backup_kib} KiB"
  # POSTGRES_* is intentionally expanded inside the remote pod shell.
  # shellcheck disable=SC2016
  "${kubectl_cmd[@]}" exec --namespace "$namespace" "$postgres_pod" -- sh -ec \
    'pg_dump --format=custom --username="$POSTGRES_USER" --dbname="$POSTGRES_DB"' \
    >"$backup_dir/astronomer.pgcustom"
  pg_restore --list "$backup_dir/astronomer.pgcustom" >"$backup_dir/astronomer.pgcustom.list" || \
    die "bundled PostgreSQL backup is not a readable custom-format archive"

  verify_database="astronomer_upgrade_verify_${timestamp//[^0-9A-Za-z]/_}"
  # Positional parameters and POSTGRES_USER expand inside the remote pod shell.
  # shellcheck disable=SC2016
  "${kubectl_cmd[@]}" exec --namespace "$namespace" "$postgres_pod" -- sh -ec \
    'createdb --username="$POSTGRES_USER" "$1"' sh "$verify_database" || \
    die "could not create temporary database for restore verification"
  restore_status=0
  # Positional parameters and POSTGRES_USER expand inside the remote pod shell.
  # shellcheck disable=SC2016
  "${kubectl_cmd[@]}" exec --namespace "$namespace" -i "$postgres_pod" -- sh -ec \
    'pg_restore --exit-on-error --no-owner --no-privileges --username="$POSTGRES_USER" --dbname="$1"' sh "$verify_database" \
    <"$backup_dir/astronomer.pgcustom" || restore_status=$?
  restored_state=""
  if ((restore_status == 0)); then
    # Positional parameters and POSTGRES_USER expand inside the remote pod shell.
    # shellcheck disable=SC2016
    restored_state="$("${kubectl_cmd[@]}" exec --namespace "$namespace" "$postgres_pod" -- sh -ec \
      'psql -X -v ON_ERROR_STOP=1 --username="$POSTGRES_USER" --dbname="$1" -AtF "|" -c "SELECT count(*), COALESCE(max(version),0), COALESCE(bool_or(dirty),false) FROM schema_migrations"' sh "$verify_database")" || restore_status=$?
  fi
  if ((restore_status == 0)) && [[ -n "${RC_DECRYPT_PROOF_WEBHOOK_ID:-}" ]]; then
    [[ "$RC_DECRYPT_PROOF_WEBHOOK_ID" =~ ^[a-f0-9-]{36}$ ]] || die "RC_DECRYPT_PROOF_WEBHOOK_ID must be a UUID"
    # Positional parameters and POSTGRES_USER expand inside the remote pod shell.
    # shellcheck disable=SC2016
    restored_proof="$("${kubectl_cmd[@]}" exec --namespace "$namespace" "$postgres_pod" -- sh -ec \
      'psql -X -v ON_ERROR_STOP=1 --username="$POSTGRES_USER" --dbname="$1" -At --set=proof_id="$2" -c "SELECT count(*) FROM webhook_subscriptions WHERE id=:'"'"'proof_id'"'"'::uuid AND secret_encrypted <> '"'"''"'"'"' sh "$verify_database" "$RC_DECRYPT_PROOF_WEBHOOK_ID")" || restore_status=$?
    [[ "$restored_proof" == 1 ]] || restore_status=1
    unset restored_proof
  fi
  if [[ "${RC_REPLACE_DATABASE_FROM_BACKUP:-0}" != 1 ]] || ((dry_run_only || !approve)); then
    # Positional parameters and POSTGRES_USER expand inside the remote pod shell.
    # shellcheck disable=SC2016
    "${kubectl_cmd[@]}" exec --namespace "$namespace" "$postgres_pod" -- sh -ec \
      'dropdb --if-exists --username="$POSTGRES_USER" "$1"' sh "$verify_database" >/dev/null || true
  fi
  ((restore_status == 0)) || die "temporary restore verification failed"
  [[ "$restored_state" == "$migration_state" ]] || \
    die "restored backup schema state ${restored_state} does not match source ${migration_state}"
else
  [[ "${EXTERNAL_DB_BACKUP_CONFIRMED:-0}" == 1 ]] || \
    die "confirm a current external database backup with EXTERNAL_DB_BACKUP_CONFIRMED=1"
  external_schema_version="${EXTERNAL_DB_SCHEMA_VERSION:-}"
  [[ "$external_schema_version" =~ ^[1-9][0-9]*$ ]] || \
    die "set EXTERNAL_DB_SCHEMA_VERSION to the live clean schema version"
  ((external_schema_version >= minimum_upgrade_schema && external_schema_version <= target_schema)) || \
    die "external schema ${external_schema_version} is outside target release range ${minimum_upgrade_schema}-${target_schema}"
fi

backup_manifest_files=(values-all.yaml values-user.yaml manifest-before.yaml history.json release-secrets.yaml target-release-compatibility.json)
if [[ -n "$postgres_pod" ]]; then
  backup_manifest_files+=(astronomer.pgcustom astronomer.pgcustom.list)
fi
(
  cd "$backup_dir"
  sha256sum "${backup_manifest_files[@]}" >BACKUP_SHA256SUMS
)

mkdir -p "$sanitized_evidence_dir"
chmod 0755 "$sanitized_evidence_dir"
evidence_path="${sanitized_evidence_dir%/}/${release}-${timestamp}-${image_tag}.json"
release_manifest_sha256="$(sha256sum "$backup_dir/release-assets/release-manifest.json" | awk '{print $1}')"
backup_manifest_sha256="$(sha256sum "$backup_dir/BACKUP_SHA256SUMS" | awk '{print $1}')"
api_identity_sha256="$(printf '%s' "$actual_kube_api_server" | sha256sum | awk '{print $1}')"
write_sanitized_evidence() {
  local result="$1" mode="$2" rollback="${3:-false}" tmp="${evidence_path}.tmp"
  jq -n \
    --arg release "$release" --arg namespace "$namespace" --arg target "$image_tag" \
    --arg mode "$mode" --arg result "$result" \
    --arg api "sha256:${api_identity_sha256}" \
    --arg manifest "sha256:${release_manifest_sha256}" \
    --arg backup "sha256:${backup_manifest_sha256}" \
    --arg restore_mode "${rc_database_restore_mode:-verified_copy}" \
    --argjson rollback "$rollback" --arg completed "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    '{schema_version:1,release:$release,namespace:$namespace,target_version:$target,mode:$mode,result:$result,api_identity_sha256:$api,release_manifest_sha256:$manifest,backup_manifest_sha256:$backup,database_restore_mode:$restore_mode,rollback_attempted:$rollback,completed_at:$completed}' \
    >"$tmp"
  mv -- "$tmp" "$evidence_path"
  chmod 0644 "$evidence_path"
}

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
  --set-string "utilities.busybox.digest=$(runtime_digest busybox:1.36)"
  --set-string "postgres.image.digest=$(runtime_digest postgres:16-alpine)"
  --set-string "redis.image.digest=$(runtime_digest valkey/valkey:8-alpine)"
  --set-string "dex.image.digest=$(runtime_digest dexidp/dex:v2.41.1)"
  --set-string "managementRestoreDrill.sidecar.image.digest=$(runtime_digest postgres:16-alpine)"
  --set-string "managementLogging.image.digest=$(runtime_digest fluent/fluent-bit:3.2.4)"
  --set-file "release.manifest=$backup_dir/release-assets/release-manifest.json"
  --timeout "$timeout"
)

printf 'Running server-side dry run with preserved values and immutable release images\n'
"${helm_cmd[@]}" upgrade "${release_args[@]}" --dry-run=server --hide-secret >"$backup_dir/dry-run.yaml"

if ((dry_run_only)); then
  write_sanitized_evidence passed dry_run false
  printf 'Dry run succeeded; no cluster changes were made. Backup: %s\n' "$backup_dir"
  exit 0
fi
if ((!approve)); then
  write_sanitized_evidence passed dry_run false
  printf 'Dry run succeeded. Review %s and re-run with --yes.\n' "$backup_dir"
  exit 0
fi

port_forward_pid=""
target_upgrade_applied=0
rollback_attempted=0
rc_database_replaced=0
rc_database_restore_mode=verified_copy
rc_deployments=()
restore_rc_replicas() {
  local entry deployment replicas
  for entry in "${rc_deployments[@]}"; do
    IFS=$'\t' read -r deployment replicas <<<"$entry"
    "${kubectl_cmd[@]}" scale --namespace "$namespace" "deployment/$deployment" --replicas="$replicas" >/dev/null || true
  done
}
restore_previous_release() {
  rollback_attempted=1
  printf 'Rolling back %s/%s to Helm revision %s\n' "$namespace" "$release" "$previous_revision" >&2
  "${helm_cmd[@]}" rollback "$release" "$previous_revision" --namespace "$namespace" \
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
  if ((status != 0 && rc_database_replaced)); then
    restore_rc_replicas
  fi
  if ((status != 0)); then
    write_sanitized_evidence failed upgrade "$([[ $rollback_attempted == 1 ]] && printf true || printf false)" || true
  fi
  return "$status"
}
trap cleanup EXIT

if [[ "${RC_REPLACE_DATABASE_FROM_BACKUP:-0}" == 1 ]]; then
  [[ -n "$postgres_pod" && -n "${verify_database:-}" ]] || die "RC live replacement requires the verified bundled database restore"
  # RC order is deliberate: quiesce the previous application, replace its DB
  # with the byte-verified pre-upgrade restore, and only then let the target
  # chart's migration hook advance that restored database.
  mapfile -t rc_deployments < <("${kubectl_cmd[@]}" get deployments --namespace "$namespace" -l "app.kubernetes.io/instance=${release},app.kubernetes.io/name=astronomer" -o json | jq -r '.items[] | select(.metadata.labels["app.kubernetes.io/component"] == "server" or .metadata.labels["app.kubernetes.io/component"] == "worker") | [.metadata.name, (.spec.replicas|tostring)] | @tsv')
  ((${#rc_deployments[@]} == 2)) || die "RC database replacement requires exactly server and worker deployments"
  for entry in "${rc_deployments[@]}"; do IFS=$'\t' read -r deployment _ <<<"$entry"; "${kubectl_cmd[@]}" scale --namespace "$namespace" "deployment/$deployment" --replicas=0; done
  for entry in "${rc_deployments[@]}"; do IFS=$'\t' read -r deployment _ <<<"$entry"; "${kubectl_cmd[@]}" rollout status --namespace "$namespace" "deployment/$deployment" --timeout "$timeout"; done
  # This entire body executes in the remote PostgreSQL pod; its variables and
  # positional parameters must remain literal until that shell runs.
  # shellcheck disable=SC2016
  "${kubectl_cmd[@]}" exec --namespace "$namespace" "$postgres_pod" -- sh -ec '
    psql -X -v ON_ERROR_STOP=1 --username="$POSTGRES_USER" --dbname=postgres --set=live="$POSTGRES_DB" --set=restored="$1" \
      -c "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = :'"'"'live'"'"' AND pid <> pg_backend_pid();" \
      -c "DROP DATABASE :\"live\";" \
      -c "ALTER DATABASE :\"restored\" RENAME TO :\"live\";"
  ' sh "$verify_database" || die "owned-disposable RC database replacement failed"
  rc_database_replaced=1
  rc_database_restore_mode=live_replaced
fi

printf 'Upgrading %s/%s to %s with atomic Helm ownership\n' "$namespace" "$release" "$image_tag"
"${helm_cmd[@]}" upgrade "${release_args[@]}" --atomic --cleanup-on-fail --wait --wait-for-jobs
target_upgrade_applied=1

"${helm_cmd[@]}" get manifest "$release" --namespace "$namespace" >"$backup_dir/manifest-after.yaml"
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
  "${kubectl_cmd[@]}" rollout status --namespace "$namespace" "deployment/${deployment}" --timeout "$timeout"
done

if [[ -n "$postgres_pod" ]]; then
  # POSTGRES_* is intentionally expanded inside the remote pod shell.
  # shellcheck disable=SC2016
  post_migration_state="$("${kubectl_cmd[@]}" exec --namespace "$namespace" "$postgres_pod" -- sh -ec \
    'psql -X -v ON_ERROR_STOP=1 --username="$POSTGRES_USER" --dbname="$POSTGRES_DB" -AtF "|" -c "SELECT count(*), COALESCE(max(version),0), COALESCE(bool_or(dirty),false) FROM schema_migrations"')"
  [[ "$post_migration_state" == "1|${target_schema}|f" ]] || \
    die "post-upgrade database schema is ${post_migration_state}; expected one clean row at ${target_schema}"
  printf '%s\n' "$post_migration_state" >"$backup_dir/schema-after.txt"
fi

# The dollar expressions belong to kubectl's Go template, not this shell.
# shellcheck disable=SC2016
mapfile -t server_services < <("${kubectl_cmd[@]}" get service --namespace "$namespace" \
  -l "app.kubernetes.io/instance=${release},app.kubernetes.io/name=astronomer,app.kubernetes.io/component=server" \
  -o go-template='{{range .items}}{{$name := .metadata.name}}{{range .spec.ports}}{{if eq .port 8000}}{{$name}}{{"\n"}}{{end}}{{end}}{{end}}')
((${#server_services[@]} == 1)) || \
  die "expected one Astronomer server Service on port 8000, found ${#server_services[@]}"
"${kubectl_cmd[@]}" port-forward --namespace "$namespace" "service/${server_services[0]}" "${health_port}:8000" \
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

"${helm_cmd[@]}" status "$release" --namespace "$namespace" >"$backup_dir/status-after.txt"
write_sanitized_evidence passed upgrade "$([[ $rollback_attempted == 1 ]] && printf true || printf false)"
printf 'Upgrade complete: %s/%s is running exact release %s. Backup: %s\n' \
  "$namespace" "$release" "$image_tag" "$backup_dir"
