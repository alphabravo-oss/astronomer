#!/usr/bin/env bash
set -Eeuo pipefail
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
baseline="${SDK_V1_BASELINE:-v1.2.1}"
fixture="$(mktemp -d "$root/.sdk-v1-fixture-XXXXXX")"
trap 'rm -rf -- "$fixture"' EXIT
cd "$root"
git show "$baseline:pkg/astroclient/astroclient.gen.go" > "$fixture/baseline.source"
go run ./scripts/sdk-compatibility "$fixture/baseline.source" "$fixture/legacy_test.go" pkg/astroclient/astroclient.gen.go
go test "$fixture"
go test ./pkg/astroclient -run 'TestExistingPublicClientAndNullableModels|TestLegacyInhibitionPointerMarshal' -count=1
