#!/usr/bin/env bash
# Upgrade an existing Astronomer Helm install to an exact published release.
# State and operator values are preserved; first-party development image
# overrides are deliberately replaced with the release images.

set -euo pipefail

usage() {
  cat <<'EOF'
Usage: scripts/upgrade-release.sh [--yes] [--dry-run-only] <version>

  version             Exact release, for example v0.3.7 or 0.3.7
  --yes               Perform the upgrade after the server-side dry run
  --dry-run-only      Stop after backup and server-side Helm dry run

Environment:
  RELEASE             Helm release name (default: astronomer)
  NAMESPACE           Helm namespace (default: astronomer)
  CHART_REF           OCI chart reference
  BACKUP_ROOT         Private backup parent (default: ./astronomer-upgrade-backups)
  TIMEOUT             Helm timeout (default: 15m)
  HEALTH_PORT         Temporary localhost port (default: 18080)
  EXTERNAL_DB_BACKUP_CONFIRMED=1
                      Required when bundled Postgres is not present
EOF
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
[[ "$chart_version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || {
  printf 'version must be an exact stable semantic version, got %q\n' "$version" >&2
  exit 2
}

release="${RELEASE:-astronomer}"
namespace="${NAMESPACE:-astronomer}"
chart_ref="${CHART_REF:-oci://ghcr.io/alphabravo-oss/charts/astronomer}"
backup_root="${BACKUP_ROOT:-./astronomer-upgrade-backups}"
timeout="${TIMEOUT:-15m}"
health_port="${HEALTH_PORT:-18080}"

for tool in helm kubectl curl; do
  command -v "$tool" >/dev/null 2>&1 || { printf 'missing required tool: %s\n' "$tool" >&2; exit 1; }
done
helm upgrade --help | grep -q -- '--reset-then-reuse-values' || {
  printf 'this upgrade requires Helm with --reset-then-reuse-values support\n' >&2
  exit 1
}
helm status "$release" --namespace "$namespace" >/dev/null
kubectl auth can-i get secrets --namespace "$namespace" | grep -qx yes || {
  printf 'current Kubernetes identity cannot back up Secrets in namespace %s\n' "$namespace" >&2
  exit 1
}

umask 077
timestamp="$(date -u +%Y%m%dT%H%M%SZ)"
backup_dir="${backup_root%/}/${release}-${timestamp}"
mkdir -p "$backup_dir"

printf 'Resolving exact chart %s:%s\n' "$chart_ref" "$chart_version"
helm show chart "$chart_ref" --version "$chart_version" >"$backup_dir/target-chart.yaml"
grep -qx "version: ${chart_version}" "$backup_dir/target-chart.yaml" || {
  printf 'registry returned a chart whose version does not match %s\n' "$chart_version" >&2
  exit 1
}

printf 'Capturing Helm state, manifests, Secret recovery material, and history in %s\n' "$backup_dir"
helm get values "$release" --namespace "$namespace" --all >"$backup_dir/values-all.yaml"
helm get values "$release" --namespace "$namespace" >"$backup_dir/values-user.yaml"
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
  --set-string "config.agentImageRepository=ghcr.io/alphabravo-oss/astronomer-go-agent"
  --set-string "config.agentImageTag=${image_tag}"
  --set-string "kubectlShell.image=ghcr.io/alphabravo-oss/astronomer-shell:${image_tag}"
  --timeout "$timeout"
)

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

printf 'Upgrading %s/%s to %s (Helm will roll back atomically on failure)\n' "$namespace" "$release" "$image_tag"
helm upgrade "${release_args[@]}" --atomic --cleanup-on-fail

helm get manifest "$release" --namespace "$namespace" >"$backup_dir/manifest-after.yaml"
for image in astronomer-go-server astronomer-go-worker astronomer-go-migrate astronomer-frontend astronomer-shell; do
  grep -q "ghcr.io/alphabravo-oss/${image}:${image_tag}" "$backup_dir/manifest-after.yaml" || {
    printf 'post-upgrade manifest does not pin %s to %s; rolling back\n' "$image" "$image_tag" >&2
    previous_revision="$(awk '/revision:/ { revision=$2 } END { print revision }' "$backup_dir/history.yaml")"
    helm rollback "$release" "$previous_revision" --namespace "$namespace" --wait --timeout "$timeout"
    exit 1
  }
done
if ! grep -q 'AGENT_IMAGE_REPOSITORY: "ghcr.io/alphabravo-oss/astronomer-go-agent"' "$backup_dir/manifest-after.yaml" ||
   ! grep -q "AGENT_IMAGE_TAG: \"${image_tag}\"" "$backup_dir/manifest-after.yaml"; then
  printf 'post-upgrade manifest does not pin the managed-cluster agent to %s; rolling back\n' "$image_tag" >&2
  previous_revision="$(awk '/revision:/ { revision=$2 } END { print revision }' "$backup_dir/history.yaml")"
  helm rollback "$release" "$previous_revision" --namespace "$namespace" --wait --timeout "$timeout"
  exit 1
fi

server_service="$(kubectl get service --namespace "$namespace" \
  -l "app.kubernetes.io/instance=${release},app.kubernetes.io/component=server" \
  -o jsonpath='{.items[0].metadata.name}')"
kubectl port-forward --namespace "$namespace" "service/${server_service}" "${health_port}:8000" \
  >"$backup_dir/port-forward.log" 2>&1 &
port_forward_pid=$!
cleanup() { kill "$port_forward_pid" >/dev/null 2>&1 || true; }
trap cleanup EXIT
ready=0
for _ in $(seq 1 60); do
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
printf 'Upgrade complete: %s/%s is running exact release %s. Backup: %s\n' \
  "$namespace" "$release" "$image_tag" "$backup_dir"
