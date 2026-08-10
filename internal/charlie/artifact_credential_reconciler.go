package charlie

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/charlie/contract"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const artifactCredentialReconcileInterval = time.Minute

type artifactCredentialMaterializer interface {
	RotateArtifactCredential(context.Context, AgentInstallSpec) (string, error)
}

// ArtifactCredentialReconciler renews the package's OCI credential without
// rotating product-agent identity. It persists only opaque lease metadata and
// a digest; the temporary credential exists only between the mTLS response and
// Kubernetes Secret writes, and is recoverable solely by replaying the same
// durable request through the product agent.
type ArtifactCredentialReconciler struct {
	pool      *pgxpool.Pool
	bridge    ArtifactCredentialBridge
	installer artifactCredentialMaterializer
	now       func() time.Time
	interval  time.Duration
}

func NewArtifactCredentialReconciler(pool *pgxpool.Pool, bridge ArtifactCredentialBridge, installer artifactCredentialMaterializer) (*ArtifactCredentialReconciler, error) {
	if pool == nil || bridge == nil || installer == nil {
		return nil, fmt.Errorf("Charlie artifact credential reconciler dependencies are unavailable")
	}
	return &ArtifactCredentialReconciler{pool: pool, bridge: bridge, installer: installer, now: time.Now, interval: artifactCredentialReconcileInterval}, nil
}

func (r *ArtifactCredentialReconciler) Run(ctx context.Context) {
	if r == nil {
		return
	}
	_ = r.RunOnce(ctx)
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = r.RunOnce(ctx)
		}
	}
}

type artifactCredentialState struct {
	CurrentLeaseID, PendingLeaseID, PendingState, MaterializationDigest string
	CurrentGeneration, PendingGeneration                                int64
	RenewAfter, ExpiresAt                                               *time.Time
	PendingRequestID                                                    *uuid.UUID
}

func (r *ArtifactCredentialReconciler) RunOnce(ctx context.Context) (runErr error) {
	connection, err := sqlc.New(r.pool).GetActiveCharlieConnection(ctx)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	poolConn, err := r.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer poolConn.Release()
	var locked bool
	if err := poolConn.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtextextended($1,0))`, "charlie-artifact:"+connection.ID.String()).Scan(&locked); err != nil || !locked {
		return err
	}
	defer func() {
		_, _ = poolConn.Exec(context.Background(), `SELECT pg_advisory_unlock(hashtextextended($1,0))`, "charlie-artifact:"+connection.ID.String())
	}()

	_, err = poolConn.Exec(ctx, `INSERT INTO charlie_artifact_credential_state(connection_id,expires_at)
		VALUES($1,$2) ON CONFLICT(connection_id) DO NOTHING`, connection.ID, connection.ArtifactCredentialExpiresAt)
	if err != nil {
		return err
	}
	fail := func(code string, cause error) error {
		_, _ = poolConn.Exec(ctx, `UPDATE charlie_artifact_credential_state SET last_error_code=$2,attempt_count=attempt_count+1,updated_at=now() WHERE connection_id=$1`, connection.ID, code)
		return cause
	}

	status, err := r.bridge.ArtifactCredentialStatus(ctx)
	if err != nil {
		return fail("artifact.status_failed", err)
	}
	if err := validateArtifactLeaseBinding(status, connection, false); err != nil {
		return fail("artifact.status_invalid", err)
	}
	now := r.now().UTC()
	state, err := readArtifactCredentialState(ctx, poolConn, connection.ID)
	if err != nil {
		return fail("artifact.state_failed", err)
	}
	// Crash recovery after Central committed the acknowledgement but before the
	// product committed its local metadata. Replaying the acknowledgement proves
	// the digest, then only the secret-free bookkeeping remains.
	if status.State == contract.ArtifactCredentialLeaseActive && state.PendingRequestID != nil &&
		string(status.RequestId) == state.PendingRequestID.String() && state.PendingLeaseID == string(status.LeaseId) &&
		state.PendingGeneration == status.Generation && state.PendingState == "materialized" && exactDigest(state.MaterializationDigest) {
		confirmed, confirmErr := r.bridge.AcknowledgeArtifactCredential(ctx, ArtifactCredentialAcknowledgement{
			RequestID: state.PendingRequestID.String(), LeaseID: state.PendingLeaseID, Generation: state.PendingGeneration, MaterializationDigest: state.MaterializationDigest,
		})
		if confirmErr != nil || confirmed.State != contract.ArtifactCredentialLeaseActive || confirmed.Generation != status.Generation {
			if confirmErr == nil {
				confirmErr = fmt.Errorf("Charlie artifact credential acknowledgement recovery changed binding")
			}
			return fail("artifact.acknowledgement_failed", confirmErr)
		}
		return r.finalize(ctx, poolConn, connection, confirmed, *state.PendingRequestID, fail)
	}

	var requestID uuid.UUID
	var currentGeneration int64
	switch status.State {
	case contract.ArtifactCredentialLeaseActive:
		if status.Credential != nil || status.Generation < state.CurrentGeneration {
			return fail("artifact.status_invalid", fmt.Errorf("Charlie artifact credential generation regressed"))
		}
		_, err = poolConn.Exec(ctx, `UPDATE charlie_artifact_credential_state SET current_lease_id=$2,current_generation=$3,renew_after=$4,expires_at=$5,
			pending_request_id=NULL,pending_lease_id='',pending_generation=0,pending_state='idle',materialization_digest='',last_error_code='',updated_at=now()
			WHERE connection_id=$1 AND pending_state='idle'`, connection.ID, string(status.LeaseId), status.Generation, status.RenewAfter, status.ExpiresAt)
		if err != nil {
			return fail("artifact.state_failed", err)
		}
		if now.Before(status.RenewAfter.UTC()) {
			return nil
		}
		currentGeneration = status.Generation
		if state.PendingRequestID != nil {
			requestID = *state.PendingRequestID
		} else {
			requestID = uuid.New()
		}
		_, err = poolConn.Exec(ctx, `UPDATE charlie_artifact_credential_state SET pending_request_id=$2,pending_lease_id='',pending_generation=0,
			pending_state='claiming',materialization_digest='',last_error_code='',updated_at=now() WHERE connection_id=$1`, connection.ID, requestID)
	case contract.ArtifactCredentialLeasePending:
		if status.Credential != nil || status.Generation < 2 {
			return fail("artifact.status_invalid", fmt.Errorf("Charlie pending artifact credential is invalid"))
		}
		parsed, parseErr := uuid.Parse(string(status.RequestId))
		if parseErr != nil {
			return fail("artifact.status_invalid", fmt.Errorf("Charlie pending artifact request is not product-owned"))
		}
		requestID, currentGeneration = parsed, status.Generation-1
		_, err = poolConn.Exec(ctx, `UPDATE charlie_artifact_credential_state SET current_generation=GREATEST(current_generation,$2),pending_request_id=$3,
			pending_lease_id=$4,pending_generation=$5,pending_state='claimed',materialization_digest='',last_error_code='',updated_at=now() WHERE connection_id=$1`,
			connection.ID, currentGeneration, requestID, string(status.LeaseId), status.Generation)
	default:
		return fail("artifact.status_invalid", fmt.Errorf("Charlie artifact credential lease is not renewable"))
	}
	if err != nil {
		return fail("artifact.state_failed", err)
	}

	claim := ArtifactCredentialClaim{RequestID: requestID.String(), DeploymentID: connection.DeploymentID, IntegrationID: string(status.IntegrationId), PackageID: connection.OnboardingPackageID, CurrentGeneration: currentGeneration}
	lease, err := r.bridge.ClaimArtifactCredential(ctx, claim)
	if err != nil {
		return fail("artifact.claim_failed", err)
	}
	if err := validateArtifactLeaseBinding(lease, connection, true); err != nil || lease.RequestId != contract.OpaqueId(requestID.String()) || lease.Generation != currentGeneration+1 || lease.IntegrationId != status.IntegrationId {
		if err == nil {
			err = fmt.Errorf("Charlie artifact credential claim changed binding")
		}
		return fail("artifact.claim_invalid", err)
	}
	credential := strings.TrimSpace(*lease.Credential)
	result, err := poolConn.Exec(ctx, `UPDATE charlie_artifact_credential_state SET pending_lease_id=$2,pending_generation=$3,pending_state='claimed',
		materialization_digest='',last_error_code='',updated_at=now() WHERE connection_id=$1 AND pending_request_id=$4`,
		connection.ID, string(lease.LeaseId), lease.Generation, requestID)
	if err != nil || result.RowsAffected() != 1 {
		if err == nil {
			err = fmt.Errorf("Charlie artifact credential claim state changed concurrently")
		}
		return fail("artifact.state_failed", err)
	}
	digest, err := r.installer.RotateArtifactCredential(ctx, AgentInstallSpec{
		InstallationID: connection.InstallationID, ConnectionID: connection.ID, DeploymentID: connection.DeploymentID,
		OnboardingPackageID: connection.OnboardingPackageID, ChartReference: connection.ChartReference, ImageReference: connection.ImageReference,
		ArtifactCredential: credential, SecretPrefix: connection.AgentSecretName,
	})
	if err != nil || !exactDigest(digest) {
		if err == nil {
			err = fmt.Errorf("Charlie artifact materialization digest is invalid")
		}
		return fail("artifact.materialization_failed", err)
	}
	result, err = poolConn.Exec(ctx, `UPDATE charlie_artifact_credential_state SET pending_state='materialized',materialization_digest=$2,last_error_code='',updated_at=now()
		WHERE connection_id=$1 AND pending_request_id=$3 AND pending_lease_id=$4 AND pending_generation=$5`, connection.ID, digest, requestID, string(lease.LeaseId), lease.Generation)
	if err != nil || result.RowsAffected() != 1 {
		if err == nil {
			err = fmt.Errorf("Charlie artifact credential materialization state changed concurrently")
		}
		return fail("artifact.state_failed", err)
	}
	acknowledged, err := r.bridge.AcknowledgeArtifactCredential(ctx, ArtifactCredentialAcknowledgement{
		RequestID: requestID.String(), LeaseID: string(lease.LeaseId), Generation: lease.Generation, MaterializationDigest: digest,
	})
	if err != nil {
		return fail("artifact.acknowledgement_failed", err)
	}
	if err := validateArtifactLeaseBinding(acknowledged, connection, false); err != nil || acknowledged.State != contract.ArtifactCredentialLeaseActive || acknowledged.Generation != lease.Generation || acknowledged.RequestId != lease.RequestId {
		if err == nil {
			err = fmt.Errorf("Charlie artifact credential acknowledgement changed binding")
		}
		return fail("artifact.acknowledgement_invalid", err)
	}
	return r.finalize(ctx, poolConn, connection, acknowledged, requestID, fail)
}

func (r *ArtifactCredentialReconciler) finalize(ctx context.Context, poolConn *pgxpool.Conn, connection sqlc.CharlieConnection, acknowledged contract.ArtifactCredentialLease, requestID uuid.UUID, fail func(string, error) error) error {
	tx, err := poolConn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return fail("artifact.state_failed", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	result, err := tx.Exec(ctx, `UPDATE charlie_artifact_credential_state SET current_lease_id=$2,current_generation=$3,renew_after=$4,expires_at=$5,
		pending_request_id=NULL,pending_lease_id='',pending_generation=0,pending_state='idle',materialization_digest='',last_error_code='',attempt_count=0,
		acknowledged_at=now(),updated_at=now() WHERE connection_id=$1 AND pending_request_id=$6 AND pending_lease_id=$2`,
		connection.ID, string(acknowledged.LeaseId), acknowledged.Generation, acknowledged.RenewAfter, acknowledged.ExpiresAt, requestID)
	if err == nil && result.RowsAffected() == 1 {
		_, err = tx.Exec(ctx, `UPDATE charlie_connections SET artifact_credential_expires_at=$2,last_rotated_at=now(),updated_at=now() WHERE id=$1`, connection.ID, acknowledged.ExpiresAt)
	} else if err == nil {
		err = fmt.Errorf("Charlie artifact credential state changed concurrently")
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	if err != nil {
		return fail("artifact.state_failed", err)
	}
	return nil
}

func readArtifactCredentialState(ctx context.Context, conn *pgxpool.Conn, connectionID uuid.UUID) (artifactCredentialState, error) {
	var state artifactCredentialState
	var renewAfter, expiresAt *time.Time
	err := conn.QueryRow(ctx, `SELECT current_lease_id,current_generation,renew_after,expires_at,pending_request_id,pending_lease_id,pending_generation,pending_state,materialization_digest
		FROM charlie_artifact_credential_state WHERE connection_id=$1`, connectionID).Scan(&state.CurrentLeaseID, &state.CurrentGeneration, &renewAfter, &expiresAt,
		&state.PendingRequestID, &state.PendingLeaseID, &state.PendingGeneration, &state.PendingState, &state.MaterializationDigest)
	state.RenewAfter, state.ExpiresAt = renewAfter, expiresAt
	return state, err
}

func validateArtifactLeaseBinding(lease contract.ArtifactCredentialLease, connection sqlc.CharlieConnection, requireCredential bool) error {
	if string(lease.LeaseId) == "" || string(lease.RequestId) == "" || string(lease.DeploymentId) != connection.DeploymentID ||
		string(lease.PackageId) != connection.OnboardingPackageID || string(lease.IntegrationId) == "" || lease.Generation < 1 ||
		!lease.RenewAfter.Before(lease.ExpiresAt) || !lease.IssuedAt.Before(lease.ExpiresAt) {
		return fmt.Errorf("Charlie artifact credential lease binding is invalid")
	}
	if requireCredential {
		if lease.State != contract.ArtifactCredentialLeasePending || lease.Credential == nil || len(strings.TrimSpace(*lease.Credential)) < 24 {
			return fmt.Errorf("Charlie pending artifact credential is unavailable")
		}
	} else if lease.Credential != nil {
		return fmt.Errorf("Charlie artifact credential secret appeared outside a claim")
	}
	return nil
}
