package auth

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestSyncUserGroupsRejectsMissingMappingScopeID(t *testing.T) {
	for _, scope := range []string{"cluster", "project"} {
		t.Run(scope, func(t *testing.T) {
			q := newFakeSync()
			q.addMapping(uuid.Nil, "operators", scope, uuid.New(), uuid.Nil, uuid.Nil)
			result, err := SyncUserGroups(context.Background(), q, uuid.New(), pgtype.UUID{}, []string{"operators"}, true)
			if err == nil || len(result.Added)+len(result.Removed) != 0 {
				t.Fatal("invalid mapping was reported as a successful reconciliation")
			}
		})
	}
}
