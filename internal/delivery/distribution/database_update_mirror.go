package distribution

import (
	"context"
	"errors"
	"strings"
)

// Repository aliases are authenticated operator intent in the immutable base
// endorsement. They do not grant a target network origin, change a vendor
// signature/digest, select a key, or allow chained/ambiguous translations.
func validateDatabaseMirrorRepositories(rewrites []Rewrite) error {
	mappings := map[string]string{}
	targets := map[string]bool{}
	for _, rewrite := range rewrites {
		if rewrite.Kind != "database_repository" {
			continue
		}
		if len(mappings) >= 16 || mappings[rewrite.Source] != "" || targets[rewrite.Target] || rewrite.Source == rewrite.Target {
			return errors.New("mirror database repository mapping duplicate or exceeds bound")
		}
		mappings[rewrite.Source] = rewrite.Target
		targets[rewrite.Target] = true
	}
	for source, target := range mappings {
		if mappings[target] != "" || targets[source] {
			return errors.New("mirror database repository mapping is chained or colliding")
		}
	}
	for _, rewrite := range rewrites {
		if rewrite.Kind != "artifact" {
			continue
		}
		source, _, _ := strings.Cut(rewrite.Source, "@")
		target, _, _ := strings.Cut(rewrite.Target, "@")
		if mapped := mappings[source]; mapped != "" && mapped != target {
			return errors.New("mirror database exact subject and repository destinations disagree")
		}
	}
	return nil
}

func databaseMirrorSubject(plan *MirrorPlan, binding DatabaseUpdateBinding, subject string) (string, error) {
	repository, reference, valid := strings.Cut(subject, "@")
	if !valid || !validDatabaseUpdateSubject(subject, reference) {
		return "", errors.New("database mirror original immutable subject invalid")
	}
	if plan == nil {
		if binding.MirrorEndorsementDigest != "" {
			return "", errors.New("database mirror bound endorsement unavailable")
		}
		return subject, nil
	}
	frozen, err := plan.immutable()
	if err != nil {
		return "", err
	}
	if frozen.identity != binding.MirrorEndorsementDigest || frozen.DistributionDigest != binding.BaseDistributionDigest || frozen.Release != binding.BaseRelease || frozen.CatalogDigest != binding.CatalogDigest {
		return "", errors.New("database mirror endorsement differs from signed base binding")
	}
	destination := ""
	for _, rewrite := range frozen.Rewrites {
		candidate := ""
		if rewrite.Kind == "artifact" && rewrite.Source == subject {
			candidate = rewrite.Target
		}
		if rewrite.Kind == "database_repository" && rewrite.Source == repository {
			candidate = rewrite.Target + "@" + reference
		}
		if candidate == "" {
			continue
		}
		if destination != "" && destination != candidate {
			return "", errors.New("database mirror authenticated destinations disagree")
		}
		destination = candidate
	}
	if destination == "" {
		return subject, nil
	}
	_, targetDigest, found := strings.Cut(destination, "@")
	if !found || targetDigest != reference || !validDatabaseUpdateSubject(destination, reference) {
		return "", errors.New("database mirror target changed immutable content")
	}
	return destination, nil
}

// DatabaseUpdateDestination exposes retrieval DATA from this exact physical
// owner for the protected importer. No caller-selected mapping or policy exists.
// Each actual write/read revalidates the same mounted authority independently.
func (authority *DatabaseAuthority) DatabaseUpdateDestination(ctx context.Context, proof *VerifiedDatabaseAuthorityBase, subject string) (string, error) {
	if err := authority.Revalidate(ctx, proof); err != nil {
		return "", err
	}
	authority.mutex.RLock()
	defer authority.mutex.RUnlock()
	if authority.closed || proof.base == nil || proof.base.owner != authority.loader || proof.fingerprint != authority.physicalFingerprint() {
		return "", errors.New("database mirror physical owner differs")
	}
	base, valid := proof.base.Snapshot()
	if !valid {
		return "", errors.New("database mirror base unavailable")
	}
	target, err := databaseMirrorSubject(authority.loader.options.MirrorPlan, base.Binding, subject)
	if err != nil {
		return "", err
	}
	repository, _, _ := strings.Cut(target, "@")
	if authority.loader.options.Network.ValidateURL(ctx, "https://"+repository) != nil {
		return "", supplierDatabaseError(ctx, "database mirror destination outside captured policy")
	}
	if err := authority.revalidatePhysicalTrust(ctx); err != nil {
		return "", err
	}
	return target, nil
}
