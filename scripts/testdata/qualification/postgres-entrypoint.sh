#!/usr/bin/env bash
# Test-only bootstrap for the unchanged official CloudNativePG operand image.
set -Eeuo pipefail
[[ "${POSTGRES_USER:-}" =~ ^[a-z][a-z0-9_]*$ && "${POSTGRES_DB:-}" =~ ^[a-z][a-z0-9_]*$ && "${POSTGRES_PASSWORD:-}" =~ ^[a-f0-9]{36,}$ ]] || {
  echo 'qualification PostgreSQL: invalid disposable identity' >&2; exit 2;
}
case "${QUALIFICATION_POSTGRES_MAJOR:-}" in 17|18) ;; *) exit 2 ;; esac
export PATH="/usr/lib/postgresql/$QUALIFICATION_POSTGRES_MAJOR/bin:$PATH"
export PGDATA=/var/lib/postgresql/qualification-data
install -d -m 0700 -o postgres -g postgres "$PGDATA"
if [[ ! -f "$PGDATA/PG_VERSION" ]]; then
  password_file="$(mktemp)"
  chmod 0600 "$password_file"
  printf '%s\n' "$POSTGRES_PASSWORD" >"$password_file"
  chown postgres:postgres "$password_file"
  runuser -u postgres -- initdb -D "$PGDATA" -U "$POSTGRES_USER" \
    --encoding=UTF8 --locale=C.UTF-8 --pwfile="$password_file" --auth-local=trust --auth-host=scram-sha-256 >/dev/null
  rm -f "$password_file"
  printf 'host all all all scram-sha-256\n' >>"$PGDATA/pg_hba.conf"
  runuser -u postgres -- pg_ctl -D "$PGDATA" -o '-c listen_addresses= -c unix_socket_directories=/var/run/postgresql' -w start >/dev/null
  runuser -u postgres -- createdb -h /var/run/postgresql -U "$POSTGRES_USER" "$POSTGRES_DB"
  runuser -u postgres -- pg_ctl -D "$PGDATA" -m fast -w stop >/dev/null
fi
unset POSTGRES_PASSWORD
exec runuser -u postgres -- postgres -D "$PGDATA" -c listen_addresses='*' -c unix_socket_directories=/var/run/postgresql "$@"
