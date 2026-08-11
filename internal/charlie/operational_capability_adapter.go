package charlie

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	productversion "github.com/alphabravocompany/astronomer-go/pkg/version"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

type operationalCapabilityQueries interface {
	GetPlatformConfig(context.Context) (sqlc.PlatformConfiguration, error)
	GetPlatformSetting(context.Context, string) (sqlc.PlatformSetting, error)
	ListBackups(context.Context, sqlc.ListBackupsParams) ([]sqlc.Backup, error)
	GetLatestBackupDrillResult(context.Context) (sqlc.BackupDrillResult, error)
	ListAlertEventsFiltered(context.Context, sqlc.ListAlertEventsFilteredParams) ([]sqlc.AlertEvent, error)
	GetAlertEventByID(context.Context, uuid.UUID) (sqlc.AlertEvent, error)
	ListAuditLogV1Filtered(context.Context, sqlc.AuditLogFilterParams) ([]sqlc.AuditLog, error)
	ListHelmRepositories(context.Context, sqlc.ListHelmRepositoriesParams) ([]sqlc.HelmRepository, error)
}

type operationalDatabase interface {
	Health(context.Context) error
	SchemaVersion(context.Context) (int64, bool, error)
	Pool() *pgxpool.Pool
}

type OperationalCapabilityConfig struct {
	Database     operationalDatabase
	Queries      operationalCapabilityQueries
	Kubernetes   kubernetes.Interface
	Queue        queueCapabilityInspector
	Namespace    string
	Release      string
	ChartVersion string
	TLSCertFiles []string
}

type OperationalCapabilityAdapter struct {
	config           OperationalCapabilityConfig
	databaseSnapshot func(context.Context) (bool, int64, error)
}

func NewOperationalCapabilityAdapter(config OperationalCapabilityConfig) (*OperationalCapabilityAdapter, error) {
	if config.Database == nil || config.Queries == nil || strings.TrimSpace(config.Namespace) == "" || strings.TrimSpace(config.Release) == "" {
		return nil, fmt.Errorf("Charlie operational capability adapter is unavailable")
	}
	config.TLSCertFiles = append([]string(nil), config.TLSCertFiles...)
	adapter := &OperationalCapabilityAdapter{config: config}
	adapter.databaseSnapshot = func(ctx context.Context) (bool, int64, error) {
		pool := config.Database.Pool()
		if pool == nil {
			return false, 0, fmt.Errorf("database pool is unavailable")
		}
		var recovery bool
		var databaseBytes int64
		err := pool.QueryRow(ctx, `SELECT pg_is_in_recovery(), pg_database_size(current_database())`).Scan(&recovery, &databaseBytes)
		return recovery, databaseBytes, err
	}
	return adapter, nil
}

func OperationalCapabilityAdapters(adapter CapabilityExecutor) map[string]CapabilityExecutor {
	adapters := map[string]CapabilityExecutor{}
	for _, name := range []string{
		"astronomer.installation.summary", "astronomer.installation.readiness", "astronomer.installation.configuration",
		"astronomer.database.health", "astronomer.migrations.status", "astronomer.backups.status",
		"astronomer.tls.status", "astronomer.observability.health", "astronomer.alert.list",
		"astronomer.alert.get", "astronomer.audit.recent_changes", "astronomer.audit.search",
		"astronomer.catalog.repositories",
	} {
		adapters[name] = adapter
	}
	return adapters
}

func (a *OperationalCapabilityAdapter) Execute(ctx context.Context, capability CapabilityDescriptor, arguments map[string]json.RawMessage) (json.RawMessage, error) {
	var value any
	var err error
	switch capability.Name {
	case "astronomer.installation.summary":
		value, err = a.installationSummary(ctx)
	case "astronomer.installation.readiness":
		value, err = a.readiness(ctx)
	case "astronomer.installation.configuration":
		value, err = a.configuration(ctx, arguments)
	case "astronomer.database.health":
		value, err = a.databaseHealth(ctx)
	case "astronomer.migrations.status":
		value, err = a.migrations(ctx)
	case "astronomer.backups.status":
		value, err = a.backups(ctx)
	case "astronomer.tls.status":
		value, err = a.tlsStatus()
	case "astronomer.observability.health":
		value, err = a.observability(ctx, arguments)
	case "astronomer.alert.list":
		value, err = a.alerts(ctx, arguments)
	case "astronomer.alert.get":
		value, err = a.alert(ctx, arguments)
	case "astronomer.audit.recent_changes":
		value, err = a.audit(ctx, arguments)
	case "astronomer.audit.search":
		value, err = a.auditSearch(ctx, arguments)
	case "astronomer.catalog.repositories":
		value, err = a.catalogRepositories(ctx, arguments)
	default:
		return nil, fmt.Errorf("unsupported operational capability")
	}
	if err != nil {
		return nil, err
	}
	return marshalBounded(value, capability.MaxResponseBytes)
}

func (a *OperationalCapabilityAdapter) catalogRepositories(ctx context.Context, arguments map[string]json.RawMessage) (map[string]any, error) {
	page, size := pagination(arguments, 50)
	rows, err := a.config.Queries.ListHelmRepositories(ctx, sqlc.ListHelmRepositoriesParams{
		Limit: int32(size), Offset: int32((page - 1) * size),
	})
	if err != nil {
		return nil, err
	}
	items := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		status := "never_attempted"
		failureCode := ""
		if row.LastSyncAttemptedAt.Valid {
			status = "succeeded"
			if strings.TrimSpace(row.LastSyncError) != "" {
				status = "failed"
				failureCode = classifyTaskFailure(row.LastSyncError)
			}
		}
		item := map[string]any{
			"repository_id": row.ID, "name": row.Name, "repository_type": row.RepoType,
			"endpoint": safeRepositoryEndpoint(row.Url), "enabled": row.Enabled,
			"default": row.IsDefault, "scope": "global", "sync_status": status,
			"authentication_type":       row.AuthType,
			"authentication_configured": strings.TrimSpace(row.AuthConfigEncrypted) != "" || (len(row.AuthConfig) > 0 && string(row.AuthConfig) != "{}"),
		}
		if row.OwnerProjectID.Valid {
			item["scope"] = "project"
		}
		if row.LastSyncedAt.Valid {
			item["last_succeeded_at"] = row.LastSyncedAt.Time.UTC()
		}
		if row.LastSyncAttemptedAt.Valid {
			item["last_attempted_at"] = row.LastSyncAttemptedAt.Time.UTC()
		}
		if failureCode != "" {
			item["failure_code"] = failureCode
		}
		items = append(items, item)
	}
	return map[string]any{"items": items, "page": page, "page_size": size}, nil
}

func safeRepositoryEndpoint(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "withheld"
	}
	return strings.ToLower(parsed.Scheme) + "://" + parsed.Host
}

func (a *OperationalCapabilityAdapter) Verify(context.Context, CapabilityDescriptor, map[string]json.RawMessage, json.RawMessage) (bool, error) {
	return true, nil
}

func (a *OperationalCapabilityAdapter) installationSummary(ctx context.Context) (map[string]any, error) {
	platform, err := a.config.Queries.GetPlatformConfig(ctx)
	if err != nil {
		return nil, err
	}
	kubernetesVersion, distribution := "unavailable", "unknown"
	if a.config.Kubernetes != nil {
		if info, versionErr := a.config.Kubernetes.Discovery().ServerVersion(); versionErr == nil {
			kubernetesVersion = info.GitVersion
			distribution = kubernetesDistribution(info.GitVersion)
		}
	}
	components, componentsReady := a.managementComponents(ctx)
	return map[string]any{
		"installation_id": platform.InstanceID, "platform_name": platform.PlatformName,
		"astronomer_version": productversion.Version, "chart_version": a.config.ChartVersion,
		"namespace": a.config.Namespace, "release": a.config.Release,
		"kubernetes_version": kubernetesVersion, "kubernetes_distribution": distribution,
		"component_health": components, "components_ready": componentsReady,
	}, nil
}

func (a *OperationalCapabilityAdapter) readiness(ctx context.Context) (map[string]any, error) {
	databaseErr := a.config.Database.Health(ctx)
	version, dirty, schemaErr := a.config.Database.SchemaVersion(ctx)
	queues := []map[string]any{}
	if a.config.Queue != nil {
		for _, name := range charlieQueueNames {
			_, queueErr := a.config.Queue.GetQueueInfo(name)
			queues = append(queues, map[string]any{"queue": name, "ready": queueErr == nil})
		}
	}
	components, componentsReady := a.managementComponents(ctx)
	return map[string]any{
		"ready":    databaseErr == nil && schemaErr == nil && !dirty && version >= db.ExpectedSchemaVersion && componentsReady,
		"database": map[string]any{"ready": databaseErr == nil},
		"schema":   map[string]any{"ready": schemaErr == nil && !dirty && version >= db.ExpectedSchemaVersion, "version": version, "dirty": dirty, "expected": db.ExpectedSchemaVersion},
		"queues":   queues, "workers": components, "tunnel_hub": map[string]any{"ready": componentsReady},
		"locator": map[string]any{"ready": componentsReady}, "security_cache": map[string]any{"ready": componentsReady},
	}, nil
}

func (a *OperationalCapabilityAdapter) managementComponents(ctx context.Context) ([]map[string]any, bool) {
	if a.config.Kubernetes == nil {
		return []map[string]any{}, false
	}
	rows, err := a.config.Kubernetes.AppsV1().Deployments(a.config.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return []map[string]any{}, false
	}
	items := []map[string]any{}
	ready := true
	for _, row := range rows.Items {
		if !strings.HasPrefix(row.Name, a.config.Release+"-") || (row.Labels["app.kubernetes.io/instance"] != "" && row.Labels["app.kubernetes.io/instance"] != a.config.Release) {
			continue
		}
		desired := int32Value(row.Spec.Replicas)
		componentReady := desired > 0 && row.Status.ReadyReplicas >= desired && row.Status.AvailableReplicas >= desired
		ready = ready && componentReady
		items = append(items, map[string]any{"component": strings.TrimPrefix(row.Name, a.config.Release+"-"), "desired": desired, "ready": row.Status.ReadyReplicas, "available": row.Status.AvailableReplicas, "healthy": componentReady})
	}
	sort.Slice(items, func(i, j int) bool { return fmt.Sprint(items[i]["component"]) < fmt.Sprint(items[j]["component"]) })
	return items, ready && len(items) > 0
}

func (a *OperationalCapabilityAdapter) configuration(ctx context.Context, arguments map[string]json.RawMessage) (map[string]any, error) {
	keys := safeConfigurationKeys()
	if raw := arguments["keys"]; len(raw) > 0 {
		if err := json.Unmarshal(raw, &keys); err != nil {
			return nil, err
		}
	}
	values := map[string]any{}
	for _, key := range keys {
		if !containsString(safeConfigurationKeys(), key) {
			return nil, fmt.Errorf("configuration key is not allowlisted")
		}
		setting, err := a.config.Queries.GetPlatformSetting(ctx, key)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		var value any
		if json.Unmarshal(setting.Value, &value) != nil {
			continue
		}
		values[key] = value
	}
	return map[string]any{"settings": values}, nil
}

func (a *OperationalCapabilityAdapter) databaseHealth(ctx context.Context) (map[string]any, error) {
	if err := a.config.Database.Health(ctx); err != nil {
		return nil, err
	}
	recovery, databaseBytes, err := a.databaseSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	poolStats := map[string]any{"max_connections": int32(0), "total_connections": int32(0), "acquired_connections": int32(0), "idle_connections": int32(0), "empty_acquires": int64(0)}
	if pool := a.config.Database.Pool(); pool != nil {
		stats := pool.Stat()
		poolStats = map[string]any{"max_connections": stats.MaxConns(), "total_connections": stats.TotalConns(), "acquired_connections": stats.AcquiredConns(), "idle_connections": stats.IdleConns(), "empty_acquires": stats.EmptyAcquireCount()}
	}
	return map[string]any{
		"reachable": true, "in_recovery": recovery, "database_bytes": databaseBytes,
		"pool": poolStats,
	}, nil
}

func (a *OperationalCapabilityAdapter) migrations(ctx context.Context) (map[string]any, error) {
	version, dirty, err := a.config.Database.SchemaVersion(ctx)
	if err != nil {
		return nil, err
	}
	return map[string]any{"current": version, "expected": db.ExpectedSchemaVersion, "dirty": dirty, "ready": !dirty && version >= db.ExpectedSchemaVersion}, nil
}

func (a *OperationalCapabilityAdapter) backups(ctx context.Context) (map[string]any, error) {
	backups, err := a.config.Queries.ListBackups(ctx, sqlc.ListBackupsParams{Limit: 50})
	if err != nil {
		return nil, err
	}
	items := []map[string]any{}
	for _, backup := range backups {
		if backup.ClusterID.Valid {
			continue
		}
		items = append(items, map[string]any{"id": backup.ID, "name": backup.Name, "type": backup.BackupType, "status": backup.Status, "size_bytes": backup.FileSizeBytes, "started_at": nullableTime(backup.StartedAt), "completed_at": nullableTime(backup.CompletedAt)})
	}
	drillValue := any(nil)
	drill, drillErr := a.config.Queries.GetLatestBackupDrillResult(ctx)
	if drillErr == nil {
		drillValue = map[string]any{"id": drill.ID, "status": drill.Status, "started_at": drill.StartedAt, "finished_at": nullableTime(drill.FinishedAt), "schema_version": nullableInt(drill.SchemaVersion)}
	} else if !errors.Is(drillErr, pgx.ErrNoRows) {
		return nil, drillErr
	}
	return map[string]any{"management_backups": items, "latest_restore_drill": drillValue}, nil
}

func (a *OperationalCapabilityAdapter) tlsStatus() (map[string]any, error) {
	items := []map[string]any{}
	for _, path := range a.config.TLSCertFiles {
		if strings.TrimSpace(path) == "" {
			continue
		}
		encoded, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		block, _ := pem.Decode(encoded)
		if block == nil || block.Type != "CERTIFICATE" {
			return nil, fmt.Errorf("TLS certificate is invalid")
		}
		certificate, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, err
		}
		items = append(items, map[string]any{"subject": certificate.Subject.CommonName, "issuer": certificate.Issuer.CommonName, "not_before": certificate.NotBefore.UTC(), "not_after": certificate.NotAfter.UTC(), "dns_names": certificate.DNSNames, "expired": time.Now().After(certificate.NotAfter)})
	}
	return map[string]any{"certificates": items}, nil
}

func (a *OperationalCapabilityAdapter) observability(ctx context.Context, arguments map[string]json.RawMessage) (map[string]any, error) {
	template := stringArgument(arguments, "query_template")
	rangeName := stringArgument(arguments, "range")
	if rangeName == "" {
		rangeName = "15m"
	}
	value := map[string]any{"query_template": template, "range": rangeName, "series": []map[string]any{}}
	switch template {
	case "availability":
		value["series"] = []map[string]any{{"component": "database", "healthy": a.config.Database.Health(ctx) == nil}}
	case "latency":
		if a.config.Database.Pool() == nil {
			return nil, fmt.Errorf("database pool is unavailable")
		}
		pool := a.config.Database.Pool().Stat()
		value["series"] = []map[string]any{{"component": "database_pool", "acquire_duration_seconds": pool.AcquireDuration().Seconds()}}
	case "errors":
		series := []map[string]any{}
		if a.config.Queue != nil {
			for _, queue := range charlieQueueNames {
				if info, err := a.config.Queue.GetQueueInfo(queue); err == nil {
					series = append(series, map[string]any{"component": "queue", "queue": queue, "archived": info.Archived, "retry": info.Retry})
				}
			}
		}
		value["series"] = series
	case "saturation":
		if a.config.Database.Pool() == nil {
			return nil, fmt.Errorf("database pool is unavailable")
		}
		pool := a.config.Database.Pool().Stat()
		value["series"] = []map[string]any{{"component": "database_pool", "acquired": pool.AcquiredConns(), "maximum": pool.MaxConns()}}
	default:
		return nil, fmt.Errorf("observability template is not allowlisted")
	}
	return value, nil
}

func (a *OperationalCapabilityAdapter) alerts(ctx context.Context, arguments map[string]json.RawMessage) (map[string]any, error) {
	page, size := pagination(arguments, 50)
	rows, err := a.config.Queries.ListAlertEventsFiltered(ctx, sqlc.ListAlertEventsFilteredParams{Limit: int32(size * 2), Offset: int32((page - 1) * size), Status: optionalPGText(arguments, "status"), Severity: optionalPGText(arguments, "severity")})
	if err != nil {
		return nil, err
	}
	items := []map[string]any{}
	for _, row := range rows {
		if row.ClusterID.Valid {
			continue
		}
		items = append(items, sanitizedAlert(row))
		if len(items) >= int(size) {
			break
		}
	}
	return map[string]any{"items": items, "page": page, "page_size": size}, nil
}

func (a *OperationalCapabilityAdapter) alert(ctx context.Context, arguments map[string]json.RawMessage) (map[string]any, error) {
	id, err := uuidArgument(arguments, "alert_id")
	if err != nil {
		return nil, err
	}
	row, err := a.config.Queries.GetAlertEventByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if row.ClusterID.Valid {
		return nil, fmt.Errorf("alert is outside the management plane")
	}
	return sanitizedAlert(row), nil
}

func (a *OperationalCapabilityAdapter) audit(ctx context.Context, arguments map[string]json.RawMessage) (map[string]any, error) {
	limit := int32(int64Argument(arguments, "limit", 50))
	filter := sqlc.AuditLogFilterParams{ResourceType: stringArgument(arguments, "resource_type"), ResourceID: stringArgument(arguments, "resource_id"), From: sinceArgument(arguments, 24*time.Hour), HasFrom: true, Limit: limit}
	rows, err := a.config.Queries.ListAuditLogV1Filtered(ctx, filter)
	if err != nil {
		return nil, err
	}
	items := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		items = append(items, map[string]any{"id": row.ID, "created_at": row.CreatedAt.UTC(), "source": row.Source, "action": row.Action, "action_class": row.ActionClass, "resource_type": row.ResourceType, "resource_id": row.ResourceID, "resource_name": row.ResourceName, "status_code": row.StatusCode, "correlation_id": row.CorrelationID})
	}
	return map[string]any{"items": items}, nil
}

func (a *OperationalCapabilityAdapter) auditSearch(ctx context.Context, arguments map[string]json.RawMessage) (map[string]any, error) {
	page, size := pagination(arguments, 50)
	filter := sqlc.AuditLogFilterParams{
		ResourceType:  stringArgument(arguments, "resource_type"),
		ResourceID:    stringArgument(arguments, "resource_id"),
		Action:        stringArgument(arguments, "action"),
		ActionClass:   stringArgument(arguments, "action_class"),
		Result:        stringArgument(arguments, "result"),
		Source:        stringArgument(arguments, "source"),
		CorrelationID: stringArgument(arguments, "correlation_id"),
		From:          sinceArgument(arguments, 24*time.Hour),
		HasFrom:       true,
		Limit:         int32(size),
		Offset:        int32((page - 1) * size),
	}
	rows, err := a.config.Queries.ListAuditLogV1Filtered(ctx, filter)
	if err != nil {
		return nil, err
	}
	items := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		items = append(items, map[string]any{
			"id": row.ID, "created_at": row.CreatedAt.UTC(), "source": row.Source,
			"action": row.Action, "action_class": row.ActionClass, "resource_type": row.ResourceType,
			"resource_id": row.ResourceID, "resource_name": row.ResourceName,
			"status_code": row.StatusCode, "correlation_id": row.CorrelationID,
		})
	}
	return map[string]any{"items": items, "page": page, "page_size": size}, nil
}

func sanitizedAlert(row sqlc.AlertEvent) map[string]any {
	message := strings.Join(redactLogLines([]byte(row.Message), 1), "")
	return map[string]any{"id": row.ID, "rule_id": row.RuleID, "status": row.Status, "message": message, "fired_at": row.FiredAt.UTC(), "resolved_at": nullableTime(row.ResolvedAt), "acknowledged_at": nullableTime(row.AcknowledgedAt)}
}

func kubernetesDistribution(version string) string {
	lower := strings.ToLower(version)
	for _, candidate := range []string{"eks", "gke", "aks", "openshift", "k3s"} {
		if strings.Contains(lower, candidate) {
			return candidate
		}
	}
	return "kubernetes"
}
