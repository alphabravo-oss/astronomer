#!/bin/sh
# Authentication and closed inventory are mandatory; historical unsigned archive
# arguments intentionally fail the new contract rather than extracting first.
set -eu
root_dir="$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)"
exec python3 "$root_dir/scripts/application-catalog-airgap.py" "$@"
