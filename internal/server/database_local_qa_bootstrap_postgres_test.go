//go:build unix && integration

package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/db"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/resolver"
	"github.com/alphabravocompany/astronomer-go/internal/jsoncontract"
	"github.com/alphabravocompany/astronomer-go/pkg/version"
)

const localQARuntimeLabel = "LOCAL QA CERTIFICATE VERIFICATION"

type localQARuntimeInput struct {
	Scope                 string `json:"scope"`
	SourceCommit          string `json:"source_commit"`
	KitDirectory          string `json:"kit_directory"`
	MovedKitDirectory     string `json:"moved_kit_directory"`
	FeedDigest            string `json:"feed_digest"`
	PushFile              string `json:"push_file"`
	PushRealm             string `json:"push_realm"`
	PushService           string `json:"push_service"`
	ListenURL             string `json:"listen_url"`
	ServerCAFile          string `json:"server_ca_file"`
	ServerCertificateFile string `json:"server_certificate_file"`
	ServerKeyFile         string `json:"server_key_file"`
	ServerPinnedAddress   string `json:"server_pinned_address"`
	ClusterName           string `json:"cluster_name"`
	InstallerManifest     string `json:"installer_manifest"`
	EnrollmentReceipt     string `json:"enrollment_receipt"`
}
type localQARuntimePush struct {
	SchemaVersion int    `json:"schema"`
	Username      string `json:"username"`
	Password      string `json:"password"`
}

// Fresh canonical enrollment occurs while actual update-enabled NewApp owns
// the base. ROOT applies the emitted installer to its independently owned real
// cluster. The actual cmd/agent must connect and materialize ordinary provider
// state before import. No fixture cluster/token/assignment/feed/witness INSERT.
func localQABootstrapAndImport(t *testing.T, ctx context.Context, cfg *config.Config, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	var input localQARuntimeInput
	encoded := localQAReadPrivateInput(t, os.Getenv("W04_LOCAL_QA_RUNTIME_INPUT"), 64<<10)
	if jsoncontract.Decode(encoded, &input) != nil || input.Scope != localQARuntimeLabel || input.SourceCommit != localQACandidateRuntimeCompiledSource(t) || input.FeedDigest == "" {
		t.Fatal("local QA actual candidate runtime input differs")
	}
	clear(encoded)
	for _, path := range []string{input.KitDirectory, input.MovedKitDirectory, input.PushFile} {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path {
			t.Fatal("local QA owned transfer path invalid")
		}
	}
	if input.KitDirectory == input.MovedKitDirectory || filepath.Dir(input.KitDirectory) != filepath.Dir(input.MovedKitDirectory) {
		t.Fatal("local QA move requires distinct same-owned-parent directories")
	}
	if _, err := os.Lstat(input.MovedKitDirectory); !os.IsNotExist(err) {
		t.Fatal("local QA moved kit must be absent")
	}
	var authorityRows, updates, attempts int64
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM public.vulnerability_database_authority),(SELECT count(*) FROM public.vulnerability_database_updates),(SELECT count(*) FROM public.vulnerability_database_update_attempts)`).Scan(&authorityRows, &updates, &attempts); err != nil || authorityRows != 0 || updates != 0 || attempts != 0 {
		t.Fatal("local QA must initialize fresh authority only through real NewApp")
	}
	boot := *cfg
	boot.DeliveryDatabaseScanEnabled = false
	boot.DeliveryDatabaseJobPullCredentialRef = ""
	bootCtx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	app, err := NewApp(bootCtx, &boot, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal("local QA actual unscanned NewApp/base bootstrap failed")
	}
	result := make(chan error, 1)
	joined, stopped := false, false
	closeOwner := func() {
		if stopped {
			return
		}
		stopped = true
		bounded, stop := context.WithTimeout(context.Background(), 20*time.Second)
		defer stop()
		if app.Shutdown(bounded) != nil {
			t.Error("local QA initial NewApp runtime did not join")
			return
		}
		select {
		case err := <-result:
			joined = true
			if err != nil {
				t.Error("local QA initial listener failed")
			}
		case <-time.After(7 * time.Second):
			t.Error("local QA initial listener unjoined; ROOT must fail and drain process")
		}
	}
	t.Cleanup(closeOwner)
	go func() { result <- app.Start(positiveWitnessServerAddress(t, input.ListenURL)) }()
	cluster := localQAEnrollCanonicalAgent(t, bootCtx, cfg, pool, input)
	localQAAwaitAgentInput(t, bootCtx)
	_, actualAgent := localQAKubernetes(t)
	if actualAgent.AgentImage != cfg.AgentImageRepository {
		t.Fatal("actual enrolled agent input differs from canonical selected release mapping")
	}
	localQAObserveCurrentAgent(t, bootCtx, cluster, cfg.ServerURL)
	localQAConfigureNativeFlux(t, bootCtx, cfg)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		// Presence is retrieval DATA only. Canonical import subsequently validates
		// the complete source/compiler/renderer/spec/generation and locked receipt.
		var materialized bool
		err = pool.QueryRow(bootCtx, `SELECT EXISTS(SELECT 1 FROM public.cluster_deployments d JOIN public.delivery_targets t ON t.id=d.target_id JOIN public.component_bundle_versions v ON v.id=d.desired_bundle_version_id JOIN public.component_bundles b ON b.id=v.bundle_id JOIN public.projects p ON p.id=b.project_id WHERE d.cluster_id=$1 AND p.managed_by='system' AND v.renderer_spec->'helm'->>'chart'='trivy-operator' AND d.desired_generation>0 AND t.deletion_state='active')`, cluster).Scan(&materialized)
		if err != nil {
			t.Fatal("local QA actual materialization observation failed")
		}
		if app.hub != nil && app.hub.GetAgent(cluster.String()) != nil && materialized {
			break
		}
		select {
		case <-bootCtx.Done():
			t.Fatal("local QA genuine agent/provider did not materialize scanner before deadline")
		case <-ticker.C:
		}
	}
	closeOwner()
	if !joined {
		t.Fatal("local QA initial owner must join before protected import")
	}
	// Exported bytes are physically moved, not reconstructed or re-signed. No
	// application code can convert this into a claim of local OIDC publication.
	before, err := os.Lstat(input.KitDirectory)
	if err != nil || !before.IsDir() || before.Mode().Perm() != 0700 {
		t.Fatal("local QA genuine exported kit unavailable")
	}
	if err = os.Rename(input.KitDirectory, input.MovedKitDirectory); err != nil {
		t.Fatal("local QA owned disconnected move failed")
	}
	after, err := os.Lstat(input.MovedKitDirectory)
	if err != nil || !os.SameFile(before, after) {
		t.Fatal("local QA moved kit identity changed")
	}
	data := localQAReadPrivateInput(t, input.PushFile, 32<<10)
	var private localQARuntimePush
	if jsoncontract.Decode(data, &private) != nil || private.SchemaVersion != 1 || private.Username == "" || private.Password == "" || len(private.Username) > 4096 || len(private.Password) > 16384 || strings.ContainsAny(private.Username, "\r\n\x00:") || strings.ContainsAny(private.Password, "\r\n\x00") {
		clear(data)
		t.Fatal("local QA separate private push material invalid")
	}
	clear(data)
	authentication := resolver.DatabaseRegistryAuthentication{RealmURL: input.PushRealm, Service: input.PushService, Username: private.Username, Password: private.Password}
	defer func() {
		authentication.Username = ""
		authentication.Password = ""
		private.Username = ""
		private.Password = ""
	}()
	// Actual product command: full signed capture/copy/readback FIRST, accurate
	// imported local reservation + canonical locked acceptance SECOND, current
	// discovery locator LAST. No caller attempt ID, approval flag, signer exists.
	imported, err := ImportDatabaseUpdate(ctx, cfg, DatabaseImportCommandOptions{KitDirectory: input.MovedKitDirectory, Authentication: authentication})
	if err != nil || !imported.Copied || !imported.Accepted || !imported.DiscoveryUpdated || imported.FeedDigest != input.FeedDigest || imported.Generation != 1 {
		t.Fatal("local QA actual complete imported product transaction failed")
	}
	var importedRows, publishedRows int64
	if err = pool.QueryRow(ctx, `SELECT count(*) FILTER(WHERE phase='imported'),count(*) FILTER(WHERE phase='published') FROM public.vulnerability_database_update_attempts WHERE published_feed_digest=$1`, input.FeedDigest).Scan(&importedRows, &publishedRows); err != nil || importedRows != 1 || publishedRows != 0 {
		t.Fatal("local QA import relabeled local certificate bytes as production publication")
	}
	// Cancellation never grants a second attempt. Use the real command before
	// continuing into actual enabled NewApp/Job/witness completion in the caller.
	canceled, stop := context.WithCancel(ctx)
	stop()
	if _, err = ImportDatabaseUpdate(canceled, cfg, DatabaseImportCommandOptions{KitDirectory: input.MovedKitDirectory, Authentication: authentication}); !errors.Is(err, context.Canceled) {
		t.Fatal("local QA canceled import did not preserve cancellation")
	}
	return cluster
}

func localQAReadPrivateInput(t *testing.T, path string, maximum int64) []byte {
	t.Helper()
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		t.Fatal("local QA private input path invalid")
	}
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() || before.Mode().Perm() != 0600 || uint32(before.Mode())&uint32(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 || before.Size() <= 0 || before.Size() > maximum {
		t.Fatal("local QA private input kind/mode/size invalid")
	}
	descriptor, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		t.Fatal("local QA private input open failed")
	}
	file := os.NewFile(uintptr(descriptor), path)
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(before, opened) {
		t.Fatal("local QA private input replaced before open")
	}
	stat, ok := opened.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) || stat.Nlink != 1 {
		t.Fatal("local QA private input ownership invalid")
	}
	data, err := io.ReadAll(io.LimitReader(file, maximum+1))
	after, e1 := file.Stat()
	current, e2 := os.Lstat(path)
	if err != nil || int64(len(data)) != before.Size() || e1 != nil || e2 != nil || !os.SameFile(before, after) || !os.SameFile(before, current) || after.Size() != before.Size() || current.Size() != before.Size() || after.Mode() != before.Mode() || current.Mode() != before.Mode() || !after.ModTime().Equal(before.ModTime()) || !current.ModTime().Equal(before.ModTime()) {
		clear(data)
		t.Fatal("local QA private input changed during finite read")
	}
	for _, info := range []os.FileInfo{after, current} {
		value, ok := info.Sys().(*syscall.Stat_t)
		if !ok || value.Uid != uint32(os.Geteuid()) || value.Nlink != 1 {
			clear(data)
			t.Fatal("local QA private input owner/link changed")
		}
	}
	return data
}

// This receipt is emitted only after genuine completion, joined owner shutdown,
// and canonical ordinary-ingest equal-clock invalidation all passed. It contains
// comparison identities/counts only, never native report content or credentials.
func localQAWriteActualCompletion(t *testing.T, claim sqlc.VulnerabilityDatabaseScanClaim, witness sqlc.ImageVulnerabilityDatabaseWitness, before, after int64, findings int) {
	t.Helper()
	if t.Failed() {
		t.Fatal("failed LOCAL QA parent cannot emit successful completion receipt")
	}
	path := os.Getenv("W04_LOCAL_QA_COMPLETION_RECEIPT")
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		t.Fatal("local QA completion receipt path invalid")
	}
	parent, err := os.Lstat(filepath.Dir(path))
	if err != nil || !parent.IsDir() || parent.Mode().Perm() != 0700 {
		t.Fatal("local QA completion receipt parent not owned private directory")
	}
	state, ok := parent.Sys().(*syscall.Stat_t)
	if !ok || state.Uid != uint32(os.Geteuid()) {
		t.Fatal("local QA completion parent owner differs")
	}
	receipt := struct {
		Scope                         string    `json:"scope"`
		SourceCommit                  string    `json:"source_commit"`
		SchemaVersion                 int64     `json:"schema_version"`
		ClusterID                     uuid.UUID `json:"cluster_id"`
		ClaimID                       uuid.UUID `json:"claim_id"`
		JobUID                        uuid.UUID `json:"job_uid"`
		PodUID                        uuid.UUID `json:"pod_uid"`
		ReportID                      uuid.UUID `json:"report_id"`
		BeforeRevision                int64     `json:"before_revision"`
		AfterRevision                 int64     `json:"after_revision"`
		RetainedFindings              int       `json:"retained_findings"`
		NewAppQualified               bool      `json:"new_app_qualified"`
		NativeScannerCompleted        bool      `json:"native_scanner_completed"`
		OrdinaryEqualClockInvalidated bool      `json:"ordinary_equal_clock_invalidated"`
		JavaApplicability             string    `json:"java_applicability"`
		CleanEligibility              bool      `json:"clean_eligibility"`
		CNINoEgressQualified          bool      `json:"cni_no_egress_qualified"`
		ProductionOIDCExecuted        bool      `json:"production_oidc_executed"`
		ProductionReleaseApproved     bool      `json:"production_release_approved"`
	}{Scope: localQARuntimeLabel, SourceCommit: version.GitCommit, SchemaVersion: db.ExpectedSchemaVersion, ClusterID: claim.ClusterID, ClaimID: claim.ID, JobUID: witness.ScanJobUid, PodUID: witness.ScanPodUid, ReportID: witness.ReportID, BeforeRevision: before, AfterRevision: after, RetainedFindings: findings, NewAppQualified: true, NativeScannerCompleted: true, OrdinaryEqualClockInvalidated: true, JavaApplicability: witness.JavaApplicability}
	contents, err := json.Marshal(receipt)
	if err != nil || len(contents) > 16<<10 {
		t.Fatal("local QA completion receipt encoding failed")
	}
	descriptor, err := syscall.Open(path, syscall.O_WRONLY|syscall.O_CREAT|syscall.O_EXCL|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		t.Fatal("local QA completion receipt must be new exclusive file")
	}
	file := os.NewFile(uintptr(descriptor), path)
	written, writeErr := file.Write(contents)
	syncErr := file.Sync()
	closeErr := file.Close()
	if writeErr != nil || written != len(contents) || syncErr != nil || closeErr != nil {
		t.Fatal("local QA completion receipt write failed")
	}
	current, err := os.Lstat(filepath.Dir(path))
	if err != nil || !os.SameFile(parent, current) {
		t.Fatal("local QA completion receipt parent changed")
	}
}

func localQACandidateRuntimeCompiledSource(t *testing.T) string {
	t.Helper()
	source := version.GitCommit
	if len(source) != 40 {
		t.Fatal("actual compiled owner source identity missing")
	}
	for _, value := range source {
		if !(value >= '0' && value <= '9' || value >= 'a' && value <= 'f') {
			t.Fatal("actual compiled owner source identity invalid")
		}
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		revisionSeen, modifiedSeen := false, false
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision":
				if revisionSeen || setting.Value != source {
					t.Fatal("actual native VCS and owner source identity differ")
				}
				revisionSeen = true
			case "vcs.modified":
				if modifiedSeen || setting.Value != "false" {
					t.Fatal("actual native VCS owner source is modified or ambiguous")
				}
				modifiedSeen = true
			}
		}
		if revisionSeen && !modifiedSeen {
			t.Fatal("actual native VCS owner cleanliness unavailable")
		}
	}
	return source
}
