package builtin

import (
	"fmt"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
	"strings"

	builtinbundles "github.com/alphabravocompany/astronomer-go/deploy/bundles"
	"github.com/google/uuid"
)

func catalogSourceIdentityForComponent(project uuid.UUID, component builtinbundles.Component, distributionIdentity string) (sourceIdentity, error) {
	if distributionIdentity == "" || component.Source.OriginalURL == "" {
		return catalogSourceIdentity(project, component.Source.URL)
	}
	normalized, err := builtinbundles.NormalizeOCISourceURL(component.Source.URL)
	if err != nil {
		return sourceIdentity{}, err
	}
	identity := sourceIdentityFromCanonical(project, normalized)
	identity.kind = model.SourceHelmOCI
	identity.id = stableID("source", project.String(), normalized, distributionIdentity)
	identity.name = fmt.Sprintf("astronomer-builtins-source-%s", stableID(normalized, distributionIdentity).String())
	return identity, nil
}

// Source-development keeps historical deterministic IDs. A verified release or
// mirror transition adds its authenticated identity, preserving old rows and
// stable target ownership while producing a new immutable version.
func componentVersionIdentity(bundle uuid.UUID, release string, component builtinbundles.Component, distributionIdentity string) (uuid.UUID, string) {
	parts := []string{"version", bundle.String(), release, component.Source.Version, component.Source.ChartDigest}
	label := strings.TrimPrefix(release, "v") + "+" + component.Source.Version
	if distributionIdentity != "" {
		parts = append(parts, distributionIdentity)
		label += fmt.Sprintf(".distribution.%s", stableID(distributionIdentity).String())
	}
	return stableID(parts...), label
}

func catalogTrustPolicy(catalog builtinbundles.Catalog) model.TrustPolicy {
	if catalog.DistributionIdentity == "" {
		return model.TrustPolicy{AllowUnsigned: true}
	}
	return model.TrustPolicy{Provider: model.SignatureProvider(catalog.VerificationProvider), Identity: catalog.VerificationIdentity, Issuer: catalog.VerificationIssuer, KeyRef: catalog.VerificationKeyRef}
}
func sourceDescription(source sourceIdentity) string {
	return systemSourceDescription
}

func sourceType(source sourceIdentity) model.SourceType {
	if source.kind == "" {
		return model.SourceHelmHTTP
	}
	return source.kind
}
