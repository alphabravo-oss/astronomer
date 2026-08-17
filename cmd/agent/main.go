package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/metadata"
	metricsv "k8s.io/metrics/pkg/client/clientset/versioned"

	"github.com/alphabravocompany/astronomer-go/internal/agent"
	agentdelivery "github.com/alphabravocompany/astronomer-go/internal/agent/delivery"
	"github.com/alphabravocompany/astronomer-go/internal/agent2"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
	"github.com/alphabravocompany/astronomer-go/pkg/version"
)

// upgradeReportInterval is how often the agent re-reads its own Deployment for
// a watchdog verdict it has not yet delivered. One named Get per minute against
// the local apiserver, and only until the verdict is reported and cleared.
const upgradeReportInterval = time.Minute

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	rootCmd := &cobra.Command{
		Use:     "astronomer-agent",
		Short:   "Astronomer agent for connecting Kubernetes clusters",
		Version: version.Version,
	}

	connectCmd := &cobra.Command{
		Use:   "connect",
		Short: "Connect this agent to the Astronomer server",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runConnect(logger)
		},
	}

	// connect2 is experimental and supports already-adopted durable identity
	// only. It has no CONNECT_ACK credential handoff, so bootstrap adoption and
	// rotation remain on the deployed `connect` path.
	connect2Cmd := &cobra.Command{
		Use:   "connect2",
		Short: "Experimental remotedialer tunnel for already-adopted agents",
		Long:  "Experimental existing-durable-identity-only path. It cannot adopt bootstrap credentials or receive durable-token rotations; use the deployed connect command for those workflows.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runConnect2(logger)
		},
	}

	// upgrade-watchdog is the self-upgrade safety net. It runs as a short-lived
	// Job created by the agent BEFORE the agent patches its own Deployment: with
	// strategy Recreate the patching process is terminated by its own rollout,
	// so verification and rollback must live outside the pod. See
	// internal/agent/upgrade_watchdog.go.
	upgradeWatchdogCmd := &cobra.Command{
		Use:    "upgrade-watchdog",
		Short:  "Verify an in-flight agent self-upgrade and roll it back if it never becomes healthy",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return agent.RunUpgradeWatchdogFromEnv(cmd.Context(), logger)
		},
	}

	rootCmd.AddCommand(connectCmd)
	rootCmd.AddCommand(connect2Cmd)
	rootCmd.AddCommand(upgradeWatchdogCmd)

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

// runConnect2 is the experimental remotedialer tunnel. It deliberately rejects
// bootstrap/legacy/environment sources because remotedialer has no CONNECT_ACK
// channel for durable identity handoff or rotation.
func runConnect2(logger *slog.Logger) error {
	cfg, err := agent.LoadAgentConfigWithLogger(logger)
	if err != nil {
		logger.Error("failed to load config", "error", err)
		return err
	}
	if err := validateConnect2CredentialSource(cfg.CredentialSource); err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		logger.Info("received signal, shutting down", "signal", sig)
		cancel()
	}()

	logger.Info("starting agent (remotedialer)",
		"server_url", cfg.ServerURL,
		"cluster_id", cfg.ClusterID,
	)
	if err := agent2.ConnectAndServe(ctx, logger, cfg.ServerURL, cfg.ClusterID, cfg.AgentToken, cfg.CACert, cfg.CAChecksum); err != nil && err != context.Canceled {
		logger.Error("agent2 exited with error", "error", err)
		return err
	}
	return nil
}

func validateConnect2CredentialSource(source string) error {
	if source != agent.CredentialSourceIdentity {
		return fmt.Errorf("connect2 requires credential_source=%s; bootstrap, legacy, and environment credentials must use connect", agent.CredentialSourceIdentity)
	}
	return nil
}

func runConnect(logger *slog.Logger) error {
	cfg, err := agent.LoadAgentConfigWithLogger(logger)
	if err != nil {
		logger.Error("failed to load config", "error", err)
		return err
	}

	tunnel := agent.NewTunnelClient(cfg, logger)

	// K8s proxy (also exposes the shared clientset and rest.Config used by
	// exec/logs/rbac/health).
	k8s, err := agent.NewK8sProxy(logger)
	if err != nil {
		logger.Warn("k8s proxy unavailable (not running in cluster?)", "error", err)
	} else {
		// Streaming variant chunks large response
		// bodies through K8sStreamFrame instead of one giant
		// K8sResponse that hits the 16 MiB WS frame cap. Small
		// responses still travel as a single K8sResponse — the
		// streaming handler decides per-call.
		tunnel.RegisterHandler(protocol.MsgK8sRequest, agent.AdaptStreamingHandler(tunnel, k8s.HandleRequestStreaming))
		// Streaming variant for long-lived k8s responses (Watch). Uses the
		// existing AdaptStreamingHandler pattern: handler emits frames via
		// sendFn and returns nil so the dispatcher doesn't expect a single
		// reply.
		tunnel.RegisterHandler(protocol.MsgK8sStreamRequest,
			agent.AdaptStreamingHandler(tunnel, k8s.HandleStreamRequest))
		// Per-stream cancellation: the server sends MsgK8sStreamStop when a
		// watch/passthrough-stream client disconnects; without this handler the
		// agent leaks the kube-apiserver watch + goroutine per abandoned stream.
		tunnel.RegisterHandler(protocol.MsgK8sStreamStop, agent.AdaptVoidHandler(k8s.HandleStreamStop))

		client := k8s.Client()
		restConfig := k8s.RESTConfig()

		// Exec: start (streaming), input (write), resize (signal).
		execHandler := agent.NewExecHandler(client, restConfig, logger)
		tunnel.RegisterHandler(protocol.MsgExecStart,
			agent.AdaptStreamingHandler(tunnel, execHandler.HandleExecStart))
		tunnel.RegisterHandler(protocol.MsgExecInput,
			agent.AdaptVoidHandler(execHandler.HandleExecInput))
		tunnel.RegisterHandler(protocol.MsgExecResize,
			agent.AdaptVoidHandler(execHandler.HandleExecResize))
		// EXEC_END from the server is treated as a session terminator — close
		// the local session if we know about it.
		tunnel.RegisterHandler(protocol.MsgExecEnd, agent.AdaptVoidHandler(func(msg *protocol.Message) error {
			execHandler.CloseSession(msg.StreamID)
			return nil
		}))

		// Logs: start (streaming), stop terminates an active follow early.
		logHandler := agent.NewLogHandler(client, logger)
		tunnel.RegisterHandler(protocol.MsgLogStart,
			agent.AdaptStreamingHandler(tunnel, logHandler.HandleLogStart))
		tunnel.RegisterHandler(protocol.MsgLogStop,
			agent.AdaptVoidHandler(logHandler.HandleLogStop))

		// RBAC sync.
		rbac := agent.NewRBACSyncer(client, logger)
		tunnel.RegisterHandler(protocol.MsgRBACSyncRequest,
			agent.AdaptStreamingHandler(tunnel, rbac.HandleSyncRequest))

		// Service proxy.
		svcProxy := agent.NewServiceProxy(logger)
		tunnel.RegisterHandler(protocol.MsgServiceProxyRequest, svcProxy.HandleRequest)

		// Shared guard stops protocol-v2 delivery before decommission removes the
		// managed footprint and schedules this agent Deployment for deletion.
		pauseGuard := &atomic.Bool{}

		// Cluster decommission. Receives MsgDecommission from the server,
		// uninstalls our managed-side resources (Fluent Bit / logging
		// namespace, labeled Velero CRs) and finally schedules the agent's
		// own Deployment for deletion AFTER the ACK is queued for writing.
		// When the dynamic client can't be constructed (unlikely — same
		// rest.Config as the typed clientset above), log and continue:
		// the operator can still kubectl-delete manually.
		if decomm, err := agent.NewDecommissionHandler(client, restConfig, logger); err != nil {
			logger.Warn("decommission handler unavailable", "error", err)
		} else {
			decomm.SetPauseGuard(pauseGuard)
			tunnel.RegisterHandler(protocol.MsgDecommission, decomm.HandleDecommission)
		}

		selfUpgrade := agent.NewSelfUpgradeHandler(client, logger)
		selfUpgrade.SetUpgradePolicy(agent.UpgradePolicy{
			AllowedRepository: cfg.AgentImageRepository,
			AllowMutableTag:   cfg.AgentAllowMutableTag,
			RolloutTimeout:    time.Duration(cfg.AgentUpgradeRolloutTimeout) * time.Second,
		})
		tunnel.RegisterHandler(protocol.MsgAgentUpgrade, selfUpgrade.HandleUpgrade)

		// Health reporter (heartbeat + metrics tickers + JSON probes).
		health := agent.NewHealthReporter(client, logger, cfg.HeartbeatInterval, cfg.MetricsInterval)
		health.SetAgentVersion(version.Version)
		health.SetAgentBuildSHA(version.GitCommit)
		health.SetPrivilegeProfile(cfg.PrivilegeProfile)
		health.SetClusterID(cfg.ClusterID)
		if mc, err := metricsv.NewForConfig(restConfig); err == nil {
			health.SetMetricsClient(mc)
		} else {
			logger.Warn("metrics client unavailable", "error", err)
		}

		// Set up graceful shutdown.
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		go func() {
			sig := <-sigCh
			logger.Info("received signal, shutting down", "signal", sig)
			cancel()
		}()

		// Health server (probes for k8s).
		go health.ServeHealth(ctx, cfg.HealthAddr)

		// Live state subscriber: SharedInformerFactory fan-out for resource
		// CRUD. Wait for tunnel readiness in its own goroutine; on RBAC
		// failure (insufficient list/watch on the SA) the subscriber logs and
		// continues — the dashboard remains polling-only without crashing.
		go func() {
			pollTicker := time.NewTicker(250 * time.Millisecond)
			defer pollTicker.Stop()
			for !tunnel.IsConnected() {
				select {
				case <-ctx.Done():
					return
				case <-pollTicker.C:
				}
			}
			subscriber := agent.NewStateSubscriber(client, tunnel, logger)
			subscriber.SetWatchSecrets(agent.ProfileAllowsSecrets(cfg.PrivilegeProfile))
			// P4.6 informer expansion: metadata-only informers (extra
			// built-in kinds, Helm release Secrets, discover-if-present
			// CRDs). Failure to build the client just skips the expansion.
			if mc, mErr := metadata.NewForConfig(restConfig); mErr == nil {
				subscriber.SetMetadataClient(mc)
			} else {
				logger.Warn("state subscriber: metadata client init failed; expanded informer set disabled", "error", mErr)
			}
			// Wire the tunnel as the connection watcher so the subscriber's
			// replay loop re-emits cached informer state on every WS reconnect
			// (L12 defense-in-depth — mirrors the MirrorSubscriber wiring below).
			subscriber.SetConnectionWatcher(tunnel)
			// Serve the heartbeat/metrics node + pod inventory from these
			// informer caches instead of re-listing the whole cluster from the
			// apiserver every 30s. Set before Run: until the caches report
			// synced the reporter transparently uses its paged fallback.
			health.SetInventorySource(subscriber)
			subscriber.Run(ctx)
		}()

		// CRD-mirror subscriber (sprint 069 + 062): IngressClass /
		// GatewayClass / NetworkPolicy / ResourceQuota / LimitRange
		// from the typed informer set, plus trivy-operator
		// VulnerabilityReport from the dynamic informer. Runs in its
		// own goroutine so the agent's hot path (tunnel reads, k8s
		// proxy, exec/logs) doesn't share its cache-sync stall budget.
		// The subscriber logs + skips kinds whose RBAC or CRD isn't
		// present, so a cluster without trivy-operator still mirrors
		// the other four GVKs.
		go func() {
			pollTicker := time.NewTicker(250 * time.Millisecond)
			defer pollTicker.Stop()
			for !tunnel.IsConnected() {
				select {
				case <-ctx.Done():
					return
				case <-pollTicker.C:
				}
			}
			var dyn dynamic.Interface
			if dc, dErr := dynamic.NewForConfig(restConfig); dErr == nil {
				dyn = dc
			} else {
				logger.Warn("mirror subscriber: dynamic client init failed; GatewayClass + VulnerabilityReport mirror disabled", "error", dErr)
			}
			mirror := agent.NewMirrorSubscriber(client, dyn, tunnel, logger)
			// Wire the tunnel as the connection watcher so the
			// subscriber's replay loop re-emits cached mirror items on
			// every WS reconnect (closes the "events lost during
			// bootstrap" hole).
			mirror.SetConnectionWatcher(tunnel)
			mirror.Run(ctx)
		}()

		// kube-apiserver audit-log forwarder (opt-in; disabled by default).
		// Requires a cluster-admin prerequisite: the apiserver must be started
		// with --audit-policy-file + --audit-log-path and that path hostPath-
		// mounted into this pod. See docs/agent-apiserver-audit.md. The delivery
		// path is selected by AUDIT_DELIVERY: tunnel (default; reuses the
		// authenticated WS tunnel so no second credential is needed), http
		// (direct POST with the scoped ingest token from CONNECT_ACK), or stub
		// (drops batches, logging only).
		if cfg.AuditEnabled {
			sender := agent.SelectAuditSender(cfg, tunnel, tunnel.AuditIngestToken, logger)
			if tailer, terr := agent.NewAuditTailer(cfg, sender, logger); terr != nil {
				logger.Warn("apiserver-audit: tailer disabled", "error", terr)
			} else {
				go tailer.Run(ctx)
			}
		}

		// Deliver the verdict the upgrade watchdog recorded on our own
		// Deployment. If this process is running because the watchdog rolled a
		// bad image back, the reason is durable in the cluster and the control
		// plane still believes the operation is in flight — which is exactly
		// why the rollback itself never depended on the control plane being
		// reachable. Reporting it does need the tunnel.
		//
		// Re-checked periodically rather than once at first connect: the
		// watchdog writes its `succeeded` verdict only after the replacement
		// agent is Available, i.e. after THIS process already connected, so a
		// one-shot report would systematically miss the success edge. The check
		// is a single named Get on our own Deployment and the annotation is
		// cleared as soon as a report is delivered, so the steady state is one
		// cheap read per interval — an order of magnitude less traffic than the
		// heartbeat, and well inside the server's stuck-operation deadline.
		go func() {
			ticker := time.NewTicker(upgradeReportInterval)
			defer ticker.Stop()
			for {
				if tunnel.IsConnected() {
					if err := agent.ReportPendingUpgradeOutcome(ctx, client, logger, cfg.ClusterID,
						agent.DefaultAgentNamespace, agent.DefaultAgentDeploymentName, tunnel.SendFunc(ctx)); err != nil {
						logger.Warn("could not report pending agent upgrade outcome", "error", err)
					}
				}
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
				}
			}
		}()

		// M4: drive readiness from EVERY tunnel transition (not a one-shot latch),
		// so /readyz flips to NotReady when the tunnel drops and back on reconnect.
		tunnel.SetConnectionListener(health.SetConnected)
		// Heartbeat + metrics tickers. Wait until the tunnel is connected so
		// frames don't immediately get dropped.
		go func() {
			pollTicker := time.NewTicker(250 * time.Millisecond)
			defer pollTicker.Stop()
			for !tunnel.IsConnected() {
				select {
				case <-ctx.Done():
					return
				case <-pollTicker.C:
				}
			}
			health.SetConnected(true)
			health.Start(ctx, tunnel.SendFunc(ctx))
		}()

		// Delivery protocol v2 is the single workload reconciliation path. The
		// agent validates a complete typed snapshot before applying only the
		// allowlisted Flux/RBAC/Secret graph; Flux then continues convergence if
		// this tunnel or the management plane is unavailable.
		deliveryDynamic, err := dynamic.NewForConfig(restConfig)
		if err != nil {
			return fmt.Errorf("initialize delivery dynamic client: %w", err)
		}
		deliveryExecutor, err := agentdelivery.NewExecutor(deliveryDynamic)
		if err != nil {
			return fmt.Errorf("initialize delivery executor: %w", err)
		}
		deliveryStore, err := agentdelivery.NewKubernetesCheckpointStore(client, agent.DefaultAgentNamespace)
		if err != nil {
			return fmt.Errorf("initialize delivery checkpoint: %w", err)
		}
		allowPlatformScope := cfg.PrivilegeProfile == "admin"
		deliveryProbe, err := agentdelivery.NewClusterProbe(client, client.Discovery(), allowPlatformScope)
		if err != nil {
			return fmt.Errorf("initialize delivery capability probe: %w", err)
		}
		deliveryRuntime, err := agentdelivery.NewRuntime(agentdelivery.RuntimeConfig{
			ClusterID:        cfg.ClusterID,
			AgentVersion:     version.Version,
			ValidationPolicy: agentdelivery.ValidationPolicy{AllowPlatformScope: allowPlatformScope},
			Connected:        tunnel.IsConnected,
			Logger:           logger,
		}, deliveryExecutor, deliveryStore, deliveryProbe)
		if err != nil {
			return fmt.Errorf("initialize delivery runtime: %w", err)
		}
		systemManager, err := agentdelivery.NewSystemManager(deliveryDynamic, client, agentdelivery.SystemManagerConfig{
			CurrentAgentVersion: version.Version,
			AgentNamespace:      agent.DefaultAgentNamespace,
			AgentDeployment:     "astronomer-agent",
			TrustPolicy: agentdelivery.SystemTrustPolicy{
				OIDCIdentities: []protocol.DeliveryOIDCIdentity{{
					Issuer:  cfg.SystemOIDCIssuer,
					Subject: cfg.SystemOIDCIdentity,
				}},
				AgentImageRepositories: []string{cfg.AgentImageRepository},
			},
		})
		if err != nil {
			return fmt.Errorf("initialize delivery system manager: %w", err)
		}
		deliveryRuntime.SetSystemManager(systemManager)
		deliveryRuntime.SetPauseGuard(pauseGuard)
		tunnel.RegisterHandler(protocol.MsgDeliveryStateResponse, deliveryRuntime.HandleStateResponse)
		tunnel.RegisterHandler(protocol.MsgDeliveryReconcile, deliveryRuntime.HandleReconcile)
		go func() {
			if err := deliveryRuntime.Run(ctx, tunnel.SendFunc(ctx)); err != nil && ctx.Err() == nil {
				logger.Error("delivery runtime stopped", "error", err)
				cancel()
			}
		}()

		return runHelmAndConnect(ctx, tunnel, logger, cfg)
	}

	// k8s proxy was unavailable: fall back to helm-only registration so the
	// agent can still serve helm requests off-cluster (testing scenario).
	registerHelm(tunnel, logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		logger.Info("received signal, shutting down", "signal", sig)
		cancel()
	}()

	logger.Info("starting agent",
		"server_url", cfg.ServerURL,
		"cluster_id", cfg.ClusterID,
		"agent_id", cfg.AgentID,
	)

	if err := tunnel.Connect(ctx); err != nil {
		logger.Error("tunnel connection error", "error", err)
		return err
	}
	return tunnel.Close()
}

func registerHelm(tunnel *agent.TunnelClient, _ *slog.Logger) {
	helm := agent.NewHelmHandler(slog.Default())
	tunnel.RegisterHandler(protocol.MsgHelmInstall, helm.HandleInstall)
	tunnel.RegisterHandler(protocol.MsgHelmUpgrade, helm.HandleUpgrade)
	tunnel.RegisterHandler(protocol.MsgHelmUninstall, helm.HandleUninstall)
	tunnel.RegisterHandler(protocol.MsgHelmRollback, helm.HandleRollback)
	tunnel.RegisterHandler(protocol.MsgHelmStatus, helm.HandleStatus)
	tunnel.RegisterHandler(protocol.MsgHelmHistory, helm.HandleHistory)
}

func runHelmAndConnect(ctx context.Context, tunnel *agent.TunnelClient, logger *slog.Logger, cfg *agent.AgentConfig) error {
	registerHelm(tunnel, logger)

	logger.Info("starting agent",
		"server_url", cfg.ServerURL,
		"cluster_id", cfg.ClusterID,
		"agent_id", cfg.AgentID,
	)

	if err := tunnel.Connect(ctx); err != nil {
		logger.Error("tunnel connection error", "error", err)
		return err
	}
	return tunnel.Close()
}
