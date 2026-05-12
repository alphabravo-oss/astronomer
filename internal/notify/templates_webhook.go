// Webhook built-in template registrations.
//
// Pre-migration, webhook bodies were either:
//
//  1. The verbatim event-JSON (`json.Marshal(Event)`) when the
//     subscription had no PayloadTemplate, OR
//  2. The operator-supplied PayloadTemplate, which is a free-form
//     text/template rendered against the event data bag.
//
// We KEEP both paths. Migration 059 adds a third, optional layer: a
// per-event-family default body the dispatcher consults when the
// subscription itself has no PayloadTemplate but an operator has
// installed an override at the notification_templates table. The
// resolution order on a per-delivery basis becomes:
//
//   subscription.PayloadTemplate  →  notify override  →  Event JSON
//
// So a row with no subscription template + no override stays
// byte-identical to the pre-migration default (canonicalEventJSON).
//
// The set of registered keys mirrors the event families the platform
// emits today; see internal/events/bus.go and the audit/alert/cluster
// publish call sites. Keys not in the registry can still be sent to
// by a subscription — those just don't get an admin-editable default.

package notify

// canonicalEventJSON is the format json.Marshal(Event) currently
// produces. The body template uses text/template syntax over the
// flattened event map (see internal/webhook/sender.go
// eventToTemplateData). This text is informative for operators
// editing an override — the actual default JSON output for a row
// with no override is produced by json.Marshal, NOT by rendering
// this template. The defaults snapshot test asserts that
// json.Marshal(sampleEvent) is byte-identical to its pre-refactor
// output to lock that invariant in.
const canonicalEventJSON = `{
  "event_name": "{{ .event_name }}",
  "event_id": "{{ .event_id }}",
  "timestamp": "{{ .timestamp }}",
  "actor_user_id": "{{ .actor_user_id }}",
  "resource_id": "{{ .resource_id }}",
  "resource_type": "{{ .resource_type }}",
  "delivery_id": "{{ .delivery_id }}",
  "detail": {{ .detail | toJSON }}
}`

// Webhook template keys. The convention is "webhook.<family>" so the
// admin UI can group related families under one section.
const (
	KeyWebhookAuditEvent             = "webhook.audit.event"
	KeyWebhookAlertFired             = "webhook.alert.fired"
	KeyWebhookAlertResolved          = "webhook.alert.resolved"
	KeyWebhookClusterConnected       = "webhook.cluster.connected"
	KeyWebhookClusterDisconnected    = "webhook.cluster.disconnected"
	KeyWebhookClusterStatusChanged   = "webhook.cluster.status_changed"
	KeyWebhookClusterCreated         = "webhook.cluster.created"
	KeyWebhookClusterUpdated         = "webhook.cluster.updated"
	KeyWebhookClusterDeleted         = "webhook.cluster.deleted"
	KeyWebhookClusterDecommissioned  = "webhook.cluster.decommissioned"
)

// commonWebhookVars are the keys every webhook event carries (the
// eventToTemplateData flattening in internal/webhook/sender.go).
var commonWebhookVars = []VariableSpec{
	{Name: "event_name", Description: "Dotted event identifier — e.g. cluster.connected", Required: true, Example: "cluster.connected"},
	{Name: "event_id", Description: "Stable per-event UUID for de-duplication on the receiver", Required: false, Example: "1d3f…"},
	{Name: "timestamp", Description: "RFC 3339 UTC timestamp the event was published", Required: true, Example: "2026-01-01T12:34:00Z"},
	{Name: "actor_user_id", Description: "User who triggered the event (empty for system-emitted events)", Required: false, Example: ""},
	{Name: "resource_id", Description: "ID of the resource the event is about (cluster id, audit row id, …)", Required: false, Example: ""},
	{Name: "resource_type", Description: "Type tag of the resource", Required: false, Example: "cluster"},
	{Name: "delivery_id", Description: "Per-delivery UUID stamped by the dispatcher (for retries / dedup)", Required: false, Example: ""},
	{Name: "detail", Description: "Event-specific payload map (see Description for the family)", Required: false, Example: "{}"},
}

func webhookVars(extra ...VariableSpec) []VariableSpec {
	out := make([]VariableSpec, 0, len(commonWebhookVars)+len(extra))
	out = append(out, commonWebhookVars...)
	out = append(out, extra...)
	return out
}

func registerWebhookFamily(key, description string, extraVars ...VariableSpec) {
	registerBuiltin(TemplateDef{
		Key:         key,
		Channel:     ChannelWebhook,
		Subject:     "", // webhooks don't carry a subject line; the URL identifies the receiver
		Body:        canonicalEventJSON,
		BodyFormat:  BodyFormatJSON,
		Description: description,
		Variables:   webhookVars(extraVars...),
	})
}

func init() {
	registerWebhookFamily(KeyWebhookAuditEvent,
		"Fired for every audit row (action prefix audit.*). The detail map mirrors the audit log JSON column.")

	registerWebhookFamily(KeyWebhookAlertFired,
		"Fired when an alert rule transitions into the firing state.",
		VariableSpec{Name: "detail.alert_name", Description: "Rule name", Required: true, Example: "etcd-quorum-loss"},
		VariableSpec{Name: "detail.severity", Description: "critical | warning | info", Required: true, Example: "critical"},
	)

	registerWebhookFamily(KeyWebhookAlertResolved,
		"Fired when an alert resolves (either auto-resolve or an admin acknowledged it).")

	registerWebhookFamily(KeyWebhookClusterConnected,
		"Emitted when an agent dial-in succeeds for a managed cluster.")

	registerWebhookFamily(KeyWebhookClusterDisconnected,
		"Emitted when an agent for a managed cluster drops or fails its heartbeat budget.")

	registerWebhookFamily(KeyWebhookClusterStatusChanged,
		"Emitted on cluster lifecycle status transitions (active → suspended, etc.).")

	registerWebhookFamily(KeyWebhookClusterCreated,
		"Emitted when a new managed cluster is registered.")

	registerWebhookFamily(KeyWebhookClusterUpdated,
		"Emitted when a cluster's mutable config (display name, labels, kubeconfig) changes.")

	registerWebhookFamily(KeyWebhookClusterDeleted,
		"Emitted when a cluster row is soft-deleted (before the decommission worker runs).")

	registerWebhookFamily(KeyWebhookClusterDecommissioned,
		"Emitted on each phase of the cluster decommission worker (cleanup_managed_side, argocd_secret_orphan, …). Detail carries the phase + outcome.",
		VariableSpec{Name: "detail.phase", Description: "Phase tag", Required: true, Example: "cleanup_managed_side"},
		VariableSpec{Name: "detail.outcome", Description: "success | failed", Required: true, Example: "success"},
	)
}
