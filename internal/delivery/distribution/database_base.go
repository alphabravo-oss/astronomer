package distribution

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"

	builtinbundles "github.com/alphabravocompany/astronomer-go/deploy/bundles"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
)

// DatabaseBaseSnapshot describes a verified catalog and frozen constructor
// intent. CandidateSourceCommit is injected build identity, not a source claim
// authenticated by the catalog BOM. Durable composition must separately bind
// the deployed build and recheck current base/source/assignment revocation.
type DatabaseBaseSnapshot struct {
	Binding                                          DatabaseUpdateBinding
	PolicyFingerprint                                string
	ChartSourceURL, Chart, ChartVersion, ChartDigest string
	ScanningEnabled                                  bool
}

// VerifiedDatabaseBase cannot be assembled by a caller or from Loader.Status.
// It proves catalog/constructor binding only, never payload availability, an
// accepted generation, current revocation state or measured scan-used inputs.
type VerifiedDatabaseBase struct {
	owner    *Loader
	snapshot DatabaseBaseSnapshot
}

func (loader *Loader) DatabaseBase(ctx context.Context) (*VerifiedDatabaseBase, error) {
	if loader == nil || ctx == nil || loader.options.UpdateBinding == nil {
		return nil, errors.New("database update base constructor unavailable")
	}
	catalog, err := loader.Load(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("database update base catalog verification failed")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Options and the cached catalog are constructor-owned immutable snapshots.
	// Load returns a deep copy, so callers cannot modify the verified cache.
	binding := *loader.options.UpdateBinding
	if catalog.DistributionIdentity != binding.BaseDistributionDigest || catalog.Release != binding.BaseRelease {
		return nil, errors.New("database update base catalog identity differs")
	}
	component, enabled, err := databaseBaseScanner(catalog, binding.Scanner)
	if err != nil {
		return nil, err
	}
	snapshot := DatabaseBaseSnapshot{Binding: binding, PolicyFingerprint: databasePolicyFingerprint(loader.options.Policy), ChartSourceURL: component.Source.URL, Chart: component.Source.Chart, ChartVersion: component.Source.Version, ChartDigest: component.Source.ChartDigest, ScanningEnabled: component.DefaultEnabled && enabled}
	return &VerifiedDatabaseBase{owner: loader, snapshot: snapshot}, nil
}

func databaseBaseScanner(catalog builtinbundles.Catalog, expected DatabaseScannerCompatibility) (builtinbundles.Component, bool, error) {
	var selected builtinbundles.Component
	found, enabled := false, false
	for _, component := range catalog.Components {
		if component.Source.Chart != "trivy-operator" {
			continue
		}
		if found {
			return selected, false, errors.New("database update base scanner is ambiguous")
		}
		found = true
		active, values, err := ScannerValues(component.Values)
		image, imageOK := values["image"].(map[string]any)
		tag, tagOK := image["tag"].(string)
		if err != nil || !imageOK || !tagOK || tag != expected.Version+"@"+expected.ImageDigest {
			return selected, false, errors.New("database update base scanner differs from reviewed values")
		}
		listed := false
		for _, reference := range component.Images {
			_, observed, valid := strings.Cut(reference, "@")
			listed = listed || valid && observed == expected.ImageDigest
		}
		if !listed {
			return selected, false, errors.New("database update base scanner absent from inventory")
		}
		selected, enabled = component, active
	}
	if !found {
		return selected, false, errors.New("database update base scanner unavailable")
	}
	return selected, enabled, nil
}

// Length framing and a versioned domain bind public policy fields without a
// second JSON serializer. Policies have already passed their canonical bounds.
func databasePolicyFingerprint(policy model.TrustPolicy) string {
	hash := sha256.New()
	for _, field := range []string{"astronomer-database-policy-v1", string(policy.Provider), policy.Identity, policy.Issuer, policy.KeyRef} {
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(field)))
		_, _ = hash.Write(size[:])
		_, _ = hash.Write([]byte(field))
	}
	return fmt.Sprintf("sha256:%x", hash.Sum(nil))
}

func (base *VerifiedDatabaseBase) Snapshot() (DatabaseBaseSnapshot, bool) {
	if base == nil || base.owner == nil || base.snapshot.Binding.validate() != nil || !digestPattern.MatchString(base.snapshot.PolicyFingerprint) || !digestPattern.MatchString(base.snapshot.ChartDigest) {
		return DatabaseBaseSnapshot{}, false
	}
	return base.snapshot, true
}
