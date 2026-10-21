#!/usr/bin/env bash
# Retry transient GitHub OIDC / registry / transparency-log failures against
# the same immutable subject. Never changes identity, trust, or keyless mode.
set -euo pipefail
for attempt in 1 2 3; do
  if cosign "$@"; then
    exit 0
  fi
  if [ "$attempt" -lt 3 ]; then
    echo "cosign failed; retrying the same keyless operation (attempt $((attempt + 1))/3)" >&2
    sleep "$((attempt * 2))"
  fi
done
exit 1
