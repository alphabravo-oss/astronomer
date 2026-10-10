package astroclient

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

func TestPaginationCursorUsesExistingClientMethodsAndTransport(t *testing.T) {
	for _, token := range []string{"", "p1.fixture-opaque_+/="} {
		t.Run(token, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				values, present := r.URL.Query()["cursor"]
				if !present || len(values) != 1 || values[0] != token || r.URL.Query().Has("offset") {
					t.Errorf("cursor was lost, rewritten or combined with offset: %v", r.URL.Query())
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			defer server.Close()
			client, err := NewClient(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			methods := []func() (*http.Response, error){
				func() (*http.Response, error) {
					return client.GetCatalogCharts(context.Background(), &GetCatalogChartsParams{}, WithPaginationCursor(token))
				},
				func() (*http.Response, error) {
					return client.GetControllersAlerts(context.Background(), &GetControllersAlertsParams{}, WithPaginationCursor(token))
				},
				func() (*http.Response, error) {
					return client.GetControllersSilences(context.Background(), &GetControllersSilencesParams{}, WithPaginationCursor(token))
				},
			}
			for _, method := range methods {
				response, err := method()
				if err != nil {
					t.Fatal(err)
				}
				if response.StatusCode != http.StatusNoContent {
					t.Fatalf("status%d", response.StatusCode)
				}
				if err := response.Body.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if calls.Load() != 3 {
				t.Fatalf("existing transport calls%d", calls.Load())
			}
		})
	}
}

func TestPaginationCursorRejectsMixedRepeatedOversizedAndCanceledBeforeTransport(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer server.Close()
	client, err := NewClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	zero := 0
	queryEditor := func(raw string) RequestEditorFn {
		return func(_ context.Context, r *http.Request) error { r.URL.RawQuery = raw; return nil }
	}
	for _, tc := range []struct {
		name    string
		params  *GetCatalogChartsParams
		editors []RequestEditorFn
	}{
		{"offset", &GetCatalogChartsParams{Offset: &zero}, []RequestEditorFn{WithPaginationCursor("")}},
		{"repeated", &GetCatalogChartsParams{}, []RequestEditorFn{WithPaginationCursor(""), WithPaginationCursor("p1.next")}},
		{"oversized", &GetCatalogChartsParams{}, []RequestEditorFn{WithPaginationCursor(strings.Repeat("a", 4097))}},
		{"malformed query", &GetCatalogChartsParams{}, []RequestEditorFn{queryEditor("cursor=%GG"), WithPaginationCursor("")}},
		{"malformed separator", &GetCatalogChartsParams{}, []RequestEditorFn{queryEditor("search=a;b"), WithPaginationCursor("")}},
		{"repeated filter", &GetCatalogChartsParams{}, []RequestEditorFn{queryEditor("search=a&search=b"), WithPaginationCursor("")}},
		{"preexisting cursor", &GetCatalogChartsParams{}, []RequestEditorFn{queryEditor("cursor=&cursor=other"), WithPaginationCursor("")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := client.GetCatalogCharts(context.Background(), tc.params, tc.editors...); err == nil {
				t.Fatal("invalid cursor request reached transport")
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.GetCatalogCharts(ctx, &GetCatalogChartsParams{}, WithPaginationCursor("")); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation identity lost: %v", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("rejected requests reached transport%d times", calls.Load())
	}
}

func TestPaginationCursorPreservesAuthorityFiltersAndRequestContext(t *testing.T) {
	type contextKey struct{}
	ctx := context.WithValue(context.Background(), contextKey{}, "fixture-context")
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.example.invalid:8443/api/v1/catalog/charts/?search=a%2Bb&project_id=fixture&limit=25", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer fixture")
	if err := WithPaginationCursor("p1.next+/=")(ctx, request); err != nil {
		t.Fatal(err)
	}
	if request.URL.Scheme != "https" || request.URL.Host != "api.example.invalid:8443" || request.URL.Path != "/api/v1/catalog/charts/" || request.Header.Get("Authorization") != "Bearer fixture" || request.Context().Value(contextKey{}) != "fixture-context" {
		t.Fatal("cursor editor changed request authority, authentication or context")
	}
	values, err := url.ParseQuery(request.URL.RawQuery)
	if err != nil {
		t.Fatal(err)
	}
	if values.Get("search") != "a+b" || values.Get("project_id") != "fixture" || values.Get("limit") != "25" || values.Get("cursor") != "p1.next+/=" {
		t.Fatal("cursor editor changed scoped filters or opaque token")
	}
}
