#!/usr/bin/env bash

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
harness="$repo_root/scripts/test-flux-integration.sh"
mock_dir="$(mktemp -d)"
trap 'rm -rf "$mock_dir"' EXIT

for command in docker k3d; do
    cat >"$mock_dir/$command" <<EOF
#!/usr/bin/env bash
echo "$command must not be invoked by --validate-only" >&2
exit 99
EOF
    chmod +x "$mock_dir/$command"
done

PATH="$mock_dir:$PATH" "$harness" --validate-only >/dev/null

if FLUX_INTEGRATION_CLUSTER=production PATH="$mock_dir:$PATH" "$harness" --validate-only >/dev/null 2>&1; then
    echo "unsafe non-test cluster name was accepted" >&2
    exit 1
fi

grep -q 'kubeconfig-update-default=false' "$harness"
grep -q 'test-run-flux-\*' "$harness"
grep -q 'check-build-capacity.sh' "$harness"
grep -q 'drift-remediation' "$harness"
grep -q 'controller-restart' "$harness"
grep -q 'delete-and-prune' "$harness"

echo "flux-integration-harness-test: OK"
