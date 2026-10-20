package resolver

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/sigstore/sigstore-go/pkg/root"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
)

// CosignTrustSnapshot captures constructor-owned policy and physical mounted
// material before canonical verification. A mounted rotation invalidates this
// snapshot on Revalidate; it never silently adopts new material or clears a
// durable revocation. Public key/root bytes are not returned to consumers.
type CosignTrustSnapshot struct {
	mutex                                              sync.Mutex
	policy                                             model.TrustPolicy
	mounted, directory, reference, suffix, fingerprint string
	owned                                              os.FileInfo
	closed                                             bool
}

func CaptureCosignTrust(ctx context.Context, directory string, policy model.TrustPolicy) (*CosignTrustSnapshot, error) {
	if !strictTrustOpeningAvailable {
		return nil, errors.New("database physical trust platform unqualified")
	}
	if ctx == nil || policy.AllowUnsigned || policy.Validate() != nil {
		return nil, errors.New("database physical trust constructor invalid")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	reference, suffix, err := databaseCosignTrustReference(policy)
	if err != nil {
		return nil, err
	}
	verifier, err := NewExecVerifier(directory)
	if err != nil {
		return nil, err
	}
	material, _, err := verifier.readTrustKeyWithHook(ctx, reference, suffix, nil)
	if err != nil {
		return nil, err
	}
	defer clearBytes(material)
	err = validateDatabaseCosignTrustMaterial(policy, material)
	if err != nil {
		return nil, errors.New("database physical trust material invalid")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	owned, err := os.MkdirTemp("", "astronomer-database-public-trust-")
	if err != nil {
		return nil, errors.New("database owned trust directory unavailable")
	}
	info, err := os.Lstat(owned)
	if err != nil || !info.IsDir() {
		_ = os.RemoveAll(owned)
		return nil, errors.New("database owned trust directory unavailable")
	}
	if err := os.WriteFile(filepath.Join(owned, reference+suffix), material, 0400); err != nil {
		_ = os.RemoveAll(owned)
		return nil, errors.New("database owned trust snapshot unavailable")
	}
	return &CosignTrustSnapshot{policy: policy, mounted: verifier.trustDir, directory: owned, reference: reference, suffix: suffix, fingerprint: cosignTrustFingerprint(policy, material), owned: info}, nil
}

func cosignTrustFingerprint(policy model.TrustPolicy, material []byte) string {
	hash := sha256.New()
	for _, field := range []string{"astronomer-database-physical-trust-v1", string(policy.Provider), policy.Identity, policy.Issuer, policy.KeyRef} {
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(field)))
		_, _ = hash.Write(size[:])
		_, _ = hash.Write([]byte(field))
	}
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(material)))
	_, _ = hash.Write(size[:])
	_, _ = hash.Write(material)
	return fmt.Sprintf("sha256:%x", hash.Sum(nil))
}

// Revalidate checks both current operator mount and the exact owned snapshot.
// The durable adapter calls this before authority reads and immediately before
// commit/use. Changes require explicit durable promotion of a new constructor.
// Privileged local writers can still change a file after a check; canonical
// verification separately pins each opened regular FD. This is not a filesystem
// sandbox or an assertion that rotations cannot occur after a transaction.
func (snapshot *CosignTrustSnapshot) Revalidate(ctx context.Context) error {
	if snapshot == nil || ctx == nil {
		return errors.New("database physical trust unavailable")
	}
	snapshot.mutex.Lock()
	defer snapshot.mutex.Unlock()
	if snapshot.closed || snapshot.fingerprint == "" {
		return errors.New("database physical trust unavailable")
	}
	for _, directory := range []string{snapshot.mounted, snapshot.directory} {
		verifier, err := NewExecVerifier(directory)
		if err != nil {
			return errors.New("database physical trust unavailable")
		}
		material, _, err := verifier.readTrustKeyWithHook(ctx, snapshot.reference, snapshot.suffix, nil)
		if err != nil {
			return err
		}
		actual := cosignTrustFingerprint(snapshot.policy, material)
		clearBytes(material)
		if actual != snapshot.fingerprint {
			return errors.New("database physical trust changed")
		}
	}
	return ctx.Err()
}

// FrozenDirectory is operator constructor composition, never a signed-data
// selected location. Callers must not mutate it; Revalidate detects changed bytes.
func (snapshot *CosignTrustSnapshot) FrozenDirectory() string {
	if snapshot == nil {
		return ""
	}
	snapshot.mutex.Lock()
	defer snapshot.mutex.Unlock()
	if snapshot.closed {
		return ""
	}
	return snapshot.directory
}

func (snapshot *CosignTrustSnapshot) Fingerprint() string {
	if snapshot == nil {
		return ""
	}
	snapshot.mutex.Lock()
	defer snapshot.mutex.Unlock()
	if snapshot.closed {
		return ""
	}
	return snapshot.fingerprint
}

// OriginFingerprint binds the captured normalized mounted origin to the exact
// policy/material fingerprint without exposing an operator filesystem path.
// Callers retain this immutable value for historical provenance before Close.
func (snapshot *CosignTrustSnapshot) OriginFingerprint() string {
	if snapshot == nil {
		return ""
	}
	snapshot.mutex.Lock()
	defer snapshot.mutex.Unlock()
	if snapshot.closed {
		return ""
	}
	return cosignTrustOriginFingerprint(snapshot.mounted, snapshot.fingerprint)
}

func (snapshot *CosignTrustSnapshot) Close() {
	if snapshot == nil {
		return
	}
	snapshot.mutex.Lock()
	defer snapshot.mutex.Unlock()
	if snapshot.closed {
		return
	}
	snapshot.closed = true
	// A replaced path belongs to its replacer, not this constructor cleanup.
	current, err := os.Lstat(snapshot.directory)
	if err == nil && current.IsDir() && os.SameFile(snapshot.owned, current) {
		_ = os.RemoveAll(snapshot.directory)
	}
}

func databaseCosignTrustReference(policy model.TrustPolicy) (reference, suffix string, err error) {
	if policy.Provider == model.SignatureCosignKeyless {
		return trustedRootRef, ".json", nil
	}
	if policy.Provider == model.SignatureCosignKey {
		return policy.KeyRef, ".pub", nil
	}
	return "", "", errors.New("database physical trust provider unsupported")
}

func validateDatabaseCosignTrustMaterial(policy model.TrustPolicy, material []byte) error {
	if policy.Provider == model.SignatureCosignKey {
		return validateMountedPublicKey(material)
	}
	_, err := root.NewTrustedRootFromJSON(material)
	return err
}

func cosignTrustOriginFingerprint(mounted, fingerprint string) string {
	hash := sha256.New()
	for _, field := range []string{"astronomer-database-trust-origin-v1", mounted, fingerprint} {
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(field)))
		_, _ = hash.Write(size[:])
		_, _ = hash.Write([]byte(field))
	}
	return fmt.Sprintf("sha256:%x", hash.Sum(nil))
}
