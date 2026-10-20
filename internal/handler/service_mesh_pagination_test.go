package handler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

type boundedMeshPeer struct {
	requests int
	cancel   context.CancelFunc
	mode     string
}

func (p *boundedMeshPeer) Do(ctx context.Context, _ string, _ string, target string, _ []byte, _ map[string]string) (*protocol.K8sResponsePayload, error) {
	p.requests++
	if p.mode == "deadline" {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	u, err := url.Parse(target)
	if err != nil {
		return nil, err
	}
	if u.Query().Get("limit") != "500" || u.Query().Has("resourceVersion") {
		return nil, context.Canceled
	}
	if p.cancel != nil {
		p.cancel()
	}
	items := []any{map[string]any{"name": "fixture"}}
	if p.mode == "missing-items" {
		items = nil
	}
	next := ""
	if p.mode == "page-budget" || p.cancel != nil {
		next = "opaque " + strconv.Itoa(p.requests)
	}
	raw, err := json.Marshal(map[string]any{"items": items, "metadata": map[string]any{"continue": next, "resourceVersion": "snapshot"}})
	if err != nil {
		return nil, err
	}
	switch p.mode {
	case "invalid-json":
		raw = []byte(`{"items":`)
	case "duplicate-items":
		raw = []byte(`{"items":[{"name":"first"}],"items":[{"name":"second"}]}`)
	case "oversize":
		raw = []byte(strings.Repeat(" ", meshListMaxBytes+1))
	case "additive":
		raw = []byte(`{"items":[{"name":"fixture"}],"metadata":{"resourceVersion":"snapshot"},"futureVendorField":{"enabled":true}}`)
	}
	return &protocol.K8sResponsePayload{StatusCode: http.StatusOK, Body: base64.StdEncoding.EncodeToString(raw)}, nil
}

func TestServiceMeshContinuationBoundsDiscardPartialResults(t *testing.T) {
	for _, mode := range []string{"page-budget", "missing-items", "cancel-after-response"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			peer := &boundedMeshPeer{mode: mode}
			if mode == "cancel-after-response" {
				peer.cancel = cancel
			}
			items, err := collectMeshPages[map[string]any](ctx, peer, uuid.New(), "/apis/fixture/items")
			if err == nil || len(items) != 0 {
				t.Fatalf("incomplete mesh list escaped: items=%d error=%v", len(items), err)
			}
			want := 1
			if mode == "page-budget" {
				want = 10
			}
			if peer.requests != want {
				t.Fatalf("requests=%d, want %d", peer.requests, want)
			}
		})
	}
}

func TestServiceMeshCanceledListDoesNotContactTunnel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	peer := &boundedMeshPeer{}
	items, err := collectMeshPages[map[string]any](ctx, peer, uuid.New(), "/apis/fixture/items")
	if err == nil || len(items) != 0 || peer.requests != 0 {
		t.Fatalf("canceled list contacted tunnel: items=%d calls=%d error=%v", len(items), peer.requests, err)
	}
}

func TestServiceMeshSupplierBodyValidation(t *testing.T) {
	for _, mode := range []string{"invalid-json", "duplicate-items", "oversize", "additive"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			peer := &boundedMeshPeer{mode: mode}
			items, err := collectMeshPages[map[string]any](ctx, peer, uuid.New(), "/apis/fixture/items")
			if mode == "additive" {
				if err != nil || len(items) != 1 || items[0]["name"] != "fixture" {
					t.Fatalf("additive supplier shape rejected: items=%d error=%v", len(items), err)
				}
			} else if err == nil || len(items) != 0 {
				t.Fatalf("invalid supplier body escaped: items=%d error=%v", len(items), err)
			}
			if peer.requests != 1 {
				t.Fatalf("requests=%d, want 1", peer.requests)
			}
		})
	}
}

func TestServiceMeshCallerDeadlineStopsSupplierRequest(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	peer := &boundedMeshPeer{mode: "deadline"}
	items, err := collectMeshPages[map[string]any](ctx, peer, uuid.New(), "/apis/fixture/items")
	if err == nil || len(items) != 0 || peer.requests != 1 || ctx.Err() != context.DeadlineExceeded {
		t.Fatalf("caller deadline did not stop supplier request: items=%d calls=%d error=%v", len(items), peer.requests, err)
	}
}
