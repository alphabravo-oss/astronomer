#!/usr/bin/env bash

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
script="$repo_root/scripts/safe-disk-cleanup.sh"
fixture_dir="$(mktemp -d)"
trap 'rm -rf "$fixture_dir"' EXIT

image_a="sha256:$(printf 'a%.0s' {1..64})"
image_b="sha256:$(printf 'b%.0s' {1..64})"
image_c="sha256:$(printf 'c%.0s' {1..64})"

cat >"$fixture_dir/docker" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
case "$1 $2" in
    "container ls") printf '%s\n' container-running container-stopped ;;
    "container inspect")
        [[ "${@: -1}" == "container-running" ]] && printf '%s\n' "$IMAGE_B" || printf '%s\n' "$IMAGE_C"
        ;;
    "image ls") printf '%s\n' "$IMAGE_A" "$IMAGE_B" "$IMAGE_C" ;;
    "image rm") printf '%s\n' "removed $3" >>"$REMOVE_LOG" ;;
    *) echo "unexpected docker arguments: $*" >&2; exit 9 ;;
esac
EOF
chmod +x "$fixture_dir/docker"
export IMAGE_A="$image_a" IMAGE_B="$image_b" IMAGE_C="$image_c" REMOVE_LOG="$fixture_dir/removed"

PATH="$fixture_dir:$PATH" "$script" plan --output "$fixture_dir/plan" >/dev/null
grep -Fx $'astronomer-safe-cleanup-v1' "$fixture_dir/plan" >/dev/null
grep -Fx $'docker-image\t'"$image_a" "$fixture_dir/plan" >/dev/null
if grep -F "$image_b" "$fixture_dir/plan" >/dev/null || grep -F "$image_c" "$fixture_dir/plan" >/dev/null; then
    echo "container-referenced image entered cleanup plan" >&2
    exit 1
fi

PATH="$fixture_dir:$PATH" "$script" apply --plan "$fixture_dir/plan" >/dev/null
grep -Fx "removed $image_a" "$fixture_dir/removed" >/dev/null
[[ "$(wc -l <"$fixture_dir/removed")" -eq 1 ]]

printf '%s\n' "$image_a" >"$fixture_dir/protected"
PATH="$fixture_dir:$PATH" "$script" plan --output "$fixture_dir/protected-plan" --protect-file "$fixture_dir/protected" >/dev/null
if grep -F "$image_a" "$fixture_dir/protected-plan" >/dev/null; then
    echo "explicitly protected image entered cleanup plan" >&2
    exit 1
fi

sed -i "s/$image_a/sha256:$(printf 'd%.0s' {1..64})/" "$fixture_dir/plan"
if PATH="$fixture_dir:$PATH" "$script" apply --plan "$fixture_dir/plan" >/dev/null 2>&1; then
    echo "stale plan unexpectedly applied" >&2
    exit 1
fi

echo "safe-disk-cleanup-test: OK"
