package rollout

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	deliveryconfig "github.com/alphabravocompany/astronomer-go/internal/delivery/configuration"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/placement"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

func TestOverrideAppliesUsesExactFrozenCandidateScope(t *testing.T) {
	targetID := uuid.MustParse("10000000-0000-4000-8000-000000000001")
	clusterID := uuid.MustParse("20000000-0000-4000-8000-000000000002")
	groupID := uuid.MustParse("30000000-0000-4000-8000-000000000003")
	candidate := &placement.Candidate{ID: clusterID, GroupIDs: []uuid.UUID{groupID}}
	tests := []struct {
		scope deliveryconfig.Scope
		id    uuid.UUID
		want  bool
	}{
		{deliveryconfig.ScopeOrganization, uuid.Nil, true},
		{deliveryconfig.ScopeProject, uuid.Nil, true},
		{deliveryconfig.ScopeCluster, clusterID, true},
		{deliveryconfig.ScopeCluster, targetID, false},
		{deliveryconfig.ScopeGroup, groupID, true},
		{deliveryconfig.ScopeEnvironment, groupID, true},
		{deliveryconfig.ScopeRollout, targetID, true},
		{deliveryconfig.ScopeRollout, clusterID, false},
	}
	for _, test := range tests {
		override := sqlc.DeliveryOverrideSet{ScopeType: string(test.scope)}
		if test.id != uuid.Nil {
			override.ScopeID = pgtype.UUID{Bytes: test.id, Valid: true}
		}
		if got := overrideApplies(targetID, override, candidate); got != test.want {
			t.Errorf("scope %s id %s applies = %v, want %v", test.scope, test.id, got, test.want)
		}
	}
}

func TestPostgresStoreStrictMetadataDecoding(t *testing.T) {
	t.Parallel()
	var decoded struct {
		Enabled bool `json:"enabled"`
	}
	for _, raw := range []string{
		`{"enabled":true,"unknown":false}`,
		`{"enabled":true} {}`,
		``,
	} {
		if err := decodeStrict([]byte(raw), &decoded); err == nil {
			t.Fatalf("invalid metadata %q was accepted", raw)
		}
	}
	if err := decodeStrict([]byte(`{"enabled":true}`), &decoded); err != nil || !decoded.Enabled {
		t.Fatalf("valid metadata decoded as %+v: %v", decoded, err)
	}

	capabilities, err := deliveryCapabilities("v2.9.3", []byte(`{"source-controller":"v1.7.4","helm-controller":"v1.4.5"}`))
	if err != nil {
		t.Fatal(err)
	}
	if capabilities[protocol.FeatureDeliverySourceHelmHTTP] != "v1.7.4" || capabilities[protocol.FeatureDeliveryRendererHelm] != "v1.4.5" ||
		capabilities[protocol.FeatureDeliveryPlatformScope] != "" {
		t.Fatalf("capabilities = %#v", capabilities)
	}
	if _, err := deliveryCapabilities("v2.9.3", []byte(`{"source-controller":7}`)); err == nil {
		t.Fatal("non-string controller version was accepted")
	}
}

func TestPlannerIdempotencyKeyMatchesDatabaseBoundary(t *testing.T) {
	t.Parallel()
	snapshot, preview := testSnapshot(t, 1)
	request := testCreateRequest(preview, testStrategy("rolling", 1))
	request.IdempotencyKey = strings.Repeat("a", MaxIdempotencyKeyLength)
	if _, err := mustPlanner(t, newMemoryPlanningStore(snapshot)).Create(t.Context(), request); err != nil {
		t.Fatalf("maximum database idempotency key was rejected: %v", err)
	}
	request.IdempotencyKey += "b"
	if _, err := mustPlanner(t, newMemoryPlanningStore(snapshot)).Create(t.Context(), request); !HasCode(err, CodeInvalidInput) {
		t.Fatalf("overlong database idempotency key = %v", err)
	}
}

func TestCommonPreviousVersionRequiresEveryClusterToMatch(t *testing.T) {
	t.Parallel()
	version := uuid.New()
	clusters := []PlannedCluster{
		{Previous: &PreviousDeployment{Version: VersionIdentity{BundleVersionID: version}}},
		{Previous: &PreviousDeployment{Version: VersionIdentity{BundleVersionID: version}}},
	}
	if got := commonPreviousVersion(clusters); !got.Valid || got.Bytes != version {
		t.Fatalf("common previous version = %+v", got)
	}
	clusters[1].Previous = nil
	if got := commonPreviousVersion(clusters); got.Valid {
		t.Fatalf("partial previous version was treated as common: %+v", got)
	}
}
