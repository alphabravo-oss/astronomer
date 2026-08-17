#!/usr/bin/env bash
# Exercise the committed Flux distribution in an isolated, disposable k3d
# cluster. The harness never switches or writes the caller's kubeconfig and it
# deletes only the exact test-run-flux-* cluster it created in this process.

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
fixture_root_source="$repo_root/scripts/fixtures/flux"
distribution="$repo_root/deploy/flux/install.yaml"
capacity_check="$repo_root/scripts/check-build-capacity.sh"

default_cluster_name="test-run-flux-$(date -u +%s)-$((BASHPID % 1000000))"
cluster_name="${FLUX_INTEGRATION_CLUSTER:-$default_cluster_name}"
k3s_image="${K3S_IMAGE:-rancher/k3s:v1.35.0-k3s1}"
ready_timeout="${FLUX_INTEGRATION_READY_TIMEOUT:-5m}"
suspend_seconds="${FLUX_INTEGRATION_SUSPEND_SECONDS:-12}"
evidence_max_mib="${FLUX_INTEGRATION_EVIDENCE_MAX_MIB:-50}"
keep_cluster="${FLUX_INTEGRATION_KEEP_CLUSTER:-0}"
evidence_dir="${FLUX_INTEGRATION_EVIDENCE_DIR:-}"
validate_only=0

usage() {
    cat <<'EOF'
Usage: test-flux-integration.sh [--validate-only] [--keep-cluster] [--evidence-dir DIR]

Creates one uniquely named test-run-flux-* k3d cluster, installs the committed
Flux distribution, and proves source/release readiness, drift repair,
suspend/resume, controller restart, API discovery, and deletion. Evidence is
retained under a run-specific temporary directory; kubeconfig credentials and
build scratch data are removed on exit.

--validate-only performs static fixture/distribution checks and never invokes
Docker or k3d.
EOF
}

while (($# > 0)); do
    case "$1" in
        --validate-only)
            validate_only=1
            shift
            ;;
        --keep-cluster)
            keep_cluster=1
            shift
            ;;
        --evidence-dir)
            (($# >= 2)) || { echo "flux-integration: --evidence-dir requires a value" >&2; exit 2; }
            evidence_dir="$2"
            shift 2
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        *)
            echo "flux-integration: unknown argument: $1" >&2
            usage >&2
            exit 2
            ;;
    esac
done

fail() {
    echo "flux-integration: $*" >&2
    exit 1
}

require_command() {
    command -v "$1" >/dev/null 2>&1 || fail "required command is unavailable: $1"
}

require_uint() {
    local name="$1" value="$2"
    [[ "$value" =~ ^[0-9]+$ ]] || fail "$name must be a non-negative integer"
}

validate_inputs() {
    [[ "$cluster_name" =~ ^test-run-flux-[a-z0-9][a-z0-9-]{0,17}$ ]] ||
        fail "cluster name must be unique and match test-run-flux-[a-z0-9-] (maximum 32 characters)"
    [[ "$ready_timeout" =~ ^[1-9][0-9]*[sm]$ ]] || fail "ready timeout must be a positive seconds/minutes duration"
    require_uint "suspend observation seconds" "$suspend_seconds"
    ((suspend_seconds >= 10 && suspend_seconds <= 120)) || fail "suspend observation seconds must be between 10 and 120"
    require_uint "evidence maximum MiB" "$evidence_max_mib"
    ((evidence_max_mib >= 10 && evidence_max_mib <= 500)) || fail "evidence maximum MiB must be between 10 and 500"
    [[ "$keep_cluster" == 0 || "$keep_cluster" == 1 ]] || fail "keep-cluster must be 0 or 1"
}

validate_static() {
    require_command git
    require_command go
    require_command helm
    require_command kubectl
    require_command sha256sum
    require_command sed
    "$capacity_check" --path "$repo_root" >/dev/null
    (
        cd "$repo_root/deploy/flux"
        sha256sum --check checksums.txt >/dev/null
    )
    # kubectl apply still performs API discovery in client dry-run mode. Render
    # the committed overlay instead so validation never consults any current
    # kubeconfig or cluster.
    kubectl kustomize "$repo_root/deploy/flux" >/dev/null
    go test "$repo_root/scripts/fixtures/flux/server" >/dev/null
    helm lint "$fixture_root_source/chart" \
        --set image.repository=astronomer-flux-fixture-server \
        --set image.tag=validation \
        --set image.pullPolicy=Never >/dev/null
    local template placeholder
    template="$fixture_root_source/integration-resources.yaml.tmpl"
    for placeholder in __TEST_NAMESPACE__ __FIXTURE_PORT__ __IMAGE_TAG__; do
        grep -q "$placeholder" "$template" || fail "integration template is missing $placeholder"
    done
    grep -q 'app.kubernetes.io/managed-by: astronomer-agent' "$template" ||
        fail "integration Flux resources do not carry the controller watch label"
    grep -q 'driftDetection:' "$template" || fail "Helm drift detection fixture is missing"
    grep -q 'deletionPolicy: Delete' "$template" || fail "Kustomization deletion policy fixture is missing"
    echo "flux-integration: static validation passed; Docker and k3d were not invoked"
}

validate_inputs
if ((validate_only == 1)); then
    validate_static
    exit 0
fi

for command in bash docker k3d kubectl helm git go sha256sum sed awk grep curl; do
    require_command "$command"
done

if [[ -z "$evidence_dir" ]]; then
    evidence_dir="$(mktemp -d "${TMPDIR:-/tmp}/astronomer-flux-integration.XXXXXXXX")"
else
    if [[ -e "$evidence_dir" && ! -d "$evidence_dir" ]]; then
        fail "evidence path exists and is not a directory"
    fi
    mkdir -p "$evidence_dir"
    if find "$evidence_dir" -mindepth 1 -maxdepth 1 -print -quit | grep -q .; then
        fail "evidence directory must be empty"
    fi
    evidence_dir="$(cd "$evidence_dir" && pwd)"
fi
chmod 0700 "$evidence_dir"
exec > >(tee -a "$evidence_dir/integration.log") 2>&1

work_dir="$(mktemp -d "${TMPDIR:-/tmp}/astronomer-flux-work.XXXXXXXX")"
kubeconfig_file="$work_dir/kubeconfig"
fixture_root="$work_dir/fixtures"
rendered_resources="$work_dir/integration-resources.yaml"
fixture_binary="$work_dir/image/flux-fixture-server"
fixture_port_file="$work_dir/fixture.port"
run_token="${cluster_name#test-run-flux-}"
image_tag="$(printf '%s' "$run_token" | tr -cd 'a-z0-9-' | cut -c1-40)"
fixture_image="astronomer-flux-fixture-server:${image_tag}"
namespace="flux-test-$(printf '%s' "$run_token" | tr -cd 'a-z0-9-' | cut -c1-40)"
created_cluster=0
created_image=0
fixture_pid=""
current_phase="preflight"
passed_phases=()

record_pass() {
    passed_phases+=("$current_phase")
    echo "flux-integration: PASS $current_phase"
}

write_junit() {
    local exit_code="$1" failures=0 tests phase
    tests="${#passed_phases[@]}"
    if ((exit_code != 0)); then
        failures=1
        tests=$((tests + 1))
    fi
    {
        printf '<?xml version="1.0" encoding="UTF-8"?>\n'
        printf '<testsuite name="flux-integration" tests="%d" failures="%d">\n' "$tests" "$failures"
        for phase in "${passed_phases[@]}"; do
            printf '  <testcase classname="flux.integration" name="%s"/>\n' "$phase"
        done
        if ((exit_code != 0)); then
            printf '  <testcase classname="flux.integration" name="%s"><failure message="phase failed"/></testcase>\n' "$current_phase"
        fi
        printf '</testsuite>\n'
    } >"$evidence_dir/junit.xml"
}

capture_evidence() {
    ((created_cluster == 1)) || return 0
    {
        echo "cluster=$cluster_name"
        echo "namespace=$namespace"
        echo "k3s_image=$k3s_image"
        echo "flux_version=$(tr -d '\n' <"$repo_root/deploy/flux/VERSION")"
        echo "captured_at=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
        kubectl version -o yaml 2>/dev/null || true
        helm version --short 2>/dev/null || true
        k3d version 2>/dev/null || true
    } >"$evidence_dir/versions.txt"
    kubectl get gitrepositories.source.toolkit.fluxcd.io,kustomizations.kustomize.toolkit.fluxcd.io,helmrepositories.source.toolkit.fluxcd.io,helmcharts.source.toolkit.fluxcd.io,helmreleases.helm.toolkit.fluxcd.io \
        -A -o yaml >"$evidence_dir/flux-objects.yaml" 2>&1 || true
    kubectl -n astronomer-delivery-system get deployments,pods,networkpolicies -o wide \
        >"$evidence_dir/controllers.txt" 2>&1 || true
    kubectl -n "$namespace" get all,configmaps,secrets -o wide \
        >"$evidence_dir/workloads.txt" 2>&1 || true
    kubectl get events -A --sort-by=.metadata.creationTimestamp 2>/dev/null | tail -n 300 \
        >"$evidence_dir/events.txt" || true
    local controller
    for controller in source-controller kustomize-controller helm-controller; do
        kubectl -n astronomer-delivery-system logs "deployment/$controller" --all-containers \
            --tail=400 --limit-bytes=1048576 >"$evidence_dir/${controller}.log" 2>&1 || true
    done
}

on_exit() {
    local exit_code="$?" evidence_kib
    trap - EXIT
    set +e
    capture_evidence
    write_junit "$exit_code"
    if [[ -n "$fixture_pid" ]]; then
        kill "$fixture_pid" >/dev/null 2>&1 || true
        wait "$fixture_pid" >/dev/null 2>&1 || true
    fi
    if ((created_cluster == 1)); then
        if [[ "$keep_cluster" == 1 ]]; then
            echo "flux-integration: retained requested disposable cluster $cluster_name"
        elif [[ "$cluster_name" == test-run-flux-* ]]; then
            echo "flux-integration: deleting owned disposable cluster $cluster_name"
            k3d cluster delete "$cluster_name" >/dev/null 2>&1 || exit_code=1
        else
            echo "flux-integration: REFUSE cleanup for unexpected cluster name $cluster_name" >&2
            exit_code=1
        fi
    fi
    if ((created_image == 1)); then
        docker image rm "$fixture_image" >/dev/null 2>&1 || true
    fi
    if [[ "$work_dir" == "${TMPDIR:-/tmp}/astronomer-flux-work."* && -d "$work_dir" ]]; then
        rm -rf -- "$work_dir"
    else
        echo "flux-integration: REFUSE cleanup for unexpected work directory $work_dir" >&2
        exit_code=1
    fi
    evidence_kib="$(du -sk "$evidence_dir" | awk '{print $1}')"
    printf '{"cluster":"%s","namespace":"%s","exit_code":%d,"evidence_kib":%d,"completed_at":"%s"}\n' \
        "$cluster_name" "$namespace" "$exit_code" "$evidence_kib" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
        >"$evidence_dir/run.json"
    if ((evidence_kib > evidence_max_mib * 1024)); then
        echo "flux-integration: evidence exceeded ${evidence_max_mib}MiB bound" >&2
        exit_code=1
    fi
    echo "flux-integration: evidence retained at $evidence_dir (${evidence_kib} KiB)"
    exit "$exit_code"
}
trap on_exit EXIT
trap 'exit 130' INT TERM HUP

wait_ready() {
    local resource="$1" name="$2"
    kubectl -n "$namespace" wait --for=condition=Ready --timeout="$ready_timeout" "$resource/$name"
}

wait_config_value() {
    local name="$1" expected="$2" timeout_seconds="${3:-120}" start value
    start="$SECONDS"
    while ((SECONDS - start < timeout_seconds)); do
        value="$(kubectl -n "$namespace" get configmap "$name" -o jsonpath='{.data.message}' 2>/dev/null || true)"
        [[ "$value" == "$expected" ]] && return 0
        sleep 2
    done
    echo "flux-integration: configmap $name did not converge to $expected" >&2
    return 1
}

assert_config_value_for() {
    local name="$1" expected="$2" seconds="$3" start value
    start="$SECONDS"
    while ((SECONDS - start < seconds)); do
        value="$(kubectl -n "$namespace" get configmap "$name" -o jsonpath='{.data.message}' 2>/dev/null || true)"
        if [[ "$value" != "$expected" ]]; then
            echo "flux-integration: configmap $name changed during suspended observation: $value" >&2
            return 1
        fi
        sleep 2
    done
}

current_phase="preflight"
"$capacity_check" --path "$repo_root" --json | tee "$evidence_dir/capacity.json"
validate_static
if k3d cluster list --no-headers 2>/dev/null | awk '{print $1}' | grep -Fxq "$cluster_name"; then
    fail "refusing to reuse or modify existing k3d cluster $cluster_name"
fi
record_pass

current_phase="fixtures"
mkdir -p "$fixture_root/git" "$fixture_root/helm" "$work_dir/git-worktree/kustomize" "$work_dir/image"
cp -R "$fixture_root_source/kustomize/." "$work_dir/git-worktree/kustomize/"
git -C "$work_dir/git-worktree" init --initial-branch=main >/dev/null
git -C "$work_dir/git-worktree" config user.name "Astronomer Flux Integration"
git -C "$work_dir/git-worktree" config user.email "flux-integration@example.invalid"
git -C "$work_dir/git-worktree" add kustomize
git -C "$work_dir/git-worktree" commit -m "synthetic Flux integration fixture" >/dev/null
git clone --bare "$work_dir/git-worktree" "$fixture_root/git/repository.git" >/dev/null
git -C "$fixture_root/git/repository.git" fsck --strict >/dev/null
helm package "$fixture_root_source/chart" --destination "$fixture_root/helm" >/dev/null
helm repo index "$fixture_root/helm" --url "http://host.k3d.internal:1/helm"
CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o "$fixture_binary" "$fixture_root_source/server"
chmod 0555 "$fixture_binary"
docker build --quiet --tag "$fixture_image" --file "$fixture_root_source/server/Dockerfile" "$work_dir/image" >/dev/null
created_image=1
"$fixture_binary" --root "$fixture_root" --listen 0.0.0.0:0 --port-file "$fixture_port_file" \
    >"$evidence_dir/fixture-server.log" 2>&1 &
fixture_pid="$!"
for _ in $(seq 1 50); do
    [[ -s "$fixture_port_file" ]] && break
    kill -0 "$fixture_pid" 2>/dev/null || fail "local fixture server exited during startup"
    sleep 0.1
done
[[ -s "$fixture_port_file" ]] || fail "local fixture server did not publish its port"
fixture_port="$(tr -d '\n' <"$fixture_port_file")"
[[ "$fixture_port" =~ ^[0-9]+$ ]] && ((fixture_port >= 1024 && fixture_port <= 65535)) || fail "fixture server returned an invalid port"
curl --fail --silent --show-error "http://127.0.0.1:${fixture_port}/healthz" >/dev/null
git ls-remote "http://127.0.0.1:${fixture_port}/git/repository.git" refs/heads/main >/dev/null
helm repo index "$fixture_root/helm" --url "http://host.k3d.internal:${fixture_port}/helm"
record_pass

current_phase="cluster-create"
k3d cluster create "$cluster_name" \
    --servers 1 --agents 0 --no-lb \
    --image "$k3s_image" \
    --k3s-arg '--disable=traefik@server:0' \
    --kubeconfig-update-default=false \
    --kubeconfig-switch-context=false \
    --wait --timeout 4m >/dev/null
created_cluster=1
k3d kubeconfig get "$cluster_name" >"$kubeconfig_file"
chmod 0600 "$kubeconfig_file"
export KUBECONFIG="$kubeconfig_file"
k3d image import --cluster "$cluster_name" "$fixture_image" >/dev/null
kubectl wait --for=condition=Ready node --all --timeout="$ready_timeout"
record_pass

current_phase="distribution-ready"
kubectl apply -f "$distribution" >/dev/null
kubectl -n astronomer-delivery-system rollout status deployment/source-controller --timeout="$ready_timeout"
kubectl -n astronomer-delivery-system rollout status deployment/kustomize-controller --timeout="$ready_timeout"
kubectl -n astronomer-delivery-system rollout status deployment/helm-controller --timeout="$ready_timeout"
[[ "$(kubectl -n astronomer-delivery-system get deployments -l app.kubernetes.io/part-of=flux -o name | wc -l)" -eq 3 ]] ||
    fail "distribution did not install exactly three Flux controllers"
record_pass

current_phase="api-discovery"
for resource in \
    gitrepositories.source.toolkit.fluxcd.io \
    helmrepositories.source.toolkit.fluxcd.io \
    helmcharts.source.toolkit.fluxcd.io \
    kustomizations.kustomize.toolkit.fluxcd.io \
    helmreleases.helm.toolkit.fluxcd.io; do
    kubectl api-resources --api-group="${resource#*.}" -o name | grep -Fxq "$resource" ||
        fail "Flux API discovery is missing $resource"
done
record_pass

current_phase="sources-and-renderers-ready"
sed \
    -e "s|__TEST_NAMESPACE__|$namespace|g" \
    -e "s|__FIXTURE_PORT__|$fixture_port|g" \
    -e "s|__IMAGE_TAG__|$image_tag|g" \
    "$fixture_root_source/integration-resources.yaml.tmpl" >"$rendered_resources"
kubectl apply -f "$rendered_resources" >/dev/null
wait_ready gitrepository integration-git
wait_ready helmrepository integration-helm
wait_ready helmcharts.source.toolkit.fluxcd.io integration-helm
wait_ready kustomization integration-kustomization
wait_ready helmrelease integration-helm
wait_config_value flux-kustomize-managed desired
wait_config_value flux-helm-managed desired
record_pass

current_phase="drift-remediation"
kubectl -n "$namespace" patch configmap flux-kustomize-managed --type=merge -p '{"data":{"message":"drifted"}}' >/dev/null
kubectl -n "$namespace" patch configmap flux-helm-managed --type=merge -p '{"data":{"message":"drifted"}}' >/dev/null
wait_config_value flux-kustomize-managed desired 120
wait_config_value flux-helm-managed desired 120
record_pass

current_phase="suspend-resume"
kubectl -n "$namespace" patch kustomization integration-kustomization --type=merge -p '{"spec":{"suspend":true}}' >/dev/null
kubectl -n "$namespace" patch helmrelease integration-helm --type=merge -p '{"spec":{"suspend":true}}' >/dev/null
sleep 3
kubectl -n "$namespace" patch configmap flux-kustomize-managed --type=merge -p '{"data":{"message":"suspended"}}' >/dev/null
kubectl -n "$namespace" patch configmap flux-helm-managed --type=merge -p '{"data":{"message":"suspended"}}' >/dev/null
assert_config_value_for flux-kustomize-managed suspended "$suspend_seconds"
assert_config_value_for flux-helm-managed suspended "$suspend_seconds"
kubectl -n "$namespace" patch kustomization integration-kustomization --type=merge -p '{"spec":{"suspend":false}}' >/dev/null
kubectl -n "$namespace" patch helmrelease integration-helm --type=merge -p '{"spec":{"suspend":false}}' >/dev/null
wait_config_value flux-kustomize-managed desired 120
wait_config_value flux-helm-managed desired 120
wait_ready kustomization integration-kustomization
wait_ready helmrelease integration-helm
record_pass

current_phase="controller-restart"
restart_nonce="$(date -u +%s)"
for controller in source-controller kustomize-controller helm-controller; do
    kubectl -n astronomer-delivery-system rollout restart "deployment/$controller" >/dev/null
done
for controller in source-controller kustomize-controller helm-controller; do
    kubectl -n astronomer-delivery-system rollout status "deployment/$controller" --timeout="$ready_timeout"
done
kubectl -n "$namespace" annotate --overwrite gitrepository/integration-git reconcile.fluxcd.io/requestedAt="$restart_nonce" >/dev/null
kubectl -n "$namespace" annotate --overwrite helmrepository/integration-helm reconcile.fluxcd.io/requestedAt="$restart_nonce" >/dev/null
kubectl -n "$namespace" annotate --overwrite kustomization/integration-kustomization reconcile.fluxcd.io/requestedAt="$restart_nonce" >/dev/null
kubectl -n "$namespace" annotate --overwrite helmrelease/integration-helm reconcile.fluxcd.io/requestedAt="$restart_nonce" >/dev/null
wait_ready gitrepository integration-git
wait_ready helmrepository integration-helm
wait_ready kustomization integration-kustomization
wait_ready helmrelease integration-helm
wait_config_value flux-kustomize-managed desired
wait_config_value flux-helm-managed desired
record_pass

current_phase="delete-and-prune"
kubectl -n "$namespace" delete helmrelease integration-helm --wait=true --timeout="$ready_timeout" >/dev/null
kubectl -n "$namespace" delete kustomization integration-kustomization --wait=true --timeout="$ready_timeout" >/dev/null
if kubectl -n "$namespace" get configmap flux-helm-managed >/dev/null 2>&1; then
    fail "HelmRelease deletion did not uninstall the managed ConfigMap"
fi
if kubectl -n "$namespace" get configmap flux-kustomize-managed >/dev/null 2>&1; then
    fail "Kustomization deletion did not prune the managed ConfigMap"
fi
kubectl -n "$namespace" delete \
    gitrepository/integration-git helmcharts.source.toolkit.fluxcd.io/integration-helm helmrepository/integration-helm \
    --wait=true --timeout="$ready_timeout" >/dev/null
record_pass

current_phase="complete"
capture_evidence
record_pass
echo "flux-integration: all behavioral checks passed"
