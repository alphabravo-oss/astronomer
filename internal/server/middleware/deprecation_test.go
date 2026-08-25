package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
)

func TestDeprecatedRoutePreservesResponseAndEmitsLifecycleHeaders(t *testing.T) {
	sunset := time.Date(2027, time.August, 23, 0, 0, 0, 0, time.UTC)
	handler := DeprecatedRoute(sunset, "/api/v1/replacement")(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("preserved"))
	}))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/legacy", nil))
	if recorder.Code != http.StatusAccepted || recorder.Body.String() != "preserved" {
		t.Fatalf("legacy response changed: %d %q", recorder.Code, recorder.Body.String())
	}
	if recorder.Header().Get("Deprecation") != "true" || recorder.Header().Get("Sunset") != "Mon, 23 Aug 2027 00:00:00 GMT" || recorder.Header().Get("Link") != `</api/v1/replacement>; rel="successor-version"` {
		t.Fatalf("deprecation headers are incomplete: %#v", recorder.Header())
	}
}

func TestDeprecatedRouteResolvesPathParameters(t *testing.T) {
	router := chi.NewRouter()
	deprecated := router.With(DeprecatedRoute(time.Date(2027, time.August, 23, 0, 0, 0, 0, time.UTC), "/api/v1/resources/{id}"))
	deprecated.Get("/legacy/{id}", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/legacy/cluster-123", nil))
	if got := recorder.Header().Get("Link"); got != `</api/v1/resources/cluster-123>; rel="successor-version"` {
		t.Fatalf("resolved successor Link = %q", got)
	}
}

func TestDeprecatedRouteRejectsHeaderInjection(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("unsafe successor did not panic during route construction")
		}
	}()
	_ = DeprecatedRoute(time.Now(), "/replacement\r\nX-Injected: true")
}
