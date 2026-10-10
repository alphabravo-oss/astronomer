package provider

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ownedFinalizationQueries struct {
	fakeQueries
	finalized sqlc.DeliveryAssignmentReceipt
}

func (queries *ownedFinalizationQueries) FinalizeDeliveryAssignmentSnapshot(ctx context.Context, parameters sqlc.FinalizeDeliveryAssignmentSnapshotParams) (sqlc.DeliveryAssignmentReceipt, error) {
	receipt, err := queries.fakeQueries.FinalizeDeliveryAssignmentSnapshot(ctx, parameters)
	if err != nil {
		return receipt, err
	}
	// A server-owned field deliberately differs from the Advance return. The
	// internal result must be the actual finalizer row, never a reconstructed row.
	receipt.CredentialContentDigest = "finalizer-owned-credential-digest"
	queries.finalized = receipt
	return receipt, nil
}

func TestCanonicalFinalizationReturnsActualReceiptIncludingNotModified(t *testing.T) {
	queries := &ownedFinalizationQueries{}
	provider := New(queries, nil)
	request := validRequest()
	first, actual, err := provider.finalizeSnapshot(t.Context(), testCluster, request)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Validate(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actual, queries.finalized) || actual.CredentialContentDigest != "finalizer-owned-credential-digest" || actual.DesiredSnapshotEtag != first.ETag || actual.DesiredSnapshotGeneration != first.SnapshotGeneration {
		t.Fatal("canonical finalizer result was reconstructed or differs from response")
	}
	request.AckedSnapshotGeneration, request.AckedETag = first.SnapshotGeneration, first.ETag
	unchanged, actual, err := provider.finalizeSnapshot(t.Context(), testCluster, request)
	if err != nil {
		t.Fatal(err)
	}
	if !unchanged.NotModified || unchanged.ETag != first.ETag || !reflect.DeepEqual(actual, queries.finalized) {
		t.Fatal("not-modified path discarded actual finalized receipt")
	}
}

type ownedProjectionFixture struct{}

func (*ownedProjectionFixture) prepareOwnedSnapshot(context.Context, *sqlc.Queries, uuid.UUID) (ownedSnapshotRefresh, error) {
	return nil, errors.New("fixture has no database projection authority")
}
func (*ownedProjectionFixture) revalidateOwnedSnapshot(context.Context) error {
	return errors.New("fixture has no physical authority")
}

func TestOwnedSnapshotConstructorCapturesProviderWithoutCallerAuthority(t *testing.T) {
	// This unused pool is a constructor-only fixture. No connection or transaction
	// is created; it cannot demonstrate PostgreSQL lock/rollback qualification.
	pool := &pgxpool.Pool{}
	original := New(&fakeQueries{}, nil)
	original.builtinCA, original.builtinSigningKey = []byte("captured CA"), []byte("captured public key")
	owned, err := newOwnedSnapshotCoordinator(pool, original, &ownedProjectionFixture{})
	if err != nil {
		t.Fatal(err)
	}
	original.builtinCA[0], original.builtinSigningKey[0] = 'x', 'x'
	if string(owned.builtinCA) != "captured CA" || string(owned.builtinSigningKey) != "captured public key" || owned.ownedSnapshots.prototype.ownedSnapshots != nil {
		t.Fatal("constructor retained mutable provider material or recursive coordinator")
	}
	for _, arguments := range []struct {
		pool       *pgxpool.Pool
		provider   *Provider
		projection ownedSnapshotProjection
	}{
		{nil, original, &ownedProjectionFixture{}}, {pool, nil, &ownedProjectionFixture{}}, {pool, New(nil, nil), &ownedProjectionFixture{}}, {pool, original, nil}, {pool, owned, &ownedProjectionFixture{}},
	} {
		if result, err := newOwnedSnapshotCoordinator(arguments.pool, arguments.provider, arguments.projection); err == nil || result != nil {
			t.Fatal("invalid private constructor accepted")
		}
	}
}

func TestOwnedSnapshotFailuresPreserveCancellationWithoutSQLDetails(t *testing.T) {
	secret := "private SQL detail must not cross delivery boundary"
	original := &pgconn.PgError{Code: "40001", Message: secret, Detail: secret}
	for _, code := range []string{"40001", "40P01", "23505"} {
		failure := safeOwnedSnapshotError(t.Context(), "commit", &pgconn.PgError{Code: code, Message: secret})
		if retryOwnedSnapshot(failure) != (code == "40001" || code == "40P01") || strings.Contains(failure.Error(), secret) {
			t.Fatal("retry or sanitized diagnostic changed")
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	failure := safeOwnedSnapshotError(ctx, "session lock", original)
	if !errors.Is(failure, context.Canceled) || retryOwnedSnapshot(failure) || strings.Contains(failure.Error(), secret) {
		t.Fatal("cancellation was retried or disclosed SQL error")
	}
	for _, sentinel := range []error{context.Canceled, context.DeadlineExceeded, ErrSnapshotChanged} {
		failure := safeOwnedSnapshotError(t.Context(), "projection", sentinel)
		if !errors.Is(failure, sentinel) || retryOwnedSnapshot(failure) {
			t.Fatal("canonical sentinel was discarded or retried")
		}
	}
}

// Transaction atomicity, blocked-lock cancellation/drain and current authority
// races require the separately planned real PostgreSQL17/18 integration lane.
