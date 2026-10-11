package astroclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestCatalogCollectionGeneratedTransportEmptyProgress(t *testing.T) {
	for _, name := range []string{"empty-progress", "repeated-token", "missing-token", "revoked", "canceled", "duplicate-chart", "terminal-token"} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			chartID := uuid.New()
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				values, present := r.URL.Query()["cursor"]
				if !present || len(values) != 1 || r.URL.Query().Get("tag") != "requested-tag" {
					t.Error("released request editor/filter did not reach generated transport")
				}
				w.Header().Set("Content-Type", "application/json")
				if calls == 2 && name == "revoked" {
					w.WriteHeader(http.StatusForbidden)
					return
				}
				if calls == 2 && name == "canceled" {
					cancel()
					return
				}
				offset := 0
				data := []map[string]any{}
				more := true
				var next any = "advance-one"
				if calls == 2 {
					if values[0] != "advance-one" {
						t.Error("empty page did not advance exact opaque token")
					}
					switch name {
					case "repeated-token":
						next = "advance-one"
					case "missing-token":
						next = nil
					default:
						data = []map[string]any{{"id": chartID}}
						more = false
						next = nil
					}
					if name == "duplicate-chart" {
						data = append(data, map[string]any{"id": chartID})
					}
					if name == "terminal-token" {
						next = "invalid-terminal"
					}
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"data": data, "pagination": map[string]any{"limit": 20, "offset": offset, "has_more": more, "next_offset": nil, "next_cursor": next}})
			}))
			defer server.Close()
			client, err := NewClientWithResponses(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			tag := "requested-tag"
			got, err := CollectCatalogCharts(ctx, client, GetCatalogChartsParams{Tag: &tag})
			if calls != 2 {
				t.Fatalf("actual generated transport calls %d, want two", calls)
			}
			if name == "empty-progress" {
				if err != nil || len(got) != 1 || got[0].Id == nil || *got[0].Id != chartID {
					t.Fatalf("empty progress traversal: %v %v", got, err)
				}
			} else if err == nil || got != nil {
				t.Fatal("failed traversal returned a successful partial collection")
			}
		})
	}
}

func TestCatalogCollectionRequiresFiniteOwner(t *testing.T) {
	if _, err := CollectCatalogCharts(context.Background(), nil, GetCatalogChartsParams{}); err == nil {
		t.Fatal("unbounded catalog traversal accepted")
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	client, err := NewClientWithResponses(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	offset := 0
	if _, err := CollectCatalogCharts(ctx, client, GetCatalogChartsParams{Offset: &offset}); err == nil || !strings.Contains(err.Error(), "offset") {
		t.Fatal("offset did not reach its actual owner guard")
	}
	if requests.Load() != 0 {
		t.Fatal("offset rejection contacted generated transport")
	}
}
