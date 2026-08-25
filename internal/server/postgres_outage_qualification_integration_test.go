package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	deliverydeployment "github.com/alphabravocompany/astronomer-go/internal/delivery/deployment"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
	deliveryrollout "github.com/alphabravocompany/astronomer-go/internal/delivery/rollout"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/systemrollout"
	"github.com/alphabravocompany/astronomer-go/internal/email"
	"github.com/alphabravocompany/astronomer-go/internal/handler"
	deliveryhandler "github.com/alphabravocompany/astronomer-go/internal/handler/delivery"
	appmiddleware "github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

const (
	postgresOutageDatabaseEnv  = "ASTRONOMER_POSTGRES_OUTAGE_DATABASE_URL"
	postgresOutageContainerEnv = "ASTRONOMER_POSTGRES_OUTAGE_CONTAINER"
	postgresOutageGuardEnv     = "ASTRONOMER_POSTGRES_OUTAGE_DEDICATED"
	postgresOutageLabel        = "astronomer.qualification=postgres-outage"
	postgresOutageSecret       = "POSTGRES-OUTAGE-SECRET-SENTINEL"
)

type postgresOutageFamily struct {
	name     string
	typeName string
	run      func(context.Context, func(any) error) error
}

func postgresOutageTypedFamily[T any](database *db.DB, name string) postgresOutageFamily {
	runner := sqlcMutationTxRunner[T](database)
	return postgresOutageFamily{name: name, typeName: reflect.TypeOf((*T)(nil)).Elem().String(), run: func(ctx context.Context, fn func(any) error) error {
		return runner(ctx, func(q T) error { return fn(q) })
	}}
}

// TestPostgresOutageQualification is intentionally opt-in. Its companion
// script owns the labelled PostgreSQL 16 container that this test stops and
// restarts while the production pool and mutation adapters stay alive.
func TestPostgresOutageQualification(t *testing.T) {
	databaseURL := os.Getenv(postgresOutageDatabaseEnv)
	container := os.Getenv(postgresOutageContainerEnv)
	if databaseURL == "" || container == "" {
		t.Skip("run scripts/test-postgres-outage-qualification.sh")
	}
	if os.Getenv(postgresOutageGuardEnv) != "1" || !strings.HasPrefix(container, "astronomer-postgres-outage-") {
		t.Fatal("refusing PostgreSQL failure injection without the dedicated dependency guard")
	}
	assertPostgresOutageContainer(t, container)

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	database, err := db.ConnectWithConfig(ctx, databaseURL, db.PoolConfig{
		MaxConns: 8, MinConns: 1, HealthCheckPeriod: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	queries := sqlc.New(database.Pool())
	var postgresVersion string
	if err := database.Pool().QueryRow(ctx, `SHOW server_version`).Scan(&postgresVersion); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(postgresVersion, "16.") {
		t.Fatalf("PostgreSQL version = %q, want 16.x", postgresVersion)
	}

	families := postgresOutageFamilies(database)
	assertProductionPostgresOutageInventory(t, families)
	for _, family := range families {
		family := family
		t.Run("healthy_transaction/"+family.name, func(t *testing.T) {
			err := family.run(ctx, func(q any) error {
				txQueries, ok := q.(*sqlc.Queries)
				if !ok {
					return fmt.Errorf("%s transaction supplied %T, want *sqlc.Queries", family.name, q)
				}
				key := postgresOutageMarkerKey("healthy", family.name)
				if _, err := txQueries.UpsertPlatformSetting(ctx, sqlc.UpsertPlatformSettingParams{
					Key: key, Value: json.RawMessage(`true`), Description: "PostgreSQL outage qualification",
				}); err != nil {
					return err
				}
				_, err := audit.RecordOutbox(ctx, txQueries, audit.Event{
					Source: "service", Action: "qualification.postgres_outage", ResourceType: "mutation_family",
					ResourceID: family.name, ResourceName: family.name, StatusCode: http.StatusOK,
					Detail: map[string]any{"family": family.name, "password": postgresOutageSecret},
				}, "postgres-outage-healthy-"+family.name, audit.OutboxOptions{})
				return err
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}

	admin, password := seedPostgresOutageAdmin(t, ctx, queries)
	boundaries := newPostgresOutageBoundaries(t, database, queries, admin)
	boundaries.assertHealthy(t, ctx, password)
	residuals := newPostgresOutageResiduals(t, ctx, database, queries, admin)
	residuals.assertHealthy(t, ctx)

	stopped := true
	t.Cleanup(func() {
		if stopped {
			startPostgresOutageContainer(t, container)
		}
	})
	stopPostgresOutageContainer(t, container)

	for _, family := range families {
		family := family
		t.Run("outage_transaction/"+family.name, func(t *testing.T) {
			var callbackCalls atomic.Int32
			attemptCtx, attemptCancel := context.WithTimeout(ctx, 750*time.Millisecond)
			defer attemptCancel()
			err := family.run(attemptCtx, func(q any) error {
				callbackCalls.Add(1)
				txQueries := q.(*sqlc.Queries)
				_, writeErr := txQueries.UpsertPlatformSetting(attemptCtx, sqlc.UpsertPlatformSettingParams{
					Key: postgresOutageMarkerKey("outage", family.name), Value: json.RawMessage(`true`),
				})
				return writeErr
			})
			if err == nil {
				t.Fatal("mutation unexpectedly succeeded while PostgreSQL was unavailable")
			}
			if callbackCalls.Load() != 0 {
				t.Fatalf("mutation callback ran %d times while PostgreSQL was unavailable", callbackCalls.Load())
			}
		})
	}
	boundaries.assertOutage(t, password)
	residuals.assertOutage(t)

	startPostgresOutageContainer(t, container)
	stopped = false
	waitForPostgresOutageRecovery(t, ctx, database)
	boundaries.assertRecovered(t, ctx, len(families))
	residuals.assertRecovered(t, ctx)
}

func postgresOutageFamilies(database *db.DB) []postgresOutageFamily {
	return []postgresOutageFamily{
		postgresOutageTypedFamily[handler.AdminQueueMutationTx](database, "admin_queue"),
		postgresOutageTypedFamily[handler.AdminTaskOutboxMutationTx](database, "admin_task_outbox"),
		postgresOutageTypedFamily[handler.AlertingMutationTx](database, "alerting"),
		postgresOutageTypedFamily[handler.ApiserverAllowlistMutationTx](database, "apiserver_allowlist"),
		postgresOutageTypedFamily[handler.AuthMutationTx](database, "auth"),
		postgresOutageTypedFamily[handler.BackupMutationTx](database, "backup"),
		postgresOutageTypedFamily[handler.CatalogMutationTx](database, "catalog"),
		postgresOutageTypedFamily[handler.ChartRatingMutationTx](database, "chart_rating"),
		postgresOutageTypedFamily[handler.CloudCredentialMutationTx](database, "cloud_credential"),
		postgresOutageTypedFamily[handler.ClusterAgentMutationTx](database, "cluster_agent"),
		postgresOutageTypedFamily[handler.ClusterGroupMutationTx](database, "cluster_group"),
		postgresOutageTypedFamily[handler.ClusterMutationTx](database, "cluster"),
		postgresOutageTypedFamily[handler.ClusterRegistrationMutationTx](database, "cluster_registration"),
		postgresOutageTypedFamily[handler.ClusterRegistryMutationTx](database, "cluster_registry"),
		postgresOutageTypedFamily[handler.ClusterSnapshotMutationTx](database, "cluster_snapshot"),
		postgresOutageTypedFamily[handler.ClusterTemplateMutationTx](database, "cluster_template"),
		postgresOutageTypedFamily[handler.ControlPlaneMutationTx](database, "control_plane"),
		postgresOutageTypedFamily[handler.ControlPlaneSnapshotMutationTx](database, "control_plane_snapshot"),
		postgresOutageTypedFamily[handler.DashboardMutationTx](database, "dashboard"),
		postgresOutageTypedFamily[handler.DexMutationTx](database, "dex"),
		postgresOutageTypedFamily[handler.ExtensionMutationTx](database, "extension"),
		postgresOutageTypedFamily[handler.GatekeeperConstraintMutationTx](database, "gatekeeper_constraint"),
		postgresOutageTypedFamily[handler.GitOpsMutationTx](database, "gitops"),
		postgresOutageTypedFamily[handler.GroupMappingsMutationTx](database, "group_mappings"),
		postgresOutageTypedFamily[handler.ImageVulnMutationTx](database, "image_vulnerability"),
		postgresOutageTypedFamily[handler.LoggingMutationTx](database, "logging"),
		postgresOutageTypedFamily[handler.MaintenanceGateMutationTx](database, "maintenance_gate"),
		postgresOutageTypedFamily[handler.MaintenanceMutationTx](database, "maintenance"),
		postgresOutageTypedFamily[handler.ManagementBackupMutationTx](database, "management_backup"),
		postgresOutageTypedFamily[handler.MonitoringMutationTx](database, "monitoring"),
		postgresOutageTypedFamily[handler.NativeRBACMutationTx](database, "native_rbac"),
		postgresOutageTypedFamily[handler.NetworkPolicyMutationTx](database, "network_policy"),
		postgresOutageTypedFamily[handler.NodeMutationTx](database, "node"),
		postgresOutageTypedFamily[handler.NotificationTemplateMutationTx](database, "notification_template"),
		postgresOutageTypedFamily[handler.PlatformDefaultTemplateMutationTx](database, "platform_default_template"),
		postgresOutageTypedFamily[handler.PlatformSettingsMutationTx](database, "platform_settings"),
		postgresOutageTypedFamily[handler.ProjectCatalogMutationTx](database, "project_catalog"),
		postgresOutageTypedFamily[handler.ProjectNamespaceTx](database, "project_namespace"),
		postgresOutageTypedFamily[handler.QuotaMutationTx](database, "quota"),
		postgresOutageTypedFamily[handler.RBACMutationTx](database, "rbac"),
		postgresOutageTypedFamily[handler.ReadAuditPolicyMutationTx](database, "read_audit_policy"),
		postgresOutageTypedFamily[handler.ResourceMutationTx](database, "resource"),
		postgresOutageTypedFamily[handler.ResourceSettingsMutationTx](database, "resource_settings"),
		postgresOutageTypedFamily[handler.SecurityMutationTx](database, "security"),
		postgresOutageTypedFamily[handler.SIEMMutationTx](database, "siem"),
		postgresOutageTypedFamily[handler.SMTPMutationTx](database, "smtp"),
		postgresOutageTypedFamily[handler.ToolMutationTx](database, "tool"),
		postgresOutageTypedFamily[handler.UserMutationTx](database, "user"),
		postgresOutageTypedFamily[handler.VaultMutationTx](database, "vault"),
		postgresOutageTypedFamily[handler.WebhookMutationTx](database, "webhook"),
		postgresOutageTypedFamily[handler.WorkloadMutationTx](database, "workload"),
		postgresOutageTypedFamily[deliveryhandler.BundleMutationTx](database, "delivery_bundle"),
		postgresOutageTypedFamily[deliveryhandler.SourceMutationTx](database, "delivery_source"),
		postgresOutageTypedFamily[deliveryhandler.TargetMutationTx](database, "delivery_target"),
	}
}

func assertProductionPostgresOutageInventory(t *testing.T, families []postgresOutageFamily) {
	t.Helper()
	wiring := ""
	for _, path := range []string{"server.go", "management_backup_enablement.go"} {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		wiring += "\n" + string(content)
	}
	matches := regexp.MustCompile(`sqlcMutationTxRunner\[([^]]+)\]`).FindAllStringSubmatch(wiring, -1)
	wired := make(map[string]struct{}, len(matches))
	for _, match := range matches {
		name := strings.ReplaceAll(match[1], "deliveryhandler.", "delivery.")
		wired[name] = struct{}{}
	}
	expected := make(map[string]struct{}, len(families))
	for _, family := range families {
		expected[family.typeName] = struct{}{}
	}
	var missing, unexpected []string
	for name := range wired {
		if _, ok := expected[name]; !ok {
			missing = append(missing, name)
		}
	}
	for name := range expected {
		if _, ok := wired[name]; !ok {
			unexpected = append(unexpected, name)
		}
	}
	sort.Strings(missing)
	sort.Strings(unexpected)
	if len(wired) != 54 || len(expected) != 54 || len(missing) > 0 || len(unexpected) > 0 {
		t.Fatalf("production transaction inventory wired=%d qualified=%d missing=%v stale=%v", len(wired), len(expected), missing, unexpected)
	}
}

type postgresOutageBoundaries struct {
	database       *db.DB
	queries        *sqlc.Queries
	admin          sqlc.User
	rbac           *handler.RBACHandler
	auth           *handler.AuthHandler
	smtp           *handler.SMTPHandler
	smtpSends      atomic.Int32
	k8sRemoteCalls atomic.Int32
}

func newPostgresOutageBoundaries(t *testing.T, database *db.DB, queries *sqlc.Queries, admin sqlc.User) *postgresOutageBoundaries {
	t.Helper()
	b := &postgresOutageBoundaries{database: database, queries: queries, admin: admin}
	b.rbac = handler.NewRBACHandler(queries)
	b.rbac.SetRunTx(sqlcMutationTxRunner[handler.RBACMutationTx](database))
	jwt := auth.MustNewJWTManager("postgres-outage-qualification-signing-key", 60)
	b.auth = handler.NewAuthHandler(queries, jwt)
	b.auth.SetAuditWriter(queries)
	b.auth.SetRunTx(sqlcMutationTxRunner[handler.AuthMutationTx](database))
	b.smtp = handler.NewSMTPHandler(postgresOutageSMTPQueries{Queries: queries, admin: admin}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	b.smtp.SetAuditWriter(queries)
	b.smtp.SetRunTx(sqlcMutationTxRunner[handler.SMTPMutationTx](database))
	b.smtp.SetTestSenderFactory(func(email.Settings) handler.SMTPTestSender {
		return postgresOutageSMTPSender{calls: &b.smtpSends}
	})
	return b
}

func (b *postgresOutageBoundaries) assertHealthy(t *testing.T, ctx context.Context, password string) {
	t.Helper()
	healthyRole := "postgres-outage-healthy-role"
	recorder := httptest.NewRecorder()
	b.rbac.CreateGlobalRole(recorder, postgresOutageRequest(http.MethodPost, "/api/v1/rbac/global-roles/", `{"name":"`+healthyRole+`","display_name":"Healthy","permissions":[]}`, b.admin))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("healthy RBAC mutation = %d %s", recorder.Code, recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	b.auth.Login(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/auth/login/", strings.NewReader(`{"email":"outage-admin@example.test","password":"`+password+`"}`)))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"token"`) || strings.Contains(recorder.Body.String(), password) {
		t.Fatalf("healthy credential response = %d %s", recorder.Code, recorder.Body.String())
	}

	if _, err := b.queries.UpsertSMTPSettings(ctx, sqlc.UpsertSMTPSettingsParams{
		ID: email.SingletonSettingsID, Enabled: true, Host: "smtp.invalid", Port: 587,
		FromAddress: "noreply@example.test", FromName: "Qualification", AuthMechanism: "plain",
		Encryption: "starttls", RequireTls: true, TimeoutSeconds: 1,
	}); err != nil {
		t.Fatal(err)
	}
	recorder = httptest.NewRecorder()
	b.smtp.Test(recorder, postgresOutageRequest(http.MethodPost, "/api/v1/admin/smtp/test/", `{"recipient":"operator@example.test"}`, b.admin))
	if recorder.Code != http.StatusOK || b.smtpSends.Load() != 1 || strings.Contains(recorder.Body.String(), postgresOutageSecret) {
		t.Fatalf("healthy SMTP test = %d sends=%d body=%s", recorder.Code, b.smtpSends.Load(), recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	b.k8sRouter().ServeHTTP(recorder, postgresOutageK8sRequest())
	if recorder.Code != http.StatusAccepted || b.k8sRemoteCalls.Load() != 1 {
		t.Fatalf("healthy raw Kubernetes mutation = %d remote_calls=%d", recorder.Code, b.k8sRemoteCalls.Load())
	}
}

func (b *postgresOutageBoundaries) assertOutage(t *testing.T, password string) {
	t.Helper()
	assertFailClosed := func(name string, recorder *httptest.ResponseRecorder, forbidden ...string) {
		t.Helper()
		if recorder.Code < 500 || recorder.Code >= 600 {
			t.Fatalf("%s outage response = %d %s, want fail-closed 5xx", name, recorder.Code, recorder.Body.String())
		}
		for _, value := range forbidden {
			if value != "" && strings.Contains(recorder.Body.String(), value) {
				t.Fatalf("%s outage response leaked %q: %s", name, value, recorder.Body.String())
			}
		}
	}

	recorder := httptest.NewRecorder()
	b.rbac.CreateGlobalRole(recorder, postgresOutageRequest(http.MethodPost, "/api/v1/rbac/global-roles/", `{"name":"postgres-outage-leaked-role","display_name":"Outage","permissions":[]}`, b.admin))
	assertFailClosed("RBAC", recorder, postgresOutageSecret)
	if !strings.Contains(recorder.Body.String(), `"code":"audit_unavailable"`) {
		t.Fatalf("RBAC outage response is not the standard audit error: %s", recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	b.auth.Login(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/auth/login/", strings.NewReader(`{"email":"outage-admin@example.test","password":"`+password+`"}`)))
	assertFailClosed("credential", recorder, password, `"token"`, `"refresh"`)
	if !strings.Contains(recorder.Body.String(), `"code":"audit_unavailable"`) || len(recorder.Result().Cookies()) != 0 {
		t.Fatalf("credential outage response escaped the standard audit boundary: %s", recorder.Body.String())
	}

	smtpBefore := b.smtpSends.Load()
	recorder = httptest.NewRecorder()
	b.smtp.Test(recorder, postgresOutageRequest(http.MethodPost, "/api/v1/admin/smtp/test/", `{"recipient":"`+postgresOutageSecret+`@example.test"}`, b.admin))
	assertFailClosed("SMTP test", recorder, postgresOutageSecret, `"success":true`)
	if b.smtpSends.Load() != smtpBefore {
		t.Fatalf("SMTP remote effect count changed during outage: %d -> %d", smtpBefore, b.smtpSends.Load())
	}

	k8sBefore := b.k8sRemoteCalls.Load()
	recorder = httptest.NewRecorder()
	b.k8sRouter().ServeHTTP(recorder, postgresOutageK8sRequest())
	assertFailClosed("raw Kubernetes", recorder, postgresOutageSecret)
	if !strings.Contains(recorder.Body.String(), "audit_unavailable") || b.k8sRemoteCalls.Load() != k8sBefore {
		t.Fatalf("raw Kubernetes outage crossed pre-effect audit boundary: calls=%d body=%s", b.k8sRemoteCalls.Load(), recorder.Body.String())
	}
}

func (b *postgresOutageBoundaries) assertRecovered(t *testing.T, ctx context.Context, familyCount int) {
	t.Helper()
	var healthyMarkers, outageMarkers, qualificationAudits, leakedSecrets, healthyRoles, leakedRoles int
	err := b.database.Pool().QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM platform_settings WHERE key LIKE 'qualification.pg_outage.healthy.%'),
			(SELECT count(*) FROM platform_settings WHERE key LIKE 'qualification.pg_outage.outage.%'),
			(SELECT count(*) FROM audit_outbox WHERE action='qualification.postgres_outage'),
			(SELECT count(*) FROM audit_outbox WHERE detail::text LIKE '%' || $1 || '%'),
			(SELECT count(*) FROM global_roles WHERE name='postgres-outage-healthy-role'),
			(SELECT count(*) FROM global_roles WHERE name='postgres-outage-leaked-role')`, postgresOutageSecret).
		Scan(&healthyMarkers, &outageMarkers, &qualificationAudits, &leakedSecrets, &healthyRoles, &leakedRoles)
	if err != nil {
		t.Fatal(err)
	}
	if healthyMarkers != familyCount || outageMarkers != 0 || qualificationAudits != familyCount || leakedSecrets != 0 || healthyRoles != 1 || leakedRoles != 0 {
		t.Fatalf("recovered state markers=%d/%d audits=%d secret_leaks=%d roles=%d/%d, want %d/0/%d/0/1/0",
			healthyMarkers, outageMarkers, qualificationAudits, leakedSecrets, healthyRoles, leakedRoles, familyCount, familyCount)
	}
	for action, want := range map[string]int{
		"auth.login": 1, "admin.smtp.test": 1, "cluster.k8s_proxy.intent": 1, "cluster.k8s_proxy.outcome": 1,
	} {
		var count int
		if err := b.database.Pool().QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE action=$1`, action).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != want {
			t.Fatalf("audit_log action %s rows = %d, want %d", action, count, want)
		}
	}
	if b.smtpSends.Load() != 1 || b.k8sRemoteCalls.Load() != 1 {
		t.Fatalf("remote effects after recovery SMTP=%d Kubernetes=%d, want 1/1", b.smtpSends.Load(), b.k8sRemoteCalls.Load())
	}
}

func (b *postgresOutageBoundaries) k8sRouter() http.Handler {
	router := chi.NewRouter()
	router.With(auditK8sProxyMutations(b.queries)).HandleFunc("/clusters/{cluster_id}/k8s/*", func(w http.ResponseWriter, _ *http.Request) {
		b.k8sRemoteCalls.Add(1)
		w.WriteHeader(http.StatusAccepted)
	})
	return router
}

type postgresOutageSMTPQueries struct {
	*sqlc.Queries
	admin sqlc.User
}

// postgresOutageResiduals covers the production transaction owners that do
// not use sqlcMutationTxRunner. Their healthy mutations commit state and audit
// together; while PostgreSQL is stopped they cannot reach any durable or
// downstream effect because BeginTx is the first effectful operation.
type postgresOutageResiduals struct {
	database        *db.DB
	queries         *sqlc.Queries
	admin           sqlc.User
	compliance      *handler.ComplianceBaselinesHandler
	planner         *deliveryrollout.Planner
	rolloutControl  *deliveryrollout.PostgresController
	deployment      *deliverydeployment.PostgresController
	systemRollout   *systemrollout.Service
	projectID       uuid.UUID
	targetID        uuid.UUID
	approvalTarget  uuid.UUID
	deploymentID    uuid.UUID
	draftReleaseID  uuid.UUID
	strategy        model.RolloutStrategy
	preview         model.Digest
	approvalPreview model.Digest
	rolloutID       uuid.UUID
	approvalID      uuid.UUID
	approvalDigest  model.Digest
	applicationID   uuid.UUID
}

type postgresOutageComplianceReader struct {
	*sqlc.Queries
	admin sqlc.User
}

func (q postgresOutageComplianceReader) GetUserByID(context.Context, uuid.UUID) (sqlc.User, error) {
	return q.admin, nil
}

func newPostgresOutageResiduals(t *testing.T, ctx context.Context, database *db.DB, queries *sqlc.Queries, admin sqlc.User) *postgresOutageResiduals {
	t.Helper()
	residuals := &postgresOutageResiduals{database: database, queries: queries, admin: admin}
	residuals.seedDeliveryGraph(t, ctx)

	reader := postgresOutageComplianceReader{Queries: queries, admin: admin}
	residuals.compliance = handler.NewComplianceBaselinesHandler(reader,
		func(txCtx context.Context, fn func(handler.ComplianceBaselineMutationTx) error) error {
			return sqlcMutationTxRunner[handler.ComplianceBaselineMutationTx](database)(txCtx, fn)
		}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	store, err := deliveryrollout.NewPostgresPlanningStore(database.Pool())
	if err != nil {
		t.Fatal(err)
	}
	residuals.planner, err = deliveryrollout.NewPlanner(store, time.Now, uuid.New)
	if err != nil {
		t.Fatal(err)
	}
	residuals.planner.RequireTransactionalAudit()
	residuals.rolloutControl, err = deliveryrollout.NewPostgresController(database.Pool(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	residuals.rolloutControl.RequireTransactionalAudit()
	residuals.deployment, err = deliverydeployment.NewPostgresController(database.Pool(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	residuals.deployment.RequireTransactionalAudit()
	residuals.systemRollout, err = systemrollout.New(database.Pool())
	if err != nil {
		t.Fatal(err)
	}
	residuals.systemRollout.RequireTransactionalAudit()

	_, preview, err := store.Preview(ctx, residuals.targetID)
	if err != nil {
		t.Fatal(err)
	}
	residuals.preview = preview.PreviewDigest
	_, preview, err = store.Preview(ctx, residuals.approvalTarget)
	if err != nil {
		t.Fatal(err)
	}
	residuals.approvalPreview = preview.PreviewDigest
	return residuals
}

func (r *postgresOutageResiduals) seedDeliveryGraph(t *testing.T, ctx context.Context) {
	t.Helper()
	clusterID, projectID, sourceID := uuid.New(), uuid.New(), uuid.New()
	r.projectID = projectID
	bundleID, versionID := uuid.New(), uuid.New()
	r.targetID, r.approvalTarget, r.deploymentID = uuid.New(), uuid.New(), uuid.New()
	currentReleaseID := uuid.New()
	r.draftReleaseID = uuid.New()
	digest := "sha256:" + strings.Repeat("a", 64)
	sourceSpec, err := json.Marshal(model.ResolvedSourceSpec{
		SourceID: sourceID, Type: model.SourceGit, URL: "https://git.example.test/qualification.git", AuthMode: model.AuthNone,
		Trust: model.TrustPolicy{AllowUnsigned: true}, Revision: model.ImmutableRevision{
			Kind: model.RevisionGitCommit, Value: strings.Repeat("b", 40), ArtifactDigest: model.Digest(digest),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	statements := []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO clusters (id,name,display_name,status,is_local,registration_phase) VALUES ($1,$2,$2,'active',false,'ready')`, []any{clusterID, "postgres-outage-residual-cluster"}},
		{`INSERT INTO projects (id,name,display_name,cluster_id) VALUES ($1,$2,$2,$3)`, []any{projectID, "postgres-outage-residual-project", clusterID}},
		{`INSERT INTO cluster_agent_tokens (cluster_id,token,token_hash) VALUES ($1,$2,$3)`, []any{clusterID, "qualification-token-" + uuid.NewString(), "qualification-hash-" + uuid.NewString()}},
		{`INSERT INTO agent_connections (cluster_id,agent_id,session_id,status) VALUES ($1,$2,$3,'connected')`, []any{clusterID, "qualification-agent", uuid.NewString()}},
		{`INSERT INTO delivery_controller_inventory (cluster_id,flux_version,components,ready,compatibility_status) VALUES ($1,'2.4.0','{}',true,'compatible')`, []any{clusterID}},
		{`INSERT INTO delivery_sources (id,project_id,name,source_type,url,status) VALUES ($1,$2,$3,'git',$4,'ready')`, []any{sourceID, projectID, "qualification-source", "https://git.example.test/qualification.git"}},
		{`INSERT INTO component_bundles (id,project_id,name) VALUES ($1,$2,$3)`, []any{bundleID, projectID, "qualification-bundle"}},
		{`INSERT INTO component_bundle_versions (id,bundle_id,source_id,version,renderer,requested_revision,resolved_revision,artifact_digest,source_spec,requirements,spec_digest,verification_status,state) VALUES ($1,$2,$3,'v1','kustomize',$4,$4,$5,$6,'[]',$5,'verified','ready')`, []any{versionID, bundleID, sourceID, strings.Repeat("b", 40), digest, sourceSpec}},
		{`INSERT INTO delivery_targets (id,project_id,name,bundle_version_id,placement,rollout_policy) VALUES ($1,$2,$3,$4,'{"all_clusters":true}','{"approval_required":false}')`, []any{r.targetID, projectID, "qualification-target", versionID}},
		{`INSERT INTO delivery_targets (id,project_id,name,bundle_version_id,placement,rollout_policy) VALUES ($1,$2,$3,$4,'{"all_clusters":true}','{"approval_required":true}')`, []any{r.approvalTarget, projectID, "qualification-approval-target", versionID}},
		{`INSERT INTO cluster_deployments (id,target_id,cluster_id,desired_bundle_version_id,desired_generation,desired_spec_digest,desired_revision,action,phase) VALUES ($1,$2,$3,$4,1,$5,$6,'apply','ready')`, []any{r.deploymentID, r.targetID, clusterID, versionID, digest, strings.Repeat("b", 40)}},
		{`INSERT INTO delivery_system_releases (id,version,artifact_url,artifact_digest,distribution_digest,agent_version,agent_image,minimum_kubernetes,maximum_kubernetes,crd_storage_version,verification_policy,spec_digest,state,released_at) VALUES ($1,$2,$3,$4,$4,'1.0.0','example.invalid/agent:1','1.28','1.34','v1','{}',$4,'released',now())`, []any{currentReleaseID, "qualification-current-" + uuid.NewString(), "https://example.invalid/current.tgz", "sha256:" + strings.Repeat("c", 64)}},
		{`INSERT INTO delivery_system_releases (id,version,artifact_url,artifact_digest,distribution_digest,agent_version,agent_image,minimum_kubernetes,maximum_kubernetes,crd_storage_version,verification_policy,spec_digest,state) VALUES ($1,$2,$3,$4,$4,'1.1.0','example.invalid/agent:2','1.28','1.34','v1','{}',$4,'draft')`, []any{r.draftReleaseID, "qualification-draft-" + uuid.NewString(), "https://example.invalid/draft.tgz", "sha256:" + strings.Repeat("d", 64)}},
	}
	for _, statement := range statements {
		if _, err := r.database.Pool().Exec(ctx, statement.sql, statement.args...); err != nil {
			t.Fatalf("seed residual PostgreSQL fixture: %v", err)
		}
	}
	r.strategy = model.RolloutStrategy{
		Type: model.StrategyRolling, MaxConcurrent: 1,
		MaxUnavailable:   model.Amount{Type: model.AmountCount, Value: 1},
		ProgressDeadline: model.Duration(10 * time.Minute),
		FailureThreshold: model.Amount{Type: model.AmountCount, Value: 1}, OnFailure: model.FailurePause,
	}
}

func postgresOutageIntent(action, resourceType, phase string) audit.Intent {
	return audit.Intent{
		Event: audit.Event{Source: "service", Action: action, ResourceType: resourceType, StatusCode: http.StatusOK,
			Detail: map[string]any{"phase": phase, "password": postgresOutageSecret}},
		DedupeKey: audit.MutationDedupeKey("postgres-outage-"+phase, action, resourceType, phase),
	}
}

func (r *postgresOutageResiduals) rolloutRequest(target uuid.UUID, preview model.Digest, key, phase string) deliveryrollout.CreateRequest {
	return deliveryrollout.CreateRequest{
		TargetID: target, ExpectedTargetGeneration: 1, PreviewDigest: preview, ConfirmAllClusters: true,
		Strategy: r.strategy, Actor: r.admin.ID.String(), IdempotencyKey: key,
		Audit: postgresOutageIntent("qualification.delivery.rollout.plan", "delivery_rollout", phase),
	}
}

func (r *postgresOutageResiduals) assertHealthy(t *testing.T, ctx context.Context) {
	t.Helper()
	const baselineID = "0dddab87-044e-41d5-8a10-c313480753fb"
	recorder := httptest.NewRecorder()
	r.compliance.Apply(recorder, postgresOutageChiRequest(http.MethodPost, "/api/v1/admin/compliance-baselines/"+baselineID+"/apply/", baselineID, `{"notes":"qualification"}`, r.admin, ctx))
	if recorder.Code != http.StatusOK {
		t.Fatalf("healthy compliance apply = %d %s", recorder.Code, recorder.Body.String())
	}
	var applyResponse struct {
		Data struct {
			ApplicationID uuid.UUID `json:"application_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &applyResponse); err != nil || applyResponse.Data.ApplicationID == uuid.Nil {
		t.Fatalf("decode compliance application: %v body=%s", err, recorder.Body.String())
	}
	r.applicationID = applyResponse.Data.ApplicationID
	recorder = httptest.NewRecorder()
	r.compliance.Revert(recorder, postgresOutageChiRequest(http.MethodPost, "/api/v1/admin/compliance-baseline-applications/"+r.applicationID.String()+"/revert/", r.applicationID.String(), "", r.admin, ctx))
	if recorder.Code != http.StatusOK {
		t.Fatalf("healthy compliance revert = %d %s", recorder.Code, recorder.Body.String())
	}

	plan, err := r.planner.Create(ctx, r.rolloutRequest(r.targetID, r.preview, "qualification-plan", "healthy-planner"))
	if err != nil {
		t.Fatal(err)
	}
	r.rolloutID = plan.ID
	action, err := r.rolloutControl.Act(ctx, deliveryrollout.ActionRequest{
		ProjectID: plan.ProjectID, RolloutID: plan.ID, ExpectedFence: 1, Action: deliveryrollout.ActionPause,
		ActorID: pgtype.UUID{Bytes: r.admin.ID, Valid: true}, Audit: postgresOutageIntent("qualification.delivery.rollout.action", "delivery_rollout", "healthy-control"),
	})
	if err != nil || !action.AuditPersisted || action.Rollout.State != string(model.RolloutPaused) {
		t.Fatalf("healthy rollout action result=%+v err=%v", action, err)
	}

	approvalPlan, err := r.planner.Create(ctx, r.rolloutRequest(r.approvalTarget, r.approvalPreview, "qualification-approval-plan", "healthy-approval-plan"))
	if err != nil {
		t.Fatal(err)
	}
	r.approvalID, r.approvalDigest = approvalPlan.ID, approvalPlan.Approval.Digest
	if _, err := r.database.Pool().Exec(ctx, `UPDATE delivery_rollouts SET state='awaiting_approval' WHERE id=$1`, r.approvalID); err != nil {
		t.Fatal(err)
	}
	approval, err := r.rolloutControl.Approve(ctx, deliveryrollout.ApprovalRequest{
		ProjectID: approvalPlan.ProjectID, RolloutID: approvalPlan.ID, ExpectedFence: 1, Cohort: -1,
		BindingDigest: approvalPlan.Approval.Digest, Decision: "approved", ActorID: pgtype.UUID{Bytes: r.admin.ID, Valid: true},
		ExpiresAt: time.Now().Add(time.Hour), Audit: postgresOutageIntent("qualification.delivery.rollout.approval", "delivery_rollout", "healthy-approval"),
	})
	if err != nil || !approval.AuditPersisted || approval.Approval == nil || approval.Rollout.State != string(model.RolloutQueued) {
		t.Fatalf("healthy rollout approval result=%+v err=%v", approval, err)
	}

	deployment, err := r.deployment.Act(ctx, deliverydeployment.Request{
		ProjectID: plan.ProjectID, DeploymentID: r.deploymentID, ExpectedGeneration: 1,
		Action: deliverydeployment.ActionSuspend, Audit: postgresOutageIntent("qualification.delivery.deployment.action", "cluster_deployment", "healthy-deployment"),
	})
	if err != nil || !deployment.AuditPersisted || deployment.Deployment.DesiredGeneration != 2 {
		t.Fatalf("healthy deployment action result=%+v err=%v", deployment, err)
	}

	view, err := r.systemRollout.Start(ctx, systemrollout.StartRequest{
		ReleaseID: r.draftReleaseID, Strategy: r.strategy, IdempotencyKey: "qualification-system-rollout", ActorID: r.admin.ID,
		Audit: postgresOutageIntent("qualification.delivery.system_rollout.start", "delivery_system_rollout", "healthy-system"),
	})
	if err != nil || view.ID == uuid.Nil || view.TotalClusters != 1 {
		t.Fatalf("healthy system rollout result=%+v err=%v", view, err)
	}
}

func (r *postgresOutageResiduals) assertOutage(t *testing.T) {
	t.Helper()
	attempt := func(name string, call func(context.Context) error) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 750*time.Millisecond)
		defer cancel()
		if err := call(ctx); err == nil {
			t.Fatalf("%s unexpectedly succeeded while PostgreSQL was unavailable", name)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 750*time.Millisecond)
	recorder := httptest.NewRecorder()
	r.compliance.Apply(recorder, postgresOutageChiRequest(http.MethodPost, "/api/v1/admin/compliance-baselines/80bbc817-eea5-47fb-a6b6-60d93f630067/apply/", "80bbc817-eea5-47fb-a6b6-60d93f630067", `{"notes":"`+postgresOutageSecret+`"}`, r.admin, ctx))
	cancel()
	if recorder.Code != http.StatusServiceUnavailable || !strings.Contains(recorder.Body.String(), `"code":"audit_unavailable"`) || strings.Contains(recorder.Body.String(), postgresOutageSecret) {
		t.Fatalf("compliance apply outage response = %d %s", recorder.Code, recorder.Body.String())
	}
	ctx, cancel = context.WithTimeout(context.Background(), 750*time.Millisecond)
	recorder = httptest.NewRecorder()
	r.compliance.Revert(recorder, postgresOutageChiRequest(http.MethodPost, "/api/v1/admin/compliance-baseline-applications/"+r.applicationID.String()+"/revert/", r.applicationID.String(), "", r.admin, ctx))
	cancel()
	if recorder.Code != http.StatusServiceUnavailable || !strings.Contains(recorder.Body.String(), `"code":"audit_unavailable"`) {
		t.Fatalf("compliance revert outage response = %d %s", recorder.Code, recorder.Body.String())
	}

	attempt("rollout planner", func(ctx context.Context) error {
		_, err := r.planner.Create(ctx, r.rolloutRequest(r.targetID, r.preview, "qualification-outage-plan", "outage-planner"))
		return err
	})
	attempt("rollout control", func(ctx context.Context) error {
		_, err := r.rolloutControl.Act(ctx, deliveryrollout.ActionRequest{ProjectID: r.projectID, RolloutID: r.rolloutID, ExpectedFence: 2, Action: deliveryrollout.ActionResume, Audit: postgresOutageIntent("qualification.delivery.rollout.action", "delivery_rollout", "outage-control")})
		return err
	})
	attempt("rollout approval", func(ctx context.Context) error {
		_, err := r.rolloutControl.Approve(ctx, deliveryrollout.ApprovalRequest{ProjectID: r.projectID, RolloutID: r.approvalID, ExpectedFence: 2, Cohort: -1, BindingDigest: r.approvalDigest, Decision: "approved", ExpiresAt: time.Now().Add(time.Hour), Audit: postgresOutageIntent("qualification.delivery.rollout.approval", "delivery_rollout", "outage-approval")})
		return err
	})
	attempt("deployment control", func(ctx context.Context) error {
		_, err := r.deployment.Act(ctx, deliverydeployment.Request{ProjectID: r.projectID, DeploymentID: r.deploymentID, ExpectedGeneration: 2, Action: deliverydeployment.ActionResume, Audit: postgresOutageIntent("qualification.delivery.deployment.action", "cluster_deployment", "outage-deployment")})
		return err
	})
	attempt("system rollout", func(ctx context.Context) error {
		_, err := r.systemRollout.Start(ctx, systemrollout.StartRequest{ReleaseID: r.draftReleaseID, Strategy: r.strategy, IdempotencyKey: "qualification-outage-system", Audit: postgresOutageIntent("qualification.delivery.system_rollout.start", "delivery_system_rollout", "outage-system")})
		return err
	})
}

func (r *postgresOutageResiduals) assertRecovered(t *testing.T, ctx context.Context) {
	t.Helper()
	var applications, reverted, rollouts, approvals, deploymentEvents, systemRollouts, systemAssignments, outageAudits, secretLeaks int
	err := r.database.Pool().QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM compliance_baseline_applications),
			(SELECT count(*) FROM compliance_baseline_applications WHERE status='reverted'),
			(SELECT count(*) FROM delivery_rollouts WHERE id IN ($1,$2)),
			(SELECT count(*) FROM delivery_rollout_approvals WHERE rollout_id=$2),
			(SELECT count(*) FROM cluster_deployment_events WHERE deployment_id=$3),
			(SELECT count(*) FROM delivery_system_rollouts WHERE idempotency_key='qualification-system-rollout'),
			(SELECT count(*) FROM delivery_system_cluster_assignments WHERE rollout_id IS NOT NULL),
			(SELECT count(*) FROM audit_outbox WHERE detail->>'phase' LIKE 'outage-%'),
			(SELECT count(*) FROM audit_outbox WHERE detail::text LIKE '%' || $4 || '%')`,
		r.rolloutID, r.approvalID, r.deploymentID, postgresOutageSecret).Scan(
		&applications, &reverted, &rollouts, &approvals, &deploymentEvents, &systemRollouts, &systemAssignments, &outageAudits, &secretLeaks)
	if err != nil {
		t.Fatal(err)
	}
	if applications != 1 || reverted != 1 || rollouts != 2 || approvals != 1 || deploymentEvents != 1 || systemRollouts != 1 || systemAssignments != 1 || outageAudits != 0 || secretLeaks != 0 {
		t.Fatalf("residual recovery apps=%d/%d rollouts=%d approvals=%d deployment_events=%d system=%d/%d outage_audits=%d secret_leaks=%d",
			applications, reverted, rollouts, approvals, deploymentEvents, systemRollouts, systemAssignments, outageAudits, secretLeaks)
	}
	for action, want := range map[string]int{
		"compliance.baseline.applied": 1, "compliance.baseline.reverted": 1,
		"qualification.delivery.rollout.plan": 2, "qualification.delivery.rollout.action": 1,
		"qualification.delivery.rollout.approval": 1, "qualification.delivery.deployment.action": 1,
		"qualification.delivery.system_rollout.start": 1,
	} {
		var count int
		if err := r.database.Pool().QueryRow(ctx, `SELECT count(*) FROM audit_outbox WHERE action=$1`, action).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != want {
			t.Fatalf("residual audit action %s rows=%d, want %d", action, count, want)
		}
	}
}

func postgresOutageChiRequest(method, path, id, body string, admin sqlc.User, base context.Context) *http.Request {
	request := postgresOutageRequest(method, path, body, admin)
	ctx := request.Context()
	if base != nil {
		ctx = base
		ctx = appmiddleware.SetAuthenticatedUserForTest(ctx, &appmiddleware.AuthenticatedUser{
			ID: admin.ID.String(), Email: admin.Email, Username: admin.Username, AuthMethod: "jwt",
		})
	}
	route := chi.NewRouteContext()
	route.URLParams.Add("id", id)
	return request.WithContext(context.WithValue(ctx, chi.RouteCtxKey, route))
}

func (q postgresOutageSMTPQueries) GetUserByID(context.Context, uuid.UUID) (sqlc.User, error) {
	return q.admin, nil
}

type postgresOutageSMTPSender struct{ calls *atomic.Int32 }

func (s postgresOutageSMTPSender) Send(context.Context, email.Message) error {
	s.calls.Add(1)
	return nil
}

func seedPostgresOutageAdmin(t *testing.T, ctx context.Context, queries *sqlc.Queries) (sqlc.User, string) {
	t.Helper()
	password := "Qualification-password-" + uuid.NewString()
	hash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := queries.CreateUser(ctx, sqlc.CreateUserParams{
		Email: "outage-admin@example.test", Username: "outage-admin", Password: hash,
		IsActive: true, IsStaff: true, IsSuperuser: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return admin, password
}

func postgresOutageRequest(method, path, body string, admin sqlc.User) *http.Request {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Secret", postgresOutageSecret)
	ctx := appmiddleware.SetAuthenticatedUserForTest(request.Context(), &appmiddleware.AuthenticatedUser{
		ID: admin.ID.String(), Email: admin.Email, Username: admin.Username, AuthMethod: "jwt",
	})
	return request.WithContext(ctx)
}

func postgresOutageK8sRequest() *http.Request {
	request := httptest.NewRequest(http.MethodPatch,
		"/clusters/qualification-cluster/k8s/api/v1/namespaces/default/secrets/database?token="+postgresOutageSecret,
		bytes.NewBufferString(`{"stringData":{"password":"`+postgresOutageSecret+`"}}`))
	request.Header.Set("Authorization", "Bearer "+postgresOutageSecret)
	request.Header.Set("X-Secret", postgresOutageSecret)
	return request
}

func postgresOutageMarkerKey(phase, family string) string {
	return "qualification.pg_outage." + phase + "." + strings.ReplaceAll(family, "_", "-")
}

func assertPostgresOutageContainer(t *testing.T, container string) {
	t.Helper()
	command := exec.Command("docker", "inspect", "--format", `{{index .Config.Labels "astronomer.qualification"}}`, container)
	output, err := command.Output()
	if err != nil || strings.TrimSpace(string(output)) != "postgres-outage" {
		t.Fatalf("dedicated PostgreSQL outage container guard failed")
	}
}

func stopPostgresOutageContainer(t *testing.T, container string) {
	t.Helper()
	if output, err := exec.Command("docker", "stop", "--time", "1", container).CombinedOutput(); err != nil {
		t.Fatalf("stop dedicated PostgreSQL container: %v: %s", err, output)
	}
}

func startPostgresOutageContainer(t *testing.T, container string) {
	t.Helper()
	if output, err := exec.Command("docker", "start", container).CombinedOutput(); err != nil {
		t.Fatalf("start dedicated PostgreSQL container: %v: %s", err, output)
	}
}

func waitForPostgresOutageRecovery(t *testing.T, ctx context.Context, database *db.DB) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		pingCtx, cancel := context.WithTimeout(ctx, time.Second)
		err := database.Health(pingCtx)
		cancel()
		if err == nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("PostgreSQL did not recover within 20 seconds")
}
