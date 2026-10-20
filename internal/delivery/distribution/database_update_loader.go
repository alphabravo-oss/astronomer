package distribution

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/resolver"
	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
)

// DatabaseUpdateOptions is constructor-owned immutable candidate, trust and
// network intent. No signed feed field selects these policies or this client.
type DatabaseUpdateOptions struct {
	Repository, Digest, TrustDirectory string
	Binding                            DatabaseUpdateBinding
	Timing                             DatabaseUpdateTiming
	JavaRequired                       bool
	Policy                             model.TrustPolicy
	Network                            httpclient.DestinationPolicy
	Client                             *http.Client
}

type DatabaseUpdateLoader struct {
	options DatabaseUpdateOptions
	mirror  *MirrorPlan
	// Set only by the operator-only closed-store constructor. No public option or
	// feed flag can disable runtime destination checks or select this transport.
	offline *offlineRegistry
}

// AuthenticatedDatabaseUpdate proves original signed feed/child descriptors,
// not whole payload availability, parsed payload metadata, scan-used provenance,
// release/catalog revocation state or authorization to mutate desired state.
// Durable composition must establish those separate preconditions first.
type AuthenticatedDatabaseUpdate struct {
	digest       string
	document     DatabaseUpdateDocument
	timing       DatabaseUpdateTiming
	javaRequired bool
}

func NewDatabaseUpdateLoader(options DatabaseUpdateOptions) (*DatabaseUpdateLoader, error) {
	if options.Client == nil || options.Policy.AllowUnsigned || options.Policy.Validate() != nil || options.Binding.validate() != nil || options.Timing.validate() != nil || !digestPattern.MatchString(options.Digest) || !filepath.IsAbs(options.TrustDirectory) || options.Network.Validate() != nil || !validDatabaseUpdateSubject(options.Repository+"@"+options.Digest, options.Digest) {
		return nil, errors.New("signed database update constructor incomplete")
	}
	options.Network = options.Network.Clone()
	return &DatabaseUpdateLoader{options: options}, nil
}

func (loader *DatabaseUpdateLoader) Load(ctx context.Context, now time.Time) (*AuthenticatedDatabaseUpdate, error) {
	if loader == nil || ctx == nil || now.IsZero() {
		return nil, errors.New("database update loader context/clock unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	options := loader.options
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if loader.offline == nil && options.Network.ValidateURL(ctx, "https://"+options.Repository) != nil {
		return nil, errors.New("database update feed outside operator network policy")
	}
	payload, verification, err := resolver.FetchVerifiedOCIArtifact(ctx, options.Client, options.Repository, options.Digest, DatabaseUpdateMediaType, options.Policy, options.TrustDirectory, MaximumDatabaseUpdateBytes)
	defer clear(payload)
	if err != nil || verification.Status != "verified" {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("database update feed authentication failed")
	}
	document, err := DecodeDatabaseUpdateDocument(payload, options.Binding, options.Timing, now, options.JavaRequired)
	if err != nil {
		return nil, err
	}
	for _, snapshot := range document.Databases {
		if err := loader.verifySnapshot(ctx, snapshot); err != nil {
			return nil, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &AuthenticatedDatabaseUpdate{digest: options.Digest, document: document, timing: options.Timing, javaRequired: options.JavaRequired}, nil
}

func (loader *DatabaseUpdateLoader) verifySnapshot(ctx context.Context, snapshot DatabaseUpdateSnapshot) error {
	location := snapshot.PublishedSubject
	if loader.offline == nil {
		var err error
		location, err = databaseMirrorSubject(loader.mirror, loader.options.Binding, location)
		if err != nil {
			return err
		}
	}
	repository, subject, ok := strings.Cut(location, "@")
	if !ok || (loader.offline == nil && loader.options.Network.ValidateURL(ctx, "https://"+repository) != nil) {
		return errors.New("database update child outside operator network policy")
	}
	metadata, verification, err := resolver.FetchVerifiedOCIArtifactMetadata(ctx, loader.options.Client, repository, subject, snapshot.Layer.MediaType, loader.options.Policy, loader.options.TrustDirectory, MaximumDatabaseLayerBytes)
	if err != nil || verification.Status != "verified" || int64(len(metadata.Manifest)) != snapshot.Manifest.Size || contentDigest(metadata.Manifest) != snapshot.Manifest.Digest || int64(len(metadata.Config)) != snapshot.Config.Size || contentDigest(metadata.Config) != snapshot.Config.Digest || !snapshot.Layer.matches(metadata.Layer) {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("database update child authentication or descriptor binding failed")
	}
	var manifest ocispec.Manifest
	if decodeClosed(metadata.Manifest, &manifest) != nil || manifest.MediaType != snapshot.Manifest.MediaType || manifest.Config.MediaType != snapshot.Config.MediaType || manifest.Config.Size != snapshot.Config.Size || manifest.Config.Digest.String() != snapshot.Config.Digest {
		return errors.New("database update manifest/config media binding failed")
	}
	return nil
}

// Snapshot returns owned copies for later full-payload validation. A zero value
// cannot forge descriptor authentication or mutable captured document authority.
func (update *AuthenticatedDatabaseUpdate) Snapshot() (string, DatabaseUpdateDocument, bool) {
	if update == nil || !digestPattern.MatchString(update.digest) || update.document.SchemaVersion != 1 {
		return "", DatabaseUpdateDocument{}, false
	}
	document := update.document
	document.Databases = append([]DatabaseUpdateSnapshot(nil), update.document.Databases...)
	return update.digest, document, true
}

// ValidateCurrentMetadata reevaluates finite clocks on this constructor's
// authenticated immutable document. It does not revalidate physical trust,
// durable revocation, payload bytes or scan-used provenance. Durable owners
// perform those independent checks before accepting and committing state.
func (update *AuthenticatedDatabaseUpdate) ValidateCurrentMetadata(now time.Time) error {
	if _, _, valid := update.Snapshot(); !valid {
		return errors.New("database update authenticated metadata unavailable")
	}
	_, err := validateDatabaseUpdateDocument(update.document, update.document.Binding, update.timing, now, update.javaRequired)
	return err
}
