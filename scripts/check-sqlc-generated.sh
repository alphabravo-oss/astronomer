#!/usr/bin/env bash
set -euo pipefail

SQLC_VERSION="${SQLC_VERSION:-v1.31.1}"

# sqlc preserves the query filename in generated `*.sql.go` output. Go applies
# GOOS suffix rules before the first dot, so a source such as
# `maintenance_windows.sql` silently generates a Windows-only Go file. Reject
# every recognized GOOS suffix at the source instead of maintaining parallel
# hand-written implementations for other platforms.
goos_named_queries="$({ find internal/db/queries -maxdepth 1 -type f -printf '%f\n' || true; } | rg '_(aix|android|darwin|dragonfly|freebsd|hurd|illumos|ios|js|linux|netbsd|openbsd|plan9|solaris|wasip1|windows|zos)\.sql$' || true)"
if [[ -n "$goos_named_queries" ]]; then
  printf 'sqlc query filenames must not end in a GOOS suffix:\n%s\n' "$goos_named_queries" >&2
  exit 1
fi

# Compare generator output with the caller's current tree, not with HEAD. This
# keeps the drift gate meaningful while a legitimate query/generated change is
# still uncommitted (the old git-diff check rejected every such change even
# when sqlc produced no further edits).
snapshot_dir="$(mktemp -d)"
trap 'rm -rf -- "$snapshot_dir"' EXIT
cp -a internal/db/sqlc "$snapshot_dir/sqlc"

go run "github.com/sqlc-dev/sqlc/cmd/sqlc@${SQLC_VERSION}" generate

if ! diff -ru "$snapshot_dir/sqlc" internal/db/sqlc; then
  printf 'sqlc generated output was stale; generated files were refreshed above\n' >&2
  exit 1
fi

printf 'sqlc generated output is current\n'
