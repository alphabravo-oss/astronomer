package apiutil

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestFromErrorMapsDomainErrors(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		err     error
		code    Code
		status  int
		connect connect.Code
	}{
		{"not found sentinel", fmt.Errorf("cluster x: %w", ErrNotFound), NotFound, 404, connect.CodeNotFound},
		{"pgx no rows", fmt.Errorf("get: %w", pgx.ErrNoRows), NotFound, 404, connect.CodeNotFound},
		{"already exists", ErrAlreadyExists, AlreadyExists, 409, connect.CodeAlreadyExists},
		{"invalid argument", ErrInvalidArgument, ValidationError, 400, connect.CodeInvalidArgument},
		{"failed precondition", ErrFailedPrecondition, FailedPrecondition, 409, connect.CodeFailedPrecondition},
		{"conflict", ErrConflict, ConcurrentModification, 409, connect.CodeAborted},
		{"permission", ErrPermissionDenied, PermissionDenied, 403, connect.CodePermissionDenied},
		{"unauthenticated", ErrUnauthenticated, AuthenticationRequired, 401, connect.CodeUnauthenticated},
		{"unavailable", ErrUnavailable, ServiceUnavailable, 503, connect.CodeUnavailable},
		{"deadline", context.DeadlineExceeded, DeadlineExceeded, 504, connect.CodeDeadlineExceeded},
		{"canceled", context.Canceled, RequestCanceled, 408, connect.CodeCanceled},
		{"unique", &pgconn.PgError{Code: "23505"}, AlreadyExists, 409, connect.CodeAlreadyExists},
		{"fk delete", &pgconn.PgError{Code: "23503", Message: `update or delete on table "a" violates foreign key constraint "f" on table "b"`}, ResourceInUse, 409, connect.CodeFailedPrecondition},
		{"fk insert", &pgconn.PgError{Code: "23503", Message: `insert or update on table "b" violates foreign key constraint "f"`}, InvalidReference, 400, connect.CodeInvalidArgument},
		{"check", &pgconn.PgError{Code: "23514"}, ValidationError, 400, connect.CodeInvalidArgument},
		{"serialization", &pgconn.PgError{Code: "40001"}, ConcurrentModification, 409, connect.CodeAborted},
		{"connection", &pgconn.PgError{Code: "08006"}, DBUnavailable, 503, connect.CodeUnavailable},
		{"typed", New(BundleNotReady, "not yet"), BundleNotReady, 409, connect.CodeFailedPrecondition},
		{"unmapped", errors.New("password=hunter2 dial tcp 10.0.0.1"), InternalError, 500, connect.CodeInternal},
	}
	for _, tc := range cases {
		got := FromError(tc.err)
		if got.Code != tc.code || got.statusOf() != tc.status || got.ConnectCode() != tc.connect {
			t.Errorf("%s: got (%s, %d, %s), want (%s, %d, %s)", tc.name, got.Code, got.statusOf(), got.ConnectCode(), tc.code, tc.status, tc.connect)
		}
		if tc.code != BundleNotReady && !errors.Is(got, tc.err) {
			t.Errorf("%s: cause not preserved", tc.name)
		}
	}
	if FromError(nil) != nil {
		t.Fatal("FromError(nil) must be nil")
	}
}

func TestRespondScrubsInternalCause(t *testing.T) {
	t.Parallel()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/x", nil)
	Respond(w, r, errors.New("password=hunter2 host=internal-db:5432"))
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", w.Code)
	}
	if strings.Contains(w.Body.String(), "hunter2") || strings.Contains(w.Body.String(), "internal-db") {
		t.Fatalf("internal cause leaked: %s", w.Body.String())
	}
	var body Body
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Code != InternalError || body.Error.RequestID == "" {
		t.Fatalf("body = %+v", body)
	}
	if ce := ConnectError(errors.New("password=hunter2")); ce.Code() != connect.CodeInternal || strings.Contains(ce.Message(), "hunter2") {
		t.Fatalf("connect error leaked or misclassified: %v", ce)
	}
}

func TestWriteErrorCanceledClient(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w := httptest.NewRecorder()
	WriteError(w, httptest.NewRequest(http.MethodGet, "/x", nil).WithContext(ctx), http.StatusInternalServerError, DBError, "context canceled")
	if w.Code != StatusClientClosedRequest || w.Body.Len() != 0 {
		t.Fatalf("status = %d body = %q", w.Code, w.Body.String())
	}
}
