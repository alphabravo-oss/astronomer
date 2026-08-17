#!/usr/bin/env bash
set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
catalog="$root_dir/deploy/bundles/catalog.json"
output=""
check=""
publish=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --output) output="${2:?missing output path}"; shift 2 ;;
    --check) check="${2:?missing artifact path}"; shift 2 ;;
    --publish) publish="${2:?missing OCI reference}"; shift 2 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done

for tool in jq helm sha256sum tar gzip; do
  command -v "$tool" >/dev/null || { echo "$tool is required" >&2; exit 1; }
done
jq -e '.schema_version == 1 and (.components | length > 0) and all(.components[];
  .source.kind == "helm_http" and
  (.source.chart_digest | test("^sha256:[0-9a-f]{64}$")) and
  (.images | length > 0) and
  all(.images[]; test("^[a-z0-9][a-z0-9.:-]*/[a-z0-9][a-z0-9._/-]*@sha256:[0-9a-f]{64}$"))
)' "$catalog" >/dev/null

build_dir="$(mktemp -d)"
trap 'rm -rf "$build_dir"' EXIT
install -m 0644 "$catalog" "$build_dir/catalog.json"
install -m 0644 "$root_dir/deploy/bundles/README.md" "$build_dir/README.md"
mkdir -p "$build_dir/charts"

while IFS=$'\t' read -r repository chart version digest; do
  helm pull "$chart" --repo "$repository" --version "$version" --destination "$build_dir/charts" >/dev/null
  archive="$build_dir/charts/$chart-$version.tgz"
  actual="sha256:$(sha256sum "$archive" | awk '{print $1}')"
  [[ "$actual" == "$digest" ]] || { echo "$chart $version digest mismatch: $actual != $digest" >&2; exit 1; }
done < <(jq -r '.components[] | [.source.url,.source.chart,.source.version,.source.chart_digest] | @tsv' "$catalog")

artifact="$build_dir/builtin-bundles.tar.gz"
TZ=UTC tar --sort=name --mtime='@0' --owner=0 --group=0 --numeric-owner -C "$build_dir" \
  -cf - README.md catalog.json charts | gzip -n -9 > "$artifact"

if [[ -n "$check" ]]; then
  cmp -s "$artifact" "$check" || { echo "built-in bundle artifact is not reproducible or is stale: $check" >&2; exit 1; }
fi
if [[ -n "$output" ]]; then
  mkdir -p "$(dirname "$output")"
  install -m 0644 "$artifact" "$output"
  sha256sum "$output"
fi
if [[ -n "$publish" ]]; then
  command -v oras >/dev/null || { echo "oras is required for publish" >&2; exit 1; }
  command -v cosign >/dev/null || { echo "cosign is required for publish" >&2; exit 1; }
  ref="${publish#oci://}"
  oras push "$ref" --artifact-type application/vnd.astronomer.builtin-bundles.v1+tar+gzip \
    "$artifact:application/vnd.astronomer.builtin-bundles.v1+tar+gzip"
  cosign sign --yes "$ref"
fi

if [[ -z "$check$output$publish" ]]; then
  sha256sum "$artifact"
fi
