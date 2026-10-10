package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/distribution"
)

// The v2 index selects only the versioned transfer shape. It cannot endorse
// missing payloads: the canonical verifier derives those claims from the signed
// BOM and unchanged signed child manifests, after caller-pinned identity checks.
func readOfflineDatabaseMetadata(ctx context.Context, metadata []byte, options distribution.OfflineOptions) (offlineTransferIndex, map[string][]byte, *distribution.VerifiedOfflineDatabaseGraph, error) {
	var index offlineTransferIndex
	members, err := distribution.ReadBoundedRegularArchive(metadata, 2*distribution.MaximumExpandedBytes, 2*distribution.MaximumExpandedBytes, 4*distribution.MaximumMembers)
	if err != nil {
		return index, nil, nil, err
	}
	if len(members["offline-index.json"]) > 1<<20 || closedJSON(members["offline-index.json"], &index) != nil || index.SchemaVersion != 2 || index.Release != options.Release || index.Repository != options.Repository || index.Digest != options.Digest || index.CatalogDigest != options.CatalogDigest || index.Profile != "builtins" || index.Profile != options.Profile {
		return offlineTransferIndex{}, nil, nil, errors.New("offline database metadata differs from caller-pinned identity")
	}
	blobs := map[string][]byte{}
	for name, content := range members {
		if name == "offline-index.json" {
			continue
		}
		if !strings.HasPrefix(name, "blobs/sha256/") || len(name) != len("blobs/sha256/")+64 {
			return offlineTransferIndex{}, nil, nil, errors.New("unexpected offline database metadata member")
		}
		blobs["sha256:"+strings.TrimPrefix(name, "blobs/sha256/")] = content
	}
	graph, err := distribution.VerifyOfflineDatabaseMetadata(ctx, options, index.Subjects, blobs)
	if err != nil {
		return offlineTransferIndex{}, nil, nil, err
	}
	return index, blobs, graph, nil
}

func packOfflineDatabaseMetadata(ctx context.Context, options distribution.OfflineOptions, subjects []distribution.StoredSubject, blobs map[string][]byte) ([]byte, *distribution.VerifiedOfflineDatabaseGraph, error) {
	if options.Profile != "builtins" {
		return nil, nil, errors.New("offline database transfer requires builtins profile")
	}
	graph, err := distribution.VerifyOfflineDatabaseMetadata(ctx, options, subjects, blobs)
	if err != nil {
		return nil, nil, err
	}
	index := offlineTransferIndex{SchemaVersion: 2, Release: options.Release, Repository: options.Repository, Digest: options.Digest, CatalogDigest: options.CatalogDigest, Profile: options.Profile, Subjects: append([]distribution.StoredSubject(nil), subjects...)}
	sort.Slice(index.Subjects, func(i, j int) bool {
		return index.Subjects[i].Repository+index.Subjects[i].Digest < index.Subjects[j].Repository+index.Subjects[j].Digest
	})
	directory, err := os.MkdirTemp("", "astronomer-database-transfer-metadata-")
	if err != nil {
		return nil, nil, errors.New("owned database metadata unavailable")
	}
	defer os.RemoveAll(directory)
	if err := writeJSON(filepath.Join(directory, "index.json"), index); err != nil {
		return nil, nil, err
	}
	data, err := boundedFile(filepath.Join(directory, "index.json"), 1<<20)
	if err != nil {
		return nil, nil, err
	}
	members := map[string][]byte{"offline-index.json": data}
	for subject, content := range blobs {
		members["blobs/sha256/"+strings.TrimPrefix(subject, "sha256:")] = content
	}
	payload, err := packMembers(members)
	if err != nil || int64(len(payload)) > 2*distribution.MaximumExpandedBytes {
		return nil, nil, errors.New("bounded database metadata encoding failed")
	}
	// The emitted archive must authenticate through the actual import reader.
	if _, _, _, err := readOfflineDatabaseMetadata(ctx, payload, options); err != nil {
		return nil, nil, err
	}
	return payload, graph, nil
}
