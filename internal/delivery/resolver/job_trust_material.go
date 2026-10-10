package resolver

import (
	"context"
	"errors"
)

// PublicJobMaterial copies the canonical captured signing material only after
// current mounted and frozen origins pass the SAME snapshot check. The caller
// may deliver this public root/key to its owned process; copying grants no trust
// policy, runtime identity, database acceptance or witness authority.
func (snapshot *CosignTrustSnapshot) PublicJobMaterial(ctx context.Context) ([]byte, error) {
	if ctx == nil || snapshot == nil {
		return nil, errors.New("owned Job signing material unavailable")
	}
	if err := snapshot.Revalidate(ctx); err != nil {
		return nil, err
	}
	snapshot.mutex.Lock()
	if snapshot.closed {
		snapshot.mutex.Unlock()
		return nil, errors.New("owned Job signing material unavailable")
	}
	verifier, err := NewExecVerifier(snapshot.directory)
	if err != nil {
		snapshot.mutex.Unlock()
		return nil, errors.New("owned Job signing origin unavailable")
	}
	material, _, err := verifier.readTrustKeyWithHook(ctx, snapshot.reference, snapshot.suffix, nil)
	if err == nil && cosignTrustFingerprint(snapshot.policy, material) != snapshot.fingerprint {
		err = errors.New("owned Job signing material changed")
	}
	snapshot.mutex.Unlock()
	if err == nil {
		err = snapshot.Revalidate(ctx)
	}
	if err != nil {
		clearBytes(material)
		return nil, err
	}
	return material, nil
}

// ReadMountedJobMetadata is the same canonical regular-FD, no-follow,
// nonblocking and stable identity reader used by FingerprintMountedPublicMetadata.
// It is restricted to public release/mirror mapping comparison DATA by the
// private constructor; credentials use the independently typed transport path.
func ReadMountedJobMetadata(ctx context.Context, path string) ([]byte, error) {
	return readMountedPublicMetadata(ctx, path)
}
