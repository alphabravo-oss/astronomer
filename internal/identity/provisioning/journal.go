package provisioning

import (
	"context"
	"errors"
	"math"
	"regexp"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/pkg/safeconv"
)

var specDigestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// Intent contains no credentials. The caller must authorize the actor for the
// project before preparing it. Preparation additionally verifies the current
// installation/target/project/cluster relationship in the database.
type Intent struct {
	Scope               Scope
	CredentialNamespace string
	DesiredSpecDigest   string
	ActorID             uuid.UUID
	CorrelationID       uuid.UUID
}

// Journal commits operation state and its audit outbox entry atomically. It has
// no issuer client: callers cannot accidentally create an upstream application
// inside a transaction that might later roll back.
type Journal struct{ pool *pgxpool.Pool }

func NewJournal(pool *pgxpool.Pool) (*Journal, error) {
	if pool == nil {
		return nil, ErrInvalid
	}
	return &Journal{pool: pool}, nil
}

// Prepare must commit before Claim, and Claim must commit before any external
// mutation. An idempotent retry returns the existing state, including ambiguous
// work; it never resets that work to pending.
func (j *Journal) Prepare(ctx context.Context, intent Intent) (sqlc.IdentityProvisioningOperation, error) {
	if !intent.Scope.valid() || intent.Scope.Generation > math.MaxInt64 ||
		len(validation.IsDNS1123Label(intent.CredentialNamespace)) != 0 || !specDigestPattern.MatchString(intent.DesiredSpecDigest) ||
		intent.ActorID == uuid.Nil || intent.CorrelationID == uuid.Nil {
		return sqlc.IdentityProvisioningOperation{}, ErrInvalid
	}
	operation := "register"
	if intent.Scope.Generation > 1 {
		operation = "rotate"
	}
	return j.transact(ctx, func(q *sqlc.Queries) (sqlc.IdentityProvisioningOperation, bool, error) {
		row, err := q.PrepareIdentityProvisioning(ctx, sqlc.PrepareIdentityProvisioningParams{
			ID: uuid.New(), Issuer: intent.Scope.Issuer, ProjectID: intent.Scope.ProjectID, InstallationID: intent.Scope.InstallationID,
			Generation: safeconv.Int64(intent.Scope.Generation), CredentialNamespace: intent.CredentialNamespace, DesiredSpecDigest: intent.DesiredSpecDigest,
			Operation: operation, ActorID: intent.ActorID, CorrelationID: intent.CorrelationID,
		})
		if !errors.Is(err, pgx.ErrNoRows) {
			return row, err == nil, err
		}
		row, err = q.GetIdentityProvisioningScope(ctx, sqlc.GetIdentityProvisioningScopeParams{
			Issuer: intent.Scope.Issuer, ProjectID: intent.Scope.ProjectID, InstallationID: intent.Scope.InstallationID, Generation: safeconv.Int64(intent.Scope.Generation),
		})
		if err != nil {
			return row, false, err
		}
		if row.DesiredSpecDigest != intent.DesiredSpecDigest || row.CredentialNamespace != intent.CredentialNamespace ||
			row.ActorID != intent.ActorID || row.CorrelationID != intent.CorrelationID {
			return row, false, ErrConflict
		}
		return row, false, nil
	})
}

// Claim is a compare-and-swap transition, never a lease. A new worker must not
// reclaim provisioning/rotating work, even after a timeout. It requires recovery.
func (j *Journal) Claim(ctx context.Context, scope Scope, id uuid.UUID, revision int64) (sqlc.IdentityProvisioningOperation, error) {
	return j.change(ctx, scope, id, func(q *sqlc.Queries, _ sqlc.IdentityProvisioningOperation) (sqlc.IdentityProvisioningOperation, error) {
		return q.ClaimIdentityProvisioning(ctx, sqlc.ClaimIdentityProvisioningParams{ID: id, Revision: revision})
	})
}

// RequireRecovery fences a previous worker by advancing the revision. Callers
// take the installation lock before deciding work has been interrupted. A late
// result from that worker cannot complete with its old revision.
func (j *Journal) RequireRecovery(ctx context.Context, scope Scope, id uuid.UUID, revision int64) (sqlc.IdentityProvisioningOperation, error) {
	return j.change(ctx, scope, id, func(q *sqlc.Queries, _ sqlc.IdentityProvisioningOperation) (sqlc.IdentityProvisioningOperation, error) {
		return q.RecoverIdentityProvisioning(ctx, sqlc.RecoverIdentityProvisioningParams{ID: id, Revision: revision})
	})
}

// Complete requires a successful authenticated read from durable storage. The
// worker must verify the upstream result against the desired spec before calling
// this method. No secret value is copied into the journal or its audit event.
func (j *Journal) Complete(ctx context.Context, store *SecretStore, id uuid.UUID, revision int64) (sqlc.IdentityProvisioningOperation, error) {
	if store == nil {
		return sqlc.IdentityProvisioningOperation{}, ErrInvalid
	}
	result, err := store.Get(ctx)
	if err != nil {
		return sqlc.IdentityProvisioningOperation{}, err
	}
	for _, value := range []string{result.OrgID, result.ProjectID, result.AppID, result.ClientID} {
		if len(value) > 1024 {
			return sqlc.IdentityProvisioningOperation{}, ErrInvalid
		}
	}
	return j.change(ctx, store.scope, id, func(q *sqlc.Queries, row sqlc.IdentityProvisioningOperation) (sqlc.IdentityProvisioningOperation, error) {
		if row.CredentialNamespace != store.namespace {
			return row, ErrConflict
		}
		return q.CompleteIdentityProvisioning(ctx, sqlc.CompleteIdentityProvisioningParams{
			ID: id, Revision: revision, UpstreamOrgID: result.OrgID, UpstreamProjectID: result.ProjectID,
			UpstreamAppID: result.AppID, UpstreamClientID: result.ClientID, CredentialSecretName: store.Name(),
		})
	})
}

func (j *Journal) change(ctx context.Context, scope Scope, id uuid.UUID, change func(*sqlc.Queries, sqlc.IdentityProvisioningOperation) (sqlc.IdentityProvisioningOperation, error)) (sqlc.IdentityProvisioningOperation, error) {
	if !scope.valid() || scope.Generation > math.MaxInt64 || id == uuid.Nil {
		return sqlc.IdentityProvisioningOperation{}, ErrInvalid
	}
	return j.transact(ctx, func(q *sqlc.Queries) (sqlc.IdentityProvisioningOperation, bool, error) {
		row, err := q.GetIdentityProvisioningOperation(ctx, id)
		if err != nil {
			return row, false, err
		}
		if row.Issuer != scope.Issuer || row.ProjectID != scope.ProjectID || row.InstallationID != scope.InstallationID || row.Generation != safeconv.Int64(scope.Generation) {
			return row, false, ErrConflict
		}
		row, err = change(q, row)
		return row, err == nil, err
	})
}

func (j *Journal) transact(ctx context.Context, mutate func(*sqlc.Queries) (sqlc.IdentityProvisioningOperation, bool, error)) (sqlc.IdentityProvisioningOperation, error) {
	tx, err := j.pool.Begin(ctx)
	if err != nil {
		return sqlc.IdentityProvisioningOperation{}, ErrUnavailable
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}()
	q := sqlc.New(tx)
	row, changed, err := mutate(q)
	if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, ErrConflict) {
		return sqlc.IdentityProvisioningOperation{}, ErrConflict
	}
	if err != nil {
		return sqlc.IdentityProvisioningOperation{}, ErrUnavailable
	}
	if changed {
		if err := audit.RecordIntent(ctx, q, journalAudit(row)); err != nil {
			return sqlc.IdentityProvisioningOperation{}, ErrUnavailable
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return sqlc.IdentityProvisioningOperation{}, ErrUnavailable
	}
	return row, nil
}

func journalAudit(row sqlc.IdentityProvisioningOperation) audit.Intent {
	action := "identity.provisioning." + row.State
	request := row.CorrelationID.String() + ":" + strconv.FormatInt(row.Revision, 10)
	return audit.Intent{
		DedupeKey: audit.MutationDedupeKey(request, action, "identity_provisioning", row.ID.String()),
		Event: audit.Event{Source: "service", ActionClass: "mutation", Action: action,
			UserID: pgtype.UUID{Bytes: row.ActorID, Valid: true}, ResourceType: "identity_provisioning", ResourceID: row.ID.String(),
			RequestID: request, CorrelationID: row.CorrelationID.String(), Detail: map[string]any{
				"project_id": row.ProjectID.String(), "installation_id": row.InstallationID.String(),
				"generation": row.Generation, "revision": row.Revision, "state": row.State,
			}},
	}
}
