#!/usr/bin/env bash
set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source_dir="$root_dir/deploy/flux"
output=""
check=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --output) output="${2:?missing output path}"; shift 2 ;;
    --check) check="${2:?missing artifact path}"; shift 2 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done

for tool in sha256sum tar gzip; do
  command -v "$tool" >/dev/null || { echo "$tool is required" >&2; exit 1; }
done

required=(
  VERSION
  LICENSE.flux2
  README.md
  checksums.txt
  install.yaml
  provenance.json
  trust-policy.json
)
for file in "${required[@]}"; do
  [[ -f "$source_dir/$file" ]] || { echo "missing Flux release input: deploy/flux/$file" >&2; exit 1; }
done

build_dir="$(mktemp -d)"
trap 'rm -rf -- "$build_dir"' EXIT
mkdir -p "$build_dir/flux"
for file in "${required[@]}"; do
  install -m 0644 "$source_dir/$file" "$build_dir/flux/$file"
done

(
  cd "$build_dir/flux"
  sha256sum VERSION LICENSE.flux2 README.md checksums.txt install.yaml provenance.json trust-policy.json > CONTENTS.sha256
)

artifact="$build_dir/flux-distribution.tar.gz"
TZ=UTC tar --sort=name --mtime='@0' --owner=0 --group=0 --numeric-owner -C "$build_dir" \
  -cf - flux | gzip -n -9 > "$artifact"

if [[ -n "$check" ]]; then
  cmp -s "$artifact" "$check" || { echo "Flux distribution artifact is not reproducible or is stale: $check" >&2; exit 1; }
fi
if [[ -n "$output" ]]; then
  mkdir -p "$(dirname "$output")"
  install -m 0644 "$artifact" "$output"
  sha256sum "$output"
fi
if [[ -z "$check$output" ]]; then
  sha256sum "$artifact"
fi
