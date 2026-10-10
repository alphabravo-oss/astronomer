package provider

import (
	"context"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/distribution"
	"github.com/google/uuid"
)

func TestDatabaseImportCannotGrantAuthorityFromUnownedDataOrRelabelFailedAttempt(t *testing.T) {
	if _, err := NewDatabaseImportOwner(context.Background(), nil, nil, nil, "operator.invalid/update"); err == nil {
		t.Fatal("zero repository/private constructor granted import authority")
	}
	var owner DatabaseImportOwner
	if err := owner.AcceptCopied(t.Context(), nil, nil); err == nil {
		t.Fatal("caller supplied missing opaque import capture")
	}
	for _, phase := range []string{"reserved", "failed", "revoked", "", "imported-by-caller"} {
		if databaseCompleteAttemptPhase(phase) {
			t.Fatal("uncompleted decision matched imported/published enum")
		}
	}
	if !databaseCompleteAttemptPhase("published") || !databaseCompleteAttemptPhase("imported") {
		t.Fatal("accurate complete dispositions unavailable")
	}
	captured := databaseAcceptanceDataFixture()
	base := sqlc.VulnerabilityDatabaseBasis{ID: uuid.New(), Epoch: 2, AcceptedGeneration: 2}
	attempt := uuid.New()
	snapshot := distribution.DatabaseAuthoritySnapshot{TrustMaterialFingerprint: "trust"}
	snapshot.Base.PolicyFingerprint = "policy"
	row := sqlc.VulnerabilityDatabaseUpdate{ID: uuid.New(), AttemptID: attempt, AttemptPhase: "imported", DecisionDigest: "decision", BaseID: base.ID, BaseEpoch: 2, Generation: 2, FeedDigest: captured.feed, PolicyFingerprint: "policy", TrustMaterialFingerprint: "trust", PublishedAt: captured.document.PublishedAt, ExpiresAt: captured.document.ExpiresAt}
	if !databaseAcceptedUpdateMatches(row, base, attempt, "decision", snapshot, captured) {
		t.Fatal("accurate imported record DATA rejected")
	}
	row.AttemptPhase = "failed"
	if databaseAcceptedUpdateMatches(row, base, attempt, "decision", snapshot, captured) {
		t.Fatal("failed decision resurrected by imported matcher")
	}
	// This comparator is not an authority grant. Actual acceptance separately
	// locks the FK-linked attempt and requires its exact phase equals this row.
}
