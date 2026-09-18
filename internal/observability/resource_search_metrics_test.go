package observability

import (
	"testing"
	"time"
)

func TestRecordResourceSearch(t *testing.T) {
	oldInstanceID := InstanceID()
	SetInstanceID("test-resource-search-metrics")
	t.Cleanup(func() { SetInstanceID(oldInstanceID) })

	beforeRequests := metricFamilyCounterValue(t, "astronomer_resource_search_requests_total", "outcome", "partial")
	beforeAttempted := metricFamilyCounterValue(t, "astronomer_resource_search_clusters_total", "outcome", "attempted")
	beforeFailed := metricFamilyCounterValue(t, "astronomer_resource_search_clusters_total", "outcome", "failed")

	RecordResourceSearch("partial", 19, 3, time.Now().Add(-time.Millisecond))

	if got := metricFamilyCounterValue(t, "astronomer_resource_search_requests_total", "outcome", "partial"); got != beforeRequests+1 {
		t.Fatalf("partial requests = %v, want %v", got, beforeRequests+1)
	}
	if got := metricFamilyCounterValue(t, "astronomer_resource_search_clusters_total", "outcome", "attempted"); got != beforeAttempted+19 {
		t.Fatalf("attempted clusters = %v, want %v", got, beforeAttempted+19)
	}
	if got := metricFamilyCounterValue(t, "astronomer_resource_search_clusters_total", "outcome", "failed"); got != beforeFailed+3 {
		t.Fatalf("failed clusters = %v, want %v", got, beforeFailed+3)
	}
}
