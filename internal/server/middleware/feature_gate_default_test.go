package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

type fixedFeatureReader struct {
	value bool
}

func (f fixedFeatureReader) BoolValue(context.Context, string, bool) bool { return f.value }

func TestFeatureGateDefaultOptInFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name   string
		reader FeatureFlagReader
	}{
		{name: "nil reader", reader: nil},
		{name: "false value", reader: fixedFeatureReader{value: false}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true })
			rr := httptest.NewRecorder()

			FeatureGateDefault("feature.charlie", tc.reader, false)(next).ServeHTTP(
				rr,
				httptest.NewRequest(http.MethodGet, "/api/v1/charlie/status/", nil),
			)

			if rr.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404", rr.Code)
			}
			if called {
				t.Fatal("disabled opt-in gate called the protected handler")
			}
		})
	}
}

func TestFeatureGateDefaultOptInAllowsExplicitTrue(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	})
	rr := httptest.NewRecorder()

	FeatureGateDefault("feature.charlie", fixedFeatureReader{value: true}, false)(next).ServeHTTP(
		rr,
		httptest.NewRequest(http.MethodGet, "/api/v1/charlie/status/", nil),
	)

	if !called || rr.Code != http.StatusNoContent {
		t.Fatalf("called=%v status=%d, want true/204", called, rr.Code)
	}
}
