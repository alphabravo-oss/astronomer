package server

import (
	"context"
	"log/slog"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/crd"
	"github.com/alphabravocompany/astronomer-go/internal/handler"
	"github.com/alphabravocompany/astronomer-go/internal/scanner"
	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
	"github.com/google/uuid"
)

func (c *productionComposition) initializeClusterHandlers(ctx context.Context, cfg *config.Config, logger *slog.Logger) error {
	database := c.database
	queries := c.queries
	bus := c.bus
	hub := c.hub
	requester := c.requester
	queue := c.queue
	rbacEngine := c.rbacEngine
	rbacQuerier := c.rbacQuerier
	monitoringHandler := c.monitoringHandler
	toolHandler := c.toolHandler
	catalogHandler := c.catalogHandler
	backupHandler := c.backupHandler
	loggingHandler := c.loggingHandler
	securityHandler := c.securityHandler
	// Cluster snapshots (migration 052). Velero CRDs are driven over
	// the existing tunnel K8sRequester so the same circuit-breaker /
	// retry behaviour as every other tunnel-mediated K8s op applies.
	// Metric registration is idempotent — see RegisterClusterSnapshotsMetrics.
	clusterSnapshotsHandler := handler.NewClusterSnapshotsHandler(queries)
	clusterSnapshotsHandler.SetRunTx(sqlcMutationTxRunner[handler.ClusterSnapshotMutationTx](database))
	clusterSnapshotsHandler.SetRequester(requester)
	clusterSnapshotsHandler.SetEventBus(bus)
	// Control-plane (etcd) DR snapshots — OFF unless an operator opts in via
	// config (control_plane_snapshots_enabled). Left nil, the etcd routes below
	// never register, so the privileged snapshot Job path is unreachable.
	// ponytail: manual snapshots only; scheduled sweep wired separately if asked.
	var controlPlaneSnapshotHandler *handler.ControlPlaneSnapshotHandler
	controlPlaneSnapshotRuntime := tasks.ControlPlaneSnapshotRuntime{}
	if cfg.ControlPlaneSnapshotsEnabled {
		controlPlaneSnapshotHandler = handler.NewControlPlaneSnapshotHandler(queries)
		controlPlaneSnapshotHandler.SetRunTx(sqlcMutationTxRunner[handler.ControlPlaneSnapshotMutationTx](database))
		controlPlaneSnapshotHandler.SetRequester(requester)
		// Wire the sweep worker: it reconciles in-flight snapshot Jobs to a
		// terminal DB state (and auto-schedules rolling snapshots only when
		// feature.control_plane_snapshots is additionally enabled). The task
		// registry declares this handler optional when the chart feature is off.
		controlPlaneSnapshotRuntime = tasks.ControlPlaneSnapshotRuntime{
			Deps:         tasks.ControlPlaneSnapshotSweepDeps{Queries: queries, Log: logger},
			Applier:      controlPlaneSnapshotHandler.ApplySnapshotJob,
			StatusReader: controlPlaneSnapshotHandler.ReadSnapshotJobStatus,
		}
	}

	// Native / CRD grants — additive allow on the k8s-proxy authz hook after a
	// coarse deny. Always wired so CRD grants folded into cluster/project roles
	// take effect. The standalone /native-rbac-rules CRUD stays behind the
	// native_rbac_enabled flag.
	nativeRBACAuthz := newNativeRBACAuthorizer(queries)
	nativeRBACAuthz.setBindings(rbacQuerier)
	var nativeRBACHandler *handler.NativeRBACHandler
	if cfg.NativeRBACEnabled {
		nativeRBACHandler = handler.NewNativeRBACHandler(queries)
		nativeRBACHandler.SetRunTx(sqlcMutationTxRunner[handler.NativeRBACMutationTx](database))
		nativeRBACHandler.SetInvalidator(nativeRBACAuthz.Invalidate)
		// Privilege-escalation guard on native-rule authoring: the caller must
		// already hold the mapped (resource, verb) at the rule's scope. Without
		// this the guard is a no-op and an rbac:create holder can self-escalate.
		nativeRBACHandler.SetAuthorization(rbacEngine, rbacQuerier)
	}
	handler.RegisterClusterSnapshotsMetrics()
	handler.WireSnapshotWorkerMetrics()
	clusterSnapshotRuntime := tasks.ClusterSnapshotRuntime{Deps: tasks.ClusterSnapshotDeps{
		Queries: queries,
		Driver:  handler.NewVeleroDriverAdapter(requester),
		Log:     logger,
	}}
	// Migration 071 — service mesh detector. The handler's POST /detect/
	// path delegates to tasks.DetectAndUpsert, so the deps need to be
	// configured here in addition to the worker scheduler that fires the
	// periodic sweep.
	meshRuntime := tasks.MeshRuntime{Deps: tasks.MeshDetectDeps{
		Queries:   queries,
		Requester: requester,
	}}
	// Sprint 069: CRD-mirror v2 cluster-detail read surface + tunnel
	// ingest router. The Hub routes MIRROR_EVENT frames into
	// MirrorRouter, which upserts into the mirrored_* tables; the REST
	// handler reads them back out for the cluster-detail page. Periodic
	// prune (every 30m) is wired via worker/scheduler.go.
	clusterResourcesHandler := handler.NewClusterResourcesHandler(queries)
	mirrorRouter := crd.NewMirrorRouter(queries)
	// Sprint 062: wire the trivy VulnerabilityReport ingester into the
	// same MirrorRouter the sprint-069 GVKs use. The agent emits trivy
	// CRs via the same MIRROR_EVENT channel with Kind=VulnerabilityReport;
	// the router dispatches them into scanner.Ingester which writes the
	// image_vulnerability_reports + image_vulnerabilities tables that
	// the Image Scans dashboard reads. Without this wiring trivy reports
	// on the cluster are silently dropped (the router used to error on
	// unknown kind; now it'd no-op without the ingester) — exactly the
	// "image scan tab is empty even though trivy is running" symptom
	// operators were seeing.
	// T6.062 — wire the Ingester's audit hook to the audit writer
	// so every successful Trivy ingest (and every delete that follows
	// a stale-report prune) lands a `image_vulns.ingested` audit row.
	// Was nil since the package landed; the hook surface existed but
	// no caller plumbed it.
	trivyAuditHook := scanner.AuditHook(func(ctx context.Context, clusterID uuid.UUID, reportName, action string) {
		audit.Record(ctx, queries, audit.Event{
			Source:       "crd_mirror",
			Action:       "image_vulns." + action,
			ResourceType: "image_vulnerability_report",
			ResourceID:   reportName,
			Detail: map[string]any{
				"cluster_id":  clusterID.String(),
				"report_name": reportName,
			},
		})
	})
	trivyIngester := scanner.NewIngester(queries, database.Pool(), nil, trivyAuditHook)
	trivyIngester.SetEventBus(bus)
	mirrorRouter.SetVulnIngester(crd.NewVulnIngesterAdapter(trivyIngester.IngestUnstructured))
	hub.SetMirrorIngester(mirrorRouter)
	apiserverAuditHandler := handler.NewApiserverAuditHandler(handler.NewApiserverAuditStoreAdapter(queries))
	hub.SetAuditPersister(apiserverAuditHandler)
	// PATH A: mint the scoped apiserver-audit ingest token in CONNECT_ACK so an
	// agent configured with AUDIT_DELIVERY=http can authenticate its direct POST
	// to /clusters/{id}/apiserver-audit/ (clusters:write scope + audit_ingest:create).
	if issuer := auth.NewIngestIssuer(queries); issuer != nil {
		hub.SetAuditIngestIssuer(issuer)
	}
	controlPlaneHandler := handler.NewControlPlaneHandler(queries, monitoringHandler, toolHandler, catalogHandler, backupHandler, loggingHandler, securityHandler, queue)
	controlPlaneHandler.SetRunTx(sqlcMutationTxRunner[handler.ControlPlaneMutationTx](database))
	c.clusterSnapshotsHandler = clusterSnapshotsHandler
	c.controlPlaneSnapshotHandler = controlPlaneSnapshotHandler
	c.controlPlaneSnapshotRuntime = controlPlaneSnapshotRuntime
	c.nativeRBACAuthz = nativeRBACAuthz
	c.nativeRBACHandler = nativeRBACHandler
	c.clusterSnapshotRuntime = clusterSnapshotRuntime
	c.meshRuntime = meshRuntime
	c.clusterResourcesHandler = clusterResourcesHandler
	c.apiserverAuditHandler = apiserverAuditHandler
	c.controlPlaneHandler = controlPlaneHandler
	return nil
}
