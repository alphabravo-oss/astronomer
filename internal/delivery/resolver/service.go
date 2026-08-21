package resolver

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	deliverymetrics "github.com/alphabravocompany/astronomer-go/internal/delivery/metrics"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
)

type Service struct {
	resolvers map[model.SourceType]Resolver
	clients   HTTPClientFactory
	verifier  Verifier
	allowSSH  bool
	now       func() time.Time
}

// SetSSHAllowed controls the only non-HTTPS source transport. It is disabled
// by default and must be opted into by the operator; SSH still requires a
// private-host/CIDR policy, a private key, and pinned known-hosts material.
func (s *Service) SetSSHAllowed(allowed bool) {
	if s != nil {
		s.allowSSH = allowed
	}
}

func New(verifier Verifier) *Service {
	return &Service{
		resolvers: map[model.SourceType]Resolver{
			model.SourceGit:         gitResolver{},
			model.SourceOCIArtifact: ociResolver{helmChart: false},
			model.SourceHelmHTTP:    helmHTTPResolver{},
			model.SourceHelmOCI:     ociResolver{helmChart: true},
		},
		clients:  safeClientFactory{},
		verifier: verifier,
		now:      time.Now,
	}
}

func (s *Service) Resolve(ctx context.Context, request Request) (result Result, err error) {
	started := time.Now()
	defer func() {
		verification := result.Verification.Status
		if verification == "" {
			verification = "not_attempted"
			if HasCode(err, CodeVerification) {
				verification = "failed"
			}
		}
		deliverymetrics.ObserveSourceResolution(string(request.Source.Type), resolutionMetricResult(err), verification, time.Since(started))
	}()
	if request.Credential != nil {
		defer request.Credential.Clear()
	}
	if s == nil || s.clients == nil {
		return Result{}, invalid("resolver service is not configured")
	}
	if err := request.Source.Validate(); err != nil {
		return Result{}, invalid("source metadata is invalid")
	}
	if strings.TrimSpace(request.RequestedRevision) == "" || len(request.RequestedRevision) > model.MaxRequestedRevisionLength {
		return Result{}, invalid("requested revision is required and must be bounded")
	}
	resolver, ok := s.resolvers[request.Source.Type]
	if !ok || resolver == nil {
		return Result{}, invalid("source type is unsupported")
	}
	limits := request.Limits.withDefaults()
	request.Limits = limits
	sshGit := request.Source.Type == model.SourceGit && strings.HasPrefix(request.Source.URL, "ssh://")
	if sshGit && !s.allowSSH {
		return Result{}, &Error{Code: CodeNetworkDenied, Message: "SSH Git sources are disabled by operator policy"}
	}
	fetchURL := request.Source.URL
	if strings.HasPrefix(fetchURL, "oci://") {
		fetchURL = "https://" + strings.TrimPrefix(fetchURL, "oci://")
	}
	if sshGit {
		if request.ProxyURL != "" {
			return Result{}, &Error{Code: CodeInvalidRequest, Message: "SSH Git sources do not support an HTTP proxy"}
		}
	} else {
		if err := validateFetchURL(ctx, request.NetworkPolicy, fetchURL); err != nil {
			return Result{}, err
		}
	}
	tlsConfig, err := tlsConfigForCA(request.CABundle)
	if err != nil {
		return Result{}, sanitize(err, CodeInvalidRequest, false)
	}
	client, err := s.clients.Client(request.NetworkPolicy, cloneTLS(tlsConfig), request.ProxyURL, limits)
	if err != nil {
		return Result{}, sanitize(err, CodeNetworkDenied, false)
	}
	resolutionCtx, cancel := context.WithTimeout(ctx, limits.Timeout)
	defer cancel()
	result, err = resolver.Resolve(resolutionCtx, request, client)
	if err != nil {
		if resolutionCtx.Err() != nil {
			return Result{}, &Error{Code: CodeCanceled, Message: "source resolution was canceled", Retryable: errorsRetryable(resolutionCtx.Err())}
		}
		return Result{}, sanitize(err, CodeUpstreamTemporary, true)
	}
	if request.ExpectedDigest != "" && request.ExpectedDigest != result.Revision.ArtifactDigest {
		return Result{}, &Error{Code: CodeDigestMismatch, Message: "resolved artifact digest does not match the expected digest"}
	}
	defer clearBytes(result.verificationPayload)
	defer clearBytes(result.verificationSignature)
	defer clearBytes(result.verificationCertificate)
	defer clearBytes(result.verificationBundle)
	defer clearSignatureEvidence(result.verificationEvidence)
	verification, err := s.verify(resolutionCtx, VerificationInput{
		Source: request.Source, Revision: result.Revision, Artifact: result.verificationPayload,
		Signature: result.verificationSignature, Certificate: result.verificationCertificate,
		Bundle: result.verificationBundle, Evidence: result.verificationEvidence,
	})
	if err != nil {
		return Result{}, err
	}
	result.Verification = verification
	result.ResolvedAt = s.now().UTC()
	return result, nil
}

func resolutionMetricResult(err error) string {
	if err == nil {
		return "success"
	}
	for _, candidate := range []struct {
		code   ErrorCode
		result string
	}{
		{CodeInvalidRequest, "invalid"}, {CodeNetworkDenied, "network_denied"},
		{CodeAuthentication, "authentication_failed"}, {CodeNotFound, "not_found"},
		{CodeDigestMismatch, "digest_mismatch"}, {CodeVerification, "verification_failed"},
		{CodeLimitExceeded, "limit_exceeded"}, {CodeUpstreamTemporary, "upstream_temporary"},
		{CodeCanceled, "canceled"},
	} {
		if HasCode(err, candidate.code) {
			return candidate.result
		}
	}
	return "upstream_temporary"
}

func (s *Service) verify(ctx context.Context, input VerificationInput) (Verification, error) {
	if input.Source.Trust.AllowUnsigned {
		return Verification{Status: "unsigned"}, nil
	}
	if s.verifier == nil {
		return Verification{}, &Error{Code: CodeVerification, Message: "source trust policy requires a configured verifier"}
	}
	verification, err := s.verifier.Verify(ctx, input)
	if err != nil || verification.Status != "verified" || strings.TrimSpace(verification.Identity) == "" {
		return Verification{}, &Error{Code: CodeVerification, Message: "source signature verification failed"}
	}
	if len(verification.Identity) > 512 || len(verification.Provider) > 64 || strings.ContainsAny(verification.Identity+verification.Provider, "\r\n\x00") {
		return Verification{}, &Error{Code: CodeVerification, Message: "source verification identity is invalid"}
	}
	return verification, nil
}

func digestBytes(value []byte) model.Digest {
	sum := sha256.Sum256(value)
	return model.Digest("sha256:" + hex.EncodeToString(sum[:]))
}

func clearBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}

func clearSignatureEvidence(evidence []SignatureEvidence) {
	for index := range evidence {
		clearBytes(evidence[index].Payload)
		clearBytes(evidence[index].Signature)
		clearBytes(evidence[index].Certificate)
		clearBytes(evidence[index].Bundle)
		evidence[index] = SignatureEvidence{}
	}
}

func cloneTLS(config *tls.Config) *tls.Config {
	if config == nil {
		return &tls.Config{MinVersion: tls.VersionTLS12}
	}
	return config.Clone()
}

func invalid(message string) error {
	return &Error{Code: CodeInvalidRequest, Message: message}
}

func sanitize(err error, fallback ErrorCode, retryable bool) error {
	if err == nil {
		return nil
	}
	var typed *Error
	if errorsAs(err, &typed) {
		return typed
	}
	return &Error{Code: fallback, Message: "source operation failed", Retryable: retryable}
}

// Function variables make the sanitization path independently fuzzable without
// retaining arbitrary upstream error strings in a wrapped error chain.
var (
	errorsAs        = func(err error, target any) bool { return errorAs(err, target) }
	errorsRetryable = func(err error) bool { return err != context.Canceled }
)

func errorAs(err error, target any) bool {
	return errors.As(err, target)
}

func immutableGitDigest(commit string) model.Digest {
	return digestBytes([]byte(fmt.Sprintf("astronomer.git.commit.v1\x00%s", commit)))
}

var _ HTTPClientFactory = safeClientFactory{}
