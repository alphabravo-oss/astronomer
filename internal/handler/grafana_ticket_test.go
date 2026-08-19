package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
	"github.com/google/uuid"
)

func grafanaTicketHandler(t *testing.T, bindings []rbac.RoleBinding) *MonitoringHandler {
	t.Helper()
	h, _ := newStackLifecycleHandler(t)
	h.SetServerURL("https://astronomer.example.com")
	h.SetGrafanaTickets(auth.NewGrafanaTicketStore(time.Minute))
	h.SetAuthorization(rbac.NewEngine(), stubMonitoringRBACQuerier{bindings: bindings})
	return h
}

func grafanaTicketAuthed(email string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/observability/grafana-ticket?return=https://grafana.astronomer.example.com/", nil)
	return req.WithContext(middleware.SetAuthenticatedUserForTest(req.Context(), &middleware.AuthenticatedUser{
		ID: uuid.NewString(), Email: email, AuthMethod: "jwt",
	}))
}

func TestGrafanaTicketAllowListRejectsOpenRedirect(t *testing.T) {
	h := grafanaTicketHandler(t, grantMonitoring())
	cases := []string{
		"https://evil.example.com/",
		"https://grafana.astronomer.example.com.evil.com/",
		"//evil.example.com/",
		"https://grafana.astronomer.example.com/explore",
		"http://grafana.astronomer.example.com/",
		"https://grafana.astronomer.example.com//evil",
		"https://user@grafana.astronomer.example.com/",
	}
	for _, ret := range cases {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/observability/grafana-ticket?return="+ret, nil)
		req = req.WithContext(middleware.SetAuthenticatedUserForTest(req.Context(), &middleware.AuthenticatedUser{
			ID: uuid.NewString(), Email: "a@example.com", AuthMethod: "jwt",
		}))
		rec := httptest.NewRecorder()
		h.MintGrafanaTicket(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("return %q status = %d, want 400 body=%s", ret, rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), ret) && strings.Contains(ret, "ticket") {
			t.Fatalf("response echoed return: %s", rec.Body.String())
		}
	}
}

func TestGrafanaTicketAllowListAcceptsHostRootAndCallback(t *testing.T) {
	h := grafanaTicketHandler(t, grantMonitoring())
	for _, ret := range []string{
		"https://grafana.astronomer.example.com/",
		"https://grafana.astronomer.example.com/auth/callback",
	} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/observability/grafana-ticket?return="+ret, nil)
		req = req.WithContext(middleware.SetAuthenticatedUserForTest(req.Context(), &middleware.AuthenticatedUser{
			ID: uuid.NewString(), Email: "a@example.com", AuthMethod: "jwt",
		}))
		rec := httptest.NewRecorder()
		h.MintGrafanaTicket(rec, req)
		if rec.Code != http.StatusFound {
			t.Fatalf("return %q status = %d, want 302: %s", ret, rec.Code, rec.Body.String())
		}
		loc := rec.Header().Get("Location")
		if !strings.Contains(loc, "ticket=") {
			t.Fatalf("location missing ticket: %s", loc)
		}
		if !strings.HasPrefix(loc, "https://grafana.astronomer.example.com/") {
			t.Fatalf("location host = %s", loc)
		}
	}
}

func TestGrafanaTicketRedeemOneUse(t *testing.T) {
	h := grafanaTicketHandler(t, grantMonitoring())
	mint := httptest.NewRecorder()
	h.MintGrafanaTicket(mint, grafanaTicketAuthed("ops@example.com"))
	if mint.Code != http.StatusFound {
		t.Fatalf("mint status = %d: %s", mint.Code, mint.Body.String())
	}
	loc, err := mint.Result().Location()
	if err != nil {
		t.Fatal(err)
	}
	ticket := loc.Query().Get("ticket")
	if ticket == "" {
		t.Fatal("missing ticket")
	}

	first := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/observability/grafana-ticket/redeem/", strings.NewReader(`{"ticket":"`+ticket+`"}`))
	h.RedeemGrafanaTicket(first, req)
	if first.Code != http.StatusOK {
		t.Fatalf("redeem status = %d: %s", first.Code, first.Body.String())
	}
	var wrap struct {
		Data struct {
			Email string `json:"email"`
			Role  string `json:"role"`
			TTL   int    `json:"ttl"`
		} `json:"data"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &wrap); err != nil {
		t.Fatal(err)
	}
	if wrap.Data.Email != "ops@example.com" || wrap.Data.Role == "" || wrap.Data.TTL <= 0 {
		t.Fatalf("redeem payload = %+v", wrap.Data)
	}

	second := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/observability/grafana-ticket/redeem/", strings.NewReader(`{"ticket":"`+ticket+`"}`))
	h.RedeemGrafanaTicket(second, req2)
	if second.Code != http.StatusUnauthorized {
		t.Fatalf("reuse status = %d, want 401: %s", second.Code, second.Body.String())
	}
}

func TestGrafanaTicketRedeemMissing(t *testing.T) {
	h := grafanaTicketHandler(t, grantMonitoring())
	rec := httptest.NewRecorder()
	h.RedeemGrafanaTicket(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`)))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestGrafanaTicketMintRequiresMonitoringRead(t *testing.T) {
	h := grafanaTicketHandler(t, denyMonitoring())
	rec := httptest.NewRecorder()
	h.MintGrafanaTicket(rec, grafanaTicketAuthed("a@example.com"))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestAllowListedGrafanaReturn(t *testing.T) {
	if _, err := allowListedGrafanaReturn("https://grafana.example.com/", "grafana.example.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := allowListedGrafanaReturn("https://evil.com/", "grafana.example.com"); err == nil {
		t.Fatal("expected reject")
	}
}
