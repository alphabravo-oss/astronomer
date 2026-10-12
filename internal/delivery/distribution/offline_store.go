package distribution

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	builtinbundles "github.com/alphabravocompany/astronomer-go/deploy/bundles"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/resolver"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// StoredSubject identifies unchanged vendor manifests and their unchanged
// legacy signature manifests in a closed offline transfer. Locations are audit
// identities, not destinations: the verifier has no HTTP or DNS transport.
type StoredSubject struct {
	Repository      string `json:"repository"`
	Digest          string `json:"digest"`
	SignatureDigest string `json:"signature_digest"`
}

// OfflineOptions deliberately has no development, network, client or mirror
// fields. This operator-tool-only verifier cannot relax runtime network policy.
type OfflineOptions struct {
	Repository, Digest, CatalogDigest, Release, Profile, TrustDirectory string
	Policy                                                              model.TrustPolicy
}

type offlineRegistry struct {
	manifests    map[string]string
	blobs        map[string][]byte
	repositories map[string]bool
	used         map[string]bool
	// Present only in the constructor-only database metadata verifier. Missing
	// bytes never become network permissions or successful payload verification.
	databases map[string]ocispec.Descriptor
}

// VerifyOfflineStore reuses canonical OCI signature and archive verification
// before extraction or import. An immutable owned copy contains every required
// config, payload and signature blob; orphan or unavailable bytes fail closed.
func VerifyOfflineStore(ctx context.Context, options OfflineOptions, subjects []StoredSubject, blobs map[string][]byte) (builtinbundles.Catalog, Status, error) {
	store, err := newOfflineRegistry(subjects, blobs)
	if err != nil {
		return builtinbundles.Catalog{}, Status{}, err
	}
	client := &http.Client{Transport: store, Timeout: time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	loader, err := New(Options{Repository: options.Repository, Digest: options.Digest, CatalogDigest: options.CatalogDigest, Release: options.Release, Profile: options.Profile, TrustDirectory: options.TrustDirectory, Policy: options.Policy, Client: client})
	if err != nil {
		return builtinbundles.Catalog{}, Status{}, err
	}
	loader.offline = store
	catalog, err := loader.Load(ctx)
	if err != nil {
		return builtinbundles.Catalog{}, loader.Status(), err
	}
	for _, subject := range subjects {
		if !store.used[subject.Repository+"@"+subject.Digest] {
			status := loader.Status()
			status.Verification = "failed"
			status.InventoryComplete = false
			status.BlockedCategory = "offline_subject"
			return builtinbundles.Catalog{}, status, errors.New("offline transfer contains an unconsumed subject")
		}
	}
	return catalog, loader.Status(), nil
}

func newOfflineRegistry(subjects []StoredSubject, blobs map[string][]byte) (*offlineRegistry, error) {
	return newOfflineRegistryGraph(subjects, blobs, false)
}

func newOfflineRegistryGraph(subjects []StoredSubject, blobs map[string][]byte, databaseMetadata bool) (*offlineRegistry, error) {
	if len(subjects) == 0 || len(subjects) > MaximumMembers || len(blobs) == 0 || len(blobs) > 4*MaximumMembers {
		return nil, errors.New("offline graph exceeds count bounds")
	}
	store := &offlineRegistry{manifests: map[string]string{}, blobs: map[string][]byte{}, repositories: map[string]bool{}, used: map[string]bool{}}
	if databaseMetadata {
		store.databases = map[string]ocispec.Descriptor{}
	}
	var total int64
	for subject, content := range blobs {
		total += int64(len(content))
		if !digestPattern.MatchString(subject) || len(content) == 0 || int64(len(content)) > MaximumArchiveBytes || total > 2*MaximumExpandedBytes || contentDigest(content) != subject {
			return nil, errors.New("offline blob identity or size invalid")
		}
		store.blobs[subject] = bytes.Clone(content)
	}
	usedBlobs := map[string]bool{}
	for _, subject := range subjects {
		if _, err := builtinbundles.NormalizeOCISourceURL("oci://" + subject.Repository); err != nil || len(subject.Repository) > 2048 || !digestPattern.MatchString(subject.Digest) || !digestPattern.MatchString(subject.SignatureDigest) || subject.Digest == subject.SignatureDigest {
			return nil, errors.New("offline subject identity invalid")
		}
		key := subject.Repository + "@" + subject.Digest
		if store.manifests[key] != "" {
			return nil, errors.New("duplicate offline subject")
		}
		if err := validateStoredManifestGraph(store.blobs, usedBlobs, subject.Digest, false, store.databases); err != nil {
			return nil, err
		}
		if err := validateStoredManifest(store.blobs, usedBlobs, subject.SignatureDigest, true); err != nil {
			return nil, err
		}
		store.manifests[key] = subject.Digest
		signatureTag := "sha256-" + strings.TrimPrefix(subject.Digest, "sha256:") + ".sig"
		store.manifests[subject.Repository+"@"+signatureTag] = subject.SignatureDigest
		store.repositories[subject.Repository] = true
	}
	if len(usedBlobs) != len(store.blobs) {
		return nil, errors.New("offline graph contains orphan blobs")
	}
	return store, nil
}

func validateStoredManifest(blobs map[string][]byte, used map[string]bool, subject string, signature bool) error {
	return validateStoredManifestGraph(blobs, used, subject, signature, nil)
}

func validateStoredManifestGraph(blobs map[string][]byte, used map[string]bool, subject string, signature bool, databases map[string]ocispec.Descriptor) error {
	data := blobs[subject]
	var manifest ocispec.Manifest
	if len(data) == 0 || len(data) > 1<<20 || decodeClosed(data, &manifest) != nil || manifest.SchemaVersion != 2 || (manifest.MediaType != "" && manifest.MediaType != ocispec.MediaTypeImageManifest) || manifest.Subject != nil {
		return errors.New("offline OCI manifest invalid")
	}
	if len(manifest.Layers) == 0 || (!signature && len(manifest.Layers) != 1) || len(manifest.Layers) > 32 {
		return errors.New("offline OCI layer count invalid")
	}
	used[subject] = true
	if !resolver.ArtifactConfigurationMatches(manifest.Config, blobs[manifest.Config.Digest.String()]) {
		return errors.New("offline OCI configuration descriptor missing or changed")
	}
	used[manifest.Config.Digest.String()] = true
	for _, layer := range manifest.Layers {
		if databases != nil && !signature && databaseLayerMedia(layer.MediaType) {
			if err := retainMissingDatabaseDescriptor(blobs, databases, layer); err != nil {
				return err
			}
			continue
		}
		maximum := MaximumArchiveBytes
		if signature {
			maximum = 1 << 20
			if layer.MediaType != "application/vnd.dev.cosign.simplesigning.v1+json" {
				return errors.New("offline legacy signature layer type invalid")
			}
		}
		if err := validateStoredDescriptor(blobs, used, layer, maximum); err != nil {
			return err
		}
	}
	return nil
}

func validateStoredDescriptor(blobs map[string][]byte, used map[string]bool, descriptor ocispec.Descriptor, maximum int64) error {
	if !digestPattern.MatchString(descriptor.Digest.String()) || descriptor.Size <= 0 || descriptor.Size > maximum || len(descriptor.URLs) != 0 || len(descriptor.Data) != 0 || int64(len(blobs[descriptor.Digest.String()])) != descriptor.Size {
		return errors.New("offline OCI descriptor missing or has external content")
	}
	used[descriptor.Digest.String()] = true
	return nil
}

func (store *offlineRegistry) RoundTrip(request *http.Request) (*http.Response, error) {
	if err := request.Context().Err(); err != nil {
		return nil, err
	}
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		return nil, errors.New("offline store is read-only")
	}
	location := request.URL
	if location.Scheme != "https" || location.User != nil || location.RawPath != "" || location.RawQuery != "" || location.Fragment != "" || !strings.HasPrefix(location.Path, "/v2/") {
		return nil, errors.New("offline store refuses non-registry request")
	}
	path := strings.TrimPrefix(location.Path, "/v2/")
	var subject string
	media := "application/octet-stream"
	if repository, reference, found := strings.Cut(path, "/manifests/"); found {
		key := location.Host + "/" + repository + "@" + reference
		subject = store.manifests[key]
		if digestPattern.MatchString(reference) && subject != "" {
			store.used[key] = true
		}
		media = ocispec.MediaTypeImageManifest
	} else if repository, reference, found := strings.Cut(path, "/blobs/"); found {
		if store.repositories[location.Host+"/"+repository] {
			subject = reference
		}
	}
	data, exists := store.blobs[subject]
	if !exists {
		return nil, errors.New("offline registry subject unavailable")
	}
	header := http.Header{"Content-Type": []string{media}, "Content-Length": []string{strconv.Itoa(len(data))}, "Docker-Content-Digest": []string{subject}}
	body := data
	if request.Method == http.MethodHead {
		body = nil
	}
	return &http.Response{StatusCode: http.StatusOK, Status: fmt.Sprint(http.StatusOK) + " OK", Header: header, Body: io.NopCloser(bytes.NewReader(body)), ContentLength: int64(len(data)), Request: request}, nil
}
