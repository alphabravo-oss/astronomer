#!/usr/bin/env bash

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
script="$repo_root/scripts/check-build-capacity.sh"
fixture_dir="$(mktemp -d)"
trap 'rm -rf "$fixture_dir"' EXIT

cat >"$fixture_dir/df" <<'EOF'
#!/usr/bin/env bash
cat <<'OUT'
Filesystem 1024-blocks Used Available Capacity Mounted on
/dev/test 104857600 52428800 52428800 50% /test
OUT
EOF
chmod +x "$fixture_dir/df"

PATH="$fixture_dir:$PATH" "$script" --path "$fixture_dir" --min-free-gib 40 --min-free-percent 40 >/dev/null

if PATH="$fixture_dir:$PATH" "$script" --path "$fixture_dir" --min-free-gib 60 --min-free-percent 40 >/dev/null 2>&1; then
    echo "expected absolute-capacity failure" >&2
    exit 1
fi
if PATH="$fixture_dir:$PATH" "$script" --path "$fixture_dir" --min-free-gib 40 --min-free-percent 60 >/dev/null 2>&1; then
    echo "expected percentage-capacity failure" >&2
    exit 1
fi

json="$(PATH="$fixture_dir:$PATH" "$script" --path "$fixture_dir" --min-free-gib 40 --min-free-percent 40 --json)"
[[ "$json" == *'"available_gib":50'* ]]
[[ "$json" == *'"free_percent":50'* ]]
[[ "$json" == *'"meets_minimum":true'* ]]

if PATH="$fixture_dir:$PATH" "$script" --min-free-percent 101 >/dev/null 2>&1; then
    echo "expected invalid-percent failure" >&2
    exit 1
fi

echo "check-build-capacity-test: OK"
