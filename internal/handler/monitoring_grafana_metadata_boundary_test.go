package handler

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

type grafanaMetadataBoundaryQuerier struct {
	*stackLifecycleQuerier
	writes int
}

func (q *grafanaMetadataBoundaryQuerier) UpsertDefaultMonitoringBackend(ctx context.Context, arg sqlc.UpsertDefaultMonitoringBackendParams) (sqlc.MonitoringBackend, error) {
	q.writes++
	return q.stackLifecycleQuerier.UpsertDefaultMonitoringBackend(ctx, arg)
}

func grafanaMetadataBoundaryFixture(t *testing.T, replicas string) (*MonitoringHandler, *grafanaMetadataBoundaryQuerier, *grafanaFolderK8sFake, []byte) {
	t.Helper()
	h, base := newStackLifecycleHandler(t)
	base.backend.AuthConfig = []byte(fmt.Sprintf(`{"sharedGrafana":{"managementClusterId":%q,"namespace":"monitoring","releaseName":"astronomer-grafana","status":"degraded","replicas":%s},"sharedThanos":{"status":"healthy","releaseName":"thanos","namespace":"monitoring"}}`, stackTestClusterID, replicas))
	base.backend.AuthConfigEncrypted = ""
	original := bytes.Clone(base.backend.AuthConfig)
	q := &grafanaMetadataBoundaryQuerier{stackLifecycleQuerier: base}
	peer := newGrafanaFolderK8sFake()
	h.queries = q
	h.requester = peer
	return h, q, peer, original
}

func TestGrafanaMetadataNumericBoundaryBeforeDatasourceEffects(t *testing.T) {
	for _, replicas := range []string{"2147483648", "-2147483649", "1.25"} {
		t.Run(replicas, func(t *testing.T) {
			h, q, peer, original := grafanaMetadataBoundaryFixture(t, replicas)
			if err := h.syncGrafanaThanosDatasource(context.Background()); err == nil {
				t.Error("invalid stored replica number was accepted")
			}
			if len(peer.calls) != 0 {
				t.Errorf("invalid metadata reached Kubernetes: %d calls", len(peer.calls))
			}
			if q.writes != 0 || !bytes.Equal(original, q.backend.AuthConfig) || len(q.audits) != 0 {
				t.Error("invalid metadata reached a durable writer")
			}
		})
	}
}

func TestGrafanaMetadataNumericBoundaryBeforeFolderEffects(t *testing.T) {
	for _, replicas := range []string{"2147483648", "-2147483649", "1.25"} {
		t.Run(replicas, func(t *testing.T) {
			h, q, peer, original := grafanaMetadataBoundaryFixture(t, replicas)
			if err := h.ReconcileGrafanaClusterFolders(context.Background()); err == nil {
				t.Error("invalid stored replica number was accepted")
			}
			if len(peer.calls) != 0 {
				t.Errorf("invalid metadata reached Kubernetes: %d calls", len(peer.calls))
			}
			if q.writes != 0 || !bytes.Equal(original, q.backend.AuthConfig) || len(q.audits) != 0 {
				t.Error("invalid metadata reached a durable writer")
			}
		})
	}
}
