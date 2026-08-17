// Package releasecontract loads the signed, generated compatibility unit that
// binds the management chart, images, downstream Flux/bundles, and Charlie.
package releasecontract

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
)

const maximumManifestBytes = 1 << 20

var (
	digestPattern    = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
	referencePattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?(?::[0-9]{1,5})?/[a-z0-9][a-z0-9._/-]*@sha256:[a-f0-9]{64}$`)
	versionPattern   = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`)
	mappingIDPattern = regexp.MustCompile(`^[a-f0-9]{16}$`)
)

type Evidence struct {
	Signature  string `json:"signature"`
	SBOM       string `json:"sbom"`
	Provenance string `json:"provenance"`
}

type Artifact struct {
	Name          string   `json:"name"`
	Kind          string   `json:"kind"`
	Reference     string   `json:"reference"`
	ContentDigest string   `json:"content_digest"`
	Evidence      Evidence `json:"evidence"`
}

type RuntimeImage struct {
	SourceReference string `json:"source_reference"`
	Reference       string `json:"reference"`
}

type Manifest struct {
	SchemaVersion int `json:"schema_version"`
	Release       struct {
		Version       string `json:"version"`
		SourceCommit  string `json:"source_commit"`
		GeneratedAt   string `json:"generated_at"`
		InstallMode   string `json:"install_mode"`
		SigningPolicy struct {
			CertificateOIDCIssuer string `json:"certificate_oidc_issuer"`
			CertificateIdentity   string `json:"certificate_identity"`
		} `json:"artifact_signing_policy"`
	} `json:"release"`
	Compatibility struct {
		Kubernetes struct {
			MinimumMinor string `json:"minimum_minor"`
			MaximumMinor string `json:"maximum_minor"`
		} `json:"kubernetes"`
		AgentProtocol struct {
			Name    string `json:"name"`
			Minimum int    `json:"minimum"`
			Maximum int    `json:"maximum"`
		} `json:"agent_protocol"`
		PostgreSQL struct {
			SupportedMajors []int `json:"supported_majors"`
		} `json:"postgresql"`
		Browsers struct {
			SupportPolicy  string `json:"support_policy"`
			MinimumVersion struct {
				Chrome  int    `json:"chrome"`
				Edge    int    `json:"edge"`
				Firefox int    `json:"firefox"`
				Safari  string `json:"safari"`
			} `json:"minimum_versions"`
		} `json:"browsers"`
	} `json:"compatibility"`
	Astronomer struct {
		Chart         Artifact       `json:"chart"`
		Images        []Artifact     `json:"images"`
		RuntimeImages []RuntimeImage `json:"runtime_images"`
	} `json:"astronomer"`
	Flux struct {
		Version      string     `json:"version"`
		Distribution Artifact   `json:"distribution"`
		Controllers  []Artifact `json:"controllers"`
		APIs         []string   `json:"apis"`
	} `json:"flux"`
	BuiltInBundles struct {
		Artifact      Artifact `json:"artifact"`
		CatalogDigest string   `json:"catalog_digest"`
		Components    []struct {
			Slug         string   `json:"slug"`
			Chart        string   `json:"chart"`
			ChartVersion string   `json:"chart_version"`
			ChartDigest  string   `json:"chart_digest"`
			Images       []string `json:"images"`
		} `json:"components"`
	} `json:"built_in_bundles"`
	Charlie struct {
		QualifiedVersion           string   `json:"qualified_version"`
		Artifact                   Artifact `json:"artifact"`
		CapabilityDisclosureDigest string   `json:"capability_disclosure_digest"`
		SigningPolicy              struct {
			CertificateOIDCIssuer string `json:"certificate_oidc_issuer"`
			CertificateIdentity   string `json:"certificate_identity"`
		} `json:"artifact_signing_policy"`
	} `json:"charlie"`
}

// Projection is the narrow release data consumed by registration and system
// rollout. It intentionally contains no registry credentials or mutable tags.
type Projection struct {
	Version                string
	AgentImage             string
	FluxVersion            string
	FluxRepository         string
	FluxDigest             string
	BundleRepository       string
	BundleDigest           string
	MinimumKubernetesMinor string
	MaximumKubernetesMinor string
	CertificateOIDCIssuer  string
	CertificateIdentity    string
}

type mirrorMapping struct {
	SchemaVersion         int               `json:"schema_version"`
	ReleaseVersion        string            `json:"release_version"`
	ReleaseManifestDigest string            `json:"release_manifest_digest"`
	DestinationRegistry   string            `json:"destination_registry"`
	RegistryRewrites      map[string]string `json:"registry_rewrites"`
	Entries               []struct {
		ID       string `json:"id"`
		Kind     string `json:"kind"`
		Source   string `json:"source"`
		Target   string `json:"target"`
		Digest   string `json:"digest"`
		CopyTool string `json:"copy_tool"`
	} `json:"entries"`
}

func Load(path, expectedVersion string) (Manifest, Projection, error) {
	file, err := os.Open(path)
	if err != nil {
		return Manifest{}, Projection{}, fmt.Errorf("open release manifest: %w", err)
	}
	defer func() { _ = file.Close() }()
	limited := io.LimitReader(file, maximumManifestBytes+1)
	payload, err := io.ReadAll(limited)
	if err != nil {
		return Manifest{}, Projection{}, fmt.Errorf("read release manifest: %w", err)
	}
	if len(payload) > maximumManifestBytes {
		return Manifest{}, Projection{}, errors.New("release manifest exceeds 1 MiB")
	}
	var manifest Manifest
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, Projection{}, fmt.Errorf("decode release manifest: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Manifest{}, Projection{}, errors.New("release manifest has trailing data")
	}
	projection, err := manifest.Validate(expectedVersion)
	if err != nil {
		return Manifest{}, Projection{}, err
	}
	return manifest, projection, nil
}

// ApplyMirrorMapping validates a separately signed air-gap mapping against the
// exact release-manifest bytes, then rewrites only the three runtime subjects
// consumed by registration/system delivery. Every target must retain the
// source digest; mutable tags and unlisted rewrites are impossible.
func ApplyMirrorMapping(path, manifestPath string, release Projection) (Projection, error) {
	payload, err := readBounded(path)
	if err != nil {
		return Projection{}, fmt.Errorf("read release mirror mapping: %w", err)
	}
	var mapping mirrorMapping
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&mapping); err != nil {
		return Projection{}, fmt.Errorf("decode release mirror mapping: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Projection{}, errors.New("release mirror mapping has trailing data")
	}
	manifestPayload, err := readBounded(manifestPath)
	if err != nil {
		return Projection{}, fmt.Errorf("rehash release manifest: %w", err)
	}
	manifestDigest := fmt.Sprintf("sha256:%x", sha256.Sum256(manifestPayload))
	if mapping.SchemaVersion != 1 || mapping.ReleaseVersion != release.Version ||
		mapping.ReleaseManifestDigest != manifestDigest || mapping.DestinationRegistry == "" ||
		len(mapping.RegistryRewrites) == 0 || len(mapping.Entries) == 0 {
		return Projection{}, errors.New("release mirror mapping does not bind this release manifest")
	}
	for _, targetRegistry := range mapping.RegistryRewrites {
		if targetRegistry != mapping.DestinationRegistry {
			return Projection{}, errors.New("release mirror mapping contains an inconsistent registry rewrite")
		}
	}
	rewrites := make(map[string]string, len(mapping.Entries))
	seenIDs := make(map[string]struct{}, len(mapping.Entries))
	for _, entry := range mapping.Entries {
		toolMatchesKind := (entry.Kind == "container_image" && entry.CopyTool == "skopeo") ||
			((entry.Kind == "helm_chart" || entry.Kind == "oci_artifact") && entry.CopyTool == "oras")
		if _, duplicate := seenIDs[entry.ID]; duplicate || !mappingIDPattern.MatchString(entry.ID) || !toolMatchesKind ||
			!referencePattern.MatchString(entry.Source) || !referencePattern.MatchString(entry.Target) ||
			!digestPattern.MatchString(entry.Digest) || !strings.HasSuffix(entry.Source, "@"+entry.Digest) ||
			!strings.HasSuffix(entry.Target, "@"+entry.Digest) || !strings.HasPrefix(entry.Target, mapping.DestinationRegistry+"/") {
			return Projection{}, fmt.Errorf("release mirror mapping entry %q is invalid", entry.ID)
		}
		seenIDs[entry.ID] = struct{}{}
		if existing, duplicate := rewrites[entry.Source]; duplicate && existing != entry.Target {
			return Projection{}, fmt.Errorf("release mirror source %q has conflicting targets", entry.Source)
		}
		rewrites[entry.Source] = entry.Target
	}
	mapRequired := func(source string) (string, error) {
		target, found := rewrites[source]
		if !found {
			return "", fmt.Errorf("release mirror mapping has no target for %q", source)
		}
		return target, nil
	}
	agent, err := mapRequired(release.AgentImage)
	if err != nil {
		return Projection{}, err
	}
	flux, err := mapRequired(release.FluxRepository + "@" + release.FluxDigest)
	if err != nil {
		return Projection{}, err
	}
	bundles, err := mapRequired(release.BundleRepository + "@" + release.BundleDigest)
	if err != nil {
		return Projection{}, err
	}
	release.AgentImage = agent
	release.FluxRepository, release.FluxDigest = splitReference(flux)
	release.BundleRepository, release.BundleDigest = splitReference(bundles)
	return release, nil
}

func (manifest Manifest) Validate(expectedVersion string) (Projection, error) {
	if manifest.SchemaVersion != 1 || manifest.Release.InstallMode != "fresh_only" || !versionPattern.MatchString(manifest.Release.Version) {
		return Projection{}, errors.New("release manifest metadata is invalid")
	}
	expectedVersion = "v" + strings.TrimPrefix(strings.TrimSpace(expectedVersion), "v")
	if manifest.Release.Version != expectedVersion {
		return Projection{}, fmt.Errorf("release manifest version %s does not match binary %s", manifest.Release.Version, expectedVersion)
	}
	expectedSigningIdentity := "https://github.com/alphabravo-oss/astronomer/.github/workflows/release.yaml@refs/tags/" + manifest.Release.Version
	if manifest.Release.SigningPolicy.CertificateOIDCIssuer != "https://token.actions.githubusercontent.com" ||
		manifest.Release.SigningPolicy.CertificateIdentity != expectedSigningIdentity {
		return Projection{}, errors.New("release manifest signing identity is invalid")
	}
	if manifest.Compatibility.AgentProtocol.Name != "astronomer.delivery" ||
		manifest.Compatibility.AgentProtocol.Minimum != 2 || manifest.Compatibility.AgentProtocol.Maximum != 2 ||
		manifest.Compatibility.Kubernetes.MinimumMinor == "" || manifest.Compatibility.Kubernetes.MaximumMinor == "" {
		return Projection{}, errors.New("release manifest compatibility is invalid")
	}
	if manifest.Flux.Version == "" || len(manifest.Flux.Controllers) != 3 || len(manifest.Flux.APIs) < 3 ||
		len(manifest.Astronomer.Images) != 6 || len(manifest.Astronomer.RuntimeImages) < 6 || len(manifest.BuiltInBundles.Components) == 0 ||
		!digestPattern.MatchString(manifest.BuiltInBundles.CatalogDigest) || !digestPattern.MatchString(manifest.Charlie.CapabilityDisclosureDigest) {
		return Projection{}, errors.New("release manifest artifact inventory is incomplete")
	}
	if !versionPattern.MatchString(manifest.Charlie.QualifiedVersion) ||
		manifest.Charlie.SigningPolicy.CertificateOIDCIssuer != "https://token.actions.githubusercontent.com" ||
		!strings.HasPrefix(manifest.Charlie.SigningPolicy.CertificateIdentity, "https://github.com/") ||
		!strings.HasSuffix(manifest.Charlie.SigningPolicy.CertificateIdentity, "@refs/tags/"+manifest.Charlie.QualifiedVersion) {
		return Projection{}, errors.New("release manifest Charlie signing policy is invalid")
	}
	artifacts := append([]Artifact{manifest.Astronomer.Chart, manifest.Flux.Distribution, manifest.BuiltInBundles.Artifact, manifest.Charlie.Artifact}, manifest.Astronomer.Images...)
	artifacts = append(artifacts, manifest.Flux.Controllers...)
	for _, item := range artifacts {
		if err := validateArtifact(item); err != nil {
			return Projection{}, err
		}
	}
	for _, item := range manifest.Astronomer.RuntimeImages {
		if item.SourceReference == "" || !referencePattern.MatchString(item.Reference) {
			return Projection{}, errors.New("release manifest contains an invalid runtime image")
		}
	}
	agentImage := ""
	for _, item := range manifest.Astronomer.Images {
		if item.Name == "agent" {
			agentImage = item.Reference
		}
	}
	if agentImage == "" {
		return Projection{}, errors.New("release manifest has no agent image")
	}
	fluxRepository, fluxDigest := splitReference(manifest.Flux.Distribution.Reference)
	bundleRepository, bundleDigest := splitReference(manifest.BuiltInBundles.Artifact.Reference)
	return Projection{
		Version: manifest.Release.Version, AgentImage: agentImage, FluxVersion: manifest.Flux.Version,
		FluxRepository: fluxRepository, FluxDigest: fluxDigest, BundleRepository: bundleRepository, BundleDigest: bundleDigest,
		MinimumKubernetesMinor: manifest.Compatibility.Kubernetes.MinimumMinor,
		MaximumKubernetesMinor: manifest.Compatibility.Kubernetes.MaximumMinor,
		CertificateOIDCIssuer:  manifest.Release.SigningPolicy.CertificateOIDCIssuer,
		CertificateIdentity:    manifest.Release.SigningPolicy.CertificateIdentity,
	}, nil
}

func validateArtifact(item Artifact) error {
	kindAllowed := item.Kind == "container_image" || item.Kind == "helm_chart" || item.Kind == "oci_artifact"
	if item.Name == "" || !kindAllowed || !referencePattern.MatchString(item.Reference) || !digestPattern.MatchString(item.ContentDigest) ||
		item.Evidence.Signature != "cosign://"+item.Reference ||
		item.Evidence.SBOM != "cosign-attestation://"+item.Reference+"#spdxjson" ||
		item.Evidence.Provenance != "cosign-attestation://"+item.Reference+"#slsaprovenance" {
		return fmt.Errorf("release artifact %q is invalid or lacks signed evidence", item.Name)
	}
	return nil
}

func splitReference(reference string) (string, string) {
	parts := strings.SplitN(reference, "@", 2)
	return parts[0], parts[1]
}

func readBounded(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	payload, err := io.ReadAll(io.LimitReader(file, maximumManifestBytes+1))
	if err != nil {
		return nil, err
	}
	if len(payload) > maximumManifestBytes {
		return nil, errors.New("file exceeds 1 MiB")
	}
	return payload, nil
}
