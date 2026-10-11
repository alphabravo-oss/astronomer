package distribution

import (
	"context"
	"errors"
	"strings"

	builtinbundles "github.com/alphabravocompany/astronomer-go/deploy/bundles"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// DatabaseScanImage is authenticated image-graph DATA, never a scan witness.
// The worker must also observe the actual node, imageID, Job/Pod identities,
// scanner termination and final database/report measurements under current
// relational authority before recording any qualified report.
type DatabaseScanImage struct {
	Reference string
	Platforms []DatabaseScanPlatform
}

type DatabaseScanPlatform struct {
	Architecture string
	Digest       string
}

type DatabaseScanImages struct {
	DistributionIdentity string
	Helper, Scanner      DatabaseScanImage
}

type databaseScanImageMember struct {
	SchemaVersion int    `json:"schema_version"`
	Kind          string `json:"kind"`
	Helper        struct {
		Reference string `json:"reference"`
		Index     []byte `json:"index"`
	} `json:"helper"`
	Scanner struct {
		Reference string `json:"reference"`
		Index     []byte `json:"index"`
	} `json:"scanner"`
}

// DatabaseScanImages returns an owned copy from the same signed parent/base
// loader. Absence of the additive measured-scanning capability is an explicit
// refusal, preserving legacy scanner operation with UNKNOWN provenance.
func (authority *DatabaseAuthority) DatabaseScanImages(ctx context.Context, proof *VerifiedDatabaseAuthorityBase) (DatabaseScanImages, error) {
	if err := authority.Revalidate(ctx, proof); err != nil {
		return DatabaseScanImages{}, err
	}
	catalog, err := authority.Catalog(ctx, proof)
	if err != nil {
		return DatabaseScanImages{}, err
	}
	loader := authority.loader
	loader.mutex.Lock()
	if loader.scanImages == nil || loader.scanImages.DistributionIdentity != catalog.DistributionIdentity {
		loader.mutex.Unlock()
		return DatabaseScanImages{}, errors.New("measured scanning helper is absent from authenticated profile")
	}
	copy := *loader.scanImages
	copy.Helper.Platforms = append([]DatabaseScanPlatform(nil), copy.Helper.Platforms...)
	copy.Scanner.Platforms = append([]DatabaseScanPlatform(nil), copy.Scanner.Platforms...)
	loader.mutex.Unlock()
	if err := authority.Revalidate(ctx, proof); err != nil {
		return DatabaseScanImages{}, err
	}
	return copy, nil
}

// ValidateDatabaseScanImageProfile is the shared producer/archive contract.
// Its return is DATA, not a verifier, network permission or scan-used proof.
func ValidateDatabaseScanImageProfile(catalog builtinbundles.Catalog, bom BOM, members map[string][]byte, profileName string) (*DatabaseScanImages, error) {
	enabled := false
	for _, profile := range bom.Profiles {
		if profile.Name == profileName {
			for _, capability := range profile.Capabilities {
				enabled = enabled || capability == "measured-vulnerability-scanning"
			}
		}
	}
	if !enabled {
		return nil, nil
	}
	var selected *Asset
	for index := range bom.Assets {
		if bom.Assets[index].ID == "database-scan-helper" {
			selected = &bom.Assets[index]
		}
	}
	if selected == nil || len(selected.Images) != 1 {
		return nil, errors.New("measured scanning helper image role is incomplete")
	}
	var declared databaseScanImageMember
	if len(members[selected.MemberPath]) > 4<<20 || decodeClosed(members[selected.MemberPath], &declared) != nil || declared.SchemaVersion != 1 || declared.Kind != "AstronomerMeasuredDatabaseScanImages" || declared.Helper.Reference != selected.Images[0] {
		return nil, errors.New("measured scanning helper image contract differs")
	}
	result := &DatabaseScanImages{DistributionIdentity: catalog.DistributionIdentity}
	for _, item := range []struct {
		reference string
		index     []byte
		target    *DatabaseScanImage
	}{{declared.Helper.Reference, declared.Helper.Index, &result.Helper}, {declared.Scanner.Reference, declared.Scanner.Index, &result.Scanner}} {
		image, err := databaseScanImageGraph(item.reference, item.index)
		if err != nil {
			return nil, err
		}
		*item.target = image
	}
	selectedCount := 0
	for _, component := range catalog.Components {
		if component.Source.Chart != "trivy-operator" {
			continue
		}
		selectedCount++
		active, values, err := ScannerValues(component.Values)
		imageValues, valid := values["image"].(map[string]any)
		registry, validRegistry := imageValues["registry"].(string)
		repository, validRepository := imageValues["repository"].(string)
		tag, validTag := imageValues["tag"].(string)
		version, configuredDigest, digestTag := strings.Cut(tag, "@")
		if err != nil || !component.DefaultEnabled || !active || !valid || !validRegistry || !validRepository || registry == "" || repository == "" || !validTag || !digestTag || version == "" || !digestPattern.MatchString(configuredDigest) || registry+"/"+repository+"@"+configuredDigest != result.Scanner.Reference {
			return nil, errors.New("measured scanner differs from exact enabled effective scanner reference")
		}
		listed := false
		for _, image := range component.Images {
			listed = listed || image == result.Scanner.Reference
		}
		if !listed {
			return nil, errors.New("measured scanner image differs from canonical component inventory")
		}
	}
	if selectedCount != 1 {
		return nil, errors.New("measured scanner component is absent or ambiguous")
	}
	return result, nil
}

func databaseScanImageGraph(reference string, data []byte) (DatabaseScanImage, error) {
	repository, digest, found := strings.Cut(reference, "@")
	if !found || !digestPattern.MatchString(digest) {
		return DatabaseScanImage{}, errors.New("measured scan image/index reference invalid")
	}
	if _, err := builtinbundles.NormalizeOCISourceURL("oci://" + repository); err != nil {
		return DatabaseScanImage{}, errors.New("measured scan image repository invalid")
	}
	var index ocispec.Index
	if len(data) == 0 || len(data) > 1<<20 || contentDigest(data) != digest || decodeClosed(data, &index) != nil || index.SchemaVersion != 2 || index.MediaType != ocispec.MediaTypeImageIndex || index.Subject != nil || index.ArtifactType != "" || len(index.Manifests) != 2 {
		return DatabaseScanImage{}, errors.New("measured scan image index is not authenticated closed content")
	}
	result := DatabaseScanImage{Reference: reference}
	seen := map[string]bool{}
	for _, manifest := range index.Manifests {
		platform := manifest.Platform
		if manifest.MediaType != ocispec.MediaTypeImageManifest || manifest.Digest.Validate() != nil || !digestPattern.MatchString(manifest.Digest.String()) || manifest.Size <= 0 || manifest.Size > 4<<20 || len(manifest.URLs) != 0 || len(manifest.Data) != 0 || manifest.ArtifactType != "" || platform == nil || platform.OS != "linux" || platform.OSVersion != "" || len(platform.OSFeatures) != 0 || (platform.Architecture != "amd64" && platform.Architecture != "arm64") || seen[platform.Architecture] || (platform.Variant != "" && (platform.Architecture != "arm64" || platform.Variant != "v8")) {
			return DatabaseScanImage{}, errors.New("measured scan image platform descriptor invalid")
		}
		seen[platform.Architecture] = true
		result.Platforms = append(result.Platforms, DatabaseScanPlatform{Architecture: platform.Architecture, Digest: manifest.Digest.String()})
	}
	return result, nil
}

func (loader *Loader) prepareDatabaseScanImages(ctx context.Context, catalog builtinbundles.Catalog, bom BOM, members map[string][]byte) (*DatabaseScanImages, error) {
	images, err := ValidateDatabaseScanImageProfile(catalog, bom, members, loader.options.Profile)
	if err != nil || images == nil {
		return images, err
	}
	for _, image := range []*DatabaseScanImage{&images.Helper, &images.Scanner} {
		image.Reference, err = loader.projectDatabaseScanImage(ctx, image.Reference)
		if err != nil {
			return nil, err
		}
	}
	return images, nil
}

func (loader *Loader) projectDatabaseScanImage(ctx context.Context, reference string) (string, error) {
	if loader.options.MirrorPlan != nil {
		found := false
		for _, rewrite := range loader.options.MirrorPlan.Rewrites {
			if rewrite.Kind == "image" && rewrite.Source == reference {
				reference, found = rewrite.Target, true
				break
			}
		}
		if !found {
			return "", errors.New("mirror endorsement omits measured scan image")
		}
	}
	repository, _, _ := strings.Cut(reference, "@")
	if err := loader.validateLocation(ctx, repository); err != nil {
		return "", errors.New("measured scan image destination is outside operator policy")
	}
	return reference, nil
}
