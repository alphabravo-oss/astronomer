package handler

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// --- Request types ---

// CreateChannelRequest represents the request body for creating a notification channel.
// openapi:request AlertChannelRequest
type CreateChannelRequest struct {
	Name          string          `json:"name" validate:"required"`
	ChannelType   string          `json:"channel_type"`
	Type          string          `json:"type"`
	Configuration json.RawMessage `json:"configuration"`
	Config        json.RawMessage `json:"config"`
	Enabled       bool            `json:"enabled"`
}

// CreateAlertRuleRequest represents the request body for creating an alert rule.
//
// Sprint 072 added the RuleKind / Anomaly* fields. Submitting a body
// with RuleKind="anomaly" requires the operator to supply Metric +
// AnomalyStddev + AnomalyWindowSeconds; the other anomaly fields
// default sensibly (stddev=3, direction=above, min_samples=50).
// openapi:request AlertRuleRequest
type CreateAlertRuleRequest struct {
	Name                   string            `json:"name" validate:"required"`
	Description            string            `json:"description"`
	ClusterID              *uuid.UUID        `json:"cluster_id"`
	RuleType               string            `json:"rule_type"`
	Type                   string            `json:"type"`
	Configuration          json.RawMessage   `json:"configuration"`
	Query                  string            `json:"query"`
	Threshold              *float64          `json:"threshold"`
	Duration               string            `json:"duration"`
	Labels                 map[string]string `json:"labels"`
	Annotations            map[string]string `json:"annotations"`
	NotificationChannelIDs []string          `json:"notificationChannelIds"`
	Severity               string            `json:"severity"`
	Enabled                bool              `json:"enabled"`
	CooldownMinutes        int32             `json:"cooldown_minutes"`
	// Sprint 072 anomaly-rule fields. RuleKind="anomaly" engages the
	// rolling-baseline evaluator path; everything else (including the
	// default RuleKind="threshold") uses the existing static-threshold
	// path unchanged.
	RuleKind             string   `json:"rule_kind"`
	Metric               string   `json:"metric"`
	AnomalyStddev        *float64 `json:"anomaly_stddev"`
	AnomalyWindowSeconds *int32   `json:"anomaly_window_seconds"`
	AnomalyMinSamples    *int32   `json:"anomaly_min_samples"`
	AnomalyDirection     string   `json:"anomaly_direction"`
}

// CreateSilenceRequest represents the request body for creating an alert silence.
// openapi:request AlertSilenceRequest
type CreateSilenceRequest struct {
	RuleID    *uuid.UUID        `json:"rule_id"`
	ClusterID *uuid.UUID        `json:"cluster_id"`
	Reason    string            `json:"reason" validate:"required"`
	StartsAt  time.Time         `json:"starts_at"`
	EndsAt    time.Time         `json:"ends_at"`
	Duration  string            `json:"duration"`
	Matchers  map[string]string `json:"matchers"`
}

// InhibitionMatcher is one label matcher in a source/target matcher set.
// is_regex selects full-string regex matching (anchored) instead of exact
// string equality. Mirrors the Alertmanager inhibit_rule matcher shape.
type InhibitionMatcher struct {
	Label   string `json:"label"`
	Value   string `json:"value"`
	IsRegex bool   `json:"is_regex"`
}

// InhibitionRequest is the create/update body for an inhibition rule (P-03).
// openapi:request InhibitionRequest
type InhibitionRequest struct {
	Name           string              `json:"name" validate:"required"`
	SourceMatchers []InhibitionMatcher `json:"source_matchers"`
	TargetMatchers []InhibitionMatcher `json:"target_matchers"`
	EqualLabels    []string            `json:"equal_labels"`
	Enabled        *bool               `json:"enabled"`
}
