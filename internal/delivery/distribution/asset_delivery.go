package distribution

import (
	"bytes"
	"context"
	"errors"
	"strings"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	builtinbundles "github.com/alphabravocompany/astronomer-go/deploy/bundles"
)

// Upstream database bytes are unchanged; Astronomer endorses their immutable
// copied OCI subject. Original supplier identity remains in the signed BOM.
func projectArtifactValues(catalog *builtinbundles.Catalog, bom BOM) error {
	locations := map[string]string{}
	for _, asset := range bom.Assets {
		if asset.ArtifactSubject != "" {
			locations[asset.ArtifactSubject] = asset.PublishedSubject
		}
	}
	for index := range catalog.Components {
		component := &catalog.Components[index]
		if component.Source.ChartName() != "trivy-operator" {
			continue
		}
		enabled, trivy, err := ScannerValues(component.Values)
		if err != nil {
			return err
		}
		if !enabled {
			continue
		}
		if _, err := rewriteDatabaseLocations(trivy, exactTable(locations)); err != nil {
			return err
		}
	}
	return nil
}

func (loader *Loader) verifyPublishedAssets(ctx context.Context, bom BOM, members map[string][]byte) error {
	hasDatabase := false
	payloadState := "verified-payload-bytes"
	for _, asset := range bom.Assets {
		if asset.ArtifactSubject == "" {
			continue
		}
		hasDatabase = true
		subject := asset.PublishedSubject
		if loader.options.MirrorPlan != nil {
			found := false
			for _, rewrite := range loader.options.MirrorPlan.Rewrites {
				if rewrite.Kind == "artifact" && rewrite.Source == subject {
					subject = rewrite.Target
					found = true
					break
				}
			}
			if !found {
				return blocked("vulnerability_database", "mirror endorsement omits exact database artifact")
			}
		}
		repository, digest, ok := strings.Cut(subject, "@")
		if !ok || loader.validateLocation(ctx, repository) != nil {
			return blocked("vulnerability_database", "database child location outside operator policy")
		}
		var manifest ocispec.Manifest
		if decodeClosed(members[asset.ArtifactManifestPath], &manifest) != nil || len(manifest.Layers) != 1 {
			return blocked("vulnerability_database", "database child manifest invalid")
		}
		if bom.AssetContractVersion == DescriptorAssetContract {
			metadata, verification, err := loader.fetchMetadata(ctx, loader.options.Client, repository, digest, manifest.Layers[0].MediaType, loader.options.Policy, loader.options.TrustDirectory, MaximumDatabaseLayerBytes)
			if err != nil || verification.Status != "verified" || !bytes.Equal(metadata.Manifest, members[asset.ArtifactManifestPath]) || !bytes.Equal(metadata.Config, members[asset.ArtifactConfigPath]) || !asset.DatabaseDescriptor.matches(metadata.Layer) {
				return blocked("vulnerability_database", "database child metadata signature/content differs from signed claim")
			}
			payloadState = "descriptor-only"
			continue
		}
		payload, verification, err := loader.fetch(ctx, loader.options.Client, repository, digest, manifest.Layers[0].MediaType, loader.options.Policy, loader.options.TrustDirectory, MaximumArchiveBytes)
		if err != nil || verification.Status != "verified" {
			clear(payload)
			return blocked("vulnerability_database", "database child signature invalid or unavailable")
		}
		actual := contentDigest(payload)
		clear(payload)
		if actual != contentDigest(members[asset.MemberPath]) {
			return blocked("vulnerability_database", "database child differs from signed upstream archive")
		}
	}
	if hasDatabase {
		loader.status.DatabasePayloadVerification = payloadState
		loader.status.DatabaseFreshness = "not-assessed"
	}
	return nil
}

// Trivy splits the registry from database repository@digest. Reconstruct and
// rewrite the exact signed subject rather than replacing strings or leaving a
// mutable downstream fetch behind. Returned keys must not be rewritten twice.
func rewriteDatabaseLocations(values map[string]any, locations locationTable) (map[string]bool, error) {
	handled := map[string]bool{}
	for repositoryKey, registryKey := range map[string]string{"dbRepository": "dbRegistry", "javaDbRepository": "javaDbRegistry"} {
		raw, exists := values[repositoryKey]
		if !exists {
			continue
		}
		repository, ok := raw.(string)
		if !ok {
			return nil, errors.New("database repository value must be a string")
		}
		if !strings.Contains(repository, "@sha256:") {
			continue
		}
		registry := ""
		if raw, exists := values[registryKey]; exists {
			var ok bool
			registry, ok = raw.(string)
			if !ok {
				return nil, errors.New("database registry value must be a string")
			}
		}
		source := repository
		if registry != "" {
			source = registry + "/" + repository
		}
		target, found := locations.target(source)
		if !found {
			return nil, blocked("vulnerability_database", "database location outside authenticated asset inventory")
		}
		targetRepository, targetDigest, found := strings.Cut(target, "@")
		_, sourceDigest, sourceOK := strings.Cut(source, "@")
		if !found || !sourceOK || !digestPattern.MatchString(targetDigest) || targetDigest != sourceDigest {
			return nil, errors.New("database rewrite changes content identity")
		}
		host, path, found := strings.Cut(targetRepository, "/")
		if !found {
			return nil, errors.New("database target repository invalid")
		}
		values[registryKey] = host
		values[repositoryKey] = path + "@" + targetDigest
		handled[registryKey], handled[repositoryKey] = true, true
	}
	return handled, nil
}
