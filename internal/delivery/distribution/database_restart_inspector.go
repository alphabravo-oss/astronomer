package distribution

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/resolver"
)

// DatabaseRestartInspectionOptions is protected constructor configuration,
// never feed fields or an HTTP request. It grants reductions only. Production
// startup must inject the same central typed base/scanner and origin configuration.
type DatabaseRestartInspectionOptions struct {
	Distribution Options
	Registry     *resolver.DatabaseRegistryTrustOptions
	Mirror       *DatabaseRestartMirrorReference
}

type DatabaseRestartMirrorReference struct {
	PayloadPath, TrustDirectory, KeyRef string
}

// DatabaseRestartInspector is deliberately independent of Loader, frozen trust
// allocation, a registry client or current signing material. This lets the
// private owner revoke the exact retained base after loss, including on restart.
type DatabaseRestartInspector struct {
	binding                  DatabaseUpdateBinding
	policy                   string
	vendor, mirror           *resolver.MountedCosignTrustInspector
	registry                 *resolver.MountedDatabaseRegistryInspector
	mirrorPolicy, mirrorPath string
}

var ErrNoConfirmedDatabaseRestartTrustLoss = errors.New("database restart has no confirmed public trust loss")

func NewDatabaseRestartInspector(options DatabaseRestartInspectionOptions) (*DatabaseRestartInspector, error) {
	d := options.Distribution
	if d.UpdateBinding == nil || d.Development || d.Client != nil || d.MirrorPlan != nil || d.Policy.AllowUnsigned || d.Policy.Validate() != nil {
		return nil, errors.New("database restart configuration incomplete")
	}
	binding := *d.UpdateBinding
	if binding.validate() != nil || binding.BaseRelease != d.Release || binding.BaseDistributionDigest != d.Digest || binding.CatalogDigest != d.CatalogDigest || binding.Profile != d.Profile || d.Network.Validate() != nil {
		return nil, errors.New("database restart base configuration differs")
	}
	vendor, err := resolver.NewMountedCosignTrustInspector(d.TrustDirectory, d.Policy)
	if err != nil {
		return nil, err
	}
	inspector := &DatabaseRestartInspector{binding: binding, policy: databasePolicyFingerprint(d.Policy), vendor: vendor}
	if options.Registry != nil {
		inspector.registry, err = resolver.NewMountedDatabaseRegistryInspector(*options.Registry)
		if err != nil {
			return nil, err
		}
		configured := *options.Registry
		configured.Network = d.Network
		expectedRegistry, err := resolver.NewMountedDatabaseRegistryInspector(configured)
		if err != nil {
			return nil, err
		}
		actualPolicy, actualOrigin := inspector.registry.Bindings()
		expectedPolicy, expectedOrigin := expectedRegistry.Bindings()
		if actualPolicy != expectedPolicy || actualOrigin != expectedOrigin {
			return nil, errors.New("database restart registry and distribution policy differ")
		}
	} else if d.Network.Restricted {
		return nil, errors.New("database restart restricted registry authority missing")
	}
	if binding.MirrorEndorsementDigest == "" {
		if options.Mirror != nil || d.Network.Restricted {
			return nil, errors.New("database restart mirror configuration differs")
		}
		return inspector, nil
	}
	if options.Mirror == nil || !filepath.IsAbs(options.Mirror.PayloadPath) {
		return nil, errors.New("database restart configured mirror source missing")
	}
	policy := model.TrustPolicy{Provider: model.SignatureCosignKey, KeyRef: options.Mirror.KeyRef}
	inspector.mirror, err = resolver.NewMountedCosignTrustInspector(options.Mirror.TrustDirectory, policy)
	if err != nil {
		return nil, err
	}
	inspector.mirrorPolicy = databasePolicyFingerprint(policy)
	inspector.mirrorPath = filepath.Clean(options.Mirror.PayloadPath)
	return inspector, nil
}

type ObservedDatabaseRestartTrustLoss struct {
	owner    *DatabaseRestartInspector
	snapshot DatabaseAuthoritySnapshot
	expires  time.Time
}

func (inspector *DatabaseRestartInspector) ObserveLoss(ctx context.Context, snapshot DatabaseAuthoritySnapshot) (*ObservedDatabaseRestartTrustLoss, error) {
	if err := inspector.inspectLoss(ctx, snapshot); err != nil {
		return nil, err
	}
	return &ObservedDatabaseRestartTrustLoss{owner: inspector, snapshot: snapshot, expires: time.Now().UTC().Add(30 * time.Second)}, nil
}

func (inspector *DatabaseRestartInspector) RevalidateLoss(ctx context.Context, loss *ObservedDatabaseRestartTrustLoss) error {
	if inspector == nil || loss == nil || loss.owner != inspector || !time.Now().UTC().Before(loss.expires) {
		return errors.New("database restart loss proof differs or expired")
	}
	return inspector.inspectLoss(ctx, loss.snapshot)
}

func (inspector *DatabaseRestartInspector) inspectLoss(ctx context.Context, snapshot DatabaseAuthoritySnapshot) error {
	if ctx == nil || inspector == nil || inspector.vendor == nil {
		return errors.New("database restart inspection owner unavailable")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := inspector.matchConfiguredBase(ctx, snapshot); err != nil {
		return err
	}
	lost := false
	for _, pair := range []struct {
		reader   func(context.Context) (string, error)
		expected string
	}{{inspector.vendor.Fingerprint, snapshot.VendorTrustFingerprint}, {inspector.mirrorFingerprint, snapshot.MirrorTrustFingerprint}, {inspector.registryFingerprint, snapshot.RegistryRootsFingerprint}} {
		actual, err := pair.reader(ctx)
		if err != nil && !errors.Is(err, resolver.ErrMountedDatabaseTrustLost) {
			return err // Unknown/permission/I/O/context never authorizes revocation.
		}
		lost = lost || err != nil || actual != pair.expected
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !lost {
		return ErrNoConfirmedDatabaseRestartTrustLoss
	}
	return nil
}

func (inspector *DatabaseRestartInspector) matchConfiguredBase(ctx context.Context, snapshot DatabaseAuthoritySnapshot) error {
	b := snapshot.Base
	if b.Binding != inspector.binding || b.PolicyFingerprint != inspector.policy || b.DistributionIdentity != mirrorBindingIdentity(b.Binding) || !digestPattern.MatchString(snapshot.VendorTrustFingerprint) || snapshot.VendorTrustOriginFingerprint != inspector.vendor.OriginFingerprint(snapshot.VendorTrustFingerprint) {
		return errors.New("database restart retained base differs from configured vendor identity")
	}
	if inspector.mirror == nil {
		if snapshot.MirrorPolicyFingerprint != "" || snapshot.MirrorTrustFingerprint != "" || snapshot.MirrorTrustOriginFingerprint != "" {
			return errors.New("database restart unexpected retained mirror authority")
		}
	} else {
		if snapshot.MirrorPolicyFingerprint != inspector.mirrorPolicy || !digestPattern.MatchString(snapshot.MirrorTrustFingerprint) || snapshot.MirrorTrustOriginFingerprint != inspector.mirror.OriginFingerprint(snapshot.MirrorTrustFingerprint) {
			return errors.New("database restart configured operator policy or origin differs")
		}
		// Hash comparison only. No unsigned mirror document is decoded/applied.
		identity, err := resolver.FingerprintMountedPublicMetadata(ctx, inspector.mirrorPath)
		if err != nil || identity != b.Binding.MirrorEndorsementDigest {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return errors.New("database restart configured mirror metadata unavailable or changed")
		}
	}
	if inspector.registry == nil {
		if snapshot.RegistryRootsFingerprint != "" || snapshot.RegistryPolicyFingerprint != "" || snapshot.RegistryRootOriginFingerprint != "" {
			return errors.New("database restart unexpected retained registry authority")
		}
	} else {
		policy, origin := inspector.registry.Bindings()
		if snapshot.RegistryPolicyFingerprint != policy || snapshot.RegistryRootOriginFingerprint != origin || !digestPattern.MatchString(snapshot.RegistryRootsFingerprint) {
			return errors.New("database restart configured registry policy or origin differs")
		}
	}
	if snapshot.TrustMaterialFingerprint != databasePhysicalFingerprint(snapshot.VendorTrustFingerprint, b.Binding.MirrorEndorsementDigest, snapshot.MirrorTrustFingerprint, snapshot.MirrorTrustOriginFingerprint, snapshot.RegistryRootsFingerprint, snapshot.RegistryPolicyFingerprint, snapshot.RegistryRootOriginFingerprint) {
		return errors.New("database restart retained physical framing differs")
	}
	return nil
}

func (inspector *DatabaseRestartInspector) mirrorFingerprint(ctx context.Context) (string, error) {
	if inspector.mirror == nil {
		return "", ctx.Err()
	}
	return inspector.mirror.Fingerprint(ctx)
}

func (inspector *DatabaseRestartInspector) registryFingerprint(ctx context.Context) (string, error) {
	if inspector.registry == nil {
		return "", ctx.Err()
	}
	return inspector.registry.Fingerprint(ctx)
}
