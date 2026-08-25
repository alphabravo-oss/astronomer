#!/usr/bin/env bash
set -Eeuo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
version="${OAPI_CODEGEN_VERSION:-v2.5.0}"
temporary="$(mktemp -d)"
trap 'rm -rf -- "$temporary"' EXIT

config="$temporary/oapi-codegen.yaml"
output="$temporary/astroclient.gen.go"
sed \
  -e "s#^output: .*#output: $output#" \
  -e "s#^    path: oapi-codegen.overlay.yaml#    path: $root/oapi-codegen.overlay.yaml#" \
  "$root/oapi-codegen.yaml" >"$config"

cd "$root"
go run "github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@${version}" \
  -config "$config" docs/openapi.yaml
diff -u pkg/astroclient/astroclient.gen.go "$output"
printf 'generated Go SDK is current\n'
