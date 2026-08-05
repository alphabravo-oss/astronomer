package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/charlie"
	charliecontract "github.com/alphabravocompany/astronomer-go/internal/charlie/contract"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	appmiddleware "github.com/alphabravocompany/astronomer-go/internal/server/middleware"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type charlieCreatorFake struct {
	input  charlie.CreateSessionInput
	result charlie.CreatedSession
	err    error
}

func (f *charlieCreatorFake) Create(_ context.Context, input charlie.CreateSessionInput) (charlie.CreatedSession, error) {
	f.input = input
	return f.result, f.err
}

type charlieAccessFake struct {
	actor, session, request uuid.UUID
	message                 uuid.UUID
	messageBody             string
	result                  json.RawMessage
}

func (f *charlieAccessFake) ListPrivate(context.Context, uuid.UUID, int32, int32) ([]sqlc.CharlieSession, error) {
	return nil, nil
}
func (f *charlieAccessFake) Get(context.Context, uuid.UUID, uuid.UUID) (charlie.SessionView, error) {
	return charlie.SessionView{}, nil
}
func (f *charlieAccessFake) History(context.Context, uuid.UUID, uuid.UUID, string, int) (json.RawMessage, error) {
	return f.result, nil
}
func (f *charlieAccessFake) Message(_ context.Context, actor, session, message uuid.UUID, body string) (json.RawMessage, error) {
	f.actor, f.session, f.message, f.messageBody = actor, session, message, body
	return f.result, nil
}
func (f *charlieAccessFake) Abort(_ context.Context, actor, session, request uuid.UUID) error {
	f.actor, f.session, f.request = actor, session, request
	return nil
}
func (f *charlieAccessFake) Stream(_ context.Context, actor, session uuid.UUID, _ string, handle func(charliecontract.Event) error) error {
	f.actor, f.session = actor, session
	return handle(charliecontract.Event{ID: "event-1", Event: "message.delta", Data: []byte(`{"text":"safe"}`)})
}

func authenticatedCharlieRequest(method, target, body string, actor uuid.UUID, authMethod string) *http.Request {
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	ctx := appmiddleware.SetAuthenticatedUserForTest(r.Context(), &appmiddleware.AuthenticatedUser{ID: actor.String(), Email: "operator@example.test", AuthMethod: authMethod})
	return r.WithContext(ctx)
}

func withSessionParam(r *http.Request, sessionID uuid.UUID) *http.Request {
	routeContext := chi.NewRouteContext()
	routeContext.URLParams.Add("session_id", sessionID.String())
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, routeContext))
}

func TestCharlieSessionCreateReturnsOnlySafeMetadata(t *testing.T) {
	actor, clientID, localID := uuid.New(), uuid.New(), uuid.New()
	creator := &charlieCreatorFake{result: charlie.CreatedSession{
		Local:            sqlc.CharlieSession{ID: localID, ClientSessionID: clientID, Intent: "troubleshoot", State: "active", Visibility: "private", CentralRevision: 3, CharlieSessionID: "central-secret-ish"},
		AuthorizationRef: "astro_charlie_auth_plaintext",
	}}
	handler := NewCharlieSessionHandler(creator, &charlieAccessFake{})
	request := authenticatedCharlieRequest(http.MethodPost, "/api/v1/charlie/sessions/", `{"client_session_id":"`+clientID.String()+`","intent":"troubleshoot"}`, actor, "jwt")
	recorder := httptest.NewRecorder()
	handler.Create(recorder, request)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "central-secret-ish") || strings.Contains(recorder.Body.String(), "astro_charlie_auth") {
		t.Fatalf("response exposed central or delegation data: %s", recorder.Body.String())
	}
	if creator.input.OwnerID != actor || creator.input.ClientSessionID != clientID || creator.input.ActorType != "user" {
		t.Fatalf("unexpected creator input: %#v", creator.input)
	}
}

func TestCharlieSessionCreateRejectsUnknownFieldsAndAPITokens(t *testing.T) {
	actor := uuid.New()
	for name, tc := range map[string]struct {
		body, method string
		want         int
	}{
		"unknown field": {`{"client_session_id":"` + uuid.NewString() + `","intent":"x","api_key":"leak"}`, "jwt", http.StatusBadRequest},
		"api token":     {`{"client_session_id":"` + uuid.NewString() + `","intent":"x"}`, "api_token", http.StatusUnauthorized},
	} {
		t.Run(name, func(t *testing.T) {
			creator := &charlieCreatorFake{}
			handler := NewCharlieSessionHandler(creator, &charlieAccessFake{})
			request := authenticatedCharlieRequest(http.MethodPost, "/", tc.body, actor, tc.method)
			recorder := httptest.NewRecorder()
			handler.Create(recorder, request)
			if recorder.Code != tc.want || creator.input.ClientSessionID != uuid.Nil {
				t.Fatalf("status=%d input=%#v", recorder.Code, creator.input)
			}
		})
	}
}

func TestCharlieSessionCreateReportsInvalidObjectiveAsValidationError(t *testing.T) {
	creator := &charlieCreatorFake{err: charlie.ErrInvalidSessionRequest}
	handler := NewCharlieSessionHandler(creator, &charlieAccessFake{})
	request := authenticatedCharlieRequest(http.MethodPost, "/", `{"client_session_id":"`+uuid.NewString()+`","intent":"invalid"}`, uuid.New(), "jwt")
	recorder := httptest.NewRecorder()
	handler.Create(recorder, request)

	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), `"code":"validation_error"`) {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestCharlieMessageForwardsStableIDsAndContentOnlyToBridgeService(t *testing.T) {
	actor, sessionID, messageID := uuid.New(), uuid.New(), uuid.New()
	access := &charlieAccessFake{result: json.RawMessage(`{"turn_id":"turn-1","replayed":true}`)}
	handler := NewCharlieSessionHandler(&charlieCreatorFake{}, access)
	request := authenticatedCharlieRequest(http.MethodPost, "/", `{"client_message_id":"`+messageID.String()+`","message":"check readiness"}`, actor, "jwt")
	request = withSessionParam(request, sessionID)
	recorder := httptest.NewRecorder()
	handler.Message(recorder, request)

	if recorder.Code != http.StatusAccepted || access.actor != actor || access.session != sessionID || access.message != messageID || access.messageBody != "check readiness" {
		t.Fatalf("message was not proxied exactly: status=%d access=%#v", recorder.Code, access)
	}
	if recorder.Body.String() != string(access.result) {
		t.Fatalf("turn receipt changed: %s", recorder.Body.String())
	}
}

func TestCharlieSessionEventsWritesResumableSSE(t *testing.T) {
	actor, sessionID := uuid.New(), uuid.New()
	handler := NewCharlieSessionHandler(&charlieCreatorFake{}, &charlieAccessFake{})
	request := authenticatedCharlieRequest(http.MethodGet, "/", "", actor, "jwt")
	request.Header.Del("Content-Type")
	request.Header.Set("Last-Event-ID", "event-0")
	request = withSessionParam(request, sessionID)
	recorder := httptest.NewRecorder()
	handler.Events(recorder, request)

	if recorder.Code != http.StatusOK || recorder.Header().Get("Content-Type") != "text/event-stream" {
		t.Fatalf("status=%d content-type=%q", recorder.Code, recorder.Header().Get("Content-Type"))
	}
	body := recorder.Body.String()
	for _, expected := range []string{"id: event-1", "event: message.delta", `data: {"text":"safe"}`} {
		if !strings.Contains(body, expected) {
			t.Fatalf("missing %q in %q", expected, body)
		}
	}
}

func TestCharlieSessionEventsRejectsCursorInjection(t *testing.T) {
	request := authenticatedCharlieRequest(http.MethodGet, "/", "", uuid.New(), "jwt")
	request.Header.Set("Last-Event-ID", "event-1\nevent: forged")
	request = withSessionParam(request, uuid.New())
	recorder := httptest.NewRecorder()
	NewCharlieSessionHandler(&charlieCreatorFake{}, &charlieAccessFake{}).Events(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
