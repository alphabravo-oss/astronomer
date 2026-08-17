#!/usr/bin/env bash
set -euo pipefail

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
repo_root=$(cd -- "$script_dir/../.." && pwd)

"$repo_root/scripts/verify-flux-version.sh"
if [[ "${FLUX_DISTRIBUTION_OFFLINE_ONLY:-false}" != "true" ]]; then
  "$repo_root/scripts/update-flux-distribution.sh" --check
fi
