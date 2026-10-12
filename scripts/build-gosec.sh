#!/usr/bin/env bash
# Build unchanged released scanner source with the qualified upstream dependency closure.
set -euo pipefail
root_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
if [[ $# != 1 || "$1" != /* ]]; then
  echo 'usage: scripts/build-gosec.sh /absolute/output/gosec' >&2
  exit 2
fi
export GOWORK=off
python3 "$root_dir/scripts/check-gosec-tool.py"
expected_compiler="$(python3 -c 'import json,sys; print("go"+json.load(open(sys.argv[1]))["compiler"])' "$root_dir/tools/gosec/closure.json")"
actual_compiler="$(go -C "$root_dir/tools/gosec" env GOVERSION)"
if [[ "$actual_compiler" != "$expected_compiler" ]]; then
  echo 'Selected scanner compiler differs from qualified closure' >&2
  exit 1
fi
go -C "$root_dir/tools/gosec" mod verify
go -C "$root_dir/tools/gosec" build -mod=readonly -trimpath -o "$1" github.com/securego/gosec/v2/cmd/gosec
