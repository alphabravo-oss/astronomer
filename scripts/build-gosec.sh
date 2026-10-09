#!/usr/bin/env bash
# Build unchanged released scanner source with the qualified upstream dependency closure.
set -euo pipefail
root_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
if [[ $# != 1 || "$1" != /* ]]; then
  echo 'usage: scripts/build-gosec.sh /absolute/output/gosec' >&2
  exit 2
fi
python3 "$root_dir/scripts/check-gosec-tool.py"
go -C "$root_dir/tools/gosec" mod verify
go -C "$root_dir/tools/gosec" build -mod=readonly -trimpath -o "$1" github.com/securego/gosec/v2/cmd/gosec
