package resolver

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"path/filepath"
)

// FingerprintMountedPublicMetadata compares protected constructor input to a
// retained authenticated identity. It never interprets or authenticates bytes.
// Missing/changed metadata is a source error, not a signing-material loss proof.
// The identical canonical regular-FD reader pins identity and the 1 MiB bound.
func FingerprintMountedPublicMetadata(ctx context.Context, path string) (string, error) {
	material, err := readMountedPublicMetadata(ctx, path)
	if err != nil {
		return "", err
	}
	defer clearBytes(material)

	digest := sha256.Sum256(material)
	return fmt.Sprintf("sha256:%x", digest), nil
}

func readMountedPublicMetadata(ctx context.Context, path string) ([]byte, error) {
	if ctx == nil || !strictTrustOpeningAvailable || !filepath.IsAbs(path) {
		return nil, errors.New("database mounted public metadata configuration invalid")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path = filepath.Clean(path)
	verifier, err := NewExecVerifier(filepath.Dir(path))
	if err != nil {
		return nil, errors.New("database mounted public metadata origin invalid")
	}
	material, _, err := verifier.readTrustKeyWithHook(ctx, filepath.Base(path), "", nil)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		clearBytes(material)
		return nil, err
	}
	return material, nil
}
