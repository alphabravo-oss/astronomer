package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
)

// MaxPayloadBytes caps the body the sender will ship in one POST. A
// payload larger than this is marked dropped (no retry) on the delivery
// row — almost certainly an operator-written template gone wild rather
// than a legitimate event-shape change.
const MaxPayloadBytes = 1 << 20 // 1 MiB

// MaxResponseBodyBytes is the cap on captured receiver responses. The
// admin "recent deliveries" view shows this; keeping it small avoids
// shipping a hostile receiver's 100 MB HTML error page into Postgres.
const MaxResponseBodyBytes = 4 * 1024 // 4 KiB

// SignatureHeader is the HTTP header the receiver checks. The format
// is `sha256=<hex>` so a future hash-rotation can be encoded in the
// prefix without breaking compatibility.
const SignatureHeader = "X-Astronomer-Signature"

// EventNameHeader / EventIDHeader / DeliveryIDHeader are convenience
// headers the receiver can use without parsing the body. The signature
// only covers the body — the headers are informational.
const (
	EventNameHeader  = "X-Astronomer-Event"
	EventIDHeader    = "X-Astronomer-Event-Id"
	DeliveryIDHeader = "X-Astronomer-Delivery-Id"
)

// BackoffSchedule is the per-attempt wait. attempt index 0 means
// "first retry", so a row with attempts=2 returns 10m (the third entry).
// After the schedule is exhausted the dispatcher promotes the row to
// status='dropped'.
var BackoffSchedule = []time.Duration{
	30 * time.Second,
	2 * time.Minute,
	10 * time.Minute,
	1 * time.Hour,
	6 * time.Hour,
}

// NextBackoff returns the wait before the (attempts+1)-th attempt.
// attempts is the number of already-completed attempts (so 1 after the
// first failure). Out-of-range indices return the last slot.
func NextBackoff(attempts int) time.Duration {
	if attempts <= 0 {
		return BackoffSchedule[0]
	}
	if attempts > len(BackoffSchedule) {
		return BackoffSchedule[len(BackoffSchedule)-1]
	}
	return BackoffSchedule[attempts-1]
}

// Subscription is the narrow view Sender needs. The dispatcher decrypts
// SecretEncrypted into Secret before calling Send.
type Subscription struct {
	ID              string
	Name            string
	URL             string
	Secret          string
	PayloadTemplate string
	ExtraHeaders    map[string]string
	TimeoutSeconds  int
	// MaxRetries is the retry budget the dispatcher consults to decide
	// when to promote a row to status='dropped'. The Sender itself
	// doesn't read this field — it lives on the struct so the dispatcher
	// doesn't have to thread it separately alongside Subscription.
	MaxRetries int
}

// OverrideLookup is the bridge from the Sender to the notify package
// (migration 059). When set, the Sender consults it after the
// per-subscription template and before the JSON-marshal default. A
// miss (ok=false) means "no operator override for this event family
// — fall through to the next layer".
//
// Keys are of the form "webhook.<family>"; see
// internal/notify/templates_webhook.go for the canonical list.
type OverrideLookup func(ctx context.Context, key string) (body string, ok bool)

// Event is the JSON-shaped payload the bus delivers + the dispatcher
// hands to Send. Field names match the JSON keys that go to receivers
// when no template is configured.
type Event struct {
	EventName    string          `json:"event_name"`
	EventID      string          `json:"event_id,omitempty"`
	Timestamp    time.Time       `json:"timestamp"`
	Detail       json.RawMessage `json:"detail,omitempty"`
	ActorUserID  string          `json:"actor_user_id,omitempty"`
	ResourceID   string          `json:"resource_id,omitempty"`
	ResourceType string          `json:"resource_type,omitempty"`
	DeliveryID   string          `json:"delivery_id,omitempty"`
}

// Outcome describes a single attempt. The dispatcher consumes this to
// decide what to write back to the row.
type Outcome struct {
	Status       int    // HTTP status (0 if the request never completed)
	ResponseBody string // truncated to MaxResponseBodyBytes
	Err          error  // non-nil for transport-level failures (DNS, timeout)
}

// IsRetryable reports whether the outcome is worth retrying. 4xx is
// permanent (operator must fix the URL or the receiver's bug); 5xx
// and transport failures are transient.
func (o Outcome) IsRetryable() bool {
	if o.Err != nil {
		return true
	}
	if o.Status >= 200 && o.Status < 300 {
		return false
	}
	if o.Status >= 400 && o.Status < 500 {
		return false
	}
	return true
}

// IsSuccess reports whether the outcome counts as delivered.
func (o Outcome) IsSuccess() bool {
	return o.Err == nil && o.Status >= 200 && o.Status < 300
}

// HTTPDoer is the subset of *http.Client the Sender depends on. Tests
// substitute a recorded fake.
type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Sender is the per-delivery HTTP machinery. Safe for concurrent use.
type Sender struct {
	client    HTTPDoer
	now       func() time.Time
	overrides OverrideLookup
}

// NewSender wires the outbound HTTP client. When client is nil, a
// dial-guarded SafeClient (30s) is used so operator-configured webhook
// URLs cannot rebind to loopback/private/metadata (SEC-R02). Callers
// that need private on-prem receivers must pass an explicit
// httpclient.SafeClientAllowPrivate (or DisableGuardForTest in tests).
func NewSender(client HTTPDoer) *Sender {
	if client == nil {
		client = httpclient.SafeClient(30 * time.Second)
	}
	return &Sender{client: client, now: time.Now}
}

// SetOverrideLookup wires the notify.Resolve bridge. Optional —
// when nil the Sender falls back to the pre-migration behaviour
// (subscription template, else JSON-marshal). The dispatcher sets
// this once at startup; safe to read concurrently because the
// closure body itself does the DB lookup.
func (s *Sender) SetOverrideLookup(o OverrideLookup) {
	s.overrides = o
}

// SetNow is the test seam for stamping a predictable Date / Timestamp.
func (s *Sender) SetNow(now func() time.Time) {
	if now != nil {
		s.now = now
	}
}

// Send applies the template (if any), computes the HMAC, and POSTs to
// sub.URL with a per-call timeout derived from sub.TimeoutSeconds. The
// returned payloadSize is the size of the bytes actually shipped — the
// dispatcher uses it to stamp payload_size on the delivery row.
func (s *Sender) Send(ctx context.Context, sub Subscription, event Event) (Outcome, int, error) {
	body, err := s.buildBody(ctx, sub, event)
	if err != nil {
		return Outcome{Err: err}, 0, err
	}
	if len(body) > MaxPayloadBytes {
		return Outcome{Err: fmt.Errorf("payload exceeds %d bytes", MaxPayloadBytes)}, len(body), fmt.Errorf("payload too large")
	}

	timeout := time.Duration(sub.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, sub.URL, bytes.NewReader(body))
	if err != nil {
		return Outcome{Err: fmt.Errorf("build request: %w", err)}, len(body), err
	}

	// Default Content-Type. The operator can override via extra_headers
	// (e.g. application/x-www-form-urlencoded for some quirky relays);
	// the override pattern is "if extra_headers carries the key, use it
	// verbatim; otherwise use the default".
	if _, ok := sub.ExtraHeaders["Content-Type"]; !ok {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set(SignatureHeader, "sha256="+computeHMAC(sub.Secret, body))
	req.Header.Set(EventNameHeader, event.EventName)
	if event.EventID != "" {
		req.Header.Set(EventIDHeader, event.EventID)
	}
	if event.DeliveryID != "" {
		req.Header.Set(DeliveryIDHeader, event.DeliveryID)
	}
	for k, v := range sub.ExtraHeaders {
		// Skip the signature header — operators MUST NOT be able to
		// fake the receiver's verification step.
		if strings.EqualFold(k, SignatureHeader) {
			continue
		}
		req.Header.Set(k, v)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return Outcome{Err: err}, len(body), nil
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	// Read up to MaxResponseBodyBytes + 1 so the truncation flag is
	// honest about whether we dropped content.
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, int64(MaxResponseBodyBytes+1)))
	truncated := false
	if len(respBody) > MaxResponseBodyBytes {
		respBody = respBody[:MaxResponseBodyBytes]
		truncated = true
	}
	out := Outcome{Status: resp.StatusCode, ResponseBody: string(respBody)}
	if truncated {
		// Tag the captured body so the admin view shows operators
		// that more data existed at the receiver.
		out.ResponseBody += "\n…[truncated]"
	}
	return out, len(body), nil
}

// buildBody applies the template (if any) and returns the bytes that
// will be hashed + shipped. Resolution precedence (highest first):
//
//  1. sub.PayloadTemplate — per-subscription override set by the
//     operator at subscription create/update time.
//  2. notify override on key webhook.<family> — operator-tunable
//     default-per-event-family (migration 059).
//  3. json.Marshal(event) — the pre-migration default; byte-
//     identical when neither 1 nor 2 is present.
func (s *Sender) buildBody(ctx context.Context, sub Subscription, event Event) ([]byte, error) {
	if strings.TrimSpace(sub.PayloadTemplate) == "" {
		// Level 2: notify override.
		if s.overrides != nil {
			key := overrideKeyForEvent(event.EventName)
			if key != "" {
				if body, ok := s.overrides(ctx, key); ok && strings.TrimSpace(body) != "" {
					data, err := eventToTemplateData(event)
					if err != nil {
						return nil, err
					}
					rendered, err := Render(body, data)
					if err != nil {
						return nil, err
					}
					if rendered != nil {
						return rendered, nil
					}
				}
			}
		}
		// Level 3: ship the event verbatim as JSON.
		return json.Marshal(event)
	}
	data, err := eventToTemplateData(event)
	if err != nil {
		return nil, err
	}
	rendered, err := Render(sub.PayloadTemplate, data)
	if err != nil {
		return nil, err
	}
	if rendered == nil {
		// Template rendered to empty — shouldn't happen because the
		// validator rejects empty + non-empty templates the same way,
		// but fall back to the raw event so a misconfigured row
		// doesn't ship a literal empty body.
		return json.Marshal(event)
	}
	return rendered, nil
}

// overrideKeyForEvent maps an event_name (e.g. "audit.user.login")
// to the notify-registry key family ("webhook.audit.event"). The
// mapping is intentionally coarse: every audit.* event shares the
// same override, every cluster.* event family has its own. Returns
// "" when no override key applies (in which case the dispatcher
// falls through to the JSON-marshal default).
func overrideKeyForEvent(eventName string) string {
	switch {
	case strings.HasPrefix(eventName, "audit."):
		return "webhook.audit.event"
	case eventName == "alert.fired" || strings.HasSuffix(eventName, ".alert.fired"):
		return "webhook.alert.fired"
	case eventName == "alert.resolved" || strings.HasSuffix(eventName, ".alert.resolved"):
		return "webhook.alert.resolved"
	case strings.HasPrefix(eventName, "cluster.decommission"):
		return "webhook.cluster.decommissioned"
	case eventName == "cluster.connected":
		return "webhook.cluster.connected"
	case eventName == "cluster.disconnected":
		return "webhook.cluster.disconnected"
	case eventName == "cluster.status_changed":
		return "webhook.cluster.status_changed"
	case eventName == "cluster.created":
		return "webhook.cluster.created"
	case eventName == "cluster.updated":
		return "webhook.cluster.updated"
	case eventName == "cluster.deleted":
		return "webhook.cluster.deleted"
	}
	return ""
}

// eventToTemplateData converts an Event into the map the template
// engine consumes. We use lowercase keys to match the JSON-on-the-wire
// shape so {{ .event_name }} in a template "just works" without the
// operator needing to know Go field naming.
func eventToTemplateData(e Event) (map[string]any, error) {
	out := map[string]any{
		"event_name":    e.EventName,
		"event_id":      e.EventID,
		"timestamp":     e.Timestamp.UTC().Format(time.RFC3339),
		"actor_user_id": e.ActorUserID,
		"resource_id":   e.ResourceID,
		"resource_type": e.ResourceType,
		"delivery_id":   e.DeliveryID,
	}
	if len(e.Detail) > 0 {
		var detail any
		if err := json.Unmarshal(e.Detail, &detail); err != nil {
			return nil, fmt.Errorf("decode detail: %w", err)
		}
		out["detail"] = detail
	}
	return out, nil
}

// computeHMAC returns hex(HMAC-SHA256(secret, body)). The receiver
// recomputes this exact transform and uses hmac.compare_digest
// (constant-time) to verify; see the README recipe.
func computeHMAC(secret string, body []byte) string {
	h := hmac.New(sha256.New, []byte(secret))
	h.Write(body)
	return hex.EncodeToString(h.Sum(nil))
}

// ErrPayloadTooLarge is exported for callers that want to discriminate
// between transport failures and an oversized template render.
var ErrPayloadTooLarge = errors.New("webhook payload exceeds 1 MiB cap")
