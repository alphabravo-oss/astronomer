package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/distribution"
	digest "github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

type offlineTransferIndex struct {
	SchemaVersion int                          `json:"schema_version"`
	Release       string                       `json:"release"`
	Repository    string                       `json:"repository"`
	Digest        string                       `json:"digest"`
	CatalogDigest string                       `json:"catalog_digest"`
	Profile       string                       `json:"profile"`
	Subjects      []distribution.StoredSubject `json:"subjects"`
}

func packOfflineTransfer(ctx context.Context, options distribution.OfflineOptions, subjects []distribution.StoredSubject, blobs map[string][]byte) ([]byte, error) {
	if options.Profile != "builtins" {
		return nil, errors.New("offline transfer v1 requires the builtins profile")
	}
	if _, _, err := distribution.VerifyOfflineStore(ctx, options, subjects, blobs); err != nil {
		return nil, err
	}
	index := offlineTransferIndex{SchemaVersion: 1, Release: options.Release, Repository: options.Repository, Digest: options.Digest, CatalogDigest: options.CatalogDigest, Profile: options.Profile, Subjects: subjects}
	index.Subjects = append([]distribution.StoredSubject(nil), subjects...)
	sort.Slice(index.Subjects, func(i, j int) bool {
		return index.Subjects[i].Repository+index.Subjects[i].Digest < index.Subjects[j].Repository+index.Subjects[j].Digest
	})
	// Reuse the canonical preparation serializer, then read its bounded owned
	// output. The transfer index grants no trust: caller-pinned identity and the
	// original vendor parent/child signatures are mandatory on import.
	directory, err := os.MkdirTemp("", "astronomer-transfer-index-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(directory)
	if err := writeJSON(filepath.Join(directory, "index.json"), index); err != nil {
		return nil, err
	}
	indexBytes, err := boundedFile(filepath.Join(directory, "index.json"), 1<<20)
	if err != nil {
		return nil, err
	}
	members := map[string][]byte{"offline-index.json": indexBytes}
	for digest, data := range blobs {
		members["blobs/sha256/"+strings.TrimPrefix(digest, "sha256:")] = data
	}
	payload, err := packMembers(members)
	if err != nil {
		return nil, err
	}
	if _, _, err := readOfflineTransfer(ctx, payload, options); err != nil {
		return nil, err
	}
	return payload, nil
}

func readOfflineTransfer(ctx context.Context, payload []byte, options distribution.OfflineOptions) (offlineTransferIndex, map[string][]byte, error) {
	var empty offlineTransferIndex
	members, err := distribution.ReadBoundedRegularArchive(payload, 2*distribution.MaximumExpandedBytes, 2*distribution.MaximumExpandedBytes, 4*distribution.MaximumMembers)
	if err != nil {
		return empty, nil, err
	}
	var index offlineTransferIndex
	if len(members["offline-index.json"]) > 1<<20 || closedJSON(members["offline-index.json"], &index) != nil || index.SchemaVersion != 1 || index.Release != options.Release || index.Repository != options.Repository || index.Digest != options.Digest || index.CatalogDigest != options.CatalogDigest || index.Profile != "builtins" || index.Profile != options.Profile {
		return empty, nil, errors.New("offline transfer differs from caller-pinned release identity")
	}
	blobs := map[string][]byte{}
	for path, data := range members {
		if path == "offline-index.json" {
			continue
		}
		if !strings.HasPrefix(path, "blobs/sha256/") || len(path) != len("blobs/sha256/")+64 {
			return empty, nil, errors.New("unexpected offline transfer member")
		}
		blobs["sha256:"+strings.TrimPrefix(path, "blobs/sha256/")] = data
	}
	if _, _, err := distribution.VerifyOfflineStore(ctx, options, index.Subjects, blobs); err != nil {
		return empty, nil, err
	}
	return index, blobs, nil
}

// A new private directory is written only after all vendor signatures and the
// exact closed graph have authenticated. No archive path is trusted for output.
func importOfflineTransfer(ctx context.Context, archive, destination string, options distribution.OfflineOptions) error {
	compressed, err := boundedFile(archive, 2*distribution.MaximumExpandedBytes)
	if err != nil {
		return err
	}
	index, blobs, err := readOfflineTransfer(ctx, compressed, options)
	if err != nil {
		return err
	}
	if err := os.Mkdir(destination, 0700); err != nil {
		return errors.New("offline destination must be new")
	}
	success := false
	defer func() {
		if !success {
			_ = os.RemoveAll(destination)
		}
	}()
	if err := os.MkdirAll(filepath.Join(destination, "blobs", "sha256"), 0700); err != nil {
		return err
	}
	for digest, data := range blobs {
		if err := os.WriteFile(filepath.Join(destination, "blobs", "sha256", digest[7:]), data, 0600); err != nil {
			return err
		}
	}
	if err := writeJSON(filepath.Join(destination, "offline-index.json"), index); err != nil {
		return err
	}
	layout := ocispec.Index{Versioned: specs.Versioned{SchemaVersion: 2}, MediaType: ocispec.MediaTypeImageIndex, Manifests: []ocispec.Descriptor{}}
	for position, subject := range index.Subjects {
		for prefix, subjectDigest := range map[string]string{"subject": subject.Digest, "signature": subject.SignatureDigest} {
			layout.Manifests = append(layout.Manifests, ocispec.Descriptor{MediaType: ocispec.MediaTypeImageManifest, Digest: digest.Digest(subjectDigest), Size: int64(len(blobs[subjectDigest])), Annotations: map[string]string{ocispec.AnnotationRefName: fmt.Sprintf("%s_%04d", prefix, position)}})
		}
	}
	sort.Slice(layout.Manifests, func(i, j int) bool {
		return layout.Manifests[i].Annotations[ocispec.AnnotationRefName] < layout.Manifests[j].Annotations[ocispec.AnnotationRefName]
	})
	if err := writeJSON(filepath.Join(destination, "index.json"), layout); err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(destination, "oci-layout"), map[string]string{"imageLayoutVersion": "1.0.0"}); err != nil {
		return err
	}
	success = true
	return nil
}
