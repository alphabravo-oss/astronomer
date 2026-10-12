package main

import (
	"errors"
	"github.com/alphabravocompany/astronomer-go/internal/releasecontract"
)

// This adapter does not duplicate install-mode, image-role or compatibility
// rules in jq/Python. It authenticates the owned bytes, then calls the same
// validator consumed by the application. Evidence cannot select its own trust.
func validateReleaseManifest(document, evidence, trustDirectory, provider, keyRef, identity, issuer, expectedRelease string) error {
	if expectedRelease == "" {
		return errors.New("exact expected release required")
	}
	payload, err := authenticateDetachedDocument(document, evidence, trustDirectory, provider, keyRef, identity, issuer)
	if err != nil {
		return err
	}
	if len(payload) > 1<<20 {
		return errors.New("release manifest exceeds canonical bounds")
	}
	var manifest releasecontract.Manifest
	if err := closedJSON(payload, &manifest); err != nil {
		return err
	}
	_, err = manifest.Validate(expectedRelease)
	return err
}
