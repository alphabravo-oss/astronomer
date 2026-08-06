package handler

import (
	"context"
	"errors"
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
	status               charlie.AdminStatusView
	localStatus          charlie.AdminStatusView
	statusCalls          int
	localCalls           int
	diagnosticCalls      int
	localDiagnosticCalls int
	uninstallCalls       int
	uninstallActor       uuid.UUID
	mode                 charlie.AdminModeView
	modeInput            charlie.Mode
	modeCalls            int
	emergency            bool
	triggerEvents        []charlie.AdminTriggerEventView
	retryEvent           charlie.AdminTriggerEventView
	retrySource          uuid.UUID
	retryRequest         uuid.UUID
	actionPolicy         charlie.AdminActionPolicy
	policyName           string
	policyInput          charlie.AdminActionPolicyInput
	alertPolicy          charlie.AdminAlertPolicyView
	alertPolicyErr       error
	alertPolicyInput     charlie.AdminAlertPolicyInput
	alertPolicyActor     uuid.UUID
}

func (f *charlieAdminFake) AlertPolicy(context.Context) (charlie.AdminAlertPolicyView, error) {
	return f.alertPolicy, nil
}
func (f *charlieAdminFake) UpdateAlertPolicy(_ context.Context, input charlie.AdminAlertPolicyInput, actor uuid.UUID) (charlie.AdminAlertPolicyView, error) {
	f.alertPolicyInput, f.alertPolicyActor = input, actor
	return f.alertPolicy, f.alertPolicyErr
}

func TestCharlieAdminAlertPolicyRejectsStaleRevision(t *testing.T) {
	fake := &charlieAdminFake{alertPolicyErr: charlie.ErrAdminConflict}
	h := NewCharlieAdminHandler(fake, &charlieAuditWriterFake{})
	body := `{"revision":3,"enabled":true,"minimum_severity":"high","dedupe_window_seconds":900,"escalation_after_seconds":0,"quiet_hours_enabled":false,"quiet_hours_start":"22:00","quiet_hours_end":"07:00","quiet_hours_timezone":"UTC","channel_ids":[]}`
	recorder := httptest.NewRecorder()
	h.UpdateAlertPolicy(recorder, authenticatedCharlieRequest(http.MethodPut, "/", body, uuid.New(), "jwt"))
	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), "conflict") {
		t.Fatalf("stale alert policy response=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func (f *charlieAdminFake) UpdateActionPolicy(_ context.Context, name string, input charlie.AdminActionPolicyInput) (charlie.AdminActionPolicy, error) {
	f.policyName, f.policyInput = name, input
	return f.actionPolicy, nil
}

func (f *charlieAdminFake) ListTriggerEvents(context.Context, string, int32, int32) ([]charlie.AdminTriggerEventView, error) {
	return f.triggerEvents, nil
}
func (f *charlieAdminFake) RetryTriggerEvent(_ context.Context, source, request uuid.UUID) (charlie.AdminTriggerEventView, error) {
	f.retrySource, f.retryRequest = source, request
	return f.retryEvent, nil
}

func (f *charlieAdminFake) Status(context.Context) (charlie.AdminStatusView, error) {
	f.statusCalls++
	return f.status, nil
}
func (f *charlieAdminFake) LocalStatus(context.Context) (charlie.AdminStatusView, error) {
	f.localCalls++
	return f.localStatus, nil
}
func (f *charlieAdminFake) Diagnostics(context.Context, string) (charlie.AdminDiagnosticsView, error) {
	f.diagnosticCalls++
	return charlie.AdminDiagnosticsView{Overall: "healthy"}, nil
}
func (f *charlieAdminFake) LocalDiagnostics(context.Context, string) (charlie.AdminDiagnosticsView, error) {
	f.localDiagnosticCalls++
	return charlie.AdminDiagnosticsView{Overall: "inactive"}, nil
}
func (f *charlieAdminFake) Uninstall(_ context.Context, actor uuid.UUID) error {
	f.uninstallCalls++
	f.uninstallActor = actor
	return nil
}
func (f *charlieAdminFake) UpdateMode(_ context.Context, mode charlie.Mode, _ int64, emergency bool, _ uuid.UUID) (charlie.AdminModeView, error) {
	f.modeCalls++
	f.modeInput, f.emergency = mode, emergency
	return f.mode, nil
}

func TestCharlieAdminStatusReturnsOnlySafeMetadata(t *testing.T) {
	fake := &charlieAdminFake{status: charlie.AdminStatusView{Connection: charlie.AdminConnectionView{
		Connected: true, ProductID: "astronomer", DeploymentID: "deployment-a", RouteID: "route-a",
		SigningFingerprint: strings.Repeat("a", 64), PackageDigest: strings.Repeat("b", 64),
	}}}
	handler := NewCharlieAdminHandler(fake, &charlieAuditWriterFake{})
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

func TestCharlieAdminAlertPolicyUsesOnlyProductOwnedRoutingFields(t *testing.T) {
	actor, channelID := uuid.New(), uuid.New()
	fake := &charlieAdminFake{alertPolicy: charlie.AdminAlertPolicyView{Enabled: true, MinimumSeverity: "high", Revision: 2, ChannelIDs: []string{channelID.String()}, InAppEnabled: true}}
	writer := &charlieAuditWriterFake{}
	h := NewCharlieAdminHandler(fake, writer)
	body := `{"revision":1,"enabled":true,"minimum_severity":"high","dedupe_window_seconds":900,"escalation_after_seconds":3600,"quiet_hours_enabled":true,"quiet_hours_start":"22:00","quiet_hours_end":"07:00","quiet_hours_timezone":"UTC","channel_ids":["` + channelID.String() + `"]}`
	recorder := httptest.NewRecorder()
	h.UpdateAlertPolicy(recorder, authenticatedCharlieRequest(http.MethodPut, "/", body, actor, "jwt"))
	if recorder.Code != http.StatusOK || fake.alertPolicyActor != actor || fake.alertPolicyInput.MinimumSeverity != "high" || fake.alertPolicyInput.Revision != 1 {
		t.Fatalf("alert policy response=%d actor=%s input=%#v", recorder.Code, fake.alertPolicyActor, fake.alertPolicyInput)
	}
	for _, forbidden := range []string{"approval", "capability", "api_key", "secret"} {
		if strings.Contains(strings.ToLower(recorder.Body.String()), forbidden) {
			t.Fatalf("alert policy leaked authority/credential field %q: %s", forbidden, recorder.Body.String())
		}
	}
	audit := string(writer.row.Detail)
	for _, forbidden := range []string{channelID.String(), "destination", "configuration", "credential", "channel_ids"} {
		if strings.Contains(strings.ToLower(audit), strings.ToLower(forbidden)) {
			t.Fatalf("alert-policy audit leaked channel routing detail %q: %s", forbidden, audit)
		}
	}
	for _, expected := range []string{`"minimum_severity":"high"`, `"channel_count":1`, `"revision":2`} {
		if !strings.Contains(audit, expected) {
			t.Fatalf("alert-policy audit lacks bounded field %s: %s", expected, audit)
		}
	}
}

func TestCharlieAdminDisabledStatusAndDiagnosticsAreLocalOnly(t *testing.T) {
	fake := &charlieAdminFake{
		status:      charlie.AdminStatusView{Connection: charlie.AdminConnectionView{DeploymentID: "remote-should-not-run"}},
		localStatus: charlie.AdminStatusView{Connection: charlie.AdminConnectionView{DeploymentID: "durable-local"}},
	}
	handler := NewCharlieAdminHandler(fake, &charlieAuditWriterFake{})
	handler.SetSettingsCache(NewSettingsCache(nil, time.Minute))

	status := httptest.NewRecorder()
	handler.Status(status, authenticatedCharlieRequest(http.MethodGet, "/", "", uuid.New(), "jwt"))
	if status.Code != http.StatusOK || !strings.Contains(status.Body.String(), "durable-local") || fake.localCalls != 1 || fake.statusCalls != 0 {
		t.Fatalf("disabled status was not local-only: code=%d local=%d remote=%d body=%s", status.Code, fake.localCalls, fake.statusCalls, status.Body.String())
	}

	diagnostics := httptest.NewRecorder()
	handler.Diagnostics(diagnostics, authenticatedCharlieRequest(http.MethodPost, "/", "{}", uuid.New(), "jwt"))
	if diagnostics.Code != http.StatusOK || !strings.Contains(diagnostics.Body.String(), "inactive") || fake.localDiagnosticCalls != 1 || fake.diagnosticCalls != 0 {
		t.Fatalf("disabled diagnostics were not local-only: code=%d local=%d remote=%d body=%s", diagnostics.Code, fake.localDiagnosticCalls, fake.diagnosticCalls, diagnostics.Body.String())
	}
}

func TestCharlieAdminDestructiveActionsRequireBrowserAndExactConfirmation(t *testing.T) {
	fake := &charlieAdminFake{}
	handler := NewCharlieAdminHandler(fake, &charlieAuditWriterFake{})

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
	handler := NewCharlieAdminHandler(fake, &charlieAuditWriterFake{})
	recorder := httptest.NewRecorder()
	handler.Mode(recorder, authenticatedCharlieRequest(http.MethodPatch, "/", `{"mode":"disabled","revision":7,"emergency_disable":true}`, uuid.New(), "jwt"))
	if recorder.Code != http.StatusOK || fake.modeInput != charlie.ModeDisabled || !fake.emergency {
		t.Fatalf("emergency mode = %d input=%q emergency=%v body=%s", recorder.Code, fake.modeInput, fake.emergency, recorder.Body.String())
	}
}

func TestCharlieAdminAuditFailureBlocksAuthorityWideningButNotEmergencyDisable(t *testing.T) {
	fake := &charlieAdminFake{mode: charlie.AdminModeView{Requested: charlie.ModeAuto, Authoritative: charlie.ModeAuto}}
	handler := NewCharlieAdminHandler(fake, &charlieAuditWriterFake{err: errors.New("database-SENTINEL")})

	widen := httptest.NewRecorder()
	handler.Mode(widen, authenticatedCharlieRequest(http.MethodPatch, "/", `{"mode":"auto","revision":7}`, uuid.New(), "jwt"))
	if widen.Code != http.StatusServiceUnavailable || fake.modeCalls != 0 {
		t.Fatalf("audit failure admitted authority widening: status=%d calls=%d body=%s", widen.Code, fake.modeCalls, widen.Body.String())
	}
	if strings.Contains(widen.Body.String(), "database-SENTINEL") {
		t.Fatalf("storage failure leaked to caller: %s", widen.Body.String())
	}

	fake.mode = charlie.AdminModeView{Requested: charlie.ModeDisabled, Authoritative: charlie.ModeDisabled, EmergencyDisabled: true}
	disable := httptest.NewRecorder()
	handler.Mode(disable, authenticatedCharlieRequest(http.MethodPatch, "/", `{"mode":"disabled","revision":8,"emergency_disable":true}`, uuid.New(), "jwt"))
	if disable.Code != http.StatusOK || fake.modeCalls != 1 || !fake.emergency || fake.modeInput != charlie.ModeDisabled {
		t.Fatalf("audit failure blocked emergency disable: status=%d calls=%d mode=%s emergency=%t body=%s", disable.Code, fake.modeCalls, fake.modeInput, fake.emergency, disable.Body.String())
	}
}

func TestCharlieAdminActionPolicyUsesBoundedStructuredInput(t *testing.T) {
	fake := &charlieAdminFake{actionPolicy: charlie.AdminActionPolicy{Capability: "astronomer.queue.retry_task", Enabled: true, Revision: 2}}
	handler := NewCharlieAdminHandler(fake, &charlieAuditWriterFake{})
	recorder := httptest.NewRecorder()
	request := authenticatedCharlieRequest(http.MethodPut, "/", `{"enabled":true,"max_actions_per_incident":1,"max_actions_per_window":2,"budget_window_seconds":900,"cooldown_seconds":300}`, uuid.New(), "jwt")
	route := chi.NewRouteContext()
	route.URLParams.Add("capability", "astronomer.queue.retry_task")
	request = request.WithContext(context.WithValue(request.Context(), chi.RouteCtxKey, route))
	handler.UpdateActionPolicy(recorder, request)
	if recorder.Code != http.StatusOK || fake.policyName != "astronomer.queue.retry_task" || !fake.policyInput.Enabled || fake.policyInput.MaxActionsPerWindow != 2 {
		t.Fatalf("action policy = %d name=%q input=%+v body=%s", recorder.Code, fake.policyName, fake.policyInput, recorder.Body.String())
	}
}

func TestCharlieAdminEmergencyDisableRemainsAvailableWhenFeatureIsOff(t *testing.T) {
	fake := &charlieAdminFake{mode: charlie.AdminModeView{Requested: charlie.ModeDisabled, Authoritative: charlie.ModeDisabled, EmergencyDisabled: true}}
	handler := NewCharlieAdminHandler(fake, &charlieAuditWriterFake{})
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
	handler := NewCharlieAdminHandler(fake, &charlieAuditWriterFake{})

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
