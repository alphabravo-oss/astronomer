package distribution

import (
	"context"
	"errors"
	"net/http"
	"time"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// VerifiedOfflineDatabaseGraph is constructor-produced authority for exact
// database file identities. Transfer data cannot construct it or grant network
// access. Its metadata was authenticated by the same canonical offline loader;
// database bytes and freshness are still unverified.
type VerifiedOfflineDatabaseGraph struct {
	status Status
	layers map[string]DatabaseDescriptor
}

// DatabaseLayers returns a copy of authenticated missing-file claims. This is
// staging authority only; callers must verify every byte before accepting a kit.
func (graph *VerifiedOfflineDatabaseGraph) DatabaseLayers() map[string]DatabaseDescriptor {
	if graph == nil || graph.status.Verification != "verified" {
		return nil
	}
	layers := make(map[string]DatabaseDescriptor, len(graph.layers))
	for subject, descriptor := range graph.layers {
		layers[subject] = descriptor
	}
	return layers
}

// VerifyOfflineDatabaseMetadata authenticates a closed small graph before any
// large file may be staged. It is operator-tool-only and installs a no-network
// transport through a private constructor, never a runtime or document flag.
func VerifyOfflineDatabaseMetadata(ctx context.Context, options OfflineOptions, subjects []StoredSubject, blobs map[string][]byte) (*VerifiedOfflineDatabaseGraph, error) {
	if ctx == nil || options.Profile != "builtins" {
		return nil, errors.New("offline database context and builtins profile required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	store, err := newOfflineRegistryGraph(subjects, blobs, true)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Transport: store, Timeout: time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	loader, err := New(Options{Repository: options.Repository, Digest: options.Digest, CatalogDigest: options.CatalogDigest, Release: options.Release, Profile: options.Profile, TrustDirectory: options.TrustDirectory, Policy: options.Policy, Client: client})
	if err != nil {
		return nil, err
	}
	loader.offline = store
	_, err = loader.Load(ctx)
	if err != nil {
		return nil, err
	}
	for _, subject := range subjects {
		if !store.used[subject.Repository+"@"+subject.Digest] {
			return nil, errors.New("offline database metadata contains an unconsumed subject")
		}
	}
	// Load authenticated these exact parent bytes and all child metadata. The
	// repeated bounded parse grants no authority to unauthenticated content.
	var parent ocispec.Manifest
	if decodeClosed(store.blobs[options.Digest], &parent) != nil || len(parent.Layers) != 1 {
		return nil, errors.New("authenticated database parent unavailable")
	}
	_, bom, err := ReadArchive(store.blobs[parent.Layers[0].Digest.String()], options.Release, options.CatalogDigest, options.Profile)
	if err != nil || bom.AssetContractVersion != DescriptorAssetContract {
		return nil, errors.New("offline database metadata requires descriptor contract")
	}
	layers := map[string]DatabaseDescriptor{}
	for _, asset := range bom.Assets {
		claim := asset.DatabaseDescriptor
		if claim == nil {
			continue
		}
		missing, exists := store.databases[claim.Digest]
		if !exists || missing.Size != claim.Size || missing.MediaType != claim.MediaType {
			return nil, errors.New("offline database missing-file set differs from authenticated BOM")
		}
		layers[claim.Digest] = *claim
	}
	if len(layers) == 0 || len(layers) > 2 || len(layers) != len(store.databases) {
		return nil, errors.New("offline database metadata has unendorsed missing content")
	}
	return &VerifiedOfflineDatabaseGraph{status: loader.Status(), layers: layers}, nil
}

func databaseLayerMedia(media string) bool {
	return media == "application/vnd.aquasec.trivy.db.layer.v1.tar+gzip" || media == "application/vnd.aquasec.trivy.javadb.layer.v1.tar+gzip"
}

func retainMissingDatabaseDescriptor(blobs map[string][]byte, missing map[string]ocispec.Descriptor, layer ocispec.Descriptor) error {
	if !databaseLayerMedia(layer.MediaType) || !digestPattern.MatchString(layer.Digest.String()) || layer.Size <= 0 || layer.Size > MaximumDatabaseLayerBytes || len(layer.URLs) != 0 || len(layer.Data) != 0 {
		return errors.New("offline database descriptor is unbounded or external")
	}
	if _, exists := blobs[layer.Digest.String()]; exists {
		return errors.New("database metadata graph must omit database payloads")
	}
	if previous, exists := missing[layer.Digest.String()]; exists && (previous.Size != layer.Size || previous.MediaType != layer.MediaType) {
		return errors.New("offline database descriptors disagree on size or media")
	}
	// Strip unrelated annotations before returning private file authority.
	missing[layer.Digest.String()] = ocispec.Descriptor{MediaType: layer.MediaType, Digest: layer.Digest, Size: layer.Size}
	return nil
}

// DatabaseMetadataStatus reports conservative canonical loader status. In
// particular, it never upgrades descriptor-only to verified payload or fresh.
func (graph *VerifiedOfflineDatabaseGraph) DatabaseMetadataStatus() Status {
	if graph == nil || graph.status.Verification != "verified" {
		return Status{Verification: "failed", BlockedCategory: "vulnerability_database"}
	}
	status := graph.status
	status.DatabasePayloadVerification = "descriptor-only"
	status.DatabaseFreshness = "not-assessed"
	return status
}
