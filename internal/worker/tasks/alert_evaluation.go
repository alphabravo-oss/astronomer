package tasks

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/hibiken/asynq"
)

// AlertEvaluationPayload contains parameters for alert evaluation.
type AlertEvaluationPayload struct {
	RuleID string `json:"rule_id,omitempty"` // empty = evaluate all rules
}

// NewAlertEvaluationTask creates a new alert evaluation task.
func NewAlertEvaluationTask(payload AlertEvaluationPayload) (*asynq.Task, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal alert evaluation payload: %w", err)
	}
	return asynq.NewTask("alert:evaluate", data), nil
}

// HandleAlertEvaluation evaluates all enabled alert rules against current metrics.
func HandleAlertEvaluation(ctx context.Context, t *asynq.Task) error {
	var p AlertEvaluationPayload
	if len(t.Payload()) > 0 {
		if err := json.Unmarshal(t.Payload(), &p); err != nil {
			return fmt.Errorf("unmarshal alert evaluation payload: %w", err)
		}
	}

	if p.RuleID != "" {
		slog.InfoContext(ctx, "evaluating alert rule", "rule_id", p.RuleID)
	} else {
		slog.InfoContext(ctx, "evaluating all alert rules")
	}

	// TODO: Fetch enabled alert rules from DB, evaluate conditions against metrics,
	// fire notifications for triggered alerts.

	slog.InfoContext(ctx, "alert evaluation complete")
	return nil
}
