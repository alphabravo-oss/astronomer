#!/usr/bin/env bash

# Docker-backed qualification scripts normally run directly on a host, where
# loopback is both the safest bind address and the correct client endpoint.
# Local CI runs the official Actions runner inside a container while mounting
# the host Docker socket. In that topology, publish only on the Docker bridge
# gateway and connect through Local CI's host.docker.internal mapping.
if [[ -z "${DOCKER_TEST_CONNECT_HOST:-}" ]]; then
  if [[ "${LOCAL_CI_LOCAL:-false}" == "true" ]]; then
    DOCKER_TEST_CONNECT_HOST="host.docker.internal"
  else
    DOCKER_TEST_CONNECT_HOST="127.0.0.1"
  fi
fi

if [[ -z "${DOCKER_TEST_BIND_HOST:-}" ]]; then
  if [[ "${LOCAL_CI_LOCAL:-false}" == "true" ]]; then
    DOCKER_TEST_BIND_HOST="$(getent ahostsv4 host.docker.internal 2>/dev/null | awk 'NR == 1 {print $1}')"
    if [[ -z "$DOCKER_TEST_BIND_HOST" ]]; then
      echo "docker-test-endpoint: cannot resolve Local CI's host.docker.internal gateway" >&2
      return 2
    fi
  else
    DOCKER_TEST_BIND_HOST="127.0.0.1"
  fi
fi

export DOCKER_TEST_BIND_HOST DOCKER_TEST_CONNECT_HOST

docker_test_host_path() {
  local path="${1:?path is required}"
  if [[ "${LOCAL_CI_LOCAL:-false}" != "true" ]]; then
    printf '%s\n' "$path"
    return 0
  fi

  local runner_root="/home/runner/_work"
  local host_root
  host_root="$(docker inspect "$HOSTNAME" --format '{{range .Mounts}}{{if eq .Destination "/home/runner/_work"}}{{.Source}}{{end}}{{end}}')"
  if [[ -z "$host_root" || "$path" != "$runner_root"/* ]]; then
    echo "docker-test-endpoint: cannot translate runner path for host Docker: $path" >&2
    return 2
  fi
  printf '%s/%s\n' "${host_root%/}" "${path#"$runner_root"/}"
}
