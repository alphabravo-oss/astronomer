# ADR: Go platform tooling and runtime conventions

- **Status:** Accepted
- **Date:** 2026-09-17
- **Target:** Astronomer v1.1.0
- **Owners:** Backend platform, security, and SRE
- **Review trigger:** A production base-image family change, migration-engine
  replacement, telemetry transport expansion, or lint-policy change
- **Machine contracts:** [`go.mod`](../../../go.mod),
  [`.golangci.yml`](../../../.golangci.yml), and
  [`scripts/check-go-lint.sh`](../../../scripts/check-go-lint.sh)

## Context

The implementation already converged on standard-library logging,
golang-migrate, OTLP/HTTP tracing, Alpine runtime images, and golangci-lint v2.
Earlier engineering guides also mentioned zerolog, Goose, distroless images,
OTLP/gRPC, and Air. The release needs an explicit choice so contributors do not
create parallel operational paths.

## Decision

### Logging

Application processes use `log/slog` with structured JSON handlers and stable
event/correlation attributes. Zerolog is not added. The standard library is
sufficient for the current throughput and keeps shared packages independent of
a third-party logging API.

### Database migrations

`github.com/golang-migrate/migrate/v4` and ordered SQL files under
`internal/db/migrations` are authoritative. The dedicated migrator image,
startup jobs, local commands, rollback runbook, and migration safety checks all
use that format. Goose is not introduced as a second migration ledger.

### Telemetry transport

Traces export through OTLP/HTTP with W3C Trace Context and Baggage propagation.
HTTP works through the same common proxy and egress controls as other HTTPS
traffic and avoids a second production protocol requirement. An empty endpoint
is a supported no-export configuration; an explicit zero sample ratio exports
no sampled traces. OTLP/gRPC may be added only when a measured environment
requires it and must share the same resource identity, shutdown, redaction, and
zero-sampling semantics.

### Lint policy

golangci-lint v2 runs the `standard` set from `.golangci.yml` with uncapped
findings. The executable version is pinned by `scripts/check-go-lint.sh`, which
is used by `make lint`, the enterprise verifier, and active CI. Generated sqlc
output is excluded because its generator owns it. Any disabled check requires
an inline rationale and review trigger; a growing baseline or new-issues-only
mode is not accepted.

### Local reload workflow

The supported backend development topology is the checked-in Compose stack.
After a Go or migration change, `make dev-reload` rebuilds and replaces the
migrator, server, and worker from the working tree; health checks and service
dependencies gate the replacement. This is a deliberate, reproducible reload
workflow rather than a filesystem watcher. Air is not a repository dependency:
its watcher timing and locally installed version would create a second startup
path that the release images never exercise. Vite remains the frontend's
purpose-built hot-reload server.

### Runtime images

Service build and runtime bases are immutable digest-pinned Alpine images.
Production stages contain only the binary and explicitly installed runtime
packages, run as non-root where the workload permits, and are hardened by the
chart's read-only filesystem, capability, seccomp, and network policy. Mutable
`apk upgrade` is forbidden.

Distroless is not the current default. The shell, migration, DR, health-check,
certificate, and timezone requirements are explicit and tested with Alpine,
while using two base families would enlarge qualification and incident-debug
surface. This is not permission to add a shell or package manager to a service
image without a runtime requirement.

## Consequences

- Logging and migration code have one API and one operational ledger.
- Collectors must expose OTLP/HTTP; gRPC-only collector configurations need an
  HTTP receiver or a future superseding decision.
- Local Go edits require one explicit rebuild command rather than automatic hot
  reload, but that command exercises the production Dockerfiles.
- Alpine CVE response includes updating reviewed digests and rebuilding all
  affected images; tags alone are never a production input.

## Rejected alternatives

- **zerolog:** no measured need justifies a second logging abstraction.
- **Goose:** a second migration engine could disagree about version and dirty
  state with the deployed migrator.
- **OTLP/gRPC by default:** adds another egress/proxy protocol without current
  evidence of a benefit.
- **Air as a required tool:** creates an unqualified local-only process path and
  another pinned tool lifecycle.
- **distroless immediately:** would split the runtime family while shell/DR and
  health tooling still have explicit package needs.

## Review date

Review by **2027-09-17**, or sooner if the runtime image or collector threat
model changes.
