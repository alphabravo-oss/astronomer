package model

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

type AmountType string

const (
	AmountCount   AmountType = "count"
	AmountPercent AmountType = "percent"
)

// Amount is a closed count-or-percentage union used for availability,
// failures, and canary sizing. Percentage is an integer in [0,100].
type Amount struct {
	Type  AmountType `json:"type"`
	Value uint32     `json:"value"`
}

func (a Amount) validate(field string, requirePositive bool) error {
	var collector validationCollector
	switch a.Type {
	case AmountCount:
	case AmountPercent:
		if a.Value > 100 {
			collector.add(field+".value", CodeInvalid, "percentage must not exceed 100")
		}
	default:
		collector.add(field+".type", CodeUnsupported, "must be count or percent")
	}
	if requirePositive && a.Value == 0 {
		collector.add(field+".value", CodeInvalid, "must be greater than zero")
	}
	return collector.err()
}

type StrategyType string

const (
	StrategyAllAtOnce   StrategyType = "all_at_once"
	StrategyRolling     StrategyType = "rolling"
	StrategyCanary      StrategyType = "canary"
	StrategyPartitioned StrategyType = "partitioned"
)

type FailureAction string

const (
	FailurePause    FailureAction = "pause"
	FailureAbort    FailureAction = "abort"
	FailureRollback FailureAction = "rollback"
)

type CanarySpec struct {
	Size                Amount      `json:"size"`
	ClusterIDs          []uuid.UUID `json:"cluster_ids,omitempty"`
	ApprovalAfterCanary bool        `json:"approval_after_canary"`
	Soak                Duration    `json:"soak"`
}

type Partition struct {
	Name             string    `json:"name"`
	Selector         Placement `json:"selector"`
	ApprovalRequired bool      `json:"approval_required"`
	Soak             Duration  `json:"soak"`
}

// RolloutStrategy freezes every scheduling decision except the rollout's
// selected cluster snapshot. MaxConcurrent is required for every strategy and
// remains a hard global cap for all-at-once.
type RolloutStrategy struct {
	Type                      StrategyType  `json:"type"`
	MaxConcurrent             uint32        `json:"max_concurrent"`
	MaxUnavailable            Amount        `json:"max_unavailable"`
	MinReady                  Duration      `json:"min_ready"`
	ProgressDeadline          Duration      `json:"progress_deadline"`
	FailureThreshold          Amount        `json:"failure_threshold"`
	OnFailure                 FailureAction `json:"on_failure"`
	RespectMaintenanceWindows bool          `json:"respect_maintenance_windows"`
	ShuffleSeed               string        `json:"shuffle_seed,omitempty"`
	Canary                    *CanarySpec   `json:"canary,omitempty"`
	Partitions                []Partition   `json:"partitions,omitempty"`
}

func (s RolloutStrategy) Validate() error {
	var collector validationCollector
	if s.MaxConcurrent == 0 {
		collector.add("max_concurrent", CodeInvalid, "must be greater than zero")
	}
	collector.append("", s.MaxUnavailable.validate("max_unavailable", false))
	collector.append("", s.FailureThreshold.validate("failure_threshold", true))
	validatePositiveDuration(&collector, "progress_deadline", s.ProgressDeadline)
	if s.MinReady < 0 {
		collector.add("min_ready", CodeInvalid, "must not be negative")
	}
	switch s.OnFailure {
	case FailurePause, FailureAbort, FailureRollback:
	default:
		collector.add("on_failure", CodeUnsupported, "must be pause, abort, or rollback")
	}
	if len(s.ShuffleSeed) > 128 {
		collector.add("shuffle_seed", CodeLimitExceeded, "must be at most 128 bytes")
	}
	switch s.Type {
	case StrategyAllAtOnce, StrategyRolling:
		if s.Canary != nil {
			collector.add("canary", CodeConflict, "is only valid for a canary strategy")
		}
		if len(s.Partitions) != 0 {
			collector.add("partitions", CodeConflict, "is only valid for a partitioned strategy")
		}
	case StrategyCanary:
		if s.Canary == nil {
			collector.add("canary", CodeRequired, "is required for a canary strategy")
		} else {
			collector.append("canary", s.Canary.validate())
		}
		if len(s.Partitions) != 0 {
			collector.add("partitions", CodeConflict, "is only valid for a partitioned strategy")
		}
	case StrategyPartitioned:
		if s.Canary != nil {
			collector.add("canary", CodeConflict, "is only valid for a canary strategy")
		}
		if len(s.Partitions) == 0 {
			collector.add("partitions", CodeRequired, "must contain at least one partition")
		}
		seen := make(map[string]struct{}, len(s.Partitions))
		for i, partition := range s.Partitions {
			collector.append(fmt.Sprintf("partitions[%d]", i), partition.validate())
			if _, exists := seen[partition.Name]; exists {
				collector.add(fmt.Sprintf("partitions[%d].name", i), CodeConflict, "partition names must be unique")
			}
			seen[partition.Name] = struct{}{}
		}
	default:
		collector.add("type", CodeUnsupported, "must be all_at_once, rolling, canary, or partitioned")
	}
	return collector.err()
}

func (s RolloutStrategy) CanonicalDigest() (Digest, error) {
	if err := s.Validate(); err != nil {
		return "", err
	}
	canonical := s
	if s.Canary != nil {
		canary := *s.Canary
		canary.ClusterIDs = sortedUUIDs(canary.ClusterIDs)
		canonical.Canary = &canary
	}
	canonical.Partitions = append([]Partition(nil), s.Partitions...)
	for i := range canonical.Partitions {
		selector, _ := canonical.Partitions[i].Selector.Canonical()
		canonical.Partitions[i].Selector = selector
	}
	return CanonicalDigest(canonical)
}

func (s CanarySpec) validate() error {
	var collector validationCollector
	if len(s.ClusterIDs) == 0 {
		collector.append("", s.Size.validate("size", true))
	} else {
		validateUUIDSet(&collector, "cluster_ids", s.ClusterIDs)
		if s.Size.Type != "" || s.Size.Value != 0 {
			collector.add("size", CodeConflict, "must be omitted when explicit canary cluster IDs are set")
		}
	}
	if s.Soak < 0 {
		collector.add("soak", CodeInvalid, "must not be negative")
	}
	return collector.err()
}

func (p Partition) validate() error {
	var collector validationCollector
	validateName(&collector, "name", p.Name)
	collector.append("selector", p.Selector.Validate())
	if p.Selector.IsEmpty() {
		collector.add("selector", CodeRequired, "must select a bounded group or label cohort")
	}
	if p.Selector.AllClusters || len(p.Selector.ClusterIDs) != 0 || len(p.Selector.ProjectIDs) != 0 || len(p.Selector.ExcludeClusterIDs) != 0 {
		collector.add("selector", CodeConflict, "partition selectors may contain only group and label predicates")
	}
	if p.Soak < 0 {
		collector.add("soak", CodeInvalid, "must not be negative")
	}
	return collector.err()
}

type RolloutState string

const (
	RolloutDraft            RolloutState = "draft"
	RolloutResolving        RolloutState = "resolving"
	RolloutAwaitingApproval RolloutState = "awaiting_approval"
	RolloutRejected         RolloutState = "rejected"
	RolloutQueued           RolloutState = "queued"
	RolloutProgressing      RolloutState = "progressing"
	RolloutPaused           RolloutState = "paused"
	RolloutAborted          RolloutState = "aborted"
	RolloutSucceeded        RolloutState = "succeeded"
	RolloutFailed           RolloutState = "failed"
	RolloutRollingBack      RolloutState = "rolling_back"
	RolloutRolledBack       RolloutState = "rolled_back"
	RolloutRollbackFailed   RolloutState = "rollback_failed"
)

var AllRolloutStates = []RolloutState{
	RolloutDraft, RolloutResolving, RolloutAwaitingApproval, RolloutRejected,
	RolloutQueued, RolloutProgressing, RolloutPaused, RolloutAborted,
	RolloutSucceeded, RolloutFailed, RolloutRollingBack, RolloutRolledBack,
	RolloutRollbackFailed,
}

func (s RolloutState) Valid() bool { return contains(AllRolloutStates, s) }

type RolloutClusterState string

const (
	RolloutClusterPending       RolloutClusterState = "pending"
	RolloutClusterReleased      RolloutClusterState = "released"
	RolloutClusterAcknowledged  RolloutClusterState = "acknowledged"
	RolloutClusterReconciling   RolloutClusterState = "reconciling"
	RolloutClusterReady         RolloutClusterState = "ready"
	RolloutClusterBlocked       RolloutClusterState = "blocked"
	RolloutClusterTimedOut      RolloutClusterState = "timed_out"
	RolloutClusterFailed        RolloutClusterState = "failed"
	RolloutClusterSkipped       RolloutClusterState = "skipped"
	RolloutClusterRollingBack   RolloutClusterState = "rolling_back"
	RolloutClusterReadyPrevious RolloutClusterState = "ready_previous"
)

var AllRolloutClusterStates = []RolloutClusterState{
	RolloutClusterPending, RolloutClusterReleased, RolloutClusterAcknowledged,
	RolloutClusterReconciling, RolloutClusterReady, RolloutClusterBlocked,
	RolloutClusterTimedOut, RolloutClusterFailed, RolloutClusterSkipped,
	RolloutClusterRollingBack, RolloutClusterReadyPrevious,
}

func (s RolloutClusterState) Valid() bool { return contains(AllRolloutClusterStates, s) }

type DeploymentPhase string

const (
	DeploymentPending   DeploymentPhase = "pending"
	DeploymentBlocked   DeploymentPhase = "blocked"
	DeploymentApplying  DeploymentPhase = "applying"
	DeploymentReady     DeploymentPhase = "ready"
	DeploymentDegraded  DeploymentPhase = "degraded"
	DeploymentFailed    DeploymentPhase = "failed"
	DeploymentSuspended DeploymentPhase = "suspended"
	DeploymentDeleting  DeploymentPhase = "deleting"
	DeploymentRemoved   DeploymentPhase = "removed"
	DeploymentUnknown   DeploymentPhase = "unknown"
)

var AllDeploymentPhases = []DeploymentPhase{
	DeploymentPending, DeploymentBlocked, DeploymentApplying, DeploymentReady,
	DeploymentDegraded, DeploymentFailed, DeploymentSuspended, DeploymentDeleting,
	DeploymentRemoved, DeploymentUnknown,
}

func (s DeploymentPhase) Valid() bool { return contains(AllDeploymentPhases, s) }

type DeploymentAction string

const (
	ActionApply   DeploymentAction = "apply"
	ActionSuspend DeploymentAction = "suspend"
	ActionDelete  DeploymentAction = "delete"
)

type ConditionType string

const (
	ConditionReady       ConditionType = "Ready"
	ConditionReconciling ConditionType = "Reconciling"
	ConditionStalled     ConditionType = "Stalled"
	ConditionDrifted     ConditionType = "Drifted"
)

type ConditionStatus string

const (
	ConditionTrue    ConditionStatus = "True"
	ConditionFalse   ConditionStatus = "False"
	ConditionUnknown ConditionStatus = "Unknown"
)

// Condition is a bounded normalized Flux condition. Message must already be
// sanitized by the observer; the model prevents unbounded persistence.
type Condition struct {
	Type               ConditionType   `json:"type"`
	Status             ConditionStatus `json:"status"`
	Reason             string          `json:"reason,omitempty"`
	Message            string          `json:"message,omitempty"`
	ObservedGeneration int64           `json:"observed_generation"`
	LastTransitionTime time.Time       `json:"last_transition_time"`
}

func (c Condition) Validate() error {
	var collector validationCollector
	switch c.Type {
	case ConditionReady, ConditionReconciling, ConditionStalled, ConditionDrifted:
	default:
		collector.add("type", CodeUnsupported, "is not a normalized delivery condition")
	}
	switch c.Status {
	case ConditionTrue, ConditionFalse, ConditionUnknown:
	default:
		collector.add("status", CodeUnsupported, "must be True, False, or Unknown")
	}
	if len(c.Reason) > 128 {
		collector.add("reason", CodeLimitExceeded, "must be at most 128 bytes")
	}
	if len(c.Message) > 2048 {
		collector.add("message", CodeLimitExceeded, "must be at most 2048 bytes")
	}
	if c.ObservedGeneration < 0 {
		collector.add("observed_generation", CodeInvalid, "must not be negative")
	}
	if c.LastTransitionTime.IsZero() {
		collector.add("last_transition_time", CodeRequired, "is required")
	}
	return collector.err()
}

// CanonicalConditions validates, de-duplicates, and sorts conditions by type.
func CanonicalConditions(input []Condition) ([]Condition, error) {
	result := append([]Condition(nil), input...)
	sort.Slice(result, func(i, j int) bool { return result[i].Type < result[j].Type })
	var collector validationCollector
	for i, condition := range result {
		collector.append(fmt.Sprintf("conditions[%d]", i), condition.Validate())
		if i > 0 && result[i-1].Type == condition.Type {
			collector.add(fmt.Sprintf("conditions[%d].type", i), CodeConflict, "condition types must be unique")
		}
	}
	return result, collector.err()
}

type EventKind string

const (
	EventStateTransition EventKind = "state_transition"
	EventWarning         EventKind = "warning"
	EventOperatorAction  EventKind = "operator_action"
	EventRevisionChange  EventKind = "revision_change"
)

// Event is the bounded domain event payload stored with a transition/outbox
// record. Detail is a sanitized code, never raw controller output.
type Event struct {
	ID          uuid.UUID `json:"id"`
	AggregateID uuid.UUID `json:"aggregate_id"`
	Kind        EventKind `json:"kind"`
	From        string    `json:"from,omitempty"`
	To          string    `json:"to,omitempty"`
	Code        string    `json:"code,omitempty"`
	Detail      string    `json:"detail,omitempty"`
	Fence       int64     `json:"fence"`
	OccurredAt  time.Time `json:"occurred_at"`
}

func (e Event) Validate() error {
	var collector validationCollector
	validateUUID(&collector, "id", e.ID)
	validateUUID(&collector, "aggregate_id", e.AggregateID)
	switch e.Kind {
	case EventStateTransition, EventWarning, EventOperatorAction, EventRevisionChange:
	default:
		collector.add("kind", CodeUnsupported, "is not a supported delivery event kind")
	}
	if e.Kind == EventStateTransition && (e.From == "" || e.To == "") {
		collector.add("from", CodeRequired, "from and to are required for state transitions")
	}
	if e.Code != "" && !SafeCode(e.Code) {
		collector.add("code", CodeInvalid, "must be a bounded lowercase machine code")
	}
	if len(e.Detail) > 2048 {
		collector.add("detail", CodeLimitExceeded, "must be at most 2048 bytes")
	}
	if e.Fence < 0 {
		collector.add("fence", CodeInvalid, "must not be negative")
	}
	if e.OccurredAt.IsZero() {
		collector.add("occurred_at", CodeRequired, "is required")
	}
	return collector.err()
}

func contains[T comparable](values []T, wanted T) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func CanonicalStrings(input []string) []string {
	result := append([]string(nil), input...)
	sort.Strings(result)
	return result
}

func SafeCode(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, r := range value {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' && r != '-' {
			return false
		}
	}
	return !strings.Contains(value, "--")
}
