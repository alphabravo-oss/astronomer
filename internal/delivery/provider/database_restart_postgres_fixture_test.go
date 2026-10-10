//go:build unix && integration

package provider

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/distribution"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/resolver"
	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
	"github.com/alphabravocompany/astronomer-go/internal/jsoncontract"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/sys/unix"
)

// Only a separately produced QA fixture document is accepted. These pins are
// retrieval/configuration DATA. Canonical signatures and actual Base still
// determine authority; a fixture boolean/snapshot cannot approve initialization.
type databaseRestartFixtureDocument struct {
	Repository            string                                    `json:"repository"`
	DistributionDigest    string                                    `json:"distribution_digest"`
	CatalogDigest         string                                    `json:"catalog_digest"`
	Release               string                                    `json:"release"`
	CandidateSourceCommit string                                    `json:"candidate_source_commit"`
	Scanner               distribution.DatabaseScannerCompatibility `json:"scanner"`
	TrustDirectory        string                                    `json:"trust_directory"`
	VendorKeyRef          string                                    `json:"vendor_key_ref"`
	RegistryRootsRef      string                                    `json:"registry_roots_ref"`
	DestinationPolicy     string                                    `json:"destination_policy"`
	MirrorPayloadPath     string                                    `json:"mirror_payload_path"`
	MirrorSignaturePath   string                                    `json:"mirror_signature_path"`
	OperatorKeyRef        string                                    `json:"operator_key_ref"`
	ReleaseManifestDigest string                                    `json:"release_manifest_digest"`
}

type databaseRestartPostgresFixture struct {
	pool            *pgxpool.Pool
	authority       *distribution.DatabaseAuthority
	baseOwner       *DatabaseBaseOwner
	revoker         *DatabaseRestartRevoker
	applicationName string
	vendorPath      string
	vendorBytes     []byte
}

func openDatabaseRestartPostgresFixture(t *testing.T) *databaseRestartPostgresFixture {
	t.Helper()
	dsn := os.Getenv("W04_TEST_DATABASE_URL")
	major, err := strconv.Atoi(os.Getenv("W04_TEST_POSTGRES_MAJOR"))
	if dsn == "" || err != nil || major != 17 && major != 18 {
		t.Fatal("explicit owned selected PostgreSQL fixture configuration missing")
	}
	documentBytes := readRestartFixturePublicFile(t, os.Getenv("W04_TEST_SIGNED_BASE_FIXTURE"), 128<<10)
	var document databaseRestartFixtureDocument
	if jsoncontract.Decode(documentBytes, &document) != nil {
		t.Fatal("closed signed-base fixture configuration invalid")
	}
	policy := model.TrustPolicy{Provider: model.SignatureCosignKey, KeyRef: document.VendorKeyRef}
	operatorPolicy := model.TrustPolicy{Provider: model.SignatureCosignKey, KeyRef: document.OperatorKeyRef}
	if policy.Validate() != nil || operatorPolicy.Validate() != nil || !filepath.IsAbs(document.TrustDirectory) {
		t.Fatal("fixture public trust references invalid")
	}
	trust := t.TempDir()
	for _, name := range []string{document.VendorKeyRef + ".pub", document.OperatorKeyRef + ".pub", document.RegistryRootsRef + ".pem"} {
		if filepath.Base(name) != name {
			t.Fatal("public fixture member path invalid")
		}
		bytes := readRestartFixturePublicFile(t, filepath.Join(document.TrustDirectory, name), 256<<10)
		if err := os.WriteFile(filepath.Join(trust, name), bytes, 0600); err != nil {
			t.Fatal(err)
		}
	}
	payload := readRestartFixturePublicFile(t, document.MirrorPayloadPath, 1<<20)
	detached := readRestartFixturePublicFile(t, document.MirrorSignaturePath, 256<<10)
	mirror, err := distribution.VerifyMirrorPlan(t.Context(), payload, detached, document.OperatorKeyRef, trust, document.Release, document.DistributionDigest, document.CatalogDigest, document.ReleaseManifestDigest)
	if err != nil {
		t.Fatal("actual fixture operator mirror endorsement failed")
	}
	mirrorPath := filepath.Join(trust, "mirror-plan.json")
	if err := os.WriteFile(mirrorPath, payload, 0600); err != nil {
		t.Fatal(err)
	}
	network, err := httpclient.ParseDestinationPolicy(document.DestinationPolicy)
	if err != nil || !network.Restricted {
		t.Fatal("owned QA registry policy invalid")
	}
	binding := distribution.DatabaseUpdateBinding{BaseRelease: document.Release, BaseDistributionDigest: document.DistributionDigest, MirrorEndorsementDigest: mirror.Identity(), CatalogDigest: document.CatalogDigest, Profile: "builtins", CandidateSourceCommit: document.CandidateSourceCommit, Scanner: document.Scanner}
	options := distribution.Options{Repository: document.Repository, Digest: document.DistributionDigest, CatalogDigest: document.CatalogDigest, Release: document.Release, Profile: "builtins", UpdateBinding: &binding, Policy: policy, TrustDirectory: trust, Network: network, MirrorPlan: mirror}
	registry := resolver.DatabaseRegistryTrustOptions{TrustDirectory: trust, RootsRef: document.RegistryRootsRef, Network: network.Clone()}
	authority, err := distribution.NewPinnedDatabaseAuthority(t.Context(), options, registry)
	if err != nil {
		t.Fatal("actual fixture physical constructor failed")
	}
	t.Cleanup(authority.Close)
	if _, err := authority.Base(t.Context()); err != nil {
		t.Fatal("genuine signed QA fixture graph failed canonical Base")
	}
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal("owned fixture DSN invalid")
	}
	config.MaxConns = 4
	applicationName := "plan032_w04_" + uuid.NewString()
	config.ConnConfig.RuntimeParams["application_name"] = applicationName
	pool, err := pgxpool.NewWithConfig(t.Context(), config)
	if err != nil {
		t.Fatal("owned fixture pool unavailable")
	}
	t.Cleanup(pool.Close)
	verifyOwnedDatabaseRestartPool(t, pool, major)
	// This runner-created DB is private to these tests. Reset only the unpublished
	// authority subtree, never a live user database or another executor fixture.
	if _, err := pool.Exec(t.Context(), "TRUNCATE public.vulnerability_database_bases CASCADE"); err != nil {
		t.Fatal("private authority fixture reset failed")
	}
	baseOwner, err := NewDatabaseBaseOwner(t.Context(), pool, authority)
	if err != nil {
		t.Fatal("actual authenticated base owner failed")
	}
	options.MirrorPlan = nil
	revoker, err := NewDatabaseRestartRevoker(pool, distribution.DatabaseRestartInspectionOptions{Distribution: options, Registry: &registry, Mirror: &distribution.DatabaseRestartMirrorReference{PayloadPath: mirrorPath, TrustDirectory: trust, KeyRef: document.OperatorKeyRef}})
	if err != nil {
		t.Fatal("actual protected restart constructor failed")
	}
	vendorPath := filepath.Join(trust, document.VendorKeyRef+".pub")
	fixture := &databaseRestartPostgresFixture{pool: pool, authority: authority, baseOwner: baseOwner, revoker: revoker, applicationName: applicationName, vendorPath: vendorPath, vendorBytes: readRestartFixturePublicFile(t, vendorPath, 256<<10)}
	t.Cleanup(func() {
		if err := os.WriteFile(vendorPath, fixture.vendorBytes, 0600); err != nil {
			t.Error("owned public key fixture restoration failed")
		}
	})
	return fixture
}

func readRestartFixturePublicFile(t *testing.T, path string, limit int64) []byte {
	t.Helper()
	if !filepath.IsAbs(path) || limit <= 0 {
		t.Fatal("absolute public fixture path missing")
	}
	descriptor, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal("public fixture file unavailable")
	}
	file := os.NewFile(uintptr(descriptor), path)
	if file == nil {
		_ = unix.Close(descriptor)
		t.Fatal("public fixture descriptor unavailable")
	}
	defer func() { _ = file.Close() }()
	before, err := file.Stat()
	if err != nil || !before.Mode().IsRegular() || before.Size() <= 0 || before.Size() > limit {
		t.Fatal("bounded regular public fixture required")
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	after, statErr := file.Stat()
	if err != nil || statErr != nil || !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) || int64(len(data)) != before.Size() {
		t.Fatal("public fixture changed during bounded read")
	}
	return data
}

func TestDatabaseRestartPostgresCanceledAndChangedRetainedDataCannotWrite(t *testing.T) {
	fixture := openDatabaseRestartPostgresFixture(t)
	if _, err := fixture.baseOwner.Initialize(t.Context()); err != nil {
		t.Fatal("actual authenticated QA base initialization failed")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := fixture.revoker.RevokeLostCurrent(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled restart lost canonical identity")
	}
	fixture.assertState(t, false, 0)
	// A changed comparison row is not signing loss. ObserveLoss must never run
	// against altered retained data, even when the mounted public key is absent.
	original, err := sqlc.New(fixture.pool).GetCurrentVulnerabilityDatabaseBaseForUpdate(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.pool.Exec(t.Context(), "UPDATE public.vulnerability_database_bases SET candidate_source_commit=$1 WHERE id=$2", "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz", original.ID); err == nil {
		t.Fatal("invalid retained source escaped actual schema constraint")
	}
	changed := original.CandidateSourceCommit
	if changed[0] == 'a' {
		changed = "b" + changed[1:]
	} else {
		changed = "a" + changed[1:]
	}
	if _, err := fixture.pool.Exec(t.Context(), "UPDATE public.vulnerability_database_bases SET candidate_source_commit=$1 WHERE id=$2", changed, original.ID); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(fixture.vendorPath); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.revoker.RevokeLostCurrent(t.Context()); err == nil {
		t.Fatal("unknown retained comparison mutation authorized revocation")
	}
	fixture.assertState(t, false, 0)
	fixture.assertBackendReleased(t)
}

func TestDatabaseRestartPostgresConcurrentLossRemainsSinglePermanentBase(t *testing.T) {
	fixture := openDatabaseRestartPostgresFixture(t)
	expected, err := fixture.baseOwner.Initialize(t.Context())
	if err != nil {
		t.Fatal("actual authenticated QA base initialization failed")
	}
	if err := os.Remove(fixture.vendorPath); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	type result struct {
		receipt DatabaseBaseReceipt
		err     error
	}
	done := make(chan result, 2)
	for range 2 {
		go func() { receipt, err := fixture.revoker.RevokeLostCurrent(ctx); done <- result{receipt, err} }()
	}
	joined := 0
	for joined < 2 {
		select {
		case actual := <-done:
			joined++
			if actual.err != nil || actual.receipt != expected {
				t.Error("concurrent exact loss did not converge through actual Serializable retry")
			}
		case <-ctx.Done():
			cancel()
			drain := time.NewTimer(7 * time.Second)
			defer drain.Stop()
			for joined < 2 {
				select {
				case <-done:
					joined++
				case <-drain.C:
					t.Fatal("concurrent restart workers remain unjoined after cancellation")
				}
			}
			t.Fatal("concurrent restart exceeded bounded deadline; joined cleanup but qualification failed")
		}
	}
	fixture.assertState(t, true, 0)
	fixture.assertBackendReleased(t)
}
