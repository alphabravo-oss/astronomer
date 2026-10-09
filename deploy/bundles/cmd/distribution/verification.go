package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"path/filepath"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/resolver"
	"github.com/alphabravocompany/astronomer-go/internal/jsoncontract"
)

// verifyDetachedDocument is an offline preparation/import adapter for the same
// production verifier. Signing evidence never selects its own trust material.
func verifyDetachedDocument(document, evidence, trustDirectory, provider, keyRef, identity, issuer string) error {
	if !filepath.IsAbs(trustDirectory) {
		return errors.New("mounted trust directory required")
	}
	policy := model.TrustPolicy{Provider: model.SignatureProvider(provider), KeyRef: keyRef, Identity: identity, Issuer: issuer}
	if err := policy.Validate(); err != nil || policy.AllowUnsigned {
		return errors.New("invalid document trust policy")
	}
	payload, err := boundedFile(document, 4<<20)
	if err != nil {
		return err
	}
	signed, err := boundedFile(evidence, 1<<20)
	if err != nil {
		return err
	}
	input := resolver.VerificationInput{Source: model.Source{Type: model.SourceHelmHTTP, Trust: policy}, Artifact: payload, Signature: signed}
	if bytes.HasPrefix(bytes.TrimSpace(signed), []byte("{")) {
		var bundle map[string]any
		if err := jsoncontract.Decode(signed, &bundle); err != nil {
			return err
		}
		message, ok := bundle["messageSignature"].(map[string]any)
		if !ok {
			return errors.New("signed bundle message required")
		}
		encoded, ok := message["signature"].(string)
		if !ok {
			return errors.New("signed bundle signature required")
		}
		input.Signature, err = base64.StdEncoding.DecodeString(encoded)
		if err != nil || len(input.Signature) == 0 {
			return errors.New("signed bundle signature invalid")
		}
		input.Bundle = signed
	}
	if policy.Provider == model.SignatureCosignKeyless && len(input.Bundle) == 0 {
		return errors.New("keyless signing-time evidence required")
	}
	verifier, err := resolver.NewExecVerifier(trustDirectory)
	if err != nil {
		return err
	}
	_, err = verifier.Verify(context.Background(), input)
	return err
}
