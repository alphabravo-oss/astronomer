#!/usr/bin/env bash
set -euo pipefail

SQLC_VERSION="${SQLC_VERSION:-v1.31.1}"

go run "github.com/sqlc-dev/sqlc/cmd/sqlc@${SQLC_VERSION}" generate

git diff --exit-code -- \
  sqlc.yaml \
  internal/db/queries \
  internal/db/sqlc
