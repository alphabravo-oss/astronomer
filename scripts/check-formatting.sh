#!/usr/bin/env bash
set -Eeuo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"

mapfile -d '' go_files < <(rg --files -0 -g '*.go')
unformatted="$(gofmt -l "${go_files[@]}")"
if [[ -n "$unformatted" ]]; then
  printf 'FAIL: gofmt required for:\n%s\n' "$unformatted" >&2
  exit 1
fi
git diff --check
printf 'Go formatting and patch whitespace are clean\n'
