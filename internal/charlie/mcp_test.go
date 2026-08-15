package charlie

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

const testMCPClientURI = "spiffe://astronomer.local/installations/installation-a/charlie-agent-mcp"

func testMCPHandler(t *testing.T, facts AuthorityInput) (*MCPHandler, *fakeCapabilityExecutor, ed25519.PrivateKey) {
	t.Helper()
	authority := &fakeLiveAuthority{facts: []AuthorityInput{facts, facts}}
	receipts := &fakeReceipts{}
	executor := &fakeCapabilityExecutor{verified: true}
	guard, privateKey := newTestActionGuard(t, authority, receipts, executor)
	handler, err := NewMCPHandler(guard, func(context.Context) bool { return true }, func(context.Context) bool { return true }, testMCPClientURI)
	if err != nil {
		t.Fatal(err)
	}
	return handler, executor, privateKey
}

func authenticatedMCPRequest(t *testing.T, body string) *http.Request {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "https://astronomer-charlie-mcp.astronomer.svc:7444/mcp", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	identity, err := url.Parse(testMCPClientURI)
	if err != nil {
		t.Fatal(err)
	}
	leaf := &x509.Certificate{Raw: []byte{1, 2, 3}, URIs: []*url.URL{identity}, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
	request.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{leaf}, VerifiedChains: [][]*x509.Certificate{{leaf}}}
	return request
}

func TestMCPInactiveRejectsInitializeDiscoveryAndCalls(t *testing.T) {
	facts := allowedWriteFacts(ModeAuto)
	handler, executor, _ := testMCPHandler(t, facts)
	handler.discoverable = func(context.Context) bool { return false }
	handler.active = func(context.Context) bool { return false }
	for _, method := range []string{"initialize", "notifications/initialized", "tools/list", "tools/call"} {
		t.Run(method, func(t *testing.T) {
			id := `,"id":"one"`
			if method == "notifications/initialized" {
				id = ""
			}
			request := authenticatedMCPRequest(t, `{"jsonrpc":"2.0"`+id+`,"method":"`+method+`","params":{}}`)
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusServiceUnavailable || executor.calls != 0 || !strings.Contains(recorder.Body.String(), "integration_inactive") {
				t.Fatalf("inactive MCP operation escaped: method=%s status=%d calls=%d body=%s", method, recorder.Code, executor.calls, recorder.Body.String())
			}
		})
	}
}

func TestMCPConfigurationOnlyAllowsDiscoveryAndRejectsCalls(t *testing.T) {
	facts := allowedWriteFacts(ModeAuto)
	handler, executor, _ := testMCPHandler(t, facts)
	handler.discoverable = func(context.Context) bool { return true }
	handler.active = func(context.Context) bool { return false }

	for _, requestBody := range []string{
		`{"jsonrpc":"2.0","id":"initialize","method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}`,
		`{"jsonrpc":"2.0","id":"tools","method":"tools/list","params":{}}`,
	} {
		request := authenticatedMCPRequest(t, requestBody)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK && recorder.Code != http.StatusAccepted {
			t.Fatalf("configuration discovery failed closed unexpectedly: status=%d body=%s", recorder.Code, recorder.Body.String())
		}
	}

	request := authenticatedMCPRequest(t, `{"jsonrpc":"2.0","id":"call","method":"tools/call","params":{}}`)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable || executor.calls != 0 || !strings.Contains(recorder.Body.String(), "integration_inactive") {
		t.Fatalf("configuration-only MCP admitted work: status=%d calls=%d body=%s", recorder.Code, executor.calls, recorder.Body.String())
	}
}

func TestMCPRejectsMissingIdentityCookiesAndAPITokens(t *testing.T) {
	facts := allowedWriteFacts(ModeAuto)
	handler, _, _ := testMCPHandler(t, facts)
	body := `{"jsonrpc":"2.0","id":"one","method":"tools/list","params":{}}`
	for name, mutate := range map[string]func(*http.Request){
		"missing_tls": func(request *http.Request) { request.TLS = nil },
		"cookie":      func(request *http.Request) { request.Header.Set("Cookie", "session=secret") },
		"api_token":   func(request *http.Request) { request.Header.Set("Authorization", "Bearer secret") },
	} {
		t.Run(name, func(t *testing.T) {
			request := authenticatedMCPRequest(t, body)
			mutate(request)
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusUnauthorized {
				t.Fatalf("credential accepted: %d %s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestMCPToolsExposeOnlyBoundedManagementCapabilities(t *testing.T) {
	facts := allowedWriteFacts(ModeAuto)
	handler, _, _ := testMCPHandler(t, facts)
	request := authenticatedMCPRequest(t, `{"jsonrpc":"2.0","id":"one","method":"tools/list","params":{}}`)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("tools/list failed: %d %s", recorder.Code, recorder.Body.String())
	}
	body := recorder.Body.String()
	for _, prohibited := range []string{"downstream", "pods.delete", "exec", "shell", `"destructiveHint":true`, `"managed_target_access":true`} {
		if strings.Contains(body, prohibited) {
			t.Fatalf("catalog disclosed prohibited capability %q", prohibited)
		}
	}
	for _, required := range []string{"astronomer.agent_fleet.summary", "astronomer.tunnel.health", "astronomer.queue.retry_task"} {
		if !strings.Contains(body, required) {
			t.Fatalf("catalog omitted %q", required)
		}
	}
	if !strings.Contains(body, `"charlie/capability"`) || !strings.Contains(body, `"schema":"charlie.mcp-capability/v1"`) || !strings.Contains(body, `"post_verification"`) {
		t.Fatalf("catalog omitted Charlie write safety disclosure: %s", body)
	}
}

func TestMCPReadOnlyWriteReturnsActionableDenialWithoutExecuting(t *testing.T) {
	facts := allowedWriteFacts(ModeReadOnly)
	handler, executor, privateKey := testMCPHandler(t, facts)
	action := signedTestAction(t, privateKey, "astronomer.queue.retry_task", map[string]any{"resource_id": "resource-a", "task_id": "task-a", "operation_id": "action-a"})
	params := map[string]any{
		"name":      "astronomer.queue.retry_task",
		"arguments": map[string]any{"resource_id": "resource-a", "task_id": "task-a", "operation_id": "action-a"},
		"_meta":     map[string]any{"charlie/action": action},
	}
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": "call-a", "method": "tools/call", "params": params})
	request := authenticatedMCPRequest(t, string(body))
	request.Header.Set("Idempotency-Key", "action-a")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden || executor.calls != 0 || !strings.Contains(recorder.Body.String(), string(DeniedReadOnlyWrite)) {
		t.Fatalf("read-only MCP boundary failed: status=%d calls=%d body=%s", recorder.Code, executor.calls, recorder.Body.String())
	}
}

func TestMCPArgumentsCannotDifferFromSignedEnvelope(t *testing.T) {
	facts := allowedWriteFacts(ModeAuto)
	handler, executor, privateKey := testMCPHandler(t, facts)
	action := signedTestAction(t, privateKey, "astronomer.queue.retry_task", map[string]any{"resource_id": "resource-a", "task_id": "task-a", "operation_id": "action-a"})
	params := map[string]any{
		"name":      "astronomer.queue.retry_task",
		"arguments": map[string]any{"resource_id": "resource-a", "task_id": "tampered", "operation_id": "action-a"},
		"_meta":     map[string]any{"charlie/action": action},
	}
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": "call-a", "method": "tools/call", "params": params})
	recorder := httptest.NewRecorder()
	request := authenticatedMCPRequest(t, string(body))
	request.Header.Set("Idempotency-Key", "action-a")
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden || executor.calls != 0 {
		t.Fatalf("tampered arguments executed: %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestMCPRejectsCallerSuppliedDescriptorSafetyFields(t *testing.T) {
	facts := allowedWriteFacts(ModeAuto)
	handler, executor, privateKey := testMCPHandler(t, facts)
	action := signedTestAction(t, privateKey, "astronomer.queue.retry_task", map[string]any{"resource_id": "resource-a", "task_id": "task-a", "operation_id": "action-a"})
	encodedAction, err := json.Marshal(action)
	if err != nil {
		t.Fatal(err)
	}
	var actionFields map[string]any
	if err := json.Unmarshal(encodedAction, &actionFields); err != nil {
		t.Fatal(err)
	}
	for field, value := range map[string]any{
		"effect": "read", "risk": "low", "rollback": "fully_reversible", "requires_verification": true,
		"idempotent": true, "auto_eligible": true, "approval": "approved", "destructive": false,
	} {
		t.Run(field, func(t *testing.T) {
			crafted := make(map[string]any, len(actionFields)+1)
			for key, item := range actionFields {
				crafted[key] = item
			}
			crafted[field] = value
			body, _ := json.Marshal(map[string]any{
				"jsonrpc": "2.0", "id": "call-a", "method": "tools/call",
				"params": map[string]any{
					"name":      "astronomer.queue.retry_task",
					"arguments": map[string]any{"resource_id": "resource-a", "task_id": "task-a", "operation_id": "action-a"},
					"_meta":     map[string]any{"charlie/action": crafted},
				},
			})
			request := authenticatedMCPRequest(t, string(body))
			request.Header.Set("Idempotency-Key", "action-a")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest || executor.calls != 0 {
				t.Fatalf("caller-supplied %s reached action guard: status=%d calls=%d body=%s", field, response.Code, executor.calls, response.Body.String())
			}
		})
	}
}

func TestMCPRequiresHTTPIdempotencyKeyBoundToSignedAction(t *testing.T) {
	facts := allowedWriteFacts(ModeAuto)
	handler, executor, privateKey := testMCPHandler(t, facts)
	action := signedTestAction(t, privateKey, "astronomer.queue.retry_task", map[string]any{"resource_id": "resource-a", "task_id": "task-a", "operation_id": "action-a"})
	params := map[string]any{
		"name":      action.Capability,
		"arguments": map[string]any{"resource_id": "resource-a", "task_id": "task-a", "operation_id": "action-a"},
		"_meta":     map[string]any{"charlie/action": action},
	}
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": "call-a", "method": "tools/call", "params": params})
	for _, header := range []string{"", "different"} {
		request := authenticatedMCPRequest(t, string(body))
		request.Header.Set("Idempotency-Key", header)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusBadRequest || executor.calls != 0 {
			t.Fatalf("unbound HTTP idempotency key executed: status=%d calls=%d", recorder.Code, executor.calls)
		}
	}
}

func TestMCPRequestBodyIsBounded(t *testing.T) {
	facts := allowedWriteFacts(ModeAuto)
	handler, _, _ := testMCPHandler(t, facts)
	body := bytes.Repeat([]byte("x"), maxMCPRequestBytes+1)
	request := authenticatedMCPRequest(t, string(body))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("oversized body accepted: %d", recorder.Code)
	}
}

func TestMCPUnknownMethodIsContentFree(t *testing.T) {
	facts := allowedWriteFacts(ModeAuto)
	handler, _, _ := testMCPHandler(t, facts)
	request := authenticatedMCPRequest(t, `{"jsonrpc":"2.0","id":"one","method":"secret/raw","params":{"token":"do-not-log"}}`)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	response, _ := io.ReadAll(recorder.Result().Body)
	if recorder.Code != http.StatusNotFound || strings.Contains(string(response), "do-not-log") {
		t.Fatalf("unknown method response unsafe: %s", response)
	}
}

func TestMCPInitializeNotificationUsesNotificationSemantics(t *testing.T) {
	facts := allowedWriteFacts(ModeAuto)
	handler, _, _ := testMCPHandler(t, facts)
	request := authenticatedMCPRequest(t, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusAccepted || recorder.Body.Len() != 0 {
		t.Fatalf("initialized notification returned a JSON-RPC response: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestMCPToolsCallContentIncludesSucceededPayload(t *testing.T) {
	payload := json.RawMessage(`{"kubernetes_version":"v1.36.2+k3s1","server_version":"v1.36.2+k3s1"}`)
	facts := allowedReadFacts()
	authority := &fakeLiveAuthority{facts: []AuthorityInput{facts, facts}}
	executor := &fakeCapabilityExecutor{result: payload, verified: true}
	guard, privateKey := newTestActionGuard(t, authority, &fakeReceipts{}, executor)
	handler, err := NewMCPHandler(guard, func(context.Context) bool { return true }, func(context.Context) bool { return true }, testMCPClientURI)
	if err != nil {
		t.Fatal(err)
	}
	action := signedTestAction(t, privateKey, "astronomer.installation.summary", map[string]any{})
	params := map[string]any{
		"name":      "astronomer.installation.summary",
		"arguments": map[string]any{},
		"_meta":     map[string]any{"charlie/action": action},
	}
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": "call-read", "method": "tools/call", "params": params})
	request := authenticatedMCPRequest(t, string(body))
	request.Header.Set("Idempotency-Key", action.ActionID)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("tools/call status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	responseBody := recorder.Body.String()
	if !strings.Contains(responseBody, "v1.36.2+k3s1") {
		t.Fatalf("model-visible content omitted adapter payload: %s", responseBody)
	}
	if strings.Contains(responseBody, `"text":"Astronomer completed and verified the bounded action."`) {
		t.Fatalf("tools/call still returned only the lifecycle summary: %s", responseBody)
	}
}

func TestBoundedActionContentPrefersPayload(t *testing.T) {
	got := boundedActionContent(ActionResult{State: "succeeded", Result: json.RawMessage(`{"kubernetes_version":"v1.36.2+k3s1"}`)})
	if got != `{"kubernetes_version":"v1.36.2+k3s1"}` {
		t.Fatalf("boundedActionContent = %q", got)
	}
	if summary := boundedActionContent(ActionResult{State: "succeeded"}); summary != "Astronomer completed and verified the bounded action." {
		t.Fatalf("empty success summary = %q", summary)
	}
}
