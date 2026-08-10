package charlie

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/charlie/contract"
	"github.com/gowebpki/jcs"
)

const MaxOnboardingPackageBytes = 262144

type OnboardingConfirmation struct {
	SigningPublicKeyBase64      string
	ConfirmedSigningKeyID       string
	ConfirmedSigningFingerprint string
	ExpectedDeploymentID        string
	ExpectedRouteID             string
	ExpectedMCPURL              string
	Now                         time.Time
}

type ValidatedOnboarding struct {
	Package               contract.OnboardingPackage `json:"-"`
	RawPackage            json.RawMessage            `json:"-"`
	RawDigest             string                     `json:"-"`
	PackageID             string
	EnrollmentCredentials []string          `json:"-"`
	ArtifactCredential    string            `json:"-"`
	SigningPublicKey      ed25519.PublicKey `json:"-"`
}

type OnboardingStatus struct {
	PackageID               string                 `json:"package_id"`
	ProductID               string                 `json:"product_id"`
	ProductSlug             string                 `json:"product_slug"`
	DeploymentID            string                 `json:"deployment_id"`
	LogicalAgentID          string                 `json:"logical_agent_id"`
	IntegrationID           string                 `json:"integration_id"`
	MCPURL                  string                 `json:"mcp_url"`
	RouteID                 string                 `json:"route_id"`
	AllowedRouteIDs         []string               `json:"allowed_route_ids"`
	Schema                  string                 `json:"schema"`
	CentralAPIVersion       string                 `json:"central_api_version"`
	CentralTrustFingerprint string                 `json:"central_trust_fingerprint"`
	SigningKeyID            string                 `json:"signing_key_id"`
	SigningFingerprint      string                 `json:"signing_fingerprint"`
	PackageDigest           string                 `json:"package_digest"`
	Artifact                SafeOnboardingArtifact `json:"artifact"`
	ReplicaCount            int                    `json:"replica_count"`
	IssuedAt                time.Time              `json:"issued_at"`
	ExpiresAt               time.Time              `json:"expires_at"`
	State                   string                 `json:"state"`
	Idempotent              bool                   `json:"idempotent"`
}

type SafeOnboardingArtifact struct {
	Image          string `json:"image"`
	ManifestDigest string `json:"manifest_digest"`
	Chart          string `json:"chart"`
	ChartDigest    string `json:"chart_digest"`
}

func (v ValidatedOnboarding) SafeStatus(state string, idempotent bool) OnboardingStatus {
	allowedRoutes := make([]string, len(v.Package.Route.AllowedRouteIds))
	for index, routeID := range v.Package.Route.AllowedRouteIds {
		allowedRoutes[index] = string(routeID)
	}
	return OnboardingStatus{
		PackageID: v.PackageID, ProductID: string(v.Package.ProductId), ProductSlug: v.Package.ProductSlug, DeploymentID: string(v.Package.DeploymentId),
		LogicalAgentID: string(v.Package.LogicalAgentId), RouteID: string(v.Package.Route.RouteId), AllowedRouteIDs: allowedRoutes,
		IntegrationID: string(v.Package.Integration.IntegrationId), MCPURL: v.Package.Integration.McpUrl,
		Schema: v.Package.Schema, CentralAPIVersion: v.Package.CentralApiVersion,
		CentralTrustFingerprint: v.Package.Central.CertificateSha256, SigningKeyID: string(v.Package.Signing.KeyId),
		SigningFingerprint: v.Package.Signing.PublicKeySha256, PackageDigest: v.RawDigest,
		Artifact:     SafeOnboardingArtifact{Image: v.Package.Artifact.Image, ManifestDigest: v.Package.Artifact.ManifestDigest, Chart: v.Package.Artifact.Chart, ChartDigest: v.Package.Artifact.ChartDigest},
		ReplicaCount: v.Package.ReplicaCount, IssuedAt: v.Package.IssuedAt.UTC(), ExpiresAt: v.Package.ExpiresAt.UTC(),
		State: state, Idempotent: idempotent,
	}
}

// ValidateOnboardingPackage performs only local deterministic verification. It
// has no transport dependency and therefore cannot call Charlie central.
func ValidateOnboardingPackage(raw []byte, confirmation OnboardingConfirmation) (ValidatedOnboarding, error) {
	if len(raw) == 0 || len(raw) > MaxOnboardingPackageBytes {
		return ValidatedOnboarding{}, fmt.Errorf("onboarding package size is invalid")
	}
	pkg, err := contract.ParseOnboardingPackage(raw)
	if err != nil {
		return ValidatedOnboarding{}, err
	}
	now := confirmation.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if !pkg.IssuedAt.Before(pkg.ExpiresAt) || !now.Before(pkg.ExpiresAt) || pkg.IssuedAt.After(now.Add(5*time.Minute)) {
		return ValidatedOnboarding{}, fmt.Errorf("onboarding package is expired or not yet valid")
	}
	packageID := string(pkg.PackageId)
	if pkg.ProductSlug != "astronomer" {
		return ValidatedOnboarding{}, fmt.Errorf("onboarding product must be astronomer")
	}
	if pkg.CentralApiVersion != contract.CentralAPISchemaVersion {
		return ValidatedOnboarding{}, fmt.Errorf("unsupported Charlie central API version")
	}
	if string(pkg.DeploymentId) != confirmation.ExpectedDeploymentID || string(pkg.Route.RouteId) != confirmation.ExpectedRouteID {
		return ValidatedOnboarding{}, fmt.Errorf("onboarding deployment and route confirmation do not match")
	}
	if confirmation.ExpectedMCPURL == "" || pkg.Integration.McpUrl != confirmation.ExpectedMCPURL || string(pkg.Integration.IntegrationId) == "" {
		return ValidatedOnboarding{}, fmt.Errorf("onboarding integration and private MCP confirmation do not match")
	}
	allowedRoute := false
	for _, routeID := range pkg.Route.AllowedRouteIds {
		if routeID == pkg.Route.RouteId {
			allowedRoute = true
		}
	}
	if !allowedRoute {
		return ValidatedOnboarding{}, fmt.Errorf("selected route is not allowed by onboarding package")
	}
	if err := validateCentralTrust(pkg.Central.BaseUrl, pkg.Central.CaBundlePem, pkg.Central.CertificateSha256); err != nil {
		return ValidatedOnboarding{}, err
	}
	manifestDigest := strings.TrimPrefix(pkg.Artifact.ManifestDigest, "sha256:")
	if !strings.HasSuffix(pkg.Artifact.Image, "@sha256:"+manifestDigest) {
		return ValidatedOnboarding{}, fmt.Errorf("agent image and manifest digest do not match")
	}
	if string(pkg.Signing.KeyId) == "" {
		return ValidatedOnboarding{}, fmt.Errorf("signing key ID is required")
	}
	if string(pkg.Signing.KeyId) != confirmation.ConfirmedSigningKeyID {
		return ValidatedOnboarding{}, fmt.Errorf("signing key ID confirmation does not match")
	}
	publicKey, err := verifyOnboardingSignature(raw, pkg, confirmation)
	if err != nil {
		return ValidatedOnboarding{}, err
	}

	enrollment := make([]string, pkg.ReplicaCount)
	seenEnrollment := make(map[string]struct{}, pkg.ReplicaCount)
	artifact := ""
	for _, credential := range pkg.Credentials {
		if !now.Before(credential.ExpiresAt) {
			return ValidatedOnboarding{}, fmt.Errorf("onboarding credential is expired")
		}
		switch credential.Purpose {
		case contract.CredentialPurposeAgentEnrollment:
			if credential.ReplicaOrdinal == nil || *credential.ReplicaOrdinal < 0 || *credential.ReplicaOrdinal >= pkg.ReplicaCount || enrollment[*credential.ReplicaOrdinal] != "" {
				return ValidatedOnboarding{}, fmt.Errorf("onboarding enrollment replica slots are invalid")
			}
			if _, duplicate := seenEnrollment[credential.Credential]; duplicate {
				return ValidatedOnboarding{}, fmt.Errorf("onboarding enrollment credentials must be unique per replica")
			}
			enrollment[*credential.ReplicaOrdinal] = credential.Credential
			seenEnrollment[credential.Credential] = struct{}{}
		case contract.CredentialPurposeArtifactPull:
			if credential.ReplicaOrdinal != nil || artifact != "" {
				return ValidatedOnboarding{}, fmt.Errorf("onboarding artifact credential slot is invalid")
			}
			artifact = credential.Credential
		default:
			return ValidatedOnboarding{}, fmt.Errorf("onboarding credential purpose is not allowed")
		}
	}
	if artifact == "" || len(enrollment) != pkg.ReplicaCount {
		return ValidatedOnboarding{}, fmt.Errorf("onboarding credential purposes are incomplete")
	}
	for ordinal, credential := range enrollment {
		if credential == "" {
			return ValidatedOnboarding{}, fmt.Errorf("onboarding enrollment credential for replica %d is missing", ordinal)
		}
	}
	digest := sha256.Sum256(raw)
	return ValidatedOnboarding{
		Package: pkg, RawPackage: append(json.RawMessage(nil), raw...), RawDigest: "sha256:" + hex.EncodeToString(digest[:]), PackageID: packageID,
		EnrollmentCredentials: append([]string(nil), enrollment...), ArtifactCredential: artifact,
		SigningPublicKey: append(ed25519.PublicKey(nil), publicKey...),
	}, nil
}

func verifyOnboardingSignature(raw []byte, pkg contract.OnboardingPackage, confirmation OnboardingConfirmation) (ed25519.PublicKey, error) {
	publicKey, err := base64.RawURLEncoding.DecodeString(confirmation.SigningPublicKeyBase64)
	if err != nil || len(publicKey) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("operator signing public key is invalid")
	}
	fingerprint := sha256.Sum256(publicKey)
	fingerprintHex := hex.EncodeToString(fingerprint[:])
	if fingerprintHex != strings.ToLower(confirmation.ConfirmedSigningFingerprint) || fingerprintHex != pkg.Signing.PublicKeySha256 {
		return nil, fmt.Errorf("Charlie signing fingerprint does not match operator confirmation")
	}
	signature, err := base64.RawURLEncoding.DecodeString(pkg.Signature)
	if err != nil || len(signature) != ed25519.SignatureSize {
		return nil, fmt.Errorf("onboarding signature is invalid")
	}
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil, err
	}
	delete(object, "signature")
	unsigned, err := json.Marshal(object)
	if err != nil {
		return nil, err
	}
	canonical, err := jcs.Transform(unsigned)
	if err != nil {
		return nil, fmt.Errorf("canonicalize onboarding package: %w", err)
	}
	signingBytes := append([]byte("charlie.onboarding/v1\n"), canonical...)
	if !ed25519.Verify(ed25519.PublicKey(publicKey), signingBytes, signature) {
		return nil, fmt.Errorf("onboarding signature verification failed")
	}
	// The package is authentic under the independently supplied key. Only now
	// trust its embedded copy enough to compare it; the embedded key is for the
	// air-gapped agent's action-envelope checks, never for verifying this package.
	embeddedKey, err := base64.RawURLEncoding.DecodeString(pkg.Signing.PublicKey)
	if err != nil || len(embeddedKey) != ed25519.PublicKeySize || !bytes.Equal(embeddedKey, publicKey) {
		return nil, fmt.Errorf("Charlie embedded signing key does not match operator confirmation")
	}
	return ed25519.PublicKey(publicKey), nil
}

func validateCentralTrust(rawURL, caBundle, expectedFingerprint string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return fmt.Errorf("Charlie central URL must be an origin-only HTTPS URL")
	}
	certificates, err := parseCertificateBundle(caBundle)
	if err != nil {
		return err
	}
	for index, certificate := range certificates {
		if !certificate.IsCA || !certificate.BasicConstraintsValid || certificate.KeyUsage&x509.KeyUsageCertSign == 0 {
			return fmt.Errorf("Charlie central CA bundle contains a non-CA certificate")
		}
		issuer := certificate
		if index+1 < len(certificates) {
			issuer = certificates[index+1]
		}
		if err := certificate.CheckSignatureFrom(issuer); err != nil {
			return fmt.Errorf("Charlie central CA chain is invalid")
		}
	}
	root := certificates[len(certificates)-1]
	if !bytes.Equal(root.RawSubject, root.RawIssuer) || root.CheckSignatureFrom(root) != nil {
		return fmt.Errorf("Charlie central CA bundle must end in a self-signed root")
	}
	fingerprint := sha256.Sum256(certificates[0].Raw)
	if hex.EncodeToString(fingerprint[:]) != expectedFingerprint {
		return fmt.Errorf("Charlie central certificate fingerprint does not match CA bundle")
	}
	return nil
}

func parseCertificateBundle(bundle string) ([]*x509.Certificate, error) {
	var certificates []*x509.Certificate
	rest := []byte(bundle)
	for len(strings.TrimSpace(string(rest))) > 0 {
		block, remaining := pem.Decode(rest)
		if block == nil || block.Type != "CERTIFICATE" || len(block.Headers) != 0 {
			return nil, fmt.Errorf("Charlie central CA bundle is invalid")
		}
		certificate, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parse Charlie central CA: %w", err)
		}
		certificates = append(certificates, certificate)
		rest = remaining
	}
	if len(certificates) == 0 {
		return nil, fmt.Errorf("Charlie central CA bundle is empty")
	}
	return certificates, nil
}
