#!/usr/bin/env bash
# Upgrade an existing Astronomer Helm install to an exact published release.
# State and operator values are preserved; first-party development image
# overrides are deliberately replaced with the release images.

set -euo pipefail

post_render_quiesced_argo_controller() {
  awk '
    function flush(    i, kind, name, in_metadata, in_spec, replacements) {
      if (line_count == 0) return
      kind = ""
      name = ""
      in_metadata = 0
      for (i = 1; i <= line_count; i++) {
        if (doc[i] ~ /^kind:[[:space:]]*StatefulSet[[:space:]]*$/) kind = "StatefulSet"
        if (doc[i] ~ /^metadata:[[:space:]]*$/) {
          in_metadata = 1
          continue
        }
        if (in_metadata && doc[i] ~ /^[^[:space:]#]/) in_metadata = 0
        if (in_metadata && doc[i] ~ /^  name:[[:space:]]*astro-argocd-application-controller[[:space:]]*$/) {
          name = "astro-argocd-application-controller"
        }
      }
      if (kind == "StatefulSet" && name == "astro-argocd-application-controller") {
        target_documents++
        in_spec = 0
        replacements = 0
        for (i = 1; i <= line_count; i++) {
          if (doc[i] ~ /^spec:[[:space:]]*$/) {
            in_spec = 1
            continue
          }
          if (in_spec && doc[i] ~ /^[^[:space:]#]/) in_spec = 0
          if (in_spec && doc[i] ~ /^  replicas:[[:space:]]*[0-9]+[[:space:]]*$/) {
            doc[i] = "  replicas: 0"
            replacements++
            target_replacements++
          }
        }
        if (replacements != 1) invalid_target = 1
      }
      for (i = 1; i <= line_count; i++) print doc[i]
      delete doc
      line_count = 0
    }
    /^---[[:space:]]*$/ {
      flush()
      print
      next
    }
    { doc[++line_count] = $0 }
    END {
      flush()
      if (target_documents != 1 || target_replacements != 1 || invalid_target) {
        print "quiesce post-renderer expected exactly one target StatefulSet and one top-level replicas field" > "/dev/stderr"
        exit 42
      }
    }
  '
}

if [[ "${1:-}" == "__quiesce-argo-post-renderer" ]]; then
  [[ $# == 1 ]] || { printf 'internal post-renderer accepts no additional arguments\n' >&2; exit 2; }
  post_render_quiesced_argo_controller
  exit
fi

usage() {
  cat <<'EOF'
Usage: scripts/upgrade-release.sh [--yes] [--dry-run-only] [--quiesce-argo-controller] <version>

  version             Exact release, for example v0.3.8 or 0.3.8
  --yes               Perform the upgrade after the server-side dry run
  --dry-run-only      Stop after backup and server-side Helm dry run
  --quiesce-argo-controller
                      Keep astro-argocd-application-controller at zero through
                      a bounded Helm-to-Argo self-managed upgrade. A successful
                      run deliberately leaves the controller stopped for the
                      operator-gated non-pruning acceptance sync.

Environment:
  RELEASE             Helm release name (default: astronomer)
  NAMESPACE           Helm namespace (default: astronomer)
  CHART_REF           OCI chart reference
  RELEASE_REPO        GitHub release repository (default: alphabravo-oss/astronomer)
  BACKUP_ROOT         Private backup parent (default: ./astronomer-upgrade-backups)
  TIMEOUT             Helm timeout (default: 15m)
  HEALTH_PORT         Temporary localhost port (default: 18080)
  ARGO_QUIESCE_TIMEOUT
                      Seconds to wait for controller termination (default: 180)
  EXTERNAL_DB_BACKUP_CONFIRMED=1
                      Required when bundled Postgres is not present
EOF
}

approve=0
dry_run_only=0
quiesce_argo_controller=0
version=""
while (($#)); do
  case "$1" in
    --yes) approve=1 ;;
    --dry-run-only) dry_run_only=1 ;;
    --quiesce-argo-controller) quiesce_argo_controller=1 ;;
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
[[ "$chart_version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || {
  printf 'version must be an exact stable semantic version, got %q\n' "$version" >&2
  exit 2
}

release="${RELEASE:-astronomer}"
namespace="${NAMESPACE:-astronomer}"
chart_ref="${CHART_REF:-oci://ghcr.io/alphabravo-oss/charts/astronomer}"
release_repo="${RELEASE_REPO:-alphabravo-oss/astronomer}"
backup_root="${BACKUP_ROOT:-./astronomer-upgrade-backups}"
timeout="${TIMEOUT:-15m}"
health_port="${HEALTH_PORT:-18080}"
argo_quiesce_timeout="${ARGO_QUIESCE_TIMEOUT:-180}"
argo_controller="astro-argocd-application-controller"
script_path="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)/$(basename -- "${BASH_SOURCE[0]}")"

[[ "$argo_quiesce_timeout" =~ ^[1-9][0-9]*$ ]] || {
  printf 'ARGO_QUIESCE_TIMEOUT must be a positive integer\n' >&2
  exit 2
}

for tool in helm kubectl curl gh cosign sha256sum cmp jq awk; do
  command -v "$tool" >/dev/null 2>&1 || { printf 'missing required tool: %s\n' "$tool" >&2; exit 1; }
done
helm upgrade --help | grep -q -- '--reset-then-reuse-values' || {
  printf 'this upgrade requires Helm with --reset-then-reuse-values support\n' >&2
  exit 1
}
if ((quiesce_argo_controller)); then
  helm upgrade --help | grep -q -- '--post-renderer-args' || {
    printf 'this upgrade requires Helm with --post-renderer-args support\n' >&2
    exit 1
  }
  [[ -x "$script_path" ]] || {
    printf 'quiesce post-renderer must be executable: %s\n' "$script_path" >&2
    exit 1
  }
  kubectl get statefulset --namespace "$namespace" "$argo_controller" >/dev/null
fi
helm status "$release" --namespace "$namespace" >/dev/null
kubectl auth can-i get secrets --namespace "$namespace" | grep -qx yes || {
  printf 'current Kubernetes identity cannot back up Secrets in namespace %s\n' "$namespace" >&2
  exit 1
}

umask 077
timestamp="$(date -u +%Y%m%dT%H%M%SZ)"
backup_dir="${backup_root%/}/${release}-${timestamp}"
mkdir -p "$backup_dir"

current_metadata="$(helm get metadata "$release" --namespace "$namespace" --output json)"
current_chart_name="$(jq -r '.chart // empty' <<<"$current_metadata")"
current_chart_version="$(jq -r '.version // empty' <<<"$current_metadata")"
[[ "$current_chart_name" == "astronomer" ]] || {
  printf 'installed release chart is %q, expected astronomer\n' "$current_chart_name" >&2
  exit 1
}
[[ "$current_chart_version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || {
  printf 'installed chart version is not an exact stable semantic version: %q\n' "$current_chart_version" >&2
  exit 1
}

printf 'Downloading and verifying published release %s from %s\n' "$image_tag" "$release_repo"
mkdir -p "$backup_dir/release-assets" "$backup_dir/oci-chart"
gh release download "$image_tag" --repo "$release_repo" \
  --dir "$backup_dir/release-assets" \
  --pattern RELEASE_IMAGES --pattern SHA256SUMS \
  --pattern "astronomer-${chart_version}.tgz"

verify_release_asset() {
  local name="$1" expected actual
  expected="$(awk -v name="$name" '$2 == name {print $1}' "$backup_dir/release-assets/SHA256SUMS")"
  [[ "$expected" =~ ^[a-f0-9]{64}$ ]] || {
    printf 'SHA256SUMS has no unique valid entry for %s\n' "$name" >&2
    exit 1
  }
  actual="$(sha256sum "$backup_dir/release-assets/$name" | awk '{print $1}')"
  [[ "$actual" == "$expected" ]] || {
    printf 'release checksum mismatch for %s\n' "$name" >&2
    exit 1
  }
}
verify_release_asset RELEASE_IMAGES
verify_release_asset "astronomer-${chart_version}.tgz"
gh attestation verify "$backup_dir/release-assets/astronomer-${chart_version}.tgz" \
  --repo "$release_repo" >/dev/null

release_image_ref() {
  local repository="$1" ref count
  count="$(grep -Ec "^ghcr\\.io/alphabravo-oss/${repository}@sha256:[a-f0-9]{64}$" "$backup_dir/release-assets/RELEASE_IMAGES" || true)"
  [[ "$count" == 1 ]] || {
    printf 'RELEASE_IMAGES must contain exactly one immutable reference for %s\n' "$repository" >&2
    exit 1
  }
  ref="$(grep -E "^ghcr\\.io/alphabravo-oss/${repository}@sha256:[a-f0-9]{64}$" "$backup_dir/release-assets/RELEASE_IMAGES")"
  printf '%s' "$ref"
}
server_ref="$(release_image_ref astronomer-go-server)"
worker_ref="$(release_image_ref astronomer-go-worker)"
agent_ref="$(release_image_ref astronomer-go-agent)"
migrate_ref="$(release_image_ref astronomer-go-migrate)"
frontend_ref="$(release_image_ref astronomer-frontend)"
shell_ref="$(release_image_ref astronomer-shell)"
[[ "$(wc -l <"$backup_dir/release-assets/RELEASE_IMAGES")" == 6 ]] || {
  printf 'RELEASE_IMAGES must contain exactly the six Astronomer first-party images\n' >&2
  exit 1
}
release_identity="https://github.com/alphabravo-oss/astronomer/.github/workflows/release.yaml@refs/tags/${image_tag}"
for ref in "$server_ref" "$worker_ref" "$agent_ref" "$migrate_ref" "$frontend_ref" "$shell_ref"; do
  cosign verify \
    --certificate-identity "$release_identity" \
    --certificate-oidc-issuer https://token.actions.githubusercontent.com \
    "$ref" >/dev/null
done

printf 'Resolving exact chart %s:%s\n' "$chart_ref" "$chart_version"
helm show chart "$chart_ref" --version "$chart_version" >"$backup_dir/target-chart.yaml"
grep -qx "version: ${chart_version}" "$backup_dir/target-chart.yaml" || {
  printf 'registry returned a chart whose version does not match %s\n' "$chart_version" >&2
  exit 1
}
helm pull "$chart_ref" --version "$chart_version" --destination "$backup_dir/oci-chart"
cmp --silent \
  "$backup_dir/release-assets/astronomer-${chart_version}.tgz" \
  "$backup_dir/oci-chart/astronomer-${chart_version}.tgz" || {
  printf 'published OCI chart does not match the checksum-verified GitHub release chart\n' >&2
  exit 1
}
if ((quiesce_argo_controller)); then
  helm show chart "$chart_ref" --version "$current_chart_version" >"$backup_dir/current-chart.yaml"
  grep -qx "version: ${current_chart_version}" "$backup_dir/current-chart.yaml" || {
    printf 'cannot resolve exact installed chart %s for a quiesced failure restore\n' "$current_chart_version" >&2
    exit 1
  }
fi

printf 'Capturing Helm state, manifests, Secret recovery material, and history in %s\n' "$backup_dir"
helm get values "$release" --namespace "$namespace" --all --output yaml >"$backup_dir/values-all.yaml"
helm get values "$release" --namespace "$namespace" --output yaml >"$backup_dir/values-user.yaml"
helm get manifest "$release" --namespace "$namespace" >"$backup_dir/manifest-before.yaml"
helm history "$release" --namespace "$namespace" --output yaml >"$backup_dir/history.yaml"
kubectl get secrets --namespace "$namespace" \
  -l "app.kubernetes.io/instance=${release}" -o yaml >"$backup_dir/release-secrets.yaml"
kubectl get secret --namespace "$namespace" \
  "${release}-bootstrap" "${release}-secrets" -o yaml \
  >"$backup_dir/critical-secrets.yaml" 2>"$backup_dir/critical-secrets.stderr" || true

postgres_pod="$(kubectl get pods --namespace "$namespace" \
  -l "app.kubernetes.io/instance=${release},app.kubernetes.io/component=postgres" \
  -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)"
if [[ -n "$postgres_pod" ]]; then
  printf 'Backing up bundled Postgres from pod %s\n' "$postgres_pod"
  kubectl exec --namespace "$namespace" "$postgres_pod" -- sh -ec \
    'pg_dump --format=custom --username="$POSTGRES_USER" --dbname="$POSTGRES_DB"' \
    >"$backup_dir/astronomer.pgcustom"
elif [[ "${EXTERNAL_DB_BACKUP_CONFIRMED:-0}" != "1" ]]; then
  printf '%s\n' \
    'bundled Postgres was not found; confirm a current external database backup with EXTERNAL_DB_BACKUP_CONFIRMED=1' >&2
  exit 1
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
post_renderer_args=()
if ((quiesce_argo_controller)); then
  post_renderer_args=(
    --post-renderer "$script_path"
    --post-renderer-args __quiesce-argo-post-renderer
  )
  release_args+=("${post_renderer_args[@]}")
fi

printf 'Running server-side dry run with preserved values and exact release images\n'
helm upgrade "${release_args[@]}" --dry-run=server >"$backup_dir/dry-run.yaml"

if ((dry_run_only)); then
  printf 'Dry run succeeded; no cluster changes were made. Backup: %s\n' "$backup_dir"
  exit 0
fi
if ((!approve)); then
  printf 'Dry run succeeded. Re-run with --yes to perform the upgrade. Backup: %s\n' "$backup_dir"
  exit 0
fi

wait_for_argo_quiescence() {
  local deadline state desired current ready updated pods
  deadline=$((SECONDS + argo_quiesce_timeout))
  while ((SECONDS < deadline)); do
    if state="$(kubectl get statefulset --namespace "$namespace" "$argo_controller" -o json 2>/dev/null)"; then
      read -r desired current ready updated < <(
        jq -r '[.spec.replicas // 0, .status.currentReplicas // 0, .status.readyReplicas // 0, .status.updatedReplicas // 0] | @tsv' <<<"$state"
      )
      pods="$(kubectl get pods --namespace "$namespace" \
        -l app.kubernetes.io/name=argocd-application-controller -o json 2>/dev/null \
        | jq -r '.items | length' || true)"
      if [[ "$desired" == 0 && "$current" == 0 && "$ready" == 0 && "$updated" == 0 && "$pods" == 0 ]]; then
        return 0
      fi
    fi
    sleep 2
  done
  return 1
}

controller_guard_active=0
port_forward_pid=""
target_upgrade_applied=0
restore_attempted=0
cleanup() {
  local status=$?
  if [[ -n "$port_forward_pid" ]]; then
    kill "$port_forward_pid" >/dev/null 2>&1 || true
  fi
  if ((status != 0 && target_upgrade_applied && !restore_attempted)); then
    printf 'Post-upgrade verification failed; restoring the prior release\n' >&2
    restore_previous_release || true
  fi
  if ((controller_guard_active)); then
    kubectl scale statefulset --namespace "$namespace" "$argo_controller" --replicas=0 >/dev/null 2>&1 || true
    if ! wait_for_argo_quiescence; then
      printf 'WARNING: Argo controller quiescence could not be reconfirmed during exit containment\n' >&2
    fi
  fi
  return "$status"
}
trap cleanup EXIT

if ((quiesce_argo_controller)); then
  printf 'Quiescing %s/%s before the external Helm rollout\n' "$namespace" "$argo_controller"
  controller_guard_active=1
  kubectl scale statefulset --namespace "$namespace" "$argo_controller" --replicas=0 >/dev/null
  wait_for_argo_quiescence || {
    printf 'Argo application-controller did not fully quiesce within %s seconds\n' "$argo_quiesce_timeout" >&2
    exit 1
  }
fi

restore_previous_release() {
  restore_attempted=1
  if ((quiesce_argo_controller)); then
    printf 'Restoring exact chart %s with the controller still rendered at zero\n' \
      "$current_chart_version" >&2
    if helm upgrade "$release" "$chart_ref" \
      --namespace "$namespace" \
      --version "$current_chart_version" \
      --reset-values \
      --values "$backup_dir/values-user.yaml" \
      "${post_renderer_args[@]}" \
      --wait --wait-for-jobs --cleanup-on-fail --timeout "$timeout"; then
      printf 'Prior chart restored; Argo controller remains stopped. Backup: %s\n' "$backup_dir" >&2
      return 0
    fi
    printf 'FAILURE RESTORE FAILED; Argo controller remains stopped. Recover from %s\n' "$backup_dir" >&2
    return 1
  fi

  previous_revision="$(awk '/revision:/ { revision=$2 } END { print revision }' "$backup_dir/history.yaml")"
  helm rollback "$release" "$previous_revision" --namespace "$namespace" --wait --timeout "$timeout"
}

if ((quiesce_argo_controller)); then
  printf 'Upgrading %s/%s to %s with fail-closed Argo quiescence\n' "$namespace" "$release" "$image_tag"
  printf 'Using a quiesced failure restore because an automatic Helm rollback could restart the old Argo controller\n'
  if ! helm upgrade "${release_args[@]}" --wait --wait-for-jobs --cleanup-on-fail; then
    printf 'Target upgrade failed.\n' >&2
    restore_previous_release || true
    exit 1
  fi
else
  printf 'Upgrading %s/%s to %s (Helm will roll back atomically on failure)\n' "$namespace" "$release" "$image_tag"
  helm upgrade "${release_args[@]}" --atomic --cleanup-on-fail
fi
target_upgrade_applied=1

if ((quiesce_argo_controller)) && ! wait_for_argo_quiescence; then
  printf 'post-upgrade Argo controller quiescence check failed\n' >&2
  exit 1
fi

helm get manifest "$release" --namespace "$namespace" >"$backup_dir/manifest-after.yaml"
for ref in "$server_ref" "$worker_ref" "$migrate_ref" "$frontend_ref" "$shell_ref"; do
  grep -q "$ref" "$backup_dir/manifest-after.yaml" || {
    printf 'post-upgrade manifest does not pin %s; rolling back\n' "$ref" >&2
    restore_previous_release || true
    exit 1
  }
done
if ! grep -q "AGENT_IMAGE_REPOSITORY: \"${agent_ref}\"" "$backup_dir/manifest-after.yaml" ||
   ! grep -q "AGENT_IMAGE_TAG: \"${image_tag}\"" "$backup_dir/manifest-after.yaml"; then
  printf 'post-upgrade manifest does not pin the managed-cluster agent to %s; rolling back\n' "$image_tag" >&2
  restore_previous_release || true
  exit 1
fi

mapfile -t server_services < <(kubectl get service --namespace "$namespace" \
  -l "app.kubernetes.io/instance=${release},app.kubernetes.io/name=astronomer,app.kubernetes.io/component=server" \
  -o go-template='{{range .items}}{{$name := .metadata.name}}{{range .spec.ports}}{{if eq .port 8000}}{{$name}}{{"\n"}}{{end}}{{end}}{{end}}')
if ((${#server_services[@]} != 1)); then
  printf 'expected one Astronomer application server Service exposing port 8000, found %d\n' \
    "${#server_services[@]}" >&2
  exit 1
fi
server_service="${server_services[0]}"
kubectl port-forward --namespace "$namespace" "service/${server_service}" "${health_port}:8000" \
  >"$backup_dir/port-forward.log" 2>&1 &
port_forward_pid=$!
ready=0
for _ in $(seq 1 60); do
  if ! kill -0 "$port_forward_pid" >/dev/null 2>&1; then
    printf 'server port-forward exited before readiness; see %s/port-forward.log\n' "$backup_dir" >&2
    exit 1
  fi
  if curl --fail --silent --show-error "http://127.0.0.1:${health_port}/readyz" \
    >"$backup_dir/readyz.json" 2>"$backup_dir/readyz.stderr"; then
    ready=1
    break
  fi
  sleep 2
done
if ((ready == 0)); then
  printf 'readiness verification failed; use Helm history in %s to select the prior revision\n' "$backup_dir" >&2
  exit 1
fi

helm status "$release" --namespace "$namespace" >"$backup_dir/status-after.txt"
if ((quiesce_argo_controller)); then
  printf 'Upgrade complete: %s/%s is running exact release %s; %s remains stopped for restage and non-pruning acceptance. Backup: %s\n' \
    "$namespace" "$release" "$image_tag" "$argo_controller" "$backup_dir"
else
  printf 'Upgrade complete: %s/%s is running exact release %s. Backup: %s\n' \
    "$namespace" "$release" "$image_tag" "$backup_dir"
fi
