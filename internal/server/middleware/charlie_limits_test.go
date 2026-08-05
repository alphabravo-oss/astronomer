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
	for attempt := 0; attempt < 11; attempt++ {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, limitedCharlieRequest(user, ""))
		if attempt < 10 && recorder.Code == http.StatusTooManyRequests {
			t.Fatalf("request %d was prematurely limited", attempt+1)
		}
		if attempt == 10 && recorder.Code != http.StatusTooManyRequests {
			t.Fatalf("11th burst request status=%d, want 429", recorder.Code)
		}
	}
	if calls != 10 {
		t.Fatalf("handler calls=%d, want 10", calls)
	}
}

func TestCharlieSessionLimitsCapConcurrentSessionRequests(t *testing.T) {
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	handler := CharlieSessionLimits()(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		entered <- struct{}{}
		<-release
	}))
	user := uuid.NewString()
	session := uuid.NewString()
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			handler.ServeHTTP(httptest.NewRecorder(), limitedCharlieRequest(user, session))
		}()
	}
	<-entered
	<-entered
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, limitedCharlieRequest(user, session))
	if recorder.Code != http.StatusTooManyRequests {
		t.Fatalf("third concurrent session request status=%d, want 429", recorder.Code)
	}
	close(release)
	wg.Wait()
}
