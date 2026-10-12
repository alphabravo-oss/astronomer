#!/usr/bin/env bash
set -Eeuo pipefail
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
baseline="${SDK_V1_BASELINE:-v1.2.1}"
fixture="$(mktemp -d "$root/.sdk-v1-fixture-XXXXXX")"
trap 'rm -rf -- "$fixture"' EXIT
cd "$root"
git show "$baseline:pkg/astroclient/astroclient.gen.go" > "$fixture/baseline.source"
python3 scripts/tests/sdk_reviewed_breaks_test.py
python3 scripts/filter-sdk-reviewed-breaks.py "$fixture/baseline.source" pkg/astroclient/astroclient.gen.go
go run ./scripts/sdk-compatibility "$fixture/baseline.source" "$fixture/legacy_test.go" pkg/astroclient/astroclient.gen.go
go test "$fixture"
go test ./pkg/astroclient -run 'TestExistingPublicClientAndNullableModels|TestLegacyInhibitionPointerMarshal' -count=1
