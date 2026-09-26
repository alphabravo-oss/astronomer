package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestCISCanaryRequiresCompletedScanWithIngestedFindings(t *testing.T) {
	t.Parallel()
	const clusterID = "00000000-0000-0000-0000-000000000001"
	getCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/security/scans/":
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"data":{"id":"scan-1","cluster_id":"` + clusterID + `","status":"pending"}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/security/scans/scan-1/":
			getCount++
			if getCount == 1 {
				_, _ = w.Write([]byte(`{"data":{"id":"scan-1","cluster_id":"` + clusterID + `","status":"running"}}`))
				return
			}
			_, _ = w.Write([]byte(`{"data":{"id":"scan-1","cluster_id":"` + clusterID + `","status":"completed","passed":2,"failed":1,"warned":0,"skipped":0,"findings":[{"test_id":"1.1","status":"pass"}]}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	base, _ := url.Parse(server.URL)
	result := evaluateCISCanary(context.Background(), server.Client(), executionContext{Base: base, Token: "token"}, clusterID, time.Millisecond)
	if result.State != "PASS" || result.OperationID != "scan-1" || result.ArtifactSHA == "" || result.SampleAt == nil {
		t.Fatalf("canary result = %#v", result)
	}
	if !strings.Contains(result.Reason, "3 benchmark results") || getCount != 2 {
		t.Fatalf("canary reason=%q polls=%d", result.Reason, getCount)
	}
}

func TestCISCanaryRejectsCompletedScanWithoutFunctionalEvidence(t *testing.T) {
	t.Parallel()
	const clusterID = "00000000-0000-0000-0000-000000000001"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"data":{"id":"scan-empty","cluster_id":"` + clusterID + `","status":"pending"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":{"id":"scan-empty","cluster_id":"` + clusterID + `","status":"completed","passed":0,"failed":0,"warned":0,"skipped":0,"findings":[]}}`))
	}))
	t.Cleanup(server.Close)
	base, _ := url.Parse(server.URL)
	result := evaluateCISCanary(context.Background(), server.Client(), executionContext{Base: base, Token: "token"}, clusterID, time.Millisecond)
	if result.State != "FAIL" || !strings.Contains(result.Reason, "without ingested findings") {
		t.Fatalf("canary result = %#v", result)
	}
}

func TestToolIsolationRequiresCrossClusterAbsenceAndRestrictedDenial(t *testing.T) {
	t.Parallel()
	const targetClusterID = "00000000-0000-0000-0000-000000000001"
	const otherClusterID = "00000000-0000-0000-0000-000000000002"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, otherClusterID):
			_, _ = w.Write([]byte(`{"data":[{"slug":"cis-operator","status":"not_installed"}]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/tools/cis-operator/install":
			if r.Header.Get("Authorization") != "Bearer restricted-token" {
				t.Fatalf("authorization header = %q", r.Header.Get("Authorization"))
			}
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":{"code":"scope_denied","message":"Read-only token cannot mutate"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	base, _ := url.Parse(server.URL)
	result := evaluateToolIsolation(context.Background(), server.Client(), executionContext{
		Base: base, RestrictedToken: "restricted-token", RunID: "run-1",
		Config: qualificationConfig{MemberTargets: []memberTarget{{ClusterID: targetClusterID}, {ClusterID: otherClusterID}}},
	}, "cis-operator", targetClusterID)
	if result.State != "PASS" || result.HTTPStatus != http.StatusForbidden || result.IdempotencyKey == "" {
		t.Fatalf("isolation result = %#v", result)
	}
}

func TestCISRestartRecoveryUsesWorkloadAPIAndRerunsCanary(t *testing.T) {
	t.Parallel()
	const clusterID = "00000000-0000-0000-0000-000000000001"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/workloads/Deployment/cis-operator-system/cis-operator/restart/"):
			w.Header().Set("Location", "/api/v1/workloads/operations/restart-1/")
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"data":{"id":"restart-1","status":"pending"}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/workloads/operations/restart-1/":
			_, _ = w.Write([]byte(`{"data":{"id":"restart-1","status":"completed"}}`))
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/tools/status/"):
			_, _ = w.Write([]byte(`{"data":[{"slug":"cis-operator","status":"installed"}]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/security/scans/":
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"data":{"id":"scan-after-restart","cluster_id":"` + clusterID + `","status":"pending"}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/security/scans/scan-after-restart/":
			_, _ = w.Write([]byte(`{"data":{"id":"scan-after-restart","cluster_id":"` + clusterID + `","status":"completed","passed":1,"failed":0,"warned":0,"skipped":0,"findings":[{"test_id":"1.1","status":"pass"}]}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	base, _ := url.Parse(server.URL)
	result := evaluateToolRestartRecovery(context.Background(), server.Client(), executionContext{
		Base: base, Token: "token", RunID: "run-1",
	}, "cis-operator", clusterID)
	if result.State != "PASS" || result.OperationID != "restart-1" || result.ArtifactSHA == "" || result.SampleAt == nil {
		t.Fatalf("restart result = %#v", result)
	}
}
