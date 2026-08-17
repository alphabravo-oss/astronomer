// Package resolver turns user-supplied mutable source references into bounded,
// immutable identities before a delivery rollout can be approved.
package resolver

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
)

const (
	DefaultMaxIndexBytes     int64 = 8 << 20
	DefaultMaxArtifactBytes  int64 = 64 << 20
	DefaultMaxHelmChartBytes int64 = 64 << 20
	DefaultMaxRedirects            = 5
	DefaultTimeout                 = 2 * time.Minute
)

type ErrorCode string

const (
	CodeInvalidRequest    ErrorCode = "source_resolution_invalid"
	CodeNetworkDenied     ErrorCode = "source_network_denied"
	CodeAuthentication    ErrorCode = "source_authentication_failed"
	CodeNotFound          ErrorCode = "source_revision_not_found"
	CodeDigestMismatch    ErrorCode = "source_digest_mismatch"
	CodeVerification      ErrorCode = "source_verification_failed"
	CodeLimitExceeded     ErrorCode = "source_limit_exceeded"
	CodeUpstreamTemporary ErrorCode = "source_upstream_temporary"
	CodeCanceled          ErrorCode = "source_resolution_canceled"
)

// Error deliberately contains a stable code and a sanitized message only.
// Upstream responses, URLs, and credential-bearing errors must not be wrapped.
type Error struct {
	Code      ErrorCode
	Message   string
	Retryable bool
}

func (e *Error) Error() string {
	if e == nil {
		return "source resolution failed"
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func HasCode(err error, code ErrorCode) bool {
	var resolutionError *Error
	return errors.As(err, &resolutionError) && resolutionError.Code == code
}

// CredentialMaterial is an ephemeral, decrypted credential projection. Callers
// must hand ownership to Service.Resolve; it clears every byte before returning.
type CredentialMaterial struct {
	Username   []byte
	Password   []byte
	Token      []byte
	PrivateKey []byte
	KnownHosts []byte
	Passphrase []byte
}

func (c *CredentialMaterial) Clear() {
	if c == nil {
		return
	}
	for _, value := range [][]byte{c.Username, c.Password, c.Token, c.PrivateKey, c.KnownHosts, c.Passphrase} {
		for i := range value {
			value[i] = 0
		}
	}
	*c = CredentialMaterial{}
}

type Limits struct {
	MaxIndexBytes     int64
	MaxArtifactBytes  int64
	MaxHelmChartBytes int64
	MaxRedirects      int
	Timeout           time.Duration
}

func (l Limits) withDefaults() Limits {
	if l.MaxIndexBytes <= 0 {
		l.MaxIndexBytes = DefaultMaxIndexBytes
	}
	if l.MaxArtifactBytes <= 0 {
		l.MaxArtifactBytes = DefaultMaxArtifactBytes
	}
	if l.MaxHelmChartBytes <= 0 {
		l.MaxHelmChartBytes = DefaultMaxHelmChartBytes
	}
	if l.MaxRedirects <= 0 {
		l.MaxRedirects = DefaultMaxRedirects
	}
	if l.Timeout <= 0 {
		l.Timeout = DefaultTimeout
	}
	return l
}

type Request struct {
	Source            model.Source
	RequestedRevision string
	Chart             string
	ExpectedDigest    model.Digest
	Credential        *CredentialMaterial
	CABundle          []byte
	ProxyURL          string
	NetworkPolicy     NetworkPolicy
	Limits            Limits
}

type Verification struct {
	Status   string `json:"status"`
	Identity string `json:"identity,omitempty"`
	Provider string `json:"provider,omitempty"`
}

type Result struct {
	Revision                model.ImmutableRevision `json:"revision"`
	Verification            Verification            `json:"verification"`
	ResolvedAt              time.Time               `json:"resolved_at"`
	verificationPayload     []byte
	verificationSignature   []byte
	verificationCertificate []byte
	verificationEvidence    []SignatureEvidence
}

// SignatureEvidence is the complete, bounded verification material acquired by
// the resolver under its guarded HTTP client. OCI verification deliberately
// consumes these local bytes instead of allowing the cosign subprocess to make
// a second, independently-networked registry request.
type SignatureEvidence struct {
	Payload     []byte
	Signature   []byte
	Certificate []byte
	Bundle      []byte
}

// VerificationInput carries immutable identity, bounded bytes acquired under
// the resolver's network policy, and an ephemeral credential reference for a
// second authenticated registry read. Verifiers must apply the same policy to
// every subprocess fetch and must not retain or log any of this material.
type VerificationInput struct {
	Source      model.Source
	Revision    model.ImmutableRevision
	Artifact    []byte
	Signature   []byte
	Certificate []byte
	Bundle      []byte
	Evidence    []SignatureEvidence
}

// Verifier performs source-specific cryptographic verification. A nil verifier is
// accepted only by an explicit allow_unsigned source policy.
type Verifier interface {
	Verify(context.Context, VerificationInput) (Verification, error)
}

// HTTPClientFactory builds a client with redirect and dial-time network-policy
// checks. It exists to make local fixture tests deterministic without weakening
// the production dialer.
type HTTPClientFactory interface {
	Client(NetworkPolicy, *tls.Config, string, Limits) (*http.Client, error)
}

type Resolver interface {
	Resolve(context.Context, Request, *http.Client) (Result, error)
}
