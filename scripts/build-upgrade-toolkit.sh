#!/usr/bin/env bash
set -euo pipefail
if [[ $# != 4 ]]; then
  echo 'usage: build-upgrade-toolkit.sh <absolute selected-go> <absolute signed-resolution-directory> <vX.Y.Z> <absolute fresh-output>' >&2
  exit 2
fi
go_executable="$1"
resolution_directory="$2"
target="$3"
output="$4"
[[ "$go_executable" == /* && "$resolution_directory" == /* && "$output" == /* && "$target" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]
test -f "$go_executable" && test -x "$go_executable"
source_commit="$(git rev-parse --verify HEAD)"
[[ "$source_commit" =~ ^[a-f0-9]{40}$ ]]
test -z "$(git status --porcelain)"
selected_go="$(python3 - <<'AUTHORITY'
import json
rows=[row for row in json.load(open('deploy/toolchain/versions.json'))['components'] if row['id']=='go']
assert len(rows)==1
print(rows[0]['selected'])
AUTHORITY
)"
actual_go="$("$go_executable" version)"
[[ "$actual_go" == "go version go${selected_go} linux/"* ]]
# All build outputs remain outside the immutable source checkout, including VCS
# metadata used by the producer to refuse mismatched or modified binaries.
test ! -e "$output"
umask 077
mkdir -m 700 -- "$output"
mkdir -m 700 -- "$output/build" "$output/assets"
GOWORK=off GOFLAGS='-mod=readonly -p=2' CGO_ENABLED=0 "$go_executable" build -buildvcs=true -trimpath -ldflags="-X main.sourceCommit=$source_commit" -o "$output/build/upgrade-toolkit" ./cmd/upgrade-toolkit
for architecture in amd64 arm64; do
  directory="$output/build/$architecture"
  mkdir -m 700 -- "$directory" "$directory/kit"
  GOWORK=off GOFLAGS='-mod=readonly -p=2' CGO_ENABLED=0 GOOS=linux GOARCH="$architecture" "$go_executable" build -buildvcs=true -trimpath -ldflags="-X main.sourceCommit=$source_commit" -o "$directory/upgrade-release" ./cmd/upgrade-release
  GOWORK=off GOFLAGS='-mod=readonly -p=2' CGO_ENABLED=0 GOOS=linux GOARCH="$architecture" "$go_executable" build -buildvcs=true -trimpath -ldflags="-X main.sourceCommit=$source_commit" -o "$output/assets/astronomer-upgrade-toolkit-${target}-linux-${architecture}" ./cmd/upgrade-toolkit
  "$output/build/upgrade-toolkit" build --target "$target" --output "$directory/kit" --resolution-directory "$resolution_directory" --executable "$directory/upgrade-release"
  mv -- "$directory/kit/astronomer-upgrade-${target}-linux-${architecture}.tar.gz" "$output/assets/"
  mv -- "$directory/kit/upgrade-toolkit.json" "$output/assets/astronomer-upgrade-toolkit-${target}-linux-${architecture}.json"
done
test "$(git rev-parse --verify HEAD)" = "$source_commit"
test -z "$(git status --porcelain)"
