#!/usr/bin/env bash
# FEATURES-051126 T30 — migration safety lint.
#
# Fails CI when a `*.up.sql` migration adds a NOT NULL column without a
# DEFAULT clause on the same line. On a large table this pattern blocks
# writes during the schema scan (Postgres rewrites every row) and is the
# most common cause of accidental production outages from "small" schema
# changes.
#
# Allowed: `ADD COLUMN x ... NOT NULL DEFAULT y`
# Forbidden: `ADD COLUMN x ... NOT NULL` without `DEFAULT`
#
# Run locally:
#   ./scripts/check-migrations.sh
#
# Run from a different working directory by passing the migrations dir:
#   ./scripts/check-migrations.sh path/to/migrations

set -euo pipefail

MIGRATIONS_DIR="${1:-internal/db/migrations}"
if [[ ! -d "$MIGRATIONS_DIR" ]]; then
    echo "check-migrations: directory not found: $MIGRATIONS_DIR" >&2
    exit 2
fi

# grep flags:
#   -H file prefix, -n line number, -E extended regex, -i case-insensitive
#   pattern: ADD COLUMN ... NOT NULL (on the same line)
#   then filter OUT lines that ALSO have DEFAULT — those are the safe ones
violations="$(
    grep -HniE 'add[[:space:]]+column.*not[[:space:]]+null' \
        "$MIGRATIONS_DIR"/*.up.sql 2>/dev/null \
    | grep -viE 'default' \
    || true
)"

if [[ -n "$violations" ]]; then
    echo "check-migrations: BLOCK — found ADD COLUMN ... NOT NULL without DEFAULT:" >&2
    echo "" >&2
    echo "$violations" >&2
    echo "" >&2
    echo "Fix: add a DEFAULT clause on the same line. Example:" >&2
    echo "  ALTER TABLE foo ADD COLUMN bar VARCHAR(64) NOT NULL DEFAULT '';" >&2
    echo "" >&2
    echo "Why: on a populated table, NOT NULL without DEFAULT causes Postgres" >&2
    echo "to scan + rewrite every row under an ACCESS EXCLUSIVE lock, blocking" >&2
    echo "all writes for the duration of the migration." >&2
    exit 1
fi

echo "check-migrations: OK ($(ls "$MIGRATIONS_DIR"/*.up.sql 2>/dev/null | wc -l) migration files scanned)"
