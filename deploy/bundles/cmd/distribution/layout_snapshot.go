package main

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/distribution"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// Snapshot only the closed prepared graph into an owned directory before any
// network write. Revalidation binds the copied bytes, so changes to operator
// input files cannot alter publication after preparation has been accepted.
func snapshotPreparedLayout(directory string) (string, func(), error) {
	if err := validateLayout(directory); err != nil {
		return "", nil, err
	}
	layoutBytes, err := boundedFile(filepath.Join(directory, "oci-layout"), 1024)
	var layout struct {
		ImageLayoutVersion string `json:"imageLayoutVersion"`
	}
	if err != nil || closedJSON(layoutBytes, &layout) != nil || layout.ImageLayoutVersion != "1.0.0" {
		return "", nil, errors.New("prepared OCI layout version invalid")
	}
	indexBytes, err := boundedFile(filepath.Join(directory, "index.json"), 1<<20)
	var index ocispec.Index
	if err != nil || closedJSON(indexBytes, &index) != nil {
		return "", nil, errors.New("prepared layout index unavailable")
	}
	allowed := map[string]int64{"index.json": 1 << 20, "oci-layout": 1024, "subjects.json": 1 << 20, "catalog.json": 4 << 20, "distribution.tar.gz": distribution.MaximumArchiveBytes}
	for _, descriptor := range index.Manifests {
		name := "blobs/sha256/" + descriptor.Digest.Encoded()
		allowed[name] = 1 << 20
		data, err := boundedFile(filepath.Join(directory, filepath.FromSlash(name)), 1<<20)
		var manifest ocispec.Manifest
		if err != nil || closedJSON(data, &manifest) != nil {
			return "", nil, errors.New("prepared layout graph unavailable")
		}
		allowed["blobs/sha256/"+manifest.Config.Digest.Encoded()] = 1 << 20
		for _, layer := range manifest.Layers {
			allowed["blobs/sha256/"+layer.Digest.Encoded()] = distribution.MaximumArchiveBytes
		}
	}
	snapshot, err := os.MkdirTemp("", "astronomer-distribution-publication-")
	if err != nil {
		return "", nil, errors.New("owned prepared snapshot unavailable")
	}
	cleanup := func() { _ = os.RemoveAll(snapshot) }
	var total int64
	err = filepath.WalkDir(directory, func(name string, entry fs.DirEntry, walkError error) error {
		if walkError != nil {
			return errors.New("prepared layout walk unavailable")
		}
		relative, err := filepath.Rel(directory, name)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if entry.IsDir() {
			if relative != "." && relative != "blobs" && relative != "blobs/sha256" {
				return errors.New("unexpected prepared layout directory")
			}
			return os.MkdirAll(filepath.Join(snapshot, filepath.FromSlash(relative)), 0700)
		}
		maximum, exists := allowed[relative]
		if !exists || !entry.Type().IsRegular() || strings.Contains(relative, "\\") {
			return errors.New("unexpected or unsafe prepared layout member")
		}
		data, err := boundedFile(name, maximum)
		if err != nil {
			return err
		}
		total += int64(len(data))
		if total > 2*distribution.MaximumExpandedBytes {
			return errors.New("prepared layout exceeds total transfer bound")
		}
		delete(allowed, relative)
		return os.WriteFile(filepath.Join(snapshot, filepath.FromSlash(relative)), data, 0600)
	})
	if err == nil && len(allowed) != 0 {
		err = errors.New("prepared layout omits required graph bytes")
	}
	if err == nil {
		err = validateLayout(snapshot)
	}
	if err != nil {
		cleanup()
		return "", nil, err
	}
	return snapshot, cleanup, nil
}
