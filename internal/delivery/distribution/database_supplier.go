package distribution

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"time"

	builtinbundles "github.com/alphabravocompany/astronomer-go/deploy/bundles"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/resolver"
	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/registry/remote"
)

// SupplierDatabaseOptions is operator-constructor authority. Runtime catalogs,
// feeds and supplier content cannot supply or expand these network decisions.
// Client must be the canonical guarded client composed with this owned policy.
type SupplierDatabaseOptions struct {
	Repository string
	Role       string
	Scanner    DatabaseScannerCompatibility
	Timing     DatabaseUpdateTiming
	Network    httpclient.DestinationPolicy
	Client     *http.Client
}

type SupplierDatabaseInspector struct {
	options SupplierDatabaseOptions
}

// SupplierDatabaseSnapshot describes original unchanged bytes, not a vendor
// signature. PublishedSubject is deliberately absent: endorsement is a later
// configured publisher step, after full payload and upstream metadata proof.
type SupplierDatabaseSnapshot struct {
	Role            string
	SupplierSubject string
	Manifest        DatabaseDescriptor
	Config          DatabaseDescriptor
	Layer           DatabaseDescriptor
	Metadata        DatabaseUpdateMetadata
}

// CapturedSupplierDatabase proves reviewed transport and immutable descriptors
// only. It cannot be converted into the signed consumer's authenticated update.
type CapturedSupplierDatabase struct {
	owner    *SupplierDatabaseInspector
	snapshot SupplierDatabaseSnapshot
	manifest []byte
	config   []byte
}

// VerifiedSupplierDatabasePayload adds complete compressed hash, archive layout
// and derived actual metadata under finite owned timing. It is not publication,
// vendor authentication, durable acceptance or measured scan-used provenance.
type VerifiedSupplierDatabasePayload struct {
	owner    *SupplierDatabaseInspector
	snapshot SupplierDatabaseSnapshot
}

func NewSupplierDatabaseInspector(options SupplierDatabaseOptions) (*SupplierDatabaseInspector, error) {
	canonical, err := builtinbundles.NormalizeOCISourceURL("oci://" + options.Repository)
	repository, repositoryError := remote.NewRepository(options.Repository)
	_, _, roleOK := supplierDatabaseRole(options.Role)
	if err != nil || canonical != "oci://"+options.Repository || repositoryError != nil || repository.Reference.Reference != "" || !roleOK || options.Client == nil || options.Network.Validate() != nil || options.Timing.validate() != nil || !digestPattern.MatchString(options.Scanner.ImageDigest) || !databaseUpdateVersion.MatchString(options.Scanner.Version) || options.Scanner.MainSchema != 2 || options.Scanner.JavaSchema != 1 {
		return nil, errors.New("supplier database operator constructor incomplete")
	}
	options.Network = options.Network.Clone()
	client := *options.Client
	options.Client = &client
	return &SupplierDatabaseInspector{options: options}, nil
}

func (inspector *SupplierDatabaseInspector) Capture(ctx context.Context) (*CapturedSupplierDatabase, error) {
	if inspector == nil || ctx == nil {
		return nil, errors.New("supplier database capture context unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	options := inspector.options
	if options.Network.ValidateURL(ctx, "https://"+options.Repository) != nil {
		return nil, supplierDatabaseError(ctx, "supplier database outside operator network policy")
	}
	tag, media, _ := supplierDatabaseRole(options.Role)
	subject, metadata, err := resolver.CaptureSupplierDatabaseMetadata(ctx, options.Client, options.Repository, tag, media)
	if err != nil {
		return nil, supplierDatabaseError(ctx, "supplier database capture failed")
	}
	snapshot, err := bindSupplierDatabaseMetadata(options.Repository, options.Role, subject, metadata)
	if err != nil {
		return nil, err
	}
	return &CapturedSupplierDatabase{owner: inspector, snapshot: snapshot, manifest: append([]byte(nil), metadata.Manifest...), config: append([]byte(nil), metadata.Config...)}, nil
}

func bindSupplierDatabaseMetadata(repository, role, subject string, metadata resolver.ArtifactMetadata) (SupplierDatabaseSnapshot, error) {
	var manifest ocispec.Manifest
	if decodeClosed(metadata.Manifest, &manifest) != nil || manifest.MediaType != ocispec.MediaTypeImageManifest || manifest.Subject != nil || len(manifest.Layers) != 1 {
		return SupplierDatabaseSnapshot{}, errors.New("supplier database original manifest invalid")
	}
	snapshot := SupplierDatabaseSnapshot{
		Role: role, SupplierSubject: repository + "@" + subject,
		Manifest: DatabaseDescriptor{Digest: contentDigest(metadata.Manifest), Size: int64(len(metadata.Manifest)), MediaType: manifest.MediaType},
		Config:   DatabaseDescriptor{Digest: contentDigest(metadata.Config), Size: int64(len(metadata.Config)), MediaType: manifest.Config.MediaType},
		Layer:    DatabaseDescriptor{Digest: metadata.Layer.Digest.String(), Size: metadata.Layer.Size, MediaType: metadata.Layer.MediaType},
	}
	if !validDatabaseUpdateSubject(snapshot.SupplierSubject, snapshot.Manifest.Digest) || !validDatabaseUpdateSmallDescriptor(snapshot.Manifest, ocispec.MediaTypeImageManifest) || !validDatabaseUpdateConfig(snapshot.Config) || snapshot.Layer.validate(role) != nil || !snapshot.Layer.matches(manifest.Layers[0]) || snapshot.Config.Digest != manifest.Config.Digest.String() || snapshot.Config.Size != manifest.Config.Size {
		return SupplierDatabaseSnapshot{}, errors.New("supplier database original descriptor binding invalid")
	}
	return snapshot, nil
}

func (inspector *SupplierDatabaseInspector) Inspect(ctx context.Context, captured *CapturedSupplierDatabase, inputPath string, now time.Time) (*VerifiedSupplierDatabasePayload, error) {
	if inspector == nil || ctx == nil || captured == nil || captured.owner != inspector || !filepath.IsAbs(inputPath) || now.IsZero() {
		return nil, errors.New("supplier database payload authority unavailable")
	}
	snapshot := captured.snapshot
	metadataBytes, err := inspectPinnedDatabasePayload(ctx, snapshot.Role, inputPath, snapshot.Layer, 0, nil)
	if err != nil {
		return nil, err
	}
	actual, err := decodeDatabasePayloadMetadata(metadataBytes)
	if err != nil {
		return nil, err
	}
	snapshot.Metadata = DatabaseUpdateMetadata{Digest: contentDigest(metadataBytes), Size: int64(len(metadataBytes)), Schema: actual.Version, UpdatedAt: actual.UpdatedAt, NextUpdate: actual.NextUpdate}
	if validateDatabaseUpdateMetadata(snapshot.Role, snapshot.Metadata, inspector.options.Scanner, inspector.options.Timing, now, now) != nil {
		return nil, errors.New("supplier database actual metadata stale or incompatible")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &VerifiedSupplierDatabasePayload{owner: inspector, snapshot: snapshot}, nil
}

// Snapshot returns owned original content for later operator transfer/signing.
// The absent metadata is intentional until Inspect authenticates the full layer.
func (captured *CapturedSupplierDatabase) Snapshot() (SupplierDatabaseSnapshot, []byte, []byte, bool) {
	if captured == nil || captured.owner == nil || captured.snapshot.Layer.validate(captured.snapshot.Role) != nil {
		return SupplierDatabaseSnapshot{}, nil, nil, false
	}
	return captured.snapshot, append([]byte(nil), captured.manifest...), append([]byte(nil), captured.config...), true
}

func (verified *VerifiedSupplierDatabasePayload) Snapshot() (SupplierDatabaseSnapshot, bool) {
	if verified == nil || verified.owner == nil || verified.snapshot.Layer.validate(verified.snapshot.Role) != nil || !digestPattern.MatchString(verified.snapshot.Metadata.Digest) {
		return SupplierDatabaseSnapshot{}, false
	}
	return verified.snapshot, true
}

func supplierDatabaseRole(role string) (string, string, bool) {
	switch role {
	case "trivy-database":
		return "2", "application/vnd.aquasec.trivy.db.layer.v1.tar+gzip", true
	case "trivy-java-database":
		return "1", "application/vnd.aquasec.trivy.javadb.layer.v1.tar+gzip", true
	default:
		return "", "", false
	}
}

func supplierDatabaseError(ctx context.Context, message string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return errors.New(message)
}
