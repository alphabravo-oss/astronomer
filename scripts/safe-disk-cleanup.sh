#!/usr/bin/env bash
# Plan and apply narrowly scoped disk cleanup without touching containers,
# volumes, tagged/current/rollback images, backups, TLS material, or workspaces.

set -euo pipefail

mode="${1:-}"
if [[ "$mode" != "plan" && "$mode" != "apply" ]]; then
    echo "usage: safe-disk-cleanup.sh plan --output FILE [--protect-file FILE] | apply --plan FILE [--protect-file FILE]" >&2
    exit 2
fi
shift

plan_file=""
protect_file=""
while (($# > 0)); do
    case "$1" in
        --output|--plan)
            (($# >= 2)) || { echo "safe-disk-cleanup: $1 requires a file" >&2; exit 2; }
            plan_file="$2"
            shift 2
            ;;
        --protect-file)
            (($# >= 2)) || { echo "safe-disk-cleanup: --protect-file requires a file" >&2; exit 2; }
            protect_file="$2"
            shift 2
            ;;
        *)
            echo "safe-disk-cleanup: unknown argument: $1" >&2
            exit 2
            ;;
    esac
done

if [[ -z "$plan_file" ]]; then
    echo "safe-disk-cleanup: an explicit plan file is required" >&2
    exit 2
fi
if [[ "$mode" == "apply" && ! -f "$plan_file" ]]; then
    echo "safe-disk-cleanup: plan file does not exist" >&2
    exit 2
fi
if [[ -n "$protect_file" && ! -f "$protect_file" ]]; then
    echo "safe-disk-cleanup: protect file does not exist" >&2
    exit 2
fi
command -v docker >/dev/null || { echo "safe-disk-cleanup: docker is unavailable" >&2; exit 2; }

image_id_pattern='^sha256:[0-9a-f]{64}$'
declare -A protected=()

load_protected_file() {
    local file="$1" line
    [[ -n "$file" ]] || return 0
    while IFS= read -r line || [[ -n "$line" ]]; do
        line="${line%%#*}"
        line="${line//[[:space:]]/}"
        [[ -z "$line" ]] && continue
        if [[ ! "$line" =~ $image_id_pattern ]]; then
            echo "safe-disk-cleanup: invalid protected image ID" >&2
            exit 2
        fi
        protected["$line"]=1
    done <"$file"
}

load_container_references() {
    local container_id image_id
    while IFS= read -r container_id; do
        [[ -z "$container_id" ]] && continue
        image_id="$(docker container inspect --format '{{.Image}}' "$container_id")"
        [[ "$image_id" =~ $image_id_pattern ]] || {
            echo "safe-disk-cleanup: docker returned an invalid container image ID" >&2
            exit 2
        }
        protected["$image_id"]=1
    done < <(docker container ls --all --quiet --no-trunc | LC_ALL=C sort -u)
}

current_candidates() {
    local image_id
    while IFS= read -r image_id; do
        [[ -z "$image_id" ]] && continue
        [[ "$image_id" =~ $image_id_pattern ]] || {
            echo "safe-disk-cleanup: docker returned an invalid dangling image ID" >&2
            exit 2
        }
        if [[ -z "${protected[$image_id]:-}" ]]; then
            printf '%s\n' "$image_id"
        fi
    done < <(docker image ls --filter dangling=true --quiet --no-trunc | LC_ALL=C sort -u)
}

load_protected_file "$protect_file"
load_container_references

if [[ "$mode" == "plan" ]]; then
    if [[ -e "$plan_file" && ! -f "$plan_file" ]]; then
        echo "safe-disk-cleanup: output path must be a regular file" >&2
        exit 2
    fi
    umask 077
    temporary="${plan_file}.tmp.$$"
    trap 'rm -f -- "$temporary"' EXIT
    {
        echo "astronomer-safe-cleanup-v1"
        current_candidates | sed 's/^/docker-image\t/'
    } >"$temporary"
    mv -f -- "$temporary" "$plan_file"
    trap - EXIT
    count="$(awk -F '\t' '$1 == "docker-image" {count++} END {print count+0}' "$plan_file")"
    echo "safe-disk-cleanup: planned $count untagged, unreferenced Docker image(s); no data was removed"
    exit 0
fi

header="$(sed -n '1p' "$plan_file")"
if [[ "$header" != "astronomer-safe-cleanup-v1" ]]; then
    echo "safe-disk-cleanup: unsupported or malformed cleanup plan" >&2
    exit 2
fi

declare -A eligible=()
while IFS= read -r image_id; do
    [[ -n "$image_id" ]] && eligible["$image_id"]=1
done < <(current_candidates)

removed=0
while IFS=$'\t' read -r kind image_id extra; do
    [[ "$kind" == "astronomer-safe-cleanup-v1" ]] && continue
    if [[ "$kind" != "docker-image" || ! "$image_id" =~ $image_id_pattern || -n "${extra:-}" ]]; then
        echo "safe-disk-cleanup: malformed cleanup plan entry" >&2
        exit 2
    fi
    if [[ -n "${protected[$image_id]:-}" || -z "${eligible[$image_id]:-}" ]]; then
        echo "safe-disk-cleanup: REFUSE $image_id changed or became protected; regenerate the plan" >&2
        exit 1
    fi
done <"$plan_file"

while IFS=$'\t' read -r kind image_id _; do
    [[ "$kind" == "docker-image" ]] || continue
    docker image rm "$image_id"
    echo "safe-disk-cleanup: removed docker-image $image_id"
    removed=$((removed + 1))
done <"$plan_file"

echo "safe-disk-cleanup: removed $removed image(s); containers, volumes, tagged images, caches, backups, TLS material, and workspaces were untouched"
