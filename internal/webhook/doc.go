// Package webhook implements outbound webhook subscriptions: operators
// register a (url, hmac-secret, event-filter-globs, optional template)
// tuple, the platform's in-memory event bus (internal/events) is tapped,
// matching events are written to webhook_deliveries, and a periodic
// worker drains them via signed HTTP POST with exponential backoff.
//
// Architecture overview:
//
//	internal/events.Bus      ─┐
//	                           ├─►  Tap (filter by glob)  ─►  InsertWebhookDelivery
//	audit.Record (via bridge) ─┘
//
//	               Worker tick (every 15s)
//	                      │
//	                      ▼
//	               ListPendingWebhookDeliveries
//	                      │
//	                      ▼
//	               Sender.Send (HMAC sign, POST)
//	                      │
//	                      ├──► 2xx → MarkDelivered
//	                      ├──► 4xx → MarkDropped (permanent — operator fixes URL)
//	                      └──► 5xx / timeout → MarkFailed + reschedule
//
// The webhook secret is Fernet-encrypted under auth.Encryptor. Decryption
// happens inside Sender right before signing; plaintext NEVER reaches a
// log line or a database column.
//
// Migration 048 defines the persistent state.
//
// # Recipes
//
// Slack incoming-webhook target — POST to a Slack URL with their
// expected {text, blocks} shape. event_filters: ["audit.*"],
// payload_template:
//
//	{
//	  "text": "{{ .event_name }}: {{ .detail.message }}",
//	  "blocks": [
//	    {"type":"section","text":{"type":"mrkdwn","text":"*{{ .event_name }}*\n`{{ .resource_type }}/{{ .resource_id }}`"}}
//	  ]
//	}
//
// PagerDuty Events v2 — POST to https://events.pagerduty.com/v2/enqueue
// with a routing_key in extra_headers. event_filters:
// ["alert.fired", "cluster.decommission.*"], payload_template:
//
//	{
//	  "routing_key": "REPLACE_ME",
//	  "event_action": "trigger",
//	  "payload": {
//	    "summary": "{{ .event_name }} — {{ .detail.message }}",
//	    "source": "astronomer",
//	    "severity": "error"
//	  }
//	}
//
// Verifying signatures (Python receiver-side):
//
//	import hmac, hashlib
//	def verify(body_bytes, sig_header, secret):
//	    expected = "sha256=" + hmac.new(secret.encode(), body_bytes,
//	                                    hashlib.sha256).hexdigest()
//	    return hmac.compare_digest(expected, sig_header)
//
// Verifying signatures (Go receiver-side):
//
//	mac := hmac.New(sha256.New, []byte(secret))
//	mac.Write(body)
//	want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
//	return hmac.Equal([]byte(want), []byte(r.Header.Get("X-Astronomer-Signature")))
package webhook
