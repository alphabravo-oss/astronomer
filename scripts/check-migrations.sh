#!/usr/bin/env bash
# Migration safety and expand/migrate/contract policy lint.
#
# Fails CI for blocking ADD COLUMN patterns and for destructive contract DDL
# without explicit, reviewable compatibility-window metadata.
#
# Destructive DDL is accepted only when the migration contains all of:
#   -- migration-phase: contract
#   -- compatibility-window: <supported release/window description>
#   -- destructive-change-approved: <issue/change record>
#
# Run locally:
#   ./scripts/check-migrations.sh
#
# Run from a different working directory by passing the migrations dir:
#   ./scripts/check-migrations.sh path/to/migrations

set -euo pipefail
shopt -s nullglob

MIGRATIONS_DIR="${1:-internal/db/migrations}"
if [[ ! -d "$MIGRATIONS_DIR" ]]; then
    echo "check-migrations: directory not found: $MIGRATIONS_DIR" >&2
    exit 2
fi

up_files=("$MIGRATIONS_DIR"/*.up.sql)
all_files=("$MIGRATIONS_DIR"/*.up.sql "$MIGRATIONS_DIR"/*.down.sql)
if ((${#up_files[@]} == 0)); then
    echo "check-migrations: no *.up.sql files found in $MIGRATIONS_DIR" >&2
    exit 2
fi

# The canonical greenfield dump deliberately clears search_path for the
# migration session. The production migrator reuses that exact connection, so
# every incremental DDL relation reference must remain schema-qualified.
if [[ -f "$MIGRATIONS_DIR/001_initial.up.sql" ]] && grep -q "set_config('search_path', '', false)" "$MIGRATIONS_DIR/001_initial.up.sql"; then
    unqualified_ddl="$(python3 - "${all_files[@]}" <<'PY'
import pathlib
import re
import sys

pattern = re.compile(
    r"\b(?:ALTER\s+TABLE|DROP\s+TABLE|TRUNCATE(?:\s+TABLE)?)\s+"
    r"(?:ONLY\s+)?(?:IF\s+EXISTS\s+)?(?P<relation>"
    r"[A-Za-z_][A-Za-z0-9_$\"]*(?:\.[A-Za-z_][A-Za-z0-9_$\"]*)?)",
    re.IGNORECASE,
)
for name in sys.argv[1:]:
    path = pathlib.Path(name)
    if path.name.startswith("001_"):
        continue
    for number, line in enumerate(path.read_text(encoding="utf-8").splitlines(), 1):
        sql = line.split("--", 1)[0]
        match = pattern.search(sql)
        if match and "." not in match.group("relation"):
            print(f"{path}:{number}:{line.strip()}")
PY
)"
    if [[ -n "$unqualified_ddl" ]]; then
        echo "check-migrations: BLOCK — incremental DDL uses unqualified relations after 001 clears search_path:" >&2
        echo "$unqualified_ddl" >&2
        exit 1
    fi
fi

blocking_violations="$(
    grep -HniE 'add[[:space:]]+column.*not[[:space:]]+null' "${up_files[@]}" 2>/dev/null \
    | grep -viE 'default' \
    || true
)"

if [[ -n "$blocking_violations" ]]; then
    echo "check-migrations: BLOCK — found ADD COLUMN ... NOT NULL without DEFAULT:" >&2
    echo >&2
    echo "$blocking_violations" >&2
    echo >&2
    echo "Fix: add a DEFAULT clause on the same line. Example:" >&2
    echo "  ALTER TABLE foo ADD COLUMN bar VARCHAR(64) NOT NULL DEFAULT '';" >&2
    echo "" >&2
    echo "Why: on a populated table, NOT NULL without DEFAULT causes Postgres" >&2
    echo "to scan + rewrite every row under an ACCESS EXCLUSIVE lock, blocking" >&2
    echo "all writes for the duration of the migration." >&2
    exit 1
fi

destructive_pattern='(^|[^[:alpha:]_])(drop[[:space:]]+(table|schema|index)|drop[[:space:]]+column|alter[[:space:]]+column.*[[:space:]]type[[:space:]]|rename[[:space:]]+(column|table|to)|set[[:space:]]+not[[:space:]]+null|truncate[[:space:]]+(table[[:space:]]+)?)([^[:alpha:]_]|$)'
contract_violations=()
for migration in "${up_files[@]}"; do
    destructive_lines="$(
        grep -niE "$destructive_pattern" "$migration" 2>/dev/null \
        | grep -vE '^[0-9]+:[[:space:]]*--' \
        || true
    )"
    [[ -n "$destructive_lines" ]] || continue

    missing=()
    grep -qiE '^[[:space:]]*--[[:space:]]*migration-phase:[[:space:]]*contract[[:space:]]*$' "$migration" \
        || missing+=("migration-phase: contract")
    grep -qiE '^[[:space:]]*--[[:space:]]*compatibility-window:[[:space:]]*[^[:space:]].+$' "$migration" \
        || missing+=("compatibility-window: <window>")
    grep -qiE '^[[:space:]]*--[[:space:]]*destructive-change-approved:[[:space:]]*[^[:space:]].+$' "$migration" \
        || missing+=("destructive-change-approved: <change record>")

    if ((${#missing[@]} > 0)); then
        contract_violations+=("$migration: destructive DDL requires ${missing[*]}")
        while IFS= read -r line; do
            contract_violations+=("  $line")
        done <<<"$destructive_lines"
    fi
done

if ((${#contract_violations[@]} > 0)); then
    echo "check-migrations: BLOCK — destructive contract DDL lacks release-window approval:" >&2
    echo >&2
    printf '%s\n' "${contract_violations[@]}" >&2
    echo >&2
    echo "Use an expand/migrate release first. Contract DDL may be added only after" >&2
    echo "the declared compatibility window has expired and a change record approves it." >&2
    exit 1
fi

echo "check-migrations: OK (${#up_files[@]} migration files scanned)"
