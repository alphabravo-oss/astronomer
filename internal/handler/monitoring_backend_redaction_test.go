package handler

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// monitoring:read is a low-privilege verb — the shipped Cluster Troubleshooter
// and Project Operator/Troubleshooter templates all carry it — so the backend
// read must not hand those principals the operator-supplied backend auth
// material. Only the monitoring:update round-trip, where the caller supplied
// the material in the same request, echoes it back.
func TestMonitoringBackendReadResponseRedactsAuthConfig(t *testing.T) {
	backend := sqlc.MonitoringBackend{
		ID:       uuid.New(),
		AuthType: "bearer",
		AuthConfig: json.RawMessage(`{
			"bearerToken": "s3cr3t-thanos-token",
			"basicAuth": {"username": "ops", "password": "hunter2"},
			"operationPolicies": {"maxRetryAttempts": 3}
		}`),
	}

	body, err := json.Marshal(monitoringBackendResponse(backend))
	if err != nil {
		t.Fatalf("marshal read response: %v", err)
	}
	for _, secret := range []string{"s3cr3t-thanos-token", "hunter2"} {
		if strings.Contains(string(body), secret) {
			t.Fatalf("read response leaked %q: %s", secret, body)
		}
	}

	var read map[string]any
	if err := json.Unmarshal(body, &read); err != nil {
		t.Fatalf("unmarshal read response: %v", err)
	}
	authConfig, _ := read["authConfig"].(map[string]any)
	if _, ok := authConfig["operationPolicies"]; !ok {
		t.Fatalf("read response dropped the non-secret operationPolicies block: %v", read["authConfig"])
	}
	if len(authConfig) != 1 {
		t.Fatalf("read response authConfig carries more than operationPolicies: %v", authConfig)
	}
	// The key NAMES stay visible so an operator can still tell that credentials
	// are configured without receiving them.
	keys, _ := read["authConfigKeys"].([]any)
	if len(keys) != 2 || keys[0] != "basicAuth" || keys[1] != "bearerToken" {
		t.Fatalf("authConfigKeys = %v, want [basicAuth bearerToken]", keys)
	}

	writeBody, err := json.Marshal(monitoringBackendWriteResponse(backend))
	if err != nil {
		t.Fatalf("marshal write response: %v", err)
	}
	if !strings.Contains(string(writeBody), "s3cr3t-thanos-token") {
		t.Fatalf("monitoring:update round-trip lost the caller's authConfig: %s", writeBody)
	}
}
