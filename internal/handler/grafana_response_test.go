package handler

import (
	"encoding/base64"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

func TestGrafanaResponseRepairsKubernetesRewrittenAssets(t *testing.T) {
	prefix := "/api/v1/namespaces/monitoring/services/http:prometheus-grafana-proxy:8080/proxy"
	public := clusterGrafanaProxyPath(stackTestClusterID)
	html := `<base href="` + prefix + public + `"><link href="` + prefix + public + `public/build/app.css"><script src="public/build/app.js"></script>`
	resp := &protocol.K8sResponsePayload{StatusCode: 200, Headers: map[string]string{"Content-Type": "text/html; charset=utf-8", "Set-Cookie": "grafana_session=untrusted", "Location": prefix + public + "login"}, Body: base64.StdEncoding.EncodeToString([]byte(html))}
	w := httptest.NewRecorder()
	writeGrafanaResponse(w, httptest.NewRequest("GET", public, nil), resp, prefix)
	if strings.Contains(w.Body.String(), prefix) || !strings.Contains(w.Body.String(), `<base href="`+public+`">`) {
		t.Fatalf("broken public URLs: %s", w.Body.String())
	}
	if w.Header().Get("Location") != public+"login" || w.Header().Get("Set-Cookie") != "" {
		t.Fatalf("unsafe headers: %v", w.Header())
	}
}

func TestGrafanaRequestHeadersExcludeBrowserIdentityAndCompression(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Accept-Encoding", "gzip, br")
	r.Header.Set("X-WEBAUTH-USER", "attacker@example.com")
	r.Header.Set("X-WEBAUTH-ROLE", "Admin")
	r.Header.Set("Cookie", "astronomer_session=private")
	r.Header.Set("Authorization", "Bearer private")
	headers := grafanaRequestHeaders(r.Header)
	if len(headers) != 1 || headers["Accept-Encoding"] != "identity" {
		t.Fatalf("unexpected headers: %v", headers)
	}
}
