package charlie

import (
	"context"
	"encoding/json"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// Unexpected SQL calls hit the nil embedded implementation and fail the test.
// Each affected real adapter dispatches to the explicit query override below.
type sqlPageFamilyQueries struct {
	*sqlc.Queries
	query                          string
	params                         any
	countParams, bundleParams      any
	calls, countCalls, bundleCalls int
	t                              *testing.T
}

func (q *sqlPageFamilyQueries) record(name string, params any) {
	q.query = name
	q.params = params
	q.calls++
}
func (q *sqlPageFamilyQueries) ListDeliverySources(_ context.Context, p sqlc.ListDeliverySourcesParams) ([]sqlc.ListDeliverySourcesRow, error) {
	q.record("ListDeliverySources", p)
	return nil, nil
}
func (q *sqlPageFamilyQueries) ListComponentBundles(_ context.Context, p sqlc.ListComponentBundlesParams) ([]sqlc.ComponentBundle, error) {
	q.record("ListComponentBundles", p)
	return nil, nil
}
func (q *sqlPageFamilyQueries) ListComponentBundleVersions(_ context.Context, p sqlc.ListComponentBundleVersionsParams) ([]sqlc.ComponentBundleVersion, error) {
	q.record("ListComponentBundleVersions", p)
	return nil, nil
}
func (q *sqlPageFamilyQueries) ListDeliveryTargets(_ context.Context, p sqlc.ListDeliveryTargetsParams) ([]sqlc.DeliveryTarget, error) {
	q.record("ListDeliveryTargets", p)
	return nil, nil
}
func (q *sqlPageFamilyQueries) ListDeliveryRollouts(_ context.Context, p sqlc.ListDeliveryRolloutsParams) ([]sqlc.DeliveryRollout, error) {
	q.record("ListDeliveryRollouts", p)
	return nil, nil
}
func (q *sqlPageFamilyQueries) ListClusterDeployments(_ context.Context, p sqlc.ListClusterDeploymentsParams) ([]sqlc.ClusterDeployment, error) {
	q.record("ListClusterDeployments", p)
	return nil, nil
}
func (q *sqlPageFamilyQueries) ListHelmRepositories(_ context.Context, p sqlc.ListHelmRepositoriesParams) ([]sqlc.HelmRepository, error) {
	q.record("ListHelmRepositories", p)
	return nil, nil
}
func (q *sqlPageFamilyQueries) ListAlertEventsFiltered(_ context.Context, p sqlc.ListAlertEventsFilteredParams) ([]sqlc.AlertEvent, error) {
	q.record("ListAlertEventsFiltered", p)
	return nil, nil
}
func (q *sqlPageFamilyQueries) ListAuditLogV1Filtered(_ context.Context, p sqlc.AuditLogFilterParams) ([]sqlc.AuditLog, error) {
	if !p.HasFrom || p.From.IsZero() || p.From.After(time.Now()) {
		q.t.Fatal("audit lower time bound changed")
	}
	p.From = time.Time{}
	q.record("ListAuditLogV1Filtered", p)
	return nil, nil
}
func (q *sqlPageFamilyQueries) CharlieClusterAgentList(_ context.Context, p sqlc.CharlieClusterAgentListParams) ([]sqlc.CharlieClusterAgentListRow, error) {
	q.record("CharlieClusterAgentList", p)
	return nil, nil
}
func (q *sqlPageFamilyQueries) ListTaskOutboxFiltered(_ context.Context, p sqlc.ListTaskOutboxFilteredParams) ([]sqlc.TaskOutbox, error) {
	q.record("ListTaskOutboxFiltered", p)
	return nil, nil
}
func (q *sqlPageFamilyQueries) ListControlPlaneAlerts(_ context.Context, p sqlc.ListControlPlaneAlertsParams) ([]sqlc.ControlPlaneAlert, error) {
	q.record("ListControlPlaneAlerts", p)
	return nil, nil
}
func (q *sqlPageFamilyQueries) ListCatalogOperations(_ context.Context, p sqlc.ListCatalogOperationsParams) ([]sqlc.CatalogOperation, error) {
	q.record("ListCatalogOperations", p)
	return nil, nil
}
func (q *sqlPageFamilyQueries) ListToolOperations(_ context.Context, p sqlc.ListToolOperationsParams) ([]sqlc.ToolOperation, error) {
	q.record("ListToolOperations", p)
	return nil, nil
}
func (q *sqlPageFamilyQueries) ListMonitoringOperations(_ context.Context, p sqlc.ListMonitoringOperationsParams) ([]sqlc.MonitoringOperation, error) {
	q.record("ListMonitoringOperations", p)
	return nil, nil
}
func (q *sqlPageFamilyQueries) ListLoggingOperations(_ context.Context, p sqlc.ListLoggingOperationsParams) ([]sqlc.LoggingOperation, error) {
	q.record("ListLoggingOperations", p)
	return nil, nil
}
func (q *sqlPageFamilyQueries) ListWorkloadOperations(_ context.Context, p sqlc.ListWorkloadOperationsParams) ([]sqlc.WorkloadOperation, error) {
	q.record("ListWorkloadOperations", p)
	return nil, nil
}
func (q *sqlPageFamilyQueries) CountDeliverySources(_ context.Context, p sqlc.CountDeliverySourcesParams) (int64, error) {
	q.countParams = p
	q.countCalls++
	return 23, nil
}
func (q *sqlPageFamilyQueries) CountComponentBundles(_ context.Context, p uuid.UUID) (int64, error) {
	q.countParams = p
	q.countCalls++
	return 23, nil
}
func (q *sqlPageFamilyQueries) CountDeliveryTargets(_ context.Context, p uuid.UUID) (int64, error) {
	q.countParams = p
	q.countCalls++
	return 23, nil
}
func (q *sqlPageFamilyQueries) CountDeliveryRollouts(_ context.Context, p sqlc.CountDeliveryRolloutsParams) (int64, error) {
	q.countParams = p
	q.countCalls++
	return 23, nil
}
func (q *sqlPageFamilyQueries) CountClusterDeployments(_ context.Context, p sqlc.CountClusterDeploymentsParams) (int64, error) {
	q.countParams = p
	q.countCalls++
	return 23, nil
}
func (q *sqlPageFamilyQueries) GetComponentBundle(_ context.Context, p sqlc.GetComponentBundleParams) (sqlc.ComponentBundle, error) {
	q.bundleCalls++
	q.bundleParams = p
	return sqlc.ComponentBundle{ID: p.ID, ProjectID: p.ProjectID}, nil
}

type sqlPageFamilyCase struct {
	name, family, query string
	defaultSize         int64
	args                map[string]any
	want                func(page, size int64) any
	counted             bool
}

func sqlPageFamilyCases() []sqlPageFamilyCase {
	scope := uuid.MustParse("00000000-0000-4000-8000-000000000001")
	return []sqlPageFamilyCase{
		{"astronomer.delivery.sources", "delivery", "ListDeliverySources", 50, map[string]any{"project_id": scope.String(), "status": "ready"}, func(page, size int64) any {
			return sqlc.ListDeliverySourcesParams{ProjectID: scope, Status: pgtype.Text{String: "ready", Valid: true}, QueryLimit: int32(size), QueryOffset: int32((page - 1) * size)}
		}, true},
		{"astronomer.delivery.bundles", "delivery", "ListComponentBundles", 50, map[string]any{"project_id": scope.String()}, func(page, size int64) any {
			return sqlc.ListComponentBundlesParams{ProjectID: scope, QueryLimit: int32(size), QueryOffset: int32((page - 1) * size)}
		}, true},
		{"astronomer.delivery.bundle_get", "delivery", "ListComponentBundleVersions", 25, map[string]any{"project_id": scope.String(), "bundle_id": scope.String()}, func(page, size int64) any {
			return sqlc.ListComponentBundleVersionsParams{BundleID: scope, QueryLimit: int32(size), QueryOffset: int32((page - 1) * size)}
		}, false},
		{"astronomer.delivery.targets", "delivery", "ListDeliveryTargets", 50, map[string]any{"project_id": scope.String()}, func(page, size int64) any {
			return sqlc.ListDeliveryTargetsParams{ProjectID: scope, QueryLimit: int32(size), QueryOffset: int32((page - 1) * size)}
		}, true},
		{"astronomer.delivery.rollouts", "delivery", "ListDeliveryRollouts", 50, map[string]any{"project_id": scope.String(), "state": "progressing"}, func(page, size int64) any {
			return sqlc.ListDeliveryRolloutsParams{ProjectID: scope, State: pgtype.Text{String: "progressing", Valid: true}, QueryLimit: int32(size), QueryOffset: int32((page - 1) * size)}
		}, true},
		{"astronomer.delivery.deployments", "delivery", "ListClusterDeployments", 50, map[string]any{"project_id": scope.String(), "cluster_id": scope.String(), "phase": "ready"}, func(page, size int64) any {
			return sqlc.ListClusterDeploymentsParams{ProjectID: scope, ClusterID: pgtype.UUID{Bytes: scope, Valid: true}, Phase: pgtype.Text{String: "ready", Valid: true}, QueryLimit: int32(size), QueryOffset: int32((page - 1) * size)}
		}, true},
		{"astronomer.catalog.repositories", "operational", "ListHelmRepositories", 50, map[string]any{}, func(page, size int64) any {
			return sqlc.ListHelmRepositoriesParams{Limit: int32(size), Offset: int32((page - 1) * size)}
		}, false},
		{"astronomer.alert.list", "operational", "ListAlertEventsFiltered", 50, map[string]any{"status": "active", "severity": "critical"}, func(page, size int64) any {
			return sqlc.ListAlertEventsFilteredParams{Status: pgtype.Text{String: "active", Valid: true}, Severity: pgtype.Text{String: "critical", Valid: true}, Limit: int32(size * 2), Offset: int32((page - 1) * size)}
		}, false},
		{"astronomer.audit.search", "operational", "ListAuditLogV1Filtered", 50, map[string]any{"resource_type": "backup", "resource_id": "resource-a", "action": "update", "action_class": "write", "result": "success", "source": "server", "correlation_id": "corr-a"}, func(page, size int64) any {
			return sqlc.AuditLogFilterParams{ResourceType: "backup", ResourceID: "resource-a", Action: "update", ActionClass: "write", Result: "success", Source: "server", CorrelationID: "corr-a", HasFrom: true, Limit: int32(size), Offset: int32((page - 1) * size)}
		}, false},
		{"astronomer.cluster_agents.list", "cluster", "CharlieClusterAgentList", 50, map[string]any{"environment": "prod", "region": "us-east", "state": "connected"}, func(page, size int64) any {
			return sqlc.CharlieClusterAgentListParams{Environment: pgtype.Text{String: "prod", Valid: true}, Region: pgtype.Text{String: "us-east", Valid: true}, ConnectionState: pgtype.Text{String: "connected", Valid: true}, PageLimit: int32(size), PageOffset: int32((page - 1) * size)}
		}, false},
		{"astronomer.task_outbox.list", "pipeline", "ListTaskOutboxFiltered", 50, map[string]any{"status": "failed", "task_type": "catalog:sync"}, func(page, size int64) any {
			return sqlc.ListTaskOutboxFilteredParams{Status: "failed", TaskType: "catalog:sync", Limit: int32(size), Offset: int32((page - 1) * size)}
		}, false},
		{"astronomer.controllers.alerts", "pipeline", "ListControlPlaneAlerts", 50, map[string]any{"status": "active", "controller": "catalog"}, func(page, size int64) any {
			return sqlc.ListControlPlaneAlertsParams{Status: pgtype.Text{String: "active", Valid: true}, Controller: pgtype.Text{String: "catalog", Valid: true}, Limit: int32(size), Offset: int32((page - 1) * size)}
		}, false},
		{"astronomer.catalog.operations", "pipeline", "ListCatalogOperations", 50, map[string]any{"status": "failed"}, func(page, size int64) any {
			return sqlc.ListCatalogOperationsParams{Status: pgtype.Text{String: "failed", Valid: true}, Limit: int32(size), Offset: int32((page - 1) * size)}
		}, false},
		{"astronomer.tools.operations", "pipeline", "ListToolOperations", 50, map[string]any{"status": "failed"}, func(page, size int64) any {
			return sqlc.ListToolOperationsParams{Status: pgtype.Text{String: "failed", Valid: true}, Limit: int32(size), Offset: int32((page - 1) * size)}
		}, false},
		{"astronomer.monitoring.operations", "pipeline", "ListMonitoringOperations", 50, map[string]any{"status": "failed"}, func(page, size int64) any {
			return sqlc.ListMonitoringOperationsParams{Status: pgtype.Text{String: "failed", Valid: true}, Limit: int32(size), Offset: int32((page - 1) * size)}
		}, false},
		{"astronomer.logging.operations", "pipeline", "ListLoggingOperations", 50, map[string]any{"status": "failed"}, func(page, size int64) any {
			return sqlc.ListLoggingOperationsParams{Status: pgtype.Text{String: "failed", Valid: true}, Limit: int32(size), Offset: int32((page - 1) * size)}
		}, false},
		{"astronomer.workloads.operations", "pipeline", "ListWorkloadOperations", 50, map[string]any{"status": "failed"}, func(page, size int64) any {
			return sqlc.ListWorkloadOperationsParams{Status: pgtype.Text{String: "failed", Valid: true}, Limit: int32(size), Offset: int32((page - 1) * size)}
		}, false},
	}
}
func sqlPageFamilyAdapter(q *sqlPageFamilyQueries, family string) CapabilityExecutor {
	switch family {
	case "delivery":
		return &DeliveryCapabilityAdapter{queries: q}
	case "operational":
		return &OperationalCapabilityAdapter{config: OperationalCapabilityConfig{Queries: q}}
	case "cluster":
		return &ClusterAgentCapabilityAdapter{queries: q}
	case "pipeline":
		return &WorkPipelineCapabilityAdapter{queries: q}
	default:
		panic("unknown fixture family")
	}
}
func TestCapabilitySQLPaginationFamiliesPreserveQueryScopeAndMetadata(t *testing.T) {
	for _, tt := range sqlPageFamilyCases() {
		t.Run(tt.name, func(t *testing.T) {
			for _, mode := range []string{"default", "maximum"} {
				t.Run(mode, func(t *testing.T) {
					descriptor, ok := capabilityByName(tt.name)
					if !ok {
						t.Fatal("canonical descriptor missing")
					}
					args := make(map[string]any, len(tt.args)+2)
					for k, v := range tt.args {
						args[k] = v
					}
					page, size := int64(1), tt.defaultSize
					if mode == "maximum" {
						page, size = 10000, 100
						args["page"], args["page_size"] = page, size
					}
					raw := rawArguments(t, args)
					if err := validateCapabilityArguments(descriptor, raw); err != nil {
						t.Fatal("fixture violates existing canonical schema", err)
					}
					q := &sqlPageFamilyQueries{t: t}
					result, err := sqlPageFamilyAdapter(q, tt.family).Execute(context.Background(), descriptor, raw)
					if err != nil {
						t.Fatal(err)
					}
					if q.calls != 1 || q.query != tt.query || !reflect.DeepEqual(q.params, tt.want(page, size)) {
						t.Fatal("query pagination or scope/filter parameters changed")
					}
					var metadata struct {
						Page  int64  `json:"page"`
						Size  int64  `json:"page_size"`
						Total *int64 `json:"total"`
					}
					if err := json.Unmarshal(result, &metadata); err != nil {
						t.Fatal(err)
					}
					if metadata.Page != page || metadata.Size != size {
						t.Fatal("public int64 pagination metadata changed")
					}
					if tt.counted {
						if q.countCalls != 1 || metadata.Total == nil || *metadata.Total != 23 || !reflect.DeepEqual(q.countParams, expectedSQLPageCountParams(tt.want(page, size))) {
							t.Fatal("legacy total/count query changed")
						}
					} else if q.countCalls != 0 || metadata.Total != nil {
						t.Fatal("uncounted response acquired count semantics")
					}
					expectedBundle := 0
					if tt.name == "astronomer.delivery.bundle_get" {
						expectedBundle = 1
					}
					if q.bundleCalls != expectedBundle {
						t.Fatal("project-scoped bundle lookup changed")
					}
					if expectedBundle == 1 {
						scope := uuid.MustParse(tt.args["project_id"].(string))
						id := uuid.MustParse(tt.args["bundle_id"].(string))
						if !reflect.DeepEqual(q.bundleParams, sqlc.GetComponentBundleParams{ID: id, ProjectID: scope}) {
							t.Fatal("bundle lookup scope changed")
						}
					}
				})
			}
		})
	}
}
func TestCapabilitySQLPaginationSchemasKeepAllExistingBounds(t *testing.T) {
	for _, tt := range sqlPageFamilyCases() {
		t.Run(tt.name, func(t *testing.T) {
			descriptor, ok := capabilityByName(tt.name)
			if !ok {
				t.Fatal("canonical descriptor missing")
			}
			fields := capabilityFieldSchemas(tt.name)
			for _, field := range []string{"page", "page_size"} {
				maximum := int64(100)
				if field == "page" {
					maximum = 10000
				}
				schema, ok := fields[field]
				if !ok || schema.Type != "integer" || schema.Required || schema.Minimum != 1 || schema.Maximum != maximum {
					t.Fatal("canonical numeric range changed")
				}
				for _, value := range []int64{1, maximum} {
					args := make(map[string]any, len(tt.args)+1)
					for k, v := range tt.args {
						args[k] = v
					}
					args[field] = value
					if err := validateCapabilityArguments(descriptor, rawArguments(t, args)); err != nil {
						t.Fatal("accepted original numeric edge rejected", err)
					}
				}
				for _, value := range []int64{0, -1, maximum + 1, math.MaxInt64} {
					args := make(map[string]any, len(tt.args)+1)
					for k, v := range tt.args {
						args[k] = v
					}
					args[field] = value
					if validateCapabilityArguments(descriptor, rawArguments(t, args)) == nil {
						t.Fatal("canonical numeric limit not enforced")
					}
				}
			}
		})
	}
}

func expectedSQLPageCountParams(listParams any) any {
	switch p := listParams.(type) {
	case sqlc.ListDeliverySourcesParams:
		return sqlc.CountDeliverySourcesParams{ProjectID: p.ProjectID, Status: p.Status}
	case sqlc.ListComponentBundlesParams:
		return p.ProjectID
	case sqlc.ListDeliveryTargetsParams:
		return p.ProjectID
	case sqlc.ListDeliveryRolloutsParams:
		return sqlc.CountDeliveryRolloutsParams{ProjectID: p.ProjectID, State: p.State}
	case sqlc.ListClusterDeploymentsParams:
		return sqlc.CountClusterDeploymentsParams{ProjectID: p.ProjectID, ClusterID: p.ClusterID, Phase: p.Phase}
	default:
		panic("unknown counted fixture")
	}
}
