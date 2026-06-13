# Metrics Schema `metrics-v1`

`metrics-v1` is the current Prometheus contract for control-plane metrics
exposed by `astronomer-go`.

## Goals

- stable `astronomer_*` namespace
- low-cardinality labels suitable for federation
- deployment-level partitioning via `astronomer_instance_id`
- dedicated scrape listeners for server and worker processes

## Common Rules

- every metric includes `astronomer_instance_id`
- route labels use route templates, not raw paths
- status labels use status class (`2xx`, `4xx`, `5xx`), not exact status codes
- `cluster_id` labels are bounded by registered-cluster count
- no request IDs, correlation IDs, or resource names may appear in metric labels

## Server HTTP Metrics

- `astronomer_http_requests_total{astronomer_instance_id,method,route_template,status_class}`
- `astronomer_http_request_duration_seconds_bucket{astronomer_instance_id,method,route_template,status_class}`
- `astronomer_http_in_flight_requests{astronomer_instance_id,method,route_template}`

## Worker Metrics

- `astronomer_worker_jobs_total{astronomer_instance_id,job,status}`
- `astronomer_worker_job_duration_seconds_bucket{astronomer_instance_id,job,status}`
- `astronomer_worker_queue_depth{astronomer_instance_id,queue,state}`
- `astronomer_worker_queue_latency_seconds{astronomer_instance_id,queue}`
- `astronomer_worker_leader_held{astronomer_instance_id,job}`

## Database Metrics

- `astronomer_db_pool_acquired_connections{astronomer_instance_id}`
- `astronomer_db_pool_idle_connections{astronomer_instance_id}`
- `astronomer_db_pool_total_connections{astronomer_instance_id}`
- `astronomer_db_pool_max_connections{astronomer_instance_id}`
- `astronomer_db_pool_acquire_count_total{astronomer_instance_id}`
- `astronomer_db_pool_empty_acquire_count_total{astronomer_instance_id}`
- `astronomer_db_pool_canceled_acquire_count_total{astronomer_instance_id}`
- `astronomer_db_pool_acquire_duration_seconds_total{astronomer_instance_id}`
- `astronomer_db_query_duration_seconds_bucket{astronomer_instance_id,operation,status}`
- `astronomer_db_deadlocks_total{astronomer_instance_id}`
- `astronomer_db_longest_transaction_seconds{astronomer_instance_id}`
- `astronomer_task_outbox_rows{astronomer_instance_id,status}`
- `astronomer_task_outbox_oldest_due_seconds{astronomer_instance_id,status}`

Chart-shipped Prometheus rules also consume Kubernetes/Postgres exporter
metrics when available:

- `kube_job_status_start_time{job_name=~".*-migrate.*"}` and `kube_job_status_completion_time{job_name=~".*-migrate.*"}` for migration duration.
- `kube_job_status_failed{job_name=~".*-migrate.*"}` for migration failures.
- `kube_job_status_completion_time{job_name=~".*-management-backup-.*"}` and `kube_job_status_succeeded{job_name=~".*-management-backup-.*"}` for backup age.
- `kube_job_status_failed{job_name=~".*-management-backup-.*"}` for backup failures.
- `pg_replication_lag_seconds` for optional external Postgres replication lag alerting.

## Agent / Tunnel Metrics

- `astronomer_agent_connections{astronomer_instance_id,cluster_id}`
- `astronomer_agent_last_seen_seconds{astronomer_instance_id,cluster_id}`
- `astronomer_agent_messages_total{astronomer_instance_id,cluster_id,direction}`
- `astronomer_agent_state_updates_received_total{astronomer_instance_id,kind}`
- `astronomer_agent_state_updates_handled_total{astronomer_instance_id,outcome,kind}`
- `astronomer_tunnel_state_updates_received_total{astronomer_instance_id,kind}`
- `astronomer_tunnel_state_updates_handled_total{astronomer_instance_id,outcome,kind}`

## Async Drop Metrics

- `astronomer_dropped_events_total{astronomer_instance_id,component,reason}`

Current bounded label values include:

- `component=events_bus`, `reason=slow_subscriber`
- `component=agent_tunnel_send`, `reason=channel_full`
- `component=agent_exec_resize`, `reason=channel_full`
- `component=tunnel_stream_route`, `reason=channel_full`

## Exposure

- server metrics listener: configured by `SERVER_METRICS_ADDR`
- worker metrics listener: configured by `WORKER_METRICS_ADDR`
- Helm chart renders a `ServiceMonitor` when `metrics.serviceMonitor.enabled=true`

## Compatibility

- additive metrics or additive labels require documentation updates but do not
  break `metrics-v1`
- renaming metrics, removing metrics, or changing label meaning requires a new
  contract version (`metrics-v2`)
