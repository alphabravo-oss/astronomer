package server

import (
	"context"
	"log/slog"
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/charlie"
	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/handler"
	appmiddleware "github.com/alphabravocompany/astronomer-go/internal/server/middleware"
	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
	"k8s.io/client-go/discovery"
)

func (c *productionComposition) initializeCharlie(ctx context.Context, cfg *config.Config, logger *slog.Logger) error {
	database := c.database
	queries := c.queries
	bus := c.bus
	runtimeRedisClient := c.runtimeRedisClient
	localK8s := c.localK8s
	localNamespace := c.localNamespace
	localReleaseName := c.localReleaseName
	localChartVersion := c.localChartVersion
	charlieFeatures := c.charlieFeatures
	charlieAgentRuntime := c.charlieAgentRuntime
	charlieHelm := c.charlieHelm
	settingsCache := c.settingsCache
	// Charlie browser services and the signed configuration bridge are dormant
	// gate-checked objects. Listener, consumer, dispatcher, and capability-client
	// generations are materialized later only through RuntimeLifecycle.
	var (
		charlieSessionsHandler     *handler.CharlieSessionHandler
		charlieThreadsHandler      *handler.CharlieThreadHandler
		charlieApprovalsHandler    *handler.CharlieApprovalHandler
		charlieContextHandler      *handler.CharlieContextHandler
		charlieFindingsHandler     *handler.CharlieFindingHandler
		charlieOperationsHandler   *handler.CharlieOperationHandler
		charlieAdminHandler        *handler.CharlieAdminHandler
		charlieAdminService        *charlie.AdminService
		managedCharlieBridge       *charlie.ManagedBridge
		charlieInventory           *charlie.ManagementPlatformInventory
		charlieFindingProjection   *charlie.FindingProjection
		charlieCentralFindingStore *charlie.PGCentralFindingStore
		charlieFindingPublisher    *charlie.EventFindingPublisher
		charlieFindingEvents       handler.CharlieFindingEventAuthorizer
		charlieWriteFence          = charlie.NewDistributedWriteFence(database.Pool())
		charlieTriggerRuntime      = &tasks.CharlieTriggerRuntime{}
	)
	charlieBindings := charlieLiveBindings{queries: queries, bindings: appmiddleware.NewSQLCRBACQuerierWithCache(queries, nil)}
	agentNamespace := strings.TrimSpace(cfg.CharlieAgentNamespace)
	if agentNamespace == "" {
		agentNamespace = "astronomer-charlie"
	}
	if cfg.CharlieBridgeTLSCertFile != "" || cfg.CharlieBridgeTLSKeyFile != "" || cfg.CharlieBridgeCAFile != "" {
		var bridgeErr error
		managedCharlieBridge, bridgeErr = charlie.NewManagedBridge(charlie.ManagedBridgeConfig{
			AgentNamespace: agentNamespace, Certificate: cfg.CharlieBridgeTLSCertFile,
			PrivateKey: cfg.CharlieBridgeTLSKeyFile, ServerCA: cfg.CharlieBridgeCAFile, SigningKey: cfg.CharlieMCPActionSigningKeyFile,
		}, charlieFeatures, queries)
		if bridgeErr != nil {
			database.Close()
			return bridgeErr
		}
		{
			active := func() bool { return managedCharlieBridge.Active(context.Background()) }
			var discoveryClient discovery.DiscoveryInterface
			if localK8s != nil {
				discoveryClient = localK8s.Discovery()
			}
			contextProvider, contextErr := charlie.NewProductSessionContextProvider(queries, localNamespace, localReleaseName, localChartVersion, discoveryClient)
			if contextErr != nil {
				database.Close()
				return contextErr
			}
			auditor := charlie.NewDBLifecycleAuditor(queries)
			sessionService, sessionErr := charlie.NewSessionService(queries, managedCharlieBridge, contextProvider, charlieBindings, auditor, active)
			if sessionErr != nil {
				database.Close()
				return sessionErr
			}
			inventory := charlie.NewManagementPlatformInventory(discoveryClient, func(ctx context.Context) (string, error) {
				var raw string
				err := database.Pool().QueryRow(ctx, `SELECT current_setting('server_version_num')`).Scan(&raw)
				return raw, err
			}, nil)
			if runtimeRedisClient != nil {
				inventory.Valkey = func(ctx context.Context) (string, error) {
					return runtimeRedisClient.Info(ctx, "server").Result()
				}
			}
			sessionService.SetPlatformInventory(inventory)
			charlieInventory = inventory
			sessionAccess, accessErr := charlie.NewSessionAccessService(queries, charlieBindings, managedCharlieBridge, auditor, active)
			if accessErr != nil {
				database.Close()
				return accessErr
			}
			charlieSessionsHandler = handler.NewCharlieSessionHandler(sessionService, sessionAccess)
			threadService, threadErr := charlie.NewThreadService(queries, sessionService, sessionAccess, auditor, active)
			if threadErr != nil {
				database.Close()
				return threadErr
			}
			charlieThreadsHandler = handler.NewCharlieThreadHandler(threadService)
			findingAlertPlanner, plannerErr := charlie.NewFindingAlertPlanner(database.Pool())
			if plannerErr != nil {
				database.Close()
				return plannerErr
			}
			findingPublisher := charlie.NewPolicyFindingPublisher(bus, findingAlertPlanner)
			charlieFindingPublisher = findingPublisher
			approvalAccess, approvalErr := charlie.NewApprovalAccessService(queries, sessionAccess, charlieBindings, managedCharlieBridge, auditor, findingPublisher, cfg.CharlieMCPActionSigningKeyFile)
			if approvalErr != nil {
				database.Close()
				return approvalErr
			}
			charlieApprovalsHandler = handler.NewCharlieApprovalHandler(approvalAccess)
			charlieContextHandler = handler.NewCharlieContextHandler(charlie.NewContextSearchService(queries, charlieBindings, active))
			operationAccess, operationErr := charlie.NewOperationAccessService(queries, sessionAccess)
			if operationErr != nil {
				database.Close()
				return operationErr
			}
			charlieOperationsHandler = handler.NewCharlieOperationHandler(operationAccess)
			centralFindingStore, findingErr := charlie.NewPGCentralFindingStore(database.Pool())
			if findingErr != nil {
				database.Close()
				return findingErr
			}
			charlieCentralFindingStore = centralFindingStore
			centralFindingSync, findingErr := charlie.NewCentralFindingSyncService(queries, sessionAccess, managedCharlieBridge, centralFindingStore, findingPublisher, active)
			if findingErr != nil {
				database.Close()
				return findingErr
			}
			findingAccess, findingErr := charlie.NewFindingAccessService(queries, charlieBindings, managedCharlieBridge, auditor, findingPublisher, centralFindingSync, active)
			if findingErr != nil {
				database.Close()
				return findingErr
			}
			charlieFindingsHandler = handler.NewCharlieFindingHandler(findingAccess)
			charlieFindingEvents = findingAccess
		}
	}
	if managedCharlieBridge != nil && charlieCentralFindingStore != nil && charlieFindingPublisher != nil {
		findingBridge := charlie.NewManagedFindingChangeBridge(managedCharlieBridge)
		charlieFindingProjection, _ = charlie.NewFindingProjection(database.Pool(), findingBridge, charlieCentralFindingStore, charlieFindingPublisher, func() bool { return managedCharlieBridge.Active(context.Background()) })
	}
	if adminService, adminErr := charlie.NewAdminService(database.Pool(), managedCharlieBridge); adminErr != nil {
		charlie.LogOperationalFailure(context.Background(), logger, "bootstrap.admin_service_unavailable", "")
	} else {
		charlieAdminService = adminService
		adminService.SetWriteFence(charlieWriteFence)
		adminService.SetAgentRuntime(charlieAgentRuntime)
		if localK8s != nil {
			workload := charlie.NewKubernetesAgentWorkload(localK8s)
			adminService.SetAgentWorkload(workload)
			if charlieHelm != nil && managedCharlieBridge != nil {
				adminService.SetModeCeilingRollout(&charlie.HelmModeCeilingRollout{
					Queries: queries, Client: localK8s, Helm: charlieHelm,
					Workload: workload, Bridge: managedCharlieBridge,
				})
			}
		}
		charlieAdminHandler = handler.NewCharlieAdminHandler(adminService, queries)
		charlieAdminHandler.SetSettingsCache(settingsCache)
	}
	c.charlieSessionsHandler = charlieSessionsHandler
	c.charlieThreadsHandler = charlieThreadsHandler
	c.charlieApprovalsHandler = charlieApprovalsHandler
	c.charlieContextHandler = charlieContextHandler
	c.charlieFindingsHandler = charlieFindingsHandler
	c.charlieOperationsHandler = charlieOperationsHandler
	c.charlieAdminHandler = charlieAdminHandler
	c.charlieAdminService = charlieAdminService
	c.managedCharlieBridge = managedCharlieBridge
	c.charlieInventory = charlieInventory
	c.charlieFindingProjection = charlieFindingProjection
	c.charlieFindingEvents = charlieFindingEvents
	c.charlieWriteFence = charlieWriteFence
	c.charlieTriggerRuntime = charlieTriggerRuntime
	c.charlieBindings = charlieBindings
	return nil
}
