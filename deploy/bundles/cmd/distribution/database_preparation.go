package main

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/distribution"
	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// Input schema3 retains the existing fields but makes File the separately
// streamed database payload and Path the exact parent manifest member. Schema2
// retains the original embedded behavior. Neither input can select larger
// bounds for an unrelated prerequisite role.
func loadPreparedAsset(input assetInput, contract int, members map[string][]byte, kinds map[string]string) (distribution.Asset, error) {
	var empty distribution.Asset
	if _, exists := members[input.Path]; exists || input.Path == "bom.json" {
		return empty, errors.New("asset closure duplicates a reserved member")
	}
	asset := distribution.Asset{ID: input.ID, MemberPath: input.Path, Images: input.Images, ArtifactSubject: input.ArtifactSubject}
	descriptorOnly := contract == distribution.DescriptorAssetContract && input.ArtifactSubject != ""
	if descriptorOnly && input.Category != "vulnerability_database" {
		return empty, errors.New("descriptor input must classify the database role exactly")
	}
	if !descriptorOnly {
		content, err := boundedFile(input.File, distribution.MaximumArchiveBytes)
		if err != nil || sha(content) != input.Digest {
			return empty, errors.New("asset closure input is unavailable")
		}
		if input.Category == "operator" || input.Category == "plugin" || input.Category == "database" || input.Category == "runtime_image" {
			classified := map[string]bool{}
			if err := collectRenderedImages(string(content), classified); err != nil || len(classified) != len(input.Images) {
				return empty, errors.New("prerequisite manifest image set is not closed")
			}
			for _, image := range input.Images {
				if !classified[image] {
					return empty, errors.New("prerequisite manifest differs from declared immutable images")
				}
			}
		}
		members[input.Path], kinds[input.Path] = content, input.Category
	}
	if input.ArtifactSubject == "" {
		return asset, nil
	}
	manifestBytes, err := boundedFile(input.ArtifactManifestFile, 1<<20)
	_, subject, ok := strings.Cut(input.ArtifactSubject, "@")
	if err != nil || !ok || sha(manifestBytes) != subject {
		return empty, errors.New("database manifest capture missing or changed")
	}
	manifestPath := "assets/" + input.ID + ".oci-manifest.json"
	if _, exists := members[manifestPath]; exists {
		return empty, errors.New("database manifest member collision")
	}
	if descriptorOnly {
		var manifest ocispec.Manifest
		if input.Path != manifestPath || closedJSON(manifestBytes, &manifest) != nil || manifest.SchemaVersion != 2 || len(manifest.Layers) != 1 || manifest.Subject != nil {
			return empty, errors.New("descriptor database input requires its exact manifest member")
		}
		layer := manifest.Layers[0]
		if input.Digest != layer.Digest.String() || layer.Digest.Algorithm() != digest.SHA256 || layer.Size <= 0 || layer.Size > distribution.MaximumDatabaseLayerBytes || len(layer.URLs) != 0 || len(layer.Data) != 0 {
			return empty, errors.New("descriptor database payload input differs from exact child")
		}
		asset.DatabaseDescriptor = &distribution.DatabaseDescriptor{Digest: layer.Digest.String(), Size: layer.Size, MediaType: layer.MediaType}
	}
	members[manifestPath], kinds[manifestPath] = manifestBytes, "vulnerability_database"
	asset.ArtifactManifestPath = manifestPath
	config, err := boundedFile(input.ArtifactConfigFile, 1<<20)
	if err != nil {
		return empty, errors.New("database configuration capture missing")
	}
	configPath := "assets/" + input.ID + ".oci-config.json"
	if _, exists := members[configPath]; exists {
		return empty, errors.New("database configuration member collision")
	}
	members[configPath], kinds[configPath] = config, "vulnerability_database"
	asset.ArtifactConfigPath = configPath
	return asset, nil
}

// A finite operation reserve supplements the outer repository capacity gate.
// It grants no override of that gate and does not claim whole-host capacity.
func checkPreparedDatabaseCapacity(directory string, assets []distribution.Asset) error {
	var planned int64
	for _, asset := range assets {
		if asset.DatabaseDescriptor != nil {
			planned += asset.DatabaseDescriptor.Size
		}
	}
	if planned == 0 {
		return nil
	}
	// Parent duplicates, bounded chart/metadata children and index files need
	// staging space in addition to the database layers. Keep a further 512 MiB
	// reserve after that conservative 768 MiB allowance.
	planned += 3 * distribution.MaximumExpandedBytes
	if planned < 0 || planned > distribution.MaximumDatabaseKitBytes {
		return errors.New("database preparation exceeds finite aggregate bound")
	}
	var filesystem syscall.Statfs_t
	if err := syscall.Statfs(filepath.Dir(directory), &filesystem); err != nil || filesystem.Bsize <= 0 {
		return errors.New("database preparation capacity unavailable")
	}
	if filesystem.Bavail > ^uint64(0)/uint64(filesystem.Bsize) {
		return errors.New("database preparation capacity arithmetic invalid")
	}
	available := uint64(filesystem.Bsize) * filesystem.Bavail
	if available < uint64(planned)+uint64(2*distribution.MaximumExpandedBytes) {
		return errors.New("database preparation has insufficient bounded operation headroom")
	}
	return nil
}

// A content-addressed layer may be shared by two roles or already captured as
// an identical small config blob. Reuse requires both the current source and
// existing owned regular file to match the complete immutable claim.
func capturePreparedDatabase(source, destination string, descriptor ocispec.Descriptor) error {
	if _, err := os.Lstat(destination); os.IsNotExist(err) {
		return streamPreparedDatabase(source, destination, descriptor)
	} else if err != nil {
		return errors.New("owned database output unavailable")
	}
	if err := streamPreparedDatabase(source, "", descriptor); err != nil {
		return err
	}
	return streamPreparedDatabase(destination, "", descriptor)
}

// Snapshot exact regular input bytes with a fixed copy buffer. File replacement,
// partial reads, growth and changed hashes reject the output. This never reads
// a multi-GiB database into a byte slice. The caller owns and cleans destination.
func streamPreparedDatabase(source, destination string, descriptor ocispec.Descriptor) error {
	if descriptor.Digest.Algorithm() != digest.SHA256 || descriptor.Size <= 0 || descriptor.Size > distribution.MaximumDatabaseLayerBytes || len(descriptor.URLs) != 0 || len(descriptor.Data) != 0 {
		return errors.New("streamed database descriptor invalid")
	}
	before, err := os.Lstat(source)
	if err != nil || !before.Mode().IsRegular() || before.Size() != descriptor.Size {
		return errors.New("streamed database input must be an exact bounded regular file")
	}
	input, err := os.Open(source)
	if err != nil {
		return errors.New("streamed database input unavailable")
	}
	defer input.Close()
	opened, err := input.Stat()
	if err != nil || !os.SameFile(before, opened) || !opened.Mode().IsRegular() || opened.Size() != descriptor.Size {
		return errors.New("streamed database input changed before capture")
	}
	var target io.Writer = io.Discard
	var output *os.File
	if destination != "" {
		output, err = os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return errors.New("owned database output must be new")
		}
		defer output.Close()
		target = output
	}
	hash := sha256.New()
	written, copyErr := io.CopyBuffer(io.MultiWriter(target, hash), io.LimitReader(input, descriptor.Size+1), make([]byte, 64<<10))
	after, statErr := input.Stat()
	if output != nil {
		if err := output.Close(); err != nil {
			return errors.New("streamed database output close failed")
		}
	}
	if copyErr != nil || statErr != nil || written != descriptor.Size || after.Size() != opened.Size() || !after.ModTime().Equal(opened.ModTime()) || fmt.Sprintf("sha256:%x", hash.Sum(nil)) != descriptor.Digest.String() {
		return errors.New("streamed database payload size/hash changed")
	}
	return nil
}

func validatePreparedDatabase(directory string, published publishedAssetSubject, asset distribution.Asset) error {
	manifestBytes, err := boundedFile(filepath.Join(directory, "blobs", "sha256", strings.TrimPrefix(published.Digest, "sha256:")), 1<<20)
	var manifest ocispec.Manifest
	if err != nil || sha(manifestBytes) != published.Digest || closedJSON(manifestBytes, &manifest) != nil || len(manifest.Layers) != 1 || manifest.Subject != nil {
		return errors.New("prepared database subject changed")
	}
	layer := manifest.Layers[0]
	claim := asset.DatabaseDescriptor
	if claim == nil || layer.Digest.String() != claim.Digest || layer.Size != claim.Size || layer.MediaType != claim.MediaType || published.ArchiveDigest != claim.Digest || published.MediaType != claim.MediaType || manifest.Config.Digest.Algorithm() != digest.SHA256 {
		return errors.New("prepared database differs from signed descriptor")
	}
	config, err := boundedFile(filepath.Join(directory, "blobs", "sha256", manifest.Config.Digest.Encoded()), 1<<20)
	if err != nil || sha(config) != manifest.Config.Digest.String() || int64(len(config)) != manifest.Config.Size || len(manifest.Config.URLs) != 0 || len(manifest.Config.Data) != 0 {
		return errors.New("prepared database configuration changed")
	}
	return streamPreparedDatabase(filepath.Join(directory, "blobs", "sha256", layer.Digest.Encoded()), "", layer)
}
