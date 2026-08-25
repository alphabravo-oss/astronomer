package server

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/charlie"
	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler"
	"github.com/alphabravocompany/astronomer-go/internal/worker"
	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
)

type runtimeTaskComposition struct {
	core    tasks.CoreRuntime
	project tasks.ProjectRuntime
}

func (c *productionComposition) composeRuntimeTasks(cfg *config.Config, logger *slog.Logger, routed *routerComposition, foundation *runtimeFoundation) (*runtimeTaskComposition, error) {
	core := tasks.CoreRuntime{Deps: tasks.RuntimeDependencies{
		Queries: c.queries, ManagementBackup: routed.deps.AdminDrill, Log: logger,
		AgentImageRepo: cfg.AgentImageRepository, AgentImageTag: cfg.AgentImageTag,
		PlatformName: "Astronomer", Leader: c.taskLeader, K8s: c.requester,
		ResourceDecryptor: c.encryptor, Enqueuer: c.queue, Bus: c.bus,
		CatalogDecryptor: tasks.CatalogDecryptorFor(c.encryptor),
		MonitoringCipher: tasks.MonitoringCipherFor(c.encryptor),
	}}
	clusterDecommission := tasks.ClusterDecommissionRuntime{Deps: tasks.ClusterDecommissionDeps{
		Queries: c.queries, Tunnel: c.hub, RBACCache: c.rbacQuerier.Cache(),
	}}
	clusterTemplate := tasks.ClusterTemplateRuntime{Deps: tasks.ClusterTemplateApplyDeps{
		Queries: c.queries, Installer: c.toolHandler,
	}, RecoveryEnqueuer: c.queue}
	toolDrift := tasks.ToolDriftRuntime{Deps: tasks.ToolDriftSweepDeps{Queries: c.queries, Helm: c.helmRequester}}
	clusterRegistry := c.clusterRegistriesHandler.WorkerRuntime(c.queries)
	cloudCredential := tasks.CloudCredentialRuntime{Deps: tasks.CloudCredentialMaterializeDeps{
		Queries: c.queries, Requester: handler.ProjectK8sRequesterFromHandlerRequester(c.requester), Decryptor: c.encryptor,
	}}
	project := c.projectHandler.WorkerRuntime()
	crdOwnership := tasks.CRDOwnershipRuntime{Deps: tasks.CRDOwnershipDriftDeps{Queries: c.queries, Dynamic: c.localDynamic}}
	if cfg.RedisURL != "" {
		terminalPublisher, _ := charlie.NewQueueTerminalFailurePublisher(sqlc.New(c.database.Pool()))
		runtime := worker.TunnelRuntime{
			Features: tasks.TunnelRuntimeFeatures{CRDOwnership: cfg.CRDEnabled}, Core: core,
			ToolDrift: toolDrift, CharlieTrigger: c.charlieTriggerRuntime,
			ClusterTemplate: clusterTemplate, NetworkPolicy: c.networkPolicyRuntime,
			Mesh: c.meshRuntime, CloudCredential: cloudCredential,
			ClusterRegistry: clusterRegistry, Project: project,
			ClusterSnapshot: c.clusterSnapshotRuntime, ClusterDecommission: clusterDecommission,
			ControlPlaneSnapshot: c.controlPlaneSnapshotRuntime, Deferred: routed.deferredRuntime,
			KubectlSessionReap: c.kubectlSessionReapRuntime, SecurityIngest: c.securityIngestRuntime,
			CRDOwnership: crdOwnership,
			Dex:          tasks.DexOperationRuntime{Queries: c.queries, Executor: c.dexHandler},
		}
		tunnelWorker, err := worker.NewTunnelWorker(
			cfg.RedisURL, cfg.TunnelWorkerConcurrency, logger, runtime,
			worker.NewTerminalFailureErrorHandler(terminalPublisher, logger),
		)
		if err != nil {
			logger.Error("failed to create tunnel-queue asynq server", "error", err)
		} else {
			tunnelWorker.RegisterTunnelHandlers()
			foundation.server.tunnelWorker = tunnelWorker
		}
	}
	if config.IsProduction(cfg) {
		capabilities := map[worker.TaskCapability]bool{
			worker.CapabilityDatabase: c.database.Pool() != nil,
			worker.CapabilityRedis:    strings.TrimSpace(cfg.RedisURL) != "",
			worker.CapabilityTunnel:   c.hub != nil, worker.CapabilityEncryption: c.encryptor != nil,
			worker.CapabilityOutboundHTTP: true,
		}
		if foundation.server.tunnelWorker == nil {
			foundation.cancel()
			c.database.Close()
			return nil, fmt.Errorf("tunnel task runtime is unavailable; refusing production startup")
		}
		if err := worker.ValidateTaskCapabilities(worker.TaskOwnerTunnel, capabilities); err != nil {
			foundation.cancel()
			c.database.Close()
			return nil, fmt.Errorf("validate tunnel task runtime: %w", err)
		}
		if err := tasks.ValidateTunnelRuntime(tasks.TunnelRuntimeFeatures{
			KubectlShell: cfg.KubectlShellEnabled, ControlPlaneSnapshots: cfg.ControlPlaneSnapshotsEnabled,
			CharlieProductBridge: c.managedCharlieBridge != nil, CRDOwnership: cfg.CRDEnabled,
		}, core, toolDrift, c.charlieTriggerRuntime, clusterTemplate, c.networkPolicyRuntime,
			c.meshRuntime, cloudCredential, clusterRegistry, project, c.clusterSnapshotRuntime,
			clusterDecommission, c.controlPlaneSnapshotRuntime, routed.deferredRuntime,
			c.kubectlSessionReapRuntime, c.securityIngestRuntime, crdOwnership); err != nil {
			foundation.cancel()
			c.database.Close()
			return nil, fmt.Errorf("validate tunnel task composition: %w", err)
		}
		logger.Info("validated tunnel task runtime", "task_counts", worker.TaskRegistryCounts())
	}
	return &runtimeTaskComposition{core: core, project: project}, nil
}
