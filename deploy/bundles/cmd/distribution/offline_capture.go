package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/distribution"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/resolver"
	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/auth"
)

// Capture authenticates each exact subject before retrieving its graph. It
// explicitly captures legacy signature tags: a generic recursive copy may
// retain referrers while silently dropping these vendor signature manifests.
func captureOfflineTransfer(ctx context.Context, directory string, options distribution.OfflineOptions, client *http.Client, network httpclient.DestinationPolicy) ([]byte, error) {
	snapshot, cleanup, err := snapshotPreparedLayout(directory)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	data, err := boundedFile(filepath.Join(snapshot, "subjects.json"), 1<<20)
	var index subjectIndex
	if err != nil || closedJSON(data, &index) != nil || index.Repository != options.Repository || index.Digest != options.Digest || index.CatalogDigest != options.CatalogDigest || index.Release != options.Release || options.Profile != "builtins" || client == nil {
		return nil, errors.New("export differs from caller-pinned prepared identity")
	}
	if options.Policy.Validate() != nil || options.Policy.AllowUnsigned {
		return nil, errors.New("mandatory original vendor policy required")
	}
	all := append([]publishedSubject{{Repository: index.Repository, Digest: index.Digest}}, index.Children...)
	for _, asset := range index.Assets {
		all = append(all, publishedSubject{Repository: asset.Repository, Digest: asset.Digest})
	}
	stored := make([]distribution.StoredSubject, 0, len(all))
	graph := &capturedGraph{blobs: map[string][]byte{}}
	for _, subject := range all {
		if err := network.ValidateURL(ctx, "https://"+subject.Repository); err != nil {
			return nil, errors.New("export subject outside operator network policy")
		}
		if verification, err := resolver.VerifyPinnedOCI(ctx, client, subject.Repository, subject.Digest, options.Policy, options.TrustDirectory); err != nil || verification.Status != "verified" {
			return nil, errors.New("export subject signature invalid")
		}
		repository, err := remote.NewRepository(subject.Repository)
		if err != nil {
			return nil, errors.New("export repository invalid")
		}
		repository.Client = &auth.Client{Client: client}
		repository.MaxMetadataBytes = 1 << 20
		if _, err := captureManifest(ctx, repository, subject.Digest, graph, false); err != nil {
			return nil, err
		}
		signatureTag := "sha256-" + strings.TrimPrefix(subject.Digest, "sha256:") + ".sig"
		signatureDigest, err := captureManifest(ctx, repository, signatureTag, graph, true)
		if err != nil {
			return nil, err
		}
		stored = append(stored, distribution.StoredSubject{Repository: subject.Repository, Digest: subject.Digest, SignatureDigest: signatureDigest})
	}
	return packOfflineTransfer(ctx, options, stored, graph.blobs)
}

func captureManifest(ctx context.Context, repository *remote.Repository, reference string, graph *capturedGraph, signature bool) (string, error) {
	return captureManifestGraph(ctx, repository, reference, graph, signature, false)
}

func captureManifestGraph(ctx context.Context, repository *remote.Repository, reference string, graph *capturedGraph, signature, databaseMetadata bool) (string, error) {
	if databaseMetadata && signature {
		return "", errors.New("signature payload cannot use database descriptor-only capture")
	}
	descriptor, reader, err := repository.FetchReference(ctx, reference)
	if err != nil {
		return "", errors.New("exact exported manifest unavailable")
	}
	data, err := captureBytes(reader, descriptor, 1<<20)
	if err != nil {
		return "", err
	}
	var manifest ocispec.Manifest
	if closedJSON(data, &manifest) != nil || manifest.SchemaVersion != 2 || len(manifest.Layers) == 0 || len(manifest.Layers) > 32 || (!signature && len(manifest.Layers) != 1) {
		return "", errors.New("exported manifest invalid")
	}
	if err := graph.retain(descriptor, data); err != nil {
		return "", err
	}
	maximum := distribution.MaximumArchiveBytes
	if signature {
		maximum = 1 << 20
	}
	for index, child := range append([]ocispec.Descriptor{manifest.Config}, manifest.Layers...) {
		limit := maximum
		if index == 0 {
			limit = 1 << 20
			if len(child.Data) != 0 {
				if !resolver.ArtifactConfigurationMatches(child, child.Data) {
					return "", errors.New("exported inline configuration differs from exact empty object")
				}
				if existing := graph.blobs[child.Digest.String()]; existing != nil && !resolver.ArtifactConfigurationMatches(child, existing) {
					return "", errors.New("exported inline configuration conflicts with captured bytes")
				}
				if err := graph.retain(child, append([]byte(nil), child.Data...)); err != nil {
					return "", err
				}
				continue
			}
		}
		if databaseMetadata && index > 0 {
			limit = distribution.MaximumDatabaseLayerBytes
		}
		if child.Digest.Algorithm() != digest.SHA256 || child.Size <= 0 || child.Size > limit || len(child.URLs) != 0 || len(child.Data) != 0 {
			return "", errors.New("exported descriptor has external or unbounded content")
		}
		if databaseMetadata && index > 0 {
			if child.MediaType != "application/vnd.aquasec.trivy.db.layer.v1.tar+gzip" && child.MediaType != "application/vnd.aquasec.trivy.javadb.layer.v1.tar+gzip" {
				return "", errors.New("descriptor-only capture is restricted to fixed database media")
			}
			continue
		}
		if graph.blobs[child.Digest.String()] != nil {
			continue
		}
		if err := graph.check(child); err != nil {
			return "", err
		}
		reader, err := repository.Fetch(ctx, child)
		if err != nil {
			return "", errors.New("exported graph blob unavailable")
		}
		content, err := captureBytes(reader, child, limit)
		if err != nil {
			return "", err
		}
		if err := graph.retain(child, content); err != nil {
			return "", err
		}
	}
	return descriptor.Digest.String(), nil
}

func captureBytes(reader io.ReadCloser, descriptor ocispec.Descriptor, maximum int64) ([]byte, error) {
	defer reader.Close()
	if descriptor.Digest.Algorithm() != digest.SHA256 || descriptor.Size <= 0 || descriptor.Size > maximum || len(descriptor.URLs) != 0 || len(descriptor.Data) != 0 {
		return nil, errors.New("export content descriptor invalid")
	}
	data, err := io.ReadAll(io.LimitReader(reader, maximum+1))
	if err != nil || int64(len(data)) != descriptor.Size || sha(data) != descriptor.Digest.String() {
		return nil, errors.New("exported blob content changed")
	}
	return data, nil
}

type capturedGraph struct {
	blobs map[string][]byte
	total int64
}

func (graph *capturedGraph) check(descriptor ocispec.Descriptor) error {
	if graph.blobs[descriptor.Digest.String()] != nil {
		return nil
	}
	if len(graph.blobs) >= 4*distribution.MaximumMembers || descriptor.Size <= 0 || graph.total+descriptor.Size > 2*distribution.MaximumExpandedBytes {
		return errors.New("exported graph exceeds total bounds")
	}
	return nil
}
func (graph *capturedGraph) retain(descriptor ocispec.Descriptor, data []byte) error {
	if err := graph.check(descriptor); err != nil {
		return err
	}
	if graph.blobs[descriptor.Digest.String()] == nil {
		graph.total += int64(len(data))
		graph.blobs[descriptor.Digest.String()] = data
	}
	return nil
}
