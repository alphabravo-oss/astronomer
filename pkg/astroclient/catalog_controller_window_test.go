package astroclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCatalogControllerWindowCompatibleReader(t *testing.T) {
	for _, tc := range []struct {
		name, body    string
		valid, absent bool
	}{
		{"bounded", `{"operationSummary":{"scope":"authorized-clusters","windowLimit":1000,"returned":1000,"total":1001,"complete":false,"notice":"Recent authorized window"}}`, true, false},
		{"unknown", `{"operationSummary":{"scope":"global","windowLimit":1000,"returned":0,"total":null,"complete":false,"notice":"Unknown total"}}`, true, false},
		{"concurrent-count-below-list", `{"operationSummary":{"scope":"global","windowLimit":1000,"returned":2,"total":1,"complete":false,"notice":"Separate observations"}}`, true, false},
		{"compatible-extra-field", `{"operationSummary":{"scope":"global","windowLimit":1000,"returned":2,"total":3,"complete":false,"notice":"Recent window","observedAt":"2026-10-10T21:35:00Z","future":{"generation":2}}}`, true, false},
		{"wrapped-compatible-extra-field", `{"data":{"operationSummary":{"scope":"global","windowLimit":1000,"returned":2,"total":3,"complete":false,"notice":"Recent window","future":{"generation":2}}}}`, true, false},
		{"wrapped-legacy-absent", `{"data":{"operations":{"pending":0}}}`, true, true},
		{"wrapped-data-owns-window", `{"operationSummary":{"scope":"global","windowLimit":1000,"returned":1,"total":1,"complete":true,"notice":"Outer field"},"data":{"operations":{"pending":0}}}`, true, true},
		{"wrapped-null", `{"data":null}`, false, false},
		{"wrapped-array", `{"data":[]}`, false, false},
		{"wrapped-string", `{"data":"invalid"}`, false, false},
		{"legacy-absent", `{"operations":{"pending":0}}`, true, true},
		{"null", `{"operationSummary":null}`, false, false},
		{"omitted-field", `{"operationSummary":{"scope":"global","windowLimit":1000,"returned":0,"total":null,"notice":"Unknown"}}`, false, false},
		{"negative-count", `{"operationSummary":{"scope":"global","windowLimit":1000,"returned":0,"total":-1,"complete":false,"notice":"Unknown"}}`, false, false},
		{"oversized-window", `{"operationSummary":{"scope":"global","windowLimit":1000,"returned":1001,"total":1001,"complete":false,"notice":"Unknown"}}`, false, false},
		{"false-completeness", `{"operationSummary":{"scope":"global","windowLimit":1000,"returned":1,"total":null,"complete":true,"notice":"Unknown"}}`, false, false},
		{"unknown-scope", `{"operationSummary":{"scope":"namespace","windowLimit":1000,"returned":0,"total":null,"complete":false,"notice":"Unknown"}}`, false, false},
		{"malformed", `{`, false, false},
		{"oversized-body", strings.Repeat(" ", (2<<20)+1), false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := &GetCatalogControllerStatusResponse{Body: []byte(tc.body), HTTPResponse: &http.Response{StatusCode: http.StatusOK}}
			got, err := CatalogControllerWindowFromResponse(response)
			if (err == nil) != tc.valid || (tc.valid && (got == nil) != tc.absent) {
				t.Fatal("typed window reader did not preserve unavailable/invalid semantics")
			}
		})
	}
	t.Run("forbidden", func(t *testing.T) {
		_, err := CatalogControllerWindowFromResponse(&GetCatalogControllerStatusResponse{Body: []byte(`{}`), HTTPResponse: &http.Response{StatusCode: http.StatusForbidden}})
		if err == nil {
			t.Fatal("forbidden response became an empty successful window")
		}
	})
}

func TestCatalogControllerWindowUsesReleasedGeneratedHTTPClient(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/catalog/controller/status/" {
			t.Error("released generated route changed")
		}
		w.Header().Set("Content-Type", "application/json")
		_, err := w.Write([]byte(`{"operations":{"pending":2},"operationSummary":{"scope":"authorized-clusters","windowLimit":1000,"returned":2,"total":1001,"complete":false,"notice":"Recent window","futureObservation":{"generation":2}}}`))
		if err != nil {
			t.Error("owned fixture response failed")
		}
	}))
	t.Cleanup(server.Close)
	client, err := NewClientWithResponses(server.URL, WithHTTPClient(&http.Client{Timeout: 5 * time.Second}))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	response, err := client.GetCatalogControllerStatusWithResponse(ctx)
	if err != nil || response.JSON200 == nil || response.JSON200.Operations == nil || (*response.JSON200.Operations)["pending"] != 2 {
		t.Fatal("released generated response failed to retain legacy fields")
	}
	window, err := CatalogControllerWindowFromResponse(response)
	if err != nil || window == nil || window.Total == nil || *window.Total != 1001 || window.Complete || window.Returned != 2 {
		t.Fatal("additive typed reader failed through released transport")
	}
}

func TestCatalogControllerWindowUsesActualWrappedGeneratedHTTPClient(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/catalog/controller/status/" {
			t.Error("actual generated v1 route changed")
		}
		w.Header().Set("Content-Type", "application/json")
		// Match actual ControllerStatus -> RespondJSON; no response/schema mock.
		if _, err := w.Write([]byte(`{"data":{"operations":{"pending":2},"operationSummary":{"scope":"authorized-clusters","windowLimit":1000,"returned":2,"total":1001,"complete":false,"notice":"Recent window","futureObservation":{"generation":2}}}}`)); err != nil {
			t.Error("owned wrapped response failed")
		}
	}))
	t.Cleanup(server.Close)
	client, err := NewClientWithResponses(server.URL, WithHTTPClient(&http.Client{Timeout: 5 * time.Second}))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	response, err := client.GetCatalogControllerStatusWithResponse(ctx)
	if err != nil || response.JSON200 == nil {
		t.Fatal("released generated transport failed on the actual v1 envelope")
	}
	// The released flat JSON200 model is intentionally unchanged; it cannot
	// represent this pre-existing wrapper. The typed reader must use raw Body.
	if response.JSON200.Operations != nil {
		t.Fatal("legacy generated layout unexpectedly changed to unwrap the v1 envelope")
	}
	window, err := CatalogControllerWindowFromResponse(response)
	if err != nil || window == nil || window.Total == nil || *window.Total != 1001 || window.Complete || window.Returned != 2 {
		t.Fatal("typed window reader lost the actual wrapped additive response")
	}
}
