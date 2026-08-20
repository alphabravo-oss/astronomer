package grafanaproxy

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

const testClusterA = "11111111-1111-1111-1111-111111111111"
const testClusterB = "22222222-2222-2222-2222-222222222222"

func TestInjectPromQLMatcherTable(t *testing.T) {
	ids := []string{testClusterA}
	wantEq := `{cluster_id="` + testClusterA + `"}`
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{name: "bare metric", in: "up", want: "up" + wantEq},
		{name: "selector job", in: `up{job="a"}`, want: `up{job="a",cluster_id="` + testClusterA + `"}`},
		{name: "replaces other cluster_id", in: `up{cluster_id="other"}`, want: `up{cluster_id="` + testClusterA + `"}`},
		{name: "replaces regex cluster_id", in: `up{cluster_id=~".+"}`, want: `up{cluster_id="` + testClusterA + `"}`},
		{name: "name matcher", in: `{__name__="up"}`, want: `{__name__="up",cluster_id="` + testClusterA + `"}`},
		{name: "rate matrix", in: "rate(http_requests_total[5m])", want: "rate(http_requests_total" + wantEq + "[5m])"},
		{name: "sum by", in: "sum by (job) (up)", want: "sum by (job) (up" + wantEq + ")"},
		{name: "binary", in: "foo / bar", want: "foo" + wantEq + " / bar" + wantEq},
		{name: "offset", in: "up offset 5m", want: "up" + wantEq + " offset 5m"},
		{name: "or vector", in: "count(up) or vector(0)", want: "count(up" + wantEq + ") or vector(0)"},
		{name: "time scalar", in: "time()", want: "time()"},
		{name: "vector scalar", in: "vector(1)", want: "vector(1)"},
		{name: "histogram_quantile", in: "histogram_quantile(0.99, sum by (le) (rate(x_bucket[5m])))", want: "histogram_quantile(0.99, sum by (le) (rate(x_bucket" + wantEq + "[5m])))"},
		{name: "label_replace", in: `label_replace(up, "dst", "$1", "src", "(.*)")`, want: `label_replace(up` + wantEq + `, "dst", "$1", "src", "(.*)")`},
		{name: "dashboard cluster var already substituted", in: `kube_node_info{cluster="c1"}`, want: `kube_node_info{cluster="c1",cluster_id="` + testClusterA + `"}`},
		{name: "recording rule name", in: "astronomer:http_5xx_ratio:5m", want: "astronomer:http_5xx_ratio:5m" + wantEq},
		{name: "empty", in: "", want: ""},
		{name: "unterminated selector", in: `{job="a"`, wantErr: true},
		{name: "unterminated string", in: `up{job="a}`, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := injectPromQLMatcher(tc.in, promClusterLabel, ids)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("got %q, want error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestInjectPromQLMatcherMultiCluster(t *testing.T) {
	got, err := injectPromQLMatcher("up", promClusterLabel, []string{testClusterA, testClusterB})
	if err != nil {
		t.Fatal(err)
	}
	want := `up{cluster_id=~"` + testClusterA + `|` + testClusterB + `"}`
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestInjectLogQLMatcherTable(t *testing.T) {
	ids := []string{testClusterA}
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{name: "stream selector", in: `{job="fluentbit"}`, want: `{job="fluentbit",cluster="` + testClusterA + `"}`},
		{name: "replaces other cluster", in: `{cluster="other"}`, want: `{cluster="` + testClusterA + `"}`},
		{name: "rate unwrap", in: `rate({job="x"}[5m])`, want: `rate({job="x",cluster="` + testClusterA + `"}[5m])`},
		{name: "line filter", in: `{job="x"} |= "error"`, want: `{job="x",cluster="` + testClusterA + `"} |= "error"`},
		{name: "sum rate", in: `sum(rate({app="api"}[5m]))`, want: `sum(rate({app="api",cluster="` + testClusterA + `"}[5m]))`},
		{name: "or streams", in: `{job="x"} or {job="y"}`, want: `{job="x",cluster="` + testClusterA + `"} or {job="y",cluster="` + testClusterA + `"}`},
		{name: "json pipe string braces", in: `{job="x"} | json | line_format "{{.foo}}"`, want: `{job="x",cluster="` + testClusterA + `"} | json | line_format "{{.foo}}"`},
		{name: "empty matcher", in: `{}`, want: `{cluster="` + testClusterA + `"}`},
		{name: "empty expr", in: "", want: ""},
		{name: "no selector fail closed", in: "vector(1)", wantErr: true},
		{name: "unterminated", in: `{job="x"`, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := injectLogQLMatcher(tc.in, lokiClusterLabel, ids)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("got %q, want error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func clusterScopedCookie(t *testing.T, key []byte, ids ...string) string {
	t.Helper()
	signed, err := signGrafanaAuth(key, grafanaAuth{
		Email: "scoped@example.com", Role: "Viewer", Explore: true, Admin: false,
		ClusterIDs: ids,
		Exp:        time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

func editorCookie(t *testing.T, key []byte) string {
	t.Helper()
	signed, err := signGrafanaAuth(key, grafanaAuth{
		Email: "ops@example.com", Role: "Editor", Explore: true, Admin: false,
		Exp: time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

func TestGrafana11QueryPayloadRewriteTable(t *testing.T) {
	type payloadCase struct {
		name     string
		method   string
		path     string
		body     string
		rawQuery string
		ct       string
		cookie   func(*testing.T, []byte) string
		wantExpr []string // substrings that must appear in the forwarded request
		forbid   []string
		status   int
		upstream bool
	}
	cases := []payloadCase{
		{
			name:   "cluster-scoped thanos instant",
			method: http.MethodPost, path: "/api/ds/query",
			ct: "application/json",
			body: `{
				"from":"now-1h","to":"now",
				"queries":[{"refId":"A","datasource":{"type":"prometheus","uid":"thanos"},"expr":"up","instant":true}]
			}`,
			cookie:   func(t *testing.T, key []byte) string { return clusterScopedCookie(t, key, testClusterA) },
			wantExpr: []string{`cluster_id="` + testClusterA + `"`},
			status:   http.StatusOK, upstream: true,
		},
		{
			name:   "cluster-scoped thanos range with other tenant matcher",
			method: http.MethodPost, path: "/api/ds/query",
			ct: "application/json",
			body: `{
				"queries":[{"refId":"A","datasource":{"uid":"thanos","type":"prometheus"},"expr":"rate(http_requests_total{cluster_id=\"evil\"}[5m])","range":true}]
			}`,
			cookie:   func(t *testing.T, key []byte) string { return clusterScopedCookie(t, key, testClusterA) },
			wantExpr: []string{`cluster_id="` + testClusterA + `"`},
			forbid:   []string{`cluster_id="evil"`},
			status:   http.StatusOK, upstream: true,
		},
		{
			name:   "cluster-scoped loki logql",
			method: http.MethodPost, path: "/api/ds/query",
			ct: "application/json",
			body: `{
				"queries":[{"refId":"A","datasource":{"type":"loki","uid":"loki"},"expr":"{job=\"fluentbit\"}","queryType":"range","maxLines":100}]
			}`,
			cookie:   func(t *testing.T, key []byte) string { return clusterScopedCookie(t, key, testClusterA) },
			wantExpr: []string{`cluster="` + testClusterA + `"`},
			forbid:   []string{`{job="fluentbit"}`},
			status:   http.StatusOK, upstream: true,
		},
		{
			name:   "cluster-scoped mixed thanos+loki",
			method: http.MethodPost, path: "/api/ds/query",
			ct: "application/json",
			body: `{
				"queries":[
					{"refId":"A","datasource":{"type":"prometheus","uid":"thanos"},"expr":"up"},
					{"refId":"B","datasource":{"type":"loki","uid":"loki"},"expr":"{job=\"x\"} |= \"error\""}
				]
			}`,
			cookie:   func(t *testing.T, key []byte) string { return clusterScopedCookie(t, key, testClusterA) },
			wantExpr: []string{`cluster_id="` + testClusterA + `"`, `cluster="` + testClusterA + `"`},
			status:   http.StatusOK, upstream: true,
		},
		{
			name:   "cluster-scoped extraFilters",
			method: http.MethodPost, path: "/api/ds/query",
			ct: "application/json",
			body: `{
				"queries":[{"refId":"A","datasource":{"type":"loki","uid":"loki"},"expr":"{job=\"x\"}","extraFilters":"namespace=\"kube\""}]
			}`,
			cookie:   func(t *testing.T, key []byte) string { return clusterScopedCookie(t, key, testClusterA) },
			wantExpr: []string{`namespace="kube",cluster="` + testClusterA + `"`},
			status:   http.StatusOK, upstream: true,
		},
		{
			name:   "legacy tsdb query",
			method: http.MethodPost, path: "/api/tsdb/query",
			ct: "application/json",
			body: `{
				"queries":[{"refId":"A","datasource":{"type":"prometheus","uid":"thanos"},"expr":"up"}]
			}`,
			cookie:   func(t *testing.T, key []byte) string { return clusterScopedCookie(t, key, testClusterA) },
			wantExpr: []string{`cluster_id="` + testClusterA + `"`},
			status:   http.StatusOK, upstream: true,
		},
		{
			name:     "grafana 11 numeric proxy query_range",
			method:   http.MethodPost,
			path:     "/api/datasources/proxy/1/api/v1/query_range",
			ct:       "application/x-www-form-urlencoded",
			body:     "query=up&start=1&end=2",
			cookie:   func(t *testing.T, key []byte) string { return clusterScopedCookie(t, key, testClusterA) },
			wantExpr: []string{"cluster_id%3D%22" + testClusterA + "%22", "query=up"},
			status:   http.StatusOK, upstream: true,
		},
		{
			name:     "grafana 11 uid resources query",
			method:   http.MethodGet,
			path:     "/api/datasources/uid/thanos/resources/api/v1/query",
			rawQuery: "query=up",
			cookie:   func(t *testing.T, key []byte) string { return clusterScopedCookie(t, key, testClusterA) },
			wantExpr: []string{"cluster_id"},
			status:   http.StatusOK, upstream: true,
		},
		{
			name:     "grafana 11 proxy uid series",
			method:   http.MethodGet,
			path:     "/api/datasources/proxy/uid/thanos/api/v1/series",
			rawQuery: "match[]=up",
			cookie:   func(t *testing.T, key []byte) string { return clusterScopedCookie(t, key, testClusterA) },
			wantExpr: []string{"cluster_id"},
			status:   http.StatusOK, upstream: true,
		},
		{
			name:     "grafana 11 loki resources logql",
			method:   http.MethodGet,
			path:     "/api/datasources/uid/loki/resources/loki/api/v1/query",
			rawQuery: "query=" + url.QueryEscape(`{job="fluentbit"}`),
			cookie:   func(t *testing.T, key []byte) string { return clusterScopedCookie(t, key, testClusterA) },
			wantExpr: []string{"cluster"},
			status:   http.StatusOK, upstream: true,
		},
		{
			name:     "labels without match gets selector",
			method:   http.MethodGet,
			path:     "/api/datasources/uid/thanos/resources/api/v1/labels",
			cookie:   func(t *testing.T, key []byte) string { return clusterScopedCookie(t, key, testClusterA) },
			wantExpr: []string{"match"},
			status:   http.StatusOK, upstream: true,
		},
		{
			name:   "unparseable promql is not forwarded",
			method: http.MethodPost, path: "/api/ds/query",
			ct: "application/json",
			body: `{
				"queries":[{"refId":"A","datasource":{"type":"prometheus","uid":"thanos"},"expr":"{job=\"unterminated"}]
			}`,
			cookie:   func(t *testing.T, key []byte) string { return clusterScopedCookie(t, key, testClusterA) },
			status:   http.StatusBadRequest,
			upstream: false,
		},
		{
			name:   "superuser thanos unchanged",
			method: http.MethodPost, path: "/api/ds/query",
			ct: "application/json",
			body: `{
				"queries":[{"refId":"A","datasource":{"type":"prometheus","uid":"thanos"},"expr":"up"}]
			}`,
			cookie:   adminCookie,
			wantExpr: []string{`"expr":"up"`},
			forbid:   []string{"cluster_id"},
			status:   http.StatusOK, upstream: true,
		},
		{
			name:   "global monitoring update unchanged",
			method: http.MethodPost, path: "/api/ds/query",
			ct: "application/json",
			body: `{
				"queries":[{"refId":"A","datasource":{"type":"prometheus","uid":"thanos"},"expr":"sum by (cluster_id) (up)"}]
			}`,
			cookie:   editorCookie,
			wantExpr: []string{`"expr":"sum by (cluster_id) (up)"`},
			status:   http.StatusOK, upstream: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var forwarded string
			var called bool
			p, key := testProxy(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				raw, _ := io.ReadAll(r.Body)
				forwarded = r.URL.RawQuery + "\n" + string(raw)
				w.WriteHeader(http.StatusOK)
			}), nil)
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			if tc.rawQuery != "" {
				req.URL.RawQuery = tc.rawQuery
			}
			if tc.ct != "" {
				req.Header.Set("Content-Type", tc.ct)
			}
			req.AddCookie(&http.Cookie{Name: grafanaAuthCookie, Value: tc.cookie(t, key)})
			rec := httptest.NewRecorder()
			p.ServeHTTP(rec, req)
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d body=%s", rec.Code, tc.status, rec.Body.String())
			}
			if called != tc.upstream {
				t.Fatalf("upstream called=%v, want %v forwarded=%s", called, tc.upstream, forwarded)
			}
			for _, needle := range tc.wantExpr {
				if !forwardedContains(forwarded, needle) {
					t.Fatalf("forwarded %q does not contain %q", forwarded, needle)
				}
			}
			for _, needle := range tc.forbid {
				if forwardedContains(forwarded, needle) {
					t.Fatalf("forwarded %q contains forbidden %q", forwarded, needle)
				}
			}
		})
	}
}

func TestClusterScopedExploreAllowedAfterRewrite(t *testing.T) {
	p, key := testProxy(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isExplorePath(r.URL.Path) {
			t.Errorf("path = %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}), nil)
	cookie := clusterScopedCookie(t, key, testClusterA)
	for _, path := range []string{"/explore", "/explore/", "/explore/foo"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.AddCookie(&http.Cookie{Name: grafanaAuthCookie, Value: cookie})
		rec := httptest.NewRecorder()
		p.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want 200", path, rec.Code)
		}
	}
}

func TestRewriteDoesNotClaimIsolationWithoutClusterIDs(t *testing.T) {
	var forwarded string
	p, key := testProxy(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		forwarded = string(raw)
		w.WriteHeader(http.StatusOK)
	}), nil)
	req := httptest.NewRequest(http.MethodPost, "/api/ds/query", strings.NewReader(`{"queries":[{"datasource":{"type":"prometheus","uid":"thanos"},"expr":"up"}]}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: grafanaAuthCookie, Value: viewerCookie(t, key)})
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(forwarded, `"expr":"up"`) {
		t.Fatalf("global viewer without cluster IDs must not be described as isolated: %s", forwarded)
	}
}

func TestDSQueryPreservesNonExprFields(t *testing.T) {
	body, err := rewriteDSQueryBody([]byte(`{
		"from":"now-6h","to":"now",
		"queries":[{"refId":"A","maxDataPoints":1000,"datasource":{"type":"prometheus","uid":"thanos"},"expr":"up"}]
	}`), []string{testClusterA})
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["from"] != "now-6h" {
		t.Fatalf("from = %v", payload["from"])
	}
	q := payload["queries"].([]any)[0].(map[string]any)
	if q["refId"] != "A" || q["maxDataPoints"].(float64) != 1000 {
		t.Fatalf("query fields = %+v", q)
	}
	if !strings.Contains(q["expr"].(string), testClusterA) {
		t.Fatalf("expr = %v", q["expr"])
	}
}

func forwardedContains(forwarded, needle string) bool {
	if strings.Contains(forwarded, needle) {
		return true
	}
	escaped := strings.ReplaceAll(needle, `"`, `\"`)
	return escaped != needle && strings.Contains(forwarded, escaped)
}
