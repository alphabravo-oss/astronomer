#!/usr/bin/env bash
# Guard expensive build, integration, and release jobs against filling the host.
# This script is deliberately read-only: cleanup requires a separately reviewed
# list of exact, regenerable targets.

set -euo pipefail

min_free_gib="${ASTRONOMER_MIN_FREE_GIB:-20}"
min_free_percent="${ASTRONOMER_MIN_FREE_PERCENT:-20}"
check_path="${ASTRONOMER_CAPACITY_PATH:-.}"
output="text"

usage() {
    cat <<'EOF'
Usage: check-build-capacity.sh [--path PATH] [--min-free-gib N] [--min-free-percent N] [--json]

Exits 0 only when both absolute and percentage headroom meet the configured
minimums. The command never removes files, images, volumes, or containers.
EOF
}

require_uint() {
    local name="$1" value="$2"
    if [[ ! "$value" =~ ^[0-9]+$ ]]; then
        echo "check-build-capacity: $name must be a non-negative integer" >&2
        exit 2
    fi
}

while (($# > 0)); do
    case "$1" in
        --path)
            (($# >= 2)) || { echo "check-build-capacity: --path requires a value" >&2; exit 2; }
            check_path="$2"
            shift 2
            ;;
        --min-free-gib)
            (($# >= 2)) || { echo "check-build-capacity: --min-free-gib requires a value" >&2; exit 2; }
            min_free_gib="$2"
            shift 2
            ;;
        --min-free-percent)
            (($# >= 2)) || { echo "check-build-capacity: --min-free-percent requires a value" >&2; exit 2; }
            min_free_percent="$2"
            shift 2
            ;;
        --json)
            output="json"
            shift
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        *)
            echo "check-build-capacity: unknown argument: $1" >&2
            usage >&2
            exit 2
            ;;
    esac
done

require_uint "minimum free GiB" "$min_free_gib"
require_uint "minimum free percent" "$min_free_percent"
if ((min_free_percent > 100)); then
    echo "check-build-capacity: minimum free percent must be <= 100" >&2
    exit 2
fi
if [[ ! -e "$check_path" ]]; then
    echo "check-build-capacity: path does not exist: $check_path" >&2
    exit 2
fi

# POSIX 1-KiB blocks make the calculation stable across GNU/BSD human formats.
df_line="$(df -Pk "$check_path" | awk 'NR == 2 { print $2, $3, $4, $5, $6 }')"
read -r total_kib used_kib available_kib used_percent mountpoint <<<"$df_line"
if [[ -z "${mountpoint:-}" ]]; then
    echo "check-build-capacity: unable to read filesystem capacity for $check_path" >&2
    exit 2
fi

used_percent="${used_percent%%%}"
for pair in "total blocks:$total_kib" "used blocks:$used_kib" "available blocks:$available_kib" "used percent:$used_percent"; do
    require_uint "${pair%%:*}" "${pair#*:}"
done

gib_kib=$((1024 * 1024))
available_gib=$((available_kib / gib_kib))
free_percent=$((100 - used_percent))
meets_gib=true
meets_percent=true
((available_kib >= min_free_gib * gib_kib)) || meets_gib=false
((free_percent >= min_free_percent)) || meets_percent=false

if [[ "$output" == "json" ]]; then
    printf '{"path":"%s","mountpoint":"%s","total_kib":%d,"used_kib":%d,"available_kib":%d,"available_gib":%d,"free_percent":%d,"minimum_free_gib":%d,"minimum_free_percent":%d,"meets_minimum":%s}\n' \
        "$check_path" "$mountpoint" "$total_kib" "$used_kib" "$available_kib" \
        "$available_gib" "$free_percent" "$min_free_gib" "$min_free_percent" \
        "$([[ "$meets_gib" == true && "$meets_percent" == true ]] && echo true || echo false)"
else
    echo "check-build-capacity: path=$check_path mount=$mountpoint available=${available_gib}GiB free=${free_percent}% required=${min_free_gib}GiB/${min_free_percent}%"
fi

if [[ "$meets_gib" != true || "$meets_percent" != true ]]; then
    echo "check-build-capacity: BLOCK — insufficient safe filesystem headroom" >&2
    exit 1
fi

