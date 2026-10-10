package distribution

import (
	"errors"
	"regexp"
	"strings"
	"time"

	builtinbundles "github.com/alphabravocompany/astronomer-go/deploy/bundles"
	"oras.land/oras-go/v2/registry/remote"
)

const DatabaseUpdateMediaType = "application/vnd.astronomer.vulnerability-db-update.v1+json"
const MaximumDatabaseUpdateBytes int64 = 1 << 20

var databaseUpdateCommit = regexp.MustCompile(`^[a-f0-9]{40}$`)
var databaseUpdateVersion = regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+$`)

// DatabaseUpdateBinding is constructor-owned candidate/profile/scanner intent.
// An update document cannot replace these fields or choose trust/network policy.
type DatabaseUpdateBinding struct {
	BaseRelease             string                       `json:"base_release"`
	BaseDistributionDigest  string                       `json:"base_distribution_digest"`
	MirrorEndorsementDigest string                       `json:"mirror_endorsement_digest,omitempty"`
	CatalogDigest           string                       `json:"catalog_digest"`
	Profile                 string                       `json:"profile"`
	CandidateSourceCommit   string                       `json:"candidate_source_commit"`
	Scanner                 DatabaseScannerCompatibility `json:"scanner"`
}

type DatabaseScannerCompatibility struct {
	ImageDigest string `json:"image_digest"`
	Version     string `json:"version"`
	MainSchema  int    `json:"main_schema"`
	JavaSchema  int    `json:"java_schema"`
}

// DatabaseUpdateDocument is data, not an authentication or freshness proof.
// Only the canonical signed OCI consumer may promote it to an opaque update.
type DatabaseUpdateDocument struct {
	SchemaVersion int                      `json:"schema_version"`
	Kind          string                   `json:"kind"`
	Binding       DatabaseUpdateBinding    `json:"binding"`
	Generation    int64                    `json:"generation"`
	PublishedAt   time.Time                `json:"published_at"`
	ExpiresAt     time.Time                `json:"expires_at"`
	Databases     []DatabaseUpdateSnapshot `json:"databases"`
}

// The exact unchanged subject binds all config/layer bytes. Metadata clocks
// describe upstream content; they are not measured scan-used provenance.
type DatabaseUpdateSnapshot struct {
	Role             string                 `json:"role"`
	SupplierSubject  string                 `json:"supplier_subject"`
	PublishedSubject string                 `json:"published_subject"`
	Manifest         DatabaseDescriptor     `json:"manifest"`
	Config           DatabaseDescriptor     `json:"config"`
	Layer            DatabaseDescriptor     `json:"layer"`
	Metadata         DatabaseUpdateMetadata `json:"metadata"`
}

type DatabaseUpdateMetadata struct {
	Digest     string    `json:"digest"`
	Size       int64     `json:"size"`
	Schema     int       `json:"schema"`
	UpdatedAt  time.Time `json:"updated_at"`
	NextUpdate time.Time `json:"next_update"`
}

// Timing is operator-owned. A signed future NextUpdate never extends these
// finite age/lifetime ceilings. Defaults remain pending supplier measurement.
type DatabaseUpdateTiming struct {
	MainMaximumAge time.Duration
	JavaMaximumAge time.Duration
	FeedLifetime   time.Duration
	ClockSkew      time.Duration
}

func CadenceDatabaseUpdateTiming() DatabaseUpdateTiming {
	return DatabaseUpdateTiming{MainMaximumAge: 30 * time.Hour, JavaMaximumAge: 48 * time.Hour, FeedLifetime: 48 * time.Hour, ClockSkew: 5 * time.Minute}
}

func (binding DatabaseUpdateBinding) validate() error {
	if binding.MirrorEndorsementDigest != "" && !digestPattern.MatchString(binding.MirrorEndorsementDigest) {
		return errors.New("database update mirror binding invalid")
	}
	if !databaseUpdateVersion.MatchString(binding.BaseRelease) || !digestPattern.MatchString(binding.BaseDistributionDigest) || !digestPattern.MatchString(binding.CatalogDigest) || binding.Profile != "builtins" || !databaseUpdateCommit.MatchString(binding.CandidateSourceCommit) {
		return errors.New("database update base/candidate binding invalid")
	}
	if !digestPattern.MatchString(binding.Scanner.ImageDigest) || !databaseUpdateVersion.MatchString(binding.Scanner.Version) || binding.Scanner.MainSchema != 2 || binding.Scanner.JavaSchema != 1 {
		return errors.New("database update scanner compatibility invalid")
	}
	return nil
}

func (timing DatabaseUpdateTiming) validate() error {
	if timing.MainMaximumAge <= 0 || timing.MainMaximumAge > 30*time.Hour || timing.JavaMaximumAge <= 0 || timing.JavaMaximumAge > 48*time.Hour || timing.FeedLifetime <= 0 || timing.FeedLifetime > 48*time.Hour || timing.ClockSkew < 0 || timing.ClockSkew > 5*time.Minute {
		return errors.New("database update timing exceeds finite owned policy")
	}
	return nil
}

// DecodeDatabaseUpdateDocument is a bounded closed parser only. Callers must
// authenticate exact original OCI subject bytes before invoking it for updates.
func DecodeDatabaseUpdateDocument(payload []byte, expected DatabaseUpdateBinding, timing DatabaseUpdateTiming, now time.Time, javaRequired bool) (DatabaseUpdateDocument, error) {
	var document DatabaseUpdateDocument
	if len(payload) == 0 || int64(len(payload)) > MaximumDatabaseUpdateBytes || now.IsZero() || expected.validate() != nil || timing.validate() != nil {
		return document, errors.New("database update inputs invalid")
	}
	if decodeClosed(payload, &document) != nil {
		return DatabaseUpdateDocument{}, errors.New("database update closed contract or binding invalid")
	}
	return validateDatabaseUpdateDocument(document, expected, timing, now, javaRequired)
}

// The decoder and an already-authenticated update share the exact finite clock
// and descriptor rules. This typed validation does not grant authentication.
func validateDatabaseUpdateDocument(document DatabaseUpdateDocument, expected DatabaseUpdateBinding, timing DatabaseUpdateTiming, now time.Time, javaRequired bool) (DatabaseUpdateDocument, error) {
	if now.IsZero() || expected.validate() != nil || timing.validate() != nil {
		return DatabaseUpdateDocument{}, errors.New("database update inputs invalid")
	}
	if document.SchemaVersion != 1 || document.Kind != "VulnerabilityDatabaseUpdate" || document.Binding != expected || document.Generation <= 0 || len(document.Databases) == 0 || len(document.Databases) > 2 {
		return DatabaseUpdateDocument{}, errors.New("database update closed contract or binding invalid")
	}
	if !databaseUpdateUTC(document.PublishedAt) || !databaseUpdateUTC(document.ExpiresAt) || document.PublishedAt.After(now.Add(timing.ClockSkew)) || !document.ExpiresAt.After(document.PublishedAt) || document.ExpiresAt.Sub(document.PublishedAt) > timing.FeedLifetime || !now.Before(document.ExpiresAt) {
		return DatabaseUpdateDocument{}, errors.New("database update feed expired or timestamps invalid")
	}
	roles := map[string]bool{}
	layers := map[string]DatabaseDescriptor{}
	for _, snapshot := range document.Databases {
		if roles[snapshot.Role] || snapshot.validate(expected.Scanner, timing, now, document.PublishedAt) != nil {
			return DatabaseUpdateDocument{}, errors.New("database update snapshot invalid or stale")
		}
		roles[snapshot.Role] = true
		if previous, exists := layers[snapshot.Layer.Digest]; exists && previous != snapshot.Layer {
			return DatabaseUpdateDocument{}, errors.New("database update repeated layer descriptor differs")
		}
		layers[snapshot.Layer.Digest] = snapshot.Layer
	}
	if !roles["trivy-database"] || javaRequired && !roles["trivy-java-database"] {
		return DatabaseUpdateDocument{}, errors.New("database update omits required role")
	}
	return document, nil
}

func (snapshot DatabaseUpdateSnapshot) validate(scanner DatabaseScannerCompatibility, timing DatabaseUpdateTiming, now, published time.Time) error {
	if snapshot.Layer.validate(snapshot.Role) != nil || !validDatabaseUpdateSubject(snapshot.SupplierSubject, snapshot.Manifest.Digest) || !validDatabaseUpdateSubject(snapshot.PublishedSubject, snapshot.Manifest.Digest) || !validDatabaseUpdateSmallDescriptor(snapshot.Manifest, "application/vnd.oci.image.manifest.v1+json") || !validDatabaseUpdateConfig(snapshot.Config) {
		return errors.New("database update immutable child claim invalid")
	}
	return validateDatabaseUpdateMetadata(snapshot.Role, snapshot.Metadata, scanner, timing, now, published)
}

func validateDatabaseUpdateMetadata(role string, metadata DatabaseUpdateMetadata, scanner DatabaseScannerCompatibility, timing DatabaseUpdateTiming, now, published time.Time) error {
	maximumAge, schema := timing.MainMaximumAge, scanner.MainSchema
	if role == "trivy-java-database" {
		maximumAge, schema = timing.JavaMaximumAge, scanner.JavaSchema
	}
	if !digestPattern.MatchString(metadata.Digest) || metadata.Size <= 0 || metadata.Size > 64<<10 || metadata.Schema != schema || !databaseUpdateUTC(metadata.UpdatedAt) || !databaseUpdateUTC(metadata.NextUpdate) || metadata.UpdatedAt.After(now.Add(timing.ClockSkew)) || metadata.UpdatedAt.After(published.Add(timing.ClockSkew)) || !metadata.NextUpdate.After(metadata.UpdatedAt) || !now.Before(metadata.NextUpdate) || now.Sub(metadata.UpdatedAt) > maximumAge {
		return errors.New("database update upstream metadata invalid or stale")
	}
	return nil
}

// UTC permits zero-offset RFC3339 representations, never local wall-clock
// offsets. The caller's evaluation clock is an instant, not signed feed data.
func databaseUpdateUTC(value time.Time) bool {
	_, offset := value.Zone()
	return !value.IsZero() && offset == 0
}

func validDatabaseUpdateSmallDescriptor(descriptor DatabaseDescriptor, media string) bool {
	return digestPattern.MatchString(descriptor.Digest) && descriptor.Size > 0 && descriptor.Size <= 1<<20 && descriptor.MediaType == media
}

func validDatabaseUpdateSubject(subject, manifest string) bool {
	repository, observed, ok := strings.Cut(subject, "@")
	if !ok || observed != manifest || !digestPattern.MatchString(observed) {
		return false
	}
	canonical, err := builtinbundles.NormalizeOCISourceURL("oci://" + repository)
	reference, referenceError := remote.NewRepository(repository)
	return err == nil && canonical == "oci://"+repository && referenceError == nil && reference.Reference.Reference == ""
}

func validDatabaseUpdateConfig(descriptor DatabaseDescriptor) bool {
	if descriptor.MediaType == "application/vnd.oci.empty.v1+json" {
		return descriptor.Size == 2 && descriptor.Digest == contentDigest([]byte("{}"))
	}
	return validDatabaseUpdateSmallDescriptor(descriptor, "application/vnd.aquasec.trivy.config.v1+json")
}
