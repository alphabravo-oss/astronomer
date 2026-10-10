package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
)

func ownerBaselineInstance(t *testing.T) {
	t.Helper()
	prior := observability.InstanceID()
	observability.SetInstanceID(t.Name())
	t.Cleanup(func() { observability.SetInstanceID(prior) })
}

func ownerBaselineSamples(t *testing.T, collector prometheus.Collector) []*dto.Metric {
	t.Helper()
	registry := prometheus.NewRegistry()
	registry.MustRegister(collector)
	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	var samples []*dto.Metric
	for _, family := range families {
		for _, sample := range family.Metric {
			for _, label := range sample.Label {
				if label.GetName() == "astronomer_instance_id" && label.GetValue() == observability.InstanceID() {
					samples = append(samples, sample)
				}
			}
		}
	}
	return samples
}

func ownerBaselineLabel(sample *dto.Metric, name string) string {
	for _, label := range sample.Label {
		if label.GetName() == name {
			return label.GetValue()
		}
	}
	return ""
}

// Historical sessions carry a provider name, not proof that its configuration
// still exists. Audit and session cleanup must retain their existing behavior;
// these names must not each allocate a new metric owner.
func TestSSOLogoutMetricsDoNotAdmitUnverifiedHistoricalProviders(t *testing.T) {
	ownerBaselineInstance(t)
	t.Cleanup(func() {
		auth.SSOLogoutsTotal.DeletePartialMatch(prometheus.Labels{"astronomer_instance_id": t.Name()})
	})
	store := newRecordingSSOSessionStore()
	h := NewAuthHandler(nil, nil)
	h.SetSSOSessionStore(store)
	h.SetEncryptor(newTestEncryptor(t))
	userID := uuid.New()
	for i := range 32 {
		jti, provider := fmt.Sprintf("metric-jti-%d", i), fmt.Sprintf("retired-%d", i)
		store.rows[jti] = sqlc.SsoSession{Jti: jti, UserID: userID, ProviderName: provider}
		var detail map[string]any
		if redirect := h.buildSSOLogoutRedirect(httptest.NewRequest(http.MethodPost, "/logout", nil), jti, userID, &detail); redirect != "" {
			t.Fatal("session without a logout endpoint produced a redirect")
		}
		if detail["sso_provider"] != provider || detail["sso_logout"] != "no_endpoint" {
			t.Fatal("metric projection changed audit semantics")
		}
		if _, exists := store.rows[jti]; exists {
			t.Fatal("logout did not remove the resolved session")
		}
	}
	samples := ownerBaselineSamples(t, auth.SSOLogoutsTotal)
	if len(samples) != 1 || ownerBaselineLabel(samples[0], "provider") != "unknown" || samples[0].GetCounter().GetValue() != 32 {
		t.Fatalf("unverified historical providers allocated %d metric children; want one unknown counter containing all 32 attempts", len(samples))
	}
}

type ownerBaselineGroups struct {
	groups []sqlc.ClusterGroup
	counts map[uuid.UUID]int64
	failID uuid.UUID
}

func (q *ownerBaselineGroups) ListClusterGroups(context.Context) ([]sqlc.ClusterGroup, error) {
	return q.groups, nil
}

func (q *ownerBaselineGroups) CountClustersInGroup(_ context.Context, id uuid.UUID) (int64, error) {
	if id == q.failID {
		return 0, errors.New("synthetic count failure")
	}
	return q.counts[id], nil
}

func TestClusterGroupMetricsPreserveCompletePublicationOnCountFailure(t *testing.T) {
	ownerBaselineInstance(t)
	t.Cleanup(func() {
		clusterGroupClusters.DeletePartialMatch(prometheus.Labels{"astronomer_instance_id": t.Name()})
	})
	var prior dto.Metric
	if err := clusterGroupsTotal.Write(&prior); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { clusterGroupsTotal.Set(prior.GetGauge().GetValue()) })
	a, b := uuid.New(), uuid.New()
	q := &ownerBaselineGroups{
		groups: []sqlc.ClusterGroup{{ID: a, Slug: "metric-group-a"}, {ID: b, Slug: "metric-group-b"}},
		counts: map[uuid.UUID]int64{a: 3, b: 4},
	}
	RefreshClusterGroupMetrics(context.Background(), q)
	q.counts[a], q.failID = 30, b
	RefreshClusterGroupMetrics(context.Background(), q)
	got := map[string]float64{}
	for _, sample := range ownerBaselineSamples(t, clusterGroupClusters) {
		got[ownerBaselineLabel(sample, "group_slug")] = sample.GetGauge().GetValue()
	}
	if len(got) != 2 || got[a.String()] != 3 || got[b.String()] != 4 {
		t.Fatalf("failed snapshot replaced complete group publication: %v", got)
	}
}

func TestVeleroStatusMetricReplacesPriorReadyState(t *testing.T) {
	ownerBaselineInstance(t)
	t.Cleanup(func() {
		veleroInstallStatus.DeletePartialMatch(prometheus.Labels{"astronomer_instance_id": t.Name()})
	})
	id := uuid.New()
	h := NewClusterSnapshotsHandler(&generationSnapshotQuerier{fakeSnapshotQuerier: newFakeSnapshotQuerier(id, "metric-current-cluster"), id: id, generation: uuid.New()})
	requester := newFakeSnapshotRequester()
	h.SetRequester(requester)
	path := "/apis/velero.io/v1/namespaces/velero/backupstoragelocations"
	requester.setResponse(path, fakeSnapshotResponse{Status: http.StatusOK, Body: map[string]any{
		"items": []map[string]any{{"metadata": map[string]any{"name": "default"}, "status": map[string]any{"phase": "Available"}}},
	}})
	check := func(installed bool) {
		t.Helper()
		response := httptest.NewRecorder()
		h.VeleroStatus(response, snapshotReq(t, http.MethodGet, "/", nil, map[string]string{"cluster_id": id.String()}))
		if response.Code != http.StatusOK {
			t.Fatalf("unexpected HTTP status %d", response.Code)
		}
		var result VeleroStatusResponse
		unwrap(t, response, &result)
		if result.Installed != installed {
			t.Fatal("metric update changed the endpoint result")
		}
	}
	check(true)
	requester.setResponse(path, fakeSnapshotResponse{Status: http.StatusNotFound})
	check(false)
	samples := ownerBaselineSamples(t, veleroInstallStatus)
	if len(samples) != 1 || ownerBaselineLabel(samples[0], "status") != "missing" || samples[0].GetGauge().GetValue() != 0 {
		t.Fatalf("Velero transition retained %d status children; want only the current missing state", len(samples))
	}
}

// AFTER fixtures explicitly implement the new canonical generation read. The
// unchanged BEFORE fixture uses only the legacy domain reads and label names.
func (q *ownerBaselineGroups) GetMetricOwnerGeneration(_ context.Context, p sqlc.GetMetricOwnerGenerationParams) (uuid.UUID, error) {
	for _, row := range q.groups {
		if row.ID == p.OwnerID {
			return row.ID, nil
		}
	}
	return uuid.Nil, errors.New("owned fixture owner absent")
}

type generationSnapshotQuerier struct {
	*fakeSnapshotQuerier
	id, generation uuid.UUID
}

func (q *generationSnapshotQuerier) GetMetricOwnerGeneration(_ context.Context, p sqlc.GetMetricOwnerGenerationParams) (uuid.UUID, error) {
	if p.Family == string(observability.MetricCluster) && p.OwnerID == q.id {
		return q.generation, nil
	}
	return uuid.Nil, errors.New("owned fixture owner absent")
}
func TestSnapshotWorkerCapturedMetricsCannotResurrectDeletedCluster(t *testing.T) {
	ownerBaselineInstance(t)
	q := &generationSnapshotQuerier{id: uuid.New(), generation: uuid.New()}
	metric := CaptureSnapshotWorkerMetrics(q)(context.Background(), q.id)
	metric.Outcome(context.Background(), "completed")
	q.generation = uuid.New()
	metric.Outcome(context.Background(), "completed")
	metric.InFlight(context.Background(), 4)
	samples := ownerBaselineSamples(t, clusterSnapshotsTotal)
	if len(samples) != 1 || samples[0].GetCounter().GetValue() != 1 {
		t.Fatal("old captured generation recorded a late worker outcome")
	}
	if len(ownerBaselineSamples(t, clusterSnapshotsInFlight)) != 0 {
		t.Fatal("old captured generation admitted a late gauge")
	}
	t.Cleanup(func() {
		clusterSnapshotsTotal.DeletePartialMatch(prometheus.Labels{"astronomer_instance_id": t.Name()})
	})
}

func TestSnapshotWorkerCaptureFailureReturnsNilRecorder(t *testing.T) {
	ownerBaselineInstance(t)
	id := uuid.New()
	if CaptureSnapshotWorkerMetrics(nil)(context.Background(), id) != nil {
		t.Fatal("missing canonical reader returned a non-nil recorder")
	}
	q := &generationSnapshotQuerier{id: id, generation: uuid.Nil}
	capture := CaptureSnapshotWorkerMetrics(q)
	if capture(context.Background(), id) != nil {
		t.Fatal("invalid generation returned a non-nil recorder")
	}
	q.generation = uuid.New()
	if capture(context.Background(), uuid.New()) != nil {
		t.Fatal("failed canonical lookup returned a non-nil recorder")
	}
	if capture(context.Background(), id) == nil {
		t.Fatal("valid generation did not return a captured recorder")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if capture(canceled, id) != nil {
		t.Fatal("canceled capture returned a non-nil recorder")
	}
}
