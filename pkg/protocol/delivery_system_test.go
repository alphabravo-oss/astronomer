package protocol

import (
	"bytes"
	"testing"
)

func validSystemRelease() DeliverySystemReleaseV2 {
	return DeliverySystemReleaseV2{
		Generation: 1, Version: "v1.0.0",
		ArtifactURL:        "oci://registry.example.test/astronomer/system",
		ArtifactDigest:     "sha256:" + string(bytes.Repeat([]byte{'a'}, 64)),
		DistributionDigest: "sha256:" + string(bytes.Repeat([]byte{'b'}, 64)),
		AgentVersion:       "v1.0.0",
		AgentImage:         "registry.example.test/astronomer/agent@sha256:" + string(bytes.Repeat([]byte{'c'}, 64)),
		MinimumKubernetes:  "v1.33.0", MaximumKubernetes: "v1.35.99", CRDStorageVersion: "v1",
		Interval: "5m", Timeout: "15m",
		Verification: DeliverySystemVerification{Provider: "cosign", OIDCIdentities: []DeliveryOIDCIdentity{{
			Issuer: "https://token.actions.githubusercontent.com", Subject: "https://github.com/example/release/.github/workflows/release.yaml@refs/tags/v1.0.0",
		}}},
	}
}

func TestDeliverySystemReleaseValidation(t *testing.T) {
	base := validSystemRelease()
	if err := base.Validate(); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		edit func(*DeliverySystemReleaseV2)
	}{
		{"mutable image", func(r *DeliverySystemReleaseV2) { r.AgentImage = "example.test/agent:latest" }},
		{"http artifact", func(r *DeliverySystemReleaseV2) { r.ArtifactURL = "https://example.test/system" }},
		{"credentials in URL", func(r *DeliverySystemReleaseV2) { r.ArtifactURL = "oci://user:secret@example.test/system" }},
		{"unsigned", func(r *DeliverySystemReleaseV2) { r.Verification.OIDCIdentities = nil }},
		{"mixed trust", func(r *DeliverySystemReleaseV2) {
			r.Verification.PublicKey = []byte("public")
			r.Verification.KeyFingerprint = "sha256:" + string(bytes.Repeat([]byte{'d'}, 64))
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := base
			value.Verification.OIDCIdentities = append([]DeliveryOIDCIdentity(nil), base.Verification.OIDCIdentities...)
			test.edit(&value)
			if err := value.Validate(); err == nil {
				t.Fatal("expected validation failure")
			}
		})
	}
}

func TestSystemReleaseParticipatesInETagWithoutCredentialBytes(t *testing.T) {
	first := validSystemRelease()
	first.Credential = &DeliveryCredentialMaterial{Version: 1, Data: map[string][]byte{".dockerconfigjson": []byte(`{"auths":{"example.test":{"auth":"first"}}}`)}}
	response := DeliveryStateResponseV2{ProtocolVersion: DeliveryProtocolVersion, SnapshotGeneration: 1, FullSnapshot: true, System: &first, CredentialEpoch: 1}
	firstETag, err := response.CanonicalETag()
	if err != nil {
		t.Fatal(err)
	}
	response.System.Credential.Data[".dockerconfigjson"] = []byte(`{"auths":{"example.test":{"auth":"second"}}}`)
	secondETag, err := response.CanonicalETag()
	if err != nil {
		t.Fatal(err)
	}
	if firstETag != secondETag {
		t.Fatal("credential bytes changed the observable ETag")
	}
	response.CredentialEpoch++
	thirdETag, err := response.CanonicalETag()
	if err != nil {
		t.Fatal(err)
	}
	if thirdETag == secondETag {
		t.Fatal("credential epoch did not invalidate the ETag")
	}
}
