package server

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/alphabravocompany/astronomer-go/internal/cacheinvalidate"
	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/handler"
	"github.com/alphabravocompany/astronomer-go/internal/vault"
	"github.com/alphabravocompany/astronomer-go/internal/worker/leader"
	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
)

func (c *productionComposition) initializeTenantHandlers(ctx context.Context, cfg *config.Config, logger *slog.Logger) error {
	database := c.database
	queries := c.queries
	jwtManager := c.jwtManager
	encryptor := c.encryptor
	bus := c.bus
	requester := c.requester
	rbacEngine := c.rbacEngine
	rbacQuerier := c.rbacQuerier
	toolHandler := c.toolHandler
	catalogHandler := c.catalogHandler
	// Fail-fast on a bad REDIS_URL — the old silent localhost fallback was
	// a production footgun. Returning an error
	// surfaces the misconfig at process start instead of letting every
	// asynq enqueue silently fail downstream.
	redisOpt, redisErr := asynq.ParseRedisURI(cfg.RedisURL)
	if redisErr != nil {
		return fmt.Errorf("parse REDIS_URL %q: %w", cfg.RedisURL, redisErr)
	}
	queue := asynq.NewClient(redisOpt)
	taskLeader := leader.New(database.Pool(), logger)
	var securityCacheCoordinator *cacheinvalidate.Coordinator
	var runtimeRedisClient redis.UniversalClient
	securityTarget := securityCacheTarget{jwt: jwtManager, rbac: rbacQuerier.Cache()}
	if redisClient, ok := redisOpt.MakeRedisClient().(redis.UniversalClient); ok && redisClient != nil {
		runtimeRedisClient = redisClient
		securityCacheCoordinator = cacheinvalidate.New(redisClient, securityTarget, os.Getenv("HOSTNAME"), cacheinvalidate.DefaultPeriod, logger)
		go securityCacheCoordinator.Run(ctx)
		go func() { <-ctx.Done(); _ = redisClient.Close() }()
	} else if cfg.ServerReplicas > 1 {
		securityCacheCoordinator = cacheinvalidate.New(nil, securityTarget, os.Getenv("HOSTNAME"), cacheinvalidate.DefaultPeriod, logger)
		logger.Error("distributed security cache invalidation is unavailable in a multi-replica deployment")
	} else {
		securityCacheCoordinator = cacheinvalidate.NewLocalOnly(securityTarget, os.Getenv("HOSTNAME"), logger)
		logger.Warn("distributed security cache invalidation is disabled; using local-only caches")
	}
	jwtManager.SetCacheInvalidationCoordinator(securityCacheCoordinator)
	if securityTarget.rbac != nil {
		securityTarget.rbac.SetInvalidationCoordinator(securityCacheCoordinator)
	}
	securityIngestRuntime := tasks.SecurityIngestRuntime{Deps: tasks.SecurityIngestDeps{
		Queries: queries,
		K8s:     requester,
		Outbox:  queries,
		Log:     logger,
		Bus:     bus,
	}, Leader: taskLeader}
	// Phase B3 — project enforcement controller: wire the project handler with
	// the asynq queue (for AddNamespace → enqueue project:reconcile) and the
	// shared K8sRequester (for ResourceQuota / LimitRange / NetworkPolicy
	// server-side apply through the tunnel).
	projectHandler := handler.NewProjectHandler(queries)
	// MUST be set — see clusterHandler.SetAuthorization.
	projectHandler.SetAuthorization(rbacEngine, rbacQuerier)
	projectHandler.SetEncryptor(encryptor)
	projectHandler.SetTaskQueue(queue)
	projectHandler.SetTaskOutbox(queries)
	projectHandler.SetK8sRequester(requester)
	projectHandler.SetLogger(logger)
	// MUST be set: project_namespaces feeds the synthetic namespace-scoped
	// bindings, so an add/remove-namespace that does not flush this cache leaves
	// a revoked namespace authorizing reads until the entry expires on its own.
	projectHandler.SetRBACInvalidator(rbacQuerier)
	// Atomic namespace add/remove: row-lock the project and write the JSONB
	// array + project_namespaces sidecar in one transaction, so a mid-write
	// failure can't desync them. Pool-backed; unit tests wire their own runner.
	projectHandler.SetRunTx(sqlcMutationTxRunner[handler.ProjectNamespaceTx](database))
	// Cluster templates (migration 049). Owns /api/v1/cluster-templates/*
	// CRUD plus the per-cluster bind/apply/reapply/detach surface. The
	// asynq client is shared with the rest of the platform so apply
	// tasks land in the same queue as decommission and delivery reconciliation.
	clusterTemplateHandler := handler.NewClusterTemplateHandler(queries)
	clusterTemplateHandler.SetAuthorization(rbacEngine, rbacQuerier)
	clusterTemplateHandler.SetRunTx(sqlcMutationTxRunner[handler.ClusterTemplateMutationTx](database))
	clusterTemplateHandler.SetEventBus(bus)
	clusterTemplateHandler.SetQueue(queue)
	clusterTemplateHandler.SetTaskOutbox(queries)
	// Cluster registries (migration 050). Multi-registry-per-cluster admin
	// UX. Apply queue uses the same asynq client; tunnel requester is
	// shared with project enforcement so the /test/ endpoint can dial the
	// member cluster's network from the management plane.
	clusterRegistriesHandler := handler.NewClusterRegistriesHandler(queries)
	clusterRegistriesHandler.SetRunTx(sqlcMutationTxRunner[handler.ClusterRegistryMutationTx](database))
	clusterRegistriesHandler.SetEventBus(bus)
	clusterRegistriesHandler.SetApplyEnqueue(queue)
	clusterRegistriesHandler.SetTaskOutbox(queries)
	clusterRegistriesHandler.SetRequester(requester)
	clusterRegistriesHandler.SetEncryptor(encryptor)
	// Network policy templates (migration 068). Sister of cluster
	// templates but namespace-scoped: deny-all-ingress, project-isolated,
	// namespace-only, allow-ingress-controllers. The reconciler shares
	// the tunnel K8sRequester so the same circuit-breaker / retry
	// behavior as every other tunnel-mediated K8s op applies.
	networkPoliciesHandler := handler.NewNetworkPolicyHandler(queries)
	networkPoliciesHandler.SetRunTx(sqlcMutationTxRunner[handler.NetworkPolicyMutationTx](database))
	networkPoliciesHandler.SetQueue(queue)
	networkPoliciesHandler.SetK8sRequester(requester)
	networkPolicyRuntime := tasks.NetworkPolicyRuntime{Deps: tasks.NetworkPolicyApplyDeps{
		Queries:   queries,
		Requester: requester,
	}}
	// Cloud credentials (migration 053). Project-scoped CRUD over
	// AWS / GCP / Azure / Generic secrets, with a /test/ endpoint
	// that dials each provider's "validate" SDK and a materialization
	// worker that fans the cleartext out to in-cluster k8s Secrets
	// via the tunnel. Encryptor is required for any write; tester is
	// the default impl (10s budget per provider call).
	cloudCredentialsHandler := handler.NewCloudCredentialHandler(queries)
	cloudCredentialsHandler.SetRunTx(sqlcMutationTxRunner[handler.CloudCredentialMutationTx](database))
	cloudCredentialsHandler.SetAuditor(queries)
	cloudCredentialsHandler.SetEncryptor(encryptor)
	cloudCredentialsHandler.SetEnqueuer(queue)
	cloudCredentialsHandler.SetTaskOutbox(queries)
	cloudCredentialsHandler.SetTester(handler.NewDefaultCloudTester())
	// Dashboard widgets (migration 058). Admin CRUD over widget rows +
	// datasource rows; render endpoints serve a per-scope widget grid.
	dashboardsHandler := handler.NewDashboardHandler(queries)
	dashboardsHandler.SetRunTx(sqlcMutationTxRunner[handler.DashboardMutationTx](database))
	dashboardsHandler.SetAuditor(queries)
	dashboardsHandler.SetEncryptor(encryptor)
	// Per-project BYO catalogs (migration 061).
	projectCatalogsHandler := handler.NewProjectCatalogHandler(queries)
	projectCatalogsHandler.SetRunTx(sqlcMutationTxRunner[handler.ProjectCatalogMutationTx](database))
	projectCatalogsHandler.SetAuditor(queries)
	projectCatalogsHandler.SetEncryptor(encryptor)
	// Cluster groups (migration 066).
	clusterGroupsHandler := handler.NewClusterGroupHandler(queries)
	clusterGroupsHandler.SetRunTx(sqlcMutationTxRunner[handler.ClusterGroupMutationTx](database))
	clusterGroupsHandler.SetAuditor(queries)
	handler.RegisterClusterGroupMetrics()
	tasks.ClusterGroupMetricsRefresher = func(ctx context.Context) {
		handler.RefreshClusterGroupMetrics(ctx, queries)
	}
	// Vault integration (migration 067). Resolver wired into each
	// install path so ${vault://...} markers in operator-supplied values
	// blobs are substituted in-memory at install time.
	vaultResolver := vault.NewResolver(queries, encryptor)
	vaultResolver.SetObserver(newVaultMetricsObserver(queries))
	vaultHandler := handler.NewVaultHandler(queries)
	vaultHandler.SetRunTx(sqlcMutationTxRunner[handler.VaultMutationTx](database))
	vaultHandler.SetAuditor(queries)
	vaultHandler.SetEncryptor(encryptor)
	vaultHandler.SetProbe(handler.LiveVaultProbe{})
	vaultHandler.SetResolver(vaultResolver)
	catalogHandler.SetVaultResolver(vaultResolver)
	toolHandler.SetVaultResolver(vaultResolver)
	clusterTemplateHandler.SetVaultResolver(vaultResolver)
	c.redisOpt = redisOpt
	c.queue = queue
	c.taskLeader = taskLeader
	c.securityCacheCoordinator = securityCacheCoordinator
	c.runtimeRedisClient = runtimeRedisClient
	c.securityIngestRuntime = securityIngestRuntime
	c.projectHandler = projectHandler
	c.clusterTemplateHandler = clusterTemplateHandler
	c.clusterRegistriesHandler = clusterRegistriesHandler
	c.networkPoliciesHandler = networkPoliciesHandler
	c.networkPolicyRuntime = networkPolicyRuntime
	c.cloudCredentialsHandler = cloudCredentialsHandler
	c.dashboardsHandler = dashboardsHandler
	c.projectCatalogsHandler = projectCatalogsHandler
	c.clusterGroupsHandler = clusterGroupsHandler
	c.vaultResolver = vaultResolver
	c.vaultHandler = vaultHandler
	return nil
}
