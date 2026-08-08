package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

func limitedCharlieRequest(userID, sessionID string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "192.0.2.10:1234"
	ctx := SetAuthenticatedUserForTest(r.Context(), &AuthenticatedUser{ID: userID, AuthMethod: "jwt"})
	if sessionID != "" {
		route := chi.NewRouteContext()
		route.URLParams.Add("session_id", sessionID)
		ctx = context.WithValue(ctx, chi.RouteCtxKey, route)
	}
	return r.WithContext(ctx)
}

func TestCharlieSessionLimitsApplyPerUserAndIP(t *testing.T) {
	calls := 0
	handler := CharlieSessionLimits()(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
	user := uuid.NewString()
	for attempt := 0; attempt < charlieShortRequestBurst+1; attempt++ {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, limitedCharlieRequest(user, ""))
		if attempt < charlieShortRequestBurst && recorder.Code == http.StatusTooManyRequests {
			t.Fatalf("request %d was prematurely limited", attempt+1)
		}
		if attempt == charlieShortRequestBurst && recorder.Code != http.StatusTooManyRequests {
			t.Fatalf("request past burst status=%d, want 429", recorder.Code)
		}
	}
	if calls != charlieShortRequestBurst {
		t.Fatalf("handler calls=%d, want %d", calls, charlieShortRequestBurst)
	}
}

func TestCharlieSessionLimitsCapConcurrentSessionRequests(t *testing.T) {
	const cap = charlieSessionConcurrency
	entered := make(chan struct{}, cap)
	release := make(chan struct{})
	handler := CharlieSessionLimits()(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		entered <- struct{}{}
		<-release
	}))
	user := uuid.NewString()
	session := uuid.NewString()
	var wg sync.WaitGroup
	for range cap {
		wg.Add(1)
		go func() {
			defer wg.Done()
			handler.ServeHTTP(httptest.NewRecorder(), limitedCharlieRequest(user, session))
		}()
	}
	for range cap {
		<-entered
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, limitedCharlieRequest(user, session))
	if recorder.Code != http.StatusTooManyRequests {
		t.Fatalf("concurrent session request past cap status=%d, want 429", recorder.Code)
	}
	close(release)
	wg.Wait()
}

func TestCharlieSessionLimitsEventStreamsSkipShortRequestTokens(t *testing.T) {
	handler := CharlieSessionLimits()(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	user := uuid.NewString()
	// Exhaust the short-request token burst with non-stream calls.
	for attempt := 0; attempt < charlieShortRequestBurst+1; attempt++ {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, limitedCharlieRequest(user, ""))
		if attempt < charlieShortRequestBurst && recorder.Code == http.StatusTooManyRequests {
			t.Fatalf("request %d was prematurely limited", attempt+1)
		}
	}
	// A live event stream open must still be admitted under concurrency alone.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/charlie/sessions/session-1/events/", nil)
	req.RemoteAddr = "192.0.2.10:1234"
	req = req.WithContext(SetAuthenticatedUserForTest(req.Context(), &AuthenticatedUser{ID: user, AuthMethod: "jwt"}))
	route := chi.NewRouteContext()
	route.URLParams.Add("session_id", "session-1")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, route))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	if recorder.Code == http.StatusTooManyRequests {
		t.Fatal("event stream open was rate-token limited after short-request burst exhaustion")
	}
}
