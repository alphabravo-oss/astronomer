// Package siem implements the external-SIEM forwarder pipeline added in
// migration 055.
//
// Operators configure one or more sinks (syslog UDP/TCP/TLS, Splunk HEC,
// or generic NDJSON-over-HTTPS) and the platform ships every matching
// audit row + domain event to those sinks in batched, format-correct
// payloads. The pipeline is distinct from outbound webhooks
// (sprint 048): SIEM forwarders use industry-standard wire formats
// (RFC 5424 / RFC 3164 syslog, ArcSight CEF, NDJSON) and follow
// guaranteed-delivery semantics — when the local bounded queue fills,
// the oldest rows are dropped and operator-visible dropped_total
// metrics tick. The operator's SIEM is the source of truth, not the
// astronomer-go process.
//
// Components in this package:
//
//   - format.go       — wire-format renderers (RFC 5424, RFC 3164, CEF, NDJSON)
//   - transport.go    — per-protocol senders (syslog UDP/TCP/TLS, HEC, HTTPS)
//   - bus_tap.go      — events.Bus → siem_forward_queue INSERTer
//   - metrics.go      — Prometheus counters for dispatched / dropped events
//
// The worker-side dispatcher that drains the queue and ships batches lives
// in internal/worker/tasks/siem_dispatch.go. The admin CRUD handler lives
// in internal/handler/siem.go.
package siem
