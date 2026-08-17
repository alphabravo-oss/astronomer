package rollout

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

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
