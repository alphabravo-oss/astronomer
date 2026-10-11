//go:build unix && integration

package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/resolver"
	"github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/sigstore/sigstore-go/pkg/root"
	virtualca "github.com/sigstore/sigstore-go/pkg/testing/ca"
	"github.com/sigstore/sigstore-go/pkg/tlog"
	"github.com/sigstore/sigstore/pkg/cryptoutils"
)

const localQACertificateLabel = "LOCAL QA CERTIFICATE VERIFICATION"

// One owned virtual root signs the entire ephemeral graph. Certificate identity
// fields satisfy the unchanged literal release contract; this never contacts
// GitHub/Fulcio/Rekor and is NOT an OIDC execution or release approval.
type localQACertificateSigner struct {
	virtual        *virtualca.VirtualSigstore
	policy         model.TrustPolicy
	trustDirectory string
	verifier       *resolver.ExecVerifier
	rootBytes      []byte
}

func newLocalQACertificateSigner(directory, identity, issuer string) (*localQACertificateSigner, error) {
	virtual, err := virtualca.NewVirtualSigstore()
	if err != nil {
		return nil, errors.New("local QA certificate root unavailable")
	}
	rekorLogs, ctLogs := virtual.RekorLogs(), virtual.CTLogs()
	for id, log := range rekorLogs {
		raw, e := hex.DecodeString(id)
		if e != nil {
			return nil, e
		}
		log.ID = raw
	}
	for id, log := range ctLogs {
		raw, e := hex.DecodeString(id)
		if e != nil {
			return nil, e
		}
		log.ID = raw
	}
	trusted, err := root.NewTrustedRoot(root.TrustedRootMediaType01, virtual.FulcioCertificateAuthorities(), ctLogs, virtual.TimestampingAuthorities(), rekorLogs)
	if err != nil {
		return nil, err
	}
	encoded, err := trusted.MarshalJSON()
	if err != nil {
		return nil, err
	}
	if err = os.Mkdir(directory, 0700); err != nil {
		return nil, err
	}
	if err = os.WriteFile(filepath.Join(directory, "trusted_root.json"), encoded, 0600); err != nil {
		return nil, err
	}
	verifier, err := resolver.NewExecVerifier(directory)
	if err != nil {
		return nil, err
	}
	policy := model.TrustPolicy{Provider: model.SignatureCosignKeyless, Identity: identity, Issuer: issuer}
	if err = policy.Validate(); err != nil {
		return nil, err
	}
	return &localQACertificateSigner{virtual: virtual, policy: policy, trustDirectory: directory, verifier: verifier, rootBytes: encoded}, nil
}

type localQALegacyBundle struct {
	SignedEntryTimestamp []byte `json:"SignedEntryTimestamp"`
	Payload              struct {
		Body           string `json:"body"`
		IntegratedTime int64  `json:"integratedTime"`
		LogIndex       int64  `json:"logIndex"`
		LogID          string `json:"logID"`
	} `json:"Payload"`
}

func (signer *localQACertificateSigner) signSubject(ctx context.Context, layout, repository, subject string) (string, error) {
	selected := digest.Digest(subject)
	if selected.Validate() != nil || selected.Algorithm() != digest.SHA256 {
		return "", errors.New("local QA subject invalid")
	}
	original, err := boundedFile(filepath.Join(layout, "blobs", "sha256", selected.Encoded()), 1<<20)
	if err != nil || sha(original) != subject {
		return "", errors.New("local QA subject changed")
	}
	payload, err := json.Marshal(map[string]any{"critical": map[string]any{"identity": map[string]string{"docker-reference": repository}, "image": map[string]string{"Docker-manifest-digest": subject}, "type": "cosign container image signature"}, "optional": map[string]string{"local_qa_scope": localQACertificateLabel}})
	if err != nil {
		return "", err
	}
	entity, err := signer.virtual.Sign(signer.policy.Identity, signer.policy.Issuer, payload)
	if err != nil {
		return "", errors.New("local QA certificate signing failed")
	}
	content, err := entity.VerificationContent()
	if err != nil || content.Certificate() == nil {
		return "", errors.New("local QA certificate absent")
	}
	certificate, err := cryptoutils.MarshalCertificateToPEM(content.Certificate())
	if err != nil {
		return "", err
	}
	signature, err := entity.SignatureContent()
	if err != nil {
		return "", err
	}
	encodedSignature := base64.StdEncoding.EncodeToString(signature.MessageSignatureContent().Signature())
	entries, err := entity.TlogEntries()
	if err != nil || len(entries) != 1 {
		return "", errors.New("local QA transparency entry absent")
	}
	entry := entries[0]
	body, ok := entry.Body().(string)
	if !ok || body == "" {
		return "", errors.New("local QA hashedrekord body absent")
	}
	rekor := tlog.RekorPayload{Body: body, IntegratedTime: entry.IntegratedTime().Unix(), LogIndex: entry.LogIndex(), LogID: hex.EncodeToString([]byte(entry.LogKeyID()))}
	set, err := signer.virtual.RekorSignPayload(rekor)
	if err != nil {
		return "", err
	}
	var bundle localQALegacyBundle
	bundle.SignedEntryTimestamp = set
	bundle.Payload.Body = rekor.Body
	bundle.Payload.IntegratedTime = rekor.IntegratedTime
	bundle.Payload.LogIndex = rekor.LogIndex
	bundle.Payload.LogID = rekor.LogID
	bundleBytes, err := json.Marshal(bundle)
	if err != nil {
		return "", err
	}
	input := resolver.VerificationInput{Source: model.Source{Type: model.SourceOCIArtifact, Trust: signer.policy}, Evidence: []resolver.SignatureEvidence{{Payload: payload, Signature: []byte(encodedSignature), Certificate: certificate, Bundle: bundleBytes}}}
	verified, err := signer.verifier.Verify(ctx, input)
	if err != nil || verified.Status != "verified" || verified.Identity != signer.policy.Identity {
		return "", errors.New("local QA canonical certificate verification failed")
	}
	// Actual crypto negatives, before emitting any signature manifest.
	changed := input
	changed.Evidence = []resolver.SignatureEvidence{{Payload: append([]byte(nil), payload...), Signature: []byte(encodedSignature), Certificate: certificate, Bundle: bundleBytes}}
	changed.Evidence[0].Payload[0] = '!'
	if _, err = signer.verifier.Verify(ctx, changed); err == nil {
		return "", errors.New("local QA changed payload accepted")
	}
	changed = input
	changed.Source.Trust.Identity = "https://qa.invalid/wrong-identity"
	if _, err = signer.verifier.Verify(ctx, changed); err == nil {
		return "", errors.New("local QA wrong identity accepted")
	}
	layer, err := writeBlob(layout, "application/vnd.dev.cosign.simplesigning.v1+json", payload)
	if err != nil {
		return "", err
	}
	layer.Annotations = map[string]string{"dev.cosignproject.cosign/signature": encodedSignature, "dev.sigstore.cosign/certificate": string(certificate), "dev.sigstore.cosign/bundle": string(bundleBytes)}
	config, err := writeBlob(layout, ocispec.MediaTypeImageConfig, []byte("{}"))
	if err != nil {
		return "", err
	}
	manifest, err := json.Marshal(ocispec.Manifest{Versioned: specs.Versioned{SchemaVersion: 2}, MediaType: ocispec.MediaTypeImageManifest, Config: config, Layers: []ocispec.Descriptor{layer}})
	if err != nil {
		return "", err
	}
	descriptor, err := writeBlob(layout, ocispec.MediaTypeImageManifest, manifest)
	if err != nil {
		return "", err
	}
	again, err := boundedFile(filepath.Join(layout, "blobs", "sha256", selected.Encoded()), 1<<20)
	if err != nil || !bytes.Equal(original, again) {
		return "", errors.New("local QA original subject changed during signing")
	}
	return descriptor.Digest.String(), nil
}
