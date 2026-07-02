package auth

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// TestSyncUserGroups_WildcardGrantStampedNullProvenance is the sweep-4
// Finding #1 regression. A wildcard mapping (connector_id NULL) that a user
// first exercises via connector A must have its group_sync binding stamped
// with the MAPPING's provenance (NULL), not the login connector A. Stamping
// the login connector was the sweep-3 regression: once the operator deleted
// the wildcard mapping, a login via connector B (whose scoped enumeration
// never returned the A-stamped row) could never revoke the grant, so the
// user kept the (possibly admin) role indefinitely.
func TestSyncUserGroups_WildcardGrantStampedNullProvenance(t *testing.T) {
	f := newFakeSync()
	userID := uuid.New()
	roleID := uuid.New()

	// Wildcard mapping: "admins" -> roleID at global scope, any connector.
	f.addMapping(uuid.Nil, "admins", "global", roleID, uuid.Nil, uuid.Nil)

	// User first logs in via connector A claiming the group.
	connA := uuid.New()
	res, err := SyncUserGroups(context.Background(), f, userID,
		pgtype.UUID{Bytes: connA, Valid: true},
		[]string{"admins"}, true)
	if err != nil {
		t.Fatalf("sync A: %v", err)
	}
	if len(res.Added) != 1 {
		t.Fatalf("added = %d, want 1", len(res.Added))
	}
	if len(f.global) != 1 {
		t.Fatalf("global table = %d rows, want 1", len(f.global))
	}

	// The binding must carry the wildcard mapping's NULL provenance, NOT
	// connector A. This is the core of the fix.
	var stamped sqlc.GlobalRoleBinding
	for _, b := range f.global {
		stamped = b
	}
	if stamped.GroupSyncConnectorID.Valid {
		t.Fatalf("wildcard grant stamped connector %x; want NULL provenance",
			stamped.GroupSyncConnectorID.Bytes)
	}

	// Operator revokes the wildcard mapping fleet-wide.
	f.mappings = nil

	// The user now only logs in via a DIFFERENT connector, B. The role is
	// no longer wanted, and because the binding is NULL-stamped it is
	// enumerated on B's sync and revoked. (Pre-fix: stamped A, never
	// enumerated by B, retained forever.)
	connB := uuid.New()
	res, err = SyncUserGroups(context.Background(), f, userID,
		pgtype.UUID{Bytes: connB, Valid: true},
		[]string{"admins"}, true)
	if err != nil {
		t.Fatalf("sync B: %v", err)
	}
	if len(res.Removed) != 1 {
		t.Fatalf("removed = %d, want 1 (wildcard grant must be revoked)", len(res.Removed))
	}
	if len(f.global) != 0 {
		t.Fatalf("global table = %d rows, want 0; wildcard grant over-retained", len(f.global))
	}
}

// TestSyncUserGroups_LegacyNullBindingRevoked is the sweep-4 Finding #2
// regression. A binding that predates migration 128 carries a NULL
// group_sync_connector_id. After upgrade, a user's login through a named
// connector must still enumerate — and revoke — that legacy row once no
// mapping covers it. Pre-fix, the scoped enumeration used
// `IS NOT DISTINCT FROM <uuid>`, which never matched NULL rows, so the
// legacy (possibly admin) binding was retained indefinitely.
func TestSyncUserGroups_LegacyNullBindingRevoked(t *testing.T) {
	f := newFakeSync()
	userID := uuid.New()
	roleID := uuid.New()

	// Legacy pre-128 binding: source='group_sync', provenance NULL.
	legacy := sqlc.GlobalRoleBinding{
		ID:     uuid.New(),
		UserID: pgtype.UUID{Bytes: userID, Valid: true},
		RoleID: roleID,
		Source: "group_sync",
		// GroupSyncConnectorID left zero-value == NULL.
	}
	f.global[legacy.ID] = legacy

	// No mapping covers the role. User logs in via a named connector.
	connA := uuid.New()
	res, err := SyncUserGroups(context.Background(), f, userID,
		pgtype.UUID{Bytes: connA, Valid: true},
		[]string{}, true)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if len(res.Removed) != 1 {
		t.Fatalf("removed = %d, want 1 (legacy NULL binding must be revoked)", len(res.Removed))
	}
	if _, still := f.global[legacy.ID]; still {
		t.Fatalf("legacy NULL binding %s over-retained after mapping removal", legacy.ID)
	}
}

// TestSyncUserGroups_LegacyNullBindingPreservedWhenStillMapped is the
// fail-open half of Finding #2: enumerating NULL rows must NOT revoke a
// legacy/wildcard grant that the current mapping set still covers. A legit
// wildcard entitlement survives the reconcile (and isn't duplicated).
func TestSyncUserGroups_LegacyNullBindingPreservedWhenStillMapped(t *testing.T) {
	f := newFakeSync()
	userID := uuid.New()
	roleID := uuid.New()

	legacy := sqlc.GlobalRoleBinding{
		ID:     uuid.New(),
		UserID: pgtype.UUID{Bytes: userID, Valid: true},
		RoleID: roleID,
		Source: "group_sync",
	}
	f.global[legacy.ID] = legacy

	// A wildcard mapping still grants the role to the claimed group.
	f.addMapping(uuid.Nil, "everyone", "global", roleID, uuid.Nil, uuid.Nil)

	connA := uuid.New()
	res, err := SyncUserGroups(context.Background(), f, userID,
		pgtype.UUID{Bytes: connA, Valid: true},
		[]string{"everyone"}, true)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if len(res.Removed) != 0 {
		t.Fatalf("removed = %d, want 0 (still-mapped grant must survive)", len(res.Removed))
	}
	if len(res.Added) != 0 {
		t.Fatalf("added = %d, want 0 (must not duplicate the legacy binding)", len(res.Added))
	}
	if _, still := f.global[legacy.ID]; !still {
		t.Fatalf("still-mapped legacy binding %s was wrongly revoked", legacy.ID)
	}
	if len(f.global) != 1 {
		t.Fatalf("global table = %d rows, want 1 (no duplicate)", len(f.global))
	}
}

// TestSyncUserGroups_NamedConnectorGrantStaysScoped guards the multi-connector
// invariant the sweep-3 fix protected: a grant from a NAMED connector B is
// stamped with B (not login A, not NULL) and is NOT enumerated/revoked by a
// login through connector A.
func TestSyncUserGroups_NamedConnectorGrantStaysScoped(t *testing.T) {
	f := newFakeSync()
	userID := uuid.New()
	roleID := uuid.New()
	connB := uuid.New()

	// A named-connector mapping the user exercises via B.
	f.addMapping(connB, "eng", "global", roleID, uuid.Nil, uuid.Nil)
	res, err := SyncUserGroups(context.Background(), f, userID,
		pgtype.UUID{Bytes: connB, Valid: true},
		[]string{"eng"}, true)
	if err != nil {
		t.Fatalf("sync B: %v", err)
	}
	if len(res.Added) != 1 {
		t.Fatalf("added = %d, want 1", len(res.Added))
	}
	var stamped sqlc.GlobalRoleBinding
	for _, b := range f.global {
		stamped = b
	}
	if !stamped.GroupSyncConnectorID.Valid || stamped.GroupSyncConnectorID.Bytes != connB {
		t.Fatalf("named-connector grant provenance = %+v; want connector B", stamped.GroupSyncConnectorID)
	}

	// A login through connector A with no matching claims must leave B's
	// grant untouched.
	connA := uuid.New()
	res, err = SyncUserGroups(context.Background(), f, userID,
		pgtype.UUID{Bytes: connA, Valid: true},
		[]string{}, true)
	if err != nil {
		t.Fatalf("sync A: %v", err)
	}
	if len(res.Removed) != 0 {
		t.Fatalf("connector A revoked %d of connector B's grants", len(res.Removed))
	}
	if _, still := f.global[stamped.ID]; !still {
		t.Fatalf("connector B grant %s revoked by a connector A sync", stamped.ID)
	}
}
