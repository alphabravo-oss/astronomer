package handler

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// legacyClusterWithMetrics is a snapshot of the pre-DTO wire shape: it embeds
// sqlc.Cluster anonymously so every column is marshaled at the top level, then
// tacks on the metric scalars. We keep it here (not in production code) purely
// to pin the wire contract for TestClusterResponse_WireCompat.
type legacyClusterWithMetrics struct {
	sqlc.Cluster
	CPUPercentage    float64 `json:"cpu_percentage"`
	MemoryPercentage float64 `json:"memory_percentage"`
	PodCount         int     `json:"pod_count"`
}

// fixtureCluster returns a sqlc.Cluster populated across every column type
// (string, int32, bool, json.RawMessage, time.Time, pgtype.Timestamptz,
// pgtype.UUID — both Valid=true and Valid=false variants where applicable).
// Used by TestClusterResponse_WireCompat to confirm clusterToResponse emits
// the exact same JSON bytes the legacy embed-sqlc.Cluster shape did.
func fixtureCluster(t *testing.T, withOptionals bool) sqlc.Cluster {
	t.Helper()
	clusterID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	creatorID := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	createdAt := time.Date(2026, 1, 15, 10, 30, 0, 0, time.UTC)
	updatedAt := time.Date(2026, 2, 20, 12, 45, 30, 123456789, time.UTC)
	hb := time.Date(2026, 5, 10, 8, 15, 0, 0, time.UTC)
	dec := time.Date(2026, 5, 11, 9, 0, 0, 0, time.UTC)

	c := sqlc.Cluster{
		ID:                clusterID,
		Name:              "prod-east",
		DisplayName:       "Prod East",
		Description:       "Production east region",
		Status:            "active",
		ApiServerUrl:      "https://k8s.example.com",
		CaCertificate:     "-----BEGIN CERTIFICATE-----\nMIIB...\n-----END CERTIFICATE-----",
		Environment:       "production",
		Region:            "us-east-1",
		Provider:          "aws",
		Labels:            json.RawMessage(`{"team":"platform"}`),
		Annotations:       json.RawMessage(`{"owner":"sre"}`),
		Distribution:      "eks",
		AgentVersion:      "v1.2.3",
		KubernetesVersion: "v1.30.2",
		NodeCount:         12,
		CreatedAt:         createdAt,
		UpdatedAt:         updatedAt,
		IsLocal:           false,
	}
	if withOptionals {
		c.LastHeartbeat = pgtype.Timestamptz{Time: hb, Valid: true}
		c.DecommissionedAt = pgtype.Timestamptz{Time: dec, Valid: true}
		c.CreatedByID = pgtype.UUID{Bytes: creatorID, Valid: true}
	}
	return c
}

// TestClusterResponse_WireCompat asserts that clusterToResponse + the metric
// scalars produce byte-for-byte identical JSON to the legacy
// clusterWithMetrics{Cluster: c, ...} shape. This is the load-bearing
// guarantee that the DTO migration didn't change the dashboard API contract.
func TestClusterResponse_WireCompat(t *testing.T) {
	cases := []struct {
		name    string
		cluster sqlc.Cluster
		cpu     float64
		mem     float64
		pods    int
	}{
		{
			name:    "all fields populated",
			cluster: fixtureCluster(t, true),
			cpu:     42.5,
			mem:     63.1,
			pods:    87,
		},
		{
			name:    "optionals invalid (null pgtype)",
			cluster: fixtureCluster(t, false),
			cpu:     0,
			mem:     0,
			pods:    0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			legacy := legacyClusterWithMetrics{
				Cluster:          tc.cluster,
				CPUPercentage:    tc.cpu,
				MemoryPercentage: tc.mem,
				PodCount:         tc.pods,
			}
			legacyJSON, err := json.Marshal(legacy)
			if err != nil {
				t.Fatalf("marshal legacy: %v", err)
			}

			resp := clusterToResponse(tc.cluster)
			resp.CPUPercentage = tc.cpu
			resp.MemoryPercentage = tc.mem
			resp.PodCount = tc.pods
			dtoJSON, err := json.Marshal(resp)
			if err != nil {
				t.Fatalf("marshal dto: %v", err)
			}

			if !bytes.Equal(legacyJSON, dtoJSON) {
				t.Fatalf("wire mismatch:\n legacy: %s\n dto:    %s", legacyJSON, dtoJSON)
			}
		})
	}
}

