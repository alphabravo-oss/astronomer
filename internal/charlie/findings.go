package charlie

import (
	"context"
	"fmt"
	"strings"
	"time"
)

type FindingInput struct {
	InstallationID        string
	ResourceType          string
	ResourceID            string
	NormalizedDiagnosis   string
	RecommendedCapability string
	Severity              string
	Mode                  Mode
	Decision              AuthorityDecision
	Title                 string
	Summary               string
	RecommendedAction     string
	RiskImpact            string
	Verification          string
}

type DurableFinding struct {
	ID          string
	Fingerprint string
	Status      string
	RepeatCount int
	UpdatedAt   time.Time
	Notify      bool
}

type FindingStore interface {
	UpsertBlockedFinding(context.Context, FindingInput, FindingRecommendation, string) (DurableFinding, error)
}

type FindingAlert struct {
	FindingID    string `json:"finding_id"`
	Severity     string `json:"severity"`
	Status       string `json:"status"`
	ResourceType string `json:"resource_type"`
	ResourceID   string `json:"resource_id"`
	BlockCode    string `json:"block_code"`
	RepeatCount  int    `json:"repeat_count"`
}

type FindingAlertPublisher interface {
	PublishCharlieFinding(context.Context, FindingAlert) error
}

// BlockedFindingRecorder is the durable product-local boundary used by the
// action guard. Implementations must commit the finding and its resource scope
// before publishing an alert; a model response or in-memory recommendation is
// never sufficient evidence that an operator can act on the finding.
type BlockedFindingRecorder interface {
	RecordBlocked(context.Context, FindingInput) (DurableFinding, error)
}

type FindingService struct {
	store     FindingStore
	publisher FindingAlertPublisher
}

func NewFindingService(store FindingStore, publisher FindingAlertPublisher) (*FindingService, error) {
	if store == nil || publisher == nil {
		return nil, fmt.Errorf("Charlie findings require durable storage and alert publication")
	}
	return &FindingService{store: store, publisher: publisher}, nil
}

// RecordBlocked persists and deduplicates the finding before publishing its
// bounded alert. It never dispatches an action. Inert/disabled decisions do not
// create findings, so a disabled integration remains silent and isolated.
func (s *FindingService) RecordBlocked(ctx context.Context, input FindingInput) (DurableFinding, error) {
	recommendation := BlockedFinding(input.Decision, input.Title, input.Summary, input.RecommendedAction, input.Verification)
	if !recommendation.Actionable {
		return DurableFinding{}, nil
	}
	if !validFindingInput(input) {
		return DurableFinding{}, fmt.Errorf("Charlie finding metadata is invalid")
	}
	fingerprint := FindingDedupeFingerprint(input.InstallationID, input.ResourceType, input.ResourceID, input.NormalizedDiagnosis, input.RecommendedCapability)
	durable, err := s.store.UpsertBlockedFinding(ctx, input, recommendation, fingerprint)
	if err != nil {
		return DurableFinding{}, fmt.Errorf("Charlie finding persistence is unavailable")
	}
	if durable.Notify {
		alert := FindingAlert{
			FindingID: durable.ID, Severity: input.Severity, Status: durable.Status,
			ResourceType: input.ResourceType, ResourceID: input.ResourceID,
			BlockCode: string(input.Decision.Code), RepeatCount: durable.RepeatCount,
		}
		if err := s.publisher.PublishCharlieFinding(ctx, alert); err != nil {
			return durable, fmt.Errorf("Charlie finding alert publication is unavailable")
		}
	}
	return durable, nil
}

func validFindingInput(input FindingInput) bool {
	if input.Mode != ModeReadOnly && input.Mode != ModeApproval && input.Mode != ModeAuto {
		return false
	}
	if input.Severity != "info" && input.Severity != "low" && input.Severity != "medium" && input.Severity != "warning" && input.Severity != "high" && input.Severity != "critical" {
		return false
	}
	for _, value := range []string{input.InstallationID, input.ResourceType, input.ResourceID, input.NormalizedDiagnosis, input.RecommendedCapability} {
		if strings.TrimSpace(value) == "" || len(value) > 255 {
			return false
		}
	}
	return len(input.Title) <= 256 && len(input.Summary) <= 2048 && len(input.RecommendedAction) <= 256 && len(input.RiskImpact) <= 1024 && len(input.Verification) <= 1024
}
