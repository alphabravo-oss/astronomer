package main

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/distribution"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/resolver"
	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// Metadata-only loader success cannot qualify a contract2 publication. Every
// original layer must also be read back through the same guarded client and
// match its complete signed byte count and hash before publication succeeds.
func verifyPublishedDatabasePayloads(ctx context.Context, options publicationOptions, index subjectIndex, client *http.Client) error {
	if index.SchemaVersion == 1 {
		return nil
	}
	if index.SchemaVersion != 2 {
		return errors.New("unsupported database publication metadata version")
	}
	payload, err := boundedFile(filepath.Join(options.Directory, "distribution.tar.gz"), distribution.MaximumArchiveBytes)
	if err != nil || sha(payload) != index.ArchiveDigest {
		return errors.New("published database parent changed")
	}
	_, bom, err := distribution.ReadArchive(payload, index.Release, index.CatalogDigest, "builtins")
	if err != nil || bom.AssetContractVersion != distribution.DescriptorAssetContract {
		return errors.New("published database claims unavailable")
	}
	return verifyDatabasePayloadClaims(ctx, options, bom.Assets, client)
}

func verifyDatabasePayloadClaims(ctx context.Context, options publicationOptions, assets []distribution.Asset, client *http.Client) error {
	for _, asset := range assets {
		if claim := asset.DatabaseDescriptor; claim != nil {
			repository, subject, _ := strings.Cut(asset.PublishedSubject, "@")
			if err := options.Network.ValidateURL(ctx, "https://"+repository); err != nil {
				return errors.New("database publication readback outside operator policy")
			}
			expected := ocispec.Descriptor{Digest: digest.Digest(claim.Digest), Size: claim.Size, MediaType: claim.MediaType}
			verification, err := resolver.StreamVerifiedOCIDatabase(ctx, client, repository, subject, expected, nil, options.Trust, options.TrustDirectory)
			if err != nil || verification.Status != "verified" {
				return errors.New("published database payload byte verification failed")
			}
		}
	}
	return nil
}

func verifyMirroredDatabasePayloads(ctx context.Context, index offlineTransferIndex, blobs map[string][]byte, plan *distribution.MirrorPlan, options mirrorExecutionOptions, client *http.Client) error {
	var parent ocispec.Manifest
	if closedJSON(blobs[index.Digest], &parent) != nil || len(parent.Layers) != 1 {
		return errors.New("mirrored database parent unavailable")
	}
	_, bom, err := distribution.ReadClosedArchive(blobs[parent.Layers[0].Digest.String()], index.Release, index.CatalogDigest)
	if err != nil {
		return err
	}
	if bom.AssetContractVersion != distribution.DescriptorAssetContract {
		return nil
	}
	assets := append([]distribution.Asset(nil), bom.Assets...)
	for position := range assets {
		asset := &assets[position]
		if asset.DatabaseDescriptor == nil {
			continue
		}
		found := false
		for _, rewrite := range plan.Rewrites {
			if rewrite.Kind == "artifact" && rewrite.Source == asset.PublishedSubject {
				asset.PublishedSubject, found = rewrite.Target, true
				break
			}
		}
		if !found {
			return errors.New("mirrored database exact rewrite missing")
		}
	}
	return verifyDatabasePayloadClaims(ctx, publicationOptions{Trust: options.Vendor.Policy, TrustDirectory: options.Vendor.TrustDirectory, Network: options.Network}, assets, client)
}
