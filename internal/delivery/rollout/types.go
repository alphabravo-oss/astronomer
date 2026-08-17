// Package rollout plans immutable multi-cluster delivery operations and makes
// pure, fenced scheduling decisions. Persistence adapters are intentionally
// narrow: one planner transaction freezes the plan, while workers apply each
// returned decision with compare-and-swap and an outbox event in one database
// transaction.
package rollout

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/placement"
)

const (
	MaxActorLength          = 512
	MaxIdempotencyKeyLength = 128
	MaxLeaseOwnerLength     = 253
)

// VersionIdentity is all immutable information needed to apply or roll back a
// cluster. Mutable source requests are deliberately absent.
type VersionIdentity struct {
	BundleVersionID uuid.UUID                `json:"bundle_version_id"`
	SpecDigest      model.Digest             `json:"spec_digest"`
	Source          model.ResolvedSourceSpec `json:"source"`
}

func (v VersionIdentity) Validate() error {
	if v.BundleVersionID == uuid.Nil {
		return fail(CodeInvalidInput, "bundle_version_id", "must be a non-zero UUID")
	}
	if err := v.SpecDigest.Validate(); err != nil {
		return &Error{Code: CodeInvalidInput, Field: "spec_digest", Cause: err}
	}
	if err := v.Source.Validate(); err != nil {
		return &Error{Code: CodeInvalidInput, Field: "source", Cause: err}
	}
	return nil
}

// PreviousDeployment is frozen per cluster. Generation is the last generation
// known good before this rollout; it is evidence, not the generation used for
// the new rollback assignment.
type PreviousDeployment struct {
	Version    VersionIdentity `json:"version"`
	Generation int64           `json:"generation"`
}

func (p PreviousDeployment) Validate() error {
	if p.Generation < 1 {
		return fail(CodeInvalidInput, "previous.generation", "must be positive")
	}
	return p.Version.Validate()
}

type ApprovalBinding struct {
	Required bool         `json:"required"`
	Digest   model.Digest `json:"digest"`
}

type Cohort struct {
	Index            int            `json:"index"`
	Name             string         `json:"name"`
	ClusterIDs       []uuid.UUID    `json:"cluster_ids"`
	ApprovalRequired bool           `json:"approval_required"`
	ApprovalDigest   model.Digest   `json:"approval_digest,omitempty"`
	SoakAfter        model.Duration `json:"soak_after"`
}

type PlannedCluster struct {
	ClusterID uuid.UUID           `json:"cluster_id"`
	Cohort    int                 `json:"cohort"`
	Order     int                 `json:"order"`
	Previous  *PreviousDeployment `json:"previous,omitempty"`
}

// FrozenRollout is the immutable planner result. Runtime state, counters, and
// leases live in separate persisted rows and cannot alter this value.
type FrozenRollout struct {
	ID               uuid.UUID             `json:"id"`
	TargetID         uuid.UUID             `json:"target_id"`
	ProjectID        uuid.UUID             `json:"project_id"`
	TargetGeneration uint64                `json:"target_generation"`
	Desired          VersionIdentity       `json:"desired"`
	PlacementDigest  model.Digest          `json:"placement_digest"`
	Strategy         model.RolloutStrategy `json:"strategy"`
	StrategyDigest   model.Digest          `json:"strategy_digest"`
	Approval         ApprovalBinding       `json:"approval"`
	Actor            string                `json:"actor"`
	IdempotencyKey   string                `json:"idempotency_key"`
	RequestDigest    model.Digest          `json:"request_digest"`
	CreatedAt        time.Time             `json:"created_at"`
	Deadline         time.Time             `json:"deadline"`
	Cohorts          []Cohort              `json:"cohorts"`
	Clusters         []PlannedCluster      `json:"clusters"`
	PlanDigest       model.Digest          `json:"plan_digest"`
}

// PlanningSnapshot must be loaded under the same transaction/lock used to
// insert the plan. It is a complete batch projection; the planner never makes
// a per-cluster storage call.
type PlanningSnapshot struct {
	TargetID          uuid.UUID
	ProjectID         uuid.UUID
	TargetGeneration  uint64
	Desired           VersionIdentity
	PlacementRequest  placement.Request
	InitialApproval   bool
	PreviousByCluster map[uuid.UUID]PreviousDeployment
}

type CreateRequest struct {
	TargetID                 uuid.UUID
	ExpectedTargetGeneration uint64
	PreviewDigest            model.Digest
	ConfirmAllClusters       bool
	Strategy                 model.RolloutStrategy
	Actor                    string
	IdempotencyKey           string
}

// PlanningStore supplies an actual database transaction. The callback must be
// committed atomically on nil and rolled back on error.
type PlanningStore interface {
	InTransaction(context.Context, func(PlanningTransaction) error) error
}

type PlanningTransaction interface {
	FindByIdempotency(context.Context, uuid.UUID, string) (FrozenRollout, bool, error)
	LoadSnapshotForUpdate(context.Context, uuid.UUID) (PlanningSnapshot, error)
	InsertRollout(context.Context, FrozenRollout) error
	AppendRolloutCreated(context.Context, FrozenRollout) error
	EnqueueRollout(context.Context, uuid.UUID) error
}

// IDGenerator is injectable so retries and concurrency behavior can be tested
// without randomness. Production callers should use uuid.New.
type IDGenerator func() uuid.UUID

type ApprovalDecision struct {
	ID            uuid.UUID
	BindingDigest model.Digest
	Approved      bool
	DecidedAt     time.Time
	ExpiresAt     time.Time
}

func (d ApprovalDecision) validFor(binding model.Digest, now time.Time) bool {
	return d.ID != uuid.Nil && d.BindingDigest == binding &&
		!d.DecidedAt.IsZero() && !d.DecidedAt.After(now) && d.ExpiresAt.After(now)
}

type MaintenanceGate struct {
	Open               bool
	OverrideAuthorized bool
	OverrideReason     string
}

func (g MaintenanceGate) allowsRelease() bool {
	return g.Open || (g.OverrideAuthorized && strings.TrimSpace(g.OverrideReason) != "")
}

// Lease binds a scheduler evaluation to one durable worker claim.
type Lease struct {
	Owner     string
	Fence     int64
	ExpiresAt time.Time
}

func (l Lease) validate(now time.Time, fence int64) error {
	if l.Owner == "" || len(l.Owner) > MaxLeaseOwnerLength || strings.ContainsAny(l.Owner, "\r\n\x00") {
		return fail(CodeLeaseLost, "lease.owner", "invalid lease owner")
	}
	if l.Fence != fence {
		return fail(CodeStaleFence, "lease.fence", "claim fence no longer matches")
	}
	if !l.ExpiresAt.After(now) {
		return fail(CodeLeaseLost, "lease.expires_at", "lease has expired")
	}
	return nil
}

func validateCreateRequest(request CreateRequest) error {
	if request.TargetID == uuid.Nil {
		return fail(CodeInvalidInput, "target_id", "must be a non-zero UUID")
	}
	if request.ExpectedTargetGeneration == 0 {
		return fail(CodeInvalidInput, "expected_target_generation", "must be positive")
	}
	if err := request.PreviewDigest.Validate(); err != nil {
		return &Error{Code: CodeInvalidInput, Field: "preview_digest", Cause: err}
	}
	if err := request.Strategy.Validate(); err != nil {
		return &Error{Code: CodeInvalidInput, Field: "strategy", Cause: err}
	}
	if strings.TrimSpace(request.Actor) == "" || len(request.Actor) > MaxActorLength || strings.ContainsAny(request.Actor, "\r\n\x00") {
		return fail(CodeInvalidInput, "actor", fmt.Sprintf("must be 1..%d safe bytes", MaxActorLength))
	}
	if strings.TrimSpace(request.IdempotencyKey) == "" || len(request.IdempotencyKey) > MaxIdempotencyKeyLength || strings.ContainsAny(request.IdempotencyKey, "\r\n\x00") {
		return fail(CodeInvalidInput, "idempotency_key", fmt.Sprintf("must be 1..%d safe bytes", MaxIdempotencyKeyLength))
	}
	return nil
}
