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
// material.
//
// This test used to end by asserting the opposite for the WRITE response:
// monitoringBackendWriteResponse echoed the document back unredacted, on the
// argument that a monitoring:update caller had just supplied it in the same
// request and so was learning nothing new. Migration 146 falsified that
// argument and the assertion was pinning a disclosure: UpdateBackendConfig
// became a read-modify-write, so the rendered document is the caller's input
// merged over the STORED one, and a PUT that omitted authConfig rendered the
// stored credential to anyone holding monitoring:update or monitoring:create.
// There is now a single redacted payload for every response and no write
// variant to assert against; the end-to-end version of that assertion lives in
// TestUpdateBackendConfigResponseNeverCarriesTheCredential, which checks the
// actual rr.Body in both the omitted- and supplied-authConfig cases.
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

	// Migration 146: the payload renders from the RESOLVED document, so the
	// test feeds it the same way the handler does.
	authConfig := decodeJSONMap(backend.AuthConfig)

	body, err := json.Marshal(monitoringBackendResponse(backend, authConfig))
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
	exposed, _ := read["authConfig"].(map[string]any)
	if _, ok := exposed["operationPolicies"]; !ok {
		t.Fatalf("read response dropped the non-secret operationPolicies block: %v", read["authConfig"])
	}
	if len(exposed) != 1 {
		t.Fatalf("read response authConfig carries more than operationPolicies: %v", exposed)
	}
	// The key NAMES stay visible so an operator can still tell that credentials
	// are configured without receiving them.
	keys, _ := read["authConfigKeys"].([]any)
	if len(keys) != 2 || keys[0] != "basicAuth" || keys[1] != "bearerToken" {
		t.Fatalf("authConfigKeys = %v, want [basicAuth bearerToken]", keys)
	}

}
