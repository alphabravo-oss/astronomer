package distribution

import (
	"context"
	"errors"
	"slices"
	"time"

	builtinbundles "github.com/alphabravocompany/astronomer-go/deploy/bundles"
	"oras.land/oras-go/v2/registry/remote"
)

// DatabasePublicationRepositories is operator retrieval/destination DATA only.
// It cannot select trust, a client, or authentication. Each target must also
// pass the actual captured base constructor's independent network policy.
type DatabasePublicationRepositories struct{ Main, Java string }

func (repositories DatabasePublicationRepositories) Validate() error {
	for _, repository := range []string{repositories.Main, repositories.Java} {
		canonical, err := builtinbundles.NormalizeOCISourceURL("oci://" + repository)
		remoteRepository, remoteErr := remote.NewRepository(repository)
		if err != nil || canonical != "oci://"+repository || remoteErr != nil || remoteRepository.Reference.Reference != "" {
			return errors.New("database publication destination repository invalid")
		}
	}
	if repositories.Main == repositories.Java {
		return errors.New("database publication role destinations must be distinct")
	}
	return nil
}

// PreparedDatabasePublication is issued only after whole supplier payload
// inspection and the actual physical base. The child subjects are genuinely
// new supplier bytes; they need not already occur in the immutable base or carry
// an invented upstream signature. This object is NOT published/authenticated.
type PreparedDatabasePublication struct {
	authority *DatabaseAuthority
	proof     *VerifiedDatabaseAuthorityBase
	document  DatabaseUpdateDocument
}

func (authority *DatabaseAuthority) PrepareDatabasePublication(ctx context.Context, proof *VerifiedDatabaseAuthorityBase, main, java *VerifiedSupplierDatabasePayload, repositories DatabasePublicationRepositories, now time.Time) (*PreparedDatabasePublication, error) {
	if ctx == nil || authority == nil || proof == nil || proof.owner != authority || repositories.Validate() != nil || !databaseUpdateUTC(now) {
		return nil, errors.New("database publication physical or supplier constructor unavailable")
	}
	if _, err := authority.Catalog(ctx, proof); err != nil {
		return nil, err
	}
	snapshot, valid := proof.Snapshot()
	if !valid || !snapshot.Base.ScanningEnabled {
		return nil, errors.New("database publication current scanner is unavailable or disabled")
	}
	for _, repository := range []string{repositories.Main, repositories.Java} {
		if authority.loader.options.Network.ValidateURL(ctx, "https://"+repository) != nil {
			return nil, supplierDatabaseError(ctx, "database publication destination outside captured policy")
		}
	}
	mainSnapshot, err := supplierPublicationSnapshot(ctx, main, "trivy-database", snapshot.Base.Binding.Scanner, repositories.Main, now)
	if err != nil {
		return nil, err
	}
	javaSnapshot, err := supplierPublicationSnapshot(ctx, java, "trivy-java-database", snapshot.Base.Binding.Scanner, repositories.Java, now)
	if err != nil {
		return nil, err
	}
	document := DatabaseUpdateDocument{SchemaVersion: 1, Kind: "VulnerabilityDatabaseUpdate", Binding: snapshot.Base.Binding, Generation: 1, PublishedAt: now, ExpiresAt: now.Add(CadenceDatabaseUpdateTiming().FeedLifetime), Databases: []DatabaseUpdateSnapshot{mainSnapshot, javaSnapshot}}
	if _, err := validateDatabaseUpdateDocument(document, snapshot.Base.Binding, CadenceDatabaseUpdateTiming(), now, true); err != nil {
		return nil, err
	}
	if err := authority.Revalidate(ctx, proof); err != nil {
		return nil, err
	}
	return &PreparedDatabasePublication{authority: authority, proof: proof, document: document}, nil
}

func supplierPublicationSnapshot(ctx context.Context, proof *VerifiedSupplierDatabasePayload, role string, scanner DatabaseScannerCompatibility, repository string, now time.Time) (DatabaseUpdateSnapshot, error) {
	if ctx == nil || proof == nil || proof.owner == nil || proof.owner.options.Role != role || proof.owner.options.Scanner != scanner || !databaseUpdateUTC(now) {
		return DatabaseUpdateSnapshot{}, errors.New("database publication whole supplier proof differs")
	}
	snapshot, valid := proof.Snapshot()
	if !valid || snapshot.Role != role || validateDatabaseUpdateMetadata(role, snapshot.Metadata, scanner, proof.owner.options.Timing, now, now) != nil {
		return DatabaseUpdateSnapshot{}, errors.New("database publication actual supplier metadata stale or incompatible")
	}
	published := repository + "@" + snapshot.Manifest.Digest
	if !validDatabaseUpdateSubject(published, snapshot.Manifest.Digest) {
		return DatabaseUpdateSnapshot{}, errors.New("database publication unchanged child subject invalid")
	}
	if err := ctx.Err(); err != nil {
		return DatabaseUpdateSnapshot{}, err
	}
	return DatabaseUpdateSnapshot{Role: role, SupplierSubject: snapshot.SupplierSubject, PublishedSubject: published, Manifest: snapshot.Manifest, Config: snapshot.Config, Layer: snapshot.Layer, Metadata: snapshot.Metadata}, nil
}

// Document returns DATA for the owning reserved generation and signer. Mutating
// it cannot alter the private decision. It grants no publication or acceptance.
func (prepared *PreparedDatabasePublication) Document(generation int64) (DatabaseUpdateDocument, bool) {
	if prepared == nil || prepared.authority == nil || prepared.proof == nil || prepared.proof.owner != prepared.authority || generation <= 0 {
		return DatabaseUpdateDocument{}, false
	}
	document := prepared.document
	document.Generation = generation
	document.Databases = slices.Clone(document.Databases)
	return document, true
}

func (prepared *PreparedDatabasePublication) ValidateCurrent(ctx context.Context, now time.Time) error {
	if ctx == nil || prepared == nil || prepared.authority == nil || prepared.proof == nil {
		return errors.New("database publication preparation unavailable")
	}
	if err := prepared.authority.Revalidate(ctx, prepared.proof); err != nil {
		return err
	}
	_, err := validateDatabaseUpdateDocument(prepared.document, prepared.document.Binding, CadenceDatabaseUpdateTiming(), now, true)
	return err
}
