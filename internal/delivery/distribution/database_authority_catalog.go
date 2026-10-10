package distribution

import (
	"context"
	"errors"

	builtinbundles "github.com/alphabravocompany/astronomer-go/deploy/bundles"
)

// Catalog returns an owned copy from the same canonical loader that issued the
// opaque base proof. It is compilation input, not permission to mutate durable
// state. The owning transaction must still lock and validate relational rows.
// Current mounted vendor/mirror/TLS trust is checked before and after the load.
func (authority *DatabaseAuthority) Catalog(ctx context.Context, proof *VerifiedDatabaseAuthorityBase) (builtinbundles.Catalog, error) {
	if ctx == nil || authority == nil || proof == nil || proof.owner != authority {
		return builtinbundles.Catalog{}, errors.New("database physical catalog proof unavailable")
	}
	authority.mutex.RLock()
	defer authority.mutex.RUnlock()
	if authority.closed || proof.base == nil || proof.base.owner != authority.loader || proof.fingerprint != authority.physicalFingerprint() {
		return builtinbundles.Catalog{}, errors.New("database physical catalog authority changed")
	}
	if err := authority.revalidatePhysicalTrust(ctx); err != nil {
		return builtinbundles.Catalog{}, err
	}
	snapshot, valid := proof.base.Snapshot()
	if !valid {
		return builtinbundles.Catalog{}, errors.New("database physical catalog base unavailable")
	}
	catalog, err := authority.loader.Load(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return builtinbundles.Catalog{}, ctx.Err()
		}
		return builtinbundles.Catalog{}, errors.New("database physical catalog verification failed")
	}
	scanner, enabled, err := databaseBaseScanner(catalog, snapshot.Binding.Scanner)
	if err != nil || catalog.DistributionIdentity != snapshot.DistributionIdentity || catalog.Release != snapshot.Binding.BaseRelease || scanner.Source.URL != snapshot.ChartSourceURL || scanner.Source.Chart != snapshot.Chart || scanner.Source.Version != snapshot.ChartVersion || scanner.Source.ChartDigest != snapshot.ChartDigest || (scanner.DefaultEnabled && enabled) != snapshot.ScanningEnabled {
		return builtinbundles.Catalog{}, errors.New("database physical catalog differs from base")
	}
	if err := authority.revalidatePhysicalTrust(ctx); err != nil {
		return builtinbundles.Catalog{}, err
	}
	return catalog, nil
}

// RegistryCertificateAuthorities supplies exact constructor-captured public
// roots to the existing builtin provider. DATA never grants signature authority;
// callers must still use this same authority and revalidate before commit/use.
func (authority *DatabaseAuthority) RegistryCertificateAuthorities(ctx context.Context, proof *VerifiedDatabaseAuthorityBase) ([]byte, error) {
	if ctx == nil || authority == nil || proof == nil || proof.owner != authority {
		return nil, errors.New("database physical registry proof unavailable")
	}
	authority.mutex.RLock()
	defer authority.mutex.RUnlock()
	if authority.closed || proof.base == nil || proof.base.owner != authority.loader || proof.fingerprint != authority.physicalFingerprint() || authority.registry == nil {
		return nil, errors.New("database physical registry authority changed")
	}
	if err := authority.revalidatePhysicalTrust(ctx); err != nil {
		return nil, err
	}
	return authority.registry.CertificateAuthorities(ctx)
}
