//go:build integration

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/api/apiutil"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

const errorContractSecret = "password=hunter2 host=internal-db:5432"

// decodeErrorBody asserts the one error body shape: a JSON object with exactly
// {"error": {...}} whose object has only the documented fields.
func decodeErrorBody(t *testing.T, rec *httptest.ResponseRecorder) apiutil.BodyError {
	t.Helper()
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json (body %s)", ct, rec.Body.String())
	}
	dec := json.NewDecoder(bytes.NewReader(rec.Body.Bytes()))
	dec.DisallowUnknownFields()
	var body apiutil.Body
	if err := dec.Decode(&body); err != nil {
		t.Fatalf("error body does not match the contract: %v: %s", err, rec.Body.String())
	}
	if body.Error.Code == "" || body.Error.Message == "" {
		t.Fatalf("error body lacks code/message: %s", rec.Body.String())
	}
	return body.Error
}

// TestErrorContractEveryCode drives every catalog code through the one writer
// behind the server's RequestID middleware and checks status, content type,
// body shape, request-id echo, 5xx scrubbing and the connect.Code mapping.
func TestErrorContractEveryCode(t *testing.T) {
	t.Parallel()
	codes := apiutil.Codes()
	if len(codes) < 250 {
		t.Fatalf("catalog has %d codes; expected the full catalog", len(codes))
	}
	for _, code := range codes {
		status := apiutil.HTTPStatus(code)
		handler := middleware.RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			apiutil.WriteError(w, r, status, code, "detail: "+errorContractSecret)
		}))
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/contract", nil)
		req.Header.Set("X-Request-ID", "contract-"+string(code))
		handler.ServeHTTP(rec, req)

		if rec.Code != status {
			t.Errorf("%s: status %d, want %d", code, rec.Code, status)
			continue
		}
		body := decodeErrorBody(t, rec)
		if body.Code != code {
			t.Errorf("%s: body code %q", code, body.Code)
		}
		if body.RequestID != "contract-"+string(code) {
			t.Errorf("%s: request_id %q not echoed", code, body.RequestID)
		}
		leaked := strings.Contains(rec.Body.String(), "hunter2")
		if status >= http.StatusInternalServerError && leaked {
			t.Errorf("%s: %d response echoes the internal detail: %s", code, status, rec.Body.String())
		}
		if status < http.StatusInternalServerError && body.Message != "detail: "+errorContractSecret {
			t.Errorf("%s: client error message rewritten to %q", code, body.Message)
		}
		connectCode := apiutil.ConnectCode(code)
		if got := apiutil.ConnectError(apiutil.New(code, "x")).Code(); got != connectCode {
			t.Errorf("%s: ConnectError code %s, catalog says %s", code, got, connectCode)
		}
		if connectCode == connect.CodeInternal && status < http.StatusInternalServerError {
			t.Errorf("%s: Internal connect code on a %d", code, status)
		}
	}

	// Internal: an unmapped error never reaches the body, over HTTP or Connect.
	rec := httptest.NewRecorder()
	apiutil.Respond(rec, httptest.NewRequest(http.MethodGet, "/api/v1/contract", nil), errors.New(errorContractSecret))
	body := decodeErrorBody(t, rec)
	if rec.Code != http.StatusInternalServerError || body.Code != apiutil.InternalError || strings.Contains(rec.Body.String(), "hunter2") || body.RequestID == "" {
		t.Fatalf("internal error response = %d %s", rec.Code, rec.Body.String())
	}
	if ce := apiutil.ConnectError(errors.New(errorContractSecret)); ce.Code() != connect.CodeInternal || strings.Contains(ce.Error(), "hunter2") {
		t.Fatalf("connect internal error = %v", ce)
	}
}

// TestErrorContractDatabaseOriginatedCodes produces the DB-originated codes
// through the real production router against real Postgres: a unique
// violation, a missing row and a foreign-key violation, each translated by
// apiutil.FromError from the pgx/pgconn error the query returned.
func TestErrorContractDatabaseOriginatedCodes(t *testing.T) {
	t.Parallel()
	h := newProductionRouterHarness(t)
	_, adminToken := h.user(t, "errors-admin", true, nil)
	ctx := context.Background()
	cluster, project := uuid.New(), uuid.New()
	if _, err := h.pool.Exec(ctx, `INSERT INTO clusters (id,name,display_name) VALUES ($1,$2,$2)`, cluster, "errors-"+cluster.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := h.pool.Exec(ctx, `INSERT INTO projects (id,name,display_name,cluster_id) VALUES ($1,$2,$2,$3)`, project, "errors-"+project.String(), cluster); err != nil {
		t.Fatal(err)
	}
	send := func(method, path string, projectID uuid.UUID, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Project-ID", projectID.String())
		req.Header.Set("Idempotency-Key", uuid.NewString())
		return h.do(t, req, adminToken)
	}
	template := `{"name":"duplicate-me","renderer":"helm","values":{}}`

	if rec := send(http.MethodPost, "/api/v1/delivery/configuration-templates/", project, template); rec.Code != http.StatusCreated {
		t.Fatalf("first create = %d %s", rec.Code, rec.Body.String())
	}
	cases := []struct {
		name    string
		rec     *httptest.ResponseRecorder
		status  int
		code    apiutil.Code
		connect connect.Code
	}{
		{"unique violation", send(http.MethodPost, "/api/v1/delivery/configuration-templates/", project, template),
			http.StatusConflict, apiutil.AlreadyExists, connect.CodeAlreadyExists},
		{"no rows", send(http.MethodGet, "/api/v1/delivery/configuration-templates/"+uuid.NewString()+"/", project, ""),
			http.StatusNotFound, apiutil.NotFound, connect.CodeNotFound},
		{"foreign key on insert", send(http.MethodPost, "/api/v1/delivery/configuration-templates/", uuid.New(), template),
			http.StatusBadRequest, apiutil.InvalidReference, connect.CodeInvalidArgument},
	}
	for _, tc := range cases {
		if tc.rec.Code != tc.status {
			t.Errorf("%s: status %d, want %d (%s)", tc.name, tc.rec.Code, tc.status, tc.rec.Body.String())
			continue
		}
		body := decodeErrorBody(t, tc.rec)
		if body.Code != tc.code || apiutil.ConnectCode(body.Code) != tc.connect {
			t.Errorf("%s: code %q (connect %s), want %q (%s)", tc.name, body.Code, apiutil.ConnectCode(body.Code), tc.code, tc.connect)
		}
		if strings.Contains(tc.rec.Body.String(), "SQLSTATE") || strings.Contains(tc.rec.Body.String(), "delivery_configuration_templates") {
			t.Errorf("%s: database detail leaked: %s", tc.name, tc.rec.Body.String())
		}
	}
}
