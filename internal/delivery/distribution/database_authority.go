package distribution

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/resolver"
)

// DatabaseAuthority owns physical trust captured before its private canonical
// loader. It is optional constructor composition; production ordinary loading
// keeps its existing API. It does not promote durable epochs, approve publisher
// attempts or prove scan-used DB provenance.
type DatabaseAuthority struct {
	mutex       sync.RWMutex
	loader      *Loader
	trust       *resolver.CosignTrustSnapshot
	mirrorTrust *resolver.CosignTrustSnapshot
	registry    *resolver.DatabaseRegistryTransport
	closed      bool
}

type DatabaseAuthoritySnapshot struct {
	Base                          DatabaseBaseSnapshot
	TrustMaterialFingerprint      string
	VendorTrustFingerprint        string
	VendorTrustOriginFingerprint  string
	MirrorPolicyFingerprint       string
	MirrorTrustFingerprint        string
	MirrorTrustOriginFingerprint  string
	RegistryRootsFingerprint      string
	RegistryPolicyFingerprint     string
	RegistryRootOriginFingerprint string
}

// VerifiedDatabaseAuthorityBase is provenance from this exact constructor, not
// caller-supplied snapshot fields. Durable acceptance must Revalidate again
// before authority reads and immediately before commit/use.
type VerifiedDatabaseAuthorityBase struct {
	owner                                             *DatabaseAuthority
	base                                              *VerifiedDatabaseBase
	fingerprint                                       string
	vendorTrust, vendorOrigin                         string
	mirrorPolicy, mirrorTrust, mirrorTrustOrigin      string
	registryRoots, registryPolicy, registryRootOrigin string
}

func NewDatabaseAuthority(ctx context.Context, options Options) (*DatabaseAuthority, error) {
	if ctx == nil || options.UpdateBinding == nil || options.Development {
		return nil, errors.New("database physical authority constructor incomplete")
	}
	trust, err := resolver.CaptureCosignTrust(ctx, options.TrustDirectory, options.Policy)
	if err != nil {
		return nil, err
	}
	mirrorTrust, err := captureDatabaseMirrorTrust(ctx, options.MirrorPlan)
	if err != nil {
		trust.Close()
		return nil, err
	}
	options.TrustDirectory = trust.FrozenDirectory()
	loader, err := New(options)
	if err != nil {
		trust.Close()
		mirrorTrust.Close()
		return nil, err
	}
	return &DatabaseAuthority{loader: loader, trust: trust, mirrorTrust: mirrorTrust}, nil
}

func (authority *DatabaseAuthority) Base(ctx context.Context) (*VerifiedDatabaseAuthorityBase, error) {
	if authority == nil {
		return nil, errors.New("database physical authority unavailable")
	}
	authority.mutex.RLock()
	defer authority.mutex.RUnlock()
	if authority.closed {
		return nil, errors.New("database physical authority unavailable")
	}
	if err := authority.revalidatePhysicalTrust(ctx); err != nil {
		return nil, err
	}
	base, err := authority.loader.DatabaseBase(ctx)
	if err != nil {
		return nil, err
	}
	if err := authority.revalidatePhysicalTrust(ctx); err != nil {
		return nil, err
	}
	roots, policy := authority.registry.Fingerprints()
	return &VerifiedDatabaseAuthorityBase{owner: authority, base: base, fingerprint: authority.physicalFingerprint(), vendorTrust: authority.trust.Fingerprint(), vendorOrigin: authority.trust.OriginFingerprint(), mirrorPolicy: authority.mirrorPolicyFingerprint(), mirrorTrust: authority.mirrorTrust.Fingerprint(), mirrorTrustOrigin: authority.mirrorTrust.OriginFingerprint(), registryRoots: roots, registryPolicy: policy, registryRootOrigin: authority.registry.RootOriginFingerprint()}, nil
}

func (authority *DatabaseAuthority) Revalidate(ctx context.Context, proof *VerifiedDatabaseAuthorityBase) error {
	if authority == nil || proof == nil || proof.owner != authority {
		return errors.New("database physical authority proof differs")
	}
	authority.mutex.RLock()
	defer authority.mutex.RUnlock()
	if authority.closed || proof.base == nil || proof.base.owner != authority.loader || proof.fingerprint != authority.physicalFingerprint() {
		return errors.New("database physical authority unavailable")
	}
	return authority.revalidatePhysicalTrust(ctx)
}

// LoadUpdate supplies the identical frozen canonical trust/binding/client used
// for the catalog. A repository/digest is retrieval data only; it cannot select
// signature policy, mounted material, transport authority or larger lifetimes.
// Both DB roles are conservatively required until actual Java applicability is
// measured. This proves signed descriptors only, not inspected payload metadata.
func (authority *DatabaseAuthority) LoadUpdate(ctx context.Context, proof *VerifiedDatabaseAuthorityBase, repository, digest string, now time.Time) (*AuthenticatedDatabaseUpdate, error) {
	if authority == nil || proof == nil || proof.owner != authority {
		return nil, errors.New("database physical authority proof differs")
	}
	authority.mutex.RLock()
	defer authority.mutex.RUnlock()
	if authority.closed || proof.base == nil || proof.base.owner != authority.loader || proof.fingerprint != authority.physicalFingerprint() {
		return nil, errors.New("database physical authority unavailable")
	}
	if err := authority.revalidatePhysicalTrust(ctx); err != nil {
		return nil, err
	}
	base, valid := proof.base.Snapshot()
	if !valid {
		return nil, errors.New("database physical base unavailable")
	}
	options := authority.loader.options
	loader, err := NewDatabaseUpdateLoader(DatabaseUpdateOptions{Repository: repository, Digest: digest, Binding: base.Binding, Timing: CadenceDatabaseUpdateTiming(), JavaRequired: true, Policy: options.Policy, TrustDirectory: authority.trust.FrozenDirectory(), Network: options.Network, Client: options.Client})
	if err != nil {
		return nil, err
	}
	loader.mirror = options.MirrorPlan
	update, err := loader.Load(ctx, now)
	if err != nil {
		return nil, err
	}
	if err := authority.revalidatePhysicalTrust(ctx); err != nil {
		return nil, err
	}
	return update, nil
}

// Snapshot is historical provenance only; durable callers must Revalidate
// current physical and relational authority before acceptance or use.
func (proof *VerifiedDatabaseAuthorityBase) Snapshot() (DatabaseAuthoritySnapshot, bool) {
	if proof == nil || proof.owner == nil || !digestPattern.MatchString(proof.fingerprint) {
		return DatabaseAuthoritySnapshot{}, false
	}
	base, valid := proof.base.Snapshot()
	if !valid {
		return DatabaseAuthoritySnapshot{}, false
	}
	return DatabaseAuthoritySnapshot{Base: base, TrustMaterialFingerprint: proof.fingerprint, VendorTrustFingerprint: proof.vendorTrust, VendorTrustOriginFingerprint: proof.vendorOrigin, MirrorPolicyFingerprint: proof.mirrorPolicy, MirrorTrustFingerprint: proof.mirrorTrust, MirrorTrustOriginFingerprint: proof.mirrorTrustOrigin, RegistryRootsFingerprint: proof.registryRoots, RegistryPolicyFingerprint: proof.registryPolicy, RegistryRootOriginFingerprint: proof.registryRootOrigin}, true
}

func (authority *DatabaseAuthority) Close() {
	if authority == nil {
		return
	}
	authority.mutex.Lock()
	defer authority.mutex.Unlock()
	if authority.closed {
		return
	}
	authority.closed = true
	authority.trust.Close()
	authority.mirrorTrust.Close()
	authority.registry.Close()
}

// NewPinnedDatabaseAuthority captures the registry roots and destination policy
// before building the canonical private loader. Caller clients are rejected;
// the ordinary distribution constructor and signing-only authority are unchanged.
func NewPinnedDatabaseAuthority(ctx context.Context, options Options, registryOptions resolver.DatabaseRegistryTrustOptions) (*DatabaseAuthority, error) {
	if ctx == nil || options.UpdateBinding == nil || options.Development || options.Client != nil {
		return nil, errors.New("database pinned authority cannot accept caller transport")
	}
	registry, err := resolver.CaptureDatabaseRegistryTransport(ctx, registryOptions)
	if err != nil {
		return nil, err
	}
	options.Client = registry.Client()
	options.Network = registry.Network()
	authority, err := NewDatabaseAuthority(ctx, options)
	if err != nil {
		registry.Close()
		return nil, err
	}
	authority.registry = registry
	if err := authority.revalidatePhysicalTrust(ctx); err != nil {
		authority.Close()
		return nil, err
	}
	return authority, nil
}

func (authority *DatabaseAuthority) revalidatePhysicalTrust(ctx context.Context) error {
	if err := authority.trust.Revalidate(ctx); err != nil {
		return err
	}
	if authority.mirrorTrust != nil {
		if err := authority.mirrorTrust.Revalidate(ctx); err != nil {
			return err
		}
	}
	if authority.registry != nil {
		return authority.registry.Revalidate(ctx)
	}
	return nil
}

func (authority *DatabaseAuthority) physicalFingerprint() string {
	if authority.mirrorTrust != nil {
		return authority.mirroredPhysicalFingerprint()
	}
	if authority.registry == nil {
		return authority.trust.Fingerprint()
	}
	roots, policy := authority.registry.Fingerprints()
	if roots == "" || policy == "" {
		return ""
	}
	return databasePhysicalFingerprint(authority.trust.Fingerprint(), "", "", "", roots, policy, authority.registry.RootOriginFingerprint())
}

// No new verifier or policy is selected by document fields. The original
// operator-origin signature is checked against the captured mounted key before
// canonical loading; changing that key before/after construction fails closed.
func captureDatabaseMirrorTrust(ctx context.Context, plan *MirrorPlan) (*resolver.CosignTrustSnapshot, error) {
	if plan == nil {
		return nil, nil
	}
	frozen, err := plan.immutable()
	if err != nil {
		return nil, err
	}
	proof := frozen.verified
	trust, err := resolver.CaptureCosignTrust(ctx, proof.trustDirectory, proof.policy)
	if err != nil {
		return nil, err
	}
	verifier, err := resolver.NewExecVerifier(trust.FrozenDirectory())
	if err == nil {
		_, err = verifier.Verify(ctx, resolver.VerificationInput{Source: model.Source{Type: model.SourceHelmHTTP, Trust: proof.policy}, Artifact: proof.payload, Signature: proof.signature})
	}
	if err == nil {
		err = trust.Revalidate(ctx)
	}
	if err != nil {
		trust.Close()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("database mirror operator trust changed or unavailable")
	}
	return trust, nil
}

func (authority *DatabaseAuthority) mirroredPhysicalFingerprint() string {
	mirror, origin := authority.mirrorTrust.Fingerprint(), authority.mirrorTrust.OriginFingerprint()
	if mirror == "" || origin == "" {
		return ""
	}
	roots, policy := authority.registry.Fingerprints()
	registryOrigin := authority.registry.RootOriginFingerprint()
	if authority.registry != nil && (roots == "" || policy == "" || registryOrigin == "") {
		return ""
	}
	return databasePhysicalFingerprint(authority.trust.Fingerprint(), authority.loader.options.MirrorPlan.identity, mirror, origin, roots, policy, registryOrigin)
}

// Only the canonical loader-owned verified mirror policy supplies this field.
func (authority *DatabaseAuthority) mirrorPolicyFingerprint() string {
	if authority.mirrorTrust == nil {
		return ""
	}
	return databasePolicyFingerprint(authority.loader.options.MirrorPlan.verified.policy)
}

// The same framing is used for live physical ownership and retained comparison
// DATA. No hash is interpreted as signature, current trust or acceptance.
func databasePhysicalFingerprint(vendor, mirrorIdentity, mirror, mirrorOrigin, roots, policy, registryOrigin string) string {
	fields := []string{"astronomer-database-physical-authority-v3", vendor, mirrorIdentity, mirror, mirrorOrigin, roots, policy, registryOrigin}
	if mirrorIdentity == "" {
		if roots == "" && policy == "" && registryOrigin == "" {
			return vendor
		}
		fields = []string{"astronomer-database-physical-authority-v2", vendor, roots, policy, registryOrigin}
	}
	hash := sha256.New()
	for _, field := range fields {
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(field)))
		_, _ = hash.Write(size[:])
		_, _ = hash.Write([]byte(field))
	}
	return fmt.Sprintf("sha256:%x", hash.Sum(nil))
}
