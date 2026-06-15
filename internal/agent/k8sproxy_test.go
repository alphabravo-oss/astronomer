package agent

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"k8s.io/client-go/rest"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

func TestK8sProxyExecuteUpstreamStripsClientAuthHeaders(t *testing.T) {
	seen := make(chan http.Header, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	proxy := &K8sProxy{
		restConfig: &rest.Config{Host: server.URL},
		httpClient: server.Client(),
		log:        slog.Default(),
	}
	payload, err := json.Marshal(protocol.K8sRequestPayload{
		Method: http.MethodGet,
		Path:   "/api/v1/pods",
		Headers: map[string]string{
			"Accept":                    "application/json",
			"Authorization":             "Bearer browser-jwt",
			"Cookie":                    "astronomer_session=abc",
			"Host":                      "astronomer.example",
			"Impersonate-User":          "system:admin",
			"Impersonate-Group":         "system:masters",
			"Impersonate-Extra-Scopes":  "danger",
			"X-Forwarded-For":           "203.0.113.10",
			"X-Forwarded-Authorization": "Bearer forwarded",
		},
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	_, status, _, err := proxy.executeUpstream(context.Background(), &protocol.Message{Payload: payload})
	if err != nil {
		t.Fatalf("executeUpstream: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}

	headers := <-seen
	if got := headers.Get("Accept"); got != "application/json" {
		t.Fatalf("Accept = %q, want application/json", got)
	}
	for _, name := range []string{
		"Authorization",
		"Cookie",
		"Host",
		"Impersonate-User",
		"Impersonate-Group",
		"Impersonate-Extra-Scopes",
		"X-Forwarded-For",
		"X-Forwarded-Authorization",
	} {
		if got := headers.Get(name); got != "" {
			t.Fatalf("%s forwarded as %q", name, got)
		}
	}
}
