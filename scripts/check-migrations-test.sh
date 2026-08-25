#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
CHECK="$ROOT/scripts/check-migrations.sh"
FIXTURES="$(mktemp -d)"
cleanup() { rm -rf "$FIXTURES"; }
trap cleanup EXIT

write_pair() {
  local directory="$1" up_sql="$2"
  mkdir -p "$directory"
  printf '%s\n' "$up_sql" >"$directory/001_policy.up.sql"
  printf '%s\n' 'SELECT 1;' >"$directory/001_policy.down.sql"
}

expect_pass() {
  local name="$1" directory="$2"
  if ! "$CHECK" "$directory" >"$FIXTURES/$name.log" 2>&1; then
    echo "check-migrations-test: expected $name to pass" >&2
    sed -n '1,120p' "$FIXTURES/$name.log" >&2
    exit 1
  fi
}

expect_fail() {
  local name="$1" directory="$2" expected="$3"
  if "$CHECK" "$directory" >"$FIXTURES/$name.log" 2>&1; then
    echo "check-migrations-test: expected $name to fail" >&2
    exit 1
  fi
  grep -q "$expected" "$FIXTURES/$name.log" || {
    echo "check-migrations-test: $name did not report $expected" >&2
    sed -n '1,120p' "$FIXTURES/$name.log" >&2
    exit 1
  }
}

write_pair "$FIXTURES/safe-expand" 'ALTER TABLE widgets ADD COLUMN state text NOT NULL DEFAULT '\''ready'\'';'
expect_pass safe-expand "$FIXTURES/safe-expand"

write_pair "$FIXTURES/blocking-expand" 'ALTER TABLE widgets ADD COLUMN state text NOT NULL;'
expect_fail blocking-expand "$FIXTURES/blocking-expand" 'ADD COLUMN'

write_pair "$FIXTURES/unapproved-contract" 'ALTER TABLE widgets DROP COLUMN legacy_state;'
expect_fail unapproved-contract "$FIXTURES/unapproved-contract" 'compatibility-window'

write_pair "$FIXTURES/partial-contract" $'-- migration-phase: contract\n-- compatibility-window: after 1.4.x\nDROP TABLE legacy_widgets;'
expect_fail partial-contract "$FIXTURES/partial-contract" 'destructive-change-approved'

write_pair "$FIXTURES/approved-contract" $'-- migration-phase: contract\n-- compatibility-window: after 1.4.x support ends\n-- destructive-change-approved: ASTRO-1234\nALTER TABLE widgets DROP COLUMN legacy_state;'
expect_pass approved-contract "$FIXTURES/approved-contract"

mkdir -p "$FIXTURES/cleared-search-path"
printf '%s\n' "SELECT pg_catalog.set_config('search_path', '', false);" >"$FIXTURES/cleared-search-path/001_initial.up.sql"
printf '%s\n' 'SELECT 1;' >"$FIXTURES/cleared-search-path/001_initial.down.sql"
printf '%s\n' 'ALTER TABLE widgets ADD COLUMN state text;' >"$FIXTURES/cleared-search-path/002_incremental.up.sql"
printf '%s\n' 'ALTER TABLE public.widgets DROP COLUMN state;' >"$FIXTURES/cleared-search-path/002_incremental.down.sql"
expect_fail cleared-search-path "$FIXTURES/cleared-search-path" 'unqualified relations'

sed -i 's/ALTER TABLE widgets/ALTER TABLE public.widgets/' "$FIXTURES/cleared-search-path/002_incremental.up.sql"
expect_pass qualified-after-cleared-search-path "$FIXTURES/cleared-search-path"

echo "check-migrations-test: all policy fixtures passed"
