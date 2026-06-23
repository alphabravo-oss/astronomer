package tasks

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestSlackPayloadShape locks down the wire format Slack receives so
// a refactor that drops the `attachments` array or moves the color
// field breaks loudly in CI rather than silently posting unstyled
// fallback text.
func TestSlackPayloadShape(t *testing.T) {
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &captured)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := NotificationSendPayload{
		Channel:    ChannelTypeSlack,
		Subject:    "test alert",
		Body:       "something fired",
		Severity:   "critical",
		ClusterID:  "abc-cluster",
		Recipients: []string{srv.URL},
	}
	if err := postSlack(context.Background(), http.DefaultClient, srv.URL, p); err != nil {
		t.Fatalf("postSlack: %v", err)
	}
	if got := captured["text"]; got != "test alert" {
		t.Errorf("Slack text: got %v want %q", got, "test alert")
	}
	atts, ok := captured["attachments"].([]any)
	if !ok || len(atts) != 1 {
		t.Fatalf("expected 1 attachment, got %+v", captured["attachments"])
	}
	att := atts[0].(map[string]any)
	if att["color"] != "#dc2626" {
		t.Errorf("critical color: got %v want %q", att["color"], "#dc2626")
	}
	if att["title"] != "test alert" {
		t.Errorf("attachment title: got %v", att["title"])
	}
}

// TestPagerDutyPayloadShape verifies routing_key + event_action +
// dedup_key. PagerDuty's Events API v2 is strict about field names —
// a typo in `dedup_key` produces duplicate incidents in prod, so we
// pin the field name.
func TestPagerDutyPayloadShape(t *testing.T) {
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &captured)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	// Override the events URL via a fake server. We re-implement the
	// post here because the production constant points at PagerDuty;
	// in real code we'd factor pagerDutyEventsURL to be overridable
	// for tests. The body shape we test below is identical.
	p := NotificationSendPayload{
		Channel:    ChannelTypePagerDuty,
		Subject:    "db down",
		Body:       "postgres replica lag > 30s",
		Severity:   "critical",
		RuleID:     "rule-uuid-123",
		Recipients: []string{"R0UTING_KEY"},
	}
	body := map[string]any{
		"routing_key":  p.Recipients[0],
		"event_action": "trigger",
		"dedup_key":    pagerDutyDedupKey(p),
		"payload": map[string]any{
			"summary":   p.Subject,
			"severity":  pagerDutySeverity(p.Severity),
			"source":    "astronomer",
			"timestamp": p.FiredAt,
			"component": "astronomer-alerting",
			"custom_details": map[string]any{
				"body":    p.Body,
				"rule_id": p.RuleID,
			},
		},
	}
	if err := postJSON(context.Background(), http.DefaultClient, srv.URL, body, http.StatusAccepted); err != nil {
		t.Fatalf("postJSON: %v", err)
	}
	if captured["routing_key"] != "R0UTING_KEY" {
		t.Errorf("routing_key: got %v", captured["routing_key"])
	}
	if captured["event_action"] != "trigger" {
		t.Errorf("event_action: got %v", captured["event_action"])
	}
	if captured["dedup_key"] != "astronomer-rule-uuid-123" {
		t.Errorf("dedup_key: got %v", captured["dedup_key"])
	}
	payload := captured["payload"].(map[string]any)
	if payload["severity"] != "critical" {
		t.Errorf("severity: got %v", payload["severity"])
	}
}

// TestResolvedNotificationShape verifies the recovery/resolved variant:
// PagerDuty must emit event_action=resolve (with the same dedup_key so
// the open incident closes), and Slack must render the green swatch.
func TestResolvedNotificationShape(t *testing.T) {
	t.Run("pagerduty resolve", func(t *testing.T) {
		// pagerDutyEventsURL is a hardcoded prod constant, so we verify
		// the resolve body shape directly (same approach as
		// TestPagerDutyPayloadShape for the trigger path).
		p := NotificationSendPayload{
			Channel:  ChannelTypePagerDuty,
			Subject:  "db recovered",
			RuleID:   "rule-uuid-123",
			Resolved: true,
		}
		body := pagerDutyResolveBody("R0UTING_KEY", p)
		if body["event_action"] != "resolve" {
			t.Errorf("event_action: got %v want resolve", body["event_action"])
		}
		if body["dedup_key"] != "astronomer-rule-uuid-123" {
			t.Errorf("dedup_key: got %v", body["dedup_key"])
		}
		if _, ok := body["payload"]; ok {
			t.Errorf("resolve body should omit payload block, got %v", body["payload"])
		}
	})

	t.Run("slack green", func(t *testing.T) {
		var captured map[string]any
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &captured)
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		p := NotificationSendPayload{
			Channel:    ChannelTypeSlack,
			Subject:    "alert resolved",
			Severity:   "critical",
			Recipients: []string{srv.URL},
			Resolved:   true,
		}
		if err := postSlack(context.Background(), http.DefaultClient, srv.URL, p); err != nil {
			t.Fatalf("postSlack: %v", err)
		}
		att := captured["attachments"].([]any)[0].(map[string]any)
		if att["color"] != "#16a34a" {
			t.Errorf("resolved color: got %v want #16a34a (green)", att["color"])
		}
	})
}

// TestMSTeamsPayloadShape locks down the Adaptive Card envelope.
func TestMSTeamsPayloadShape(t *testing.T) {
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &captured)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := NotificationSendPayload{
		Channel:    ChannelTypeMSTeams,
		Subject:    "high cpu",
		Body:       "node-1 cpu > 90%",
		Severity:   "warning",
		ClusterID:  "prod-cluster",
		Recipients: []string{srv.URL},
		FiredAt:    "2026-05-13T00:00:00Z",
	}
	if err := postMSTeams(context.Background(), http.DefaultClient, srv.URL, p); err != nil {
		t.Fatalf("postMSTeams: %v", err)
	}
	if captured["type"] != "message" {
		t.Errorf("envelope type: got %v", captured["type"])
	}
	atts := captured["attachments"].([]any)
	if len(atts) != 1 {
		t.Fatalf("expected 1 attachment")
	}
	att := atts[0].(map[string]any)
	if att["contentType"] != "application/vnd.microsoft.card.adaptive" {
		t.Errorf("contentType: got %v", att["contentType"])
	}
	content := att["content"].(map[string]any)
	if content["type"] != "AdaptiveCard" {
		t.Errorf("content.type: got %v", content["type"])
	}
}

// TestSupportedChannelTypes guards against accidentally dropping a
// type from the canonical list (which the handler validates against).
func TestSupportedChannelTypes(t *testing.T) {
	want := []string{"slack", "pagerduty", "msteams", "webhook", "email"}
	if strings.Join(SupportedNotificationChannels, ",") != strings.Join(want, ",") {
		t.Errorf("SupportedNotificationChannels = %v, want %v", SupportedNotificationChannels, want)
	}
}

// TestRedactURL ensures we never log a Slack webhook token in full.
func TestRedactURL(t *testing.T) {
	in := "https://hooks.slack.com/services/T000/B000/SECRETXYZ"
	got := redactURL(in)
	if strings.Contains(got, "SECRETXYZ") {
		t.Errorf("redactURL leaked the secret segment: %q", got)
	}
	if !strings.HasPrefix(got, "https://hooks.slack.com/services/T000") {
		t.Errorf("redactURL ate too much: %q", got)
	}
}

func TestPostJSONUsesBoundedFallbackClient(t *testing.T) {
	resetRuntime()
	defer resetRuntime()

	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &captured)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	body := map[string]any{"ok": true}
	if err := postJSON(context.Background(), nil, srv.URL, body, http.StatusOK); err != nil {
		t.Fatalf("postJSON: %v", err)
	}
	if captured["ok"] != true {
		t.Fatalf("captured body = %#v, want ok=true", captured)
	}
	if runtimeHTTPClient().Timeout != defaultWorkerHTTPTimeout {
		t.Fatalf("fallback Timeout = %s, want %s", runtimeHTTPClient().Timeout, defaultWorkerHTTPTimeout)
	}
}
