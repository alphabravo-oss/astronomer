# 0011 — zerolog via pkg/logging, with a log/slog bridge

- Status: accepted
- Date: 2026-10-20
- Guide: Technology Selection Guide ch.05 (structured JSON logs via zerolog, thin `pkg/logging` wrapper, correlation ids, one request-summary line)
- Owner decision: D-I

## Context

Every binary logged through `log/slog` with a redacting JSON handler in
`internal/observability` (~280 import sites). The guide requires zerolog
behind a thin wrapper, JSON on stderr in production, a console format for
local development, correlation ids adopted from `traceparent` /
`X-Correlation-ID`, and exactly one request-summary line per request.

## Decision

- `pkg/logging` owns construction: `New(cfg)` (stderr) and
  `NewWithWriter(w, cfg)` take `Level` (zerolog names, default `info`),
  `Format` (`json` default, `text` = `zerolog.ConsoleWriter`) and
  `Component`. Output passes through a `zerolog.MultiLevelWriter` behind a
  redacting writer that re-encodes every line: sensitive keys (the former
  `internal/observability` rule set) become `[REDACTED]` with their whole
  subtree, every string is scrubbed for credential shapes, and
  `zerolog.ErrorMarshalFunc` reduces `pgconn.PgError` / Kubernetes
  `StatusError` to their safe classification. The same rules serve the OTLP
  span exporter (`logging.SensitiveKey`, `logging.Scrub`).
- `SlogHandler(zerolog.Logger)` is a hand-written bridge (slog groups become
  dotted keys; structured values without a field policy stay omitted).
  `SetDefault` installs the process logger and routes the slog default (and
  the stdlib `log` package) into it, so each binary has one encoder. Existing
  slog call sites keep compiling and are migrated package by package.
- `WithCorrelationID` / `CorrelationID` / `Ctx(ctx, base)` carry the request
  correlation id; a hook adds `correlation_id`/`trace_id` to slog `*Context`
  calls. The HTTP `RequestID` middleware adopts the `traceparent` trace-id,
  else `X-Correlation-ID`, else `X-Request-ID`, else a new UUID, and echoes it
  in `X-Correlation-ID` and `X-Request-ID`.
- `RequestLogger(logger)` emits one `event=http_request` line per request at
  info after the response: `route` (chi pattern), `method`, `status`,
  `duration_ms`, `correlation_id`, `trace_id` (when a span is active),
  `principal` (+ `auth_method`) when authenticated. No paths, queries, headers
  or bodies.
- Migrated now: `internal/server` (+ `middleware`), `internal/charlie`, and
  logger construction in every `cmd/*` binary. `LOG_LEVEL` / `LOG_FORMAT`
  configure the server and worker through `internal/config`; the agent,
  `keyrotate` and `server migrate` read the same variables via
  `logging.ConfigFromEnv`. The chart pins `LOG_FORMAT=json`.

## Consequences

- The message key is `message` (zerolog), not `msg`; levels are lowercase.
- `observability.NewJSONHandler` and its redaction helpers are deleted.
- Remaining slog packages (handlers, worker tasks, agent) log through the
  bridge until they are migrated; their output is already zerolog JSON.
- Proof: `internal/server/request_log_integration_test.go` (integration tag)
  drives the production router over a migrated Postgres.
