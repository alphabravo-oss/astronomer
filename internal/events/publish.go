package events

// Envelope contract for `<resource>.<verb>` events:
//
//   - `type` is `<resource>.<verb>`; the verb is `changed` for all DB-backed
//     domains. Reserved special verbs (never used with PublishChanged):
//     `metrics`, `status_changed`, `heartbeat`, `step`, `phase`, `ping`.
//   - `data` is snake_case on the wire.
//   - No object bodies: payloads are metadata-only (`cluster_id`, `id`, plus
//     small discriminators such as `kind` or `scope`). This avoids secret
//     leakage and server-transform drift; full payloads stay limited to the
//     existing metrics/status shapes.
//   - Cluster-scoped events MUST carry `cluster_id`: the SSE stream drops
//     events without it fail-closed for restricted users (SEC-R07), so a
//     publisher forgetting it silently breaks liveness for exactly the users
//     least able to debug it.

// PublishChanged emits the canonical `<resource>.changed` event on bus with a
// metadata-only payload of `{cluster_id, id, ...extra}`. Empty clusterID or
// entityID fields are omitted (unscoped events are superuser-only via the
// fail-closed SEC-R07 drop). Reserved keys `cluster_id` and `id` cannot be
// overridden through extra. Nil-safe on bus; fire-and-forget by design.
func PublishChanged(bus *Bus, resource, clusterID, entityID string, extra map[string]any) {
	if bus == nil || resource == "" {
		return
	}
	data := make(map[string]any, len(extra)+2)
	for k, v := range extra {
		data[k] = v
	}
	if clusterID != "" {
		data["cluster_id"] = clusterID
	}
	if entityID != "" {
		data["id"] = entityID
	}
	bus.Publish(Type(resource+".changed"), data)
}
