package handler

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// TestNotificationChannelResponseRedactsSecrets proves a channel read never
// leaks the delivery credential (Slack webhook URL, PagerDuty routing key,
// generic webhook token) while still reporting non-secret config and the fact
// that a secret is set.
func TestNotificationChannelResponseRedactsSecrets(t *testing.T) {
	cfg := map[string]any{
		"webhook_url":  "https://hooks.slack.com/services/T00/B00/SECRETSECRET",
		"routing_key":  "R0UT1NGK3Y",
		"api_token":    "xoxb-123-secret",
		"channel":      "#alerts",
		"empty_secret": "",
	}
	raw, _ := json.Marshal(cfg)
	resp := notificationChannelResponse(sqlc.NotificationChannel{ChannelType: "slack", Configuration: raw})
	body, _ := json.Marshal(resp)
	s := string(body)

	for _, secret := range []string{"SECRETSECRET", "R0UT1NGK3Y", "xoxb-123-secret"} {
		if strings.Contains(s, secret) {
			t.Fatalf("channel response leaked secret %q: %s", secret, s)
		}
	}
	// Non-secret config must survive so the UI can still render it.
	if !strings.Contains(s, "#alerts") {
		t.Fatalf("non-secret config was dropped: %s", s)
	}
	// A set secret is masked (marker present), an empty one stays empty.
	out := resp["config"].(map[string]any)
	if out["webhook_url"] != "[redacted]" {
		t.Fatalf("webhook_url should be masked, got %v", out["webhook_url"])
	}
	if out["empty_secret"] != "" {
		t.Fatalf("empty secret should stay empty, got %v", out["empty_secret"])
	}
}
