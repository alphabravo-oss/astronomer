package provider

import (
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/alphabravocompany/astronomer-go/internal/callerid"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

func TestOwnedDatabaseScanRequiresPrivateCurrentConstructorAndCannotPromoteUserOrigin(t *testing.T) {
	if owner, err := NewDatabaseScanOwner(t.Context(), nil, nil, nil, &ownedScanRecordingRequester{}, "registry.example.test/feed", nil, nil, nil); err == nil || owner != nil {
		t.Fatal("caller DATA created physical/current scan owner")
	}
	peer := &ownedScanRecordingRequester{}
	owner := &DatabaseScanOwner{pool: new(pgxpool.Pool), requester: peer}
	owner.Run(callerid.WithUser(t.Context(), uuid.New()), nil)
	owner.Run(callerid.WithMachine(t.Context(), callerid.SourceAgentLifecycle), nil)
	if peer.calls != 0 {
		t.Fatal("invalid constructor/origin performed autonomous cluster requests")
	}
	current := sqlc.VulnerabilityDatabaseClusterState{ClusterID: uuid.New(), BaseID: uuid.New(), BaseEpoch: 1, UpdateID: uuid.New(), UpdateGeneration: 1, TargetID: uuid.New(), TargetGeneration: 1, AssignmentGeneration: 1, AssignmentDigest: "actual", AssignmentEtag: "actual-etag", AssignmentCredentialEpoch: 1, AssignmentCredentialDigest: "actual-credentials"}
	claim := sqlc.VulnerabilityDatabaseScanClaim{ClusterID: current.ClusterID, BaseID: current.BaseID, BaseEpoch: current.BaseEpoch, UpdateID: current.UpdateID, UpdateGeneration: current.UpdateGeneration, TargetID: current.TargetID, TargetGeneration: current.TargetGeneration, AssignmentGeneration: current.AssignmentGeneration, AssignmentDigest: current.AssignmentDigest, AssignmentEtag: current.AssignmentEtag, AssignmentCredentialEpoch: current.AssignmentCredentialEpoch, AssignmentCredentialDigest: current.AssignmentCredentialDigest}
	if !databaseScanClaimMatchesState(claim, current) {
		t.Fatal("otherwise exact DATA tuple differs")
	}
	claim.AssignmentGeneration++
	if databaseScanClaimMatchesState(claim, current) {
		t.Fatal("stale actual generation inherited current state")
	}
}
