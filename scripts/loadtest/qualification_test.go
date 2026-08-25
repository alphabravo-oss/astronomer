package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestResourceScenariosRequireFixtureID(t *testing.T) {
	const zeroUUID = "00000000-0000-0000-0000-000000000000"
	for _, scenario := range defaultScenarios() {
		if strings.Contains(scenario.path, zeroUUID) {
			t.Fatalf("scenario %s contains the zero UUID", scenario.name)
		}
		if strings.Contains(scenario.path, fixtureClusterPathToken) {
			got := scenario.pathForCluster("11111111-1111-4111-8111-111111111111")
			if strings.Contains(got, fixtureClusterPathToken) || strings.Contains(got, zeroUUID) {
				t.Fatalf("scenario %s did not resolve a real fixture path: %s", scenario.name, got)
			}
		}
	}
}

func TestSyntheticResourcesMatchDeclaredCardinality(t *testing.T) {
	rec := newRecorder()
	agent := &syntheticAgent{rec: rec, resources: scaleResources{
		PodsPerCluster: 1000, DeploymentsPerCluster: 250, ServicesPerCluster: 350,
	}}
	for _, test := range []struct {
		path string
		kind string
		want int
	}{
		{path: "/api/v1/pods", kind: "PodList", want: 1000},
		{path: "/apis/apps/v1/deployments", kind: "DeploymentList", want: 250},
		{path: "/api/v1/services", kind: "ServiceList", want: 350},
	} {
		payload, _ := json.Marshal(map[string]string{"path": test.path})
		var body struct {
			Kind  string            `json:"kind"`
			Items []json.RawMessage `json:"items"`
		}
		if err := json.Unmarshal([]byte(agent.cannedK8sBody(&tunnelMessage{Payload: payload})), &body); err != nil {
			t.Fatal(err)
		}
		if body.Kind != test.kind || len(body.Items) != test.want {
			t.Fatalf("path %s produced kind=%s items=%d, want %s/%d", test.path, body.Kind, len(body.Items), test.kind, test.want)
		}
	}
}

func TestFetchAuditRowsFiltersExactRequestIDsAndDetectsDuplicates(t *testing.T) {
	acceptedAt := time.Now().UTC().Truncate(time.Second)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Query().Get("action") != "cluster.update" || request.URL.Query().Get("resource_type") != "cluster" {
			t.Fatalf("missing exact reconciliation filters: %s", request.URL.RawQuery)
		}
		if request.URL.Query().Get("page_size") != "500" {
			t.Fatalf("reconciliation did not request the maximum page size: %s", request.URL.RawQuery)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{
			{"correlation_id": "wanted", "action": "cluster.update", "resource_type": "cluster", "created_at": acceptedAt.Add(time.Second).Format(time.RFC3339)},
			{"correlation_id": "wanted", "action": "cluster.update", "resource_type": "cluster", "created_at": acceptedAt.Add(2 * time.Second).Format(time.RFC3339)},
			{"correlation_id": "unrelated", "action": "cluster.update", "resource_type": "cluster", "created_at": acceptedAt.Format(time.RFC3339)},
		}})
	}))
	defer server.Close()

	rows, err := fetchAuditRows(context.Background(), server.Client(), server.URL, "token", acceptedAt.Add(-time.Minute), map[string]time.Time{"wanted": acceptedAt})
	if err != nil {
		t.Fatal(err)
	}
	rec := newRecorder()
	rec.beginAuditConservation("run")
	rec.recordAuditMutation("wanted", true, acceptedAt)
	rec.recordObservedAuditIntents(1)
	if rec.finishAuditConservation(rows) {
		t.Fatal("duplicate canonical rows must fail conservation")
	}
	if rec.auditConservation.IntentsObserved != 1 || rec.auditConservation.CanonicalRows != 1 || rec.auditConservation.Duplicates != 1 {
		t.Fatalf("unexpected conservation: %+v", rec.auditConservation)
	}
	_ = rec.finishAuditConservation(rows)
	if rec.auditConservation.IntentsObserved != 1 {
		t.Fatalf("repeated reconciliation accumulated intents: %d", rec.auditConservation.IntentsObserved)
	}
}

func TestScrapeOnceCapturesAuditConservationMetrics(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`# TYPE astronomer_audit_dropped_total counter
astronomer_audit_dropped_total{reason="buffer_full"} 2
# TYPE astronomer_audit_write_failures_total counter
astronomer_audit_write_failures_total{path="sync"} 3
# TYPE astronomer_audit_outbox_rows gauge
astronomer_audit_outbox_rows{status="pending"} 4
astronomer_audit_outbox_rows{status="failed"} 5
astronomer_audit_outbox_rows{status="delivering"} 6
astronomer_audit_outbox_rows{status="dead"} 7
astronomer_audit_outbox_rows{status="delivered"} 100
# TYPE astronomer_worker_jobs_total counter
astronomer_worker_jobs_total{job="sync",status="success"} 11
# TYPE astronomer_tunnel_state_updates_handled_total counter
astronomer_tunnel_state_updates_handled_total{outcome="published",kind="Pod"} 13
`))
	}))
	defer server.Close()
	rec := newRecorder()
	if err := scrapeOnce(context.Background(), server.URL, "", rec); err != nil {
		t.Fatal(err)
	}
	for metric, want := range map[string]float64{
		"audit_dropped_total": 2, "audit_write_failures_total": 3,
		"audit_outbox_active_rows": 15, "audit_outbox_dead_rows": 7,
		"worker_jobs_total": 11, "tunnel_state_updates_handled_total": 13,
	} {
		if got := lastValue(rec.scrapeSeries[metric]); got != want {
			t.Fatalf("%s=%v, want %v", metric, got, want)
		}
	}
}
