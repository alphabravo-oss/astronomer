#!/usr/bin/env bash
# Dark-site helper: copy a saved image archive into a private registry.
# Does not accept registry passwords; Skopeo uses its normal credential store.
set -euo pipefail
root="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
exec python3 "$root/airgap-kit.py" load "$@"
