#!/usr/bin/env bash
# Go static-analysis gate: runs the pinned golangci-lint against .golangci.yml.
#
# Pinned the same way sqlc is (scripts/check-sqlc-generated.sh, Makefile
# SQLC_VERSION): `go run <module>@<version>`, so CI, `make lint` and a laptop
# all execute the same linter build with no separate install step and no
# "works on my machine" version skew. The first run builds the binary into the
# Go build cache (~2 min cold, seconds warm).
#
# .golangci.yml is the single source of truth for which checks run and why any
# check is off — do not add flags here that change the check set.
set -euo pipefail

GOLANGCI_LINT_VERSION="${GOLANGCI_LINT_VERSION:-v2.12.2}"

go run "github.com/golangci/golangci-lint/v2/cmd/golangci-lint@${GOLANGCI_LINT_VERSION}" run ./...
