// Package databasejob implements bounded preparation/measurement DATA shared by
// the owned init process, native sidecar and worker. It cannot accept a feed,
// grant scan use or construct authority from a helper log.
package databasejob

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/distribution"
	"github.com/alphabravocompany/astronomer-go/internal/jsoncontract"
)

const MaximumRecordBytes = 16 << 10

var recordDigest = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)

// Preparation is comparison DATA. The worker independently binds its feed and
// scanner to locked current accepted intent; receipt of this file is not proof
// of authentication or scanner use.
type Preparation struct {
	SchemaVersion int                                 `json:"schema_version"`
	Kind          string                              `json:"kind"`
	ClaimID       uuid.UUID                           `json:"claim_id"`
	ScannerDigest string                              `json:"scanner_digest"`
	Cache         distribution.DatabaseCacheSnapshot  `json:"cache"`
	MainMetadata  distribution.DatabaseUpdateMetadata `json:"main_metadata"`
	JavaMetadata  distribution.DatabaseUpdateMetadata `json:"java_metadata"`
	Workload      *WorkloadInputSnapshot              `json:"workload,omitempty"`
}

// NewPreparation takes actual canonical opaque update and cache owners. It
// emits no filesystem path, secret, completion timestamp or Kubernetes UID.
func NewPreparation(ctx context.Context, update *distribution.AuthenticatedDatabaseUpdate, cache *distribution.MaterializedDatabaseCache, claimID uuid.UUID) (Preparation, error) {
	if ctx == nil || claimID.Version() != 4 || claimID.Variant() != uuid.RFC4122 {
		return Preparation{}, errors.New("database scan preparation inputs invalid")
	}
	feed, document, valid := update.Snapshot()
	if !valid || len(document.Databases) != 2 {
		return Preparation{}, errors.New("database scan authenticated roles unavailable")
	}
	measured, err := cache.Measure(ctx)
	if err != nil {
		return Preparation{}, err
	}
	if measured.FeedDigest != feed {
		return Preparation{}, errors.New("database scan cache belongs to another feed")
	}
	value := Preparation{SchemaVersion: 1, Kind: "AstronomerDatabaseScanPreparation", ClaimID: claimID, ScannerDigest: document.Binding.Scanner.ImageDigest, Cache: measured}
	for _, snapshot := range document.Databases {
		switch snapshot.Role {
		case "trivy-database":
			value.MainMetadata = snapshot.Metadata
		case "trivy-java-database":
			value.JavaMetadata = snapshot.Metadata
		default:
			return Preparation{}, errors.New("database scan role unsupported")
		}
	}
	if value.validate() != nil {
		return Preparation{}, errors.New("database scan preparation comparison invalid")
	}
	return value, nil
}

func DecodePreparation(payload []byte) (Preparation, error) {
	var value Preparation
	if len(payload) == 0 || len(payload) > MaximumRecordBytes || jsoncontract.Decode(payload, &value) != nil || value.validate() != nil {
		return Preparation{}, errors.New("database scan preparation contract invalid")
	}
	return value, nil
}

func (value Preparation) validate() error {
	if !validPreparationVersion(value) || value.ClaimID.Version() != 4 || value.ClaimID.Variant() != uuid.RFC4122 || !recordDigest.MatchString(value.ScannerDigest) || !recordDigest.MatchString(value.Cache.FeedDigest) {
		return errors.New("database scan preparation identity invalid")
	}
	var aggregate int64
	for index, content := range []distribution.DatabaseCacheContent{value.Cache.Main, value.Cache.Java} {
		role, name, metadata, schema := "trivy-database", "trivy.db", value.MainMetadata, 2
		if index == 1 {
			role, name, metadata, schema = "trivy-java-database", "trivy-java.db", value.JavaMetadata, 1
		}
		if content.Role != role || content.File != name || !recordDigest.MatchString(content.Digest) || content.Size <= 0 || content.Size > distribution.MaximumDatabaseExpandedBytes-aggregate {
			return errors.New("database scan preparation member invalid")
		}
		aggregate += content.Size
		if !recordDigest.MatchString(metadata.Digest) || metadata.Size <= 0 || metadata.Size > 64<<10 || metadata.Size > distribution.MaximumDatabaseExpandedBytes-aggregate || metadata.Schema != schema || metadata.UpdatedAt.IsZero() || metadata.UpdatedAt.Location() != time.UTC || metadata.NextUpdate.Location() != time.UTC || !metadata.NextUpdate.After(metadata.UpdatedAt) {
			return errors.New("database scan preparation metadata invalid")
		}
		aggregate += metadata.Size
	}
	return nil
}

// Measurement is a helper observation, never an acceptance/scan-used proof.
// ObservedAt is a helper clock only. The worker also requires the Kubernetes
// log timestamp and actual scanner start/finish ordering and runtime identities.
type Measurement struct {
	SchemaVersion      int                                `json:"schema_version"`
	Kind               string                             `json:"kind"`
	Phase              string                             `json:"phase"`
	ClaimID            uuid.UUID                          `json:"claim_id"`
	ScannerDigest      string                             `json:"scanner_digest"`
	Cache              distribution.DatabaseCacheSnapshot `json:"cache"`
	MainMetadataDigest string                             `json:"main_metadata_digest"`
	JavaMetadataDigest string                             `json:"java_metadata_digest"`
	ObservedAt         time.Time                          `json:"observed_at"`
	Workload           *WorkloadInputSnapshot             `json:"workload,omitempty"`
}

func measurementData(value Preparation, phase string, snapshot distribution.DatabaseCacheSnapshot, now time.Time) (Measurement, error) {
	if value.validate() != nil || (phase != "initial" && phase != "final") || snapshot != value.Cache || now.IsZero() || now.Location() != time.UTC {
		return Measurement{}, errors.New("database scan measurement inputs invalid")
	}
	return Measurement{SchemaVersion: value.SchemaVersion, Kind: measurementKind(value.SchemaVersion), Workload: cloneWorkloadSnapshot(value.Workload), Phase: phase, ClaimID: value.ClaimID, ScannerDigest: value.ScannerDigest, Cache: snapshot, MainMetadataDigest: value.MainMetadata.Digest, JavaMetadataDigest: value.JavaMetadata.Digest, ObservedAt: now}, nil
}
func emitMeasurement(writer io.Writer, value Preparation, phase string, snapshot distribution.DatabaseCacheSnapshot, now time.Time) error {
	if writer == nil {
		return errors.New("database scan measurement output unavailable")
	}
	row, err := measurementData(value, phase, snapshot, now)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(row)
	if err != nil || len(payload) > MaximumRecordBytes {
		return errors.New("database scan measurement encoding failed")
	}
	payload = append(payload, '\n')
	copied, err := writer.Write(payload)
	if err != nil || copied != len(payload) {
		return errors.New("database scan measurement output incomplete")
	}
	return nil
}

// DecodeMeasurement compares only DATA. It does not validate Kubernetes log
// provenance, actual process exit, selected image IDs or current DB authority.
func DecodeMeasurement(payload []byte, expected Preparation) (Measurement, error) {
	var row Measurement
	if expected.validate() != nil || len(payload) == 0 || len(payload) > MaximumRecordBytes || jsoncontract.Decode(payload, &row) != nil || row.SchemaVersion != expected.SchemaVersion || row.Kind != measurementKind(expected.SchemaVersion) || !sameWorkloadSnapshot(row.Workload, expected.Workload) || (row.Phase != "initial" && row.Phase != "final") || row.ClaimID != expected.ClaimID || row.ScannerDigest != expected.ScannerDigest || row.Cache != expected.Cache || row.MainMetadataDigest != expected.MainMetadata.Digest || row.JavaMetadataDigest != expected.JavaMetadata.Digest || row.ObservedAt.IsZero() || row.ObservedAt.Location() != time.UTC {
		return Measurement{}, errors.New("database cache measurement contract differs")
	}
	return row, nil
}

// Schema 2 adds an owned workload layout comparison while preserving historical
// schema 1 DB-only DATA. The real measured Job route must require schema 2;
// schema 1 can never satisfy the current workload/witness requirement.
func validPreparationVersion(value Preparation) bool {
	if value.SchemaVersion == 1 {
		return value.Kind == "AstronomerDatabaseScanPreparation" && value.Workload == nil
	}
	return value.SchemaVersion == 2 && value.Kind == "AstronomerOwnedDatabaseScanPreparation" && value.Workload != nil && validateWorkloadInputSnapshot(*value.Workload) == nil
}
func measurementKind(version int) string {
	if version == 2 {
		return "AstronomerOwnedDatabaseScanMeasurement"
	}
	return "AstronomerDatabaseCacheMeasurement"
}
func cloneWorkloadSnapshot(value *WorkloadInputSnapshot) *WorkloadInputSnapshot {
	if value == nil {
		return nil
	}
	copy := *value
	copy.Members = append([]WorkloadInputMember(nil), value.Members...)
	return &copy
}
func sameWorkloadSnapshot(left, right *WorkloadInputSnapshot) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	if left.ManifestDigest != right.ManifestDigest || left.Architecture != right.Architecture || left.IndexDigest != right.IndexDigest || len(left.Members) != len(right.Members) {
		return false
	}
	for index, member := range left.Members {
		if member != right.Members[index] {
			return false
		}
	}
	return true
}
func NewWorkloadPreparation(ctx context.Context, update *distribution.AuthenticatedDatabaseUpdate, cache *distribution.MaterializedDatabaseCache, workload *WorkloadInput, claim uuid.UUID) (Preparation, error) {
	value, err := NewPreparation(ctx, update, cache, claim)
	if err != nil {
		return Preparation{}, err
	}
	measured, err := workload.Measure(ctx)
	if err != nil {
		return Preparation{}, err
	}
	value.SchemaVersion, value.Kind, value.Workload = 2, "AstronomerOwnedDatabaseScanPreparation", cloneWorkloadSnapshot(&measured)
	if value.validate() != nil {
		return Preparation{}, errors.New("owned workload preparation invalid")
	}
	return value, nil
}
