package distribution

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/resolver"
	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// DiscoverUpdate uses only this constructor's frozen transport and exact
// independently approved destination policy. The mutable tag is discovery
// DATA: LoadUpdate authenticates the immutable feed and child signatures before
// any staging or durable projection. The base release parent never changes.
func (authority *DatabaseAuthority) DiscoverUpdate(ctx context.Context, proof *VerifiedDatabaseAuthorityBase, repository string, now time.Time) (*AuthenticatedDatabaseUpdate, error) {
	if ctx == nil {
		return nil, errors.New("database update discovery context unavailable")
	}
	if err := authority.Revalidate(ctx, proof); err != nil {
		return nil, err
	}
	authority.mutex.RLock()
	if authority.closed || authority.loader.options.Network.ValidateURL(ctx, "https://"+repository) != nil {
		authority.mutex.RUnlock()
		return nil, supplierDatabaseError(ctx, "database update discovery destination denied")
	}
	resolved, err := resolver.ResolveDatabaseUpdateCurrent(ctx, authority.loader.options.Client, repository)
	authority.mutex.RUnlock()
	if err != nil {
		return nil, err
	}
	return authority.LoadUpdate(ctx, proof, repository, resolved, now)
}

// TransferUpdatePayload preserves the signed consumer boundary. Whole bytes
// are quarantined in the same private staging implementation as supplier
// transfers, with the same finite Range/hash/disk guards. Signature checks are
// additional, not replaced by TLS, a configured locator or a payload digest.
// Returned proof does not establish scan-used identity or durable acceptance.
func (authority *DatabaseAuthority) TransferUpdatePayload(ctx context.Context, proof *VerifiedDatabaseAuthorityBase, repository string, update *AuthenticatedDatabaseUpdate, role, outputPath string, now time.Time) (*VerifiedDatabaseUpdatePayload, error) {
	if ctx == nil || !filepath.IsAbs(outputPath) || filepath.Clean(outputPath) != outputPath || !databaseUpdateUTC(now) {
		return nil, errors.New("database update transfer inputs invalid")
	}
	feed, document, valid := update.Snapshot()
	if !valid {
		return nil, errors.New("database update transfer authenticated feed unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	started := time.Now()
	current, err := authority.LoadUpdate(ctx, proof, repository, feed, now)
	if err != nil {
		return nil, err
	}
	_, observed, valid := current.Snapshot()
	if !valid || !reflect.DeepEqual(document, observed) {
		return nil, errors.New("database update transfer signed decision differs")
	}
	var snapshot DatabaseUpdateSnapshot
	for _, item := range document.Databases {
		if item.Role == role {
			snapshot = item
		}
	}
	_, _, valid = strings.Cut(snapshot.PublishedSubject, "@")
	if !valid || snapshot.Layer.validate(role) != nil {
		return nil, errors.New("database update transfer signed role unavailable")
	}
	authority.mutex.RLock()
	defer authority.mutex.RUnlock()
	if authority.closed || proof == nil || proof.owner != authority || proof.fingerprint != authority.physicalFingerprint() {
		return nil, errors.New("database update transfer physical authority unavailable")
	}
	if err := authority.revalidatePhysicalTrust(ctx); err != nil {
		return nil, err
	}
	options := authority.loader.options
	location, err := databaseMirrorSubject(options.MirrorPlan, document.Binding, snapshot.PublishedSubject)
	if err != nil {
		return nil, err
	}
	child, subject, valid := strings.Cut(location, "@")
	if !valid {
		return nil, errors.New("database update transfer mirrored child invalid")
	}
	if options.Network.ValidateURL(ctx, "https://"+child) != nil {
		return nil, supplierDatabaseError(ctx, "database update transfer child destination denied")
	}
	state, err := newSupplierTransferState(outputPath, snapshot.Layer.Size)
	if err != nil {
		return nil, err
	}
	defer state.close()
	claim := ocispec.Descriptor{MediaType: snapshot.Layer.MediaType, Digest: digest.Digest(snapshot.Layer.Digest), Size: snapshot.Layer.Size}
	verification, err := resolver.StreamVerifiedOCIDatabase(ctx, options.Client, child, subject, claim, &supplierTransferWriter{file: state.file}, options.Policy, authority.trust.FrozenDirectory())
	if err != nil || verification.Status != "verified" {
		return nil, supplierDatabaseError(ctx, "database update signed complete transfer failed")
	}
	if err := state.finishStaging(snapshot.Layer.Size); err != nil {
		return nil, err
	}
	verified, err := InspectDatabaseUpdatePayload(ctx, current, role, state.temporary, CadenceDatabaseUpdateTiming(), now.Add(time.Since(started)))
	if err != nil {
		return nil, err
	}
	if err := authority.revalidatePhysicalTrust(ctx); err != nil {
		return nil, err
	}
	if err := current.ValidateCurrentMetadata(now.Add(time.Since(started))); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := state.commit(); err != nil {
		return nil, err
	}
	return verified, nil
}
