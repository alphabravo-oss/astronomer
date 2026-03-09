package middleware

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

// newTestLogger returns a slog.Logger that writes JSON to the provided buffer.
func newTestLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(buf, nil))
}

// okHandler is a simple handler that writes a 200 OK response.
var okHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	w.Write([]byte("ok"))
})

func TestAuditLog_GETNotLogged(t *testing.T) {
	var buf bytes.Buffer
	mw := AuditLog(newTestLogger(&buf))
	handler := mw(okHandler)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if buf.Len() != 0 {
		t.Fatalf("expected no audit log for GET, got: %s", buf.String())
	}
}

func TestAuditLog_MutatingMethodsLogged(t *testing.T) {
	methods := []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete}

	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			var buf bytes.Buffer
			mw := AuditLog(newTestLogger(&buf))
			handler := mw(okHandler)

			req := httptest.NewRequest(method, "/api/v1/clusters/", nil)
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			if buf.Len() == 0 {
				t.Fatalf("expected audit log for %s, got nothing", method)
			}
		})
	}
}

func TestAuditLog_SkipPaths(t *testing.T) {
	skipPaths := []string{
		"/api/v1/auth/login",
		"/api/v1/auth/refresh",
	}

	for _, path := range skipPaths {
		t.Run(path, func(t *testing.T) {
			var buf bytes.Buffer
			mw := AuditLog(newTestLogger(&buf))
			handler := mw(okHandler)

			req := httptest.NewRequest(http.MethodPost, path, nil)
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			if buf.Len() != 0 {
				t.Fatalf("expected no audit log for skip path %s, got: %s", path, buf.String())
			}
		})
	}
}

func TestParsePathResource(t *testing.T) {
	tests := []struct {
		name         string
		path         string
		wantType     string
		wantID       string
	}{
		{
			name:     "clusters path",
			path:     "/api/v1/clusters/",
			wantType: "cluster",
			wantID:   "",
		},
		{
			name:     "cluster with UUID",
			path:     "/api/v1/clusters/550e8400-e29b-41d4-a716-446655440000/",
			wantType: "cluster",
			wantID:   "550e8400-e29b-41d4-a716-446655440000",
		},
		{
			name:     "nested workload",
			path:     "/api/v1/clusters/550e8400-e29b-41d4-a716-446655440000/workloads/",
			wantType: "workload",
			wantID:   "",
		},
		{
			name:     "nested workload with UUID",
			path:     "/api/v1/clusters/550e8400-e29b-41d4-a716-446655440000/workloads/660e8400-e29b-41d4-a716-446655440000",
			wantType: "workload",
			wantID:   "660e8400-e29b-41d4-a716-446655440000",
		},
		{
			name:     "uppercase UUID",
			path:     "/api/v1/clusters/550E8400-E29B-41D4-A716-446655440000/",
			wantType: "cluster",
			wantID:   "550E8400-E29B-41D4-A716-446655440000",
		},
		{
			name:     "empty path",
			path:     "",
			wantType: "",
			wantID:   "",
		},
		{
			name:     "no match path",
			path:     "/api/v1/unknown/",
			wantType: "",
			wantID:   "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotType, gotID := parsePathResource(tc.path)
			if gotType != tc.wantType {
				t.Errorf("parsePathResource(%q) type = %q, want %q", tc.path, gotType, tc.wantType)
			}
			if gotID != tc.wantID {
				t.Errorf("parsePathResource(%q) id = %q, want %q", tc.path, gotID, tc.wantID)
			}
		})
	}
}

func TestStatusWriter_ImplicitWrite(t *testing.T) {
	rr := httptest.NewRecorder()
	sw := &statusWriter{ResponseWriter: rr}

	// Write without calling WriteHeader should capture implicit 200.
	sw.Write([]byte("hello"))

	if sw.status != http.StatusOK {
		t.Fatalf("expected status 200 after Write(), got %d", sw.status)
	}
}

func TestStatusWriter_ExplicitWriteHeader(t *testing.T) {
	rr := httptest.NewRecorder()
	sw := &statusWriter{ResponseWriter: rr}

	sw.WriteHeader(http.StatusCreated)
	sw.Write([]byte("created"))

	if sw.status != http.StatusCreated {
		t.Fatalf("expected status 201 after WriteHeader(201), got %d", sw.status)
	}
}
