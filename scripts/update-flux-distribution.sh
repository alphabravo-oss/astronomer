#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: scripts/update-flux-distribution.sh [--check] [vX.Y.Z]

Generate Astronomer's pinned Flux distribution from an authenticated upstream
release. With --check, regenerate in a temporary directory and fail if any
committed generated file differs.
EOF
}

die() {
  printf 'update-flux-distribution: %s\n' "$*" >&2
  exit 1
}

require_command() {
  command -v "$1" >/dev/null 2>&1 || die "required command not found: $1"
}

check_mode=false
requested_version=""
while (($# > 0)); do
  case "$1" in
    --check)
      check_mode=true
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    v[0-9]*.[0-9]*.[0-9]*)
      [[ -z "$requested_version" ]] || die "only one version may be specified"
      requested_version="$1"
      ;;
    *)
      usage >&2
      die "unknown argument: $1"
      ;;
  esac
  shift
done

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
repo_root=$(cd -- "$script_dir/.." && pwd)
distribution_dir="$repo_root/deploy/flux"

[[ -f "$distribution_dir/VERSION" ]] || die "missing deploy/flux/VERSION"
current_version=$(tr -d '[:space:]' < "$distribution_dir/VERSION")
version=${requested_version:-$current_version}
[[ "$version" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || die "version must be an exact stable tag such as v2.9.3"
if $check_mode && [[ "$version" != "$current_version" ]]; then
  die "--check version $version does not match committed version $current_version"
fi
version_number=${version#v}

for command_name in base64 cosign curl jq kubectl sha256sum tar; do
  require_command "$command_name"
done

for static_file in README.md system-resources.yaml trust-policy.json; do
  [[ -f "$distribution_dir/$static_file" ]] || die "missing deploy/flux/$static_file"
done
[[ -d "$distribution_dir/patches" ]] || die "missing deploy/flux/patches"

tmp_dir=$(mktemp -d "${TMPDIR:-/tmp}/astronomer-flux.XXXXXXXX")
trap 'rm -rf "$tmp_dir"' EXIT
stage_dir="$tmp_dir/distribution"
downloads_dir="$tmp_dir/downloads"
mkdir -p "$stage_dir" "$downloads_dir"
cp -R "$distribution_dir/patches" "$stage_dir/patches"
cp "$distribution_dir/README.md" "$distribution_dir/system-resources.yaml" \
  "$distribution_dir/trust-policy.json" "$stage_dir/"
printf '%s\n' "$version" > "$stage_dir/VERSION"

case "$(uname -s)" in
  Linux) cli_os=linux ;;
  Darwin) cli_os=darwin ;;
  *) die "unsupported operating system: $(uname -s)" ;;
esac
case "$(uname -m)" in
  x86_64|amd64) cli_arch=amd64 ;;
  arm64|aarch64) cli_arch=arm64 ;;
  armv7l|armv7) cli_arch=arm ;;
  *) die "unsupported architecture: $(uname -m)" ;;
esac

release_api_url="https://api.github.com/repos/fluxcd/flux2/releases/tags/$version"
release_base_url="https://github.com/fluxcd/flux2/releases/download/$version"
curl --fail --silent --show-error --location --retry 3 \
  -o "$downloads_dir/release.json" "$release_api_url"
jq -e --arg version "$version" \
  '.tag_name == $version and .draft == false and .prerelease == false' \
  "$downloads_dir/release.json" >/dev/null || die "GitHub release metadata is not an exact stable $version release"

checksums_asset="flux_${version_number}_checksums.txt"
certificate_asset="${checksums_asset}.pem"
signature_asset="${checksums_asset}.sig"
cli_asset="flux_${version_number}_${cli_os}_${cli_arch}.tar.gz"
source_asset="flux_${version_number}_source_code.tar.gz"
sbom_asset="flux_${version_number}_sbom.spdx.json"
provenance_asset="provenance.intoto.jsonl"

download_asset() {
  local asset=$1
  curl --fail --silent --show-error --location --retry 3 \
    -o "$downloads_dir/$asset" "$release_base_url/$asset"
}

verify_github_asset_digest() {
  local asset=$1
  local declared actual
  declared=$(jq -er --arg asset "$asset" '.assets[] | select(.name == $asset) | .digest' \
    "$downloads_dir/release.json") || die "release metadata has no digest for $asset"
  actual="sha256:$(sha256sum "$downloads_dir/$asset" | awk '{print $1}')"
  [[ "$actual" == "$declared" ]] || die "$asset digest $actual does not match GitHub release digest $declared"
}

for asset in "$checksums_asset" "$certificate_asset" "$signature_asset" \
  "$cli_asset" "$source_asset" "$sbom_asset" "$provenance_asset"; do
  download_asset "$asset"
  verify_github_asset_digest "$asset"
done

release_issuer=$(jq -er '.release.certificate_oidc_issuer' "$distribution_dir/trust-policy.json")
release_identity_template=$(jq -er '.release.certificate_identity_template' "$distribution_dir/trust-policy.json")
release_identity=${release_identity_template//\{version\}/$version}
cosign verify-blob "$downloads_dir/$checksums_asset" \
  --certificate "$downloads_dir/$certificate_asset" \
  --signature "$downloads_dir/$signature_asset" \
  --certificate-identity "$release_identity" \
  --certificate-oidc-issuer "$release_issuer" >/dev/null

signed_asset_sha() {
  local asset=$1
  awk -v asset="$asset" '$2 == asset {print $1}' "$downloads_dir/$checksums_asset"
}

verify_signed_asset() {
  local asset=$1
  local expected actual
  expected=$(signed_asset_sha "$asset")
  [[ "$expected" =~ ^[0-9a-f]{64}$ ]] || die "$asset is absent from the signed checksum list"
  actual=$(sha256sum "$downloads_dir/$asset" | awk '{print $1}')
  [[ "$actual" == "$expected" ]] || die "$asset digest does not match the signed checksum list"
}

verify_signed_asset "$cli_asset"
verify_signed_asset "$source_asset"
verify_signed_asset "$sbom_asset"
jq -e '.spdxVersion | startswith("SPDX-")' "$downloads_dir/$sbom_asset" >/dev/null \
  || die "upstream SBOM is not valid SPDX JSON"

if base64 --help 2>&1 | grep -q -- '--decode'; then
  jq -er '.dsseEnvelope.payload' "$downloads_dir/$provenance_asset" | base64 --decode \
    > "$downloads_dir/provenance-payload.json"
else
  jq -er '.dsseEnvelope.payload' "$downloads_dir/$provenance_asset" | base64 -D \
    > "$downloads_dir/provenance-payload.json"
fi
jq -e --arg tag "$version" --arg cli "$cli_asset" --arg sha "$(signed_asset_sha "$cli_asset")" '
  .predicateType == "https://slsa.dev/provenance/v0.2" and
  .predicate.invocation.configSource.uri == ("git+https://github.com/fluxcd/flux2@refs/tags/" + $tag) and
  any(.subject[]; .name == $cli and .digest.sha256 == $sha)
' "$downloads_dir/provenance-payload.json" >/dev/null \
  || die "upstream provenance does not bind the CLI to $version and its signed digest"
source_commit=$(jq -er '.predicate.invocation.configSource.digest.sha1' \
  "$downloads_dir/provenance-payload.json")
[[ "$source_commit" =~ ^[0-9a-f]{40}$ ]] || die "invalid source commit in upstream provenance"

tar -xzf "$downloads_dir/$cli_asset" -C "$downloads_dir" flux
[[ "$("$downloads_dir/flux" version --client | awk '$1 == "flux:" {print $2}')" == "$version" ]] \
  || die "downloaded Flux CLI does not report $version"
"$downloads_dir/flux" install \
  --version="$version" \
  --namespace=astronomer-delivery-system \
  --components=source-controller,kustomize-controller,helm-controller \
  --network-policy=true \
  --export > "$stage_dir/upstream-install.yaml"

extract_component_image() {
  local component=$1
  local images
  images=$(awk -v component="$component" \
    '$1 == "image:" && $2 ~ ("ghcr.io/fluxcd/" component ":[^@]+$") {print $2}' \
    "$stage_dir/upstream-install.yaml" | sort -u)
  [[ $(printf '%s\n' "$images" | sed '/^$/d' | wc -l) -eq 1 ]] \
    || die "expected one tagged image for $component in upstream manifest"
  printf '%s\n' "$images"
}

controller_issuer=$(jq -er '.controller_images.certificate_oidc_issuer' \
  "$distribution_dir/trust-policy.json")
controller_identity=$(jq -er '.controller_images.certificate_identity' \
  "$distribution_dir/trust-policy.json")

resolve_verified_image() {
  local image=$1
  local component=$2
  local repository tag token manifest_digest registry_digest body_digest platform_count
  local signature_json="$downloads_dir/${component}.signature.json"
  local manifest_body="$downloads_dir/${component}.manifest.json"
  local manifest_headers="$downloads_dir/${component}.manifest.headers"

  cosign verify "$image" \
    --certificate-identity "$controller_identity" \
    --certificate-oidc-issuer "$controller_issuer" \
    --output=json > "$signature_json"
  manifest_digest=$(jq -er '[.[].critical.image["docker-manifest-digest"]] | unique | if length == 1 then .[0] else error("ambiguous signed digests") end' \
    "$signature_json")
  [[ "$manifest_digest" =~ ^sha256:[0-9a-f]{64}$ ]] || die "invalid signed digest for $image"

  repository=${image#ghcr.io/}
  tag=${repository##*:}
  repository=${repository%:*}
  token=$(curl --fail --silent --show-error --location --retry 3 \
    --get --data-urlencode "scope=repository:${repository}:pull" \
    'https://ghcr.io/token' | jq -er '.token')
  curl --fail --silent --show-error --location --retry 3 \
    -D "$manifest_headers" -o "$manifest_body" \
    -H "Authorization: Bearer $token" \
    -H 'Accept: application/vnd.oci.image.index.v1+json, application/vnd.docker.distribution.manifest.list.v2+json' \
    "https://ghcr.io/v2/${repository}/manifests/${tag}"
  registry_digest=$(awk 'BEGIN {IGNORECASE=1} $1 == "docker-content-digest:" {gsub("\\r", "", $2); value=$2} END {print value}' \
    "$manifest_headers")
  body_digest="sha256:$(sha256sum "$manifest_body" | awk '{print $1}')"
  [[ "$registry_digest" == "$manifest_digest" && "$body_digest" == "$manifest_digest" ]] \
    || die "registry, body, and signed digests differ for $image"

  for platform in 'linux/amd64/' 'linux/arm/v7' 'linux/arm64/'; do
    platform_count=$(jq -r '.manifests[].platform | [.os, .architecture, (.variant // "")] | join("/")' \
      "$manifest_body" | grep -Fxc "$platform" || true)
    [[ "$platform_count" -eq 1 ]] || die "$image does not contain exactly one $platform platform manifest"
  done
  printf '%s\n' "$manifest_digest"
}

source_image=$(extract_component_image source-controller)
kustomize_image=$(extract_component_image kustomize-controller)
helm_image=$(extract_component_image helm-controller)
source_digest=$(resolve_verified_image "$source_image" source-controller)
kustomize_digest=$(resolve_verified_image "$kustomize_image" kustomize-controller)
helm_digest=$(resolve_verified_image "$helm_image" helm-controller)

cat > "$stage_dir/kustomization.yaml" <<EOF
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
resources:
  - upstream-install.yaml
  - system-resources.yaml
patches:
  - path: patches/namespace.yaml
    target:
      group: ""
      version: v1
      kind: Namespace
      name: astronomer-delivery-system
  - path: patches/source-controller.yaml
    target:
      group: apps
      version: v1
      kind: Deployment
      name: source-controller
  - path: patches/kustomize-controller.yaml
    target:
      group: apps
      version: v1
      kind: Deployment
      name: kustomize-controller
  - path: patches/helm-controller.yaml
    target:
      group: apps
      version: v1
      kind: Deployment
      name: helm-controller
  - path: patches/crd-controller-role.yaml
    target:
      group: rbac.authorization.k8s.io
      version: v1
      kind: ClusterRole
      name: crd-controller-astronomer-delivery-system
  - path: patches/crd-controller-binding.yaml
    target:
      group: rbac.authorization.k8s.io
      version: v1
      kind: ClusterRoleBinding
      name: crd-controller-astronomer-delivery-system
  - path: patches/delete-allow-egress.yaml
  - path: patches/delete-allow-scraping.yaml
  - path: patches/delete-allow-webhooks.yaml
  - path: patches/delete-flux-edit.yaml
  - path: patches/delete-flux-view.yaml
  - path: patches/delete-critical-pods-quota.yaml
images:
  - name: ghcr.io/fluxcd/source-controller
    digest: $source_digest
  - name: ghcr.io/fluxcd/kustomize-controller
    digest: $kustomize_digest
  - name: ghcr.io/fluxcd/helm-controller
    digest: $helm_digest
EOF

kubectl kustomize "$stage_dir" > "$stage_dir/install.yaml"
tar -xOf "$downloads_dir/$source_asset" LICENSE > "$stage_dir/LICENSE.flux2"

release_published_at=$(jq -er '.published_at' "$downloads_dir/release.json")
minimum_kubernetes=$(jq -er '.qualification.minimum_kubernetes_version' \
  "$distribution_dir/trust-policy.json")
documentation_reviewed_at=$(jq -er '.qualification.documentation_reviewed_at' \
  "$distribution_dir/trust-policy.json")
installation_documentation=$(jq -er '.qualification.installation_documentation' \
  "$distribution_dir/trust-policy.json")
checksums_sha=$(sha256sum "$downloads_dir/$checksums_asset" | awk '{print $1}')
provenance_sha=$(sha256sum "$downloads_dir/$provenance_asset" | awk '{print $1}')

jq -n -S \
  --arg version "$version" \
  --arg release_url "https://github.com/fluxcd/flux2/releases/tag/$version" \
  --arg published_at "$release_published_at" \
  --arg source_commit "$source_commit" \
  --arg checksums_url "$release_base_url/$checksums_asset" \
  --arg checksums_sha "$checksums_sha" \
  --arg release_identity "$release_identity" \
  --arg release_issuer "$release_issuer" \
  --arg cli_url "$release_base_url/$cli_asset" \
  --arg cli_sha "$(signed_asset_sha "$cli_asset")" \
  --arg source_url "$release_base_url/$source_asset" \
  --arg source_sha "$(signed_asset_sha "$source_asset")" \
  --arg sbom_url "$release_base_url/$sbom_asset" \
  --arg sbom_sha "$(signed_asset_sha "$sbom_asset")" \
  --arg provenance_url "$release_base_url/$provenance_asset" \
  --arg provenance_sha "$provenance_sha" \
  --arg controller_identity "$controller_identity" \
  --arg controller_issuer "$controller_issuer" \
  --arg source_image "$source_image" --arg source_digest "$source_digest" \
  --arg kustomize_image "$kustomize_image" --arg kustomize_digest "$kustomize_digest" \
  --arg helm_image "$helm_image" --arg helm_digest "$helm_digest" \
  --arg minimum_kubernetes "$minimum_kubernetes" \
  --arg documentation_reviewed_at "$documentation_reviewed_at" \
  --arg installation_documentation "$installation_documentation" '
  {
    schema_version: 1,
    flux_release: {
      version: $version,
      url: $release_url,
      published_at: $published_at,
      source_commit: $source_commit,
      signed_checksums: {
        url: $checksums_url,
        sha256: $checksums_sha,
        verified: true,
        certificate_identity: $release_identity,
        certificate_oidc_issuer: $release_issuer
      },
      assets: {
        cli: {url: $cli_url, sha256: $cli_sha},
        source: {url: $source_url, sha256: $source_sha},
        sbom: {format: "SPDX-JSON", url: $sbom_url, sha256: $sbom_sha},
        slsa_provenance: {url: $provenance_url, sha256: $provenance_sha}
      }
    },
    distribution: {
      namespace: "astronomer-delivery-system",
      components: ["source-controller", "kustomize-controller", "helm-controller"],
      generated_with: "flux install --network-policy=true --export",
      kubernetes: {
        minimum_supported: $minimum_kubernetes,
        documentation: $installation_documentation,
        documentation_reviewed_at: $documentation_reviewed_at
      }
    },
    controller_image_signature_policy: {
      certificate_identity: $controller_identity,
      certificate_oidc_issuer: $controller_issuer
    },
    controller_images: [
      {component: "source-controller", source_ref: $source_image, digest: $source_digest, platforms: ["linux/amd64", "linux/arm/v7", "linux/arm64"]},
      {component: "kustomize-controller", source_ref: $kustomize_image, digest: $kustomize_digest, platforms: ["linux/amd64", "linux/arm/v7", "linux/arm64"]},
      {component: "helm-controller", source_ref: $helm_image, digest: $helm_digest, platforms: ["linux/amd64", "linux/arm/v7", "linux/arm64"]}
    ]
  }
' > "$stage_dir/provenance.json"

(
  cd "$stage_dir"
  sha256sum \
    LICENSE.flux2 README.md VERSION install.yaml kustomization.yaml provenance.json \
    system-resources.yaml trust-policy.json upstream-install.yaml patches/*.yaml \
    | LC_ALL=C sort -k2
) > "$stage_dir/checksums.txt"

generated_files=(
  VERSION
  upstream-install.yaml
  kustomization.yaml
  install.yaml
  checksums.txt
  provenance.json
  LICENSE.flux2
)

if $check_mode; then
  stale=false
  for generated_file in "${generated_files[@]}"; do
    if [[ ! -f "$distribution_dir/$generated_file" ]]; then
      printf 'missing generated file: deploy/flux/%s\n' "$generated_file" >&2
      stale=true
      continue
    fi
    if ! diff -u "$distribution_dir/$generated_file" "$stage_dir/$generated_file"; then
      stale=true
    fi
  done
  $stale && die "committed Flux distribution is stale; run scripts/update-flux-distribution.sh $version"
  "$script_dir/verify-flux-version.sh"
  printf 'Flux distribution %s is deterministic and current.\n' "$version"
  exit 0
fi

for generated_file in "${generated_files[@]}"; do
  install -m 0644 "$stage_dir/$generated_file" "$distribution_dir/$generated_file"
done
"$script_dir/verify-flux-version.sh"
printf 'Updated deploy/flux to authenticated Flux release %s.\n' "$version"
