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
	status                    charlie.AdminStatusView
	localStatus               charlie.AdminStatusView
	statusCalls               int
	localCalls                int
	diagnosticCalls           int
	localDiagnosticCalls      int
	uninstallCalls            int
	uninstallActor            uuid.UUID
	mode                      charlie.AdminModeView
	modeInput                 charlie.Mode
	modeCalls                 int
	emergency                 bool
	ackCalls                  int
	ackDigests                []string
	ackErr                    error
	postModeDigest            string // when set, UpdateMode returns unacked post-transition digest
	triggerEvents             []charlie.AdminTriggerEventView
	retryEvent                charlie.AdminTriggerEventView
	retrySource               uuid.UUID
	retryRequest              uuid.UUID
	actionPolicy              charlie.AdminActionPolicy
	policyName                string
	policyInput               charlie.AdminActionPolicyInput
	alertPolicy               charlie.AdminAlertPolicyView
	alertPolicyErr            error
	alertPolicyInput          charlie.AdminAlertPolicyInput
	alertPolicyActor          uuid.UUID
	alertDeliveryProof        charlie.AdminAlertDeliveryProofView
	alertDeliveryFinding      uuid.UUID
	discoveryProof            charlie.AdminDiscoveryQualificationView
	discoveryScenario         string
	kubernetesVisibility      charlie.AdminKubernetesVisibilityView
	kubernetesVisibilityInput charlie.AdminKubernetesVisibilityInput
	kubernetesVisibilityActor uuid.UUID
	kubernetesVisibilityCalls int
	kubernetesVisibilityErr   error
}

func (f *charlieAdminFake) KubernetesVisibility(context.Context) (charlie.AdminKubernetesVisibilityView, error) {
	return f.kubernetesVisibility, f.kubernetesVisibilityErr
}

func (f *charlieAdminFake) UpdateKubernetesVisibility(_ context.Context, input charlie.AdminKubernetesVisibilityInput, actor uuid.UUID) (charlie.AdminKubernetesVisibilityView, error) {
	f.kubernetesVisibilityCalls++
	f.kubernetesVisibilityInput = input
	f.kubernetesVisibilityActor = actor
	return f.kubernetesVisibility, f.kubernetesVisibilityErr
}

func TestCharlieAdminKubernetesVisibilityIsBoundedAndAudited(t *testing.T) {
	actor := uuid.New()
	fake := &charlieAdminFake{kubernetesVisibility: charlie.AdminKubernetesVisibilityView{
		Schema: "charlie.kubernetes-visibility/v1", Profile: charlie.KubernetesVisibilityClusterDiagnostics,
		Revision: 8, State: "enabled", InstanceID: "astronomer-management-plane", Namespaces: []string{"astronomer"},
		ProductOwnedOnly: true, ClusterScoped: true, AvailableProfiles: []charlie.KubernetesVisibilityProfile{
			charlie.KubernetesVisibilityDisabled, charlie.KubernetesVisibilityProductNamespace, charlie.KubernetesVisibilityClusterDiagnostics,
		},
	}}
	writer := &charlieAuditWriterFake{}
	handler := NewCharlieAdminHandler(fake, writer)

	read := httptest.NewRecorder()
	handler.KubernetesVisibility(read, authenticatedCharlieRequest(http.MethodGet, "/", "", actor, "jwt"))
	if read.Code != http.StatusOK || !strings.Contains(read.Body.String(), `"profile":"cluster_diagnostics"`) || !strings.Contains(read.Body.String(), `"downstream_targets":false`) {
		t.Fatalf("Kubernetes visibility read=%d body=%s", read.Code, read.Body.String())
	}
	for _, prohibited := range []string{"kubeconfig", "token", `"secret_values":true`, "api_server", "capabilities"} {
		if strings.Contains(strings.ToLower(read.Body.String()), prohibited) {
			t.Fatalf("Kubernetes visibility response leaked %q: %s", prohibited, read.Body.String())
		}
	}

	update := httptest.NewRecorder()
	handler.UpdateKubernetesVisibility(update, authenticatedCharlieRequest(http.MethodPut, "/", `{"profile":"cluster_diagnostics","pod_logs":false,"revision":7}`, actor, "jwt"))
	if update.Code != http.StatusOK || fake.kubernetesVisibilityCalls != 1 || fake.kubernetesVisibilityActor != actor ||
		fake.kubernetesVisibilityInput.Profile != charlie.KubernetesVisibilityClusterDiagnostics || fake.kubernetesVisibilityInput.PodLogs || fake.kubernetesVisibilityInput.Revision != 7 {
		t.Fatalf("Kubernetes visibility update=%d calls=%d actor=%s input=%+v body=%s", update.Code, fake.kubernetesVisibilityCalls, fake.kubernetesVisibilityActor, fake.kubernetesVisibilityInput, update.Body.String())
	}
	if writer.row.Action != "admin.charlie.kubernetes_visibility.update" || writer.row.ResourceType != "charlie_connection" ||
		!strings.Contains(string(writer.row.Detail), `"profile":"cluster_diagnostics"`) || !strings.Contains(string(writer.row.Detail), `"revision":7`) {
		t.Fatalf("Kubernetes visibility audit incomplete: %+v detail=%s", writer.row, writer.row.Detail)
	}
}

func TestCharlieAdminKubernetesVisibilityRejectsMalformedAndConflictingUpdates(t *testing.T) {
	actor := uuid.New()
	fake := &charlieAdminFake{}
	handler := NewCharlieAdminHandler(fake, &charlieAuditWriterFake{})

	malformed := httptest.NewRecorder()
	handler.UpdateKubernetesVisibility(malformed, authenticatedCharlieRequest(http.MethodPut, "/", `{"profile":"disabled","pod_logs":false,"revision":1,"credential":"forbidden"}`, actor, "jwt"))
	if malformed.Code != http.StatusBadRequest || fake.kubernetesVisibilityCalls != 0 {
		t.Fatalf("unknown Kubernetes visibility field reached backend: status=%d calls=%d body=%s", malformed.Code, fake.kubernetesVisibilityCalls, malformed.Body.String())
	}

	fake.kubernetesVisibilityErr = charlie.ErrAdminConflict
	conflict := httptest.NewRecorder()
	handler.UpdateKubernetesVisibility(conflict, authenticatedCharlieRequest(http.MethodPut, "/", `{"profile":"product_namespace","pod_logs":true,"revision":2}`, actor, "jwt"))
	if conflict.Code != http.StatusConflict || fake.kubernetesVisibilityCalls != 1 {
		t.Fatalf("Kubernetes visibility conflict=%d calls=%d body=%s", conflict.Code, fake.kubernetesVisibilityCalls, conflict.Body.String())
	}
}

func (f *charlieAdminFake) AlertDeliveryProofs(_ context.Context, findingID uuid.UUID) (charlie.AdminAlertDeliveryProofView, error) {
	f.alertDeliveryFinding = findingID
	return f.alertDeliveryProof, nil
}

func (f *charlieAdminFake) DiscoveryQualification(_ context.Context, scenario string) (charlie.AdminDiscoveryQualificationView, error) {
	f.discoveryScenario = scenario
	return f.discoveryProof, nil
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

func TestCharlieAdminAlertDeliveryProofIsMetadataOnlyAndAuditedAsRead(t *testing.T) {
	findingID, deliveryID := uuid.New(), uuid.New()
	now := time.Now().UTC()
	fake := &charlieAdminFake{alertDeliveryProof: charlie.AdminAlertDeliveryProofView{
		FindingID: findingID.String(), FindingBlockCode: "verification_failed", FindingWorkflowState: "manual_remediation_required",
		DeliveryCount: 1, DedupeValid: true,
		Deliveries: []charlie.AdminAlertDeliveryProof{{
			DeliveryID: deliveryID.String(), FindingID: findingID.String(), DeliveryKind: "initial", Status: "delivered",
			TemplateIdentity: "charlie.finding.initial/v1", DeepLinkValid: true, ContentFree: true,
			AttemptCount: 1, MaximumAttempts: 8, CreatedAt: now, UpdatedAt: now,
		}},
	}}
	writer := &charlieAuditWriterFake{}
	h := NewCharlieAdminHandler(fake, writer)
	request := authenticatedCharlieRequest(http.MethodGet, "/?finding_id="+findingID.String(), "", uuid.New(), "jwt")
	recorder := httptest.NewRecorder()
	h.AlertDeliveryProofs(recorder, request)
	if recorder.Code != http.StatusOK || fake.alertDeliveryFinding != findingID || !strings.Contains(recorder.Body.String(), deliveryID.String()) {
		t.Fatalf("alert delivery proof response=%d finding=%s body=%s", recorder.Code, fake.alertDeliveryFinding, recorder.Body.String())
	}
	for _, prohibited := range []string{"subject", "body", "destination", "channel_id", "dedupe_bucket", "last_error"} {
		if strings.Contains(strings.ToLower(recorder.Body.String()), prohibited) {
			t.Fatalf("alert delivery proof leaked %q: %s", prohibited, recorder.Body.String())
		}
	}
	if writer.row.Action != "admin.charlie.alert_delivery.read" || writer.row.ActionClass != "read" || writer.row.ResourceID != findingID.String() ||
		!strings.Contains(string(writer.row.Detail), `"delivery_count":1`) || !strings.Contains(string(writer.row.Detail), `"dedupe_valid":true`) {
		t.Fatalf("alert delivery read audit is incomplete: %+v detail=%s", writer.row, writer.row.Detail)
	}
}

func TestCharlieAdminAlertDeliveryProofRequiresExactFindingID(t *testing.T) {
	fake := &charlieAdminFake{}
	h := NewCharlieAdminHandler(fake, &charlieAuditWriterFake{})
	for _, raw := range []string{"", "not-a-uuid", uuid.Nil.String()} {
		recorder := httptest.NewRecorder()
		h.AlertDeliveryProofs(recorder, authenticatedCharlieRequest(http.MethodGet, "/?finding_id="+raw, "", uuid.New(), "jwt"))
		if recorder.Code != http.StatusBadRequest || fake.alertDeliveryFinding != uuid.Nil {
			t.Fatalf("invalid finding %q reached backend: status=%d finding=%s", raw, recorder.Code, fake.alertDeliveryFinding)
		}
	}
}

func TestCharlieAdminDiscoveryQualificationAcceptsOnlyFixedCasesAndAuditsMetadata(t *testing.T) {
	fake := &charlieAdminFake{discoveryProof: charlie.AdminDiscoveryQualificationView{
		Scenario: charlie.DiscoveryQualificationMixed, CandidateEnabled: true, AcceptedCount: 2, RejectedCount: 1,
		AcceptedNames:    []string{"astronomer.installation.summary", "astronomer.management.workload_restart"},
		DisclosureDigest: strings.Repeat("a", 64), CatalogBound: true, MalformedRejected: true,
	}}
	writer := &charlieAuditWriterFake{}
	h := NewCharlieAdminHandler(fake, writer)
	recorder := httptest.NewRecorder()
	h.DiscoveryQualification(recorder, authenticatedCharlieRequest(http.MethodPost, "/", `{"scenario":"mixed_catalog"}`, uuid.New(), "jwt"))
	if recorder.Code != http.StatusOK || fake.discoveryScenario != charlie.DiscoveryQualificationMixed || !strings.Contains(recorder.Body.String(), `"accepted_count":2`) {
		t.Fatalf("discovery qualification response=%d scenario=%q body=%s", recorder.Code, fake.discoveryScenario, recorder.Body.String())
	}
	if writer.row.Action != "admin.charlie.discovery_qualification.run" || writer.row.ActionClass != "read" || strings.Contains(string(writer.row.Detail), "astronomer.") || strings.Contains(string(writer.row.Detail), strings.Repeat("a", 64)) {
		t.Fatalf("discovery qualification audit leaked catalog detail: %+v detail=%s", writer.row, writer.row.Detail)
	}

	for _, body := range []string{`{"scenario":"custom"}`, `{"scenario":"mixed_catalog","catalog":[]}`} {
		fake.discoveryScenario = ""
		rejected := httptest.NewRecorder()
		h.DiscoveryQualification(rejected, authenticatedCharlieRequest(http.MethodPost, "/", body, uuid.New(), "jwt"))
		if rejected.Code != http.StatusBadRequest || fake.discoveryScenario != "" {
			t.Fatalf("arbitrary discovery payload accepted: status=%d scenario=%q body=%s", rejected.Code, fake.discoveryScenario, rejected.Body.String())
		}
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
	if f.postModeDigest != "" {
		// Mode transitions rewrite disclosure from central and clear prior ack.
		f.mode = charlie.AdminModeView{
			Requested: mode, Authoritative: mode, Revision: f.mode.Revision + 1,
			DisclosureDigest: f.postModeDigest, AcknowledgedDisclosureDigest: "",
			AutoReadiness: charlie.AdminAutoReadiness{Ready: false, Blockers: []charlie.AdminAutoReadinessBlocker{{
				Code: "disclosure_unacknowledged", Message: "The current MCP capability disclosure is not acknowledged.",
			}}},
			WorkloadCeiling: mode, WorkloadCeilingReady: true,
		}
	}
	return f.mode, nil
}
func (f *charlieAdminFake) AcknowledgeDisclosure(_ context.Context, digest string) (charlie.AdminModeView, error) {
	f.ackCalls++
	f.ackDigests = append(f.ackDigests, digest)
	if f.ackErr != nil {
		return charlie.AdminModeView{}, f.ackErr
	}
	// Simulate mode-transition digest rewrite: first ack uses the supplied
	// pre-transition digest; subsequent acks accept the live post-mode digest.
	view := f.mode
	if f.postModeDigest != "" && digest == f.postModeDigest {
		view.DisclosureDigest = f.postModeDigest
		view.AcknowledgedDisclosureDigest = f.postModeDigest
		view.AutoReadiness = charlie.AdminAutoReadiness{Ready: true, Blockers: []charlie.AdminAutoReadinessBlocker{}}
		f.mode = view
		return view, nil
	}
	if f.postModeDigest != "" && digest != f.postModeDigest {
		// Pre-transition ack succeeds but mode update will present a new digest.
		view.DisclosureDigest = digest
		view.AcknowledgedDisclosureDigest = digest
		return view, nil
	}
	view.DisclosureDigest = digest
	view.AcknowledgedDisclosureDigest = digest
	view.AutoReadiness = charlie.AdminAutoReadiness{Ready: true, Blockers: []charlie.AdminAutoReadinessBlocker{}}
	f.mode = view
	return view, nil
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

func TestCharlieAdminCombinedModeRaiseReAcksPostTransitionDisclosure(t *testing.T) {
	// approval→auto rewrites disclosure (mode-dependent catalog). Combined
	// mode+ack must re-ack the live post-transition digest so auto is ready.
	preDigest := "sha256:" + strings.Repeat("b", 64)
	postDigest := "sha256:" + strings.Repeat("a", 64)
	fake := &charlieAdminFake{
		mode: charlie.AdminModeView{
			Requested: charlie.ModeApproval, Authoritative: charlie.ModeApproval, Revision: 10,
			DisclosureDigest: preDigest,
		},
		postModeDigest: postDigest,
	}
	handler := NewCharlieAdminHandler(fake, &charlieAuditWriterFake{})
	body := `{"mode":"auto","revision":10,"acknowledge_disclosure_digest":"` + preDigest + `"}`
	recorder := httptest.NewRecorder()
	handler.Mode(recorder, authenticatedCharlieRequest(http.MethodPatch, "/", body, uuid.New(), "jwt"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("combined mode raise = %d body=%s", recorder.Code, recorder.Body.String())
	}
	if fake.modeCalls != 1 || fake.modeInput != charlie.ModeAuto {
		t.Fatalf("mode not raised: calls=%d input=%s", fake.modeCalls, fake.modeInput)
	}
	// Pre-transition ack + post-transition re-ack.
	if fake.ackCalls != 2 || len(fake.ackDigests) != 2 {
		t.Fatalf("expected pre+post disclosure acks, got calls=%d digests=%v", fake.ackCalls, fake.ackDigests)
	}
	if fake.ackDigests[0] != preDigest || fake.ackDigests[1] != postDigest {
		t.Fatalf("ack digests = %v want pre=%s post=%s", fake.ackDigests, preDigest, postDigest)
	}
	if !strings.Contains(recorder.Body.String(), postDigest) || !strings.Contains(recorder.Body.String(), `"ready":true`) {
		t.Fatalf("response missing ready post-ack: %s", recorder.Body.String())
	}
}

func TestCharlieAdminDisclosureOnlyAckDoesNotChangeMode(t *testing.T) {
	digest := "sha256:" + strings.Repeat("c", 64)
	fake := &charlieAdminFake{mode: charlie.AdminModeView{
		Requested: charlie.ModeAuto, Authoritative: charlie.ModeAuto, Revision: 3,
		DisclosureDigest: digest,
	}}
	handler := NewCharlieAdminHandler(fake, &charlieAuditWriterFake{})
	recorder := httptest.NewRecorder()
	handler.Mode(recorder, authenticatedCharlieRequest(http.MethodPatch, "/", `{"acknowledge_disclosure_digest":"`+digest+`"}`, uuid.New(), "jwt"))
	if recorder.Code != http.StatusOK || fake.modeCalls != 0 || fake.ackCalls != 1 {
		t.Fatalf("disclosure-only path = %d modeCalls=%d ackCalls=%d body=%s", recorder.Code, fake.modeCalls, fake.ackCalls, recorder.Body.String())
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
