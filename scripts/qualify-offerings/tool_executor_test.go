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
