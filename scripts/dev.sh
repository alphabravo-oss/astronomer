#!/usr/bin/env bash
# Astronomer local dev stack: Postgres + Redis + telemetry (docker compose),
# API server + worker (+ optional agent) under Air, dashboard under Vite.
# Guide ch.04 "scripts/dev.sh entrypoint". Subcommands: up down status logs migrate
set -Eeuo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
RUN_DIR="$ROOT/tmp"
LOG_DIR="$RUN_DIR/logs"
PID_FILE="$RUN_DIR/dev.pids"
COMPOSE_FILE="$ROOT/deploy/docker-compose.yml"
export PATH="$HOME/.local/share/mise/shims:$PATH"

usage() {
  cat <<USAGE
usage: scripts/dev.sh <up|down|status|logs [name]|migrate>

  up            start Postgres, Redis, telemetry, Air server/worker and Vite (nohup, PID file)
  down          stop everything started by up (compose containers keep their volumes)
  status        show container and process state plus the URLs to open
  logs [name]   tail tmp/logs/*.log (or one of: server worker agent web compose)
  migrate       apply pending embedded migrations against DATABASE_URL (the server also does this at boot)
Env: .env.dev (tracked defaults) then .env.dev.local (gitignored overrides).
USAGE
}

require_dev_tools() {
  local missing=0
  for tool in docker go node npx; do
    if ! command -v "$tool" >/dev/null 2>&1; then
      missing=1
      case "$tool" in
        docker) echo "missing docker: https://docs.docker.com/engine/install/" >&2 ;;
        go)     echo "missing go: https://go.dev/dl/ (version in go.mod)" >&2 ;;
        air)    echo "missing air: go install github.com/air-verse/air@latest" >&2 ;;
        node|npx) echo "missing $tool: https://nodejs.org/ (version in .node-version)" >&2 ;;
      esac
    fi
  done
  docker compose version >/dev/null 2>&1 || { echo "missing docker compose plugin: https://docs.docker.com/compose/install/" >&2; missing=1; }
  [ "$missing" -eq 0 ] || exit 2
}

load_env() {
  [ -f .env.dev.local ] || { cp .env.dev .env.dev.local; echo "created .env.dev.local from .env.dev"; }
  set -a
  # shellcheck disable=SC1091
  source .env.dev
  # shellcheck disable=SC1091
  source .env.dev.local
  set +a
}

rotate_log() {
  local f="$LOG_DIR/$1.log"
  [ -s "$f" ] && mv "$f" "$f.$(date -u +%Y%m%dT%H%M%SZ)" || true
}

start_bg() { # name command...
  local name="$1"; shift
  rotate_log "$name"
  nohup "$@" >>"$LOG_DIR/$name.log" 2>&1 &
  echo "$name $!" >>"$PID_FILE"
  echo "started $name (pid $!) -> tmp/logs/$name.log"
}

wait_for() { # name url
  local i
  for i in $(seq 1 300); do curl -fsS -o /dev/null "$2" 2>/dev/null && { echo "$1 ready"; return 0; }; sleep 1; done
  echo "$1 did not become ready at $2 (see tmp/logs)" >&2; return 1
}

ensure_k3s() { # opt-in: DEV_USE_K3S=1 makes the local k3s kubeconfig readable and the node schedulable
  [ "${DEV_USE_K3S:-0}" = "1" ] || return 0
  local kc=/etc/rancher/k3s/k3s.yaml
  [ -f "$kc" ] || { echo "k3s not installed; run scripts/k3s-install.sh" >&2; return 0; }
  [ -r "$kc" ] || sudo chmod 644 "$kc"
  export KUBECONFIG="$kc"
  if [ "$(kubectl get node -o jsonpath='{.items[0].spec.unschedulable}' 2>/dev/null)" = "true" ]; then
    kubectl uncordon "$(kubectl get node -o jsonpath='{.items[0].metadata.name}')"
  fi
}

migrate_db() { go run ./cmd/server migrate -database "$DATABASE_URL"; }

cmd_up() {
  require_dev_tools; load_env; ensure_k3s
  mkdir -p "$LOG_DIR" tmp/server tmp/worker tmp/agent
  if [ -s "$PID_FILE" ]; then echo "stack already running (tmp/dev.pids); run 'scripts/dev.sh down' first" >&2; exit 1; fi
  : >"$PID_FILE"
  rotate_log compose
  docker compose -f "$COMPOSE_FILE" up -d --wait >>"$LOG_DIR/compose.log" 2>&1
  echo "compose services up (postgres:${DEV_PG_PORT}, redis:${DEV_REDIS_PORT}, grafana:${DEV_GRAFANA_PORT})"
  migrate_db
  if [ "${DEV_WATCH:-1}" = "0" ]; then
    # CI needs one deterministic build before readiness polling starts. Two
    # concurrent cold Air builds used to consume the entire startup deadline.
    go build -trimpath -o tmp/server/server ./cmd/server
    go build -trimpath -o tmp/worker/worker ./cmd/worker
    start_bg server env ENV=development tmp/server/server
    start_bg worker env ENV=development tmp/worker/worker
  else
    command -v air >/dev/null || { echo "air is required when DEV_WATCH=1" >&2; exit 2; }
    start_bg server air -c .air.server.toml
    start_bg worker air -c .air.worker.toml
  fi
  [ "${DEV_START_AGENT:-0}" = "1" ] && start_bg agent air -c .air.agent.toml
  (cd frontend && [ -d node_modules ] || npm ci --no-audit --no-fund)
  start_bg web bash -c "cd frontend && BACKEND_URL=http://127.0.0.1${SERVER_ADDR} exec npx vite --host 0.0.0.0 --port ${DEV_WEB_PORT} --strictPort"
  wait_for server "http://127.0.0.1${SERVER_ADDR}/readyz"
  wait_for web "http://127.0.0.1:${DEV_WEB_PORT}/"
  cmd_status
}

cmd_down() {
  load_env
  if [ -f "$PID_FILE" ]; then
    while read -r name pid; do
      [ -n "${pid:-}" ] && kill -INT "$pid" 2>/dev/null && echo "stopped $name ($pid)" || true
    done <"$PID_FILE"
    sleep 2
    while read -r _ pid; do kill -KILL "$pid" 2>/dev/null || true; done <"$PID_FILE"
    rm -f "$PID_FILE"
  fi
  pkill -f 'air -c \.air\.(server|worker|agent)\.toml' 2>/dev/null || true
  docker compose -f "$COMPOSE_FILE" down
}

cmd_status() {
  load_env
  docker compose -f "$COMPOSE_FILE" ps --format 'table {{.Service}}\t{{.Status}}\t{{.Ports}}' || true
  if [ -f "$PID_FILE" ]; then
    while read -r name pid; do
      if kill -0 "$pid" 2>/dev/null; then echo "$name: running (pid $pid)"; else echo "$name: NOT running"; fi
    done <"$PID_FILE"
  else
    echo "no Air/Vite processes recorded (tmp/dev.pids missing)"
  fi
  cat <<URLS
  dashboard   http://localhost:${DEV_WEB_PORT}  (admin@astronomer.local / \$ASTRONOMER_BOOTSTRAP_PASSWORD)
  api         http://localhost${SERVER_ADDR}/api/v1/   metrics http://localhost${SERVER_METRICS_ADDR}/metrics
  grafana     http://localhost:${DEV_GRAFANA_PORT}   prometheus http://localhost:${DEV_PROMETHEUS_PORT}
URLS
}

cmd_logs() {
  mkdir -p "$LOG_DIR"
  if [ -n "${1:-}" ]; then tail -n 200 -F "$LOG_DIR/$1.log"; else tail -n 50 -F "$LOG_DIR"/*.log; fi
}

trap 'echo; echo "interrupted"; exit 130' INT TERM

case "${1:-}" in
  up) cmd_up ;;
  down) cmd_down ;;
  status) cmd_status ;;
  logs) cmd_logs "${2:-}" ;;
  migrate) load_env; migrate_db ;;
  *) usage; exit 2 ;;
esac
