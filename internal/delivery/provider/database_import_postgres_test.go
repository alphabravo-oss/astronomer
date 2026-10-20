//go:build unix && integration

package provider

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	builtinbundles "github.com/alphabravocompany/astronomer-go/deploy/bundles"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/builtin"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/distribution"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/resolver"
	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
	"github.com/alphabravocompany/astronomer-go/internal/jsoncontract"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// Genuine local QA artifacts and the existing unique DB lease are mandatory.
// The runner must create a fresh coherent079 DB and materialize this actual
// signed profile's scanner assignment through the ordinary keyed QA provisioner.
// No skipped pseudo-qualification, hand-written successful feed, witness row,
// caller approval, publication disposition or production OIDC claim exists here.
type databaseImportPostgresCatalog struct {
	authority *distribution.DatabaseAuthority
	proof     *distribution.VerifiedDatabaseAuthorityBase
}

func (loader databaseImportPostgresCatalog) Load(ctx context.Context) (builtinbundles.Catalog, error) {
	return loader.authority.Catalog(ctx, loader.proof)
}

func openDatabaseImportPostgresFixture(t *testing.T) (*databaseRestartPostgresFixture, *DatabaseImportOwner, *distribution.VerifiedOfflineDatabaseUpdate, *distribution.DatabaseRefreshPayloads, uuid.UUID) {
	t.Helper()
	fixture := openDatabaseRestartPostgresFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	t.Cleanup(cancel)
	if _, err := fixture.baseOwner.Initialize(ctx); err != nil {
		t.Fatal("actual signed base initialization failed")
	}
	data := readRestartFixturePublicFile(t, os.Getenv("W04_TEST_SIGNED_BASE_FIXTURE"), 128<<10)
	var document databaseRestartFixtureDocument
	if jsoncontract.Decode(data, &document) != nil {
		t.Fatal("closed actual QA fixture invalid")
	}
	network, err := httpclient.ParseDestinationPolicy(document.DestinationPolicy)
	if err != nil {
		t.Fatal("actual fixture destination policy unavailable")
	}
	proof, err := fixture.authority.Base(ctx)
	if err != nil {
		t.Fatal("actual fixture physical base unavailable")
	}
	ca, err := fixture.authority.RegistryCertificateAuthorities(ctx, proof)
	if err != nil {
		t.Fatal("actual captured registry roots unavailable")
	}
	defer clear(ca)
	key, err := resolver.CaptureMountedPublicKey(filepath.Dir(fixture.vendorPath), strings.TrimSuffix(filepath.Base(fixture.vendorPath), ".pub"))
	if err != nil {
		t.Fatal("actual QA public-key adapter unavailable")
	}
	t.Cleanup(key.Close)
	loader := databaseImportPostgresCatalog{authority: fixture.authority, proof: proof}
	authorize := func(ctx context.Context, cluster, project, source uuid.UUID, assignment protocol.DeliveryAssignmentV2) (bool, error) {
		return builtin.AuthorizeCatalogSource(ctx, loader, network, cluster, project, source, assignment)
	}
	original, err := newWithBuiltinPublicKey(sqlc.New(fixture.pool), nil, key, ca, authorize)
	if err != nil {
		t.Fatal("canonical keyed QA provider unavailable")
	}
	repository := os.Getenv("W04_TEST_IMPORT_REPOSITORY")
	owner, err := NewDatabaseImportOwner(ctx, fixture.pool, original, fixture.authority, repository)
	if err != nil {
		t.Fatal("actual enabled signed profile/private import owner unavailable")
	}
	captured, err := owner.CaptureKit(ctx, os.Getenv("W04_TEST_IMPORT_KIT"), time.Now().UTC())
	if err != nil {
		t.Fatal("actual original signed kit failed canonical full authentication")
	}
	t.Cleanup(captured.Close)
	cache := t.TempDir()
	if err := os.Chmod(cache, 0700); err != nil {
		t.Fatal(err)
	}
	authentication := resolver.DatabaseRegistryAuthentication{RealmURL: os.Getenv("W04_TEST_IMPORT_REALM"), Service: os.Getenv("W04_TEST_IMPORT_SERVICE"), Username: os.Getenv("W04_TEST_IMPORT_USERNAME"), Password: os.Getenv("W04_TEST_IMPORT_PASSWORD")}
	readback, err := owner.CopyKit(ctx, captured, authentication, cache)
	if err != nil {
		t.Fatal("actual unchanged registry copy/canonical complete readback failed")
	}
	t.Cleanup(readback.Close)
	cluster, err := uuid.Parse(os.Getenv("W04_TEST_IMPORT_MATERIALIZED_CLUSTER"))
	if err != nil || cluster == uuid.Nil {
		t.Fatal("runner-owned real canonical scanner target missing")
	}
	return fixture, owner, captured, readback, cluster
}

func TestDatabaseOfflineImportPostgresAccurateDispositionAcceptanceAndIdempotentRescan(t *testing.T) {
	fixture, owner, captured, readback, cluster := openDatabaseImportPostgresFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	if err := owner.AcceptCopied(ctx, captured, readback); err != nil {
		t.Fatal("actual import/canonical assignment acceptance failed")
	}
	feed, document, _, _, valid := captured.Snapshot()
	if !valid {
		t.Fatal("actual import capture closed")
	}
	queries := sqlc.New(fixture.pool)
	state, err := queries.GetVulnerabilityDatabaseClusterStateForUpdate(ctx, cluster)
	if err != nil || state.UpdateGeneration != document.Generation {
		t.Fatal("real materialized cluster was skipped or wrong accepted generation")
	}
	update, err := queries.GetVulnerabilityDatabaseUpdateByFeedForUpdate(ctx, feed)
	if err != nil || update.AttemptPhase != "imported" || update.ID != state.UpdateID {
		t.Fatal("import was relabeled as local publication")
	}
	attempt, err := queries.GetVulnerabilityDatabaseUpdateAttemptForUpdate(ctx, update.AttemptID)
	if err != nil || attempt.Phase != "imported" || !attempt.PublishedFeedDigest.Valid || attempt.PublishedFeedDigest.String != feed {
		t.Fatal("actual FK-linked imported disposition differs")
	}
	var intents int64
	if err := fixture.pool.QueryRow(ctx, "SELECT count(*) FROM public.vulnerability_database_rescan_intents WHERE cluster_id=$1 AND update_id=$2 AND reason='database-updated'", cluster, update.ID).Scan(&intents); err != nil || intents != 1 {
		t.Fatal("actual acceptance did not create one rescan obligation")
	}
	if err := owner.AcceptCopied(ctx, captured, readback); err != nil {
		t.Fatal("exact authenticated import replay was not idempotent")
	}
	var repeated int64
	if err := fixture.pool.QueryRow(ctx, "SELECT count(*) FROM public.vulnerability_database_rescan_intents WHERE cluster_id=$1 AND update_id=$2 AND reason='database-updated'", cluster, update.ID).Scan(&repeated); err != nil || repeated != intents {
		t.Fatal("same import fabricated another scan cycle")
	}
	fixture.assertBackendReleased(t)
}

func TestDatabaseOfflineImportPostgresNewReservationAndPermanentRevocationRejectReplay(t *testing.T) {
	fixture, owner, captured, readback, _ := openDatabaseImportPostgresFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	attempt, _, err := owner.recordImported(ctx, captured, readback)
	if err != nil || attempt.Phase != "imported" {
		t.Fatal("actual original copy/readback import decision missing")
	}
	transactionOwner := &DatabasePublicationOwner{pool: fixture.pool, projection: owner.projection}
	if err := transactionOwner.transaction(ctx, func(ctx context.Context, queries *sqlc.Queries) error {
		base, err := transactionOwner.currentBase(ctx, queries)
		if err != nil {
			return err
		}
		_, err = queries.ReserveVulnerabilityDatabaseGeneration(ctx, sqlc.ReserveVulnerabilityDatabaseGenerationParams{BaseID: base.ID, BaseEpoch: base.Epoch})
		return err
	}); err != nil {
		t.Fatal("actual newer reserved highwater failed")
	}
	if _, _, err := owner.recordImported(ctx, captured, readback); err == nil {
		t.Fatal("old imported feed bypassed newer local reservation")
	}
	if _, err := fixture.baseOwner.Revoke(ctx); err != nil {
		t.Fatal("actual owner permanent revoke failed")
	}
	if _, _, err := owner.recordImported(ctx, captured, readback); err == nil {
		t.Fatal("kit resurrected permanently revoked authority")
	}
	actual, err := sqlc.New(fixture.pool).GetVulnerabilityDatabaseUpdateAttemptForUpdate(ctx, attempt.ID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("actual import history unavailable")
	}
	if err == nil && actual.Phase != "imported" {
		t.Fatal("rejected replay relabeled history")
	}
	fixture.assertBackendReleased(t)
}
