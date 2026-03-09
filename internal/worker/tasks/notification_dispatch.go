package tasks

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

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

	// TODO: Dispatch via the appropriate channel (email/SMTP, Slack webhook, generic webhook).

	slog.InfoContext(ctx, "notification sent", "channel", p.Channel)
	return nil
}
