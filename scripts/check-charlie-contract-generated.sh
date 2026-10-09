#!/usr/bin/env bash
set -Eeuo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source_dir="$root/internal/charlie/contract"
temporary="$(mktemp -d "${TMPDIR:-/tmp}/astronomer-charlie-contract-check.XXXXXX")"
trap 'rm -rf -- "$temporary"' EXIT

cd "$source_dir"
sha256sum -c checksums.sha256

# Keep the pinned generator's module context outside the inventoried source tree.
cp "$root/go.mod" "$root/go.sum" "$temporary/"
mkdir -p "$temporary/internal/charlie"
cp -a . "$temporary/internal/charlie/contract"
cd "$temporary/internal/charlie/contract"
go generate ./...

diff -ru "$source_dir/internal/schema" internal/schema
diff -ru "$source_dir/internal/wire" internal/wire
printf 'Charlie pinned contract and generated client are current\n'
