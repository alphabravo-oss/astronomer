#!/usr/bin/env bash
set -Eeuo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"

command -v shellcheck >/dev/null 2>&1 || {
  printf 'ERROR: shellcheck is required for the enterprise static gate\n' >&2
  exit 2
}

mapfile -d '' scripts < <(find scripts -type f -name '*.sh' -print0 | sort -z)
(( ${#scripts[@]} > 0 )) || {
  printf 'ERROR: no owned shell scripts found\n' >&2
  exit 1
}

for script in "${scripts[@]}"; do
  bash -n "$script"
done
shellcheck --external-sources "${scripts[@]}"
printf 'shell syntax and ShellCheck passed for %d scripts\n' "${#scripts[@]}"
