# Log Schema `logs-v1`

`logs-v1` is the current structured logging contract for `astronomer-go`.

## Goals

- JSON logs only for control-plane processes
- stable deployment identity via `astronomer_instance_id`
- request and audit correlation via `correlation_id` when available
- event-oriented names suitable for forwarding and federation

## Required Fields

- `ts`
- `level`
- `msg`
- `astronomer_instance_id`

## Conditional Fields

- `correlation_id`: present when a request-scoped or audit-scoped operation has
  a correlation identifier
- `event`: stable lowercase event name when the emitting call site provides one
- `request_id`: present on HTTP completion logs and response errors when the
  request ID middleware ran
- `trace_id`: present when an OpenTelemetry span is active
- `actor_id`: present when the request or task payload has an authenticated
  actor identifier
- `actor_auth_method`: present on HTTP completion logs when auth middleware
  identifies the method, such as `jwt`, `api_token`, or `stream_ticket`
- `cluster_id`: present when the route or task payload has a cluster scope
- `operation_id`: present when the route or task payload has an operation scope
- `task_id`: present on worker logs when Asynq exposes a task ID in context

## Current Behavior

- server and worker process loggers use `slog` JSON handlers
- startup loggers are wrapped with `astronomer_instance_id` after the platform
  singleton config has been read or initialized
- HTTP request completion logs emit `event=http_request` and include
  `correlation_id`, `request_id`, `trace_id`, `actor_id`,
  `actor_auth_method`, `cluster_id`, `operation_id`, `method`,
  `route_template`, `status_code`, and `duration_ms` when those fields are
  available
- worker task wrapper logs emit `event=worker_job_started` and
  `event=worker_job_completed`, with `task_id`, `trace_id`, `actor_id`,
  `cluster_id`, and `operation_id` when those identifiers are available
- tunnel lifecycle logs emit `event=agent_connected`,
  `event=agent_disconnected`, and `event=agent_reconnecting`
- successful audit writes emit `event=audit_recorded`
- process lifecycle logs emit events such as `server_starting`,
  `server_stopping`, `server_stopped`, `worker_starting`, `worker_started`,
  `worker_stopping`, `worker_stopped`
- metrics listener lifecycle logs emit events such as
  `server_metrics_listener_started` and `worker_metrics_listener_started`
- request correlation currently flows through middleware and audit records; log
  call sites are still being migrated to emit explicit `event` names

## Compatibility

- additive fields are compatible within `logs-v1`
- removing required fields or changing their meaning requires `logs-v2`
