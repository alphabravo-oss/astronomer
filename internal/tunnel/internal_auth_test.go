package tunnel

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestInternalRequestEnvelopeBindsBodyIdentityAndNonce(t *testing.T) {
	const (
		secret    = "dedicated-internal-signing-key-with-at-least-32-chars"
		clusterID = "cluster-a"
	)
	body := []byte(`{"method":"GET","path":"/api/v1/pods"}`)
	req := httptest.NewRequest(http.MethodPost, "/internal/tunnel/k8s/"+clusterID, bytes.NewReader(body))
	req.Header.Set(InternalForwardedUserHeader, "11111111-1111-4111-8111-111111111111")
	if err := SignInternalK8sRequest(req, secret, clusterID, body); err != nil {
		t.Fatal(err)
	}

	authenticator := newInternalRequestAuthenticator(InternalRequestKeyring{Current: secret}, internalAudienceK8s)
	if !authenticator.verify(req, clusterID, body) {
		t.Fatal("valid envelope rejected")
	}
	if authenticator.verify(req, clusterID, body) {
		t.Fatal("replayed nonce accepted")
	}

	tampered := req.Clone(req.Context())
	tampered.Header = req.Header.Clone()
	if err := SignInternalK8sRequest(tampered, secret, clusterID, body); err != nil {
		t.Fatal(err)
	}
	tampered.Header.Set(InternalForwardedUserHeader, "22222222-2222-4222-8222-222222222222")
	if authenticator.verify(tampered, clusterID, body) {
		t.Fatal("envelope accepted after identity tampering")
	}
}

func TestInternalRequestEnvelopeRejectsExpiredTimestamp(t *testing.T) {
	const secret = "dedicated-internal-signing-key-with-at-least-32-chars"
	body := []byte(`{}`)
	req := httptest.NewRequest(http.MethodPost, "/internal/tunnel/helm/cluster-a", bytes.NewReader(body))
	if err := signInternalRequest(req, secret, internalAudienceHelm, "cluster-a", body, time.Unix(100, 0)); err != nil {
		t.Fatal(err)
	}
	authenticator := newInternalRequestAuthenticator(InternalRequestKeyring{Current: secret}, internalAudienceHelm)
	authenticator.now = func() time.Time { return time.Unix(100, 0).Add(internalEnvelopeTTL + time.Second) }
	if authenticator.verify(req, "cluster-a", body) {
		t.Fatal("expired envelope accepted")
	}
}

func TestInternalRequestEnvelopeAcceptsCurrentAndPreviousRotationKeys(t *testing.T) {
	const (
		current  = "current-internal-signing-key-with-at-least-32-chars"
		previous = "previous-internal-signing-key-with-at-least-32-chars"
		unknown  = "unknown-internal-signing-key-with-at-least-32-chars"
	)
	keys := InternalRequestKeyring{Current: current, Previous: previous}
	for name, signingKey := range map[string]string{
		"current":  current,
		"previous": previous,
		"unknown":  unknown,
	} {
		t.Run(name, func(t *testing.T) {
			body := []byte(`{"method":"GET","path":"/api/v1/pods"}`)
			req := httptest.NewRequest(http.MethodPost, "/internal/tunnel/k8s/cluster-a", bytes.NewReader(body))
			if err := SignInternalK8sRequest(req, signingKey, "cluster-a", body); err != nil {
				t.Fatal(err)
			}
			got := newInternalRequestAuthenticator(keys, internalAudienceK8s).verify(req, "cluster-a", body)
			want := signingKey != unknown
			if got != want {
				t.Fatalf("verify signed with %s key = %v, want %v", name, got, want)
			}
		})
	}
}

func TestInternalRequestEnvelopeRejectsPreviousWithoutCurrentKey(t *testing.T) {
	authenticator := newInternalRequestAuthenticator(InternalRequestKeyring{Previous: "previous-internal-signing-key-with-at-least-32-chars"}, internalAudienceK8s)
	if authenticator.enabled() {
		t.Fatal("verification-only predecessor enabled internal authentication without a current signing key")
	}
}
