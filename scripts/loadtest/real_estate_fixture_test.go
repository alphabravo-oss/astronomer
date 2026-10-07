package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func estateTestManifest() estateManifest {
	m := estateManifest{Schema: estateSchema, Tier: 1, Environment: estateEnvironment{ID: "dev", Commit: strings.Repeat("a", 40), ImagesSHA256: strings.Repeat("b", 64), ValuesSHA256: strings.Repeat("c", 64), DatasetSHA256: strings.Repeat("d", 64), HardwareSHA256: strings.Repeat("e", 64), KubernetesVersion: "v1.31.0", Replicas: map[string]int{"server": 1, "worker": 1}}}
	for i := 1; i <= 2; i++ {
		m.Members = append(m.Members, estateMember{Name: fmt.Sprintf("member-%d", i), ClusterID: fmt.Sprintf("00000000-0000-4000-8000-%012d", i), ProjectID: fmt.Sprintf("00000000-0000-4000-8000-%012d", i+10), Namespace: "benchmark", PrivilegeProfile: "privileged", Assignments: []estateAssignment{{ID: fmt.Sprintf("00000000-0000-4000-8000-%012d", i+20), TargetID: fmt.Sprintf("00000000-0000-4000-8000-%012d", i+30), Generation: 1, SpecDigest: "sha256:" + strings.Repeat("f", 64)}}, Resources: map[string]int{"pods": 1, "deployments": 1, "services": 1}, Metrics: estateMetricsTarget{URL: fmt.Sprintf("https://member-%d.test/metrics", i), InstanceID: "test"}})
	}
	m.Phases = []estatePhaseSpec{{Name: "resources", Mode: "resources", RPS: 1, WarmupSeconds: 300, MeasurementSeconds: 1800}}
	return m
}

type estateRoundTrip func(*http.Request) (*http.Response, error)

func (f estateRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func estateResponse(value any) *http.Response {
	raw, _ := json.Marshal(value)
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(raw))), Header: http.Header{}}
}
func estateFixtureClient(t *testing.T, m estateMember, now time.Time) *http.Client {
	t.Helper()
	c := estateHTTPClient()
	c.Transport = estateRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.Method != "GET" {
			t.Fatal("mutation attempted")
		}
		if r.Header.Get("Authorization") != "Bearer api-test-token" {
			t.Fatal("missing API token")
		}
		path := r.URL.Path
		var data any
		switch {
		case path == "/api/v1/clusters/"+m.ClusterID+"/":
			data = map[string]any{"id": m.ClusterID, "status": "active", "registration_phase": "ready", "last_heartbeat": now, "agent_privilege_profile": m.PrivilegeProfile}
		case path == "/api/v1/projects/"+m.ProjectID+"/":
			data = map[string]any{"id": m.ProjectID, "cluster_id": m.ClusterID, "namespaces": []string{m.Namespace}}
		case path == "/api/v1/delivery/deployments/":
			return estateResponse(map[string]any{"data": []map[string]any{{"id": m.Assignments[0].ID, "cluster_id": m.ClusterID}}, "pagination": map[string]any{"total": 1, "has_more": false}}), nil
		case strings.HasPrefix(path, "/api/v1/delivery/deployments/"):
			a := m.Assignments[0]
			data = map[string]any{"deployment": map[string]any{"id": a.ID, "target_id": a.TargetID, "cluster_id": m.ClusterID, "phase": "ready", "action": "apply", "desired_generation": 1, "observed_generation": 1, "last_observed_at": now, "desired_spec_digest": a.SpecDigest, "observed_spec_digest": a.SpecDigest}}
		case strings.HasPrefix(path, "/api/v1/delivery/targets/"):
			data = map[string]any{"id": m.Assignments[0].TargetID, "project_id": m.ProjectID, "deletion_state": "active"}
		case strings.Contains(path, "/k8s/"):
			return estateResponse(map[string]any{"items": []any{map[string]any{"metadata": map[string]any{"name": "fixture"}}}}), nil
		default:
			t.Fatalf("unexpected path %s", path)
		}
		return estateResponse(map[string]any{"data": data}), nil
	})
	return c
}
func estateMetricFixture(now time.Time) []byte {
	var s strings.Builder
	for _, name := range []string{"process_resident_memory_bytes", "process_open_fds", "go_memstats_heap_alloc_bytes", "go_goroutines"} {
		fmt.Fprintf(&s, "# TYPE %s gauge\n%s 100\n", name, name)
	}
	s.WriteString("# TYPE process_cpu_seconds_total counter\nprocess_cpu_seconds_total 5\n# TYPE astronomer_agent_observation_requests_total counter\n")
	for _, kind := range estateKinds {
		fmt.Fprintf(&s, "astronomer_agent_observation_requests_total{astronomer_instance_id=\"test\",kind=\"%s\",verb=\"list\",outcome=\"success\"} 10\n", kind)
	}
	for _, suffix := range []string{"source_available", "sampled_at_timestamp_seconds", "observed_at_timestamp_seconds"} {
		name := estateObservationPrefix + suffix
		fmt.Fprintf(&s, "# TYPE %s gauge\n", name)
		value := float64(now.Unix())
		if suffix == "source_available" {
			value = 1
		}
		for _, source := range estateSources {
			fmt.Fprintf(&s, "%s{astronomer_instance_id=\"test\",source=\"%s\"} %.0f\n", name, source, value)
		}
	}
	return []byte(s.String())
}
