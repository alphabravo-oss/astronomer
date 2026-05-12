package auth

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// fakeGroupSync is a hand-rolled in-memory implementation of the
// GroupSyncQuerier surface. The point isn't to mock the DB faithfully
// — it's to record every call + return scripted rows so the sync
// loop's add/remove decisions are visible to assertions.
type fakeGroupSync struct {
	mappings []sqlc.IdentityGroupMapping
	snapshot sqlc.UserIdpGroup
	snapErr  error

	// Indexed by uuid for lookups; group_sync bindings only.
	global  map[uuid.UUID]sqlc.GlobalRoleBinding
	cluster map[uuid.UUID]sqlc.ClusterRoleBinding
	project map[uuid.UUID]sqlc.ProjectRoleBinding

	// Manual bindings — these MUST be invisible to the sync loop.
	// We model them as a separate (user_id, role_id) tuple set the
	// CreateGroupSync* methods check before inserting, mirroring the
	// real DB's ON CONFLICT DO NOTHING behaviour.
	manualGlobal  map[[2]uuid.UUID]bool
	manualCluster map[[3]uuid.UUID]bool
	manualProject map[[3]uuid.UUID]bool

	// Call counters for the audit assertions.
	upsertCalls int
	createCalls int
	deleteCalls int
}

func newFakeSync() *fakeGroupSync {
	return &fakeGroupSync{
		global:        map[uuid.UUID]sqlc.GlobalRoleBinding{},
		cluster:       map[uuid.UUID]sqlc.ClusterRoleBinding{},
		project:       map[uuid.UUID]sqlc.ProjectRoleBinding{},
		manualGlobal:  map[[2]uuid.UUID]bool{},
		manualCluster: map[[3]uuid.UUID]bool{},
		manualProject: map[[3]uuid.UUID]bool{},
	}
}

func (f *fakeGroupSync) ListGroupMappingsForConnector(_ context.Context, connectorID pgtype.UUID) ([]sqlc.IdentityGroupMapping, error) {
	out := []sqlc.IdentityGroupMapping{}
	for _, m := range f.mappings {
		// Wildcard rows always match; connector-scoped rows match only
		// when the supplied connector_id agrees. This mirrors the
		// production SQL: WHERE connector_id = $1 OR connector_id IS NULL.
		switch {
		case !m.ConnectorID.Valid:
			out = append(out, m)
		case connectorID.Valid && m.ConnectorID.Bytes == connectorID.Bytes:
			out = append(out, m)
		}
	}
	return out, nil
}

func (f *fakeGroupSync) UpsertUserIDPGroups(_ context.Context, arg sqlc.UpsertUserIDPGroupsParams) (sqlc.UserIdpGroup, error) {
	f.upsertCalls++
	f.snapshot = sqlc.UserIdpGroup{
		UserID:      arg.UserID,
		ConnectorID: arg.ConnectorID,
		Groups:      arg.Groups,
		SyncedAt:    arg.SyncedAt,
	}
	return f.snapshot, nil
}

func (f *fakeGroupSync) GetUserIDPGroups(_ context.Context, _ uuid.UUID) (sqlc.UserIdpGroup, error) {
	if f.snapErr != nil {
		return sqlc.UserIdpGroup{}, f.snapErr
	}
	return f.snapshot, nil
}

func (f *fakeGroupSync) ListGroupSyncGlobalBindings(_ context.Context, userID pgtype.UUID) ([]sqlc.GlobalRoleBinding, error) {
	out := []sqlc.GlobalRoleBinding{}
	for _, b := range f.global {
		if b.UserID.Valid && userID.Valid && b.UserID.Bytes == userID.Bytes {
			out = append(out, b)
		}
	}
	return out, nil
}

func (f *fakeGroupSync) ListGroupSyncClusterBindings(_ context.Context, userID pgtype.UUID) ([]sqlc.ClusterRoleBinding, error) {
	out := []sqlc.ClusterRoleBinding{}
	for _, b := range f.cluster {
		if b.UserID.Valid && userID.Valid && b.UserID.Bytes == userID.Bytes {
			out = append(out, b)
		}
	}
	return out, nil
}

func (f *fakeGroupSync) ListGroupSyncProjectBindings(_ context.Context, userID pgtype.UUID) ([]sqlc.ProjectRoleBinding, error) {
	out := []sqlc.ProjectRoleBinding{}
	for _, b := range f.project {
		if b.UserID.Valid && userID.Valid && b.UserID.Bytes == userID.Bytes {
			out = append(out, b)
		}
	}
	return out, nil
}

func (f *fakeGroupSync) CreateGroupSyncGlobalBinding(_ context.Context, arg sqlc.CreateGroupSyncGlobalBindingParams) (sqlc.GlobalRoleBinding, error) {
	f.createCalls++
	key := [2]uuid.UUID{arg.UserID.Bytes, arg.RoleID}
	if f.manualGlobal[key] {
		// ON CONFLICT DO NOTHING — leave the manual binding alone.
		return sqlc.GlobalRoleBinding{}, pgx.ErrNoRows
	}
	row := sqlc.GlobalRoleBinding{
		ID:     uuid.New(),
		UserID: arg.UserID,
		RoleID: arg.RoleID,
		Source: "group_sync",
	}
	f.global[row.ID] = row
	return row, nil
}

func (f *fakeGroupSync) CreateGroupSyncClusterBinding(_ context.Context, arg sqlc.CreateGroupSyncClusterBindingParams) (sqlc.ClusterRoleBinding, error) {
	f.createCalls++
	key := [3]uuid.UUID{arg.UserID.Bytes, arg.RoleID, arg.ClusterID}
	if f.manualCluster[key] {
		return sqlc.ClusterRoleBinding{}, pgx.ErrNoRows
	}
	row := sqlc.ClusterRoleBinding{
		ID:        uuid.New(),
		UserID:    arg.UserID,
		RoleID:    arg.RoleID,
		ClusterID: arg.ClusterID,
		Source:    "group_sync",
	}
	f.cluster[row.ID] = row
	return row, nil
}

func (f *fakeGroupSync) CreateGroupSyncProjectBinding(_ context.Context, arg sqlc.CreateGroupSyncProjectBindingParams) (sqlc.ProjectRoleBinding, error) {
	f.createCalls++
	key := [3]uuid.UUID{arg.UserID.Bytes, arg.RoleID, arg.ProjectID}
	if f.manualProject[key] {
		return sqlc.ProjectRoleBinding{}, pgx.ErrNoRows
	}
	row := sqlc.ProjectRoleBinding{
		ID:        uuid.New(),
		UserID:    arg.UserID,
		RoleID:    arg.RoleID,
		ProjectID: arg.ProjectID,
		Source:    "group_sync",
	}
	f.project[row.ID] = row
	return row, nil
}

func (f *fakeGroupSync) DeleteGroupSyncGlobalBinding(_ context.Context, id uuid.UUID) error {
	f.deleteCalls++
	delete(f.global, id)
	return nil
}
func (f *fakeGroupSync) DeleteGroupSyncClusterBinding(_ context.Context, id uuid.UUID) error {
	f.deleteCalls++
	delete(f.cluster, id)
	return nil
}
func (f *fakeGroupSync) DeleteGroupSyncProjectBinding(_ context.Context, id uuid.UUID) error {
	f.deleteCalls++
	delete(f.project, id)
	return nil
}

// addMapping is a tiny ergonomic helper for the test setup. Wildcards
// (connector_id NULL) pass uuid.Nil; the helper wires the pgtype.UUID
// Valid flag accordingly.
func (f *fakeGroupSync) addMapping(connectorID uuid.UUID, group, scope string, roleID uuid.UUID, clusterID, projectID uuid.UUID) sqlc.IdentityGroupMapping {
	m := sqlc.IdentityGroupMapping{
		ID:        uuid.New(),
		GroupName: group,
		Scope:     scope,
		RoleID:    roleID,
	}
	if connectorID != uuid.Nil {
		m.ConnectorID = pgtype.UUID{Bytes: connectorID, Valid: true}
	}
	if clusterID != uuid.Nil {
		m.ClusterID = pgtype.UUID{Bytes: clusterID, Valid: true}
	}
	if projectID != uuid.Nil {
		m.ProjectID = pgtype.UUID{Bytes: projectID, Valid: true}
	}
	f.mappings = append(f.mappings, m)
	return m
}

// TestSyncUserGroups_AddsBindings — user claims group X, mapping X->role Y
// at global scope; we expect a group_sync binding to be created.
func TestSyncUserGroups_AddsBindings(t *testing.T) {
	f := newFakeSync()
	userID := uuid.New()
	connectorID := uuid.New()
	roleID := uuid.New()
	f.addMapping(connectorID, "engineering", "global", roleID, uuid.Nil, uuid.Nil)

	res, err := SyncUserGroups(context.Background(), f, userID,
		pgtype.UUID{Bytes: connectorID, Valid: true},
		[]string{"engineering"}, true)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if res.Skipped {
		t.Fatalf("res.Skipped = true, want false")
	}
	if len(res.Added) != 1 {
		t.Fatalf("added = %d, want 1: %+v", len(res.Added), res.Added)
	}
	if got := res.Added[0]; got.Scope != "global" || got.RoleID != roleID {
		t.Fatalf("added[0] = %+v", got)
	}
	if got := len(res.Removed); got != 0 {
		t.Fatalf("removed = %d, want 0", got)
	}
	if got := len(f.global); got != 1 {
		t.Fatalf("global table = %d rows, want 1", got)
	}
}

// TestSyncUserGroups_RemovesStaleBindings — user previously had a
// group_sync binding for group X; this login's claims no longer
// include X; the binding gets deleted.
func TestSyncUserGroups_RemovesStaleBindings(t *testing.T) {
	f := newFakeSync()
	userID := uuid.New()
	connectorID := uuid.New()
	roleID := uuid.New()

	// Pre-seed a group_sync binding that doesn't correspond to any
	// mapping the user's claims will match this round.
	existing := sqlc.GlobalRoleBinding{
		ID:     uuid.New(),
		UserID: pgtype.UUID{Bytes: userID, Valid: true},
		RoleID: roleID,
		Source: "group_sync",
	}
	f.global[existing.ID] = existing

	res, err := SyncUserGroups(context.Background(), f, userID,
		pgtype.UUID{Bytes: connectorID, Valid: true},
		[]string{}, true)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if len(res.Added) != 0 {
		t.Fatalf("added = %d, want 0", len(res.Added))
	}
	if len(res.Removed) != 1 {
		t.Fatalf("removed = %d, want 1", len(res.Removed))
	}
	if _, still := f.global[existing.ID]; still {
		t.Fatalf("binding %s still present after revocation", existing.ID)
	}
}

// TestSyncUserGroups_ManualBindingPreserved — user has a manual
// binding for role R; sync creates a group_sync binding for role S;
// even after R's mapping disappears (it never had one!), the manual
// binding stays. This is the migration's backward-compat guarantee.
func TestSyncUserGroups_ManualBindingPreserved(t *testing.T) {
	f := newFakeSync()
	userID := uuid.New()
	connectorID := uuid.New()
	manualRoleID := uuid.New()
	groupRoleID := uuid.New()

	// Pre-seed a manual (user_id, role_id) pair. The fake's ON
	// CONFLICT shim makes any group_sync insert with the same key
	// fail — which is what production would do too.
	f.manualGlobal[[2]uuid.UUID{userID, manualRoleID}] = true

	// Mapping: group "ops" -> manualRoleID at global scope. This
	// would normally create a group_sync row, but the manual binding
	// wins on conflict. We never see a row for it in f.global.
	f.addMapping(connectorID, "ops", "global", manualRoleID, uuid.Nil, uuid.Nil)

	// And a distinct mapping that should result in an actual
	// group_sync binding: "platform" -> groupRoleID.
	f.addMapping(connectorID, "platform", "global", groupRoleID, uuid.Nil, uuid.Nil)

	res, err := SyncUserGroups(context.Background(), f, userID,
		pgtype.UUID{Bytes: connectorID, Valid: true},
		[]string{"ops", "platform"}, true)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	// One group_sync binding (groupRoleID); the manual one stays
	// untouched (it's not in f.global because manual bindings live
	// in the production *_role_bindings table that we don't model
	// here — the fake's manualGlobal map IS the manual binding).
	if len(res.Added) != 1 || res.Added[0].RoleID != groupRoleID {
		t.Fatalf("added = %+v; want one entry for %s", res.Added, groupRoleID)
	}
	if _, gone := f.manualGlobal[[2]uuid.UUID{userID, manualRoleID}]; !gone {
		t.Fatalf("manual binding key disappeared from fake state")
	}

	// Subsequent sync where "ops" is gone — the group_sync set must
	// not touch the manual binding. We model this by re-running with
	// just "platform" still claimed: the manual binding for
	// manualRoleID is untouched (we never created a group_sync row
	// for it, so there's nothing to delete; the fake's manualGlobal
	// map is unchanged).
	if _, err := SyncUserGroups(context.Background(), f, userID,
		pgtype.UUID{Bytes: connectorID, Valid: true},
		[]string{"platform"}, true); err != nil {
		t.Fatalf("sync second pass: %v", err)
	}
	if _, gone := f.manualGlobal[[2]uuid.UUID{userID, manualRoleID}]; !gone {
		t.Fatalf("manual binding evicted after group disappeared (must not happen)")
	}
}

// TestSyncUserGroups_WildcardConnector — mapping with NULL connector_id
// matches groups from any connector. Pass two different connector_ids
// and assert both sync runs create the binding.
func TestSyncUserGroups_WildcardConnector(t *testing.T) {
	f := newFakeSync()
	userID := uuid.New()
	roleID := uuid.New()
	f.addMapping(uuid.Nil, "everyone", "global", roleID, uuid.Nil, uuid.Nil)

	// First connector
	connA := uuid.New()
	res, err := SyncUserGroups(context.Background(), f, userID,
		pgtype.UUID{Bytes: connA, Valid: true},
		[]string{"everyone"}, true)
	if err != nil {
		t.Fatalf("sync A: %v", err)
	}
	if len(res.Added) != 1 {
		t.Fatalf("connector A: added = %d, want 1", len(res.Added))
	}

	// Wipe the in-fake binding and try a different connector. The
	// wildcard mapping should still match.
	for k := range f.global {
		delete(f.global, k)
	}
	connB := uuid.New()
	res, err = SyncUserGroups(context.Background(), f, userID,
		pgtype.UUID{Bytes: connB, Valid: true},
		[]string{"everyone"}, true)
	if err != nil {
		t.Fatalf("sync B: %v", err)
	}
	if len(res.Added) != 1 {
		t.Fatalf("connector B: added = %d, want 1", len(res.Added))
	}
}

// TestSyncUserGroups_ConnectorScopedMapping — mapping with a specific
// connector_id only matches calls with that connector_id. A call
// from a different connector gets nothing.
func TestSyncUserGroups_ConnectorScopedMapping(t *testing.T) {
	f := newFakeSync()
	userID := uuid.New()
	roleID := uuid.New()
	connOkta := uuid.New()
	f.addMapping(connOkta, "engineering", "global", roleID, uuid.Nil, uuid.Nil)

	// Call from a *different* connector — must not match.
	connGitHub := uuid.New()
	res, err := SyncUserGroups(context.Background(), f, userID,
		pgtype.UUID{Bytes: connGitHub, Valid: true},
		[]string{"engineering"}, true)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if len(res.Added) != 0 {
		t.Fatalf("cross-connector match: added = %d, want 0; res=%+v", len(res.Added), res)
	}

	// Call from the right connector — must match.
	res, err = SyncUserGroups(context.Background(), f, userID,
		pgtype.UUID{Bytes: connOkta, Valid: true},
		[]string{"engineering"}, true)
	if err != nil {
		t.Fatalf("sync (right connector): %v", err)
	}
	if len(res.Added) != 1 {
		t.Fatalf("right-connector match: added = %d, want 1", len(res.Added))
	}
}

// TestSyncUserGroups_ClaimsUnavailable — the claims-unavailable
// sentinel MUST NOT delete existing group_sync bindings. This is the
// local-admin / password-reset path described in the constraints.
func TestSyncUserGroups_ClaimsUnavailable(t *testing.T) {
	f := newFakeSync()
	userID := uuid.New()
	roleID := uuid.New()
	// Pre-seed a group_sync binding the sentinel must preserve.
	existing := sqlc.GlobalRoleBinding{
		ID:     uuid.New(),
		UserID: pgtype.UUID{Bytes: userID, Valid: true},
		RoleID: roleID,
		Source: "group_sync",
	}
	f.global[existing.ID] = existing

	res, err := SyncUserGroups(context.Background(), f, userID, pgtype.UUID{}, nil, false)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if !res.Skipped {
		t.Fatalf("res.Skipped = false, want true (claims unavailable)")
	}
	if _, still := f.global[existing.ID]; !still {
		t.Fatalf("group_sync binding deleted when claims were unavailable")
	}
	if f.upsertCalls != 0 || f.createCalls != 0 || f.deleteCalls != 0 {
		t.Fatalf("unexpected DB calls under claims-unavailable: upsert=%d create=%d delete=%d",
			f.upsertCalls, f.createCalls, f.deleteCalls)
	}
}

// TestSyncUserGroups_ClusterAndProjectScope — exercises the cluster +
// project scope branches end-to-end, which the global-scope tests
// don't reach.
func TestSyncUserGroups_ClusterAndProjectScope(t *testing.T) {
	f := newFakeSync()
	userID := uuid.New()
	connectorID := uuid.New()

	clusterRoleID := uuid.New()
	clusterID := uuid.New()
	f.addMapping(connectorID, "cluster-ops", "cluster", clusterRoleID, clusterID, uuid.Nil)

	projectRoleID := uuid.New()
	projectID := uuid.New()
	f.addMapping(connectorID, "project-leads", "project", projectRoleID, uuid.Nil, projectID)

	res, err := SyncUserGroups(context.Background(), f, userID,
		pgtype.UUID{Bytes: connectorID, Valid: true},
		[]string{"cluster-ops", "project-leads"}, true)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if len(res.Added) != 2 {
		t.Fatalf("added = %d, want 2", len(res.Added))
	}
	if len(f.cluster) != 1 || len(f.project) != 1 {
		t.Fatalf("scopes not populated: cluster=%d project=%d", len(f.cluster), len(f.project))
	}
}

// TestSyncUserGroups_NilQuerier — defensive: nil arg returns an error
// rather than panicking. Same for zero userID.
func TestSyncUserGroups_DefensiveErrors(t *testing.T) {
	if _, err := SyncUserGroups(context.Background(), nil, uuid.New(), pgtype.UUID{}, nil, true); err == nil {
		t.Fatalf("nil querier should fail")
	}
	f := newFakeSync()
	if _, err := SyncUserGroups(context.Background(), f, uuid.Nil, pgtype.UUID{}, nil, true); err == nil {
		t.Fatalf("zero userID should fail")
	}
}
