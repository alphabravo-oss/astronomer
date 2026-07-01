package middleware

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
)

// userBindingsQuerier is the subset of the sqlc-generated Queries surface that
// SQLCRBACQuerier needs. Declared as a package-private interface so tests can
// inject a fake and assert "exactly one DB call" behavior without spinning up
// a real Postgres.
type userBindingsQuerier interface {
	GetUserByID(ctx context.Context, id uuid.UUID) (sqlc.User, error)
	ListUserBindingsWithRoles(ctx context.Context, userID pgtype.UUID) ([]sqlc.ListUserBindingsWithRolesRow, error)
	// ListProjectNamespaces resolves the (cluster_id, namespace) rows a project
	// owns. Used only when namespace-scoped RBAC reads are enabled: each project
	// binding is expanded into synthetic namespace-scoped cluster bindings so the
	// pure engine matches them per-cluster.
	ListProjectNamespaces(ctx context.Context, projectID uuid.UUID) ([]sqlc.ProjectNamespace, error)
}

// SQLCRBACQuerier adapts sqlc queries to the RBAC middleware binding interface.
// Wraps a TTL+LRU cache so repeated requests for the same user inside the TTL
// window (default 15s) skip the DB entirely. Mutation handlers must call
// (*SQLCRBACQuerier).Invalidate(userID) after any *_role_bindings or *_roles
// write so the cache never serves stale authorization data.
type SQLCRBACQuerier struct {
	queries userBindingsQuerier
	cache   *RBACCache
	// namespaceScoping toggles project→namespace binding expansion in
	// GetUserBindings. Default false keeps behavior byte-identical: project
	// bindings are emitted exactly as before and grant nothing on cluster
	// resource routes. Set via SetNamespaceScoping / the WithNamespaceScoping
	// constructor from the namespace_scoped_rbac_enabled config flag.
	namespaceScoping bool
}

// NewSQLCRBACQuerier builds the querier with a default cache (15s TTL,
// 5000-entry LRU). Returns nil when `queries` is nil so the caller-side
// nil-check pattern keeps working.
func NewSQLCRBACQuerier(queries *sqlc.Queries) *SQLCRBACQuerier {
	if queries == nil {
		return nil
	}
	return &SQLCRBACQuerier{queries: queries, cache: NewRBACCache()}
}

// NewSQLCRBACQuerierWithNamespaceScoping is like NewSQLCRBACQuerier but sets the
// project→namespace expansion flag at construction time. Kept as a separate
// constructor so the existing NewSQLCRBACQuerier callers stay untouched.
func NewSQLCRBACQuerierWithNamespaceScoping(queries *sqlc.Queries, namespaceScoping bool) *SQLCRBACQuerier {
	q := NewSQLCRBACQuerier(queries)
	if q != nil {
		q.namespaceScoping = namespaceScoping
	}
	return q
}

// SetNamespaceScoping toggles project→namespace binding expansion. Safe on nil
// receivers. Flipping it invalidates any already-cached bindings so the next
// lookup rebuilds them with the new expansion behavior.
func (q *SQLCRBACQuerier) SetNamespaceScoping(enabled bool) {
	if q == nil {
		return
	}
	q.namespaceScoping = enabled
	if q.cache != nil {
		q.cache.InvalidateAll()
	}
}

// NewSQLCRBACQuerierWithCache lets callers (tests, future tuning knobs) pass
// a preconfigured cache. A nil cache disables caching entirely — the querier
// just becomes a thin shim over the single-query sqlc call.
func NewSQLCRBACQuerierWithCache(queries userBindingsQuerier, cache *RBACCache) *SQLCRBACQuerier {
	if queries == nil {
		return nil
	}
	return &SQLCRBACQuerier{queries: queries, cache: cache}
}

// Cache exposes the underlying cache so server wiring (and mutation handlers
// elsewhere in the package) can call Invalidate. Returns nil when caching was
// explicitly disabled at construction time.
func (q *SQLCRBACQuerier) Cache() *RBACCache {
	if q == nil {
		return nil
	}
	return q.cache
}

// Invalidate drops the cache entry for userID. Safe on nil receivers and on
// querier instances constructed without a cache (no-op). Callers should
// invoke this after any successful binding/role mutation; see the call sites
// in internal/handler/rbac.go.
func (q *SQLCRBACQuerier) Invalidate(userID string) {
	if q == nil || q.cache == nil {
		return
	}
	q.cache.Invalidate(userID)
}

// InvalidateAll drops every cached entry. Used when a role definition (not a
// binding) changes — the role's rules are denormalized into every cached
// binding for every user that holds it, so a targeted invalidation isn't
// possible without a reverse index. Cheaper to just dump the cache.
func (q *SQLCRBACQuerier) InvalidateAll() {
	if q == nil || q.cache == nil {
		return
	}
	q.cache.InvalidateAll()
}

// GetUserBindings returns the user's global+cluster+project role bindings,
// each with the role's rules already decoded. The result is cached for the
// configured TTL. The returned slice is shared with the cache — callers must
// treat it as read-only (the RBAC engine does).
func (q *SQLCRBACQuerier) GetUserBindings(ctx context.Context, userID string) ([]rbac.RoleBinding, error) {
	if q == nil || q.queries == nil {
		return nil, nil
	}
	if userID == "" {
		// Anonymous requests never reach RequirePermission (the auth check
		// short-circuits with 401), so this is defense-in-depth: skip the
		// cache entirely so we never persist an entry keyed by "".
		return nil, nil
	}

	if q.cache != nil {
		if cached, ok := q.cache.Get(userID); ok {
			return cached, nil
		}
	}

	parsedUserID, err := uuid.Parse(userID)
	if err != nil {
		return nil, err
	}

	// Superuser short-circuit: a single synthetic binding with IsSuperuser=true
	// causes the engine to grant any permission without consulting role data.
	// Still cached so we don't re-fetch the users row on every request.
	if user, err := q.queries.GetUserByID(ctx, parsedUserID); err == nil && user.IsSuperuser {
		bindings := []rbac.RoleBinding{{UserID: userID, IsSuperuser: true}}
		if q.cache != nil {
			q.cache.Put(userID, bindings)
		}
		return bindings, nil
	}

	pgUserID := pgtype.UUID{Bytes: parsedUserID, Valid: true}
	rows, err := q.queries.ListUserBindingsWithRoles(ctx, pgUserID)
	if err != nil {
		return nil, err
	}

	results := make([]rbac.RoleBinding, 0, len(rows))
	for _, row := range rows {
		rules, err := decodeRoleRules(row.RoleRules)
		if err != nil {
			return nil, err
		}
		binding := rbac.RoleBinding{
			UserID:    userID,
			Group:     row.Group,
			RoleRules: rules,
			BindingID: row.BindingID.String(),
			RoleID:    row.RoleID.String(),
			RoleName:  row.RoleName,
			Scope:     row.Scope,
		}
		switch row.Scope {
		case "cluster":
			if row.ClusterID.Valid {
				binding.ClusterID = uuid.UUID(row.ClusterID.Bytes).String()
			}
			binding.Namespace = row.Namespace
		case "project":
			if row.ProjectID.Valid {
				binding.ProjectID = uuid.UUID(row.ProjectID.Bytes).String()
			}
		}
		results = append(results, binding)
	}

	// Namespace-scoped reads (flag-gated): expand every project binding into
	// synthetic namespace-scoped CLUSTER bindings, one per (cluster_id,
	// namespace) row the project owns. The pure engine then matches them
	// per-cluster unchanged — this is what makes a project member's cluster-read
	// resolve to exactly their project's namespaces. The original project
	// binding is kept so project-scoped routes keep working. When the flag is
	// off this block is skipped entirely and behavior is byte-identical.
	if q.namespaceScoping {
		expanded, err := q.expandProjectBindings(ctx, userID, results)
		if err != nil {
			return nil, err
		}
		results = expanded
	}

	if q.cache != nil {
		q.cache.Put(userID, results)
	}
	return results, nil
}

// expandProjectBindings appends synthetic namespace-scoped cluster bindings for
// each project binding. Fails closed: any DB error resolving a project's
// namespaces propagates as an error (→ the caller denies the request) rather
// than silently granting or dropping scope.
func (q *SQLCRBACQuerier) expandProjectBindings(ctx context.Context, userID string, bindings []rbac.RoleBinding) ([]rbac.RoleBinding, error) {
	// Resolve each distinct project only once even if the user holds several
	// bindings on it.
	nsCache := make(map[string][]sqlc.ProjectNamespace)
	var synthetic []rbac.RoleBinding
	for _, b := range bindings {
		if b.Scope != "project" || b.ProjectID == "" {
			continue
		}
		rows, ok := nsCache[b.ProjectID]
		if !ok {
			projectUUID, err := uuid.Parse(b.ProjectID)
			if err != nil {
				// A malformed project ID cannot be expanded; fail closed by
				// contributing no namespaces for it.
				nsCache[b.ProjectID] = nil
				continue
			}
			rows, err = q.queries.ListProjectNamespaces(ctx, projectUUID)
			if err != nil {
				return nil, err
			}
			nsCache[b.ProjectID] = rows
		}
		for _, row := range rows {
			synthetic = append(synthetic, rbac.RoleBinding{
				UserID:    userID,
				Group:     b.Group,
				RoleRules: b.RoleRules,
				BindingID: b.BindingID,
				RoleID:    b.RoleID,
				RoleName:  b.RoleName,
				Scope:     "cluster",
				ClusterID: row.ClusterID.String(),
				Namespace: row.Namespace,
			})
		}
	}
	if len(synthetic) == 0 {
		return bindings, nil
	}
	return append(bindings, synthetic...), nil
}

func decodeRoleRules(raw json.RawMessage) ([]rbac.Rule, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var rules []rbac.Rule
	if err := json.Unmarshal(raw, &rules); err != nil {
		return nil, err
	}
	return rules, nil
}
