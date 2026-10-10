package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/distribution"
	digest "github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

type assetInput struct {
	ID                   string   `json:"id"`
	Path                 string   `json:"path"`
	File                 string   `json:"file"`
	Category             string   `json:"category"`
	Images               []string `json:"images"`
	Digest               string   `json:"digest"`
	ArtifactSubject      string   `json:"artifact_subject"`
	ArtifactManifestFile string   `json:"artifact_manifest_file"`
	ArtifactConfigFile   string   `json:"artifact_config_file"`
}
type assetIndex struct {
	SchemaVersion      int          `json:"schema_version"`
	RequiredCategories []string     `json:"required_categories"`
	Assets             []assetInput `json:"assets"`
	Capabilities       []string     `json:"capabilities"`
}
type publishedSubject struct {
	Component       string `json:"component"`
	Repository      string `json:"repository"`
	Digest          string `json:"digest"`
	ArchiveDigest   string `json:"archive_digest"`
	LayoutReference string `json:"layout_reference"`
}
type subjectIndex struct {
	SchemaVersion int                     `json:"schema_version"`
	Release       string                  `json:"release"`
	Repository    string                  `json:"repository"`
	Digest        string                  `json:"digest"`
	CatalogDigest string                  `json:"catalog_digest"`
	ArchiveDigest string                  `json:"archive_digest"`
	MediaType     string                  `json:"media_type"`
	Children      []publishedSubject      `json:"children"`
	Assets        []publishedAssetSubject `json:"assets"`
}

type publishedAssetSubject struct {
	ID              string `json:"id"`
	OriginalSubject string `json:"original_subject"`
	Repository      string `json:"repository"`
	Digest          string `json:"digest"`
	ArchiveDigest   string `json:"archive_digest"`
	MediaType       string `json:"media_type"`
	LayoutReference string `json:"layout_reference"`
}

// buildLayout prepares exact OCI bytes without publication. It reuses the
// consumer validator before emitting qualified metadata and deletes failed output.
func buildLayout(result output, charts, directory, repository, assetPath, platformDirectory string) (subjectIndex, error) {
	var empty subjectIndex
	if directory == "" || repository == "" || assetPath == "" || platformDirectory == "" {
		return empty, errors.New("OCI repository, asset closure and platform evidence are required")
	}
	var assets assetIndex
	data, err := boundedFile(assetPath, 1<<20)
	if err != nil || closedJSON(data, &assets) != nil || (assets.SchemaVersion != 2 && assets.SchemaVersion != 3) || assets.Capabilities == nil || assets.Assets == nil {
		return empty, errors.New("asset closure schema is invalid")
	}
	contract := distribution.EmbeddedAssetContract
	if assets.SchemaVersion == 3 {
		contract = distribution.DescriptorAssetContract
	}
	payloadSources := map[string]string{}
	members := map[string][]byte{}
	kinds := map[string]string{}
	catalog, err := json.Marshal(result.Catalog)
	if err != nil {
		return empty, err
	}
	members["catalog.json"] = catalog
	kinds["catalog.json"] = "catalog"
	values := map[string]map[string]any{}
	workloads := map[string][]string{}
	for _, component := range result.Catalog.Components {
		values[component.Slug] = component.Values
		for _, image := range component.Images {
			workloads[image] = append(workloads[image], component.Slug)
		}
	}
	closedAssets := []distribution.Asset{}
	for _, asset := range assets.Assets {
		if asset.Images == nil || (asset.ArtifactSubject == "" && (asset.ArtifactManifestFile != "" || asset.ArtifactConfigFile != "")) {
			return empty, errors.New("asset subject shape invalid")
		}
		for _, image := range asset.Images {
			normalized, err := pinnedImage(image)
			if err != nil || normalized != image {
				return empty, errors.New("external prerequisite image is not canonical and immutable")
			}
			workloads[image] = append(workloads[image], asset.Path)
		}
	}
	inventory := distribution.Inventory{SchemaVersion: 1}
	images := make([]string, 0, len(workloads))
	for image := range workloads {
		images = append(images, image)
	}
	sort.Strings(images)
	for _, image := range images {
		if err := verifyPlatforms(image, platformDirectory); err != nil {
			return empty, err
		}
		sort.Strings(workloads[image])
		inventory.Images = append(inventory.Images, distribution.Image{Reference: image, Platforms: []string{"linux/amd64", "linux/arm64"}, Workloads: workloads[image]})
	}
	members["profiles/builtins.values.json"], _ = json.Marshal(values)
	kinds["profiles/builtins.values.json"] = "values"
	members["profiles/builtins.images.json"], _ = json.Marshal(inventory)
	kinds["profiles/builtins.images.json"] = "image_inventory"
	members["profiles/builtins.render-evidence.json"], _ = json.Marshal(map[string]any{"render_digests": result.RenderDigests, "dependencies": result.Dependencies})
	kinds["profiles/builtins.render-evidence.json"] = "values"
	for _, asset := range assets.Assets {
		closedAsset, err := loadPreparedAsset(asset, contract, members, kinds)
		if err != nil {
			return empty, err
		}
		if closedAsset.DatabaseDescriptor != nil {
			payloadSources[asset.ID] = asset.File
		}
		if closedAsset.ArtifactSubject != "" {
			_, subject, _ := strings.Cut(closedAsset.ArtifactSubject, "@")
			closedAsset.PublishedSubject = repository + "/assets/" + asset.ID + "@" + subject
		}
		closedAssets = append(closedAssets, closedAsset)
	}
	if err := distribution.ValidateAssetClosure(contract, closedAssets, kinds, assets.Capabilities); err != nil {
		return empty, err
	}
	bom := distribution.BOM{SchemaVersion: 1, AssetContractVersion: contract, Assets: closedAssets, Release: result.Catalog.Release, CatalogDigest: sha(catalog), Profiles: []distribution.Profile{{Name: "builtins", Qualification: "qualified", ValuesPath: "profiles/builtins.values.json", InventoryPath: "profiles/builtins.images.json", RequiredCategories: assets.RequiredCategories, BlockedCategories: []string{}, Capabilities: assets.Capabilities}}}
	index := subjectIndex{SchemaVersion: 1, Release: result.Catalog.Release, Repository: repository, CatalogDigest: bom.CatalogDigest, MediaType: distribution.MediaType, Children: []publishedSubject{}, Assets: []publishedAssetSubject{}}
	if err := checkPreparedDatabaseCapacity(directory, closedAssets); err != nil {
		return empty, err
	}
	if contract == distribution.DescriptorAssetContract {
		index.SchemaVersion = 2
	}
	if err := os.Mkdir(directory, 0700); err != nil {
		return empty, errors.New("OCI output directory must be new")
	}
	success := false
	defer func() {
		if !success {
			_ = os.RemoveAll(directory)
		}
	}()
	if err := os.MkdirAll(filepath.Join(directory, "blobs", "sha256"), 0700); err != nil {
		return empty, err
	}
	descriptors := []ocispec.Descriptor{}
	for _, asset := range bom.Assets {
		if asset.ArtifactSubject == "" {
			continue
		}
		manifestBytes := members[asset.ArtifactManifestPath]
		var manifest ocispec.Manifest
		if closedJSON(manifestBytes, &manifest) != nil || len(manifest.Layers) != 1 {
			return empty, errors.New("captured asset manifest invalid")
		}
		if _, err := writeBlob(directory, manifest.Config.MediaType, members[asset.ArtifactConfigPath]); err != nil {
			return empty, err
		}
		if asset.DatabaseDescriptor != nil {
			if err := capturePreparedDatabase(payloadSources[asset.ID], filepath.Join(directory, "blobs", "sha256", manifest.Layers[0].Digest.Encoded()), manifest.Layers[0]); err != nil {
				return empty, err
			}
		} else if _, err := writeBlob(directory, manifest.Layers[0].MediaType, members[asset.MemberPath]); err != nil {
			return empty, err
		}
		descriptor, err := writeBlob(directory, ocispec.MediaTypeImageManifest, manifestBytes)
		if err != nil {
			return empty, err
		}
		reference := "asset_" + asset.ID
		descriptor.Annotations = map[string]string{ocispec.AnnotationRefName: reference}
		descriptors = append(descriptors, descriptor)
		publishedRepository, subject, _ := strings.Cut(asset.PublishedSubject, "@")
		if descriptor.Digest.String() != subject {
			return empty, errors.New("asset publication invented different manifest bytes")
		}
		index.Assets = append(index.Assets, publishedAssetSubject{ID: asset.ID, OriginalSubject: asset.ArtifactSubject, Repository: publishedRepository, Digest: subject, ArchiveDigest: manifest.Layers[0].Digest.String(), MediaType: manifest.Layers[0].MediaType, LayoutReference: reference})
	}
	for _, component := range result.Catalog.Components {
		content, err := boundedFile(filepath.Join(charts, component.Slug+".tgz"), distribution.MaximumArchiveBytes)
		if err != nil || sha(content) != component.Source.ChartDigest {
			return empty, errors.New("chart changed after render")
		}
		if err := distribution.ValidateChartArchive(content); err != nil {
			return empty, err
		}
		name := "charts/" + component.Slug + ".tgz"
		members[name] = content
		kinds[name] = "chart"
		config, _ := json.Marshal(map[string]string{"name": component.Source.Chart, "version": component.Source.Version})
		descriptor, err := writeOCI(directory, "application/vnd.cncf.helm.config.v1+json", config, "application/vnd.cncf.helm.chart.content.v1.tar+gzip", content)
		if err != nil {
			return empty, err
		}
		descriptor.Annotations = map[string]string{ocispec.AnnotationRefName: "chart_" + component.Slug}
		descriptors = append(descriptors, descriptor)
		bom.Charts = append(bom.Charts, distribution.ChartSubject{Component: component.Slug, Repository: repository + "/charts", Digest: descriptor.Digest.String(), ArchiveDigest: sha(content)})
		index.Children = append(index.Children, publishedSubject{Component: component.Slug, Repository: repository + "/charts/" + component.Source.Chart, Digest: descriptor.Digest.String(), ArchiveDigest: sha(content), LayoutReference: "chart_" + component.Slug})
	}
	names := make([]string, 0, len(members))
	for name := range members {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		bom.Members = append(bom.Members, distribution.Member{Path: name, Digest: sha(members[name]), Size: int64(len(members[name])), Category: kinds[name]})
	}
	members["bom.json"], _ = json.Marshal(bom)
	payload, err := packMembers(members)
	if err != nil {
		return empty, err
	}
	if _, _, err := distribution.ReadArchive(payload, bom.Release, bom.CatalogDigest, "builtins"); err != nil {
		return empty, err
	}
	descriptor, err := writeOCI(directory, "application/vnd.oci.empty.v1+json", []byte("{}"), distribution.MediaType, payload)
	if err != nil {
		return empty, err
	}
	descriptor.Annotations = map[string]string{ocispec.AnnotationRefName: "distribution"}
	descriptors = append(descriptors, descriptor)
	index.Digest = descriptor.Digest.String()
	index.ArchiveDigest = sha(payload)
	for name, value := range map[string]any{"index.json": ocispec.Index{Versioned: specs.Versioned{SchemaVersion: 2}, MediaType: ocispec.MediaTypeImageIndex, Manifests: descriptors}, "oci-layout": map[string]string{"imageLayoutVersion": "1.0.0"}, "subjects.json": index} {
		if err := writeJSON(filepath.Join(directory, name), value); err != nil {
			return empty, err
		}
	}
	if err := os.WriteFile(filepath.Join(directory, "catalog.json"), catalog, 0600); err != nil {
		return empty, err
	}
	if err := os.WriteFile(filepath.Join(directory, "distribution.tar.gz"), payload, 0600); err != nil {
		return empty, err
	}
	success = true
	return index, nil
}
func boundedFile(name string, maximum int64) ([]byte, error) {
	info, err := os.Lstat(name)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maximum {
		return nil, errors.New("bounded regular input required")
	}
	file, err := os.Open(name)
	if err != nil {
		return nil, errors.New("input unavailable")
	}
	defer file.Close()
	info, err = file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maximum {
		return nil, errors.New("bounded regular input required")
	}
	data, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil || int64(len(data)) > maximum {
		return nil, errors.New("input exceeds bounds")
	}
	return data, nil
}

func closedJSON(data []byte, target any) error {
	return distribution.DecodeClosedDocument(data, target)
}

func sha(data []byte) string { return fmt.Sprintf("sha256:%x", sha256.Sum256(data)) }
func writeJSON(path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}
func writeBlob(directory, media string, content []byte) (ocispec.Descriptor, error) {
	descriptor := ocispec.Descriptor{MediaType: media, Digest: digest.FromBytes(content), Size: int64(len(content))}
	err := os.WriteFile(filepath.Join(directory, "blobs", "sha256", descriptor.Digest.Encoded()), content, 0600)
	return descriptor, err
}
func writeOCI(directory, configType string, config []byte, media string, payload []byte) (ocispec.Descriptor, error) {
	c, err := writeBlob(directory, configType, config)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	p, err := writeBlob(directory, media, payload)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	manifest := ocispec.Manifest{Versioned: specs.Versioned{SchemaVersion: 2}, MediaType: ocispec.MediaTypeImageManifest, Config: c, Layers: []ocispec.Descriptor{p}}
	data, _ := json.Marshal(manifest)
	return writeBlob(directory, ocispec.MediaTypeImageManifest, data)
}
func packMembers(members map[string][]byte) ([]byte, error) {
	var buffer bytes.Buffer
	compressed := gzip.NewWriter(&buffer)
	writer := tar.NewWriter(compressed)
	names := make([]string, 0, len(members))
	for name := range members {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		data := members[name]
		if err := writer.WriteHeader(&tar.Header{Name: name, Mode: 0600, Typeflag: tar.TypeReg, Size: int64(len(data))}); err != nil {
			return nil, err
		}
		if _, err := writer.Write(data); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	if err := compressed.Close(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

// Exact supplier index, child manifest and configuration bytes qualify platform
// availability. A self-declared architecture list or external layer URL cannot.
func verifyPlatforms(image, directory string) error {
	_, subject, ok := strings.Cut(image, "@")
	if !ok {
		return errors.New("mutable platform subject")
	}
	data, err := boundedFile(filepath.Join(directory, strings.TrimPrefix(subject, "sha256:")+".json"), 1<<20)
	if err != nil || sha(data) != subject {
		return errors.New("image index evidence is missing or changed")
	}
	var index ocispec.Index
	if closedJSON(data, &index) != nil || index.SchemaVersion != 2 || len(index.Manifests) == 0 {
		return errors.New("multiarch image index is required")
	}
	found := map[string]bool{}
	for _, child := range index.Manifests {
		if child.Platform == nil || child.Platform.OS != "linux" {
			continue
		}
		architecture := child.Platform.Architecture
		if architecture != "amd64" && architecture != "arm64" {
			continue
		}
		if child.Digest.Algorithm() != digest.SHA256 || child.Size <= 0 || child.Size > 1<<20 || len(child.URLs) > 0 {
			return errors.New("platform descriptor is invalid")
		}
		content, err := boundedFile(filepath.Join(directory, child.Digest.Encoded()+".json"), 1<<20)
		if err != nil || sha(content) != child.Digest.String() || int64(len(content)) != child.Size {
			return errors.New("image child manifest evidence is missing or changed")
		}
		var manifest ocispec.Manifest
		if closedJSON(content, &manifest) != nil || manifest.SchemaVersion != 2 || len(manifest.Layers) == 0 {
			return errors.New("image child manifest is invalid")
		}
		if manifest.Config.Digest.Algorithm() != digest.SHA256 || manifest.Config.Size <= 0 || manifest.Config.Size > 1<<20 || len(manifest.Config.URLs) > 0 {
			return errors.New("image configuration descriptor is invalid")
		}
		configBytes, err := boundedFile(filepath.Join(directory, manifest.Config.Digest.Encoded()+".json"), 1<<20)
		if err != nil || sha(configBytes) != manifest.Config.Digest.String() || int64(len(configBytes)) != manifest.Config.Size {
			return errors.New("image configuration evidence is missing or changed")
		}
		var config ocispec.Image
		if closedJSON(configBytes, &config) != nil || config.Architecture != architecture || config.OS != "linux" {
			return errors.New("image configuration contradicts its platform descriptor")
		}
		for _, layer := range manifest.Layers {
			if layer.Digest.Algorithm() != digest.SHA256 || layer.Size <= 0 || len(layer.URLs) > 0 {
				return errors.New("image layer is invalid or has an external fallback")
			}
		}
		found[architecture] = true
	}
	if !found["amd64"] || !found["arm64"] {
		return errors.New("image lacks required linux architectures")
	}
	return nil
}

// validateLayout binds preparation metadata to the exact emitted OCI bytes. It
// does not substitute for the vendor signature checked by the runtime loader.
func validateLayout(directory string) error {
	data, err := boundedFile(filepath.Join(directory, "subjects.json"), 1<<20)
	var index subjectIndex
	if err != nil || closedJSON(data, &index) != nil || (index.SchemaVersion != 1 && index.SchemaVersion != 2) || index.MediaType != distribution.MediaType || len(index.Children) == 0 {
		return errors.New("emitted distribution metadata is invalid")
	}
	payload, err := boundedFile(filepath.Join(directory, "distribution.tar.gz"), distribution.MaximumArchiveBytes)
	if err != nil || sha(payload) != index.ArchiveDigest {
		return errors.New("emitted distribution archive identity changed")
	}
	catalog, bom, err := distribution.ReadArchive(payload, index.Release, index.CatalogDigest, "builtins")
	if err != nil {
		return err
	}
	if (index.SchemaVersion == 1 && bom.AssetContractVersion != distribution.EmbeddedAssetContract) || (index.SchemaVersion == 2 && bom.AssetContractVersion != distribution.DescriptorAssetContract) {
		return errors.New("prepared metadata/asset contract version mismatch")
	}
	catalogBytes, err := boundedFile(filepath.Join(directory, "catalog.json"), 4<<20)
	if err != nil || sha(catalogBytes) != index.CatalogDigest || len(index.Children) != len(catalog.Components) {
		return errors.New("emitted catalog identity changed")
	}
	if err := validateSubjectBlob(directory, index.Digest, index.ArchiveDigest, distribution.MediaType); err != nil {
		return err
	}
	indexData, err := boundedFile(filepath.Join(directory, "index.json"), 1<<20)
	var layoutIndex ocispec.Index
	if err != nil || closedJSON(indexData, &layoutIndex) != nil || layoutIndex.SchemaVersion != 2 || len(layoutIndex.Manifests) != len(index.Children)+len(index.Assets)+1 {
		return errors.New("OCI layout index does not close declared subjects")
	}
	expectedReferences := map[string]string{"distribution": index.Digest}
	for _, child := range index.Children {
		expectedReferences[child.LayoutReference] = child.Digest
	}
	for _, asset := range index.Assets {
		expectedReferences[asset.LayoutReference] = asset.Digest
	}
	for _, descriptor := range layoutIndex.Manifests {
		name := descriptor.Annotations[ocispec.AnnotationRefName]
		if expectedReferences[name] == "" || descriptor.Digest.String() != expectedReferences[name] || descriptor.MediaType != ocispec.MediaTypeImageManifest || len(descriptor.URLs) != 0 || len(descriptor.Data) != 0 {
			return errors.New("OCI layout reference differs from declared immutable subject")
		}
		data, err := boundedFile(filepath.Join(directory, "blobs", "sha256", descriptor.Digest.Encoded()), 1<<20)
		if err != nil || descriptor.Size != int64(len(data)) {
			return errors.New("OCI layout subject descriptor size differs")
		}
		delete(expectedReferences, name)
	}
	if len(expectedReferences) != 0 {
		return errors.New("OCI layout reference omitted")
	}
	seen := map[string]bool{}
	for _, child := range index.Children {
		if seen[child.Component] || child.LayoutReference != "chart_"+child.Component {
			return errors.New("emitted chart subject metadata is invalid")
		}
		seen[child.Component] = true
		found := false
		for _, component := range catalog.Components {
			if component.Slug == child.Component && child.Repository == index.Repository+"/charts/"+component.Source.Chart {
				for _, subject := range bom.Charts {
					if subject.Component == child.Component && subject.Repository == index.Repository+"/charts" && subject.Digest == child.Digest && subject.ArchiveDigest == child.ArchiveDigest {
						found = true
					}
				}
			}
		}
		if !found {
			return errors.New("emitted chart subject differs from signed BOM")
		}
		if err := validateSubjectBlob(directory, child.Digest, child.ArchiveDigest, "application/vnd.cncf.helm.chart.content.v1.tar+gzip"); err != nil {
			return err
		}
	}
	if err := validatePublishedAssets(directory, index, bom); err != nil {
		return err
	}
	return nil
}

func validatePublishedAssets(directory string, index subjectIndex, bom distribution.BOM) error {
	expected := map[string]distribution.Asset{}
	for _, asset := range bom.Assets {
		if asset.ArtifactSubject != "" {
			expected[asset.ID] = asset
		}
	}
	if len(expected) != len(index.Assets) {
		return errors.New("emitted prerequisite subjects incomplete")
	}
	for _, asset := range index.Assets {
		original, found := expected[asset.ID]
		if !found || asset.LayoutReference != "asset_"+asset.ID || asset.Repository != index.Repository+"/assets/"+asset.ID || original.PublishedSubject != asset.Repository+"@"+asset.Digest || original.ArtifactSubject != asset.OriginalSubject {
			return errors.New("emitted prerequisite differs from signed original subject")
		}
		memberDigest := ""
		for _, member := range bom.Members {
			if member.Path == original.MemberPath {
				memberDigest = member.Digest
			}
		}
		if bom.AssetContractVersion == distribution.DescriptorAssetContract && original.DatabaseDescriptor != nil {
			memberDigest = original.DatabaseDescriptor.Digest
		}
		if asset.ArchiveDigest != memberDigest {
			return errors.New("emitted prerequisite payload differs from signed BOM")
		}
		if original.DatabaseDescriptor != nil {
			if err := validatePreparedDatabase(directory, asset, original); err != nil {
				return err
			}
		} else if err := validateSubjectBlob(directory, asset.Digest, asset.ArchiveDigest, asset.MediaType); err != nil {
			return err
		}
		delete(expected, asset.ID)
	}
	return nil
}
func validateSubjectBlob(directory, subject, archiveDigest, media string) error {
	parsed, err := digest.Parse(subject)
	if err != nil || parsed.Algorithm() != digest.SHA256 {
		return errors.New("emitted OCI digest is invalid")
	}
	manifestBytes, err := boundedFile(filepath.Join(directory, "blobs", "sha256", parsed.Encoded()), 1<<20)
	if err != nil || sha(manifestBytes) != subject {
		return errors.New("emitted OCI subject identity changed")
	}
	var manifest ocispec.Manifest
	if closedJSON(manifestBytes, &manifest) != nil || manifest.SchemaVersion != 2 || len(manifest.Layers) != 1 || manifest.Layers[0].Digest.String() != archiveDigest || manifest.Layers[0].MediaType != media || len(manifest.Layers[0].URLs) > 0 || len(manifest.Layers[0].Data) > 0 {
		return errors.New("emitted OCI payload descriptor changed")
	}
	configuration := manifest.Config
	if configuration.Digest.Algorithm() != digest.SHA256 || configuration.Size <= 0 || configuration.Size > 1<<20 || len(configuration.URLs) != 0 || len(configuration.Data) != 0 {
		return errors.New("OCI layout config descriptor invalid")
	}
	config, err := boundedFile(filepath.Join(directory, "blobs", "sha256", configuration.Digest.Encoded()), 1<<20)
	if err != nil || sha(config) != configuration.Digest.String() || int64(len(config)) != configuration.Size {
		return errors.New("OCI layout configuration missing or changed")
	}
	layer := manifest.Layers[0]
	if layer.Digest.Algorithm() != digest.SHA256 {
		return errors.New("emitted OCI payload digest is invalid")
	}
	bytes, err := boundedFile(filepath.Join(directory, "blobs", "sha256", layer.Digest.Encoded()), distribution.MaximumArchiveBytes)
	if err != nil || sha(bytes) != archiveDigest || int64(len(bytes)) != layer.Size {
		return errors.New("emitted OCI payload identity changed")
	}
	return nil
}
