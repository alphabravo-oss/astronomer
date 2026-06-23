// Notification dispatcher.
//
// Owns the `notification:send` asynq task: alert-evaluator enqueues
// one task per (rule fire, bound channel), this file shapes the
// payload for that channel and POSTs it.
//
// Channel types we format natively:
//
//   slack       — Slack Incoming Webhook (blocks + severity colour)
//   pagerduty   — Events API v2 (routing_key flows through configuration.routing_key)
//   msteams     — MS Teams Workflow / Power Automate webhook (Adaptive Card)
//   webhook     — generic JSON POST with the unstructured envelope
//   email       — handled by the SMTP path (worker/tasks/email_dispatch.go);
//                 we no-op here so the asynq task succeeds without double-send
//
// Each formatter produces a fresh `http.Request` body shaped exactly
// like the receiving service wants. Slack/MS Teams accept any 200; we
// treat 4xx/5xx as a task-level error so asynq retries (MaxRetry=3).
// PagerDuty's Events API v2 returns 202 on accept, which we treat as
// success.
//
// The receiver address (Slack webhook URL, PagerDuty routing key,
// Teams workflow URL, generic webhook URL) lives in the channel's
// `configuration` JSONB; alert_evaluation.notificationRecipients
// extracts it into the Recipients slice on the payload before
// enqueueing.

package tasks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/hibiken/asynq"

	"github.com/alphabravocompany/astronomer-go/internal/strutil"
)

// Recognized channel-type strings. Comparison is case-insensitive
// (operators set these via the dashboard, where casing varies).
const (
	ChannelTypeSlack     = "slack"
	ChannelTypePagerDuty = "pagerduty"
	ChannelTypeMSTeams   = "msteams"
	ChannelTypeWebhook   = "webhook"
	ChannelTypeEmail     = "email"
)

// SupportedNotificationChannels is the canonical list the alerting
// handler validates against before persisting a new channel.
// Exported so the handler doesn't have to duplicate the constants.
var SupportedNotificationChannels = []string{
	ChannelTypeSlack,
	ChannelTypePagerDuty,
	ChannelTypeMSTeams,
	ChannelTypeWebhook,
	ChannelTypeEmail,
}

// NotificationSendPayload is the asynq task body. Severity is
// optional but recommended — Slack + MS Teams render with a colour
// swatch based on it, PagerDuty maps it onto the event severity field.
type NotificationSendPayload struct {
	Channel    string   `json:"channel"`              // canonical type string (slack|pagerduty|msteams|webhook|email)
	Subject    string   `json:"subject"`              // short title — alert rule name
	Body       string   `json:"body"`                 // long-form text — what fired, links, etc.
	Recipients []string `json:"recipients"`           // destination URL(s) / routing key(s); shape depends on channel
	Severity   string   `json:"severity,omitempty"`   // critical|warning|info — drives styling
	ClusterID  string   `json:"cluster_id,omitempty"` // optional source cluster, surfaced as a field
	RuleID     string   `json:"rule_id,omitempty"`    // optional alert-rule UUID
	FiredAt    string   `json:"fired_at,omitempty"`   // RFC3339; defaults to time.Now() inside the formatter
	// Resolved marks this as a recovery/resolved notification (the alert
	// transitioned firing->resolved). Formatters render a green
	// "resolved" variant and PagerDuty emits event_action=resolve so the
	// open incident auto-closes instead of paging again.
	Resolved bool `json:"resolved,omitempty"`
}

// NewNotificationSendTask builds an asynq task. MaxRetry=3 with the
// default exponential backoff so a flaky upstream (Slack throttling,
// PagerDuty 503) gets a few automatic retries before we give up.
func NewNotificationSendTask(payload NotificationSendPayload) (*asynq.Task, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal notification send payload: %w", err)
	}
	return asynq.NewTask("notification:send", data, asynq.MaxRetry(3), asynq.Queue("critical")), nil
}

// HandleNotificationSend is the asynq HandleFunc — dispatches to the
// right per-channel formatter.
func HandleNotificationSend(ctx context.Context, t *asynq.Task) error {
	var p NotificationSendPayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return fmt.Errorf("unmarshal notification send payload: %w", err)
	}
	if p.Channel == "" {
		return fmt.Errorf("channel is required")
	}
	if len(p.Recipients) == 0 && strings.ToLower(p.Channel) != ChannelTypeEmail {
		return fmt.Errorf("at least one recipient is required for channel %q", p.Channel)
	}
	if p.FiredAt == "" {
		p.FiredAt = time.Now().UTC().Format(time.RFC3339)
	}
	client := runtimeDeps.HTTPClient
	if client == nil {
		client = runtimeHTTPClient()
	}

	slog.InfoContext(ctx, "sending notification",
		"channel", p.Channel,
		"subject", p.Subject,
		"severity", p.Severity,
		"recipient_count", len(p.Recipients),
	)

	switch strings.ToLower(p.Channel) {
	case ChannelTypeSlack:
		for _, url := range p.Recipients {
			if err := postSlack(ctx, client, url, p); err != nil {
				return err
			}
		}
	case ChannelTypePagerDuty:
		for _, routingKey := range p.Recipients {
			if err := postPagerDuty(ctx, client, routingKey, p); err != nil {
				return err
			}
		}
	case ChannelTypeMSTeams:
		for _, url := range p.Recipients {
			if err := postMSTeams(ctx, client, url, p); err != nil {
				return err
			}
		}
	case ChannelTypeWebhook:
		for _, url := range p.Recipients {
			if err := postGenericWebhook(ctx, client, url, p); err != nil {
				return err
			}
		}
	case ChannelTypeEmail:
		// Email goes through internal/worker/tasks/email_dispatch.go
		// driven by the email_messages table. The alert-evaluator
		// writes a row there separately; this dispatcher just
		// acknowledges the notification:send task so the asynq queue
		// stays clean.
		runtimeLogger().InfoContext(ctx, "email channel handled by smtp dispatcher", "subject", p.Subject)
	default:
		return fmt.Errorf("unsupported notification channel %q", p.Channel)
	}

	slog.InfoContext(ctx, "notification sent", "channel", p.Channel, "subject", p.Subject)
	return nil
}

// ---------------------------------------------------------------------
// Slack
// ---------------------------------------------------------------------
//
// Slack Incoming Webhook accepts a JSON body with a `text` fallback
// and optional `attachments`/`blocks`. We use `attachments` with the
// `color` bar because it renders identically on free + Enterprise
// Grid and on mobile — `blocks` is shinier but gets ignored if the
// webhook target is configured in legacy mode.

func postSlack(ctx context.Context, client *http.Client, webhookURL string, p NotificationSendPayload) error {
	color := slackSeverityColor(p.Severity)
	if p.Resolved {
		color = "#16a34a" // green-600 — recovered
	}
	body := map[string]any{
		"text": p.Subject,
		"attachments": []map[string]any{{
			"color":  color,
			"title":  p.Subject,
			"text":   p.Body,
			"footer": "Astronomer",
			"ts":     time.Now().Unix(),
			"fields": slackFields(p),
		}},
	}
	return postJSON(ctx, client, webhookURL, body, http.StatusOK)
}

func slackSeverityColor(sev string) string {
	switch strings.ToLower(strings.TrimSpace(sev)) {
	case "critical", "error", "fatal":
		return "#dc2626" // red-600
	case "warning", "warn":
		return "#f59e0b" // amber-500
	case "info":
		return "#3b82f6" // blue-500
	default:
		return "#6b7280" // gray-500
	}
}

func slackFields(p NotificationSendPayload) []map[string]any {
	fields := []map[string]any{}
	if p.Severity != "" {
		fields = append(fields, map[string]any{"title": "Severity", "value": p.Severity, "short": true})
	}
	if p.ClusterID != "" {
		fields = append(fields, map[string]any{"title": "Cluster", "value": p.ClusterID, "short": true})
	}
	return fields
}

// ---------------------------------------------------------------------
// PagerDuty
// ---------------------------------------------------------------------
//
// PagerDuty Events API v2 is a single endpoint regardless of routing
// key — the routing_key in the JSON body selects which service receives
// the alert. We always emit `event_action: trigger`; resolve/auto-
// resolve flows aren't yet wired through the alert-rule schema and
// would belong in a follow-up.

const pagerDutyEventsURL = "https://events.pagerduty.com/v2/enqueue"

func postPagerDuty(ctx context.Context, client *http.Client, routingKey string, p NotificationSendPayload) error {
	// On resolve, send event_action=resolve with the same dedup_key so
	// PagerDuty closes the incident this rule opened. The Events API v2
	// ignores the payload block for resolve, so we only send the keys.
	if p.Resolved {
		return postJSON(ctx, client, pagerDutyEventsURL, pagerDutyResolveBody(routingKey, p), http.StatusAccepted)
	}
	body := map[string]any{
		"routing_key":  routingKey,
		"event_action": "trigger",
		"dedup_key":    pagerDutyDedupKey(p),
		"payload": map[string]any{
			"summary":   strutil.FirstNonBlank(p.Subject, p.Body, "Astronomer alert"),
			"source":    strutil.FirstNonBlank(p.ClusterID, "astronomer"),
			"severity":  pagerDutySeverity(p.Severity),
			"timestamp": p.FiredAt,
			"component": "astronomer-alerting",
			"custom_details": map[string]any{
				"body":    p.Body,
				"rule_id": p.RuleID,
			},
		},
	}
	// PagerDuty returns 202 on accept; treat anything < 300 as success.
	return postJSON(ctx, client, pagerDutyEventsURL, body, http.StatusAccepted)
}

// pagerDutyResolveBody is the Events API v2 resolve payload. The
// payload block is omitted because PagerDuty ignores it on resolve; the
// dedup_key must match the trigger so the right incident closes.
func pagerDutyResolveBody(routingKey string, p NotificationSendPayload) map[string]any {
	return map[string]any{
		"routing_key":  routingKey,
		"event_action": "resolve",
		"dedup_key":    pagerDutyDedupKey(p),
	}
}

func pagerDutySeverity(sev string) string {
	switch strings.ToLower(strings.TrimSpace(sev)) {
	case "critical", "error", "fatal":
		return "critical"
	case "warning", "warn":
		return "warning"
	case "info":
		return "info"
	default:
		return "error"
	}
}

// Deterministic dedup so the same rule firing repeatedly while open
// updates one incident instead of paging the on-call human N times.
// Falls back to subject hash when rule_id is unknown.
func pagerDutyDedupKey(p NotificationSendPayload) string {
	if p.RuleID != "" {
		return "astronomer-" + p.RuleID
	}
	return "astronomer-" + p.Subject
}

// ---------------------------------------------------------------------
// MS Teams
// ---------------------------------------------------------------------
//
// Microsoft deprecated the classic Office 365 Connector webhook in
// early 2025. The current path is a Power Automate / Teams Workflow
// "Post to channel when a webhook request is received" trigger, which
// accepts an Adaptive Card payload. We emit a minimal but legible card
// — title + body + facts — so it renders cleanly in Teams desktop +
// mobile without depending on a specific card schema version.

func postMSTeams(ctx context.Context, client *http.Client, webhookURL string, p NotificationSendPayload) error {
	facts := []map[string]any{}
	if p.Severity != "" {
		facts = append(facts, map[string]any{"title": "Severity:", "value": p.Severity})
	}
	if p.ClusterID != "" {
		facts = append(facts, map[string]any{"title": "Cluster:", "value": p.ClusterID})
	}
	facts = append(facts, map[string]any{"title": "Fired at:", "value": p.FiredAt})

	body := map[string]any{
		"type": "message",
		"attachments": []map[string]any{{
			"contentType": "application/vnd.microsoft.card.adaptive",
			"content": map[string]any{
				"$schema": "http://adaptivecards.io/schemas/adaptive-card.json",
				"type":    "AdaptiveCard",
				"version": "1.4",
				"body": []map[string]any{
					{
						"type":   "TextBlock",
						"size":   "Medium",
						"weight": "Bolder",
						"text":   p.Subject,
						"color":  msTeamsResolveColor(p),
						"wrap":   true,
					},
					{
						"type":    "TextBlock",
						"text":    p.Body,
						"wrap":    true,
						"spacing": "Small",
					},
					{
						"type":  "FactSet",
						"facts": facts,
					},
				},
			},
		}},
	}
	return postJSON(ctx, client, webhookURL, body, http.StatusOK)
}

// msTeamsResolveColor renders the title in green ("Good") for a
// resolved notification, otherwise falls back to the severity color.
func msTeamsResolveColor(p NotificationSendPayload) string {
	if p.Resolved {
		return "Good"
	}
	return msTeamsSeverityColor(p.Severity)
}

func msTeamsSeverityColor(sev string) string {
	switch strings.ToLower(strings.TrimSpace(sev)) {
	case "critical", "error", "fatal":
		return "Attention"
	case "warning", "warn":
		return "Warning"
	case "info":
		return "Accent"
	default:
		return "Default"
	}
}

// ---------------------------------------------------------------------
// Generic webhook
// ---------------------------------------------------------------------

func postGenericWebhook(ctx context.Context, client *http.Client, url string, p NotificationSendPayload) error {
	body := map[string]any{
		"subject":    p.Subject,
		"body":       p.Body,
		"text":       p.Body, // legacy compat: callers that read `text`
		"severity":   p.Severity,
		"cluster_id": p.ClusterID,
		"rule_id":    p.RuleID,
		"fired_at":   p.FiredAt,
		"resolved":   p.Resolved,
	}
	return postJSON(ctx, client, url, body, http.StatusOK)
}

// ---------------------------------------------------------------------
// Shared helpers
// ---------------------------------------------------------------------

// postJSON POSTs body as JSON and returns an error if the response
// status isn't acceptable. acceptStatus = the success code we want to
// see; any 2xx is accepted, but the caller hints at the canonical one
// so logs are clearer when an upstream silently returns 200 instead
// of 202 etc.
func postJSON(ctx context.Context, client *http.Client, url string, body any, acceptStatus int) error {
	if client == nil {
		client = runtimeHTTPClient()
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}
	postCtx, cancel := context.WithTimeout(ctx, notificationPostTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(postCtx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("post %s: %w", redactURL(url), err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode >= http.StatusBadRequest {
		return fmt.Errorf("%s returned status %d (expected 2xx, e.g. %d)", redactURL(url), resp.StatusCode, acceptStatus)
	}
	return nil
}

const notificationPostTimeout = 10 * time.Second

// redactURL hides everything after the third path segment of a webhook
// URL so accidental log leaks don't expose the secret token (Slack
// webhooks, MS Teams workflow URLs, generic webhook signing keys
// embedded in the URL all carry secrets in the path). PagerDuty uses
// a fixed endpoint with the secret in the body, so it's safe to log
// in full — the redact below is a no-op for the canonical URL.
func redactURL(u string) string {
	if !strings.HasPrefix(u, "http") {
		return u
	}
	parts := strings.SplitN(u, "/", 6) // scheme:, "", host, p1, p2, rest
	if len(parts) < 6 {
		return u
	}
	parts[5] = "<redacted>"
	return strings.Join(parts, "/")
}
