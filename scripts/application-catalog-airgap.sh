#!/bin/sh
# Export/import the curated application catalog as a static HTTPS mirror.
#
# Export resolves every pinned Helm chart into the bundle and derives a
# best-effort default-values image inventory. Import verifies the bundle,
# rewrites its placeholder URLs for the operator's internal HTTPS host, and
# prints the exact Astronomer Helm values required to consume it.
set -eu

usage() {
  echo "usage:" >&2
  echo "  $0 export <catalog.json> <output.tar.gz>" >&2
  echo "  $0 import <bundle.tar.gz> <document-root> <https-base-url>" >&2
  exit 2
}

require() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "application-catalog-airgap: missing required command: $1" >&2
    exit 1
  }
}

safe_tmp() {
  mktemp -d "${TMPDIR:-/tmp}/astronomer-catalog-airgap.XXXXXX"
}

export_bundle() {
  catalog="$1"
  output="$2"
  test -f "$catalog" || { echo "catalog not found: $catalog" >&2; exit 1; }
  require helm
  require jq
  require sha256sum
  require tar

  work="$(safe_tmp)"
  trap 'rm -rf -- "$work"' EXIT HUP INT TERM
  root="$work/application-catalog"
  mkdir -p "$root/repositories"

  jq -e '.apiVersion == "catalog.astronomer.dev/v1alpha1" and .kind == "ApplicationCatalog"' "$catalog" >/dev/null

  jq -r '.repositories[] | [.name,.url] | @tsv' "$catalog" |
    while IFS="$(printf '\t')" read -r name url; do
      helm repo add "airgap-${name}" "$url" --force-update >/dev/null
      mkdir -p "$root/repositories/$name"
    done
  helm repo update >/dev/null

  jq -r '.applications[] | [.artifact.repository,.artifact.chart,.artifact.version] | @tsv' "$catalog" |
    while IFS="$(printf '\t')" read -r repository chart version; do
      destination="$root/repositories/$repository"
      helm pull "airgap-${repository}/${chart}" --version "$version" --destination "$destination"
    done

  for directory in "$root"/repositories/*; do
    name="$(basename "$directory")"
    helm repo index "$directory" --url "__ASTRONOMER_CATALOG_BASE_URL__/${name}"
  done

  jq '
    .repositories |= map(.url = "__ASTRONOMER_CATALOG_BASE_URL__/" + .name)
    | .metadata.description = ((.metadata.description // "") + " Offline static mirror.")
  ' "$catalog" >"$root/catalog.json"

  : >"$root/images.txt"
  for chart_archive in "$root"/repositories/*/*.tgz; do
    if helm template airgap "$chart_archive" 2>/dev/null |
      sed -n 's/^[[:space:]-]*image:[[:space:]]*//p' |
      sed "s/[\"']//g; s/[[:space:]]*$//" >>"$root/images.txt"; then
      :
    else
      echo "warning: could not render image inventory for $chart_archive" >&2
    fi
  done
  sort -u -o "$root/images.txt" "$root/images.txt"

  (
    cd "$root"
    checksum_tmp="$(mktemp "${TMPDIR:-/tmp}/astronomer-catalog-checksums.XXXXXX")"
    find . -type f ! -name SHA256SUMS -print | LC_ALL=C sort | xargs sha256sum >"$checksum_tmp"
    mv "$checksum_tmp" SHA256SUMS
  )
  tar -C "$work" -czf "$output" application-catalog
  echo "exported application catalog bundle: $output"
  echo "images requiring an internal mirror: $(wc -l < "$root/images.txt" | tr -d ' ')"
}

import_bundle() {
  bundle="$1"
  destination="$2"
  base_url="${3%/}"
  case "$base_url" in https://*) ;; *) echo "base URL must use HTTPS" >&2; exit 1;; esac
  require jq
  require sha256sum
  require tar

  work="$(safe_tmp)"
  trap 'rm -rf -- "$work"' EXIT HUP INT TERM
  tar -C "$work" -xzf "$bundle"
  root="$work/application-catalog"
  test -f "$root/SHA256SUMS" || { echo "bundle has no SHA256SUMS" >&2; exit 1; }
  (cd "$root" && sha256sum -c SHA256SUMS)

  find "$root" -type f \( -name catalog.json -o -name index.yaml \) -exec \
    sed -i "s|__ASTRONOMER_CATALOG_BASE_URL__|$base_url|g" {} +
  catalog_digest="sha256:$(sha256sum "$root/catalog.json" | awk '{print $1}')"
  mkdir -p "$destination"
  cp -a "$root/." "$destination/"
  rm -f "$destination/SHA256SUMS"
  (
    cd "$destination"
    checksum_tmp="$(mktemp "${TMPDIR:-/tmp}/astronomer-catalog-checksums.XXXXXX")"
    find . -type f ! -name SHA256SUMS -print | LC_ALL=C sort | xargs sha256sum >"$checksum_tmp"
    mv "$checksum_tmp" SHA256SUMS
  )

  echo "imported static catalog mirror into: $destination"
  echo "serve that directory at: $base_url"
  echo "Astronomer Helm values:"
  echo "catalog:"
  echo "  enabled: true"
  echo "  sourceURL: ${base_url}/catalog.json"
  echo "  digest: ${catalog_digest}"
  echo "  allowPrivateMirrors: true"
  echo "Mirror every reference in ${destination}/images.txt and configure your registry rewrite policy before installing applications."
}

test "$#" -ge 1 || usage
case "$1" in
  export) test "$#" -eq 3 || usage; export_bundle "$2" "$3" ;;
  import) test "$#" -eq 4 || usage; import_bundle "$2" "$3" "$4" ;;
  *) usage ;;
esac
