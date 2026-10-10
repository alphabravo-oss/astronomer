package astroclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
)

func TestAdminDLQPageUsesReleasedClientAndReachesTask101(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		page := 1
		if values, present := r.URL.Query()["page"]; present {
			if len(values) != 1 {
				t.Error("repeated native page")
			}
			page, _ = strconv.Atoi(values[0])
		}
		if r.URL.Path != "/api/v1/admin/queues/fixture/dlq" || r.URL.Query().Has("offset") || r.URL.Query().Has("cursor") {
			t.Error("native request path/mode drift")
		}
		count := 100
		if page == 2 {
			count = 1
		}
		entries := make([]AdminQueueDLQEntryWire, count)
		for n := range entries {
			entries[n].Id = "task-" + strconv.Itoa((page-1)*100+n+1)
			entries[n].Type = "fixture"
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{"data": AdminQueueDLQWire{Queue: "fixture", Count: count, Dlq: entries}, "pagination": map[string]any{"limit": 100, "offset": (page - 1) * 100, "has_more": page == 1, "next_offset": nil}}); err != nil {
			t.Error("fixture response encode failed")
		}
	}))
	defer server.Close()
	client, err := NewClientWithResponses(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	first, err := client.GetAdminQueuesByQueueDlqWithResponse(context.Background(), "fixture")
	if err != nil || first.JSON200 == nil || first.JSON200.Data.Count != 100 {
		t.Fatalf("released initial method did not retain data: error=%v", err)
	}
	second, err := client.GetAdminQueuesByQueueDlqWithResponse(context.Background(), "fixture", WithAdminDLQPage(2))
	if err != nil || second.JSON200 == nil || second.JSON200.Data.Count != 1 || len(second.JSON200.Data.Dlq) != 1 || second.JSON200.Data.Dlq[0].Id != "task-101" {
		t.Fatalf("task 101 inaccessible using released method/editor: error=%v", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("transport calls=%d", calls.Load())
	}
}

func TestAdminDLQPageRejectsInvalidMixedAndCanceledBeforeTransport(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer server.Close()
	client, err := NewClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"page=1", "page=1&page=2", "offset=0", "cursor=", "limit=100", "page=%zz", "a=1;a=2", "other=a&other=b"} {
		t.Run(raw, func(t *testing.T) {
			edit := func(_ context.Context, r *http.Request) error { r.URL.RawQuery = raw; return nil }
			if _, err := client.GetAdminQueuesByQueueDlq(context.Background(), "fixture", edit, WithAdminDLQPage(2)); err == nil {
				t.Fatal("invalid query reached transport")
			}
		})
	}
	for _, page := range []int{-1, 0, 10001} {
		if _, err := client.GetAdminQueuesByQueueDlq(context.Background(), "fixture", WithAdminDLQPage(page)); err == nil {
			t.Fatal("invalid bound reached transport")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.GetAdminQueuesByQueueDlq(ctx, "fixture", WithAdminDLQPage(2)); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation identity=%v", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("invalid requests reached transport=%d", calls.Load())
	}
}
