package handler

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// With an encryptor configured, an undecryptable token must NOT be sent as
// ciphertext: probeInstance reports the instance unhealthy and logs the
// decrypt failure rather than probing (which would yield an opaque 401).
func TestProbeInstance_UndecryptableTokenReportsUnhealthyAndLogs(t *testing.T) {
	key, err := auth.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	enc, err := auth.NewEncryptor(key)
	if err != nil {
		t.Fatalf("NewEncryptor: %v", err)
	}

	// newArgoCDFixture disables the loopback dial guard, so a stray probe would
	// actually reach the server and set probed=true — making the assertion real.
	var probed bool
	h, _, srv := newArgoCDFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		probed = true
		w.WriteHeader(http.StatusOK)
	})
	h.SetEncryptor(enc)

	var buf bytes.Buffer
	h.SetLogger(slog.New(slog.NewTextHandler(&buf, nil)))

	instance := sqlc.ArgocdInstance{
		ID:                 uuid.New(),
		Name:               "argocd",
		ApiUrl:             srv.URL,
		AuthTokenEncrypted: "not-a-valid-fernet-token", // fails Decrypt
		IsHealthy:          true,
	}

	if h.probeInstance(context.Background(), instance) {
		t.Fatal("probeInstance returned true for an undecryptable token; want false")
	}
	if probed {
		t.Fatal("probeInstance issued an HTTP request with an undecryptable token; want no request")
	}
	if !strings.Contains(buf.String(), "decrypt failed") {
		t.Fatalf("expected a decrypt-failure log; got: %q", buf.String())
	}
}

// The dev path: with no encryptor configured, the raw column value is returned
// unchanged so plaintext-token dev environments keep working.
func TestDecryptInstanceToken_DevPathReturnsRaw(t *testing.T) {
	h := NewArgoCDHandler(&argoCDQueryRecorder{})
	instance := sqlc.ArgocdInstance{Name: "argocd", AuthTokenEncrypted: "raw-plaintext-token"}

	token, err := h.decryptInstanceToken(instance)
	if err != nil {
		t.Fatalf("decryptInstanceToken returned error on dev path: %v", err)
	}
	if token != "raw-plaintext-token" {
		t.Fatalf("token = %q, want raw column value unchanged", token)
	}
}
