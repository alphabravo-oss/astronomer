#!/usr/bin/env bash
set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
temp_dir="$(mktemp -d)"
trap 'rm -rf -- "$temp_dir"' EXIT

"$root_dir/scripts/build-flux-distribution-artifact.sh" --output "$temp_dir/flux-a.tar.gz" >/dev/null
"$root_dir/scripts/build-flux-distribution-artifact.sh" --output "$temp_dir/flux-b.tar.gz" >/dev/null
cmp "$temp_dir/flux-a.tar.gz" "$temp_dir/flux-b.tar.gz"
"$root_dir/scripts/build-flux-distribution-artifact.sh" --check "$temp_dir/flux-a.tar.gz" >/dev/null

tar -tzf "$temp_dir/flux-a.tar.gz" >"$temp_dir/flux-files"
for required in \
  flux/VERSION flux/LICENSE.flux2 flux/README.md flux/checksums.txt \
  flux/install.yaml flux/provenance.json flux/trust-policy.json flux/CONTENTS.sha256; do
  grep -Fx "$required" "$temp_dir/flux-files" >/dev/null
done

echo "release-artifacts-test: OK"
