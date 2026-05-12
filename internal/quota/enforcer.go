// Package quota implements the per-tenant resource-quota enforcer that
// gates create endpoints with the limits configured in migration 051.
//
// Model:
//
//   - Every project / user carries a `quota_plan` FK + a `quota_overrides`
//     JSONB column. The plan supplies a baseline of integer caps; the
//     override blob lets an operator pin a single cap to a custom value
//     for one tenant without forking the whole plan.
//   - The 'global' plan singleton's max_total_* fields gate fleet-wide
//     license-style caps (total clusters, total active users).
//   - Plans carry an `enforcement` flag (`hard` = reject, `soft` = warn).
//     The Check* methods always emit a metric/audit when they would have
//     rejected, but they only RETURN a *QuotaExceededError when the
//     plan's enforcement is `hard`. Soft mode is the documented Rancher-
//     style "show ops what's about to break before flipping to hard".
//
// All Check methods are O(1) DB queries: one for the effective plan +
// one for the current usage count. They run inline on the relevant POST
// handler, so the budget is "tens of ms" worst case. The 30s in-memory
// cache pattern from the RBAC engine is intentionally NOT applied here
// — quotas mutate via admin endpoints rarely, and the freshness
// requirement on a CREATE is tighter than the RBAC binding lookup.
package quota

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
)

// QuotaQuerier is the narrow DB surface the enforcer needs. *sqlc.Queries
// satisfies it; tests pass a fake that implements just these methods.
type QuotaQuerier interface {
	GetQuotaPlan(ctx context.Context, name string) (sqlc.QuotaPlan, error)
	GetEffectiveQuotaForUser(ctx context.Context, id uuid.UUID) (sqlc.GetEffectiveQuotaForUserRow, error)
	GetEffectiveQuotaForProject(ctx context.Context, id uuid.UUID) (sqlc.GetEffectiveQuotaForProjectRow, error)
	CountClustersInProject(ctx context.Context, projectID uuid.UUID) (int64, error)
	CountMembersInProject(ctx context.Context, projectID uuid.UUID) (int64, error)
	CountProjectsForUser(ctx context.Context, userID uuid.UUID) (int64, error)
	CountActiveTokensForUser(ctx context.Context, userID uuid.UUID) (int64, error)
	CountTotalClusters(ctx context.Context) (int64, error)
	CountTotalActiveUsers(ctx context.Context) (int64, error)
}

// Enforcer is the runtime check-engine. Construct via New, then call
// Check* from each gated CREATE handler.
type Enforcer struct {
	queries QuotaQuerier
	log     *slog.Logger
}

// New wires a new enforcer. log is optional; a nil log degrades to
// slog.Default().
func New(queries QuotaQuerier, log *slog.Logger) *Enforcer {
	if log == nil {
		log = slog.Default()
	}
	return &Enforcer{queries: queries, log: log}
}

// QuotaExceededError is the typed error returned when a hard-mode plan's
// cap has been reached. The handler middleware translates this into a
// 429 + structured body. The error string is human-readable but the
// fields are the contract that the API surface relies on.
type QuotaExceededError struct {
	Subject     string // "project:<uuid>" / "user:<uuid>" / "global"
	Limit       string // "max_clusters_per_project" etc.
	Current     int
	Maximum     int
	Enforcement string // "hard" — soft never gets returned, only logged
}

// Error makes QuotaExceededError satisfy the error interface.
func (e *QuotaExceededError) Error() string {
	return fmt.Sprintf("quota_exceeded: %s/%s at %d/%d (%s)", e.Subject, e.Limit, e.Current, e.Maximum, e.Enforcement)
}

// IsQuotaExceeded is the canonical type-test helper handlers use to
// decide whether to translate the error into a 429.
func IsQuotaExceeded(err error) (*QuotaExceededError, bool) {
	if err == nil {
		return nil, false
	}
	var qe *QuotaExceededError
	if errors.As(err, &qe) {
		return qe, true
	}
	return nil, false
}

// effectiveLimit picks the override value (if present) or falls back to
// the plan baseline. 0 means "unlimited" in either source.
func effectiveLimit(baseline int32, overrides json.RawMessage, key string) int32 {
	if len(overrides) == 0 {
		return baseline
	}
	var blob map[string]json.RawMessage
	if err := json.Unmarshal(overrides, &blob); err != nil {
		return baseline
	}
	raw, ok := blob[key]
	if !ok {
		return baseline
	}
	var v int32
	if err := json.Unmarshal(raw, &v); err != nil {
		return baseline
	}
	return v
}

// resolveLimit centralises the "at-cap?" decision. It returns nil if the
// caller may proceed (under cap OR plan is soft), and a typed error
// otherwise.
func (e *Enforcer) resolveLimit(subject, limit, enforcement string, current int64, max int32) error {
	// 0 = unlimited (plan default + override semantics).
	if max <= 0 {
		return nil
	}
	if current < int64(max) {
		// Pre-emptive metric: dashboards can spot the cliff before
		// the rejection lands.
		usageRatio := float64(current) / float64(max)
		quotaUsagePct.WithLabelValues(observability.MetricValues(subject, limit)...).Set(usageRatio * 100)
		return nil
	}
	// At or over cap. Emit the violation counter regardless of mode so
	// soft-mode rollouts can be observed before they're flipped to hard.
	quotaViolationsTotal.WithLabelValues(observability.MetricValues(subject, limit, enforcement)...).Inc()
	quotaUsagePct.WithLabelValues(observability.MetricValues(subject, limit)...).Set(100)
	if enforcement == "soft" {
		e.log.Warn("quota_soft_exceeded",
			"subject", subject,
			"limit", limit,
			"current", current,
			"maximum", max,
		)
		return nil
	}
	return &QuotaExceededError{
		Subject:     subject,
		Limit:       limit,
		Current:     int(current),
		Maximum:     int(max),
		Enforcement: enforcement,
	}
}

func projectSubject(id uuid.UUID) string { return "project:" + id.String() }
func userSubject(id uuid.UUID) string    { return "user:" + id.String() }

// CheckProjectClusterAdd enforces the per-project "no more than N
// clusters" cap. Because the schema fixes one cluster per project, this
// is enforced as "no more than N projects on the SAME cluster".
func (e *Enforcer) CheckProjectClusterAdd(ctx context.Context, projectID uuid.UUID) error {
	if e == nil || e.queries == nil {
		return nil
	}
	plan, err := e.queries.GetEffectiveQuotaForProject(ctx, projectID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	cur, err := e.queries.CountClustersInProject(ctx, projectID)
	if err != nil {
		return err
	}
	lim := effectiveLimit(plan.MaxClustersPerProject, plan.Overrides, "max_clusters_per_project")
	return e.resolveLimit(projectSubject(projectID), "max_clusters_per_project", plan.Enforcement, cur, lim)
}

// CheckProjectMemberAdd enforces the per-project "no more than N
// members" cap.
func (e *Enforcer) CheckProjectMemberAdd(ctx context.Context, projectID uuid.UUID) error {
	if e == nil || e.queries == nil {
		return nil
	}
	plan, err := e.queries.GetEffectiveQuotaForProject(ctx, projectID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	cur, err := e.queries.CountMembersInProject(ctx, projectID)
	if err != nil {
		return err
	}
	lim := effectiveLimit(plan.MaxMembersPerProject, plan.Overrides, "max_members_per_project")
	return e.resolveLimit(projectSubject(projectID), "max_members_per_project", plan.Enforcement, cur, lim)
}

// CheckUserProjectAdd enforces the per-user "no more than N projects I'm
// a member of" cap.
func (e *Enforcer) CheckUserProjectAdd(ctx context.Context, userID uuid.UUID) error {
	if e == nil || e.queries == nil {
		return nil
	}
	plan, err := e.queries.GetEffectiveQuotaForUser(ctx, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	cur, err := e.queries.CountProjectsForUser(ctx, userID)
	if err != nil {
		return err
	}
	lim := effectiveLimit(plan.MaxProjectsPerUser, plan.Overrides, "max_projects_per_user")
	return e.resolveLimit(userSubject(userID), "max_projects_per_user", plan.Enforcement, cur, lim)
}

// CheckUserTokenCreate enforces the per-user "no more than N active API
// tokens" cap.
func (e *Enforcer) CheckUserTokenCreate(ctx context.Context, userID uuid.UUID) error {
	if e == nil || e.queries == nil {
		return nil
	}
	plan, err := e.queries.GetEffectiveQuotaForUser(ctx, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	cur, err := e.queries.CountActiveTokensForUser(ctx, userID)
	if err != nil {
		return err
	}
	lim := effectiveLimit(plan.MaxTokensPerUser, plan.Overrides, "max_tokens_per_user")
	return e.resolveLimit(userSubject(userID), "max_tokens_per_user", plan.Enforcement, cur, lim)
}

// CheckGlobalClusterCreate enforces the fleet-wide "no more than N
// clusters total" cap. The 'global' plan singleton owns this cap.
func (e *Enforcer) CheckGlobalClusterCreate(ctx context.Context) error {
	if e == nil || e.queries == nil {
		return nil
	}
	plan, err := e.queries.GetQuotaPlan(ctx, "global")
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	cur, err := e.queries.CountTotalClusters(ctx)
	if err != nil {
		return err
	}
	return e.resolveLimit("global", "max_total_clusters", plan.Enforcement, cur, plan.MaxTotalClusters)
}

// CheckGlobalUserCreate enforces the fleet-wide "no more than N active
// users" cap.
func (e *Enforcer) CheckGlobalUserCreate(ctx context.Context) error {
	if e == nil || e.queries == nil {
		return nil
	}
	plan, err := e.queries.GetQuotaPlan(ctx, "global")
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	cur, err := e.queries.CountTotalActiveUsers(ctx)
	if err != nil {
		return err
	}
	return e.resolveLimit("global", "max_total_users", plan.Enforcement, cur, plan.MaxTotalUsers)
}
