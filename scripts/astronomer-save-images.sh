#!/usr/bin/env bash
# Connected-host helper: copy digest-pinned release images into a local tar.
# Does not accept registry passwords; Skopeo uses its normal credential store.
set -euo pipefail
root="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
exec python3 "$root/airgap-kit.py" save "$@"
