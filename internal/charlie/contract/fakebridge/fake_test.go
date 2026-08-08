package fakebridge

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func request(t *testing.T, client *http.Client, method, url string, body string, headers map[string]string) (int, []byte) {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, raw
}

func TestFakeBridgeHealthSessionsAndSSEReplay(t *testing.T) {
	fake := New()
	server := httptest.NewServer(fake)
	defer server.Close()

	status, _ := request(t, server.Client(), http.MethodGet, server.URL+"/bridge/v1/health", "", nil)
	if status != http.StatusOK {
		t.Fatalf("health status = %d", status)
	}
	status, sessionRaw := request(t, server.Client(), http.MethodPost, server.URL+"/bridge/v1/sessions", `{}`, nil)
	if status != http.StatusCreated || !bytes.Contains(sessionRaw, []byte(`"session_id":"session-0001"`)) {
		t.Fatalf("create session status=%d body=%s", status, sessionRaw)
	}
	status, _ = request(t, server.Client(), http.MethodPost, server.URL+"/bridge/v1/sessions/session-0001/messages", `{}`, nil)
	if status != http.StatusAccepted {
		t.Fatalf("message status = %d", status)
	}
	_, firstReplay := request(t, server.Client(), http.MethodGet, server.URL+"/bridge/v1/sessions/session-0001/events", "", nil)
	if !bytes.Contains(firstReplay, []byte("id: event-0001")) {
		t.Fatalf("first replay = %s", firstReplay)
	}
	_, emptyReplay := request(t, server.Client(), http.MethodGet, server.URL+"/bridge/v1/sessions/session-0001/events", "", map[string]string{"Last-Event-ID": "event-0001"})
	if len(bytes.TrimSpace(emptyReplay)) != 0 {
		t.Fatalf("replayed acknowledged event: %s", emptyReplay)
	}
}

func TestFakeBridgeModesApprovalsAndFindings(t *testing.T) {
	fake := New()
	server := httptest.NewServer(fake)
	defer server.Close()

	status, mode := request(t, server.Client(), http.MethodPut, server.URL+"/bridge/v1/mode", `{"mode":"approval","expected_revision":"revision-1"}`, nil)
	if status != http.StatusOK || !bytes.Contains(mode, []byte(`"mode":"approval"`)) {
		t.Fatalf("mode status=%d body=%s", status, mode)
	}
	status, approvals := request(t, server.Client(), http.MethodGet, server.URL+"/bridge/v1/approvals", "", nil)
	if status != http.StatusOK || !bytes.Contains(approvals, []byte("approval-0001")) {
		t.Fatalf("approvals status=%d body=%s", status, approvals)
	}
	status, finding := request(t, server.Client(), http.MethodGet, server.URL+"/bridge/v1/findings/finding-0001", "", nil)
	if status != http.StatusOK || !bytes.Contains(finding, []byte(`"schema":"charlie.finding/v1"`)) {
		t.Fatalf("finding status=%d body=%s", status, finding)
	}
}

func TestFakeBridgeInvestigationReceiptAndHistoryPage(t *testing.T) {
	fake := New()
	server := httptest.NewServer(fake)
	defer server.Close()

	status, receipt := request(t, server.Client(), http.MethodPost, server.URL+"/bridge/v1/investigations", `{}`, nil)
	if status != http.StatusAccepted || !bytes.Contains(receipt, []byte(`"revision":1`)) {
		t.Fatalf("investigation status=%d body=%s", status, receipt)
	}
	request(t, server.Client(), http.MethodPost, server.URL+"/bridge/v1/sessions", `{}`, nil)
	status, history := request(t, server.Client(), http.MethodGet, server.URL+"/bridge/v1/sessions/session-0001/history?cursor=history%3A1&limit=50", "", nil)
	if status != http.StatusOK || !bytes.Contains(history, []byte(`"data":[]`)) {
		t.Fatalf("history status=%d body=%s", status, history)
	}
}

func TestFakeBridgeDisabledAndCentralOutageIsolation(t *testing.T) {
	fake := New()
	server := httptest.NewServer(fake)
	defer server.Close()

	fake.SetProductEnabled(false)
	status, denial := request(t, server.Client(), http.MethodPost, server.URL+"/bridge/v1/sessions", `{}`, nil)
	if status != http.StatusServiceUnavailable || !bytes.Contains(denial, []byte(`"code":"unavailable"`)) {
		t.Fatalf("disabled status=%d body=%s", status, denial)
	}
	if fake.RuntimeCalls() != 0 {
		t.Fatalf("disabled bridge accepted %d runtime calls", fake.RuntimeCalls())
	}
	status, _ = request(t, server.Client(), http.MethodGet, server.URL+"/bridge/v1/health", "", nil)
	if status != http.StatusOK {
		t.Fatalf("disabled health status = %d", status)
	}

	fake.SetProductEnabled(true)
	fake.SetCentralHealthy(false)
	status, denial = request(t, server.Client(), http.MethodGet, server.URL+"/bridge/v1/findings", "", nil)
	if status != http.StatusServiceUnavailable || !bytes.Contains(denial, []byte(`"code":"central_timeout"`)) {
		t.Fatalf("outage status=%d body=%s", status, denial)
	}
	if fake.RuntimeCalls() != 0 {
		t.Fatalf("outage bridge accepted %d runtime calls", fake.RuntimeCalls())
	}
	_, statusRaw := request(t, server.Client(), http.MethodGet, server.URL+"/bridge/v1/status", "", nil)
	if !bytes.Contains(statusRaw, []byte(`"central_health":"unavailable"`)) {
		t.Fatalf("outage status body=%s", statusRaw)
	}
}

func TestFakeBridgeFailoverPreservesReplay(t *testing.T) {
	fake := New()
	server := httptest.NewServer(fake)
	defer server.Close()

	request(t, server.Client(), http.MethodPost, server.URL+"/bridge/v1/sessions", `{}`, nil)
	request(t, server.Client(), http.MethodPost, server.URL+"/bridge/v1/sessions/session-0001/messages", `{}`, nil)
	fake.Failover()
	_, statusRaw := request(t, server.Client(), http.MethodGet, server.URL+"/bridge/v1/status", "", nil)
	var status map[string]any
	if err := json.Unmarshal(statusRaw, &status); err != nil {
		t.Fatal(err)
	}
	if status["leader_instance_id"] != "instance-b" || status["epoch"] != float64(2) {
		t.Fatalf("failover status = %s", statusRaw)
	}
	_, replay := request(t, server.Client(), http.MethodGet, server.URL+"/bridge/v1/sessions/session-0001/events", "", nil)
	scanner := bufio.NewScanner(bytes.NewReader(replay))
	found := false
	for scanner.Scan() {
		if scanner.Text() == "id: event-0001" {
			found = true
		}
	}
	if !found {
		t.Fatalf("failover lost replay: %s", replay)
	}
}
