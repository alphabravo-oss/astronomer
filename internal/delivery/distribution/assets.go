package distribution

import (
	"errors"
	builtinbundles "github.com/alphabravocompany/astronomer-go/deploy/bundles"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"strings"
)

// Asset binds a fixed prerequisite role to a hashed regular member and the
// immutable OCI subjects used by that role. The signed member is the reviewed
// manifest/configuration/tool archive, not a substitute for its runtime image.
type Asset struct {
	DatabaseDescriptor   *DatabaseDescriptor `json:"database_descriptor,omitempty"`
	ID                   string              `json:"id"`
	MemberPath           string              `json:"member_path"`
	Images               []string            `json:"images"`
	ArtifactSubject      string              `json:"artifact_subject"`
	PublishedSubject     string              `json:"published_subject"`
	ArtifactManifestPath string              `json:"artifact_manifest_path"`
	ArtifactConfigPath   string              `json:"artifact_config_path"`
}

var assetRoles = map[string]string{
	"cnpg-operator": "operator", "cnpg-crds": "crd", "postgres-server": "database",
	"barman-cloud-plugin": "plugin", "barman-cloud-tools": "tool", "object-store": "runtime_image",
	"dr-shell": "tool", "valkey": "runtime_image", "dex": "runtime_image", "fluent-bit": "runtime_image",
	"charlie": "runtime_image", "trivy-database": "vulnerability_database", "trivy-java-database": "vulnerability_database",
	"application-catalog": "application_catalog", "distribution-registry": "runtime_image",
}
var capabilityRoles = map[string][]string{
	"database-cnpg":          {"cnpg-operator", "cnpg-crds", "postgres-server", "barman-cloud-plugin", "barman-cloud-tools", "object-store", "dr-shell"},
	"vulnerability-scanning": {"trivy-database", "trivy-java-database"},
	"cache-valkey":           {"valkey"}, "identity-dex": {"dex"}, "logs-fluent-bit": {"fluent-bit"},
	"charlie": {"charlie"}, "application-catalog": {"application-catalog"}, "distribution-registry": {"distribution-registry"},
	"platform-core": {"cnpg-operator", "cnpg-crds", "postgres-server", "barman-cloud-plugin", "barman-cloud-tools", "object-store", "dr-shell", "valkey", "dex", "fluent-bit", "charlie"},
}

func validateAssetDeclarations(version int, assets []Asset, members map[string]string) error {
	if version != EmbeddedAssetContract && version != DescriptorAssetContract {
		return errors.New("unsupported asset contract version")
	}
	seen := map[string]bool{}
	for _, asset := range assets {
		category, ok := assetRoles[asset.ID]
		if !ok || seen[asset.ID] || members[asset.MemberPath] != category {
			return errors.New("distribution prerequisite role/member identity invalid")
		}
		seen[asset.ID] = true
		database := asset.ID == "trivy-database" || asset.ID == "trivy-java-database"
		if (!database || version == EmbeddedAssetContract) && asset.DatabaseDescriptor != nil {
			return errors.New("descriptor is not allowed by asset role/contract")
		}
		if database {
			repository, digest, ok := strings.Cut(asset.ArtifactSubject, "@")
			if !ok || !digestPattern.MatchString(digest) || members[asset.ArtifactManifestPath] != "vulnerability_database" || members[asset.ArtifactConfigPath] != "vulnerability_database" || asset.ArtifactConfigPath == asset.ArtifactManifestPath || len(asset.Images) != 0 {
				return errors.New("distribution database artifact identity incomplete")
			}
			if version == EmbeddedAssetContract {
				if asset.ArtifactConfigPath == asset.MemberPath || asset.ArtifactManifestPath == asset.MemberPath {
					return errors.New("embedded database members collide")
				}
			} else if asset.MemberPath != asset.ArtifactManifestPath || asset.DatabaseDescriptor.validate(asset.ID) != nil {
				return errors.New("descriptor-only database role/member claim invalid")
			}
			if _, err := builtinbundles.NormalizeOCISourceURL("oci://" + repository); err != nil {
				return errors.New("distribution database repository invalid")
			}
			publishedRepository, publishedDigest, found := strings.Cut(asset.PublishedSubject, "@")
			if !found || publishedDigest != digest {
				return errors.New("endorsed database subject changed original content identity")
			}
			if _, err := builtinbundles.NormalizeOCISourceURL("oci://" + publishedRepository); err != nil {
				return errors.New("endorsed database repository invalid")
			}
		} else if asset.ArtifactSubject != "" || asset.PublishedSubject != "" || asset.ArtifactManifestPath != "" || asset.ArtifactConfigPath != "" || (asset.ID != "cnpg-crds" && asset.ID != "application-catalog" && len(asset.Images) == 0) {
			return errors.New("distribution prerequisite role lacks correct immutable OCI subjects")
		}
		images := map[string]bool{}
		for _, image := range asset.Images {
			repository, digest, ok := strings.Cut(image, "@")
			if !ok || !digestPattern.MatchString(digest) || images[image] {
				return errors.New("distribution prerequisite image identity invalid")
			}
			if _, err := builtinbundles.NormalizeOCISourceURL("oci://" + repository); err != nil {
				return errors.New("distribution prerequisite repository invalid")
			}
			images[image] = true
		}
	}
	return nil
}

func validateCapabilityAssets(capabilities []string, assets []Asset) error {
	if capabilities == nil {
		return errors.New("distribution profile must explicitly classify enabled prerequisite capabilities")
	}
	available := map[string]bool{}
	for _, asset := range assets {
		available[asset.ID] = true
	}
	seen := map[string]bool{}
	for _, capability := range capabilities {
		required, ok := capabilityRoles[capability]
		if !ok || seen[capability] {
			return errors.New("distribution prerequisite capability invalid")
		}
		seen[capability] = true
		for _, role := range required {
			if !available[role] {
				return blocked(assetRoles[role], "distribution enabled capability lacks exact prerequisite role")
			}
		}
	}
	return nil
}

// ValidateAssetClosure is the producer/consumer shared prerequisite contract.
func ValidateAssetClosure(version int, assets []Asset, members map[string]string, capabilities []string) error {
	if err := validateAssetDeclarations(version, assets, members); err != nil {
		return err
	}
	return validateCapabilityAssets(capabilities, assets)
}

// RequiredBuiltinCapabilities derives scanner prerequisites from the effective
// chart values. Other platform capabilities are explicitly bound by the signed
// profile supplied by the platform distribution owner.
func RequiredBuiltinCapabilities(catalog builtinbundles.Catalog) ([]string, error) {
	required := false
	for _, component := range catalog.Components {
		if component.Source.Chart != "trivy-operator" {
			continue
		}
		enabled, _, err := ScannerValues(component.Values)
		if err != nil {
			return nil, err
		}
		if enabled {
			required = true
		}
	}
	if required {
		return []string{"vulnerability-scanning"}, nil
	}
	return []string{}, nil
}

// ScannerValues rejects ambiguous Helm truthiness before capability derivation.
// Additive unrelated chart values remain allowed; scanner fields are typed.
func ScannerValues(values map[string]any) (bool, map[string]any, error) {
	trivy := map[string]any{}
	if raw, exists := values["trivy"]; exists {
		var ok bool
		trivy, ok = raw.(map[string]any)
		if !ok || trivy == nil {
			return false, nil, blocked("values", "scanner trivy configuration must be an object")
		}
		for _, key := range []string{"dbRegistry", "dbRepository", "javaDbRegistry", "javaDbRepository"} {
			if raw, exists := trivy[key]; exists {
				if _, ok := raw.(string); !ok {
					return false, nil, blocked("values", "scanner database location must be a string")
				}
			}
		}
	}
	if raw, exists := values["operator"]; exists {
		operator, ok := raw.(map[string]any)
		if !ok || operator == nil {
			return false, nil, blocked("values", "scanner operator configuration must be an object")
		}
		if raw, exists := operator["vulnerabilityScannerEnabled"]; exists {
			enabled, ok := raw.(bool)
			if !ok {
				return false, nil, blocked("values", "scanner enabled configuration must be boolean")
			}
			return enabled, trivy, nil
		}
	}
	return false, trivy, nil
}

func validateArtifactAssets(version int, assets []Asset, members map[string][]byte) error {
	for _, asset := range assets {
		if asset.ArtifactSubject == "" {
			continue
		}
		_, subject, _ := strings.Cut(asset.ArtifactSubject, "@")
		data := members[asset.ArtifactManifestPath]
		var manifest ocispec.Manifest
		if contentDigest(data) != subject || decodeClosed(data, &manifest) != nil || manifest.SchemaVersion != 2 || len(manifest.Layers) != 1 {
			return blocked("vulnerability_database", "database OCI manifest binding invalid")
		}
		layer := manifest.Layers[0]
		if manifest.Config.MediaType == "" || layer.MediaType == "" || len(manifest.Config.MediaType) > 256 || len(layer.MediaType) > 256 || strings.ContainsAny(manifest.Config.MediaType+layer.MediaType, "\r\n \t") {
			return blocked("vulnerability_database", "database descriptor media type invalid")
		}
		payload := members[asset.MemberPath]
		config := members[asset.ArtifactConfigPath]
		if len(config) == 0 || len(config) > 1<<20 || manifest.Config.Digest.String() != contentDigest(config) || manifest.Config.Size != int64(len(config)) || len(manifest.Config.URLs) != 0 || len(manifest.Config.Data) != 0 {
			return blocked("vulnerability_database", "database OCI configuration binding invalid")
		}
		if version == DescriptorAssetContract {
			if manifest.Subject != nil || asset.DatabaseDescriptor.validate(asset.ID) != nil || !asset.DatabaseDescriptor.matches(layer) {
				return blocked("vulnerability_database", "database descriptor differs from authenticated manifest")
			}
			var object map[string]any
			if decodeClosed(config, &object) != nil || object == nil {
				return blocked("vulnerability_database", "database configuration has ambiguous or invalid JSON")
			}
			continue
		}
		if layer.Digest.String() != contentDigest(payload) || layer.Size != int64(len(payload)) || len(layer.URLs) != 0 || len(layer.Data) != 0 {
			return blocked("vulnerability_database", "database OCI payload binding invalid")
		}
	}
	return nil
}

func validateScannerDatabaseBindings(catalog builtinbundles.Catalog, assets []Asset) error {
	subjects := map[string]string{}
	for _, asset := range assets {
		subjects[asset.ID] = asset.ArtifactSubject
	}
	for _, component := range catalog.Components {
		if component.Source.Chart != "trivy-operator" {
			continue
		}
		enabled, trivy, err := ScannerValues(component.Values)
		if err != nil {
			return err
		}
		if !enabled {
			continue
		}
		for key, role := range map[string]string{"dbRepository": "trivy-database", "javaDbRepository": "trivy-java-database"} {
			repository, _ := trivy[key].(string)
			registryKey := "dbRegistry"
			if key == "javaDbRepository" {
				registryKey = "javaDbRegistry"
			}
			registry, _ := trivy[registryKey].(string)
			if registry != "" {
				repository = registry + "/" + repository
			}
			if repository != subjects[role] {
				return blocked("vulnerability_database", "effective scanner repository differs from exact database subject")
			}
		}
	}
	return nil
}
