package lokiauth

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestHashBearerSHA256Hex(t *testing.T) {
	got := HashBearer("secret-token")
	if len(got) != 64 {
		t.Fatalf("hash length = %d, want 64 hex chars", len(got))
	}
	if HashBearer("secret-token") != got {
		t.Fatal("hash must be deterministic")
	}
	if HashBearer("other") == got {
		t.Fatal("different tokens must not collide")
	}
}

func TestSelectOrgMatrix(t *testing.T) {
	c1 := "11111111-1111-1111-1111-111111111111"
	c2 := "22222222-2222-2222-2222-222222222222"
	cases := []struct {
		name      string
		clientOrg string
		allow     []string
		wantOrg   string
		wantCode  int
	}{
		{name: "use client org in list", clientOrg: c1, allow: []string{c1, c2}, wantOrg: c1},
		{name: "missing and sole tenant", clientOrg: "", allow: []string{c1}, wantOrg: c1},
		{name: "missing and many tenants", clientOrg: "", allow: []string{c1, c2}, wantCode: http.StatusBadRequest},
		{name: "missing and empty list", clientOrg: "", allow: nil, wantCode: http.StatusUnauthorized},
		{name: "client org outside list", clientOrg: c2, allow: []string{c1}, wantCode: http.StatusUnauthorized},
		{name: "empty acl deny", clientOrg: c1, allow: []string{}, wantCode: http.StatusUnauthorized},
		{name: "duplicate sole tenant defaults", clientOrg: "", allow: []string{c1, c1, " " + c1}, wantOrg: c1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			org, status := SelectOrg(tc.clientOrg, tc.allow)
			if status != tc.wantCode {
				t.Fatalf("status = %d, want %d", status, tc.wantCode)
			}
			if tc.wantCode == 0 && org != tc.wantOrg {
				t.Fatalf("org = %q, want %q", org, tc.wantOrg)
			}
		})
	}
}

func testHandler(t *testing.T, upstream http.Handler, hashes map[string]string, acl QueryACL) http.Handler {
	t.Helper()
	var upURL *url.URL
	if upstream != nil {
		srv := httptest.NewServer(upstream)
		t.Cleanup(srv.Close)
		u, err := url.Parse(srv.URL)
		if err != nil {
			t.Fatal(err)
		}
		upURL = u
	} else {
		upURL, _ = url.Parse("http://127.0.0.1:9")
	}
	if acl.Users == nil {
		acl.Users = map[string][]string{}
	}
	return New(Config{
		ListenAddr: ":0",
		Upstream:   upURL,
		Hashes:     func() map[string]string { return hashes },
		ACL:        func() QueryACL { return acl },
	})
}

func TestPushRejectsMissingAndUnknownToken(t *testing.T) {
	cluster := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	token := "good-token"
	h := testHandler(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("upstream should not be called, path=%s", r.URL.Path)
	}), map[string]string{cluster: HashBearer(token)}, QueryACL{})

	for _, hdr := range []string{"", "Bearer unknown", "Basic abc"} {
		req := httptest.NewRequest(http.MethodPost, "/loki/api/v1/push", strings.NewReader("{}"))
		if hdr != "" {
			req.Header.Set("Authorization", hdr)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("auth %q status = %d, want 401", hdr, rec.Code)
		}
	}
}

func TestPushRejectsOrgMismatchAndOverridesBoundOrg(t *testing.T) {
	cluster := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	token := "good-token"
	var gotOrg string
	h := testHandler(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotOrg = r.Header.Get("X-Scope-OrgID")
		if r.Header.Get("Authorization") != "" {
			t.Error("bearer must not be forwarded to Loki")
		}
		w.WriteHeader(http.StatusNoContent)
	}), map[string]string{cluster: HashBearer(token)}, QueryACL{})

	mismatch := httptest.NewRequest(http.MethodPost, "/loki/api/v1/push", strings.NewReader("{}"))
	mismatch.Header.Set("Authorization", "Bearer "+token)
	mismatch.Header.Set("X-Scope-OrgID", "other-cluster")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, mismatch)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("mismatch status = %d, want 401", rec.Code)
	}

	ok := httptest.NewRequest(http.MethodPost, "/loki/api/v1/push", strings.NewReader("{}"))
	ok.Header.Set("Authorization", "Bearer "+token)
	okRec := httptest.NewRecorder()
	h.ServeHTTP(okRec, ok)
	if okRec.Code != http.StatusNoContent {
		t.Fatalf("matching push status = %d, want 204 body=%s", okRec.Code, okRec.Body.String())
	}
	if gotOrg != cluster {
		t.Fatalf("proxied X-Scope-OrgID = %q, want bound cluster", gotOrg)
	}
}

func TestQueryOrgSelectionAndACL(t *testing.T) {
	c1 := "11111111-1111-1111-1111-111111111111"
	c2 := "22222222-2222-2222-2222-222222222222"
	hashes := map[string]string{c1: HashBearer("t1"), c2: HashBearer("t2")}
	acl := QueryACL{
		Admins: []string{"admin@example.com"},
		Users:  map[string][]string{"viewer@example.com": {c1}},
	}
	var gotOrg string
	h := testHandler(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotOrg = r.Header.Get("X-Scope-OrgID")
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "success"})
	}), hashes, acl)

	query := func(user, org string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/loki/api/v1/query?query={}", nil)
		if user != "" {
			req.Header.Set("X-Grafana-User", user)
		}
		if org != "" {
			req.Header.Set("X-Scope-OrgID", org)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	if rec := query("viewer@example.com", c1); rec.Code != http.StatusOK {
		t.Fatalf("viewer in-list status = %d", rec.Code)
	}
	if gotOrg != c1 {
		t.Fatalf("viewer org = %q", gotOrg)
	}
	if rec := query("viewer@example.com", ""); rec.Code != http.StatusOK || gotOrg != c1 {
		t.Fatalf("viewer sole tenant status=%d org=%q", rec.Code, gotOrg)
	}
	if rec := query("viewer@example.com", c2); rec.Code != http.StatusUnauthorized {
		t.Fatalf("viewer outside list status = %d, want 401", rec.Code)
	}
	if rec := query("admin@example.com", ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("admin missing org with 2 tenants status = %d, want 400", rec.Code)
	}
	if rec := query("admin@example.com", c2); rec.Code != http.StatusOK || gotOrg != c2 {
		t.Fatalf("admin selected org status=%d org=%q", rec.Code, gotOrg)
	}
	if rec := query("nobody@example.com", c1); rec.Code != http.StatusUnauthorized {
		t.Fatalf("unknown user status = %d, want 401", rec.Code)
	}
}

func TestReadyDoesNotRequireToken(t *testing.T) {
	h := testHandler(t, nil, nil, QueryACL{})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ready", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("/ready status = %d, want 200", rec.Code)
	}
	body, _ := io.ReadAll(rec.Body)
	if strings.TrimSpace(string(body)) != "ok" {
		t.Fatalf("/ready body = %q", body)
	}
}

func TestConfigFromEnvRequiresUpstream(t *testing.T) {
	t.Setenv("LOKI_UPSTREAM", "")
	t.Setenv("LISTEN_ADDR", "")
	if _, err := ConfigFromEnv(); err == nil {
		t.Fatal("expected error when LOKI_UPSTREAM is empty")
	}
}

func TestQuery401sUntilHashesAndACLExist(t *testing.T) {
	h := New(Config{
		ListenAddr: ":8080",
		Upstream:   mustURL(t, "http://loki-gateway.monitoring.svc.cluster.local"),
		Hashes:     func() map[string]string { return map[string]string{} },
		ACL:        func() QueryACL { return QueryACL{} },
	})
	for _, path := range []string{"/loki/api/v1/push", "/loki/api/v1/query"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, nil))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s status = %d, want 401", path, rec.Code)
		}
	}
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}
