package resolver

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
)

// ErrMountedDatabaseTrustLost denotes a definite current material fact only:
// missing, nonregular, empty/oversized or parser-invalid public material. A
// changed valid fingerprint is comparison DATA for the private durable owner.
// Permission, I/O, unstable reads, invalid configuration and context failures
// never produce this sentinel. It cannot authorize accepting or promoting a base.
var ErrMountedDatabaseTrustLost = errors.New("database mounted public trust lost")

// MountedCosignTrustInspector owns protected constructor references. It reads
// no registry content and creates no frozen directory. Its fingerprints are
// DATA; the private durable owner must compare the locked current base and
// repeat inspection immediately before a reduce-only transaction commits.
type MountedCosignTrustInspector struct {
	verifier          *ExecVerifier
	policy            model.TrustPolicy
	reference, suffix string
}

func NewMountedCosignTrustInspector(directory string, policy model.TrustPolicy) (*MountedCosignTrustInspector, error) {
	if !strictTrustOpeningAvailable || policy.AllowUnsigned || policy.Validate() != nil {
		return nil, errors.New("database mounted signing configuration invalid")
	}
	reference, suffix, err := databaseCosignTrustReference(policy)
	if err != nil {
		return nil, err
	}
	verifier, err := NewExecVerifier(directory)
	if err != nil {
		return nil, errors.New("database mounted signing origin invalid")
	}
	return &MountedCosignTrustInspector{verifier: verifier, policy: policy, reference: reference, suffix: suffix}, nil
}

func (inspector *MountedCosignTrustInspector) Fingerprint(ctx context.Context) (string, error) {
	return inspector.fingerprintWithHook(ctx, nil)
}

// Only a package-private fixture can select the pre-open hook. Production uses
// the identical bounded no-follow/nonblocking FD reader as canonical verification.
func (inspector *MountedCosignTrustInspector) fingerprintWithHook(ctx context.Context, beforeOpen func()) (string, error) {
	if inspector == nil || inspector.verifier == nil {
		return "", errors.New("database mounted signing inspector unavailable")
	}
	material, err := readMountedDatabaseMaterial(ctx, inspector.verifier, inspector.reference, inspector.suffix, beforeOpen)
	if err != nil {
		return "", err
	}
	defer clearBytes(material)
	if err := validateDatabaseCosignTrustMaterial(inspector.policy, material); err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", ErrMountedDatabaseTrustLost
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return cosignTrustFingerprint(inspector.policy, material), nil
}

// OriginFingerprint can compare the configured origin even when material is
// missing, using the retained historical material fingerprint. It grants nothing.
func (inspector *MountedCosignTrustInspector) OriginFingerprint(materialFingerprint string) string {
	if inspector == nil || inspector.verifier == nil || model.Digest(materialFingerprint).Validate() != nil {
		return ""
	}
	return cosignTrustOriginFingerprint(inspector.verifier.trustDir, materialFingerprint)
}

// MountedDatabaseRegistryInspector shares explicit-root parsing and exact
// destination policy semantics with CaptureDatabaseRegistryTransport. It does
// not construct a client or touch system roots, DNS, TLS peers or the registry.
type MountedDatabaseRegistryInspector struct {
	verifier                  *ExecVerifier
	reference, policy, origin string
}

func NewMountedDatabaseRegistryInspector(options DatabaseRegistryTrustOptions) (*MountedDatabaseRegistryInspector, error) {
	if err := validateDatabaseRegistryTrustOptions(options); err != nil {
		return nil, err
	}
	verifier, err := NewExecVerifier(options.TrustDirectory)
	if err != nil {
		return nil, errors.New("database mounted registry configuration invalid")
	}
	// Match the canonical trust reader's reference boundary; CA references do
	// not acquire the signing policy's unrelated DNS-name restriction.
	if options.RootsRef == "" || strings.ContainsAny(options.RootsRef, "/\\\r\n\x00") || filepath.Dir(filepath.Join(verifier.trustDir, options.RootsRef+".pem")) != verifier.trustDir {
		return nil, errors.New("database mounted registry reference invalid")
	}
	policy, origin := databaseRegistryReadBindings(verifier, freezeDatabaseRegistryReadOptions(options.Read), databaseRegistryPolicyFingerprint(freezeDatabaseRegistryNetwork(options.Network)), registryTransportFingerprint("astronomer-database-registry-root-origin-v1", verifier.trustDir, options.RootsRef))
	return &MountedDatabaseRegistryInspector{verifier: verifier, reference: options.RootsRef, policy: policy, origin: origin}, nil
}

func (inspector *MountedDatabaseRegistryInspector) Fingerprint(ctx context.Context) (string, error) {
	if inspector == nil || inspector.verifier == nil {
		return "", errors.New("database mounted registry inspector unavailable")
	}
	material, err := readMountedDatabaseMaterial(ctx, inspector.verifier, inspector.reference, ".pem", nil)
	if err != nil {
		return "", err
	}
	defer clearBytes(material)
	if _, err := httpclient.ParseCertificateAuthorities(material); err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", ErrMountedDatabaseTrustLost
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return registryRootFingerprint(material), nil
}

func (inspector *MountedDatabaseRegistryInspector) Bindings() (policy, origin string) {
	if inspector == nil || inspector.verifier == nil {
		return "", ""
	}
	return inspector.policy, inspector.origin
}

func readMountedDatabaseMaterial(ctx context.Context, verifier *ExecVerifier, reference, suffix string, beforeOpen func()) ([]byte, error) {
	if ctx == nil {
		return nil, errors.New("database mounted inspection context unavailable")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	material, _, err := verifier.readTrustKeyWithHook(ctx, reference, suffix, beforeOpen)
	if err == nil {
		return material, nil
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	// A generic read/open/stat error alone never establishes physical loss.
	// Inspect the exact configured path independently after the failed read.
	info, statErr := os.Lstat(filepath.Join(verifier.trustDir, reference+suffix))
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if knownMountedDatabaseMaterialLoss(info, statErr) {
		return nil, ErrMountedDatabaseTrustLost
	}
	return nil, errors.New("database mounted inspection could not establish material state")
}

func knownMountedDatabaseMaterialLoss(info os.FileInfo, err error) bool {
	if err != nil {
		return errors.Is(err, os.ErrNotExist)
	}
	return info != nil && (!info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxTrustKeyBytes)
}
