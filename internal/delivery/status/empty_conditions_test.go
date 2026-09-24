package status

import (
	"context"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// Agents legitimately omit conditions while deleting or before a reconciler
// exists. The durable JSON contract requires an array, never JSON null.
func TestIngestPersistsEmptyConditionsAsArray(t *testing.T) {
	for _, phase := range []string{"pending", "deleting", "removed"} {
		t.Run(phase, func(t *testing.T) {
			tx := &fakeTransaction{current: sqlc.ClusterDeployment{
				ID: deploymentID, ClusterID: clusterID, DesiredGeneration: 7,
				DesiredSpecDigest: "sha256:" + strings.Repeat("a", 64), Phase: "applying",
			}}
			payload := validStatus()
			payload.Deployments[0].Phase = phase
			payload.Deployments[0].Conditions = nil
			payload.StatusDigest = payload.SemanticDigest()
			if err := NewIngester(&fakeRunner{tx: tx}).Ingest(context.Background(), clusterID, connectionID, "session", payload); err != nil {
				t.Fatal(err)
			}
			if tx.updated == nil || string(tx.updated.Conditions) != "[]" {
				t.Fatalf("empty conditions must persist as an array: %+v", tx.updated)
			}
		})
	}
}
