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

func TestTrivyCanaryRequiresFreshIngestedReportAfterRescan(t *testing.T) {
	t.Parallel()
	const clusterID = "00000000-0000-0000-0000-000000000001"
	scannedAt := time.Now().UTC().Format(time.RFC3339Nano)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/vulnerabilities/rescan/"):
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"data":{"operation_id":"rescan-1","operation_url":"/api/v1/workloads/operations/rescan-1/","cluster_id":"` + clusterID + `","status":"pending","requested_at":"` + scannedAt + `"}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/workloads/operations/rescan-1/":
			_, _ = w.Write([]byte(`{"data":{"id":"rescan-1","status":"completed"}}`))
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/vulnerabilities/images/"):
			_, _ = w.Write([]byte(`{"data":[{"id":"report-1","cluster_id":"` + clusterID + `","scanner":"Trivy","scanned_at":"` + scannedAt + `"}],"pagination":{"total":1}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	base, _ := url.Parse(server.URL)
	result := evaluateTrivyCanary(context.Background(), server.Client(), executionContext{Base: base, Token: "token", RunID: "run-1"}, clusterID, "functional", time.Millisecond)
	if result.State != "PASS" || result.OperationID != "rescan-1" || result.ArtifactSHA == "" || result.IdempotencyKey == "" {
		t.Fatalf("canary result = %#v", result)
	}
}

func TestMetricCanaryRequiresPositiveProductSamples(t *testing.T) {
	t.Parallel()
	const clusterID = "00000000-0000-0000-0000-000000000001"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		_, _ = w.Write([]byte("kube_pod_info{namespace=\"default\"} 1\nkube_node_info{node=\"n1\"} 1\n"))
	}))
	t.Cleanup(server.Close)
	base, _ := url.Parse(server.URL)
	result := evaluateMetricCanary(context.Background(), server.Client(), executionContext{Base: base, Token: "token"}, "kube-state-metrics", clusterID, metricCanarySpec{
		Namespace: "astronomer-monitoring", Service: "kube-state-metrics", Port: 8080, Path: "/metrics", Metrics: []string{"kube_pod_info", "kube_node_info"},
	})
	if result.State != "PASS" || result.ArtifactSHA == "" || result.SampleAt == nil {
		t.Fatalf("canary result = %#v", result)
	}
}

func TestIstioCanaryRequiresLiveDetectionAndPolicyValidation(t *testing.T) {
	t.Parallel()
	const clusterID = "00000000-0000-0000-0000-000000000001"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/service-mesh/detect/"):
			_, _ = w.Write([]byte(`{"data":{"cluster_id":"` + clusterID + `","detected_mesh":"istio","detected_version":"1.27.1","control_plane_namespace":"istio-system","last_error":""}}`))
		case strings.HasSuffix(r.URL.Path, "/service-mesh/validate/"):
			_, _ = w.Write([]byte(`{"data":{"cluster_id":"` + clusterID + `","valid":true,"kind":"PeerAuthentication"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	base, _ := url.Parse(server.URL)
	result := evaluateIstioCanary(context.Background(), server.Client(), executionContext{Base: base, Token: "token"}, clusterID)
	if result.State != "PASS" || !strings.Contains(result.Reason, "1.27.1") || result.ArtifactSHA == "" {
		t.Fatalf("canary result = %#v", result)
	}
}

func TestCertManagerCanaryIssuesTLSSecretAndCleansUp(t *testing.T) {
	t.Parallel()
	const clusterID = "00000000-0000-0000-0000-000000000001"
	deletes := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && (strings.HasSuffix(r.URL.Path, "/issuers") || strings.HasSuffix(r.URL.Path, "/certificates")):
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"metadata":{"name":"created"}}`))
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/certificates/"):
			_, _ = w.Write([]byte(`{"status":{"conditions":[{"type":"Ready","status":"True"}]}}`))
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/secrets/"):
			_, _ = w.Write([]byte(`{"type":"kubernetes.io/tls","data":{"tls.crt":"Y2VydA==","tls.key":"a2V5"}}`))
		case r.Method == http.MethodDelete:
			deletes++
			_, _ = w.Write([]byte(`{"status":"Success"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	base, _ := url.Parse(server.URL)
	result := evaluateCertManagerCanary(context.Background(), server.Client(), executionContext{
		Base: base, Token: "token", RunID: "run-1",
		Config: qualificationConfig{MemberTargets: []memberTarget{{ClusterID: clusterID, Namespace: "qualification"}}},
	}, clusterID, "functional", time.Millisecond)
	if result.State != "PASS" || result.ArtifactSHA == "" || deletes != 3 {
		t.Fatalf("canary result=%#v deletes=%d", result, deletes)
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
