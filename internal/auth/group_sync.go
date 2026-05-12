// Group-claim sync (migration 042).
//
// SyncUserGroups is invoked from the SSO callback after a successful
// IdP handshake. It reconciles the user's RBAC role bindings against
// the operator-configured identity_group_mappings table:
//
//   - claims contain group X
//   - the operator has a mapping from group X -> role Y at scope Z
//   - we ensure the corresponding *_role_bindings row exists with
//     source='group_sync'
//
// The inverse is equally important: any source='group_sync' row that
// the user previously had but whose mapping is no longer in the
// current claims set gets deleted. Manual bindings (source='manual',
// the default) are never touched — that's the migration's
// backward-compat guarantee and protects existing operator-created
// bindings from being deleted on first login after upgrade.
//
// The function returns the binding diff so callers (the SSO handler
// + the admin resync endpoint) can emit per-action audit rows and
// optionally invalidate the RBAC cache for the affected user.
package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
)

// GroupSyncQuerier is the narrow DB surface the sync function reads
// and writes. Implemented by *sqlc.Queries; tests use a hand-rolled
// fake so the function is exercised without a real Postgres.
type GroupSyncQuerier interface {
	ListGroupMappingsForConnector(ctx context.Context, connectorID pgtype.UUID) ([]sqlc.IdentityGroupMapping, error)
	UpsertUserIDPGroups(ctx context.Context, arg sqlc.UpsertUserIDPGroupsParams) (sqlc.UserIdpGroup, error)
	GetUserIDPGroups(ctx context.Context, userID uuid.UUID) (sqlc.UserIdpGroup, error)

	ListGroupSyncGlobalBindings(ctx context.Context, userID pgtype.UUID) ([]sqlc.GlobalRoleBinding, error)
	ListGroupSyncClusterBindings(ctx context.Context, userID pgtype.UUID) ([]sqlc.ClusterRoleBinding, error)
	ListGroupSyncProjectBindings(ctx context.Context, userID pgtype.UUID) ([]sqlc.ProjectRoleBinding, error)

	CreateGroupSyncGlobalBinding(ctx context.Context, arg sqlc.CreateGroupSyncGlobalBindingParams) (sqlc.GlobalRoleBinding, error)
	CreateGroupSyncClusterBinding(ctx context.Context, arg sqlc.CreateGroupSyncClusterBindingParams) (sqlc.ClusterRoleBinding, error)
	CreateGroupSyncProjectBinding(ctx context.Context, arg sqlc.CreateGroupSyncProjectBindingParams) (sqlc.ProjectRoleBinding, error)

	DeleteGroupSyncGlobalBinding(ctx context.Context, id uuid.UUID) error
	DeleteGroupSyncClusterBinding(ctx context.Context, id uuid.UUID) error
	DeleteGroupSyncProjectBinding(ctx context.Context, id uuid.UUID) error
}

// SyncedBinding describes one create-or-delete that the sync run
// performed. Returned to the caller so audit rows reflect the actual
// state mutation (not just "sync was called").
type SyncedBinding struct {
	Scope     string    // 'global' | 'cluster' | 'project'
	BindingID uuid.UUID // PK of the *_role_bindings row
	RoleID    uuid.UUID // mapping.role_id (canonical)
	ClusterID uuid.UUID // populated only for scope='cluster'
	ProjectID uuid.UUID // populated only for scope='project'
	GroupName string    // mapping.group_name that drove this change
}

// SyncResult is what SyncUserGroups returns. Added/Removed are mutually
// disjoint snapshots — the caller decides whether to emit per-binding
// audit rows or aggregate into one detail blob. Skipped is true when
// the function returned early because the claims slice was a "claims
// unavailable" sentinel (see SyncUserGroups for the policy).
type SyncResult struct {
	Added   []SyncedBinding
	Removed []SyncedBinding
	Skipped bool
}

// SyncUserGroups reconciles the user's group-sync bindings against
// the supplied claim set.
//
// claimsAvailable distinguishes "the IdP returned no groups" (empty
// slice, claimsAvailable=true → revoke all group_sync bindings) from
// "we didn't get fresh claims" (claimsAvailable=false → never delete,
// just no-op). The SSO callback always has fresh claims so it sets
// claimsAvailable=true even when groups is empty. The admin resync
// endpoint passes the snapshot from user_idp_groups and the same flag.
//
// connectorID may be a Valid pgtype.UUID (the Dex connector this
// claim came from) or Invalid (anonymous / single-tenant OIDC). The
// query returns wildcard (NULL connector_id) mappings either way.
func SyncUserGroups(
	ctx context.Context,
	q GroupSyncQuerier,
	userID uuid.UUID,
	connectorID pgtype.UUID,
	groups []string,
	claimsAvailable bool,
) (SyncResult, error) {
	if q == nil {
		return SyncResult{}, errors.New("group_sync: querier is nil")
	}
	if userID == uuid.Nil {
		return SyncResult{}, errors.New("group_sync: user_id is zero")
	}
	if !claimsAvailable {
		// Claims-unavailable sentinel: do nothing. Critically, we MUST
		// NOT delete existing group_sync bindings — the user may simply
		// have logged in via a non-claims path (local admin login,
		// password reset, etc.) where we have no fresh IdP signal.
		groupSyncSkippedTotal.WithLabelValues(observability.MetricValues("claims_unavailable")...).Inc()
		return SyncResult{Skipped: true}, nil
	}

	// 1) Persist the snapshot. The synced_at column drives staleness
	//    + the admin resync endpoint, so we update even when the slice
	//    is empty.
	groupsJSON, err := json.Marshal(groups)
	if err != nil {
		return SyncResult{}, fmt.Errorf("group_sync: marshal groups: %w", err)
	}
	if _, err := q.UpsertUserIDPGroups(ctx, sqlc.UpsertUserIDPGroupsParams{
		UserID:      userID,
		ConnectorID: connectorID,
		Groups:      json.RawMessage(groupsJSON),
		SyncedAt:    time.Now().UTC(),
	}); err != nil {
		groupSyncTotal.WithLabelValues(observability.MetricValues("error")...).Inc()
		return SyncResult{}, fmt.Errorf("group_sync: upsert snapshot: %w", err)
	}

	// 2) Resolve the set of mappings this connector + claim set match.
	candidates, err := q.ListGroupMappingsForConnector(ctx, connectorID)
	if err != nil {
		groupSyncTotal.WithLabelValues(observability.MetricValues("error")...).Inc()
		return SyncResult{}, fmt.Errorf("group_sync: list mappings: %w", err)
	}

	// Build a fast lookup over the claimed groups. SAML/LDAP/OIDC
	// emit case-sensitive names; we follow Dex's lead and don't
	// canonicalise them — operator's mapping must match exactly.
	claimed := make(map[string]struct{}, len(groups))
	for _, g := range groups {
		if g != "" {
			claimed[g] = struct{}{}
		}
	}

	// Filter to mappings whose group name actually appears in claims.
	// We never drop wildcard rows here; only the group_name filter
	// applies — the connector_id filter already happened in SQL.
	var matched []sqlc.IdentityGroupMapping
	for _, m := range candidates {
		if _, ok := claimed[m.GroupName]; ok {
			matched = append(matched, m)
		}
	}

	// 3) Compute the "should be present after this run" set, keyed by
	//    the same triple (scope, role, scoped-id) we use to dedupe.
	type wantKey struct {
		scope     string
		roleID    uuid.UUID
		clusterID uuid.UUID
		projectID uuid.UUID
	}
	wanted := make(map[wantKey]sqlc.IdentityGroupMapping, len(matched))
	for _, m := range matched {
		key := wantKey{scope: m.Scope, roleID: m.RoleID}
		if m.ClusterID.Valid {
			key.clusterID = m.ClusterID.Bytes
		}
		if m.ProjectID.Valid {
			key.projectID = m.ProjectID.Bytes
		}
		// First mapping wins if the operator duplicated the tuple.
		if _, dup := wanted[key]; !dup {
			wanted[key] = m
		}
	}

	res := SyncResult{}

	uid := pgtype.UUID{Bytes: userID, Valid: true}

	// 4) Diff against currently-present group_sync bindings. For each
	//    *_role_bindings table: enumerate, drop the rows missing from
	//    `wanted`, then idempotently insert anything `wanted` says
	//    should exist that we didn't already have.
	//
	//    The insert path uses ON CONFLICT DO NOTHING, which gracefully
	//    handles the case where a manual binding already covers the
	//    same (user, role) tuple — the manual row keeps source='manual'
	//    and the sync run leaves it alone. This is the
	//    ManualBindingPreserved test's protection.

	// --- global ---
	existingGlobal, err := q.ListGroupSyncGlobalBindings(ctx, uid)
	if err != nil {
		groupSyncTotal.WithLabelValues(observability.MetricValues("error")...).Inc()
		return SyncResult{}, fmt.Errorf("group_sync: list global: %w", err)
	}
	seenGlobal := make(map[wantKey]struct{}, len(existingGlobal))
	for _, b := range existingGlobal {
		key := wantKey{scope: "global", roleID: b.RoleID}
		if _, keep := wanted[key]; keep {
			seenGlobal[key] = struct{}{}
			continue
		}
		if err := q.DeleteGroupSyncGlobalBinding(ctx, b.ID); err != nil {
			groupSyncTotal.WithLabelValues(observability.MetricValues("error")...).Inc()
			return SyncResult{}, fmt.Errorf("group_sync: delete global %s: %w", b.ID, err)
		}
		res.Removed = append(res.Removed, SyncedBinding{
			Scope:     "global",
			BindingID: b.ID,
			RoleID:    b.RoleID,
		})
	}
	for key, m := range wanted {
		if key.scope != "global" {
			continue
		}
		if _, already := seenGlobal[key]; already {
			continue
		}
		row, err := q.CreateGroupSyncGlobalBinding(ctx, sqlc.CreateGroupSyncGlobalBindingParams{
			UserID: uid,
			RoleID: m.RoleID,
		})
		if err != nil {
			// pgx.ErrNoRows means ON CONFLICT DO NOTHING fired —
			// a manual binding already exists, leave it alone.
			if isNoRows(err) {
				continue
			}
			groupSyncTotal.WithLabelValues(observability.MetricValues("error")...).Inc()
			return SyncResult{}, fmt.Errorf("group_sync: create global: %w", err)
		}
		res.Added = append(res.Added, SyncedBinding{
			Scope:     "global",
			BindingID: row.ID,
			RoleID:    m.RoleID,
			GroupName: m.GroupName,
		})
	}

	// --- cluster ---
	existingCluster, err := q.ListGroupSyncClusterBindings(ctx, uid)
	if err != nil {
		groupSyncTotal.WithLabelValues(observability.MetricValues("error")...).Inc()
		return SyncResult{}, fmt.Errorf("group_sync: list cluster: %w", err)
	}
	seenCluster := make(map[wantKey]struct{}, len(existingCluster))
	for _, b := range existingCluster {
		key := wantKey{scope: "cluster", roleID: b.RoleID, clusterID: b.ClusterID}
		if _, keep := wanted[key]; keep {
			seenCluster[key] = struct{}{}
			continue
		}
		if err := q.DeleteGroupSyncClusterBinding(ctx, b.ID); err != nil {
			groupSyncTotal.WithLabelValues(observability.MetricValues("error")...).Inc()
			return SyncResult{}, fmt.Errorf("group_sync: delete cluster %s: %w", b.ID, err)
		}
		res.Removed = append(res.Removed, SyncedBinding{
			Scope:     "cluster",
			BindingID: b.ID,
			RoleID:    b.RoleID,
			ClusterID: b.ClusterID,
		})
	}
	for key, m := range wanted {
		if key.scope != "cluster" {
			continue
		}
		if !m.ClusterID.Valid {
			// Defensive: the table CHECK guarantees this, but a
			// hand-rolled SQL caller could in theory bypass it.
			continue
		}
		if _, already := seenCluster[key]; already {
			continue
		}
		row, err := q.CreateGroupSyncClusterBinding(ctx, sqlc.CreateGroupSyncClusterBindingParams{
			UserID:    uid,
			RoleID:    m.RoleID,
			ClusterID: m.ClusterID.Bytes,
		})
		if err != nil {
			if isNoRows(err) {
				continue
			}
			groupSyncTotal.WithLabelValues(observability.MetricValues("error")...).Inc()
			return SyncResult{}, fmt.Errorf("group_sync: create cluster: %w", err)
		}
		res.Added = append(res.Added, SyncedBinding{
			Scope:     "cluster",
			BindingID: row.ID,
			RoleID:    m.RoleID,
			ClusterID: m.ClusterID.Bytes,
			GroupName: m.GroupName,
		})
	}

	// --- project ---
	existingProject, err := q.ListGroupSyncProjectBindings(ctx, uid)
	if err != nil {
		groupSyncTotal.WithLabelValues(observability.MetricValues("error")...).Inc()
		return SyncResult{}, fmt.Errorf("group_sync: list project: %w", err)
	}
	seenProject := make(map[wantKey]struct{}, len(existingProject))
	for _, b := range existingProject {
		key := wantKey{scope: "project", roleID: b.RoleID, projectID: b.ProjectID}
		if _, keep := wanted[key]; keep {
			seenProject[key] = struct{}{}
			continue
		}
		if err := q.DeleteGroupSyncProjectBinding(ctx, b.ID); err != nil {
			groupSyncTotal.WithLabelValues(observability.MetricValues("error")...).Inc()
			return SyncResult{}, fmt.Errorf("group_sync: delete project %s: %w", b.ID, err)
		}
		res.Removed = append(res.Removed, SyncedBinding{
			Scope:     "project",
			BindingID: b.ID,
			RoleID:    b.RoleID,
			ProjectID: b.ProjectID,
		})
	}
	for key, m := range wanted {
		if key.scope != "project" {
			continue
		}
		if !m.ProjectID.Valid {
			continue
		}
		if _, already := seenProject[key]; already {
			continue
		}
		row, err := q.CreateGroupSyncProjectBinding(ctx, sqlc.CreateGroupSyncProjectBindingParams{
			UserID:    uid,
			RoleID:    m.RoleID,
			ProjectID: m.ProjectID.Bytes,
		})
		if err != nil {
			if isNoRows(err) {
				continue
			}
			groupSyncTotal.WithLabelValues(observability.MetricValues("error")...).Inc()
			return SyncResult{}, fmt.Errorf("group_sync: create project: %w", err)
		}
		res.Added = append(res.Added, SyncedBinding{
			Scope:     "project",
			BindingID: row.ID,
			RoleID:    m.RoleID,
			ProjectID: m.ProjectID.Bytes,
			GroupName: m.GroupName,
		})
	}

	groupSyncTotal.WithLabelValues(observability.MetricValues("success")...).Inc()
	return res, nil
}

// isNoRows centralises the pgx.ErrNoRows sentinel check. The
// CreateGroup* queries return this when ON CONFLICT DO NOTHING
// swallows the insert — a successful no-op from our perspective.
func isNoRows(err error) bool {
	return errors.Is(err, pgx.ErrNoRows)
}

// --- metrics --------------------------------------------------------

var (
	groupSyncMetricsOnce sync.Once

	groupSyncTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "astronomer",
			Name:      "auth_group_sync_total",
			Help:      "Total number of group-sync runs (per SSO login or admin resync).",
		},
		observability.MetricLabels("outcome"),
	)

	groupSyncSkippedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "astronomer",
			Name:      "auth_group_sync_skipped_total",
			Help:      "Total number of group-sync runs skipped because claims were unavailable.",
		},
		observability.MetricLabels("reason"),
	)

	// GroupSyncBindingsGauge is the live count of source='group_sync'
	// bindings across all scopes; the scope label distinguishes
	// global/cluster/project. Updated by RefreshGroupSyncMetrics
	// (called from the admin endpoint and on every successful sync).
	GroupSyncBindingsGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "astronomer",
			Name:      "auth_group_bindings",
			Help:      "Number of role bindings currently managed by identity-group sync.",
		},
		observability.MetricLabels("scope"),
	)
)

// RegisterGroupSyncMetrics is idempotent so multiple test harnesses
// don't panic on the second Register call. Mirrors the lockout +
// revocation metric registrar in metrics.go.
func RegisterGroupSyncMetrics() {
	groupSyncMetricsOnce.Do(func() {
		for _, c := range []prometheus.Collector{
			groupSyncTotal,
			groupSyncSkippedTotal,
			GroupSyncBindingsGauge,
		} {
			if err := prometheus.Register(c); err != nil {
				if _, ok := err.(prometheus.AlreadyRegisteredError); !ok {
					panic(err)
				}
			}
		}
	})
}

func init() {
	RegisterGroupSyncMetrics()
}
