package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/charlie"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type charlieAdminFake struct {
	CharlieAdminBackend
	status         charlie.AdminStatusView
	uninstallCalls int
	uninstallActor uuid.UUID
	mode           charlie.AdminModeView
	modeInput      charlie.Mode
	emergency      bool
	triggerEvents  []charlie.AdminTriggerEventView
	retryEvent     charlie.AdminTriggerEventView
	retrySource    uuid.UUID
	retryRequest   uuid.UUID
}

func (f *charlieAdminFake) ListTriggerEvents(context.Context, string, int32, int32) ([]charlie.AdminTriggerEventView, error) {
	return f.triggerEvents, nil
}
func (f *charlieAdminFake) RetryTriggerEvent(_ context.Context, source, request uuid.UUID) (charlie.AdminTriggerEventView, error) {
	f.retrySource, f.retryRequest = source, request
	return f.retryEvent, nil
}

func (f *charlieAdminFake) Status(context.Context) (charlie.AdminStatusView, error) {
	return f.status, nil
}
func (f *charlieAdminFake) Uninstall(_ context.Context, actor uuid.UUID) error {
	f.uninstallCalls++
	f.uninstallActor = actor
	return nil
}
func (f *charlieAdminFake) UpdateMode(_ context.Context, mode charlie.Mode, _ int64, emergency bool, _ uuid.UUID) (charlie.AdminModeView, error) {
	f.modeInput, f.emergency = mode, emergency
	return f.mode, nil
}

func TestCharlieAdminStatusReturnsOnlySafeMetadata(t *testing.T) {
	fake := &charlieAdminFake{status: charlie.AdminStatusView{Connection: charlie.AdminConnectionView{
		Connected: true, ProductID: "astronomer", DeploymentID: "deployment-a", RouteID: "route-a",
		SigningFingerprint: strings.Repeat("a", 64), PackageDigest: strings.Repeat("b", 64),
	}}}
	handler := NewCharlieAdminHandler(fake, nil)
	recorder := httptest.NewRecorder()
	handler.Status(recorder, authenticatedCharlieRequest(http.MethodGet, "/", "", uuid.New(), "jwt"))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "deployment-a") {
		t.Fatalf("status response = %d %s", recorder.Code, recorder.Body.String())
	}
	for _, prohibited := range []string{"private_key", "enrollment", "artifact_pull", "central_url", "local_trust"} {
		if strings.Contains(strings.ToLower(recorder.Body.String()), prohibited) {
			t.Fatalf("status leaked prohibited field %q: %s", prohibited, recorder.Body.String())
		}
	}
}

func TestCharlieAdminDestructiveActionsRequireBrowserAndExactConfirmation(t *testing.T) {
	fake := &charlieAdminFake{}
	handler := NewCharlieAdminHandler(fake, nil)

	wrong := httptest.NewRecorder()
	handler.Uninstall(wrong, authenticatedCharlieRequest(http.MethodPost, "/", `{"confirmation":"uninstall"}`, uuid.New(), "jwt"))
	if wrong.Code != http.StatusBadRequest || fake.uninstallCalls != 0 {
		t.Fatalf("wrong confirmation = %d calls=%d", wrong.Code, fake.uninstallCalls)
	}

	apiToken := httptest.NewRecorder()
	handler.Uninstall(apiToken, authenticatedCharlieRequest(http.MethodPost, "/", `{"confirmation":"UNINSTALL CHARLIE"}`, uuid.New(), "api_token"))
	if apiToken.Code != http.StatusUnauthorized || fake.uninstallCalls != 0 {
		t.Fatalf("API token destructive action = %d calls=%d", apiToken.Code, fake.uninstallCalls)
	}

	ok := httptest.NewRecorder()
	actorID := uuid.New()
	handler.Uninstall(ok, authenticatedCharlieRequest(http.MethodPost, "/", `{"confirmation":"UNINSTALL CHARLIE"}`, actorID, "jwt"))
	if ok.Code != http.StatusNoContent || fake.uninstallCalls != 1 || fake.uninstallActor != actorID {
		t.Fatalf("confirmed uninstall = %d calls=%d body=%s", ok.Code, fake.uninstallCalls, ok.Body.String())
	}
}

func TestCharlieAdminEmergencyDisableIsExplicit(t *testing.T) {
	fake := &charlieAdminFake{mode: charlie.AdminModeView{Requested: charlie.ModeDisabled, Authoritative: charlie.ModeDisabled, EmergencyDisabled: true}}
	handler := NewCharlieAdminHandler(fake, nil)
	recorder := httptest.NewRecorder()
	handler.Mode(recorder, authenticatedCharlieRequest(http.MethodPatch, "/", `{"mode":"disabled","revision":7,"emergency_disable":true}`, uuid.New(), "jwt"))
	if recorder.Code != http.StatusOK || fake.modeInput != charlie.ModeDisabled || !fake.emergency {
		t.Fatalf("emergency mode = %d input=%q emergency=%v body=%s", recorder.Code, fake.modeInput, fake.emergency, recorder.Body.String())
	}
}

func TestCharlieAdminEmergencyDisableRemainsAvailableWhenFeatureIsOff(t *testing.T) {
	fake := &charlieAdminFake{mode: charlie.AdminModeView{Requested: charlie.ModeDisabled, Authoritative: charlie.ModeDisabled, EmergencyDisabled: true}}
	handler := NewCharlieAdminHandler(fake, nil)
	handler.SetSettingsCache(NewSettingsCache(nil, time.Minute))
	emergency := httptest.NewRecorder()
	handler.Mode(emergency, authenticatedCharlieRequest(http.MethodPatch, "/", `{"mode":"disabled","revision":7,"emergency_disable":true}`, uuid.New(), "jwt"))
	if emergency.Code != http.StatusOK || !fake.emergency {
		t.Fatalf("disabled feature blocked emergency control: %d %s", emergency.Code, emergency.Body.String())
	}
	fake.emergency = false
	normal := httptest.NewRecorder()
	handler.Mode(normal, authenticatedCharlieRequest(http.MethodPatch, "/", `{"mode":"read_only","revision":8}`, uuid.New(), "jwt"))
	if normal.Code != http.StatusConflict || fake.modeInput != charlie.ModeDisabled || fake.emergency {
		// modeInput/emergency must remain from the accepted emergency request.
		t.Fatalf("disabled feature admitted normal mode change: %d input=%s emergency=%t", normal.Code, fake.modeInput, fake.emergency)
	}
}

func TestCharlieAdminRejectsUnauthenticatedStatus(t *testing.T) {
	handler := NewCharlieAdminHandler(&charlieAdminFake{}, nil)
	recorder := httptest.NewRecorder()
	handler.Status(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d", recorder.Code)
	}
}

func TestCharlieAdminDeadLetterListAndRetryAreBrowserOnlyAndBounded(t *testing.T) {
	sourceID, requestID, retryID := uuid.New(), uuid.New(), uuid.New()
	fake := &charlieAdminFake{
		triggerEvents: []charlie.AdminTriggerEventView{{ID: sourceID.String(), State: "dead", EventType: "queue_terminal_failure", LastErrorCode: "bridge_unavailable"}},
		retryEvent:    charlie.AdminTriggerEventView{ID: retryID.String(), RetryOfEventID: sourceID.String(), State: "pending", EventType: "queue_terminal_failure"},
	}
	handler := NewCharlieAdminHandler(fake, nil)

	list := httptest.NewRecorder()
	handler.ListTriggerEvents(list, authenticatedCharlieRequest(http.MethodGet, "/?state=dead&limit=20", "", uuid.New(), "jwt"))
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), sourceID.String()) || strings.Contains(list.Body.String(), "summary_metadata") {
		t.Fatalf("dead-letter list = %d %s", list.Code, list.Body.String())
	}

	retryRequest := authenticatedCharlieRequest(http.MethodPost, "/", `{"request_id":"`+requestID.String()+`"}`, uuid.New(), "jwt")
	routeContext := chi.NewRouteContext()
	routeContext.URLParams.Add("event_id", sourceID.String())
	retryRequest = retryRequest.WithContext(context.WithValue(retryRequest.Context(), chi.RouteCtxKey, routeContext))
	retry := httptest.NewRecorder()
	handler.RetryTriggerEvent(retry, retryRequest)
	if retry.Code != http.StatusAccepted || fake.retrySource != sourceID || fake.retryRequest != requestID || !strings.Contains(retry.Body.String(), retryID.String()) {
		t.Fatalf("dead-letter retry = %d source=%s request=%s body=%s", retry.Code, fake.retrySource, fake.retryRequest, retry.Body.String())
	}

	apiToken := httptest.NewRecorder()
	handler.RetryTriggerEvent(apiToken, authenticatedCharlieRequest(http.MethodPost, "/", `{"request_id":"`+uuid.NewString()+`"}`, uuid.New(), "api_token"))
	if apiToken.Code != http.StatusUnauthorized {
		t.Fatalf("API token retried dead-letter work: %d", apiToken.Code)
	}
}
