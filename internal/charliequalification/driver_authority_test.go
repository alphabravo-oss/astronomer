package charliequalification

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const qualificationCapability = "astronomer.queue.retry_task"

type qualificationApproval struct {
	fixture   ApprovalFixture
	expiresAt time.Time
	state     string
	visible   bool
	requestID string
	decision  string
	rationale string
}

type qualificationOperation struct {
	actionID   string
	capability string
}

type authorityLiveState struct {
	mu               sync.Mutex
	requested        string
	authority        string
	revision         int64
	digest           string
	acknowledged     bool
	runtime          map[string]uint64
	downstream       map[string]uint64
	approvals        map[string]*qualificationApproval
	operations       map[string]qualificationOperation
	autoAction       ActionFixture
	autoPending      PendingApprovalFixture
	autoTriggered    bool
	sessions         map[string]SessionStimulus
	clientSessions   map[string]string
	modeDelay        time.Duration
	failRestore      bool
	failAbort        bool
	replaySession    bool
	unwrapApprovals  bool
	unwrapDecision   bool
	unwrapOperation  bool
	streamVariant    string
	activeModes      atomic.Int32
	maximumModeCalls atomic.Int32
}

func authorityFixtures() LiveFixtures {
	return LiveFixtures{
		ApprovalExpiry: ApprovalFixture{
			ApprovalID: "approval-expiry", Capability: qualificationCapability,
			DecisionRequest: "00000000-0000-4000-8000-000000000001",
		},
		ApprovalOnce: ApprovalFixture{
			ApprovalID: "approval-once", ActionID: "action-once", Capability: qualificationCapability,
			DecisionRequest: "00000000-0000-4000-8000-000000000002",
			ReplayRequest:   "00000000-0000-4000-8000-000000000003",
		},
		ApprovalReject: ApprovalFixture{
			ApprovalID: "approval-reject", Capability: qualificationCapability,
			DecisionRequest: "00000000-0000-4000-8000-000000000004",
		},
		AutoAllowlistedSuccess: ActionFixture{
			Capability: qualificationCapability,
			Stimulus: SessionStimulus{
				ClientSessionID: "10000000-0000-4000-8000-000000000001", ClientMessageID: "20000000-0000-4000-8000-000000000001", AbortRequestID: "40000000-0000-4000-8000-000000000001",
				Intent: "qualification_auto_allowlisted", ResourceType: "management_component", ResourceID: "qualification-task-auto", Message: "Run the exact safe allowlisted qualification action.",
			},
		},
		AutoNonallowlisted: PendingApprovalFixture{
			Capability: "astronomer.management.workload_restart",
			Stimulus: SessionStimulus{
				ClientSessionID: "10000000-0000-4000-8000-000000000002", ClientMessageID: "20000000-0000-4000-8000-000000000002", AbortRequestID: "40000000-0000-4000-8000-000000000002",
				Intent: "qualification_auto_approval", ResourceType: "management_component", ResourceID: "qualification-workload", Message: "Propose the exact safe non-allowlisted qualification action.",
			},
		},
	}
}

func newAuthorityLiveState(fixtures LiveFixtures) *authorityLiveState {
	now := time.Now().UTC()
	approvals := map[string]*qualificationApproval{}
	for _, fixture := range []ApprovalFixture{fixtures.ApprovalExpiry, fixtures.ApprovalOnce, fixtures.ApprovalReject} {
		approvals[fixture.ApprovalID] = &qualificationApproval{fixture: fixture, expiresAt: now.Add(time.Minute), state: "pending", visible: true}
	}
	return &authorityLiveState{
		requested: "read_only", authority: "read_only", revision: 70, digest: readOnlyDisclosureDigest, acknowledged: true,
		runtime: map[string]uint64{}, downstream: map[string]uint64{}, approvals: approvals, operations: map[string]qualificationOperation{},
		sessions: map[string]SessionStimulus{}, clientSessions: map[string]string{},
	}
}

func newAuthorityDriver(t *testing.T, state *authorityLiveState, fixtures LiveFixtures) (*LiveDriver, *httptest.Server) {
	t.Helper()
	server := httptest.NewTLSServer(http.HandlerFunc(state.serveHTTP))
	driver, err := NewLiveDriver(LiveConfig{
		AstronomerURL: server.URL, AdminToken: "admin", ApproverToken: "approver", Fixtures: fixtures,
		MetricSources: []MetricSource{{URL: server.URL + "/metrics", Token: "admin"}}, HTTPClient: server.Client(),
		ProofTimeout: 2 * time.Second, ProofPoll: time.Millisecond, NoCallDwell: time.Millisecond,
	})
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	return driver, server
}

func TestAuthorityQualificationScenariosUseExactFixturesAndRestoreReadOnly(t *testing.T) {
	t.Run("approval expiry", func(t *testing.T) {
		fixtures := authorityFixtures()
		state := newAuthorityLiveState(fixtures)
		state.approvals[fixtures.ApprovalExpiry.ApprovalID].expiresAt = time.Now().UTC().Add(150 * time.Millisecond)
		driver, server := newAuthorityDriver(t, state, fixtures)
		defer server.Close()
		assertQualificationPassed(t, driver.Run(t.Context(), ScenarioRequest{Scenario: "approval_expiry"}))
		assertAuthorityRestored(t, state, 0)
	})

	t.Run("approval once and replay", func(t *testing.T) {
		fixtures := authorityFixtures()
		state := newAuthorityLiveState(fixtures)
		driver, server := newAuthorityDriver(t, state, fixtures)
		defer server.Close()
		assertQualificationPassed(t, driver.Run(t.Context(), ScenarioRequest{Scenario: "approval_once"}))
		assertAuthorityRestored(t, state, 1)
		assertQualificationPassed(t, driver.Run(t.Context(), ScenarioRequest{Scenario: "approval_replay"}))
		assertAuthorityRestored(t, state, 1)
	})

	t.Run("approval rejection", func(t *testing.T) {
		fixtures := authorityFixtures()
		state := newAuthorityLiveState(fixtures)
		driver, server := newAuthorityDriver(t, state, fixtures)
		defer server.Close()
		assertQualificationPassed(t, driver.Run(t.Context(), ScenarioRequest{Scenario: "approval_reject"}))
		assertAuthorityRestored(t, state, 0)
	})

	t.Run("allowlisted auto action", func(t *testing.T) {
		fixtures := authorityFixtures()
		state := newAuthorityLiveState(fixtures)
		state.autoAction = fixtures.AutoAllowlistedSuccess
		driver, server := newAuthorityDriver(t, state, fixtures)
		defer server.Close()
		assertQualificationPassed(t, driver.Run(t.Context(), ScenarioRequest{Scenario: "auto_allowlisted_success"}))
		assertAuthorityRestored(t, state, 1)
	})

	t.Run("nonallowlisted auto action stays pending", func(t *testing.T) {
		fixtures := authorityFixtures()
		state := newAuthorityLiveState(fixtures)
		state.autoPending = fixtures.AutoNonallowlisted
		driver, server := newAuthorityDriver(t, state, fixtures)
		defer server.Close()
		assertQualificationPassed(t, driver.Run(t.Context(), ScenarioRequest{Scenario: "auto_nonallowlisted_approval"}))
		assertAuthorityRestored(t, state, 0)
	})
}

func TestAuthorityQualificationFailsClosedOnMissingOrMismatchedProof(t *testing.T) {
	tests := []struct {
		name     string
		scenario string
		mutate   func(*authorityLiveState, *LiveFixtures)
	}{
		{name: "missing approver token", scenario: "approval_reject", mutate: func(_ *authorityLiveState, _ *LiveFixtures) {}},
		{name: "wrong approval capability", scenario: "approval_reject", mutate: func(state *authorityLiveState, fixtures *LiveFixtures) {
			state.approvals[fixtures.ApprovalReject.ApprovalID].fixture.Capability = "astronomer.queue.failed_tasks"
		}},
		{name: "replayed fixture session", scenario: "auto_allowlisted_success", mutate: func(state *authorityLiveState, fixtures *LiveFixtures) {
			state.autoAction = fixtures.AutoAllowlistedSuccess
			state.replaySession = true
		}},
		{name: "mode transition without action stimulus", scenario: "auto_allowlisted_success", mutate: func(_ *authorityLiveState, _ *LiveFixtures) {}},
		{name: "unwrapped approval list", scenario: "approval_reject", mutate: func(state *authorityLiveState, _ *LiveFixtures) {
			state.unwrapApprovals = true
		}},
		{name: "unwrapped approval decision", scenario: "approval_reject", mutate: func(state *authorityLiveState, _ *LiveFixtures) {
			state.unwrapDecision = true
		}},
		{name: "unwrapped operation receipt", scenario: "auto_allowlisted_success", mutate: func(state *authorityLiveState, fixtures *LiveFixtures) {
			state.autoAction = fixtures.AutoAllowlistedSuccess
			state.unwrapOperation = true
		}},
		{name: "unrelated turn action event", scenario: "auto_allowlisted_success", mutate: func(state *authorityLiveState, fixtures *LiveFixtures) {
			state.autoAction = fixtures.AutoAllowlistedSuccess
			state.streamVariant = "unrelated_turn"
		}},
		{name: "multiple matching actions", scenario: "auto_allowlisted_success", mutate: func(state *authorityLiveState, fixtures *LiveFixtures) {
			state.autoAction = fixtures.AutoAllowlistedSuccess
			state.streamVariant = "multiple_actions"
		}},
		{name: "conflicting stream event type", scenario: "auto_allowlisted_success", mutate: func(state *authorityLiveState, fixtures *LiveFixtures) {
			state.autoAction = fixtures.AutoAllowlistedSuccess
			state.streamVariant = "conflicting_type"
		}},
		{name: "invalid stream event identifier", scenario: "auto_allowlisted_success", mutate: func(state *authorityLiveState, fixtures *LiveFixtures) {
			state.autoAction = fixtures.AutoAllowlistedSuccess
			state.streamVariant = "invalid_event_id"
		}},
		{name: "automatic session abort failure", scenario: "auto_allowlisted_success", mutate: func(state *authorityLiveState, fixtures *LiveFixtures) {
			state.autoAction = fixtures.AutoAllowlistedSuccess
			state.failAbort = true
		}},
		{name: "read only restoration failure", scenario: "approval_reject", mutate: func(state *authorityLiveState, _ *LiveFixtures) {
			state.failRestore = true
		}},
		{name: "reused approval identifier", scenario: "approval_reject", mutate: func(_ *authorityLiveState, fixtures *LiveFixtures) {
			fixtures.ApprovalReject.ApprovalID = fixtures.ApprovalOnce.ApprovalID
		}},
		{name: "reused automatic request identifier", scenario: "auto_nonallowlisted_approval", mutate: func(state *authorityLiveState, fixtures *LiveFixtures) {
			fixtures.AutoNonallowlisted.Stimulus.ClientSessionID = fixtures.AutoAllowlistedSuccess.Stimulus.ClientSessionID
			state.autoPending = fixtures.AutoNonallowlisted
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixtures := authorityFixtures()
			state := newAuthorityLiveState(fixtures)
			test.mutate(state, &fixtures)
			driver, server := newAuthorityDriver(t, state, fixtures)
			defer server.Close()
			driver.proofTimeout = 25 * time.Millisecond
			if test.name == "missing approver token" {
				driver.approverToken = ""
			}
			result := driver.Run(t.Context(), ScenarioRequest{Scenario: test.scenario})
			if result.Passed || len(result.Assertions) != 0 {
				t.Fatalf("scenario accepted missing proof: %#v", result)
			}
		})
	}
}

func TestAuthorityQualificationDriverSerializesDirectConcurrentRuns(t *testing.T) {
	fixtures := authorityFixtures()
	state := newAuthorityLiveState(fixtures)
	state.modeDelay = 10 * time.Millisecond
	state.autoPending = fixtures.AutoNonallowlisted
	driver, server := newAuthorityDriver(t, state, fixtures)
	defer server.Close()

	start := make(chan struct{})
	results := make(chan ScenarioResult, 2)
	for _, scenario := range []string{"approval_reject", "auto_nonallowlisted_approval"} {
		scenario := scenario
		go func() {
			<-start
			results <- driver.Run(t.Context(), ScenarioRequest{Scenario: scenario})
		}()
	}
	close(start)
	for range 2 {
		assertQualificationPassed(t, <-results)
	}
	if maximum := state.maximumModeCalls.Load(); maximum != 1 {
		t.Fatalf("concurrent destructive mode transitions = %d, want 1", maximum)
	}
	assertAuthorityRestored(t, state, 0)
}

func assertQualificationPassed(t *testing.T, result ScenarioResult) {
	t.Helper()
	if !result.Passed || len(result.Assertions) != 2 {
		t.Fatalf("qualification failed: %#v", result)
	}
}

func assertAuthorityRestored(t *testing.T, state *authorityLiveState, toolCalls uint64) {
	t.Helper()
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.requested != "read_only" || state.authority != "read_only" || !state.acknowledged || state.digest != readOnlyDisclosureDigest || state.runtime["tool_calls"] != toolCalls {
		t.Fatalf("authority baseline not restored exactly: mode=%s/%s ack=%t digest=%s tool_calls=%d", state.requested, state.authority, state.acknowledged, state.digest, state.runtime["tool_calls"])
	}
}

func (s *authorityLiveState) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPatch && r.URL.Path == "/api/v1/admin/charlie/mode/" {
		active := s.activeModes.Add(1)
		for current := s.maximumModeCalls.Load(); active > current && !s.maximumModeCalls.CompareAndSwap(current, active); current = s.maximumModeCalls.Load() {
		}
		defer s.activeModes.Add(-1)
		if s.modeDelay > 0 {
			time.Sleep(s.modeDelay)
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if r.URL.Path == "/metrics" {
		if r.Header.Get("Authorization") != "Bearer admin" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		s.writeMetrics(w)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/v1/admin/") {
		if r.Header.Get("Authorization") != "Bearer admin" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		s.serveAdmin(w, r)
		return
	}
	if r.Header.Get("Authorization") != "Bearer approver" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	s.serveProduct(w, r)
}

func (s *authorityLiveState) writeMetrics(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/plain")
	for _, key := range runtimeKeys {
		_, _ = fmt.Fprintf(w, "%s %d\n", defaultCounterMetrics()[key], s.runtime[key])
	}
	_, _ = fmt.Fprintf(w, "astronomer_charlie_downstream_boundary_calls_total{entrypoint=%q,operation=%q} %d\n", "tunnel_message", "other", s.downstream["tunnel"])
	_, _ = fmt.Fprintf(w, "astronomer_charlie_downstream_boundary_calls_total{entrypoint=%q,operation=%q} %d\n", "kubernetes_proxy", "other", s.downstream["proxy"])
	for _, operation := range []string{"kubernetes", "exec", "logs", "helm"} {
		_, _ = fmt.Fprintf(w, "astronomer_charlie_downstream_boundary_calls_total{entrypoint=%q,operation=%q} %d\n", "other", operation, s.downstream[operation])
	}
}

func (s *authorityLiveState) serveAdmin(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/admin/charlie/status/":
		_, _ = fmt.Fprintf(w, `{"data":{"connection":{"connected":true,"disclosure_digest":%q,"disclosure_acknowledged":%t},"mode":{"requested":%q,"authoritative":%q,"revision":%d,"emergency_disabled":false,"disclosure_digest":%q},"agent":{"desired_replicas":2,"ready_replicas":2,"agent_version":"v1.2.3","image_digest":"sha256:test"}}}`, s.digest, s.acknowledged, s.requested, s.authority, s.revision, s.digest)
	case r.Method == http.MethodPatch && r.URL.Path == "/api/v1/admin/charlie/mode/":
		var body struct {
			Mode                        string `json:"mode"`
			Revision                    *int64 `json:"revision"`
			AcknowledgeDisclosureDigest string `json:"acknowledge_disclosure_digest"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			http.Error(w, "invalid", http.StatusBadRequest)
			return
		}
		if body.AcknowledgeDisclosureDigest != "" {
			if body.Revision != nil || body.Mode != "" || body.AcknowledgeDisclosureDigest != s.digest {
				http.Error(w, "conflict", http.StatusConflict)
				return
			}
			s.acknowledged = true
		} else {
			if body.Revision == nil || *body.Revision != s.revision || (body.Mode != "read_only" && body.Mode != "approval" && body.Mode != "auto") {
				http.Error(w, "conflict", http.StatusConflict)
				return
			}
			if body.Mode == "read_only" && s.failRestore {
				http.Error(w, "restore unavailable", http.StatusServiceUnavailable)
				return
			}
			s.requested, s.authority = body.Mode, body.Mode
			s.revision++
			s.digest = readOnlyDisclosureDigest
			s.acknowledged = false
		}
		_, _ = fmt.Fprintf(w, `{"data":{"requested":%q,"authoritative":%q,"revision":%d,"emergency_disabled":false}}`, s.requested, s.authority, s.revision)
	default:
		http.NotFound(w, r)
	}
}

func (s *authorityLiveState) serveProduct(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/charlie/sessions/":
		s.createSession(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/charlie/approvals/":
		items := []approvalProof{}
		for _, approval := range s.approvals {
			if approval.visible && approval.state == "pending" && approval.expiresAt.After(time.Now().UTC()) {
				items = append(items, approvalProof{ID: approval.fixture.ApprovalID, State: "pending", Eligible: true, Capability: approval.fixture.Capability, Target: approval.fixture.ActionID, ExpiresAt: approval.expiresAt})
			}
		}
		if s.unwrapApprovals {
			_ = json.NewEncoder(w).Encode(map[string]any{"items": items})
		} else {
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"items": items}})
		}
	case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/api/v1/charlie/approvals/") && strings.HasSuffix(r.URL.Path, "/decision/"):
		id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/charlie/approvals/"), "/decision/")
		s.decide(w, r, id)
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/v1/charlie/operations/"):
		id := strings.TrimPrefix(r.URL.Path, "/api/v1/charlie/operations/")
		operation, ok := s.operations[id]
		if !ok {
			http.Error(w, "not found", http.StatusForbidden)
			return
		}
		payload := map[string]any{"operation_id": operation.actionID, "capability": operation.capability, "state": "succeeded", "result_status": "succeeded"}
		if s.unwrapOperation {
			_ = json.NewEncoder(w).Encode(payload)
		} else {
			_ = json.NewEncoder(w).Encode(map[string]any{"data": payload})
		}
	case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/api/v1/charlie/sessions/") && strings.HasSuffix(r.URL.Path, "/messages/"):
		s.acceptStimulus(w, r)
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/v1/charlie/sessions/") && strings.HasSuffix(r.URL.Path, "/events/"):
		s.streamEvents(w, r)
	case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/api/v1/charlie/sessions/") && strings.HasSuffix(r.URL.Path, "/abort/"):
		s.abortSession(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (s *authorityLiveState) createSession(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ClientSessionID string `json:"client_session_id"`
		Intent          string `json:"intent"`
		Resources       []struct {
			Type         string `json:"type"`
			ID           string `json:"id"`
			RequiredVerb string `json:"required_verb"`
		} `json:"resources"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil || s.requested != "auto" || s.authority != "auto" || len(body.Resources) != 1 || body.Resources[0].RequiredVerb != "read" {
		http.Error(w, "invalid", http.StatusBadRequest)
		return
	}
	match := func(stimulus SessionStimulus) bool {
		return stimulus.ClientSessionID == body.ClientSessionID && stimulus.Intent == body.Intent && stimulus.ResourceType == body.Resources[0].Type && stimulus.ResourceID == body.Resources[0].ID
	}
	stimulus, localID := SessionStimulus{}, ""
	if match(s.autoAction.Stimulus) {
		stimulus, localID = s.autoAction.Stimulus, "30000000-0000-4000-8000-000000000001"
	} else if match(s.autoPending.Stimulus) {
		stimulus, localID = s.autoPending.Stimulus, "30000000-0000-4000-8000-000000000002"
	} else {
		http.Error(w, "fixture mismatch", http.StatusConflict)
		return
	}
	if existing := s.clientSessions[body.ClientSessionID]; existing != "" || s.replaySession {
		if existing == "" {
			existing = localID
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"session": map[string]any{"id": existing}, "replayed": true}})
		return
	}
	s.sessions[localID], s.clientSessions[body.ClientSessionID] = stimulus, localID
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"session": map[string]any{"id": localID}, "replayed": false}})
}

func (s *authorityLiveState) acceptStimulus(w http.ResponseWriter, r *http.Request) {
	localSessionID := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/charlie/sessions/"), "/messages/")
	var body struct {
		ClientMessageID string `json:"client_message_id"`
		Message         string `json:"message"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil || s.requested != "auto" || s.authority != "auto" {
		http.Error(w, "invalid", http.StatusBadRequest)
		return
	}
	stimulus, exists := s.sessions[localSessionID]
	if !exists || stimulus.ClientMessageID != body.ClientMessageID || stimulus.Message != body.Message {
		http.Error(w, "fixture mismatch", http.StatusConflict)
		return
	}
	turnID := "turn-auto-pending"
	if stimulus.ClientSessionID == s.autoAction.Stimulus.ClientSessionID && !s.autoTriggered {
		s.autoTriggered = true
		turnID = "turn-auto-success"
		actionID := "action-auto-generated"
		s.runtime["tool_calls"]++
		s.operations[actionID] = qualificationOperation{actionID: actionID, capability: s.autoAction.Capability}
	} else if stimulus.ClientSessionID == s.autoPending.Stimulus.ClientSessionID {
		approvalID := "approval-auto-generated"
		target := stimulus.ResourceType + ":" + stimulus.ResourceID
		s.approvals[approvalID] = &qualificationApproval{fixture: ApprovalFixture{ApprovalID: approvalID, ActionID: target, Capability: s.autoPending.Capability}, expiresAt: time.Now().UTC().Add(time.Minute), state: "pending", visible: true}
	} else {
		http.Error(w, "fixture mismatch", http.StatusConflict)
		return
	}
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(messageReceipt{TurnID: turnID, State: "queued"})
}

func (s *authorityLiveState) streamEvents(w http.ResponseWriter, r *http.Request) {
	localSessionID := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/charlie/sessions/"), "/events/")
	stimulus, exists := s.sessions[localSessionID]
	if !exists || stimulus.ClientSessionID != s.autoAction.Stimulus.ClientSessionID || !s.autoTriggered {
		http.Error(w, "stream unavailable", http.StatusConflict)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	write := func(sequence string, eventName string, event streamedActionEvent) {
		encoded, _ := json.Marshal(event)
		_, _ = fmt.Fprintf(w, "id: %s\nevent: %s\ndata: %s\n\n", sequence, eventName, encoded)
	}
	exact := streamedActionEvent{TurnID: "turn-auto-success", ActionID: "action-auto-generated", Type: "permission.requested"}
	exact.Data.Data.Capability = s.autoAction.Capability
	switch s.streamVariant {
	case "unrelated_turn":
		unrelated := exact
		unrelated.TurnID, unrelated.ActionID = "turn-unrelated", "action-unrelated"
		write("1", unrelated.Type, unrelated)
	case "multiple_actions":
		write("1", exact.Type, exact)
		second := exact
		second.ActionID = "action-auto-second"
		write("2", second.Type, second)
	case "conflicting_type":
		write("1", "tool.proposed", exact)
	case "invalid_event_id":
		write(strings.Repeat("x", 129), exact.Type, exact)
	default:
		write("1", exact.Type, exact)
	}
	terminal := streamedActionEvent{TurnID: "turn-auto-success", Type: "turn.completed"}
	write("3", terminal.Type, terminal)
}

func (s *authorityLiveState) abortSession(w http.ResponseWriter, r *http.Request) {
	localSessionID := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/charlie/sessions/"), "/abort/")
	stimulus, exists := s.sessions[localSessionID]
	var body struct {
		RequestID string `json:"request_id"`
	}
	if !exists || json.NewDecoder(r.Body).Decode(&body) != nil || body.RequestID != stimulus.AbortRequestID || s.failAbort {
		http.Error(w, "abort failed", http.StatusConflict)
		return
	}
	delete(s.sessions, localSessionID)
	w.WriteHeader(http.StatusNoContent)
}

func (s *authorityLiveState) decide(w http.ResponseWriter, r *http.Request, id string) {
	approval := s.approvals[id]
	if approval == nil {
		http.Error(w, "not found", http.StatusForbidden)
		return
	}
	var body struct {
		RequestID string `json:"request_id"`
		Decision  string `json:"decision"`
		Rationale string `json:"rationale"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil {
		http.Error(w, "invalid", http.StatusBadRequest)
		return
	}
	if !approval.expiresAt.After(time.Now().UTC()) {
		approval.state = "expired"
		http.Error(w, "expired", http.StatusConflict)
		return
	}
	if approval.state != "pending" {
		if approval.requestID == body.RequestID && approval.decision == body.Decision && approval.rationale == body.Rationale {
			s.writeDecision(w, approval)
			return
		}
		http.Error(w, "already decided", http.StatusConflict)
		return
	}
	approval.requestID, approval.decision, approval.rationale = body.RequestID, body.Decision, body.Rationale
	if body.Decision == "reject" {
		approval.state = "rejected"
		s.writeDecision(w, approval)
		return
	}
	if body.Decision != "approve" || approval.fixture.ActionID == "" {
		http.Error(w, "invalid", http.StatusBadRequest)
		return
	}
	approval.state = "approved"
	s.runtime["tool_calls"]++
	s.operations[approval.fixture.ActionID] = qualificationOperation{actionID: approval.fixture.ActionID, capability: approval.fixture.Capability}
	s.writeDecision(w, approval)
}

func (s *authorityLiveState) writeDecision(w http.ResponseWriter, approval *qualificationApproval) {
	state := approval.state
	if state == "rejected" {
		state = "denied"
	}
	payload := map[string]any{"approval": approvalProof{ID: approval.fixture.ApprovalID, State: state, Capability: approval.fixture.Capability, ExpiresAt: approval.expiresAt}}
	if s.unwrapDecision {
		_ = json.NewEncoder(w).Encode(payload)
	} else {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": payload})
	}
}
