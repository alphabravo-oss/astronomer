package tunnel

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	InternalTimestampHeader = "X-Astronomer-Internal-Timestamp"
	InternalNonceHeader     = "X-Astronomer-Internal-Nonce"
	InternalSignatureHeader = "X-Astronomer-Internal-Signature"
	internalEnvelopeTTL     = 30 * time.Second
)

const (
	internalAudienceK8s  = "tunnel-k8s-v1"
	internalAudienceHelm = "tunnel-helm-v1"
)

// internalRequestAuthenticator verifies short-lived, body-bound cross-pod
// request envelopes and rejects nonce replay. The dedicated internal PSK is
// only an HMAC key; it is never transmitted on the wire.
type internalRequestAuthenticator struct {
	secrets  [][]byte
	audience string
	now      func() time.Time
	mu       sync.Mutex
	seen     map[string]time.Time
}

// InternalRequestKeyring supports zero-downtime cross-pod key rotation. Current
// signs every new request; Previous is verification-only and should be cleared
// after all replicas have observed Current.
type InternalRequestKeyring struct {
	Current  string
	Previous string
}

func newInternalRequestAuthenticator(keys InternalRequestKeyring, audience string) *internalRequestAuthenticator {
	secrets := make([][]byte, 0, 2)
	if keys.Current != "" {
		secrets = append(secrets, []byte(keys.Current))
		if keys.Previous != "" && subtle.ConstantTimeCompare([]byte(keys.Previous), []byte(keys.Current)) != 1 {
			secrets = append(secrets, []byte(keys.Previous))
		}
	}
	return &internalRequestAuthenticator{
		secrets: secrets, audience: audience, now: time.Now,
		seen: make(map[string]time.Time),
	}
}

func (a *internalRequestAuthenticator) enabled() bool {
	return a != nil && len(a.secrets) > 0
}

func internalEnvelopePayload(r *http.Request, audience, clusterID, timestamp, nonce string, body []byte) []byte {
	digest := sha256.Sum256(body)
	return []byte(strings.Join([]string{
		audience, r.Method, r.URL.EscapedPath(), clusterID, timestamp, nonce,
		strings.TrimSpace(r.Header.Get(InternalForwardedUserHeader)),
		base64.RawURLEncoding.EncodeToString(digest[:]),
	}, "\n"))
}

func signInternalRequest(r *http.Request, secret, audience, clusterID string, body []byte, now time.Time) error {
	if r == nil || secret == "" || clusterID == "" {
		return fmt.Errorf("internal request signing is not configured")
	}
	nonceBytes := make([]byte, 24)
	if _, err := rand.Read(nonceBytes); err != nil {
		return fmt.Errorf("generate internal request nonce: %w", err)
	}
	timestamp := strconv.FormatInt(now.UTC().Unix(), 10)
	nonce := base64.RawURLEncoding.EncodeToString(nonceBytes)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(internalEnvelopePayload(r, audience, clusterID, timestamp, nonce, body))
	r.Header.Set(InternalSourceHeader, InternalSourceValue)
	r.Header.Set(InternalTimestampHeader, timestamp)
	r.Header.Set(InternalNonceHeader, nonce)
	r.Header.Set(InternalSignatureHeader, base64.RawURLEncoding.EncodeToString(mac.Sum(nil)))
	return nil
}

// SignInternalK8sRequest signs one cross-pod Kubernetes tunnel request.
func SignInternalK8sRequest(r *http.Request, secret, clusterID string, body []byte) error {
	return signInternalRequest(r, secret, internalAudienceK8s, clusterID, body, time.Now())
}

// SignInternalHelmRequest signs one cross-pod Helm tunnel request.
func SignInternalHelmRequest(r *http.Request, secret, clusterID string, body []byte) error {
	return signInternalRequest(r, secret, internalAudienceHelm, clusterID, body, time.Now())
}

func (a *internalRequestAuthenticator) verify(r *http.Request, clusterID string, body []byte) bool {
	if !a.enabled() || r == nil || !hasSiblingSourceSignal(r) {
		return false
	}
	timestampText := r.Header.Get(InternalTimestampHeader)
	nonce := r.Header.Get(InternalNonceHeader)
	signatureText := r.Header.Get(InternalSignatureHeader)
	unix, err := strconv.ParseInt(timestampText, 10, 64)
	if err != nil || nonce == "" || signatureText == "" {
		return false
	}
	now := a.now().UTC()
	signedAt := time.Unix(unix, 0).UTC()
	if signedAt.Before(now.Add(-internalEnvelopeTTL)) || signedAt.After(now.Add(internalEnvelopeTTL)) {
		return false
	}
	signature, err := base64.RawURLEncoding.DecodeString(signatureText)
	if err != nil {
		return false
	}
	payload := internalEnvelopePayload(r, a.audience, clusterID, timestampText, nonce, body)
	matched := 0
	for _, secret := range a.secrets {
		mac := hmac.New(sha256.New, secret)
		_, _ = mac.Write(payload)
		expected := mac.Sum(nil)
		if len(signature) == len(expected) {
			matched |= subtle.ConstantTimeCompare(signature, expected)
		}
	}
	if matched != 1 {
		return false
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	for key, expiresAt := range a.seen {
		if !now.Before(expiresAt) {
			delete(a.seen, key)
		}
	}
	if _, replayed := a.seen[nonce]; replayed {
		return false
	}
	a.seen[nonce] = signedAt.Add(internalEnvelopeTTL)
	return true
}
