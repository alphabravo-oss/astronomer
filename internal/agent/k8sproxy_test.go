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

	res, err := proxy.executeUpstream(context.Background(), &protocol.Message{Payload: payload})
	if err != nil {
		t.Fatalf("executeUpstream: %v", err)
	}
	defer res.Release()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
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

func TestK8sProxyInjectsTypedGrafanaAuthOnlyForDedicatedService(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		auth       *protocol.GrafanaProxyAuth
		wantCookie string
		wantTicket string
	}{
		{
			name:       "dedicated proxy",
			path:       "/api/v1/namespaces/monitoring/services/http:astronomer-grafana-grafana-proxy:8080/proxy/",
			auth:       &protocol.GrafanaProxyAuth{Cookie: "signed.cookie", Ticket: "one-use-ticket"},
			wantCookie: protocol.GrafanaProxyCookieName + "=signed.cookie",
			wantTicket: "one-use-ticket",
		},
		{
			name: "ordinary service cannot receive credentials",
			path: "/api/v1/namespaces/monitoring/services/http:prometheus:9090/proxy/",
			auth: &protocol.GrafanaProxyAuth{Cookie: "signed.cookie", Ticket: "one-use-ticket"},
		},
		{
			name: "control characters are rejected",
			path: "/api/v1/namespaces/monitoring/services/http:astronomer-grafana-grafana-proxy:8080/proxy/",
			auth: &protocol.GrafanaProxyAuth{Cookie: "bad;cookie", Ticket: "bad\nticket"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			seen := make(chan http.Header, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				seen <- r.Header.Clone()
				w.WriteHeader(http.StatusOK)
			}))
			defer server.Close()
			proxy := &K8sProxy{restConfig: &rest.Config{Host: server.URL}, httpClient: server.Client(), log: slog.Default()}
			payload, err := json.Marshal(protocol.K8sRequestPayload{Method: http.MethodGet, Path: tc.path, GrafanaAuth: tc.auth})
			if err != nil {
				t.Fatal(err)
			}
			res, err := proxy.executeUpstream(context.Background(), &protocol.Message{Payload: payload})
			if err != nil {
				t.Fatal(err)
			}
			defer res.Release()
			headers := <-seen
			if got := headers.Get("Cookie"); got != tc.wantCookie {
				t.Fatalf("Cookie = %q, want %q", got, tc.wantCookie)
			}
			if got := headers.Get(protocol.GrafanaProxyTicketHeader); got != tc.wantTicket {
				t.Fatalf("ticket = %q, want %q", got, tc.wantTicket)
			}
		})
	}
}
