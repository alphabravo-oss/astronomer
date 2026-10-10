package distribution

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/resolver"
)

// DatabaseAuthority owns physical trust captured before its private canonical
// loader. It is optional constructor composition; production ordinary loading
// keeps its existing API. It does not promote durable epochs, approve publisher
// attempts or prove scan-used DB provenance.
type DatabaseAuthority struct {
	mutex  sync.RWMutex
	loader *Loader
	trust  *resolver.CosignTrustSnapshot
	closed bool
}

type DatabaseAuthoritySnapshot struct {
	Base                     DatabaseBaseSnapshot
	TrustMaterialFingerprint string
}

// VerifiedDatabaseAuthorityBase is provenance from this exact constructor, not
// caller-supplied snapshot fields. Durable acceptance must Revalidate again
// before authority reads and immediately before commit/use.
type VerifiedDatabaseAuthorityBase struct {
	owner       *DatabaseAuthority
	base        *VerifiedDatabaseBase
	fingerprint string
}

func NewDatabaseAuthority(ctx context.Context, options Options) (*DatabaseAuthority, error) {
	if ctx == nil || options.UpdateBinding == nil || options.Development {
		return nil, errors.New("database physical authority constructor incomplete")
	}
	trust, err := resolver.CaptureCosignTrust(ctx, options.TrustDirectory, options.Policy)
	if err != nil {
		return nil, err
	}
	options.TrustDirectory = trust.FrozenDirectory()
	loader, err := New(options)
	if err != nil {
		trust.Close()
		return nil, err
	}
	return &DatabaseAuthority{loader: loader, trust: trust}, nil
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
	if err := authority.trust.Revalidate(ctx); err != nil {
		return nil, err
	}
	base, err := authority.loader.DatabaseBase(ctx)
	if err != nil {
		return nil, err
	}
	if err := authority.trust.Revalidate(ctx); err != nil {
		return nil, err
	}
	return &VerifiedDatabaseAuthorityBase{owner: authority, base: base, fingerprint: authority.trust.Fingerprint()}, nil
}

func (authority *DatabaseAuthority) Revalidate(ctx context.Context, proof *VerifiedDatabaseAuthorityBase) error {
	if authority == nil || proof == nil || proof.owner != authority {
		return errors.New("database physical authority proof differs")
	}
	authority.mutex.RLock()
	defer authority.mutex.RUnlock()
	if authority.closed || proof.base == nil || proof.base.owner != authority.loader || proof.fingerprint != authority.trust.Fingerprint() {
		return errors.New("database physical authority unavailable")
	}
	return authority.trust.Revalidate(ctx)
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
	if authority.closed || proof.base == nil || proof.base.owner != authority.loader || proof.fingerprint != authority.trust.Fingerprint() {
		return nil, errors.New("database physical authority unavailable")
	}
	if err := authority.trust.Revalidate(ctx); err != nil {
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
	update, err := loader.Load(ctx, now)
	if err != nil {
		return nil, err
	}
	if err := authority.trust.Revalidate(ctx); err != nil {
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
	return DatabaseAuthoritySnapshot{Base: base, TrustMaterialFingerprint: proof.fingerprint}, true
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
}
