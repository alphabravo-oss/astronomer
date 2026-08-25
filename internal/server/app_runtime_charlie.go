package server

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/charlie"
	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
)

func (c *productionComposition) composeCharlieLifecycles(cfg *config.Config, logger *slog.Logger, deps RouterDependencies) (*charlieLifecycleGroup, error) {
	leaseOwner := strings.TrimSpace(os.Getenv("HOSTNAME"))
	if leaseOwner == "" {
		leaseOwner = "astronomer-server-" + uuid.NewString()
	}
	var configuration *charlie.RuntimeLifecycle
	var runtime *charlie.RuntimeLifecycle
	var lifecycles *charlieLifecycleGroup
	if c.managedCharlieBridge != nil {
		mcpFactory := func(factoryCtx context.Context) (charlie.ActivationWork, error) {
			activation := charlie.EvaluateActivation(factoryCtx, c.charlieFeatures, c.queries)
			if !activation.Configurable || activation.State == charlie.ActivationEmergencyStop {
				return nil, fmt.Errorf("Charlie product configuration discovery is inactive")
			}
			clusterAdapter, err := charlie.NewClusterAgentCapabilityAdapter(c.queries)
			if err != nil {
				return nil, err
			}
			groups := []map[string]charlie.CapabilityExecutor{charlie.ClusterAgentCapabilityAdapters(clusterAdapter)}
			inspector := asynq.NewInspector(c.redisOpt)
			fail := func(err error) (charlie.ActivationWork, error) {
				_ = inspector.Close()
				return nil, err
			}
			queueAdapter, err := charlie.NewQueueCapabilityAdapter(inspector)
			if err != nil {
				return fail(err)
			}
			groups = append(groups, charlie.QueueCapabilityAdapters(queueAdapter))
			operationalAdapter, err := charlie.NewOperationalCapabilityAdapter(charlie.OperationalCapabilityConfig{
				Database: c.database, Queries: c.queries, Kubernetes: c.localK8s, Queue: inspector,
				Namespace: c.localNamespace, Release: c.localReleaseName, ChartVersion: c.localChartVersion,
				TLSCertFiles: []string{cfg.CharlieMCPTLSCertFile, cfg.CharlieBridgeTLSCertFile},
			})
			if err != nil {
				return fail(err)
			}
			groups = append(groups, charlie.OperationalCapabilityAdapters(operationalAdapter))
			pipelineAdapter, err := charlie.NewWorkPipelineCapabilityAdapter(c.queries)
			if err != nil {
				return fail(err)
			}
			groups = append(groups, charlie.WorkPipelineCapabilityAdapters(pipelineAdapter))
			deliveryAdapter, err := charlie.NewDeliveryCapabilityAdapter(
				c.queries, c.deliveryPlanningStore, c.deliveryRolloutController, c.deliveryDeploymentController,
			)
			if err != nil {
				return fail(err)
			}
			groups = append(groups, charlie.DeliveryCapabilityAdapters(deliveryAdapter))
			encryptionKeyCount, jwtKeyCount := 0, 0
			if c.encryptor != nil {
				encryptionKeyCount = c.encryptor.KeyCount()
			}
			if c.jwtManager != nil {
				jwtKeyCount = c.jwtManager.KeyCount()
			}
			runtimeAdapter, err := charlie.NewRuntimeCapabilityAdapter(charlie.RuntimeCapabilityConfig{
				Database: c.database.Pool(), Redis: c.runtimeRedisClient,
				EncryptionKeyCount: encryptionKeyCount, JWTKeyCount: jwtKeyCount,
				InsecureDevKeys: config.DevSentinelsInUse(cfg),
			})
			if err != nil {
				return fail(err)
			}
			groups = append(groups, charlie.RuntimeCapabilityAdapters(runtimeAdapter))
			adminAdapter, err := charlie.NewAdminVisibilityCapabilityAdapter(c.database.Pool())
			if err != nil {
				return fail(err)
			}
			groups = append(groups, charlie.AdminVisibilityCapabilityAdapters(adminAdapter))
			if c.localK8s != nil && c.localNamespace != "" {
				managementAdapter, managementErr := charlie.NewManagementKubernetesAdapter(c.localK8s, c.localNamespace, c.localReleaseName)
				if managementErr != nil {
					return fail(managementErr)
				}
				managementAdapter.SetMetricsClient(c.localMetrics)
				groups = append(groups, charlie.ManagementKubernetesCapabilityAdapters(managementAdapter))
			}
			baseExecutor, err := charlie.NewCatalogExecutor(charlie.MergeCapabilityAdapters(groups...))
			if err != nil {
				return fail(err)
			}
			healthAdapter, err := charlie.NewSystemHealthCapabilityAdapter(baseExecutor)
			if err != nil {
				return fail(err)
			}
			groups = append(groups, charlie.SystemHealthCapabilityAdapters(healthAdapter))
			executor, err := charlie.NewCatalogExecutor(charlie.MergeCapabilityAdapters(groups...))
			if err != nil {
				return fail(err)
			}
			visibility, err := charlie.NewKubernetesVisibilityExecutor(executor, c.queries)
			if err != nil {
				return fail(err)
			}
			safety, err := charlie.NewProductActionSafety(c.queries, c.maintenanceEvaluator)
			if err != nil {
				return fail(err)
			}
			findingStore, err := charlie.NewDBFindingStore(c.queries)
			if err != nil {
				return fail(err)
			}
			alertPlanner, err := charlie.NewFindingAlertPlanner(c.database.Pool())
			if err != nil {
				return fail(err)
			}
			findingRecorder, err := charlie.NewFindingService(findingStore, charlie.NewPolicyFindingPublisher(c.bus, alertPlanner))
			if err != nil {
				return fail(err)
			}
			mcp, err := charlie.NewMCPRuntime(charlie.MCPRuntimeConfig{
				Listener: charlie.MCPListenerConfig{
					Address: cfg.CharlieMCPListenAddress, Certificate: cfg.CharlieMCPTLSCertFile,
					PrivateKey: cfg.CharlieMCPTLSKeyFile, ClientCA: cfg.CharlieMCPClientCAFile,
				},
				ActionSigningKeyFile: cfg.CharlieMCPActionSigningKeyFile,
				LeaseOwner:           leaseOwner, ReceiptCipher: c.encryptor, WriteFence: c.charlieWriteFence,
				BridgeStatus: c.managedCharlieBridge, FindingRecorder: findingRecorder,
			}, c.charlieFeatures, c.queries, c.charlieBindings, safety, visibility, logger)
			if err != nil {
				return fail(err)
			}
			return &charlieMCPGeneration{mcp: mcp, inspector: inspector}, nil
		}
		workFactory := func(factoryCtx context.Context) (charlie.ActivationWork, error) {
			if !charlie.EvaluateActivation(factoryCtx, c.charlieFeatures, c.queries).Runnable {
				return nil, fmt.Errorf("Charlie product runtime is inactive")
			}
			auditor := charlie.NewDBLifecycleAuditor(c.queries)
			dispatcher, err := charlie.NewTriggerDispatcher(c.queries, c.managedCharlieBridge, charlie.NewEventTriggerLifecyclePublisher(c.bus), auditor, func() bool {
				return c.managedCharlieBridge.Active(context.Background())
			})
			if err != nil {
				return nil, err
			}
			dispatcher.SetPlatformInventory(c.charlieInventory)
			events, err := charlie.NewEventRuntime(c.bus, c.queries, func() bool {
				return c.managedCharlieBridge.Active(context.Background())
			})
			if err != nil {
				return nil, err
			}
			return &charlieRuntimeGeneration{events: events, dispatcher: dispatcher, triggers: c.charlieTriggerRuntime}, nil
		}
		control := func(controlCtx context.Context) {
			if c.charlieAdminService != nil {
				c.charlieAdminService.RunModeReconciler(controlCtx, 10*time.Second)
			}
		}
		var err error
		configuration, err = charlie.NewConfigurationRuntimeLifecycle(c.charlieFeatures, c.queries, mcpFactory, control, c.managedCharlieBridge.Close)
		if err != nil {
			c.database.Close()
			return nil, err
		}
		runtime, err = charlie.NewRuntimeLifecycle(c.charlieFeatures, c.queries, workFactory, nil, nil)
		if err != nil {
			c.database.Close()
			return nil, err
		}
		lifecycles = &charlieLifecycleGroup{configuration: configuration, runtime: runtime}
		c.managedCharlieBridge.SetActivationChanged(func(changeCtx context.Context) {
			if err := lifecycles.Activate(changeCtx); err != nil {
				charlie.LogOperationalFailure(changeCtx, logger, "runtime.activation_failed", "")
			}
		})
	}
	if deps.PlatformSettings != nil {
		lifecycle, err := charlie.NewFeatureLifecycle(c.database.Pool(), c.managedCharlieBridge, lifecycles, c.charlieWriteFence)
		if err != nil {
			charlie.LogOperationalFailure(context.Background(), logger, "runtime.feature_dependencies_incomplete", "")
		} else {
			deps.PlatformSettings.SetCharlieLifecycle(lifecycle)
		}
	}
	return lifecycles, nil
}
