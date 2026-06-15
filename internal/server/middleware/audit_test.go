package middleware

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// newTestLogger returns a slog.Logger that writes JSON to the provided buffer.
func newTestLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(buf, nil))
}

// okHandler is a simple handler that writes a 200 OK response.
var okHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	_, _ = w.Write([]byte("ok"))
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
		name     string
		path     string
		wantType string
		wantID   string
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
	_, _ = sw.Write([]byte("hello"))

	if sw.status != http.StatusOK {
		t.Fatalf("expected status 200 after Write(), got %d", sw.status)
	}
}

func TestStatusWriter_ExplicitWriteHeader(t *testing.T) {
	rr := httptest.NewRecorder()
	sw := &statusWriter{ResponseWriter: rr}

	sw.WriteHeader(http.StatusCreated)
	_, _ = sw.Write([]byte("created"))

	if sw.status != http.StatusCreated {
		t.Fatalf("expected status 201 after WriteHeader(201), got %d", sw.status)
	}
}

type fakeAuditWriter struct {
	lastV1 *sqlc.CreateAuditLogV1Params
}

type fakeAuditWriterV1Only struct {
	lastV1 *sqlc.CreateAuditLogV1Params
}

func (f *fakeAuditWriter) CreateAuditLogV1(_ context.Context, arg sqlc.CreateAuditLogV1Params) error {
	f.lastV1 = &arg
	return nil
}

func (f *fakeAuditWriterV1Only) CreateAuditLogV1(_ context.Context, arg sqlc.CreateAuditLogV1Params) error {
	f.lastV1 = &arg
	return nil
}

func TestAuditLogWithWriter_PersistsAuditRow(t *testing.T) {
	var buf bytes.Buffer
	writer := &fakeAuditWriter{}
	mw := AuditLogWithWriter(newTestLogger(&buf), writer)
	handler := mw(okHandler)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/660e8400-e29b-41d4-a716-446655440000/", nil)
	req.RemoteAddr = "203.0.113.9:1234"
	req.Header.Set("User-Agent", "audit-test")
	ctx := SetAuthenticatedUserForTest(req.Context(), &AuthenticatedUser{
		ID:         "550e8400-e29b-41d4-a716-446655440000",
		AuthMethod: "jwt",
	})
	ctx = context.WithValue(ctx, contextKey("request_id"), "req-1")
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if writer.lastV1 == nil {
		t.Fatal("expected audit v1 row to be persisted")
	}
	if writer.lastV1.Action != "request.post" {
		t.Fatalf("action = %q, want request.post", writer.lastV1.Action)
	}
	if writer.lastV1.ResourceType != "cluster" {
		t.Fatalf("resource_type = %q, want cluster", writer.lastV1.ResourceType)
	}
	if writer.lastV1.ResourceID != "660e8400-e29b-41d4-a716-446655440000" {
		t.Fatalf("resource_id = %q", writer.lastV1.ResourceID)
	}
	if writer.lastV1.RequestID != "req-1" {
		t.Fatalf("request_id = %q, want req-1", writer.lastV1.RequestID)
	}
	if writer.lastV1.CorrelationID != "req-1" {
		t.Fatalf("correlation_id = %q, want req-1", writer.lastV1.CorrelationID)
	}
	if writer.lastV1.Source != "http" {
		t.Fatalf("source = %q, want http", writer.lastV1.Source)
	}
	if !writer.lastV1.UserID.Valid || writer.lastV1.UserID.Bytes != uuid.MustParse("550e8400-e29b-41d4-a716-446655440000") {
		t.Fatalf("user_id = %+v, want authenticated user UUID", writer.lastV1.UserID)
	}
	if writer.lastV1.IpAddress == nil || writer.lastV1.IpAddress.String() != "203.0.113.9" {
		t.Fatalf("ip_address = %v, want 203.0.113.9", writer.lastV1.IpAddress)
	}
	var detail map[string]any
	if err := json.Unmarshal(writer.lastV1.Detail, &detail); err != nil {
		t.Fatalf("unmarshal detail: %v", err)
	}
	if detail["auth_method"] != "jwt" {
		t.Fatalf("auth_method = %v, want jwt", detail["auth_method"])
	}
	if writer.lastV1.StatusCode != int32(http.StatusOK) {
		t.Fatalf("audit v1 status_code = %d, want 200", writer.lastV1.StatusCode)
	}
	if writer.lastV1.HTTPMethod != http.MethodPost {
		t.Fatalf("audit v1 http_method = %q", writer.lastV1.HTTPMethod)
	}
}

func TestAuditLogWithWriter_V1OnlyWriter(t *testing.T) {
	var buf bytes.Buffer
	writer := &fakeAuditWriterV1Only{}
	mw := AuditLogWithWriter(newTestLogger(&buf), writer)
	handler := mw(okHandler)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/clusters/660e8400-e29b-41d4-a716-446655440000/", nil)
	req.RemoteAddr = "203.0.113.9:1234"
	req.Header.Set("User-Agent", "audit-test")
	ctx := SetAuthenticatedUserForTest(req.Context(), &AuthenticatedUser{
		ID:         "550e8400-e29b-41d4-a716-446655440000",
		AuthMethod: "jwt",
	})
	ctx = context.WithValue(ctx, contextKey("request_id"), "req-v1")
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if writer.lastV1 == nil {
		t.Fatal("expected audit v1 row to be persisted")
	}
	if writer.lastV1.Source != "http" {
		t.Fatalf("source = %q, want http", writer.lastV1.Source)
	}
	if writer.lastV1.CorrelationID != "req-v1" {
		t.Fatalf("correlation_id = %q, want req-v1", writer.lastV1.CorrelationID)
	}
	if writer.lastV1.Action != "request.delete" {
		t.Fatalf("action = %q, want request.delete", writer.lastV1.Action)
	}
	if writer.lastV1.RequestID != "req-v1" {
		t.Fatalf("request_id = %q, want req-v1", writer.lastV1.RequestID)
	}
}

func TestAuditLogWithWriter_SkipAuthPaths(t *testing.T) {
	var buf bytes.Buffer
	writer := &fakeAuditWriter{}
	mw := AuditLogWithWriter(newTestLogger(&buf), writer)
	handler := mw(okHandler)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if writer.lastV1 != nil {
		t.Fatal("expected skip path not to persist audit row")
	}
}
