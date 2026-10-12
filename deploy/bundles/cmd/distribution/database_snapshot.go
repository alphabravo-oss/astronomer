package main

import (
	"errors"
	"path/filepath"
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/distribution"
	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// Only exact database descriptors bound by the bounded prepared parent may use
// the streamed snapshot path. Other blobs retain every old allocation bound.
// validateLayout has already bound this parent to the prepared index and graph.
// Preparation is not signature authority; publication separately authenticates
// and verifies every child through the canonical runtime path.
func preparedDatabaseStreams(directory string) (map[string]ocispec.Descriptor, []distribution.Asset, int64, error) {
	data, err := boundedFile(filepath.Join(directory, "subjects.json"), 1<<20)
	var index subjectIndex
	if err != nil || closedJSON(data, &index) != nil {
		return nil, nil, 0, errors.New("prepared database metadata unavailable")
	}
	streams := map[string]ocispec.Descriptor{}
	if index.SchemaVersion == 1 {
		return streams, nil, 2 * distribution.MaximumExpandedBytes, nil
	}
	if index.SchemaVersion != 2 {
		return nil, nil, 0, errors.New("unsupported prepared database metadata version")
	}
	payload, err := boundedFile(filepath.Join(directory, "distribution.tar.gz"), distribution.MaximumArchiveBytes)
	if err != nil || sha(payload) != index.ArchiveDigest {
		return nil, nil, 0, errors.New("prepared database parent changed")
	}
	_, bom, err := distribution.ReadArchive(payload, index.Release, index.CatalogDigest, "builtins")
	if err != nil || bom.AssetContractVersion != distribution.DescriptorAssetContract {
		return nil, nil, 0, errors.New("prepared database claims unavailable")
	}
	for _, asset := range bom.Assets {
		if claim := asset.DatabaseDescriptor; claim != nil {
			streams["blobs/sha256/"+strings.TrimPrefix(claim.Digest, "sha256:")] = ocispec.Descriptor{Digest: digest.Digest(claim.Digest), Size: claim.Size, MediaType: claim.MediaType}
		}
	}
	return streams, bom.Assets, distribution.MaximumDatabaseKitBytes, nil
}
