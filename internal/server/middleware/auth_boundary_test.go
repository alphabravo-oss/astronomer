package middleware

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/tunnel"
)

func TestAuthRequiresAccessJWT(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"access", "refresh", auth.PurposeTOTPChallenge, auth.PurposeTOTPEnrollOnly} {
		for _, transport := range []string{"bearer", "cookie"} {
			t.Run(kind+"/"+transport, func(t *testing.T) {
				t.Parallel()
				manager := newTestJWTManager()
				userID := uuid.New()
				var token string
				var err error
				switch kind {
				case "access":
					token, err = manager.GenerateAccessToken(userID)
				case "refresh":
					token, err = manager.GenerateRefreshToken(userID)
				default:
					token, err = manager.GeneratePurposeToken(userID, kind, time.Minute)
				}
				if err != nil {
					t.Fatal(err)
				}
				request := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/", nil)
				if transport == "cookie" {
					request.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: token})
				} else {
					request.Header.Set("Authorization", "Bearer "+token)
				}
				called := false
				handler := Auth(manager)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					called = true
					w.WriteHeader(http.StatusNoContent)
				}))
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, request)
				want := http.StatusUnauthorized
				if kind == "access" {
					want = http.StatusNoContent
				}
				if response.Code != want || called != (kind == "access") {
					t.Fatalf("status=%d handler_called=%v, want status=%d", response.Code, called, want)
				}
			})
		}
	}
}

// Exercise the same client identity and CIDR contract through REST, direct
// streaming, and the real HTTP reverse proxy to a trusted/untrusted sibling.
func TestAPITokenCIDRBoundaryParity(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, cidrs, remote, forwarded, trusted string
		allowed                                 bool
	}{
		{name: "matching IPv4", cidrs: "192.0.2.0/24", remote: "192.0.2.7:1234", allowed: true},
		{name: "denied IPv4", cidrs: "192.0.2.0/24", remote: "198.51.100.7:1234"},
		{name: "matching IPv6", cidrs: "2001:db8::/32", remote: "[2001:db8::7]:1234", allowed: true},
		{name: "denied IPv6", cidrs: "2001:db8::/32", remote: "[2001:db9::7]:1234"},
		{name: "mapped IPv4", cidrs: "192.0.2.0/24", remote: "[::ffff:192.0.2.7]:1234", allowed: true},
		{name: "malformed CIDR", cidrs: "192.0.2.0/24,broken", remote: "192.0.2.7:1234"},
		{name: "empty networks", cidrs: ", ,", remote: "192.0.2.7:1234"},
		{name: "unrestricted", cidrs: " ", remote: "192.0.2.7:1234", allowed: true},
		{name: "invalid client", cidrs: "192.0.2.0/24", remote: "invalid"},
		{name: "spoofed header", cidrs: "192.0.2.0/24", remote: "198.51.100.7:1234", forwarded: "192.0.2.7"},
		{name: "trusted proxy", cidrs: "192.0.2.0/24", remote: "10.0.0.1:1234", forwarded: "192.0.2.7", trusted: "10.0.0.0/8", allowed: true},
		{name: "trusted chain", cidrs: "192.0.2.0/24", remote: "10.0.0.1:1234", forwarded: "192.0.2.7, 10.0.0.2", trusted: "10.0.0.0/8", allowed: true},
		{name: "spoofed chain prefix", cidrs: "192.0.2.0/24", remote: "10.0.0.1:1234", forwarded: "192.0.2.7, 198.51.100.7", trusted: "10.0.0.0/8"},
	}
	for _, tc := range cases {
		for _, mode := range []string{"REST", "stream", "trusted sibling", "untrusted sibling"} {
			t.Run(tc.name+"/"+mode, func(t *testing.T) {
				t.Parallel()
				manager := newTestJWTManager()
				q := newFakeWithCIDRs(t, tc.cidrs)
				var handler http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					_, ok := auth.AuthorizeStreamRequestWithTickets(r, q, manager, nil, auth.StreamKindExec, uuid.Nil)
					if !ok {
						w.WriteHeader(http.StatusUnauthorized)
						return
					}
					w.WriteHeader(http.StatusNoContent)
				})
				if mode == "REST" {
					handler = AuthWithQueries(manager, q)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						w.WriteHeader(http.StatusNoContent)
					}))
				}
				if strings.HasSuffix(mode, "sibling") {
					trust := ""
					if mode == "trusted sibling" {
						trust = "127.0.0.0/8,::1/128"
					}
					upstream := httptest.NewServer(TrustedRealIP(trust)(handler))
					t.Cleanup(upstream.Close)
					clusterID := uuid.NewString()
					hub := tunnel.NewHub(slog.Default())
					hub.SetLocator(tunnel.NewFakeLocatorForTest("self:9000", map[string]string{
						clusterID: strings.TrimPrefix(upstream.URL, "http://"),
					}))
					handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if !tunnel.ForwardWSToOwnerPod(hub, slog.Default(), w, r, clusterID) {
							t.Error("request did not reach sibling")
						}
					})
				}
				request := httptest.NewRequest(http.MethodGet, "/api/v1/ws/exec/", nil)
				request.Header.Set("Authorization", "Bearer astro_test_token")
				request.Header.Set("X-Forwarded-For", tc.forwarded)
				request.RemoteAddr = tc.remote
				response := httptest.NewRecorder()
				TrustedRealIP(tc.trusted)(handler).ServeHTTP(response, request)
				allowed := tc.allowed
				if mode == "untrusted sibling" && strings.TrimSpace(tc.cidrs) != "" {
					allowed = false
				}
				want := http.StatusUnauthorized
				if allowed {
					want = http.StatusNoContent
				}
				if response.Code != want {
					t.Fatalf("status=%d, want %d", response.Code, want)
				}
			})
		}
	}
}
