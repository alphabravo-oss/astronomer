package charlie

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type operationalDBFake struct{ version int64 }

func (f operationalDBFake) Health(context.Context) error { return nil }
func (f operationalDBFake) SchemaVersion(context.Context) (int64, bool, error) {
	return f.version, false, nil
}
func (f operationalDBFake) Pool() *pgxpool.Pool { return nil }

type operationalQueriesFake struct {
	settings     map[string]json.RawMessage
	backups      []sqlc.Backup
	drill        sqlc.BackupDrillResult
	alerts       []sqlc.AlertEvent
	audits       []sqlc.AuditLog
	repositories []sqlc.HelmRepository
}

func (f *operationalQueriesFake) GetPlatformConfig(context.Context) (sqlc.PlatformConfiguration, error) {
	return sqlc.PlatformConfiguration{InstanceID: uuid.MustParse("11111111-1111-4111-8111-111111111111"), PlatformName: "Astronomer"}, nil
}
func (f *operationalQueriesFake) GetPlatformSetting(_ context.Context, key string) (sqlc.PlatformSetting, error) {
	if value, ok := f.settings[key]; ok {
		return sqlc.PlatformSetting{Key: key, Value: value}, nil
	}
	return sqlc.PlatformSetting{}, pgx.ErrNoRows
}
func (f *operationalQueriesFake) ListBackups(context.Context, sqlc.ListBackupsParams) ([]sqlc.Backup, error) {
	return f.backups, nil
}
func (f *operationalQueriesFake) GetLatestBackupDrillResult(context.Context) (sqlc.BackupDrillResult, error) {
	if f.drill.ID == uuid.Nil {
		return sqlc.BackupDrillResult{}, pgx.ErrNoRows
	}
	return f.drill, nil
}
func (f *operationalQueriesFake) ListAlertEventsFiltered(context.Context, sqlc.ListAlertEventsFilteredParams) ([]sqlc.AlertEvent, error) {
	return f.alerts, nil
}
func (f *operationalQueriesFake) GetAlertEventByID(_ context.Context, id uuid.UUID) (sqlc.AlertEvent, error) {
	for _, row := range f.alerts {
		if row.ID == id {
			return row, nil
		}
	}
	return sqlc.AlertEvent{}, errors.New("missing")
}
func (f *operationalQueriesFake) ListAuditLogV1Filtered(context.Context, sqlc.AuditLogFilterParams) ([]sqlc.AuditLog, error) {
	return f.audits, nil
}
func (f *operationalQueriesFake) ListHelmRepositories(_ context.Context, params sqlc.ListHelmRepositoriesParams) ([]sqlc.HelmRepository, error) {
	start := int(params.Offset)
	if start >= len(f.repositories) {
		return []sqlc.HelmRepository{}, nil
	}
	end := min(start+int(params.Limit), len(f.repositories))
	return append([]sqlc.HelmRepository(nil), f.repositories[start:end]...), nil
}

func operationalAdapterFixture(t *testing.T, queries *operationalQueriesFake) *OperationalCapabilityAdapter {
	t.Helper()
	adapter, err := NewOperationalCapabilityAdapter(OperationalCapabilityConfig{Database: operationalDBFake{version: 147}, Queries: queries, Namespace: "astronomer", Release: "astronomer", ChartVersion: "1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	return adapter
}

func TestOperationalConfigurationIsAllowlistOnly(t *testing.T) {
	queries := &operationalQueriesFake{settings: map[string]json.RawMessage{"feature.charlie": json.RawMessage(`true`), "database.password": json.RawMessage(`"SENTINEL"`)}}
	adapter := operationalAdapterFixture(t, queries)
	descriptor, _ := capabilityByName("astronomer.installation.configuration")
	result, err := adapter.Execute(context.Background(), descriptor, rawArguments(t, map[string]any{"keys": []string{"feature.charlie"}}))
	if err != nil || string(result) != `{"settings":{"feature.charlie":true}}` {
		t.Fatalf("safe configuration=%s err=%v", result, err)
	}
	if _, err := adapter.Execute(context.Background(), descriptor, rawArguments(t, map[string]any{"keys": []string{"database.password"}})); err == nil {
		t.Fatal("sensitive configuration key was readable")
	}
}

func TestOperationalAlertsAndAuditOmitSensitiveDetailsAndDownstreamRows(t *testing.T) {
	managementAlertID := uuid.New()
	queries := &operationalQueriesFake{
		settings: map[string]json.RawMessage{},
		alerts: []sqlc.AlertEvent{
			{ID: managementAlertID, RuleID: uuid.New(), Status: "active", Message: "token=SENTINEL outage", Details: json.RawMessage(`{"secret":"SENTINEL"}`), FiredAt: time.Now()},
			{ID: uuid.New(), RuleID: uuid.New(), ClusterID: pgtype.UUID{Bytes: uuid.New(), Valid: true}, Status: "active", Message: "downstream", FiredAt: time.Now()},
		},
		audits: []sqlc.AuditLog{{ID: uuid.New(), Action: "settings.updated", ResourceType: "platform_setting", ResourceID: "feature.charlie", Detail: json.RawMessage(`{"api_key":"SENTINEL"}`), CreatedAt: time.Now()}},
	}
	adapter := operationalAdapterFixture(t, queries)
	for _, name := range []string{"astronomer.alert.list", "astronomer.audit.recent_changes"} {
		descriptor, _ := capabilityByName(name)
		result, err := adapter.Execute(context.Background(), descriptor, map[string]json.RawMessage{})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if contains := string(result); stringsContainsAny(contains, "SENTINEL", "downstream", "api_key") {
			t.Fatalf("%s leaked sensitive/downstream state: %s", name, result)
		}
	}

	descriptor, _ := capabilityByName("astronomer.alert.get")
	result, err := adapter.Execute(context.Background(), descriptor, rawArguments(t, map[string]any{"alert_id": managementAlertID.String()}))
	if err != nil || stringsContainsAny(string(result), "SENTINEL", "secret") {
		t.Fatalf("alert get leaked details: %s err=%v", result, err)
	}
}

func TestOperationalBackupsExcludeDownstreamClusterRows(t *testing.T) {
	queries := &operationalQueriesFake{settings: map[string]json.RawMessage{}, backups: []sqlc.Backup{
		{ID: uuid.New(), Name: "management", Status: "completed"},
		{ID: uuid.New(), Name: "downstream-SENTINEL", Status: "completed", ClusterID: pgtype.UUID{Bytes: uuid.New(), Valid: true}},
	}}
	adapter := operationalAdapterFixture(t, queries)
	descriptor, _ := capabilityByName("astronomer.backups.status")
	result, err := adapter.Execute(context.Background(), descriptor, nil)
	if err != nil || stringsContainsAny(string(result), "downstream", "SENTINEL") {
		t.Fatalf("backup status leaked downstream row: %s err=%v", result, err)
	}
}

func TestOperationalCatalogRepositoriesExposeSyncDiagnosticsWithoutURLSecrets(t *testing.T) {
	now := time.Now().UTC()
	queries := &operationalQueriesFake{settings: map[string]json.RawMessage{}, repositories: []sqlc.HelmRepository{{
		ID: uuid.New(), Name: "private charts", Url: "https://user:SENTINEL@charts.example.invalid/private/index.yaml?token=SENTINEL",
		RepoType: "helm", AuthType: "basic", AuthConfig: json.RawMessage(`{"username":"operator","password":"SENTINEL"}`),
		AuthConfigEncrypted: "SENTINEL-CIPHERTEXT", Enabled: true,
		LastSyncAttemptedAt: pgtype.Timestamptz{Time: now, Valid: true},
		LastSyncError:       "request returned 401 authorization token=SENTINEL",
	}}}
	adapter := operationalAdapterFixture(t, queries)
	descriptor, _ := capabilityByName("astronomer.catalog.repositories")
	result, err := adapter.Execute(context.Background(), descriptor, rawArguments(t, map[string]any{"page": 1, "page_size": 10}))
	if err != nil {
		t.Fatal(err)
	}
	value := string(result)
	for _, wanted := range []string{`"endpoint":"https://charts.example.invalid"`, `"sync_status":"failed"`, `"failure_code":"authentication"`, `"authentication_configured":true`} {
		if !strings.Contains(value, wanted) {
			t.Fatalf("catalog diagnostics missing %s: %s", wanted, value)
		}
	}
	if stringsContainsAny(value, "SENTINEL", "/private", "operator", "index.yaml") {
		t.Fatalf("catalog diagnostics leaked URL/auth/error material: %s", value)
	}
}

func stringsContainsAny(value string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}
