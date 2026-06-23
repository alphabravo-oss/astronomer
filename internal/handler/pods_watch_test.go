package handler

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

type fakePodWatcher struct {
	clusterID string
	namespace string
	events    []PodWatchEvent
}

func (f *fakePodWatcher) WatchPods(ctx context.Context, clusterID, namespace string) (<-chan PodWatchEvent, error) {
	f.clusterID = clusterID
	f.namespace = namespace
	ch := make(chan PodWatchEvent, len(f.events))
	for _, ev := range f.events {
		ch <- ev
	}
	close(ch) // closing the channel ends the SSE stream (no client polling)
	return ch, nil
}

func TestWatchPodsStreamsSSEEvents(t *testing.T) {
	h := NewWorkloadHandler()
	h.SetPodWatcher(&fakePodWatcher{
		events: []PodWatchEvent{
			{Type: "ADDED", Object: json.RawMessage(`{"metadata":{"name":"web-0"}}`)},
			{Type: "DELETED", Object: json.RawMessage(`{"metadata":{"name":"web-0"}}`)},
		},
	})

	req := httptest.NewRequest("GET", "/api/v1/clusters/c1/pods/watch/?namespace=prod", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("cluster_id", "c1")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	rr := httptest.NewRecorder()
	h.WatchPods(rr, req)

	if ct := rr.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type = %q, want text/event-stream", ct)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "event: ADDED\ndata: {\"metadata\":{\"name\":\"web-0\"}}\n\n") {
		t.Errorf("missing ADDED SSE frame; body:\n%s", body)
	}
	if !strings.Contains(body, "event: DELETED\ndata: {\"metadata\":{\"name\":\"web-0\"}}\n\n") {
		t.Errorf("missing DELETED SSE frame; body:\n%s", body)
	}
}

func TestWatchPodsNotConfigured(t *testing.T) {
	h := NewWorkloadHandler()
	req := httptest.NewRequest("GET", "/api/v1/clusters/c1/pods/watch/", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("cluster_id", "c1")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	rr := httptest.NewRecorder()
	h.WatchPods(rr, req)
	if rr.Code != 501 {
		t.Fatalf("status = %d, want 501 when no watcher wired", rr.Code)
	}
}
