package builtin

import (
	"fmt"
	"strings"

	"github.com/google/uuid"

	builtinbundles "github.com/alphabravocompany/astronomer-go/deploy/bundles"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
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

// AuthorizesCatalogAssignment identifies the exact immutable builtin source
// created by this provisioner. A platform-scoped user source is insufficient.
func AuthorizesCatalogAssignment(catalog builtinbundles.Catalog, cluster, project, source uuid.UUID, assignment protocol.DeliveryAssignmentV2) bool {
	if catalog.DistributionIdentity == "" || project != stableID("project", cluster.String()) || assignment.Scope != protocol.DeliveryScopePlatform || assignment.Source.Kind != protocol.DeliverySourceHelmOCI {
		return false
	}
	trust := catalogTrustPolicy(catalog)
	verify := assignment.Source.Verify
	if trust.AllowUnsigned || verify == nil || verify.Provider != "cosign" || assignment.ProjectID != project.String() {
		return false
	}
	switch trust.Provider {
	case model.SignatureCosignKeyless:
		if len(verify.OIDCIdentities) != 1 || verify.OIDCIdentities[0].Issuer != trust.Issuer || verify.OIDCIdentities[0].Subject != trust.Identity || assignment.Source.TrustSecret != "" {
			return false
		}
	case model.SignatureCosignKey:
		if trust.KeyRef == "" || len(verify.OIDCIdentities) != 0 || assignment.Source.TrustSecret != protocol.ObjectNames(project.String(), assignment.DeploymentID).TrustSecret {
			return false
		}
	default:
		return false
	}
	for _, component := range catalog.Components {
		identity, err := catalogSourceIdentityForComponent(project, component, catalog.DistributionIdentity)
		if err == nil && sourceType(identity) == model.SourceHelmOCI && identity.id == source && assignment.Source.URL == identity.url && assignment.Source.Chart == component.Source.Chart && assignment.Source.Revision == component.Source.Version && assignment.Source.Digest == component.Source.ChartDigest {
			return true
		}
	}
	return false
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
