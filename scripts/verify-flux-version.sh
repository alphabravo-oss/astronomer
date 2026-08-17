#!/usr/bin/env bash
set -euo pipefail

die() {
  printf 'verify-flux-version: %s\n' "$*" >&2
  exit 1
}

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
repo_root=$(cd -- "$script_dir/.." && pwd)
distribution_dir="$repo_root/deploy/flux"
manifest="$distribution_dir/install.yaml"

for command_name in awk diff grep jq kubectl sha256sum sort; do
  command -v "$command_name" >/dev/null 2>&1 || die "required command not found: $command_name"
done
for required_file in VERSION checksums.txt install.yaml kustomization.yaml provenance.json upstream-install.yaml; do
  [[ -f "$distribution_dir/$required_file" ]] || die "missing deploy/flux/$required_file"
done

version=$(tr -d '[:space:]' < "$distribution_dir/VERSION")
[[ "$version" == "v2.9.3" ]] || die "this release line is qualified only for Flux v2.9.3, found $version"
version_label="app.kubernetes.io/version: $version"

(
  cd "$distribution_dir"
  sha256sum --check --strict checksums.txt
) >/dev/null

rendered="$distribution_dir/.install.verify.tmp"
trap 'rm -f "$rendered"' EXIT
kubectl kustomize "$distribution_dir" > "$rendered"
diff -u "$manifest" "$rendered" >/dev/null || die "install.yaml does not match the Kustomize overlay"

resource_names() {
  local kind=$1
  awk -v expected_kind="$kind" '
    BEGIN { RS="---\n" }
    {
      kind=""; name=""; in_metadata=0
      count=split($0, lines, "\n")
      for (i=1; i<=count; i++) {
        if (lines[i] == "kind: " expected_kind) kind=expected_kind
        if (lines[i] == "metadata:") { in_metadata=1; continue }
        if (in_metadata && lines[i] ~ /^  name: /) {
          name=lines[i]
          sub(/^  name: /, "", name)
          in_metadata=0
        }
      }
      if (kind == expected_kind && name != "") print name
    }
  ' "$manifest" | sort
}

assert_exact_names() {
  local kind=$1
  local expected=$2
  local actual
  actual=$(resource_names "$kind")
  [[ "$actual" == "$expected" ]] || {
    printf 'Unexpected %s set.\nExpected:\n%s\nActual:\n%s\n' "$kind" "$expected" "$actual" >&2
    exit 1
  }
}

assert_exact_names Deployment $'helm-controller\nkustomize-controller\nsource-controller'
assert_exact_names CustomResourceDefinition $'buckets.source.toolkit.fluxcd.io\nexternalartifacts.source.toolkit.fluxcd.io\ngitrepositories.source.toolkit.fluxcd.io\nhelmcharts.source.toolkit.fluxcd.io\nhelmreleases.helm.toolkit.fluxcd.io\nhelmrepositories.source.toolkit.fluxcd.io\nkustomizations.kustomize.toolkit.fluxcd.io\nocirepositories.source.toolkit.fluxcd.io'
assert_exact_names PodDisruptionBudget $'helm-controller\nkustomize-controller\nsource-controller'
assert_exact_names NetworkPolicy $'allow-dns\nallow-kubernetes-api\nallow-metrics\nallow-source-artifact-client\nallow-source-artifacts\nallow-source-egress\ndefault-deny'
assert_exact_names PriorityClass 'astronomer-delivery-critical'
assert_exact_names ResourceQuota ''
assert_exact_names ClusterRole $'astronomer-delivery-platform-applier\nastronomer-delivery-system-applier\ncrd-controller-astronomer-delivery-system'
assert_exact_names ClusterRoleBinding $'astronomer-delivery-system-applier\ncluster-reconciler-astronomer-delivery-system\ncrd-controller-astronomer-delivery-system'

stored_versions=$(awk '
  BEGIN { RS="---\n" }
  /kind: CustomResourceDefinition/ {
    crd=""; version=""; count=split($0, lines, "\n")
    for (i=1; i<=count; i++) {
      if (lines[i] ~ /^  name: .*\.(source|kustomize|helm)\.toolkit\.fluxcd\.io$/ && crd == "") {
        crd=lines[i]; sub(/^  name: /, "", crd)
      }
      if (lines[i] ~ /^    name: v[0-9]/) {
        version=lines[i]; sub(/^    name: /, "", version)
      }
      if (lines[i] ~ /^    storage: true$/) print crd " " version
    }
  }
' "$manifest" | sort)
expected_stored_versions=$'buckets.source.toolkit.fluxcd.io v1\nexternalartifacts.source.toolkit.fluxcd.io v1\ngitrepositories.source.toolkit.fluxcd.io v1\nhelmcharts.source.toolkit.fluxcd.io v1\nhelmreleases.helm.toolkit.fluxcd.io v2\nhelmrepositories.source.toolkit.fluxcd.io v1\nkustomizations.kustomize.toolkit.fluxcd.io v1\nocirepositories.source.toolkit.fluxcd.io v1'
[[ "$stored_versions" == "$expected_stored_versions" ]] || die "unexpected Flux CRD storage API versions"

mapfile -t images < <(awk '$1 == "image:" {print $2}' "$manifest")
[[ ${#images[@]} -eq 3 ]] || die "expected exactly three container images, found ${#images[@]}"
for image in "${images[@]}"; do
  [[ "$image" =~ ^ghcr\.io/fluxcd/(source|kustomize|helm)-controller@sha256:[0-9a-f]{64}$ ]] \
    || die "image is not an allowlisted digest-pinned controller: $image"
done

jq -e --arg version "$version" '
  .schema_version == 1 and
  .flux_release.version == $version and
  .flux_release.signed_checksums.verified == true and
  .distribution.namespace == "astronomer-delivery-system" and
  .distribution.components == ["source-controller", "kustomize-controller", "helm-controller"] and
  .distribution.kubernetes.minimum_supported == "v1.33.0" and
  (.controller_images | length) == 3 and
  all(.controller_images[];
    (.digest | test("^sha256:[0-9a-f]{64}$")) and
    .platforms == ["linux/amd64", "linux/arm/v7", "linux/arm64"])
' "$distribution_dir/provenance.json" >/dev/null || die "invalid provenance contract"

while IFS=$'\t' read -r component source_ref digest; do
  pinned_ref="${source_ref%%:*}@${digest}"
  printf '%s\n' "${images[@]}" | grep -Fqx "$pinned_ref" \
    || die "provenance image $component does not match install.yaml"
done < <(jq -r '.controller_images[] | [.component, .source_ref, .digest] | @tsv' \
  "$distribution_dir/provenance.json")

deployment_record() {
  local name=$1
  awk -v expected_name="$name" '
    BEGIN { RS="---\n" }
    $0 ~ /kind: Deployment/ && $0 ~ ("\n  name: " expected_name "\n") { print }
  ' "$manifest"
}

assert_deployment_contains() {
  local deployment=$1
  local value=$2
  deployment_record "$deployment" | grep -Fq -- "$value" \
    || die "$deployment is missing hardening value: $value"
}

for deployment in source-controller kustomize-controller helm-controller; do
  assert_deployment_contains "$deployment" "$version_label"
  assert_deployment_contains "$deployment" 'allowPrivilegeEscalation: false'
  assert_deployment_contains "$deployment" 'readOnlyRootFilesystem: true'
  assert_deployment_contains "$deployment" 'runAsNonRoot: true'
  assert_deployment_contains "$deployment" 'type: RuntimeDefault'
  assert_deployment_contains "$deployment" 'priorityClassName: astronomer-delivery-critical'
  assert_deployment_contains "$deployment" 'topologySpreadConstraints:'
  assert_deployment_contains "$deployment" 'resources:'
  assert_deployment_contains "$deployment" 'limits:'
  assert_deployment_contains "$deployment" 'requests:'
  assert_deployment_contains "$deployment" '--feature-gates=ObjectLevelWorkloadIdentity=true'
  assert_deployment_contains "$deployment" '--watch-label-selector=app.kubernetes.io/managed-by=astronomer-agent'
done

assert_deployment_contains source-controller '--default-service-account=astronomer-noop'
assert_deployment_contains kustomize-controller '--no-cross-namespace-refs=true'
assert_deployment_contains kustomize-controller '--no-remote-bases=true'
assert_deployment_contains kustomize-controller '--default-service-account=astronomer-noop'
assert_deployment_contains kustomize-controller '--default-decryption-service-account=astronomer-noop'
assert_deployment_contains kustomize-controller '--default-kubeconfig-service-account=astronomer-noop'
assert_deployment_contains helm-controller '--no-cross-namespace-refs=true'
assert_deployment_contains helm-controller '--default-service-account=astronomer-noop'
assert_deployment_contains helm-controller '--default-kubeconfig-service-account=astronomer-noop'

grep -Fq 'pod-security.kubernetes.io/enforce: restricted' "$manifest" \
  || die "delivery namespace does not enforce the restricted Pod Security Standard"
for mode in enforce audit warn; do
  grep -Fq "pod-security.kubernetes.io/${mode}-version: v1.33" "$manifest" \
    || die "delivery namespace does not pin the $mode Pod Security Standard to v1.33"
done
grep -Fq 'automountServiceAccountToken: false' "$manifest" \
  || die "astronomer-noop must not automount a service account token"
if grep -Eq 'image: .*:(latest|main|master)(@|$)' "$manifest"; then
  die "floating image tag found"
fi
if grep -Eq 'name: (allow-egress|allow-scraping|allow-webhooks)$' "$manifest"; then
  die "permissive upstream network policy survived the hardening overlay"
fi
if grep -Eq 'notification-controller|image-reflector-controller|image-automation-controller|source-watcher|notification\.toolkit|image\.toolkit|source\.extensions' "$manifest"; then
  die "rendered distribution contains an excluded Flux component or API"
fi
if grep -Eq 'rbac.authorization.k8s.io/aggregate-to-(admin|edit|view)' "$manifest"; then
  die "Flux API access must not aggregate into downstream user roles"
fi

compatibility_file="$repo_root/deploy/release/compatibility.yaml"
if [[ -f "$compatibility_file" ]]; then
  jq -e --arg version "$version" '
    .flux.distribution_version == $version and
    ([.flux.components[].name] | sort) == ["helm-controller", "kustomize-controller", "source-controller"] and
    all(.flux.components[]; .digest_pin_required == true) and
    (.kubernetes.advertised_minors | index("1.33")) != null
  ' "$compatibility_file" >/dev/null \
    || die "deploy/release/compatibility.yaml does not match the Flux component/support contract"
  while IFS=$'\t' read -r component repository tag declared_digest; do
    declared_ref="$repository:$tag"
    qualified_ref=$(jq -er --arg component "$component" \
      '.controller_images[] | select(.component == $component) | .source_ref' \
      "$distribution_dir/provenance.json")
    qualified_digest=$(jq -er --arg component "$component" \
      '.controller_images[] | select(.component == $component) | .digest' \
      "$distribution_dir/provenance.json")
    [[ "$declared_ref" == "$qualified_ref" ]] \
      || die "compatibility contract pins $component to $declared_ref; qualified release uses $qualified_ref"
    [[ "$declared_digest" == "$qualified_digest" ]] \
      || die "compatibility contract digest for $component differs from qualified provenance"
  done < <(jq -r '.flux.components[] | [.name, .repository, .tag, .digest] | @tsv' "$compatibility_file")
else
  printf 'verify-flux-version: compatibility contract not present yet; Wave 0 must add it before release\n' >&2
fi

printf 'Verified Flux %s: three digest-pinned controllers, eight stable storage APIs, hardening, and provenance.\n' "$version"
