package handler

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
	"github.com/google/uuid"
)

type standaloneMonitoringQueries struct {
	MonitoringQuerier
	row sqlc.GetClusterMonitoringContextRow
}

func (q standaloneMonitoringQueries) GetClusterMonitoringContext(context.Context, uuid.UUID) (sqlc.GetClusterMonitoringContextRow, error) {
	return q.row, nil
}

func TestStandaloneMonitoringSummaryUsesClusterAgent(t *testing.T) {
	id := uuid.NewString()
	q := standaloneMonitoringQueries{row: sqlc.GetClusterMonitoringContextRow{QueryUrl: "http://wrong-shared-backend", LastAppliedSpecHash: "installed", Status: "healthy", StackNamespace: "monitoring", PrometheusReleaseName: "demo"}}
	requester := &stubK8sRequester{respFn: func(req stubReq) (*protocol.K8sResponsePayload, error) {
		if req.ClusterID != id {
			t.Fatalf("queried another cluster: %s", req.ClusterID)
		}
		body := `{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[1,"7"]}]}}`
		if strings.Contains(req.Path, "services?labelSelector") {
			body = `{"items":[{"metadata":{"name":"demo-prometheus"},"spec":{"ports":[{"port":9090}]}}]}`
		} else if strings.Contains(req.Path, "cluster_id%3D") {
			t.Fatal("local TSDB incorrectly filtered by external labels")
		}
		return &protocol.K8sResponsePayload{StatusCode: 200, Body: base64.StdEncoding.EncodeToString([]byte(body))}, nil
	}}
	h := NewMonitoringHandlerWithQueries(q, requester)
	summary, ok, err := h.realClusterSummary(context.Background(), id)
	if err != nil || !ok || summary == nil {
		t.Fatalf("summary=%v ok=%v err=%v", summary, ok, err)
	}
	if got := labelSelectorForConfig(monitoringContext{Local: true}); got != `job=~".*"` {
		t.Fatalf("selector=%s", got)
	}
	if _, _, _, err := NewMonitoringHandlerWithQueries(q, nil).backendClient(context.Background(), id); err == nil {
		t.Fatal("missing cluster agent silently fell back to shared backend")
	}
}
