#!/usr/bin/env bash
# Fail when any Go binary inside an image was built with a Go release older
# than go.mod's `go` line (the security floor: Go 1.27.3 distrusts the
# compromised eMudhra intermediates; older toolchains accept them).
# usage: scripts/check-image-go.sh <image-ref> [platform ...]   (default linux/amd64)
set -Eeuo pipefail
ref=${1:?usage: check-image-go.sh <image-ref> [platform ...]}; shift
platforms=("${@:-linux/amd64}")
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
floor="${GO_FLOOR:-$(awk '/^go /{print $2}' "$root/go.mod")}"
command -v go >/dev/null || { echo "check-image-go: go is required" >&2; exit 2; }

ver_ge() { # ver_ge go1.27.3 1.27.3
  local a b; a="${1#go}"; a="${a%%[!0-9.]*}"; b="$2"
  [ "$(printf '%s\n%s\n' "$b" "$a" | sort -V | head -1)" = "$b" ]
}

status=0
for platform in "${platforms[@]}"; do
  dir="$(mktemp -d)"
  target="$ref"
  # A pushed multi-arch index: pull the exact per-platform manifest by digest
  # (Docker's containerd store refuses a second platform under one index ref).
  if [[ "$ref" == *@sha256:* ]] && raw="$(docker buildx imagetools inspect --raw "$ref" 2>/dev/null)"; then
    pd="$(python3 -c 'import json,sys
d=json.loads(sys.argv[1]); o,a=sys.argv[2].split("/")[:2]
print(next((m["digest"] for m in d.get("manifests",[]) if m.get("platform",{}).get("os")==o and m.get("platform",{}).get("architecture")==a),""))' "$raw" "$platform")"
    [ -n "$pd" ] && target="${ref%@*}@$pd"
  fi
  docker pull -q --platform "$platform" "$target" >/dev/null 2>&1 || true
  cid="$(docker create --platform "$platform" "$target" /nonexistent)"
  docker export "$cid" | tar -x -C "$dir" 2>/dev/null || true
  docker rm -f "$cid" >/dev/null
  found=0
  while IFS= read -r -d '' file; do
    v="$(go version "$file" 2>/dev/null | awk '{print $NF}')" || continue
    [ -n "$v" ] || continue
    found=$((found + 1))
    if ver_ge "$v" "$floor"; then
      echo "ok   $platform ${file#"$dir"} $v"
    else
      echo "FAIL $platform ${file#"$dir"} $v < go$floor" >&2; status=1
    fi
  done < <(find "$dir" -type f -size +100k -print0)
  echo "$ref ($platform): $found Go binaries checked (floor go$floor)"
  rm -rf "$dir"
done
exit "$status"
