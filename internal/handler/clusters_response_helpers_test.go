package handler

import (
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

func mustClusterResponse(t *testing.T, cluster sqlc.Cluster) ClusterResponse {
	t.Helper()
	response, err := clusterToResponse(cluster)
	if err != nil {
		t.Fatalf("clusterToResponse: %v", err)
	}
	return response
}

func mustRenderAgentInstallManifest(t *testing.T, handler *ClusterHandler, cluster sqlc.Cluster, token, serverURL string) string {
	t.Helper()
	manifest, err := handler.renderAgentInstallManifest(cluster, token, serverURL)
	if err != nil {
		t.Fatalf("renderAgentInstallManifest: %v", err)
	}
	return manifest
}
