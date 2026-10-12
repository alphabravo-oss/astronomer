#!/usr/bin/env bash
set -Eeuo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
output="${1:?absolute output path is required}"
[[ "$output" == /* ]] || { echo 'gotestsum output must be absolute' >&2; exit 2; }
version="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["gotestsum"])' "$root/deploy/qualification/runtime.json")"
export GOWORK=off
compiler="$(python3 -c 'import json,sys; print("go"+next(c["selected"] for c in json.load(open(sys.argv[1]))["components"] if c["id"]=="go"))' "$root/deploy/toolchain/versions.json")"
[[ "$(go env GOVERSION)" == "$compiler" ]] || { echo 'gotestsum compiler differs from selected authority' >&2; exit 1; }
go -C "$root" run ./deploy/toolchain/check "$root/deploy/qualification/runtime.schema.json" "$root/deploy/qualification/runtime.json"
temporary="$(mktemp -d)"
trap 'rm -rf "$temporary"' EXIT
GOBIN="$temporary" go install "gotest.tools/gotestsum@v$version"
mkdir -p "$(dirname "$output")"
install -m 0755 "$temporary/gotestsum" "$output"
"$output" --version
