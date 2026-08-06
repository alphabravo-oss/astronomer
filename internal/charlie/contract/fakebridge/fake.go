// Package fakebridge provides deterministic Product Bridge contract behavior.
package fakebridge

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

const fixedTime = "2026-08-05T00:00:00Z"

type event struct {
	ID   string
	Data string
}

// Fake is a stateful, content-free Product Bridge test double.
type Fake struct {
	mu                sync.Mutex
	productEnabled    bool
	deploymentEnabled bool
	centralHealthy    bool
	mode              string
	integrationRev    int
	epoch             int
	leader            string
	sessions          map[string]map[string]any
	events            map[string][]event
	runtimeCalls      int
}

// New returns a deterministic, enabled read-only bridge.
func New() *Fake {
	return &Fake{
		productEnabled:    true,
		deploymentEnabled: true,
		centralHealthy:    true,
		mode:              "read_only",
		integrationRev:    1,
		epoch:             1,
		leader:            "instance-a",
		sessions:          map[string]map[string]any{},
		events:            map[string][]event{},
	}
}

func (f *Fake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if !strings.HasPrefix(r.URL.Path, "/bridge/v1") {
		http.NotFound(w, r)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/bridge/v1")
	if path == "/health" && r.Method == http.MethodGet {
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
		return
	}
	if path == "/status" && r.Method == http.MethodGet {
		f.writeStatus(w)
		return
	}
	if path == "/activation" && r.Method == http.MethodPut {
		var request struct {
			ProductEnabled   bool   `json:"product_enabled"`
			ExpectedRevision string `json:"expected_revision"`
		}
		if !decodeJSON(w, r, &request) {
			return
		}
		f.productEnabled = request.ProductEnabled
		f.integrationRev++
		f.writeStatus(w)
		return
	}
	if path == "/mode" {
		if r.Method == http.MethodPut {
			var request struct {
				Mode             string `json:"mode"`
				ExpectedRevision string `json:"expected_revision"`
			}
			if !decodeJSON(w, r, &request) {
				return
			}
			if request.Mode != "read_only" && request.Mode != "approval" && request.Mode != "auto" {
				writeError(w, http.StatusConflict, "conflict", false)
				return
			}
			f.mode = request.Mode
			f.integrationRev++
		}
		if r.Method == http.MethodGet || r.Method == http.MethodPut {
			writeJSON(w, http.StatusOK, map[string]any{"mode": f.mode, "integration_revision": f.revision()})
			return
		}
	}

	if !f.productEnabled || !f.deploymentEnabled {
		writeError(w, http.StatusServiceUnavailable, "unavailable", true)
		return
	}
	if !f.centralHealthy {
		writeError(w, http.StatusServiceUnavailable, "central_timeout", true)
		return
	}

	switch {
	case path == "/sessions" && r.Method == http.MethodGet:
		list := make([]map[string]any, 0, len(f.sessions))
		for i := 1; i <= len(f.sessions); i++ {
			list = append(list, f.sessions[fmt.Sprintf("session-%04d", i)])
		}
		f.accept(writeJSON, w, http.StatusOK, list)
	case path == "/sessions" && r.Method == http.MethodPost:
		id := fmt.Sprintf("session-%04d", len(f.sessions)+1)
		session := map[string]any{"session_id": id, "state": "open", "created_at": fixedTime, "expires_at": "2026-08-05T01:00:00Z"}
		f.sessions[id] = session
		f.accept(writeJSON, w, http.StatusCreated, session)
	case strings.HasPrefix(path, "/sessions/"):
		f.serveSession(w, r, path)
	case path == "/approvals" && r.Method == http.MethodGet:
		f.accept(writeJSON, w, http.StatusOK, []map[string]any{{"approval_id": "approval-0001", "action_id": "action-0001", "state": "pending", "expires_at": "2026-08-05T01:00:00Z"}})
	case strings.HasPrefix(path, "/approvals/") && strings.HasSuffix(path, "/decision") && r.Method == http.MethodPost:
		f.accept(writeJSON, w, http.StatusOK, map[string]any{"approval_id": "approval-0001", "action_id": "action-0001", "state": "approved", "expires_at": "2026-08-05T01:00:00Z"})
	case path == "/investigations" && r.Method == http.MethodPost:
		f.accept(writeJSON, w, http.StatusAccepted, map[string]any{"session_id": "session-investigation", "revision": 1, "turn_id": "turn-investigation", "accepted_at": fixedTime})
	case path == "/findings" && r.Method == http.MethodGet:
		f.accept(writeJSON, w, http.StatusOK, []map[string]any{findingSummary()})
	case path == "/findings/finding-0001" && r.Method == http.MethodGet:
		f.accept(writeJSON, w, http.StatusOK, findingEnvelope())
	case path == "/findings/finding-0001/transitions" && r.Method == http.MethodPost:
		f.accept(writeJSON, w, http.StatusOK, findingSummary())
	default:
		http.NotFound(w, r)
	}
}

func (f *Fake) serveSession(w http.ResponseWriter, r *http.Request, path string) {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 2 {
		http.NotFound(w, r)
		return
	}
	id := parts[1]
	session, exists := f.sessions[id]
	if !exists {
		http.NotFound(w, r)
		return
	}
	if len(parts) == 2 && r.Method == http.MethodGet {
		f.accept(writeJSON, w, http.StatusOK, session)
		return
	}
	if len(parts) != 3 {
		http.NotFound(w, r)
		return
	}
	switch parts[2] {
	case "messages":
		if r.Method != http.MethodPost {
			break
		}
		turn := fmt.Sprintf("turn-%04d", len(f.events[id])+1)
		eventID := fmt.Sprintf("event-%04d", len(f.events[id])+1)
		f.events[id] = append(f.events[id], event{ID: eventID, Data: `{"type":"turn.completed","turn_id":"` + turn + `"}`})
		f.accept(writeJSON, w, http.StatusAccepted, map[string]any{"session_id": id, "turn_id": turn, "accepted_at": fixedTime})
		return
	case "events":
		if r.Method != http.MethodGet {
			break
		}
		after := r.Header.Get("Last-Event-ID")
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		for _, item := range f.events[id] {
			if after != "" && item.ID <= after {
				continue
			}
			_, _ = fmt.Fprintf(w, "id: %s\ndata: %s\n\n", item.ID, item.Data)
		}
		f.runtimeCalls++
		return
	case "history":
		if r.Method == http.MethodGet {
			f.accept(writeJSON, w, http.StatusOK, map[string]any{"data": []any{}})
			return
		}
	case "actions":
		if r.Method == http.MethodGet {
			f.accept(writeJSON, w, http.StatusOK, []any{})
			return
		}
	case "abort":
		if r.Method == http.MethodPost {
			session["state"] = "aborted"
			f.runtimeCalls++
			w.WriteHeader(http.StatusAccepted)
			return
		}
	}
	http.NotFound(w, r)
}

func (f *Fake) accept(writer func(http.ResponseWriter, int, any), w http.ResponseWriter, status int, value any) {
	f.runtimeCalls++
	writer(w, status, value)
}

func (f *Fake) writeStatus(w http.ResponseWriter) {
	centralHealth := "healthy"
	if !f.centralHealthy {
		centralHealth = "unavailable"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"central_health": centralHealth, "logical_agent_id": "agent-0001",
		"instance_id": f.leader, "leader_instance_id": f.leader, "epoch": f.epoch,
		"lease_expires_at": "2026-08-05T00:01:00Z", "integration_revision": f.revision(),
		"disclosure_digest": "sha256:" + strings.Repeat("a", 64), "effective_mode": f.mode,
		"route_id": "route-0001", "artifact_version": "1.0.0",
		"product_enabled": f.productEnabled, "deployment_enabled": f.deploymentEnabled,
		"effective_enabled": f.productEnabled && f.deploymentEnabled,
	})
}

func (f *Fake) revision() string { return "revision-" + strconv.Itoa(f.integrationRev) }

// SetProductEnabled simulates the product-local enablement authority.
func (f *Fake) SetProductEnabled(enabled bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.productEnabled = enabled
}

// SetDeploymentEnabled simulates Charlie's deployment enablement authority.
func (f *Fake) SetDeploymentEnabled(enabled bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deploymentEnabled = enabled
}

// SetCentralHealthy simulates central health without changing local status access.
func (f *Fake) SetCentralHealthy(healthy bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.centralHealthy = healthy
}

// Failover changes the leader and fencing epoch without losing replay state.
func (f *Fake) Failover() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.epoch++
	if f.leader == "instance-a" {
		f.leader = "instance-b"
	} else {
		f.leader = "instance-a"
	}
}

// RuntimeCalls returns accepted runtime operations; denied calls do not count.
func (f *Fake) RuntimeCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.runtimeCalls
}

func findingSummary() map[string]any {
	return map[string]any{
		"finding_id": "finding-0001", "session_id": "session-0001", "investigation_id": "investigation-0001",
		"deduplication_key": "00000000000000000000000000000001", "repeat_count": 1,
		"severity": "medium", "status": "open", "workflow_state": "approval_pending",
		"block_code": "approval_required", "updated_at": fixedTime,
	}
}

func findingEnvelope() map[string]any {
	return map[string]any{
		"schema": "charlie.finding/v1",
		"finding": map[string]any{
			"finding_id": "finding-0001", "deduplication_key": "00000000000000000000000000000001",
			"severity": "medium", "status": "open",
			"workflow":           map[string]any{"state": "approval_pending", "approval_id": "approval-0001"},
			"affected_resources": []string{"resource-0001"},
			"evidence_summary":   []string{}, "diagnosis": "Deterministic fixture diagnosis.", "confidence": 1,
			"block_code": "approval_required", "risk_impact": "No action executed.",
			"operator_checks": []string{"Review the fixture resource."}, "verification_steps": []string{"Re-read fixture state."},
			"investigation_id": "investigation-0001", "session_id": "session-0001", "created_at": fixedTime, "updated_at": fixedTime,
		},
		"lifecycle": []map[string]any{{"event_id": "lifecycle-0001", "transition": "created", "workflow_state": "approval_pending", "actor_ref": "agent-0001", "occurred_at": fixedTime}},
		"storage":   map[string]any{"encryption_required": true, "retention_days": 1, "expires_at": "2026-08-06T00:00:00Z"},
	}
}

func decodeJSON(w http.ResponseWriter, r *http.Request, value any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 262144))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		writeError(w, http.StatusConflict, "conflict", false)
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code string, retryable bool) {
	writeJSON(w, status, map[string]any{"code": code, "message": "deterministic fake bridge denial", "request_id": "request-0001", "retryable": retryable})
}

var _ http.Handler = (*Fake)(nil)
