package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/reqctx"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/asyncop"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
	deliveryrollout "github.com/alphabravocompany/astronomer-go/internal/delivery/rollout"
	"github.com/alphabravocompany/astronomer-go/internal/events"
)

type rolloutControllerFake struct {
	actions   []deliveryrollout.ActionRequest
	approvals []deliveryrollout.ApprovalRequest
	act       func(deliveryrollout.ActionRequest) (deliveryrollout.ControlResult, error)
	approve   func(deliveryrollout.ApprovalRequest) (deliveryrollout.ControlResult, error)
}

type rolloutPlannerFake struct {
	requests []deliveryrollout.CreateRequest
	create   func(deliveryrollout.CreateRequest) (deliveryrollout.FrozenRollout, error)
}

func (f *rolloutPlannerFake) Create(_ context.Context, request deliveryrollout.CreateRequest) (deliveryrollout.FrozenRollout, error) {
	f.requests = append(f.requests, request)
	if f.create != nil {
		return f.create(request)
	}
	return deliveryrollout.FrozenRollout{
		ID: uuid.New(), TargetID: request.TargetID, ProjectID: uuid.New(),
		TargetGeneration: request.ExpectedTargetGeneration, Actor: request.Actor,
		IdempotencyKey: request.IdempotencyKey,
	}, nil
}

func (f *rolloutControllerFake) Act(_ context.Context, request deliveryrollout.ActionRequest) (deliveryrollout.ControlResult, error) {
	f.actions = append(f.actions, request)
	if f.act != nil {
		return f.act(request)
	}
	return rolloutControlResult(request.ProjectID, request.RolloutID, request.ExpectedFence+1, "rollout."+string(request.Action)), nil
}

func (f *rolloutControllerFake) Approve(_ context.Context, request deliveryrollout.ApprovalRequest) (deliveryrollout.ControlResult, error) {
	f.approvals = append(f.approvals, request)
	if f.approve != nil {
		return f.approve(request)
	}
	return rolloutControlResult(request.ProjectID, request.RolloutID, request.ExpectedFence+1, "rollout.approve"), nil
}

func rolloutControlResult(projectID, rolloutID uuid.UUID, fence int64, operation string) deliveryrollout.ControlResult {
	return deliveryrollout.ControlResult{
		Rollout: sqlc.DeliveryRollout{
			ID: rolloutID, TargetID: uuid.New(), State: "paused", FencingGeneration: fence,
		},
		AuditPersisted: true,
		Receipt: asyncop.Receipt{
			OperationID: uuid.New(), Operation: operation, Resource: "delivery_rollout",
			ResourceID: rolloutID, ProjectID: projectID, Status: "accepted",
			StatusURL: "/api/v1/delivery/rollouts/" + rolloutID.String(), AcceptedAt: time.Now().UTC(),
		},
	}
}

func rolloutControlRequest(t *testing.T, method, projectID, rolloutID, body string) *http.Request {
	t.Helper()
	request := httptest.NewRequest(method, "/api/v1/delivery/rollouts/"+rolloutID, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("If-Match", `"7"`)
	request.Header.Set("Idempotency-Key", "rollout-control-1")
	ctx := reqctx.WithUser(request.Context(), &reqctx.User{ID: uuid.NewString(), AuthMethod: "jwt"})
	request = request.WithContext(ctx)
	if projectID != "" {
		query := request.URL.Query()
		query.Set("project_id", projectID)
		request.URL.RawQuery = query.Encode()
	}
	return request
}

type rolloutControlCall func(*RolloutHandler, http.ResponseWriter, *http.Request)

func serveRolloutControl(t *testing.T, handler *RolloutHandler, call rolloutControlCall, request *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	router := chi.NewRouter()
	router.Method(request.Method, "/api/v1/delivery/rollouts/{id}", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call(handler, w, r)
	}))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func rolloutStartRequest(t *testing.T, projectID, targetID uuid.UUID, body startRolloutRequest) *http.Request {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/delivery/targets/"+targetID.String()+"/rollouts?project_id="+projectID.String(), strings.NewReader(string(payload)))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("If-Match", `"5"`)
	request.Header.Set("Idempotency-Key", "rollout-start-1")
	return request.WithContext(reqctx.WithUser(request.Context(), &reqctx.User{ID: uuid.NewString(), AuthMethod: "jwt"}))
}

func serveRolloutStart(t *testing.T, handler *RolloutHandler, request *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	router := chi.NewRouter()
	router.Post("/api/v1/delivery/targets/{id}/rollouts", handler.Start)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func validStartRolloutRequest() startRolloutRequest {
	return startRolloutRequest{
		PreviewDigest:      model.Digest(strings.Repeat("a", 64)),
		ConfirmAllClusters: true,
		Strategy: model.RolloutStrategy{
			Type: model.StrategyAllAtOnce, MaxConcurrent: 4,
			MaxUnavailable:   model.Amount{Type: model.AmountCount, Value: 1},
			ProgressDeadline: model.Duration(time.Hour),
			FailureThreshold: model.Amount{Type: model.AmountCount, Value: 1},
			OnFailure:        model.FailurePause,
		},
	}
}

func TestRolloutStartBindsScopeConcurrencyIdentityIdempotencyAndAudit(t *testing.T) {
	projectID := uuid.New()
	targetID := uuid.New()
	rolloutID := uuid.New()
	planner := &rolloutPlannerFake{create: func(request deliveryrollout.CreateRequest) (deliveryrollout.FrozenRollout, error) {
		return deliveryrollout.FrozenRollout{
			ID: rolloutID, TargetID: request.TargetID, ProjectID: projectID,
			TargetGeneration: request.ExpectedTargetGeneration, Actor: request.Actor,
			IdempotencyKey: request.IdempotencyKey,
		}, nil
	}}
	requestBody := validStartRolloutRequest()
	request := rolloutStartRequest(t, projectID, targetID, requestBody)
	response := serveRolloutStart(t, NewRolloutHandler(nil, planner, nil, nil), request)

	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if len(planner.requests) != 1 {
		t.Fatalf("planner calls = %d, want 1", len(planner.requests))
	}
	got := planner.requests[0]
	user, _ := reqctx.AuthenticatedUser(request.Context())
	if got.TargetID != targetID || got.ExpectedTargetGeneration != 5 || got.PreviewDigest != requestBody.PreviewDigest || !got.ConfirmAllClusters ||
		got.Strategy.Type != model.StrategyAllAtOnce || got.Actor != user.ID || got.IdempotencyKey != "rollout-start-1" {
		t.Fatalf("unexpected planner request: %+v", got)
	}
	assertRolloutAuditIntent(t, request, got.Audit, "delivery.rollout.created", targetID, projectID)
	if response.Header().Get("Location") != "/api/v1/delivery/rollouts/"+rolloutID.String()+"/?project_id="+projectID.String() || response.Header().Get("Retry-After") != "2" {
		t.Fatalf("unexpected response headers: %v", response.Header())
	}
}

func TestRolloutStartRejectsCrossProjectPlannerResult(t *testing.T) {
	projectID := uuid.New()
	targetID := uuid.New()
	planner := &rolloutPlannerFake{create: func(request deliveryrollout.CreateRequest) (deliveryrollout.FrozenRollout, error) {
		return deliveryrollout.FrozenRollout{ID: uuid.New(), TargetID: request.TargetID, ProjectID: uuid.New()}, nil
	}}
	request := rolloutStartRequest(t, projectID, targetID, validStartRolloutRequest())

	response := serveRolloutStart(t, NewRolloutHandler(nil, planner, nil, nil), request)

	assertAPIError(t, response, http.StatusNotFound, "not_found")
}

func TestRolloutStartRejectsInvalidMutationContractsBeforePlanner(t *testing.T) {
	projectID := uuid.New()
	targetID := uuid.New()
	tests := []struct {
		name       string
		mutate     func(*http.Request)
		wantStatus int
		wantCode   string
	}{
		{name: "missing idempotency key", mutate: func(r *http.Request) { r.Header.Del("Idempotency-Key") }, wantStatus: http.StatusBadRequest, wantCode: "invalid_idempotency_key"},
		{name: "missing target generation", mutate: func(r *http.Request) { r.Header.Del("If-Match") }, wantStatus: http.StatusPreconditionRequired, wantCode: "if_match_required"},
		{name: "zero target generation", mutate: func(r *http.Request) { r.Header.Set("If-Match", `"0"`) }, wantStatus: http.StatusPreconditionRequired, wantCode: "if_match_required"},
		{name: "invalid target id", mutate: func(r *http.Request) { r.URL.Path = "/api/v1/delivery/targets/not-a-uuid/rollouts" }, wantStatus: http.StatusBadRequest, wantCode: "invalid_resource_id"},
		{name: "missing project", mutate: func(r *http.Request) { r.URL.RawQuery = "" }, wantStatus: http.StatusBadRequest, wantCode: "invalid_project_scope"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			planner := &rolloutPlannerFake{}
			request := rolloutStartRequest(t, projectID, targetID, validStartRolloutRequest())
			test.mutate(request)

			response := serveRolloutStart(t, NewRolloutHandler(nil, planner, nil, nil), request)

			assertAPIError(t, response, test.wantStatus, test.wantCode)
			if len(planner.requests) != 0 {
				t.Fatalf("planner calls = %d, want 0", len(planner.requests))
			}
		})
	}
}

func assertAPIError(t *testing.T, response *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if response.Code != status {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, status, response.Body.String())
	}
	var envelope errorEnvelope
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode error response: %v; body = %s", err, response.Body.String())
	}
	if envelope.Error.Code != code {
		t.Fatalf("error code = %q, want %q; body = %s", envelope.Error.Code, code, response.Body.String())
	}
}

func assertRolloutAuditIntent(t *testing.T, request *http.Request, intent audit.Intent, action string, rolloutID, projectID uuid.UUID) {
	t.Helper()
	if intent.IsZero() {
		t.Fatal("controller received an empty transactional audit intent")
	}
	if intent.Event.Action != action || intent.Event.ResourceType != "delivery_rollout" || intent.Event.ResourceID != rolloutID.String() ||
		intent.Event.StatusCode != http.StatusAccepted || intent.Event.HTTPMethod != request.Method || intent.Event.Path != request.URL.Path {
		t.Fatalf("unexpected audit event: %+v", intent.Event)
	}
	if intent.Event.UserID != reqctx.UserUUID(request.Context()) || intent.Event.ActorAuthMethod != "jwt" {
		t.Fatalf("audit identity = %+v/%q, request identity = %+v", intent.Event.UserID, intent.Event.ActorAuthMethod, reqctx.UserUUID(request.Context()))
	}
	if got := intent.Event.Detail["project_id"]; got != projectID.String() {
		t.Fatalf("audit project_id = %v, want %s", got, projectID)
	}
}

func TestRolloutActionsForwardConcurrencyIdentityAndIdempotency(t *testing.T) {
	projectID := uuid.New()
	rolloutID := uuid.New()
	tests := []struct {
		name   string
		action deliveryrollout.Action
		call   rolloutControlCall
	}{
		{name: "pause", action: deliveryrollout.ActionPause, call: (*RolloutHandler).Pause},
		{name: "resume", action: deliveryrollout.ActionResume, call: (*RolloutHandler).Resume},
		{name: "abort", action: deliveryrollout.ActionAbort, call: (*RolloutHandler).Abort},
		{name: "retry", action: deliveryrollout.ActionRetry, call: (*RolloutHandler).Retry},
		{name: "rollback", action: deliveryrollout.ActionRollback, call: (*RolloutHandler).Rollback},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			controller := &rolloutControllerFake{}
			handler := NewRolloutHandler(nil, nil, controller, nil)
			request := rolloutControlRequest(t, http.MethodPost, projectID.String(), rolloutID.String(), `{"reason_code":"operator_requested"}`)
			response := serveRolloutControl(t, handler, test.call, request)

			if response.Code != http.StatusAccepted {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
			if len(controller.actions) != 1 {
				t.Fatalf("controller calls = %d, want 1", len(controller.actions))
			}
			got := controller.actions[0]
			if got.ProjectID != projectID || got.RolloutID != rolloutID || got.ExpectedFence != 7 || got.Action != test.action || got.ReasonCode != "operator_requested" || got.IdempotencyKey != "rollout-control-1" || got.ActorID != reqctx.UserUUID(request.Context()) {
				t.Fatalf("unexpected action request: %+v", got)
			}
			assertRolloutAuditIntent(t, request, got.Audit, rolloutAuditAction(test.action), rolloutID, projectID)
			if response.Header().Get("ETag") != `"8"` || response.Header().Get("Location") != "/api/v1/delivery/rollouts/"+rolloutID.String() {
				t.Fatalf("unexpected response headers: %v", response.Header())
			}
			if response.Header().Get("Retry-After") != "2" {
				t.Fatalf("Retry-After = %q, want 2", response.Header().Get("Retry-After"))
			}
		})
	}
}

func TestRolloutApproveForwardsBoundApproval(t *testing.T) {
	projectID := uuid.New()
	rolloutID := uuid.New()
	digest := model.Digest(strings.Repeat("a", 64))
	expiresAt := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	controller := &rolloutControllerFake{}
	handler := NewRolloutHandler(nil, nil, controller, nil)
	body := fmt.Sprintf(`{"cohort":2,"binding_digest":%q,"decision":"approved","expires_at":%q}`, digest, expiresAt.Format(time.RFC3339))
	request := rolloutControlRequest(t, http.MethodPost, projectID.String(), rolloutID.String(), body)
	response := serveRolloutControl(t, handler, (*RolloutHandler).Approve, request)

	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if len(controller.approvals) != 1 {
		t.Fatalf("controller calls = %d, want 1", len(controller.approvals))
	}
	got := controller.approvals[0]
	if got.ProjectID != projectID || got.RolloutID != rolloutID || got.ExpectedFence != 7 || got.Cohort != 2 || got.BindingDigest != digest || got.Decision != "approved" || !got.ExpiresAt.Equal(expiresAt) || got.IdempotencyKey != "rollout-control-1" || got.ActorID != reqctx.UserUUID(request.Context()) {
		t.Fatalf("unexpected approval request: %+v", got)
	}
	assertRolloutAuditIntent(t, request, got.Audit, "delivery.rollout.approval_recorded", rolloutID, projectID)
	for key, want := range map[string]any{"decision": "approved", "cohort": int32(2), "binding_digest": digest.String()} {
		if value := got.Audit.Event.Detail[key]; value != want {
			t.Fatalf("audit %s = %#v, want %#v", key, value, want)
		}
	}
}

func TestRolloutApproveRejectsInvalidContractsBeforeController(t *testing.T) {
	projectID := uuid.New()
	rolloutID := uuid.New()
	digest := strings.Repeat("a", 64)
	validBody := fmt.Sprintf(`{"cohort":0,"binding_digest":%q,"decision":"approved","expires_at":%q}`, digest, time.Now().UTC().Add(time.Hour).Format(time.RFC3339))
	tests := []struct {
		name       string
		body       string
		mutate     func(*http.Request)
		handler    func(*rolloutControllerFake) *RolloutHandler
		wantStatus int
		wantCode   string
	}{
		{name: "missing fence", body: validBody, mutate: func(r *http.Request) { r.Header.Del("If-Match") }, wantStatus: http.StatusPreconditionRequired, wantCode: "if_match_required"},
		{name: "unknown field", body: `{"unknown":true}`, wantStatus: http.StatusBadRequest, wantCode: "invalid_request"},
		{name: "conflicting project", body: fmt.Sprintf(`{"project_id":%q,"cohort":0,"binding_digest":%q,"decision":"approved","expires_at":%q}`, uuid.New(), digest, time.Now().UTC().Add(time.Hour).Format(time.RFC3339)), wantStatus: http.StatusBadRequest, wantCode: "invalid_project_scope"},
		{name: "invalid rollout id", body: validBody, mutate: func(r *http.Request) { r.URL.Path = "/api/v1/delivery/rollouts/not-a-uuid" }, wantStatus: http.StatusBadRequest, wantCode: "invalid_resource_id"},
		{name: "missing controller", body: validBody, handler: func(*rolloutControllerFake) *RolloutHandler { return NewRolloutHandler(nil, nil, nil, nil) }, wantStatus: http.StatusServiceUnavailable, wantCode: "service_unavailable"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			controller := &rolloutControllerFake{}
			handler := NewRolloutHandler(nil, nil, controller, nil)
			if test.handler != nil {
				handler = test.handler(controller)
			}
			request := rolloutControlRequest(t, http.MethodPost, projectID.String(), rolloutID.String(), test.body)
			if test.mutate != nil {
				test.mutate(request)
			}

			response := serveRolloutControl(t, handler, (*RolloutHandler).Approve, request)

			assertAPIError(t, response, test.wantStatus, test.wantCode)
			if len(controller.approvals) != 0 {
				t.Fatalf("controller calls = %d, want 0", len(controller.approvals))
			}
		})
	}
}

func TestRolloutControlsRequireIdempotencyKeyBeforeCallingController(t *testing.T) {
	projectID := uuid.New()
	rolloutID := uuid.New()
	tests := []struct {
		name string
		call rolloutControlCall
		body string
	}{
		{name: "pause", call: (*RolloutHandler).Pause, body: `{}`},
		{name: "resume", call: (*RolloutHandler).Resume, body: `{}`},
		{name: "abort", call: (*RolloutHandler).Abort, body: `{}`},
		{name: "retry", call: (*RolloutHandler).Retry, body: `{}`},
		{name: "rollback", call: (*RolloutHandler).Rollback, body: `{}`},
		{name: "approve", call: (*RolloutHandler).Approve, body: `{"cohort":1}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			controller := &rolloutControllerFake{}
			request := rolloutControlRequest(t, http.MethodPost, projectID.String(), rolloutID.String(), test.body)
			request.Header.Del("Idempotency-Key")
			response := serveRolloutControl(t, NewRolloutHandler(nil, nil, controller, nil), test.call, request)

			assertAPIError(t, response, http.StatusBadRequest, "invalid_idempotency_key")
			if len(controller.actions) != 0 || len(controller.approvals) != 0 {
				t.Fatal("controller was called without an idempotency key")
			}
		})
	}
}

func TestRolloutControlsRejectInvalidTransportContractsBeforeController(t *testing.T) {
	projectID := uuid.New()
	rolloutID := uuid.New()
	tests := []struct {
		name       string
		mutate     func(*http.Request)
		wantStatus int
		wantCode   string
	}{
		{
			name: "duplicate idempotency key", wantStatus: http.StatusBadRequest, wantCode: "invalid_idempotency_key",
			mutate: func(r *http.Request) { r.Header.Add("Idempotency-Key", "second-key") },
		},
		{
			name: "control character in idempotency key", wantStatus: http.StatusBadRequest, wantCode: "invalid_idempotency_key",
			mutate: func(r *http.Request) { r.Header.Set("Idempotency-Key", "bad\nkey") },
		},
		{
			name: "oversized idempotency key", wantStatus: http.StatusBadRequest, wantCode: "invalid_idempotency_key",
			mutate: func(r *http.Request) { r.Header.Set("Idempotency-Key", strings.Repeat("x", 129)) },
		},
		{
			name: "missing If-Match", wantStatus: http.StatusPreconditionRequired, wantCode: "if_match_required",
			mutate: func(r *http.Request) { r.Header.Del("If-Match") },
		},
		{
			name: "weak If-Match", wantStatus: http.StatusPreconditionRequired, wantCode: "if_match_required",
			mutate: func(r *http.Request) { r.Header.Set("If-Match", `W/"7"`) },
		},
		{
			name: "zero If-Match", wantStatus: http.StatusPreconditionRequired, wantCode: "if_match_required",
			mutate: func(r *http.Request) { r.Header.Set("If-Match", `"0"`) },
		},
		{
			name: "unknown request field", wantStatus: http.StatusBadRequest, wantCode: "invalid_request",
			mutate: func(r *http.Request) { r.Body = io.NopCloser(strings.NewReader(`{"unsupported":true}`)) },
		},
		{
			name: "trailing JSON object", wantStatus: http.StatusBadRequest, wantCode: "invalid_request",
			mutate: func(r *http.Request) { r.Body = io.NopCloser(strings.NewReader(`{} {}`)) },
		},
		{
			name: "conflicting project scope", wantStatus: http.StatusBadRequest, wantCode: "invalid_project_scope",
			mutate: func(r *http.Request) {
				r.Body = io.NopCloser(strings.NewReader(fmt.Sprintf(`{"project_id":%q}`, uuid.New())))
			},
		},
		{
			name: "invalid rollout id", wantStatus: http.StatusBadRequest, wantCode: "invalid_resource_id",
			mutate: func(r *http.Request) {
				r.URL.Path = "/api/v1/delivery/rollouts/not-a-uuid"
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			controller := &rolloutControllerFake{}
			request := rolloutControlRequest(t, http.MethodPost, projectID.String(), rolloutID.String(), `{}`)
			test.mutate(request)
			response := serveRolloutControl(t, NewRolloutHandler(nil, nil, controller, nil), (*RolloutHandler).Pause, request)

			assertAPIError(t, response, test.wantStatus, test.wantCode)
			if len(controller.actions) != 0 {
				t.Fatalf("controller calls = %d, want 0", len(controller.actions))
			}
		})
	}
}

func TestRolloutControlsFailClosedWhenControllerIsUnavailable(t *testing.T) {
	request := rolloutControlRequest(t, http.MethodPost, uuid.NewString(), uuid.NewString(), `{}`)
	response := serveRolloutControl(t, NewRolloutHandler(nil, nil, nil, nil), (*RolloutHandler).Pause, request)

	assertAPIError(t, response, http.StatusServiceUnavailable, "service_unavailable")
}

func TestRolloutControlErrorsHaveStableHTTPContracts(t *testing.T) {
	projectID := uuid.New()
	rolloutID := uuid.New()
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{name: "idempotency claim conflict", err: asyncop.ErrConflict, wantStatus: http.StatusConflict, wantCode: "idempotency_conflict"},
		{name: "typed idempotency conflict", err: &deliveryrollout.Error{Code: deliveryrollout.CodeIdempotencyConflict, Cause: errors.New("reused key")}, wantStatus: http.StatusConflict, wantCode: "idempotency_conflict"},
		{name: "stale preview", err: &deliveryrollout.Error{Code: deliveryrollout.CodePreviewStale, Cause: errors.New("stale")}, wantStatus: http.StatusConflict, wantCode: "preview_stale"},
		{name: "target changed", err: &deliveryrollout.Error{Code: deliveryrollout.CodeTargetChanged, Cause: errors.New("changed")}, wantStatus: http.StatusConflict, wantCode: "target_changed"},
		{name: "stale fence", err: &deliveryrollout.Error{Code: deliveryrollout.CodeStaleFence, Cause: errors.New("changed")}, wantStatus: http.StatusPreconditionFailed, wantCode: "stale_fence"},
		{name: "invalid action", err: &deliveryrollout.Error{Code: deliveryrollout.CodeInvalidInput, Cause: errors.New("bad action")}, wantStatus: http.StatusBadRequest, wantCode: "invalid_input"},
		{name: "empty placement", err: &deliveryrollout.Error{Code: deliveryrollout.CodeNoClusters, Cause: errors.New("empty")}, wantStatus: http.StatusBadRequest, wantCode: "no_clusters"},
		{name: "invalid cohorts", err: &deliveryrollout.Error{Code: deliveryrollout.CodeInvalidCohorts, Cause: errors.New("bad cohorts")}, wantStatus: http.StatusBadRequest, wantCode: "invalid_cohorts"},
		{name: "audit unavailable", err: audit.ErrOutboxUnavailable, wantStatus: http.StatusServiceUnavailable, wantCode: "audit_unavailable"},
		{name: "missing rollout", err: pgx.ErrNoRows, wantStatus: http.StatusNotFound, wantCode: "not_found"},
		{name: "unexpected persistence failure", err: errors.New("database contained a secret diagnostic"), wantStatus: http.StatusInternalServerError, wantCode: "internal_error"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			controller := &rolloutControllerFake{act: func(deliveryrollout.ActionRequest) (deliveryrollout.ControlResult, error) {
				return deliveryrollout.ControlResult{}, test.err
			}}
			request := rolloutControlRequest(t, http.MethodPost, projectID.String(), rolloutID.String(), `{}`)
			response := serveRolloutControl(t, NewRolloutHandler(nil, nil, controller, nil), (*RolloutHandler).Pause, request)

			assertAPIError(t, response, test.wantStatus, test.wantCode)
			if len(controller.actions) != 1 {
				t.Fatalf("controller calls = %d, want 1", len(controller.actions))
			}
			if test.wantCode == "internal_error" && strings.Contains(response.Body.String(), "secret diagnostic") {
				t.Fatalf("internal diagnostic leaked in response: %s", response.Body.String())
			}
		})
	}
}

func TestRolloutIdempotencyReplayDoesNotRepublishOrInventAnETag(t *testing.T) {
	projectID := uuid.New()
	rolloutID := uuid.New()
	result := rolloutControlResult(projectID, rolloutID, 8, "rollout.pause")
	result.Replayed = true
	controller := &rolloutControllerFake{act: func(deliveryrollout.ActionRequest) (deliveryrollout.ControlResult, error) {
		return result, nil
	}}
	bus := events.NewBus()
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	eventStream := bus.Subscribe(ctx, events.AcceptAll)
	request := rolloutControlRequest(t, http.MethodPost, projectID.String(), rolloutID.String(), `{}`)
	response := serveRolloutControl(t, NewRolloutHandler(nil, nil, controller, bus), (*RolloutHandler).Pause, request)

	if response.Code != http.StatusAccepted || response.Header().Get("Location") != result.Receipt.StatusURL {
		t.Fatalf("status = %d, headers = %v, body = %s", response.Code, response.Header(), response.Body.String())
	}
	if etag := response.Header().Get("ETag"); etag != "" {
		t.Fatalf("replay returned invented ETag %q", etag)
	}
	select {
	case event := <-eventStream:
		t.Fatalf("idempotency replay republished event: %+v", event)
	default:
	}
}

func TestRolloutAcceptedActionPublishesOneMetadataOnlyChangeEvent(t *testing.T) {
	projectID := uuid.New()
	rolloutID := uuid.New()
	controller := &rolloutControllerFake{}
	bus := events.NewBus()
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	eventStream := bus.Subscribe(ctx, events.AcceptAll)
	request := rolloutControlRequest(t, http.MethodPost, projectID.String(), rolloutID.String(), `{}`)

	response := serveRolloutControl(t, NewRolloutHandler(nil, nil, controller, bus), (*RolloutHandler).Pause, request)

	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	select {
	case event := <-eventStream:
		if event.Type != events.Type("delivery_rollout.changed") {
			t.Fatalf("event type = %q", event.Type)
		}
		payload, ok := event.Data.(map[string]any)
		if !ok || payload["id"] != rolloutID.String() || payload["project_id"] != projectID.String() || payload["action"] != "pause" || len(payload) != 3 {
			t.Fatalf("unexpected event payload: %#v", event.Data)
		}
	default:
		t.Fatal("accepted rollout action did not publish a change event")
	}
	select {
	case event := <-eventStream:
		t.Fatalf("accepted rollout action published duplicate event: %+v", event)
	default:
	}
}
