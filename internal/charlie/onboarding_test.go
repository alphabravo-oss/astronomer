package charlie

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"testing"
	"time"

	"github.com/gowebpki/jcs"
)

type onboardingFixture struct {
	now          time.Time
	privateKey   ed25519.PrivateKey
	confirmation OnboardingConfirmation
	object       map[string]any
}

func newOnboardingFixture(t *testing.T) onboardingFixture {
	t.Helper()
	now := time.Date(2026, 8, 5, 3, 0, 0, 0, time.UTC)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caPublic, caPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(7), Subject: pkix.Name{CommonName: "Charlie Central Test CA"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(365 * 24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, caPublic, caPrivate)
	if err != nil {
		t.Fatal(err)
	}
	caPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	caFingerprint := sha256.Sum256(der)
	keyFingerprint := sha256.Sum256(publicKey)
	digest := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	object := map[string]any{
		"schema": "charlie.onboarding/v1", "central_api_version": "charlie/v1",
		"package_id": "onboard_9e7ac3d5b7dd4a369b5abb02eabc273b",
		"issued_at":  now.Add(-time.Minute).Format(time.RFC3339), "expires_at": now.Add(time.Hour).Format(time.RFC3339),
		"organization_id": "org_1", "product_id": "product_1a8a43aab5b28bf94f330d1bff3a23c4", "product_slug": "astronomer", "deployment_id": "deployment-1",
		"environment_id": "environment-1", "tenant_id": "tenant-1", "logical_agent_id": "agent-1",
		"replica_count": 2,
		"route":         map[string]any{"route_id": "route-1", "allowed_route_ids": []any{"route-1"}},
		"central":       map[string]any{"base_url": "https://charlie.example.test", "ca_bundle_pem": caPEM, "certificate_sha256": hex.EncodeToString(caFingerprint[:])},
		"credentials": []any{
			map[string]any{"purpose": "agent_enrollment", "replica_ordinal": 0, "credential": "enrollment-secret-value-00000000001", "expires_at": now.Add(30 * time.Minute).Format(time.RFC3339)},
			map[string]any{"purpose": "agent_enrollment", "replica_ordinal": 1, "credential": "enrollment-secret-value-00000000002", "expires_at": now.Add(30 * time.Minute).Format(time.RFC3339)},
			map[string]any{"purpose": "artifact_pull", "credential": "artifact-secret-value-0000000000001", "expires_at": now.Add(30 * time.Minute).Format(time.RFC3339)},
		},
		"artifact": map[string]any{"image": "registry.example.test/charlie/agent@sha256:" + digest, "manifest_digest": "sha256:" + digest, "chart": "oci://registry.example.test/charlie/agent", "chart_digest": "sha256:" + digest},
		"signing":  map[string]any{"algorithm": "Ed25519", "canonicalization": "RFC8785", "key_id": "operator-key-1", "public_key_sha256": hex.EncodeToString(keyFingerprint[:])},
	}
	return onboardingFixture{
		now: now, privateKey: privateKey, object: object,
		confirmation: OnboardingConfirmation{
			SigningPublicKeyBase64: base64.RawURLEncoding.EncodeToString(publicKey),
			ConfirmedSigningKeyID:  "operator-key-1", ConfirmedSigningFingerprint: hex.EncodeToString(keyFingerprint[:]),
			ExpectedDeploymentID: "deployment-1", ExpectedRouteID: "route-1", Now: now,
		},
	}
}

func (f onboardingFixture) signed(t *testing.T) []byte {
	t.Helper()
	delete(f.object, "signature")
	unsigned, err := json.Marshal(f.object)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := jcs.Transform(unsigned)
	if err != nil {
		t.Fatal(err)
	}
	f.object["signature"] = base64.RawURLEncoding.EncodeToString(ed25519.Sign(f.privateKey, append([]byte("charlie.onboarding/v1\n"), canonical...)))
	raw, err := json.Marshal(f.object)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestValidateOnboardingPackageValidAndContentFree(t *testing.T) {
	fixture := newOnboardingFixture(t)
	raw := fixture.signed(t)
	validated, err := ValidateOnboardingPackage(raw, fixture.confirmation)
	if err != nil {
		t.Fatal(err)
	}
	if validated.PackageID != fixture.object["package_id"] || len(validated.EnrollmentCredentials) != 2 || validated.ArtifactCredential == "" {
		t.Fatalf("unexpected validated package: %+v", validated)
	}
	if !bytes.Equal(validated.RawPackage, raw) {
		t.Fatal("validated onboarding package did not preserve the exact signed JSON bytes")
	}
	status := validated.SafeStatus("validated", false)
	if status.ProductID != "product_1a8a43aab5b28bf94f330d1bff3a23c4" || status.ProductSlug != "astronomer" || status.DeploymentID != "deployment-1" || status.LogicalAgentID != "agent-1" ||
		status.CentralAPIVersion != "charlie/v1" || status.ReplicaCount != 2 || len(status.AllowedRouteIDs) != 1 ||
		status.PackageDigest == "" || status.SigningFingerprint == "" || status.CentralTrustFingerprint == "" ||
		status.Artifact.ManifestDigest == "" || status.Artifact.ChartDigest == "" {
		t.Fatalf("safe onboarding review is incomplete: %+v", status)
	}
	safeSerialized, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"enrollment-secret", "artifact-secret", "ca_bundle_pem", "signature"} {
		if stringContains(string(safeSerialized), forbidden) {
			t.Fatalf("safe onboarding review disclosed %q: %s", forbidden, safeSerialized)
		}
	}
	serialized, err := json.Marshal(validated)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"enrollment-secret", "artifact-secret", "PRIVATE KEY", "ca_bundle_pem"} {
		if stringContains(string(serialized), forbidden) {
			t.Fatalf("validated output disclosed %q: %s", forbidden, serialized)
		}
	}
}

func TestValidateOnboardingPackageRejectsInvalidMatrix(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*onboardingFixture)
		tamper bool
	}{
		{name: "expired", mutate: func(f *onboardingFixture) { f.confirmation.Now = f.now.Add(2 * time.Hour) }},
		{name: "wrong product", mutate: func(f *onboardingFixture) { f.object["product_slug"] = "another-product" }},
		{name: "wrong fingerprint", mutate: func(f *onboardingFixture) { f.confirmation.ConfirmedSigningFingerprint = string(make([]byte, 64)) }},
		{name: "unsupported version", mutate: func(f *onboardingFixture) { f.object["central_api_version"] = "charlie/v999" }},
		{name: "wrong deployment", mutate: func(f *onboardingFixture) { f.confirmation.ExpectedDeploymentID = "different" }},
		{name: "route not allowed", mutate: func(f *onboardingFixture) { f.object["route"].(map[string]any)["allowed_route_ids"] = []any{"route-2"} }},
		{name: "central URL with path", mutate: func(f *onboardingFixture) {
			f.object["central"].(map[string]any)["base_url"] = "https://charlie.example.test/api"
		}},
		{name: "image digest mismatch", mutate: func(f *onboardingFixture) {
			f.object["artifact"].(map[string]any)["manifest_digest"] = "sha256:abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
		}},
		{name: "overprivileged credential", mutate: func(f *onboardingFixture) {
			f.object["credentials"].([]any)[0].(map[string]any)["purpose"] = "configuration_admin"
		}},
		{name: "duplicate replica ordinal", mutate: func(f *onboardingFixture) {
			f.object["credentials"].([]any)[1].(map[string]any)["replica_ordinal"] = 0
		}},
		{name: "duplicate replica credential", mutate: func(f *onboardingFixture) {
			f.object["credentials"].([]any)[1].(map[string]any)["credential"] = f.object["credentials"].([]any)[0].(map[string]any)["credential"]
		}},
		{name: "tampered after signing", mutate: func(f *onboardingFixture) {}, tamper: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newOnboardingFixture(t)
			test.mutate(&fixture)
			raw := fixture.signed(t)
			if test.tamper {
				var object map[string]any
				if err := json.Unmarshal(raw, &object); err != nil {
					t.Fatal(err)
				}
				object["tenant_id"] = "tampered"
				raw, _ = json.Marshal(object)
			}
			if _, err := ValidateOnboardingPackage(raw, fixture.confirmation); err == nil {
				t.Fatal("invalid onboarding package accepted")
			}
		})
	}
}

func TestValidateOnboardingPackageRejectsOversized(t *testing.T) {
	fixture := newOnboardingFixture(t)
	raw := append(fixture.signed(t), make([]byte, MaxOnboardingPackageBytes)...)
	if _, err := ValidateOnboardingPackage(raw, fixture.confirmation); err == nil {
		t.Fatal("oversized package accepted")
	}
}

func TestValidateCentralTrustRequiresHeaderFreeCertSignSelfSignedRoot(t *testing.T) {
	now := time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	certificate := func(subject, issuer string, usage x509.KeyUsage) []byte {
		t.Helper()
		template := &x509.Certificate{
			SerialNumber: big.NewInt(50), Subject: pkix.Name{CommonName: subject},
			Issuer: pkix.Name{CommonName: issuer}, NotBefore: now.Add(-time.Hour),
			NotAfter: now.Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: usage,
		}
		parent := *template
		parent.Subject = pkix.Name{CommonName: issuer}
		der, createErr := x509.CreateCertificate(rand.Reader, template, &parent, publicKey, privateKey)
		if createErr != nil {
			t.Fatal(createErr)
		}
		return der
	}

	missingCertSign := certificate("root", "root", x509.KeyUsageDigitalSignature)
	missingFingerprint := sha256.Sum256(missingCertSign)
	if err := validateCentralTrust(
		"https://charlie.example.test",
		string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: missingCertSign})),
		hex.EncodeToString(missingFingerprint[:]),
	); err == nil {
		t.Fatal("CA without KeyUsageCertSign was accepted")
	}

	validRoot := certificate("root", "root", x509.KeyUsageCertSign)
	validFingerprint := sha256.Sum256(validRoot)
	if err := validateCentralTrust(
		"https://charlie.example.test",
		string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Headers: map[string]string{"X-Unsafe": "value"}, Bytes: validRoot})),
		hex.EncodeToString(validFingerprint[:]),
	); err == nil {
		t.Fatal("PEM headers were accepted in the CA bundle")
	}

	nonSelfIssued := certificate("intermediate", "root", x509.KeyUsageCertSign)
	nonSelfFingerprint := sha256.Sum256(nonSelfIssued)
	if err := validateCentralTrust(
		"https://charlie.example.test",
		string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: nonSelfIssued})),
		hex.EncodeToString(nonSelfFingerprint[:]),
	); err == nil {
		t.Fatal("CA bundle not ending in a self-issued, self-signed root was accepted")
	}
}

func stringContains(value, part string) bool {
	for i := 0; i+len(part) <= len(value); i++ {
		if value[i:i+len(part)] == part {
			return true
		}
	}
	return false
}
