package lokiauth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLokiAuth401sUntilHashesExist(t *testing.T) {
	h := New(Config{ListenAddr: ":8080", Upstream: "http://loki-gateway.monitoring.svc.cluster.local"})
	for _, path := range []string{"/loki/api/v1/push", "/loki/api/v1/query", "/ready"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, nil))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s status = %d, want 401", path, rec.Code)
		}
	}
}

func TestConfigFromEnvRequiresUpstream(t *testing.T) {
	t.Setenv("LOKI_UPSTREAM", "")
	t.Setenv("LISTEN_ADDR", "")
	if _, err := ConfigFromEnv(); err == nil {
		t.Fatal("expected error when LOKI_UPSTREAM is empty")
	}
}
