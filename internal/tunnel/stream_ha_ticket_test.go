package tunnel

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
)

// TestExecHandoffDoesNotConsumeTicketBeforeForward is the regression for the HA
// browser exec 401. Under two server replicas nginx pins the WS handshake to a
// non-owner pod. That pod must reverse-proxy the upgrade to the owner pod WITHOUT
// consuming the browser's single-use ?ticket= — otherwise the owner pod re-Validates,
// finds the ticket already burned, and returns 401 (~50% failure in HA).
//
// The store grants exactly one Validate per ticket. We forward through the
// non-owner pod's handler, then Validate the same ticket ourselves: it must
// still succeed, proving the non-owner pod did NOT consume it. Before the fix
// (authenticate-then-forward), the handler consumed the ticket first and this
// follow-up Validate would fail.
func TestExecHandoffDoesNotConsumeTicketBeforeForward(t *testing.T) {
	tickets := auth.NewStreamTicketStore(time.Minute)
	userID := uuid.New()
	clusterUUID := uuid.New()
	clusterID := clusterUUID.String()

	token, _, err := tickets.Issue(userID, auth.StreamKindExec, clusterUUID)
	if err != nil {
		t.Fatalf("issue ticket: %v", err)
	}

	forwarded := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		forwarded = true
		w.WriteHeader(http.StatusSwitchingProtocols)
	}))
	defer upstream.Close()

	hub := NewHub(slog.Default())
	hub.SetLocator(NewFakeLocatorForTest("self:9000", map[string]string{
		clusterID: strings.TrimPrefix(upstream.URL, "http://"),
	}))

	ec := NewExecConsumer(hub, slog.Default())
	ec.SetStreamTickets(tickets)

	router := chi.NewRouter()
	router.HandleFunc("/api/v1/ws/exec/{cluster_id}/{namespace}/{pod}/{container}/", ec.HandleExec)

	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/ws/exec/"+clusterID+"/default/web-0/app/?ticket="+token, nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if !forwarded {
		t.Fatalf("request was not forwarded to the owner pod (status=%d body=%s)", rec.Code, rec.Body.String())
	}

	// The non-owner pod must not have consumed the ticket — the owner pod needs it.
	if _, err := tickets.Validate(token, auth.StreamKindExec, clusterUUID); err != nil {
		t.Fatalf("ticket was consumed on the non-owner pod before forwarding: %v", err)
	}
}

// TestLogsHandoffDoesNotConsumeTicketBeforeForward is the same regression for the
// logs WS path.
func TestLogsHandoffDoesNotConsumeTicketBeforeForward(t *testing.T) {
	tickets := auth.NewStreamTicketStore(time.Minute)
	userID := uuid.New()
	clusterUUID := uuid.New()
	clusterID := clusterUUID.String()

	token, _, err := tickets.Issue(userID, auth.StreamKindLogs, clusterUUID)
	if err != nil {
		t.Fatalf("issue ticket: %v", err)
	}

	forwarded := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		forwarded = true
		w.WriteHeader(http.StatusSwitchingProtocols)
	}))
	defer upstream.Close()

	hub := NewHub(slog.Default())
	hub.SetLocator(NewFakeLocatorForTest("self:9000", map[string]string{
		clusterID: strings.TrimPrefix(upstream.URL, "http://"),
	}))

	lc := NewLogsConsumer(hub, slog.Default())
	lc.SetStreamTickets(tickets)

	router := chi.NewRouter()
	router.HandleFunc("/api/v1/ws/logs/{cluster_id}/{namespace}/{pod}/{container}/", lc.HandleLogs)

	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/ws/logs/"+clusterID+"/default/web-0/app/?ticket="+token, nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if !forwarded {
		t.Fatalf("request was not forwarded to the owner pod (status=%d body=%s)", rec.Code, rec.Body.String())
	}

	if _, err := tickets.Validate(token, auth.StreamKindLogs, clusterUUID); err != nil {
		t.Fatalf("ticket was consumed on the non-owner pod before forwarding: %v", err)
	}
}

// TestExecOwnerPodConsumesTicket confirms the pod that actually terminates the
// stream (no sibling owner) still authenticates by consuming the ticket, so the
// reorder didn't drop auth on the terminating path. With no locator entry the
// handler falls through to local handling: it Validates (consuming the ticket),
// then fails the WS Accept against the non-hijackable test recorder. The ticket
// must be gone afterward.
func TestExecOwnerPodConsumesTicket(t *testing.T) {
	tickets := auth.NewStreamTicketStore(time.Minute)
	userID := uuid.New()
	clusterUUID := uuid.New()
	clusterID := clusterUUID.String()

	token, _, err := tickets.Issue(userID, auth.StreamKindExec, clusterUUID)
	if err != nil {
		t.Fatalf("issue ticket: %v", err)
	}

	hub := NewHub(slog.Default()) // no locator → single pod, terminates locally
	ec := NewExecConsumer(hub, slog.Default())
	ec.SetStreamTickets(tickets)

	router := chi.NewRouter()
	router.HandleFunc("/api/v1/ws/exec/{cluster_id}/{namespace}/{pod}/{container}/", ec.HandleExec)

	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/ws/exec/"+clusterID+"/default/web-0/app/?ticket="+token, nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	// The terminating pod consumed the ticket during authentication.
	if _, err := tickets.Validate(token, auth.StreamKindExec, clusterUUID); err == nil {
		t.Fatal("terminating pod did not consume the ticket — auth was skipped on the stream-owning path")
	}
}
