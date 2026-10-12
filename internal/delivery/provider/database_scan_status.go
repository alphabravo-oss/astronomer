package provider

import (
	"context"
	"errors"
	"reflect"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/jsoncontract"
	"github.com/alphabravocompany/astronomer-go/internal/scanner"
	"github.com/alphabravocompany/astronomer-go/internal/scanner/databasejob"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// ReadImageDatabaseStatus performs only read/lock queries in a finite owned
// transaction. It never finalizes a provider snapshot, refreshes a projection,
// stamps a user as a machine, writes a witness or forwards Kubernetes requests.
// Physical/current state is read together with report revision and prior witness.
func (owner *DatabaseScanOwner) ReadImageDatabaseStatus(ctx context.Context, cluster, reportID uuid.UUID) (status protocol.ImageDatabaseStatusV1, resultErr error) {
	status = protocol.ImageDatabaseStatusV1{ReportID: reportID.String(), ScanState: "unavailable", MainState: "unknown", JavaState: "unknown", JavaApplicability: "unknown", Reason: "provenance-unavailable"}
	if ctx == nil || owner == nil || owner.pool == nil || cluster == uuid.Nil || reportID == uuid.Nil {
		return status, errors.New("owned database status reader unavailable")
	}
	bounded, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	defer func() {
		if err := bounded.Err(); err != nil {
			resultErr = err
			status.EligibleForCleanPosture = false
		}
	}()
	connection, err := owner.pool.Acquire(bounded)
	if err != nil {
		return status, safeOwnedSnapshotError(bounded, "database status connection", err)
	}
	session := sqlc.New(connection)
	attempted, committed := false, false
	var transaction pgx.Tx
	defer func() { finishOwnedSnapshotSession(connection, session, &transaction, cluster, &attempted, &committed) }()
	attempted = true
	if err := session.LockBuiltinAssetsSession(bounded, cluster.String()); err != nil {
		return status, safeOwnedSnapshotError(bounded, "database status lock", err)
	}
	transaction, err = connection.BeginTx(bounded, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return status, safeOwnedSnapshotError(bounded, "database status transaction", err)
	}
	queries := sqlc.New(transaction)
	report, err := queries.GetImageVulnerabilityReportByID(bounded, reportID)
	if err != nil || report.ClusterID != cluster {
		return status, errors.New("owned database status report unavailable")
	}
	status.FindingCount = int64(report.CriticalCount) + int64(report.HighCount) + int64(report.MediumCount) + int64(report.LowCount) + int64(report.UnknownCount)
	if status.FindingCount < 0 {
		return status, errors.New("owned database status finding count invalid")
	}
	status.ScanState = "complete"
	observed, err := queries.GetImageVulnerabilityObservation(bounded, reportID)
	if err != nil {
		return status, nil
	}
	status.ObservationRevision = observed.Revision
	// Legacy scanned_at/received_at are never the freshness clock. Missing or
	// invalid upstream time leaves report age UNKNOWN without hiding findings.
	if observed.TimeState == "valid" && observed.UpstreamTime.Valid && !observed.UpstreamTime.Time.After(time.Now().UTC()) {
		status.ReportExpired = time.Since(observed.UpstreamTime.Time) >= 6*time.Hour
		status.RescanRequired = status.ReportExpired
	}
	count, err := queries.GetOwnedDatabaseResourceAllocationCount(bounded, sqlc.GetOwnedDatabaseResourceAllocationCountParams{ClusterID: cluster, ClaimID: uuid.Nil})
	if err != nil {
		return status, safeOwnedSnapshotError(bounded, "database status resource count", err)
	}
	status.ResourceCapacityExhausted = count.Unremoved >= databaseScanMaximumUnremovedAllocations
	if status.ResourceCapacityExhausted {
		status.Reason = "resource-capacity-exhausted"
	}
	if err := owner.revalidateScanMaterial(bounded); err != nil {
		status.MainState = "unavailable"
		status.Reason = "current-physical-authority-unavailable"
		return status, nil
	}
	if err := owner.projection.revalidateOwnedSnapshot(bounded); err != nil {
		status.MainState = "unavailable"
		status.Reason = "current-physical-authority-unavailable"
		return status, nil
	}
	state, err := owner.lockCurrentDatabaseScanState(bounded, queries, cluster)
	if err != nil {
		status.MainState = "unavailable"
		status.Reason = "current-accepted-authority-unavailable"
		return status, nil
	}
	claim, err := queries.GetLatestOwnedDatabaseScanForReport(bounded, sqlc.GetLatestOwnedDatabaseScanForReportParams{ClusterID: cluster, ReportID: reportID})
	if err == nil {
		status.Cycle = claim.Cycle
		switch claim.Phase {
		case "pending-preparation", "preparing", "prepared", "scanning":
			status.ScanState = "pending"
		case "failed", "superseded":
			status.ScanState = "failed"
		}
	}
	witness, err := queries.GetCurrentImageVulnerabilityDatabaseWitnessForUpdate(bounded, reportID)
	if err != nil {
		if status.Reason != "resource-capacity-exhausted" {
			status.Reason = "scan-used-witness-unavailable"
		}
		return status, nil
	}
	status.WitnessRevision = witness.ReportObservationRevision
	if claim.ID != witness.ClaimID || claim.ReportID.Bytes != reportID || !claim.ReportID.Valid || !databaseScanClaimMatchesState(claim, state) || claim.Phase != "completed" || status.ObservationRevision != status.WitnessRevision || report.ImageDigest != claim.WorkloadManifestDigest {
		status.MainState = "stale"
		status.RescanRequired = true
		status.Reason = "current-assignment-or-report-revision-changed"
		return status, nil
	}
	update, err := owner.projection.authority.LoadUpdate(bounded, owner.projection.proof, owner.repository, claim.FeedDigest, time.Now().UTC())
	if err != nil {
		status.MainState = "unavailable"
		status.Reason = "authenticated-feed-unavailable"
		return status, nil
	}
	feed, document, valid := update.Snapshot()
	var prepared databasejob.Preparation
	if !valid || feed != claim.FeedDigest || document.Generation != state.UpdateGeneration || jsoncontract.Decode(claim.PreparedRecord, &prepared) != nil {
		return status, errors.New("owned database status retained feed or preparation differs")
	}
	decoded, err := databasejob.DecodePreparation(claim.PreparedRecord)
	if err != nil || !reflect.DeepEqual(decoded, prepared) || prepared.ClaimID != claim.ID || prepared.Workload == nil {
		return status, errors.New("owned database status preparation invalid")
	}
	measured := scanner.ScanUseObservation{JobUID: witness.ScanJobUid.String(), ReportUID: reportID.String(), ImageDigest: witness.WorkloadManifestDigest, ScannerDigest: claim.ScannerIndexDigest, AssignmentGeneration: claim.AssignmentGeneration, CompletedAt: witness.ScannerFinishedAt, State: scanner.WorkloadScanComplete, FindingCount: status.FindingCount, JavaApplicability: scanner.JavaDatabaseUnknown}
	accepted := &scanner.AcceptedDatabaseState{FeedDigest: feed, ScannerDigest: document.Binding.Scanner.ImageDigest, Generation: document.Generation, ExpiresAt: document.ExpiresAt, MirrorReady: true}
	for _, database := range document.Databases {
		value := scanner.UsedDatabaseObservation{SubjectDigest: database.Manifest.Digest, LayerDigest: database.Layer.Digest, MetadataDigest: database.Metadata.Digest, Schema: database.Metadata.Schema, UpdatedAt: database.Metadata.UpdatedAt, NextUpdate: database.Metadata.NextUpdate}
		used := value
		usedMetadata := prepared.MainMetadata
		if database.Role == "trivy-java-database" {
			usedMetadata = prepared.JavaMetadata
		}
		if usedMetadata != database.Metadata {
			return status, errors.New("owned database status used metadata differs from authenticated feed")
		}
		used.MetadataDigest = usedMetadata.Digest
		used.Schema = usedMetadata.Schema
		used.UpdatedAt = usedMetadata.UpdatedAt
		used.NextUpdate = usedMetadata.NextUpdate
		switch database.Role {
		case "trivy-database":
			accepted.Main = value
			measured.Main = used
		case "trivy-java-database":
			accepted.Java = value
			measured.Java = used
		}
	}
	if prepared.Cache.Main.Digest != witness.MainDatabaseDigest || prepared.Cache.Main.Size != witness.MainDatabaseSize || prepared.Cache.Java.Digest != witness.JavaDatabaseDigest || prepared.Cache.Java.Size != witness.JavaDatabaseSize || prepared.Workload.IndexDigest != witness.WorkloadLayoutDigest {
		return status, errors.New("owned database status measured cache differs")
	}
	// No configured locator becomes a measured database: the immutable witness
	// and authenticated prepared record were joined to this exact observation.
	result := scanner.EvaluateDatabaseFreshness(time.Now().UTC(), scanner.DatabaseFreshnessPolicy{ReportTTL: 6 * time.Hour, MainMaximumAge: 30 * time.Hour, JavaMaximumAge: 48 * time.Hour, ClockSkew: 0}, scanner.ScanTargetBinding{ImageDigest: claim.WorkloadManifestDigest, AssignmentGeneration: state.AssignmentGeneration}, measured, accepted)
	status.AcceptedFeedDigest = feed
	status.AcceptedGeneration = document.Generation
	status.MainState = string(result.MainState)
	status.JavaState = string(result.JavaState)
	status.ReportExpired = result.ReportExpired
	status.RescanRequired = status.RescanRequired || result.RescanRequired
	// This GET is not a joined current workload observation and cannot promote
	// its user origin. The API therefore withholds a qualified clean posture even
	// for a prior positive job witness. Java behavior is independently UNKNOWN.
	status.CurrentWorkloadChecked = false
	status.EligibleForCleanPosture = false
	if status.Reason != "resource-capacity-exhausted" {
		status.Reason = "current-workload-and-java-applicability-unknown"
	}
	if err := owner.projection.revalidateOwnedSnapshot(bounded); err != nil {
		status.MainState = "unavailable"
		status.Reason = "current-physical-authority-unavailable"
		return status, nil
	}
	if err := transaction.Commit(bounded); err != nil {
		return status, safeOwnedSnapshotError(bounded, "database status commit", err)
	}
	committed = true
	return status, nil
}
