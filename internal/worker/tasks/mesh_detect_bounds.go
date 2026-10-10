package tasks

import (
	"fmt"
	"math"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/mesh"
)

// meshDetectionParams admits the complete observation before returning its
// database parameters. An invalid observation cannot replace the last valid
// row or publish a successful mesh transition.
func meshDetectionParams(clusterID uuid.UUID, det mesh.Detection) (sqlc.UpsertClusterServiceMeshParams, error) {
	params := sqlc.UpsertClusterServiceMeshParams{
		ClusterID:             clusterID,
		DetectedMesh:          det.Mesh,
		DetectedVersion:       det.Version,
		ControlPlaneNamespace: det.ControlPlaneNamespace,
		LastError:             joinErrors(det.Errors),
	}
	fields := []struct {
		name        string
		value       int
		destination *int32
	}{
		{"gateway_count", det.GatewayCount, &params.GatewayCount},
		{"virtual_service_count", det.VirtualServiceCount, &params.VirtualServiceCount},
		{"destination_rule_count", det.DestinationRuleCount, &params.DestinationRuleCount},
		{"peer_auth_count", det.PeerAuthCount, &params.PeerAuthenticationCount},
		{"service_profile_count", det.ServiceProfileCount, &params.ServiceProfileCount},
		{"server_auth_count", det.ServerAuthCount, &params.ServerAuthCount},
	}
	for _, field := range fields {
		count, err := meshCount32(field.name, field.value)
		if err != nil {
			return sqlc.UpsertClusterServiceMeshParams{}, err
		}
		*field.destination = count
	}
	if det.MTLSCoveragePct < 0 || det.MTLSCoveragePct > 100 {
		return sqlc.UpsertClusterServiceMeshParams{}, fmt.Errorf("mesh detection mtls_coverage_pct is outside its supported range")
	}
	params.MtlsCoveragePct = int32(det.MTLSCoveragePct)
	return params, nil
}

func meshCount32(name string, value int) (int32, error) {
	if value < 0 || value > math.MaxInt32 {
		return 0, fmt.Errorf("mesh detection %s is outside its supported range", name)
	}
	return int32(value), nil
}
