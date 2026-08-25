package middleware

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestBoundRequestBodiesRejectsDeclaredOverflow(t *testing.T) {
	called := false
	handler := BoundRequestBodies(4, time.Second)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login/", strings.NewReader("12345"))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusRequestEntityTooLarge || called {
		t.Fatalf("status=%d called=%v", response.Code, called)
	}
	if got := response.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type=%q", got)
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil || body.Error.Code != "request_body_too_large" {
		t.Fatalf("error envelope code=%q err=%v", body.Error.Code, err)
	}
}

func TestBoundRequestBodiesIncludesRequestID(t *testing.T) {
	handler := BoundRequestBodies(4, time.Second)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("oversized request reached handler")
	}))
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login/", strings.NewReader("12345"))
	request = request.WithContext(context.WithValue(request.Context(), requestIDKey, "request-413"))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var body struct {
		Error struct {
			RequestID string `json:"request_id"`
		} `json:"error"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil || body.Error.RequestID != "request-413" {
		t.Fatalf("request_id=%q err=%v", body.Error.RequestID, err)
	}
}

func TestBoundRequestBodiesCapsChunkedBody(t *testing.T) {
	handler := BoundRequestBodies(4, time.Second)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := io.ReadAll(r.Body)
		if _, ok := err.(*http.MaxBytesError); !ok {
			t.Fatalf("body read error=%T %v", err, err)
		}
		w.WriteHeader(http.StatusRequestEntityTooLarge)
	}))
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login/", strings.NewReader("12345"))
	request.ContentLength = -1
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status=%d", response.Code)
	}
}

func TestBoundRequestBodiesLeavesStreamsAndUpgradesUntouched(t *testing.T) {
	for _, request := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/api/v1/events/", nil),
		httptest.NewRequest(http.MethodPost, "/api/v1/ws/tunnel/", strings.NewReader("body")),
	} {
		if request.Method == http.MethodPost {
			request.Header.Set("Connection", "Upgrade")
			request.Header.Set("Upgrade", "websocket")
		}
		original := request.Body
		BoundRequestBodies(1, time.Second)(http.HandlerFunc(func(_ http.ResponseWriter, got *http.Request) {
			if got.Body != original {
				t.Fatal("stream or upgrade body was wrapped")
			}
		})).ServeHTTP(httptest.NewRecorder(), request)
	}
}
