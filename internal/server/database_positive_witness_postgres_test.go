//go:build unix && integration

package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net"
	"net/url"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/db"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/jsoncontract"
	"github.com/alphabravocompany/astronomer-go/internal/scanner"
)

// This is a real whole-owner integration control, not a report/witness fixture.
// ROOT must provide a freshly owned current canonical-schema PG17/18 DB, actual accepted
// signed feed/profile with selected candidate helper image, real agent tunnel,
// sealed Trivy DB pair, OCI workload with a known positive finding and CNI.
// NewApp uses its ordinary keyless constructor; no keyed adapter, injected
// verifier, report callback, witness insert, caller approval or fake Job exists.
// An explicitly mounted owned VirtualSigstore root verifies actual certificates,
// SCT and Rekor bundles with the unchanged literal contract identity. Receipts
// must say LOCAL QA CERTIFICATE VERIFICATION. No GitHub OIDC execution or
// production release approval is inferred.
func TestDatabaseLocalQANewAppImportGenuineJobOrdinaryEqualClockInvalidates(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 55*time.Minute)
	defer cancel()
	cfg, err := config.Load()
	if err != nil || !cfg.DeliveryEnabled || !cfg.DeliveryDatabaseUpdatesEnabled || !cfg.DeliveryDatabaseScanEnabled {
		t.Fatal("actual enabled typed owner configuration required")
	}
	workload, workloadErr := uuid.Parse(os.Getenv("W04_TEST_SCAN_WORKLOAD_UID"))
	expectedCVE := os.Getenv("W04_TEST_POSITIVE_CVE")
	if workloadErr != nil || workload == uuid.Nil || len(expectedCVE) == 0 || len(expectedCVE) > 128 || strings.ContainsAny(expectedCVE, "\r\n\x00") {
		t.Fatal("actual owned cluster/workload/positive finding identities required")
	}
	var runtimeInput localQARuntimeInput
	runtimeBytes := localQAReadPrivateInput(t, os.Getenv("W04_LOCAL_QA_RUNTIME_INPUT"), 64<<10)
	if jsoncontract.Decode(runtimeBytes, &runtimeInput) != nil {
		clear(runtimeBytes)
		t.Fatal("actual owned runtime input invalid")
	}
	clear(runtimeBytes)
	address := positiveWitnessServerAddress(t, runtimeInput.ListenURL)
	stopTLS := localQAStartOwnedTLSFrontend(t, ctx, cfg, runtimeInput, address)
	poolConfig, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil || cfg.DatabaseURL != os.Getenv("W04_TEST_DATABASE_URL") {
		t.Fatal("actual selected owner database differs from leased fixture")
	}
	poolConfig.MaxConns = 2
	application := "plan032_w04_witness_" + uuid.NewString()
	poolConfig.ConnConfig.RuntimeParams["application_name"] = application
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		t.Fatal("owned observation pool unavailable")
	}
	joined := false
	runtimeStarted := false
	t.Cleanup(func() {
		if !runtimeStarted || joined {
			pool.Close()
		} else {
			t.Error("owner closure unqualified; observation pool retained for bounded process drain")
		}
	})
	positiveWitnessDatabaseFence(t, ctx, pool)
	// Actual update-enabled NewApp initializes fresh physical/base authority,
	// then canonical HTTPS login/create/installer enrolls the genuine agent.
	cluster := localQABootstrapAndImport(t, ctx, cfg, pool)
	var initialClaims, initialWitnesses int64
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM public.vulnerability_database_scan_claims WHERE cluster_id=$1),(SELECT count(*) FROM public.image_vulnerability_database_witnesses WHERE cluster_id=$1)`, cluster).Scan(&initialClaims, &initialWitnesses); err != nil || initialClaims != 0 || initialWitnesses != 0 {
		t.Fatal("fresh enrolled lane has preinserted claims/witnesses")
	}
	state, err := sqlc.New(pool).GetVulnerabilityDatabaseClusterStateForUpdate(ctx, cluster)
	if err != nil || state.UpdateID == uuid.Nil || state.UpdateGeneration <= 0 {
		t.Fatal("actual canonical accepted current database state missing")
	}
	server, err := NewApp(ctx, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal("actual NewApp keyless startup failed; native owner qualification unavailable")
	}
	runtimeStarted = true
	startResult := make(chan error, 1)
	startJoined := false
	stopped := false
	shutdown := func() {
		if stopped {
			return
		}
		stopped = true
		stopContext, stop := context.WithTimeout(context.Background(), 20*time.Second)
		defer stop()
		if err := server.Shutdown(stopContext); err != nil {
			t.Error("actual NewApp runtime failed bounded joined shutdown")
			return
		}
		if !startJoined {
			timer := time.NewTimer(7 * time.Second)
			defer timer.Stop()
			select {
			case err := <-startResult:
				startJoined = true
				if err != nil {
					t.Error("actual private server listener ended with an error")
				}
			case <-timer.C:
				t.Error("actual server listener remains unjoined; process closure unqualified")
				return
			}
		}
		joined = true
	}
	t.Cleanup(shutdown)
	go func() { startResult <- server.Start(address) }()
	claim, witness := positiveWitnessAwaitActualCompletion(t, ctx, pool, server, startResult, &startJoined, cluster, workload, expectedCVE)
	localQAObserveCurrentAgent(t, ctx, cluster, cfg.ServerURL)
	localQAObserveCompletedJob(t, ctx, claim, witness)
	// Stop and JOIN the real owner before introducing an ordinary observation.
	// This prevents a later genuine cycle from replacing the prior witness while
	// the equal-clock transaction assertion is being observed.
	shutdown()
	if !joined {
		t.Fatal("positive completion owner did not join before ordinary ingest")
	}
	stopTLS()
	if t.Failed() {
		t.Fatal("actual owner/frontend closure failed; no completion receipt permitted")
	}
	transactionContext, stopTransaction := context.WithTimeout(ctx, 30*time.Second)
	defer stopTransaction()
	queries := sqlc.New(pool)
	before, err := queries.GetImageVulnerabilityObservation(transactionContext, witness.ReportID)
	if err != nil || before.Revision <= 0 || before.Revision != witness.ReportObservationRevision || !before.ReceivedAt.Equal(witness.ReportObservedAt) || before.TimeState != "valid" || !before.UpstreamTime.Valid {
		t.Fatal("actual witness is not joined to its genuine ordinary observation revision")
	}
	stored, err := queries.GetImageVulnerabilityReportByID(transactionContext, witness.ReportID)
	if err != nil || stored.ClusterID != cluster || stored.ImageDigest != claim.WorkloadManifestDigest || stored.ReportName == "" || !before.UpstreamTime.Time.Equal(stored.ScannedAt) {
		t.Fatal("actual positive owned report identities changed")
	}
	findings, err := queries.ListVulnerabilitiesForReport(transactionContext, sqlc.ListVulnerabilitiesForReportParams{ReportID: stored.ID, PageLimit: 100001})
	if err != nil || len(findings) == 0 || len(findings) > 100000 {
		t.Fatal("actual positive findings unavailable or exceed canonical bound")
	}
	original := positiveWitnessOrdinaryReport(t, stored, before, findings)
	hasExpected := false
	for _, finding := range original.Report.Vulnerabilities {
		if finding.VulnerabilityID == expectedCVE && finding.Severity != "UNKNOWN" {
			hasExpected = true
		}
	}
	if !hasExpected {
		t.Fatal("real selected scan did not produce the runner's known positive finding")
	}
	positiveWitnessInstallEqualClock(t, ctx, pool, stored.ID)
	// Same actual report content/UID/upstream time and equal received_at cannot
	// inherit the old Job witness. Only the canonical ordinary Ingester writes.
	if err := scanner.NewIngester(queries, pool, nil, nil).Ingest(transactionContext, cluster, original); err != nil {
		t.Fatal("actual equal-clock ordinary transaction failed")
	}
	after, err := queries.GetImageVulnerabilityObservation(transactionContext, stored.ID)
	if err != nil || after.Revision != before.Revision+1 || !after.ReceivedAt.Equal(before.ReceivedAt) || after.UpstreamReportUid != before.UpstreamReportUid || after.TimeState != before.TimeState || !after.UpstreamTime.Time.Equal(before.UpstreamTime.Time) {
		t.Fatal("equal-clock control did not isolate the monotonic observation revision")
	}
	if _, err := queries.GetCurrentImageVulnerabilityDatabaseWitnessForUpdate(transactionContext, stored.ID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("ordinary equal-clock ingest inherited prior current scan-used authority")
	}
	var remaining int64
	if err := pool.QueryRow(transactionContext, `SELECT count(*) FROM public.image_vulnerability_database_witnesses WHERE report_id=$1`, stored.ID).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatal("ordinary ingest left a hidden historical witness row")
	}
	updated, err := queries.GetImageVulnerabilityReportByID(transactionContext, stored.ID)
	if err != nil || updated.HighCount != stored.HighCount || updated.CriticalCount != stored.CriticalCount || updated.MediumCount != stored.MediumCount || updated.LowCount != stored.LowCount || updated.UnknownCount != stored.UnknownCount || !updated.ScannedAt.Equal(stored.ScannedAt) || updated.ImageDigest != stored.ImageDigest {
		t.Fatal("ordinary witness invalidation hid known positive summary or changed legacy clock/image")
	}
	currentFindings, err := queries.ListVulnerabilitiesForReport(transactionContext, sqlc.ListVulnerabilitiesForReportParams{ReportID: stored.ID, PageLimit: 100001})
	if err != nil || !reflect.DeepEqual(positiveWitnessFindingData(t, findings), positiveWitnessFindingData(t, currentFindings)) {
		t.Fatal("actual positive CVEs changed or disappeared while invalidating witness")
	}
	retained, err := queries.GetVulnerabilityDatabaseScanClaim(transactionContext, claim.ID)
	if err != nil || retained.Phase != "completed" || retained.ScanJobUid != claim.ScanJobUid || retained.ScanPodUid != claim.ScanPodUid || retained.ReportID != claim.ReportID {
		t.Fatal("ordinary ingest rewrote the historical actual completed Job claim")
	}
	var transactions, locks int64
	if err := pool.QueryRow(transactionContext, `SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND application_name=$1 AND pid<>pg_backend_pid() AND (state='idle in transaction' OR wait_event_type='Lock')`, application).Scan(&transactions); err != nil || transactions != 0 {
		t.Fatal("ordinary transaction retained a backend or lock wait")
	}
	if err := pool.QueryRow(transactionContext, `SELECT count(*) FROM pg_locks WHERE locktype='advisory' AND pid IN(SELECT pid FROM pg_stat_activity WHERE datname=current_database() AND application_name=$1)`, application).Scan(&locks); err != nil || locks != 0 {
		t.Fatal("ordinary transaction retained advisory locks")
	}
	localQAWriteActualCompletion(t, claim, witness, before.Revision, after.Revision, len(currentFindings))
}

func positiveWitnessServerAddress(t *testing.T, raw string) string {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "http" || parsed.User != nil || parsed.Hostname() != "127.0.0.1" || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.ForceQuery {
		t.Fatal("exact privately owned loopback server URL required")
	}
	port, err := strconv.Atoi(parsed.Port())
	if err != nil || port < 1024 || port > 65535 || parsed.Host != net.JoinHostPort("127.0.0.1", strconv.Itoa(port)) {
		t.Fatal("exact finite owned loopback server port required")
	}
	return parsed.Host
}
func positiveWitnessDatabaseFence(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	lease, err := uuid.Parse(os.Getenv("W04_TEST_DATABASE_LEASE"))
	major, majorErr := strconv.Atoi(os.Getenv("W04_TEST_POSTGRES_MAJOR"))
	oid, oidErr := strconv.ParseUint(os.Getenv("W04_TEST_DATABASE_OID"), 10, 32)
	expected := os.Getenv("W04_TEST_DATABASE_NAME")
	if err != nil || lease.Version() != 4 || lease.Variant() != uuid.RFC4122 || majorErr != nil || major != 17 && major != 18 || oidErr != nil || oid == 0 || expected != "plan032_w04_"+strings.ReplaceAll(lease.String(), "-", "") {
		t.Fatal("exact unique newly created PostgreSQL lane lease missing")
	}
	var database, mounted string
	var actualMajor int
	var schema int64
	var actualOID uint32
	var dirty bool
	if err := pool.QueryRow(ctx, `SELECT current_database(),current_setting('server_version_num')::int/10000,(SELECT oid FROM pg_database WHERE datname=current_database()),current_setting('astronomer.plan032_w04_lease',true),(SELECT version FROM public.schema_migrations),(SELECT dirty FROM public.schema_migrations)`).Scan(&database, &actualMajor, &actualOID, &mounted, &schema, &dirty); err != nil || database != expected || actualMajor != major || uint64(actualOID) != oid || mounted != lease.String() || dirty || schema != db.ExpectedSchemaVersion {
		t.Fatal("actual PostgreSQL name/OID/major/lease/schema differs")
	}
}
func positiveWitnessAwaitActualCompletion(t *testing.T, ctx context.Context, pool *pgxpool.Pool, server *Server, startResult <-chan error, startJoined *bool, cluster, workload uuid.UUID, cve string) (sqlc.VulnerabilityDatabaseScanClaim, sqlc.ImageVulnerabilityDatabaseWitness) {
	t.Helper()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	queries := sqlc.New(pool)
	for {
		var identifier uuid.UUID
		err := pool.QueryRow(ctx, `SELECT c.id FROM public.vulnerability_database_scan_claims c JOIN public.image_vulnerability_database_witnesses w ON w.claim_id=c.id JOIN public.image_vulnerabilities v ON v.report_id=w.report_id JOIN public.image_vulnerability_observations o ON o.report_id=w.report_id AND o.revision=w.report_observation_revision WHERE c.cluster_id=$1 AND c.workload_pod_uid=$2 AND c.phase='completed' AND v.vulnerability_id=$3 AND v.severity<>'UNKNOWN' ORDER BY c.created_at LIMIT 1`, cluster, workload, cve).Scan(&identifier)
		if err == nil {
			claim, err := queries.GetVulnerabilityDatabaseScanClaim(ctx, identifier)
			if err != nil || !claim.ReportID.Valid {
				t.Fatal("actual completed claim/report vanished")
			}
			witness, err := queries.GetCurrentImageVulnerabilityDatabaseWitnessForUpdate(ctx, uuid.UUID(claim.ReportID.Bytes))
			scannerFinishUpper := witness.ScannerFinishedAt.Truncate(time.Second).Add(time.Second)
			if err != nil || witness.ClaimID != claim.ID || witness.ClusterID != cluster || witness.UpdateID != claim.UpdateID || !claim.ScanJobUid.Valid || !claim.ScanPodUid.Valid || witness.ScanJobUid != uuid.UUID(claim.ScanJobUid.Bytes) || witness.ScanPodUid != uuid.UUID(claim.ScanPodUid.Bytes) || witness.MainDatabaseSize <= 0 || witness.JavaDatabaseSize <= 0 || witness.ScannerImageID == "" || witness.HelperImageID == "" || witness.FinalLogAt.Before(scannerFinishUpper) || witness.InitialLogAt.After(witness.ScannerStartedAt) || witness.WorkloadManifestDigest != claim.WorkloadManifestDigest || witness.ReportObservationRevision <= 0 {
				t.Fatal("actual completed witness lost runtime/final-measurement/current-report binding")
			}
			if server.hub == nil || server.hub.GetAgent(cluster.String()) == nil {
				t.Fatal("actual current agent tunnel missing after owner completion")
			}
			return claim, witness
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatal("actual completion observation failed")
		}
		select {
		case err := <-startResult:
			*startJoined = true
			if err == nil {
				t.Fatal("actual server stopped before measured positive completion")
			}
			t.Fatal("actual server failed before measured positive completion")
		case <-ctx.Done():
			t.Fatal("actual positive Job/scan-used completion did not qualify before finite deadline")
		case <-ticker.C:
		}
	}
}
func positiveWitnessOrdinaryReport(t *testing.T, report sqlc.ImageVulnerabilityReport, observed sqlc.ImageVulnerabilityObservation, findings []sqlc.ImageVulnerability) scanner.TrivyVulnerabilityReport {
	t.Helper()
	return scanner.TrivyVulnerabilityReport{Metadata: scanner.TrivyMetadata{Name: report.ReportName, UID: observed.UpstreamReportUid, Namespace: report.Namespace, Labels: map[string]string{"trivy-operator.container.name": report.ContainerName, "trivy-operator.resource.kind": report.WorkloadKind, "trivy-operator.resource.name": report.WorkloadName, "trivy-operator.resource.namespace": report.Namespace}}, Report: scanner.TrivyReport{Scanner: scanner.TrivyScanner{Name: report.Scanner, Version: report.ScannerVersion}, Artifact: scanner.TrivyArtifact{Repository: report.ImageRepo, Tag: report.ImageTag, Digest: report.ImageDigest}, Registry: scanner.TrivyRegistry{Server: report.ImageRegistry}, Summary: scanner.TrivySummary{CriticalCount: int(report.CriticalCount), HighCount: int(report.HighCount), MediumCount: int(report.MediumCount), LowCount: int(report.LowCount), UnknownCount: int(report.UnknownCount)}, UpdateTimestamp: observed.UpstreamTime.Time.UTC().Format(time.RFC3339Nano), Vulnerabilities: positiveWitnessFindingData(t, findings)}}
}
func positiveWitnessFindingData(t *testing.T, source []sqlc.ImageVulnerability) []scanner.TrivyCVE {
	t.Helper()
	result := make([]scanner.TrivyCVE, 0, len(source))
	for _, row := range source {
		finding := scanner.TrivyCVE{VulnerabilityID: row.VulnerabilityID, Severity: row.Severity, Resource: row.PkgName, InstalledVersion: row.InstalledVersion, FixedVersion: row.FixedVersion, PrimaryLink: row.PrimaryLink, Title: row.Title, Description: row.Description}
		if row.CvssScore.Valid {
			score, err := row.CvssScore.Float64Value()
			if err != nil || !score.Valid || math.IsNaN(score.Float64) || math.IsInf(score.Float64, 0) {
				t.Fatal("actual stored score unavailable")
			}
			finding.Score = &score.Float64
		}
		result = append(result, finding)
	}
	sort.Slice(result, func(i, j int) bool {
		a, b := result[i], result[j]
		for index, left := range []string{a.VulnerabilityID, a.Resource, a.Severity, a.InstalledVersion, a.FixedVersion, a.PrimaryLink, a.Title, a.Description} {
			right := []string{b.VulnerabilityID, b.Resource, b.Severity, b.InstalledVersion, b.FixedVersion, b.PrimaryLink, b.Title, b.Description}[index]
			if left != right {
				return left < right
			}
		}
		if a.Score == nil || b.Score == nil {
			return a.Score == nil && b.Score != nil
		}
		return *a.Score < *b.Score
	})
	return result
}
func positiveWitnessInstallEqualClock(t *testing.T, ctx context.Context, pool *pgxpool.Pool, report uuid.UUID) {
	t.Helper()
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	function := pgx.Identifier{"public", "plan032_w04_positive_clock_" + suffix}.Sanitize()
	trigger := pgx.Identifier{"plan032_w04_positive_clock_" + suffix}.Sanitize()
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := pool.Exec(cleanup, "DROP TRIGGER IF EXISTS "+trigger+" ON public.image_vulnerability_observations"); err != nil {
			t.Error("exact owned equal-clock trigger cleanup failed")
		}
		if _, err := pool.Exec(cleanup, "DROP FUNCTION IF EXISTS "+function+"()"); err != nil {
			t.Error("exact owned equal-clock function cleanup failed")
		}
	})
	body := fmt.Sprintf("CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $clock$ BEGIN IF NEW.report_id='%s'::uuid THEN NEW.received_at := OLD.received_at; END IF; RETURN NEW; END; $clock$", function, report.String())
	if _, err := pool.Exec(ctx, body); err != nil {
		t.Fatal("exact private equal-clock function setup failed")
	}
	if _, err := pool.Exec(ctx, "CREATE TRIGGER "+trigger+" BEFORE UPDATE ON public.image_vulnerability_observations FOR EACH ROW EXECUTE FUNCTION "+function+"()"); err != nil {
		t.Fatal("exact private equal-clock trigger setup failed")
	}
}
