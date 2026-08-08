package charliequalification

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

type liveLeaderState struct {
	mu        sync.Mutex
	mode      string
	revision  int64
	leader    string
	epoch     int64
	toolCalls uint64
	claims    uint64
	events    chan string
}

type fakeLeaderTarget struct {
	state       *liveLeaderState
	deleteCalls int
	waitCalls   int
	observedUID string
	waitErr     error
}

func (t *fakeLeaderTarget) Snapshot(context.Context, int) (string, int, error) {
	return "pod-leader-old", 2, nil
}
func (t *fakeLeaderTarget) DeleteAndWaitReplacement(_ context.Context, ordinal int, uid string) (time.Time, error) {
	if ordinal != 0 || uid != "pod-leader-old" {
		return time.Time{}, fmt.Errorf("unexpected deletion target")
	}
	t.deleteCalls++
	t.observedUID = uid
	t.state.mu.Lock()
	t.state.leader, t.state.epoch = "leader-b", 2
	t.state.mu.Unlock()
	return time.Now().UTC(), nil
}
func (t *fakeLeaderTarget) WaitReady(context.Context, int) error { t.waitCalls++; return t.waitErr }

func TestLiveLeaderFailoverUsesFixedTargetAndRestoresReadOnly(t *testing.T) {
	state := &liveLeaderState{mode: "read_only", revision: 11, leader: "leader-a", epoch: 1, events: make(chan string, 1)}
	server := httptest.NewTLSServer(http.HandlerFunc(state.serveHTTP))
	defer server.Close()
	target := &fakeLeaderTarget{state: state}
	candidate := qualificationCandidate()
	stimulus := SessionStimulus{
		ClientSessionID: "10000000-0000-4000-8000-000000000005", ClientMessageID: "20000000-0000-4000-8000-000000000005", AbortRequestID: "40000000-0000-4000-8000-000000000005",
		Intent: "qualification_leader_failover", ResourceType: "management_component", ResourceID: "qualification-task-failover", Message: "Run the exact safe failover action.",
	}
	driver, err := NewLiveDriver(LiveConfig{
		AstronomerURL: server.URL, AdminToken: "admin", ApproverToken: "approver", HTTPClient: server.Client(),
		MetricSources: []MetricSource{{URL: server.URL + "/metrics"}}, Fixtures: LiveFixtures{LeaderKillFailover: ActionFixture{Capability: "astronomer.queue.retry_task", Stimulus: stimulus}},
		ProofPoll: time.Millisecond, ProofTimeout: 2 * time.Second, NoCallDwell: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	driver.leaderFailover = target
	result := driver.Run(t.Context(), ScenarioRequest{Scenario: "leader_kill_failover", Candidate: candidate})
	if !result.Passed || len(result.Assertions) != 7 {
		t.Fatalf("leader failover did not pass: %#v", result)
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.mode != "read_only" || state.toolCalls != 1 || target.deleteCalls != 1 || target.waitCalls != 1 || target.observedUID != "pod-leader-old" {
		t.Fatalf("unsafe final state: mode=%s calls=%d deletes=%d waits=%d uid=%s", state.mode, state.toolCalls, target.deleteCalls, target.waitCalls, target.observedUID)
	}
}

func TestLiveLeaderFailoverFailsWhenReadinessRestorationFails(t *testing.T) {
	state := &liveLeaderState{mode: "read_only", revision: 11, leader: "leader-a", epoch: 1, events: make(chan string, 1)}
	server := httptest.NewTLSServer(http.HandlerFunc(state.serveHTTP))
	defer server.Close()
	target := &fakeLeaderTarget{state: state, waitErr: fmt.Errorf("readiness unavailable")}
	stimulus := SessionStimulus{
		ClientSessionID: "10000000-0000-4000-8000-000000000005", ClientMessageID: "20000000-0000-4000-8000-000000000005", AbortRequestID: "40000000-0000-4000-8000-000000000005",
		Intent: "qualification_leader_failover", ResourceType: "management_component", ResourceID: "qualification-task-failover", Message: "Run the exact safe failover action.",
	}
	driver, err := NewLiveDriver(LiveConfig{
		AstronomerURL: server.URL, AdminToken: "admin", ApproverToken: "approver", HTTPClient: server.Client(),
		MetricSources: []MetricSource{{URL: server.URL + "/metrics"}}, Fixtures: LiveFixtures{LeaderKillFailover: ActionFixture{Capability: "astronomer.queue.retry_task", Stimulus: stimulus}},
		ProofPoll: time.Millisecond, ProofTimeout: 2 * time.Second, NoCallDwell: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	driver.leaderFailover = target
	result := driver.Run(t.Context(), ScenarioRequest{Scenario: "leader_kill_failover", Candidate: qualificationCandidate()})
	if result.Passed || target.waitCalls != 1 {
		t.Fatalf("failed restoration passed: result=%#v waits=%d", result, target.waitCalls)
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.mode != "read_only" {
		t.Fatalf("authority was not reduced during failed restoration: %s", state.mode)
	}
}

func (s *liveLeaderState) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/metrics" {
		s.mu.Lock()
		defer s.mu.Unlock()
		w.Header().Set("Content-Type", "text/plain")
		for _, key := range runtimeKeys {
			value := uint64(0)
			if key == "tool_calls" {
				value = s.toolCalls
			}
			if key == "work_claims" {
				value = s.claims
			}
			_, _ = fmt.Fprintf(w, "%s %d\n", defaultCounterMetrics()[key], value)
		}
		_, _ = fmt.Fprintln(w, `astronomer_charlie_downstream_boundary_calls_total{entrypoint="tunnel_message",operation="other"} 0`)
		_, _ = fmt.Fprintln(w, `astronomer_charlie_downstream_boundary_calls_total{entrypoint="kubernetes_proxy",operation="other"} 0`)
		for _, operation := range []string{"kubernetes", "exec", "logs", "helm"} {
			_, _ = fmt.Fprintf(w, "astronomer_charlie_downstream_boundary_calls_total{entrypoint=%q,operation=%q} 0\n", "other", operation)
		}
		return
	}
	if r.Header.Get("Authorization") == "Bearer admin" {
		s.serveAdmin(w, r)
		return
	}
	if r.Header.Get("Authorization") == "Bearer approver" {
		s.serveApprover(w, r)
		return
	}
	http.Error(w, "unauthorized", http.StatusUnauthorized)
}

func (s *liveLeaderState) serveAdmin(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/admin/charlie/status/":
		ordinal := 0
		if s.leader == "leader-b" {
			ordinal = 1
		}
		_, _ = fmt.Fprintf(w, `{"data":{"connection":{"connected":true,"central_version":"1.2.3","disclosure_digest":%q,"disclosure_acknowledged":true},"mode":{"requested":%q,"authoritative":%q,"revision":%d,"emergency_disabled":false,"disclosure_digest":%q},"agent":{"desired_replicas":2,"ready_replicas":2,"leader_replica":%q,"fencing_epoch":%d,"agent_version":"1.2.3","chart_digest":%q,"image_digest":%q,"replicas":[{"ordinal":%d,"instance_id":%q,"role":"leader","state":"ready"}]}}}`, readOnlyDisclosureDigest, s.mode, s.mode, s.revision, readOnlyDisclosureDigest, s.leader, s.epoch, digestOf('4'), digestOf('2'), ordinal, s.leader)
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
			if body.AcknowledgeDisclosureDigest != readOnlyDisclosureDigest {
				http.Error(w, "conflict", http.StatusConflict)
				return
			}
		} else {
			if body.Revision == nil || *body.Revision != s.revision || (body.Mode != "auto" && body.Mode != "read_only") {
				http.Error(w, "conflict", http.StatusConflict)
				return
			}
			s.mode = body.Mode
			s.revision++
		}
		_, _ = fmt.Fprintf(w, `{"data":{"requested":%q,"authoritative":%q,"revision":%d,"emergency_disabled":false}}`, s.mode, s.mode, s.revision)
	default:
		http.NotFound(w, r)
	}
}

func (s *liveLeaderState) serveApprover(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/charlie/sessions/":
		w.WriteHeader(http.StatusCreated)
		_, _ = fmt.Fprint(w, `{"data":{"session":{"id":"50000000-0000-4000-8000-000000000005"},"replayed":false}}`)
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/charlie/sessions/50000000-0000-4000-8000-000000000005/events/":
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, ": connected\n\n")
		w.(http.Flusher).Flush()
		select {
		case frames := <-s.events:
			_, _ = fmt.Fprint(w, frames)
			w.(http.Flusher).Flush()
		case <-r.Context().Done():
		}
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/charlie/sessions/50000000-0000-4000-8000-000000000005/messages/":
		s.mu.Lock()
		s.toolCalls++
		s.claims++
		s.mu.Unlock()
		s.events <- "id: event-after-1\nevent: action.proposed\ndata: {\"turn_id\":\"turn-failover\",\"action_id\":\"action-failover\",\"type\":\"action.proposed\",\"data\":{\"data\":{\"capability\":\"astronomer.queue.retry_task\"}}}\n\nid: event-after-2\nevent: turn.completed\ndata: {\"turn_id\":\"turn-failover\",\"type\":\"turn.completed\"}\n\n"
		w.WriteHeader(http.StatusAccepted)
		_, _ = fmt.Fprintf(w, `{"session_id":"central-session","turn_id":"turn-failover","accepted_at":%q}`, time.Now().UTC().Format(time.RFC3339Nano))
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/charlie/operations/action-failover":
		_, _ = fmt.Fprint(w, `{"data":{"operation_id":"action-failover","capability":"astronomer.queue.retry_task","state":"succeeded","result_status":"succeeded"}}`)
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/charlie/sessions/50000000-0000-4000-8000-000000000005/abort/":
		w.WriteHeader(http.StatusNoContent)
	default:
		http.NotFound(w, r)
	}
}
