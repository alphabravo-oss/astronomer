package distribution

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/resolver"
	digest "github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// PublishSupplierChild copies a genuinely new original supplier subject. The
// historical inspection proof cannot replace rehashing the current owned file.
// Exact unchanged config/manifest bytes, full current layer and local canonical
// signature are all mandatory. This grants no durable reservation phase.
func (registry *DatabasePublicationRegistry) PublishSupplierChild(ctx context.Context, signer *DatabasePublicationSigner, captured *CapturedSupplierDatabase, verified *VerifiedSupplierDatabasePayload, inputPath string, expected DatabaseUpdateSnapshot, now time.Time) error {
	if err := registry.current(ctx); err != nil {
		return err
	}
	if captured == nil || captured.owner == nil || verified == nil || captured.owner != verified.owner || !databaseUpdateUTC(now) {
		return errors.New("database child publication whole supplier owner differs")
	}
	original, manifest, config, valid := captured.Snapshot()
	measured, measuredValid := verified.Snapshot()
	if !valid || !measuredValid || original.Role != expected.Role || measured.Role != expected.Role || original.SupplierSubject != expected.SupplierSubject || measured.SupplierSubject != expected.SupplierSubject || measured.Metadata != expected.Metadata || original.Manifest != expected.Manifest || original.Config != expected.Config || original.Layer != expected.Layer || expected.PublishedSubject != registry.repository+"@"+expected.Manifest.Digest || contentDigest(manifest) != expected.Manifest.Digest || int64(len(manifest)) != expected.Manifest.Size || contentDigest(config) != expected.Config.Digest || int64(len(config)) != expected.Config.Size {
		return errors.New("database child publication differs from reserved original subject")
	}
	reader, err := captured.owner.OpenPublicationPayload(ctx, captured, verified, inputPath, now)
	if err != nil {
		return err
	}
	layer := ocispec.Descriptor{MediaType: expected.Layer.MediaType, Digest: digest.Digest(expected.Layer.Digest), Size: expected.Layer.Size}
	joined, copyErr := registry.pushOwnedBlob(ctx, layer, reader)
	if !joined {
		// No caller drain/close may race a still-owned transport reader. Retain the
		// pinned resource and explicit failure; the protected command cannot sign.
		return copyErr
	}
	closeErr := reader.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	configDescriptor := ocispec.Descriptor{MediaType: expected.Config.MediaType, Digest: digest.Digest(expected.Config.Digest), Size: expected.Config.Size}
	if err := registry.pushBlob(ctx, configDescriptor, bytes.NewReader(config)); err != nil {
		return err
	}
	manifestDescriptor := ocispec.Descriptor{MediaType: expected.Manifest.MediaType, Digest: digest.Digest(expected.Manifest.Digest), Size: expected.Manifest.Size}
	if err := registry.pushManifest(ctx, manifestDescriptor, manifest, manifestDescriptor.Digest.String()); err != nil {
		return err
	}
	return registry.signAndPublish(ctx, signer, manifestDescriptor.Digest.String())
}

// PublishFeed publishes only the exact reserved document DATA. Consumer trust
// and a real published reservation are still established by PublishVerified.
// It deliberately never writes the discovery tag.
func (registry *DatabasePublicationRegistry) PublishFeed(ctx context.Context, signer *DatabasePublicationSigner, document DatabaseUpdateDocument, now time.Time) (string, error) {
	if err := registry.current(ctx); err != nil {
		return "", err
	}
	snapshot, valid := registry.proof.Snapshot()
	if !valid || !reflect.DeepEqual(document.Binding, snapshot.Base.Binding) {
		return "", errors.New("database feed publication current base differs")
	}
	if _, err := validateDatabaseUpdateDocument(document, snapshot.Base.Binding, CadenceDatabaseUpdateTiming(), now, true); err != nil {
		return "", err
	}
	content, err := json.Marshal(document)
	if err != nil || int64(len(content)) > MaximumDatabaseUpdateBytes {
		return "", errors.New("database feed publication encoding exceeds bound")
	}
	descriptor, manifest, err := registry.publishArtifact(ctx, DatabaseUpdateMediaType, content, nil)
	if err != nil {
		return "", err
	}
	if err := registry.pushManifest(ctx, descriptor, manifest, descriptor.Digest.String()); err != nil {
		return "", err
	}
	if err := registry.signAndPublish(ctx, signer, descriptor.Digest.String()); err != nil {
		return "", err
	}
	return descriptor.Digest.String(), nil
}

func (registry *DatabasePublicationRegistry) signAndPublish(ctx context.Context, signer *DatabasePublicationSigner, subject string) error {
	if err := registry.current(ctx); err != nil {
		return err
	}
	registry.authority.mutex.RLock()
	policy := registry.authority.loader.options.Policy
	trustDirectory := registry.authority.loader.options.TrustDirectory
	registry.authority.mutex.RUnlock()
	evidence, err := signer.sign(ctx, policy, trustDirectory, registry.repository, subject)
	if err != nil {
		return err
	}
	annotations := map[string]string{"dev.cosignproject.cosign/signature": string(evidence.Signature)}
	if len(evidence.Certificate) > 0 {
		annotations["dev.sigstore.cosign/certificate"] = string(evidence.Certificate)
	}
	if len(evidence.Bundle) > 0 {
		annotations["dev.sigstore.cosign/bundle"] = string(evidence.Bundle)
	}
	descriptor, manifest, err := registry.publishArtifact(ctx, "application/vnd.dev.cosign.simplesigning.v1+json", evidence.Payload, annotations)
	if err != nil {
		return err
	}
	reference := "sha256-" + strings.TrimPrefix(subject, "sha256:") + ".sig"
	if err := registry.pushManifest(ctx, descriptor, manifest, reference); err != nil {
		return err
	}
	// Verify registry readback with the same in-process trust implementation.
	// Local sign success or a registry HTTP201 alone grants no endorsement.
	verification, err := resolver.VerifyPinnedOCI(ctx, registry.authority.loader.options.Client, registry.repository, subject, policy, trustDirectory)
	if err != nil || verification.Status != "verified" {
		return supplierDatabaseError(ctx, "database publication immutable signature readback failed")
	}
	return registry.current(ctx)
}

func (registry *DatabasePublicationRegistry) publishArtifact(ctx context.Context, media string, payload []byte, annotations map[string]string) (ocispec.Descriptor, []byte, error) {
	var empty ocispec.Descriptor
	if len(payload) == 0 || len(payload) > 1<<20 {
		return empty, nil, errors.New("database publication metadata payload exceeds bound")
	}
	config := []byte("{}")
	configDescriptor := ocispec.Descriptor{MediaType: ocispec.MediaTypeEmptyJSON, Digest: digest.FromBytes(config), Size: int64(len(config))}
	layer := ocispec.Descriptor{MediaType: media, Digest: digest.FromBytes(payload), Size: int64(len(payload)), Annotations: annotations}
	for _, part := range []struct {
		descriptor ocispec.Descriptor
		value      []byte
	}{{configDescriptor, config}, {layer, payload}} {
		if err := registry.pushBlob(ctx, part.descriptor, bytes.NewReader(part.value)); err != nil {
			return empty, nil, err
		}
	}
	manifest := ocispec.Manifest{Versioned: specs.Versioned{SchemaVersion: 2}, MediaType: ocispec.MediaTypeImageManifest, Config: configDescriptor, Layers: []ocispec.Descriptor{layer}}
	content, err := json.Marshal(manifest)
	if err != nil || len(content) > 1<<20 {
		return empty, nil, errors.New("database publication OCI manifest exceeds bound")
	}
	descriptor := ocispec.Descriptor{MediaType: ocispec.MediaTypeImageManifest, Digest: digest.FromBytes(content), Size: int64(len(content))}
	return descriptor, content, nil
}

// PublishCurrent is called by the protected command only AFTER the owning DAO
// commits exact canonical readback as published. Discovery is still DATA; this
// mutable locator cannot authorize acceptance or grant a local reservation.
func (registry *DatabasePublicationRegistry) PublishCurrent(ctx context.Context, feedDigest string) error {
	if err := registry.current(ctx); err != nil {
		return err
	}
	if !digestPattern.MatchString(feedDigest) {
		return errors.New("database publication discovery digest invalid")
	}
	registry.authority.mutex.RLock()
	policy, trustDirectory, client := registry.authority.loader.options.Policy, registry.authority.loader.options.TrustDirectory, registry.authority.loader.options.Client
	registry.authority.mutex.RUnlock()
	// Authentication precedes decoding/refetching exact manifest bytes. Fetching
	// a tag here is forbidden; only the already-published immutable subject is used.
	if verified, err := resolver.VerifyPinnedOCI(ctx, client, registry.repository, feedDigest, policy, trustDirectory); err != nil || verified.Status != "verified" {
		return supplierDatabaseError(ctx, "database publication current immutable signature unavailable")
	}
	metadata, _, err := resolver.FetchVerifiedOCIArtifactMetadata(ctx, client, registry.repository, feedDigest, DatabaseUpdateMediaType, policy, trustDirectory, MaximumDatabaseUpdateBytes)
	if err != nil {
		return err
	}
	descriptor := ocispec.Descriptor{MediaType: ocispec.MediaTypeImageManifest, Digest: digest.Digest(feedDigest), Size: int64(len(metadata.Manifest))}
	return registry.pushManifest(ctx, descriptor, metadata.Manifest, "current")
}
