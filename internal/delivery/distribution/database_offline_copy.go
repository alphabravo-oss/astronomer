package distribution

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"time"

	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/resolver"
)

// CopyOfflineUpdateSubject preserves the original manifest, config and legacy
// signature bytes. It never calls a signer. Targets come only from the captured
// authenticated operator mapping and remain subject to the same TLS/network
// policy. The feed is copied last; discovery and local acceptance are separate.
func (registry *DatabasePublicationRegistry) CopyOfflineUpdateSubject(ctx context.Context, captured *VerifiedOfflineDatabaseUpdate, original string, now time.Time) error {
	if ctx == nil || registry == nil || captured == nil || registry.authority != captured.owner || registry.proof != captured.proof || !databaseUpdateUTC(now) {
		return errors.New("offline database copy constructor owner differs")
	}
	if err := captured.ValidateCurrent(ctx, registry.authority, registry.proof, now); err != nil {
		return err
	}
	target, err := registry.authority.DatabaseUpdateDestination(ctx, registry.proof, original)
	if err != nil {
		return err
	}
	repository, subject, found := strings.Cut(target, "@")
	if !found || repository != registry.repository {
		return errors.New("offline database copy differs from captured mirror destination")
	}
	captured.mutex.Lock()
	defer captured.mutex.Unlock()
	if captured.closed || captured.store == nil || captured.store.manifests[original] != subject {
		return errors.New("offline database copy subject is outside verified kit")
	}
	manifestBytes := captured.store.blobs[subject]
	var manifest ocispec.Manifest
	if decodeClosed(manifestBytes, &manifest) != nil {
		return errors.New("offline database copy original manifest unavailable")
	}
	for _, layer := range manifest.Layers {
		if databaseLayerMedia(layer.MediaType) {
			if err := registry.copyOfflineDatabaseLayer(ctx, captured, layer); err != nil {
				return err
			}
		} else if err := registry.copyOfflineMetadataBlob(ctx, captured.store, layer); err != nil {
			return err
		}
	}
	if err := registry.copyOfflineMetadataBlob(ctx, captured.store, manifest.Config); err != nil {
		return err
	}
	descriptor := ocispec.Descriptor{MediaType: ocispec.MediaTypeImageManifest, Digest: digest.Digest(subject), Size: int64(len(manifestBytes))}
	if err := registry.pushManifest(ctx, descriptor, manifestBytes, subject); err != nil {
		return err
	}
	sourceRepository, _, _ := strings.Cut(original, "@")
	signatureTag := "sha256-" + strings.TrimPrefix(subject, "sha256:") + ".sig"
	signatureDigest := captured.store.manifests[sourceRepository+"@"+signatureTag]
	signatureBytes := captured.store.blobs[signatureDigest]
	var signatureManifest ocispec.Manifest
	if decodeClosed(signatureBytes, &signatureManifest) != nil || len(signatureManifest.Layers) == 0 {
		return errors.New("offline database copy original signature manifest unavailable")
	}
	for _, layer := range signatureManifest.Layers {
		if err := registry.copyOfflineMetadataBlob(ctx, captured.store, layer); err != nil {
			return err
		}
	}
	if err := registry.copyOfflineMetadataBlob(ctx, captured.store, signatureManifest.Config); err != nil {
		return err
	}
	signatureDescriptor := ocispec.Descriptor{MediaType: ocispec.MediaTypeImageManifest, Digest: digest.Digest(signatureDigest), Size: int64(len(signatureBytes))}
	if err := registry.pushManifest(ctx, signatureDescriptor, signatureBytes, signatureTag); err != nil {
		return err
	}
	// Private pull auth is independently composed beneath this readback client;
	// write credentials are never inherited by canonical consumers.
	authority := registry.authority
	authority.mutex.RLock()
	client, policy, trust := authority.loader.options.Client, authority.loader.options.Policy, authority.trust.FrozenDirectory()
	authority.mutex.RUnlock()
	verification, err := resolver.VerifyPinnedOCI(ctx, client, repository, subject, policy, trust)
	if err != nil || verification.Status != "verified" {
		return supplierDatabaseError(ctx, "offline database copy unchanged vendor signature readback failed")
	}
	return registry.current(ctx)
}

func (registry *DatabasePublicationRegistry) copyOfflineMetadataBlob(ctx context.Context, store *offlineRegistry, descriptor ocispec.Descriptor) error {
	value := store.blobs[descriptor.Digest.String()]
	if descriptor.Digest.Validate() != nil || descriptor.Digest.Algorithm() != digest.SHA256 || descriptor.Size <= 0 || descriptor.Size > 1<<20 || int64(len(value)) != descriptor.Size || contentDigest(value) != descriptor.Digest.String() || len(descriptor.URLs) != 0 {
		return errors.New("offline database copy metadata descriptor differs")
	}
	// Canonical original inline config was already checked byte-for-byte. Its
	// immutable manifest is unchanged; upload uses the same separately stored blob.
	descriptor.Data = nil
	joined, err := registry.pushOwnedBlob(ctx, descriptor, bytes.NewReader(value))
	if !joined {
		return err
	}
	return err
}

func (registry *DatabasePublicationRegistry) copyOfflineDatabaseLayer(ctx context.Context, captured *VerifiedOfflineDatabaseUpdate, layer ocispec.Descriptor) error {
	index := -1
	for candidate, payload := range []*VerifiedDatabaseUpdatePayload{captured.main, captured.java} {
		_, snapshot, valid := payload.Snapshot()
		if valid && snapshot.Layer.matches(layer) {
			index = candidate
		}
	}
	if index < 0 || captured.files[index] == nil || !sameDatabaseInspectionFile(captured.files[index], captured.paths[index], captured.identities[index]) {
		return errors.New("offline database copy original inspected layer differs")
	}
	claim := DatabaseDescriptor{Digest: layer.Digest.String(), MediaType: layer.MediaType, Size: layer.Size}
	reader, err := openDatabasePublicationPayload(ctx, captured.paths[index], claim)
	if err != nil {
		return err
	}
	if !sameDatabaseInspectionFile(reader.file, captured.paths[index], captured.identities[index]) {
		_ = reader.Close()
		return errors.New("offline database copy path changed after current hash")
	}
	joined, copyErr := registry.pushOwnedBlob(ctx, layer, reader)
	if !joined {
		// The transport still owns reads. Do not drain or close this pinned reader;
		// preserve explicit ownership failure and refuse manifest/discovery/DB writes.
		return copyErr
	}
	closeErr := reader.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if !sameDatabaseInspectionFile(captured.files[index], captured.paths[index], captured.identities[index]) {
		return errors.New("offline database copy original changed before subject write")
	}
	return registry.current(ctx)
}
