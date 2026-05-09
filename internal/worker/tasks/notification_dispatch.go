package tasks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/hibiken/asynq"
)

// NotificationSendPayload contains parameters for sending a notification.
type NotificationSendPayload struct {
	Channel string `json:"channel"` // e.g. "email", "slack", "webhook"
	Subject string `json:"subject"`
	Body    string `json:"body"`
	// Recipients is a list of destination addresses/URLs depending on channel.
	Recipients []string `json:"recipients"`
}

// NewNotificationSendTask creates a new notification dispatch task.
func NewNotificationSendTask(payload NotificationSendPayload) (*asynq.Task, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal notification send payload: %w", err)
	}
	return asynq.NewTask("notification:send", data, asynq.MaxRetry(3), asynq.Queue("critical")), nil
}

// HandleNotificationSend dispatches notifications via the configured channel.
func HandleNotificationSend(ctx context.Context, t *asynq.Task) error {
	var p NotificationSendPayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return fmt.Errorf("unmarshal notification send payload: %w", err)
	}

	if p.Channel == "" {
		return fmt.Errorf("channel is required")
	}
	if len(p.Recipients) == 0 {
		return fmt.Errorf("at least one recipient is required")
	}

	slog.InfoContext(ctx, "sending notification",
		"channel", p.Channel,
		"subject", p.Subject,
		"recipient_count", len(p.Recipients),
	)

	if runtimeDeps.HTTPClient == nil {
		runtimeDeps.HTTPClient = http.DefaultClient
	}
	switch strings.ToLower(p.Channel) {
	case "slack", "webhook":
		for _, recipient := range p.Recipients {
			if err := postWebhook(ctx, recipient, p); err != nil {
				return err
			}
		}
	case "email":
		runtimeLogger().InfoContext(ctx, "email notification queued without SMTP backend",
			"recipient_count", len(p.Recipients),
			"subject", p.Subject,
		)
	default:
		return fmt.Errorf("unsupported notification channel %q", p.Channel)
	}

	slog.InfoContext(ctx, "notification sent", "channel", p.Channel)
	return nil
}

func postWebhook(ctx context.Context, url string, payload NotificationSendPayload) error {
	body, err := json.Marshal(map[string]any{
		"subject": payload.Subject,
		"body":    payload.Body,
		"text":    payload.Body,
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := runtimeDeps.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		return fmt.Errorf("notification webhook returned status %d", resp.StatusCode)
	}
	return nil
}
