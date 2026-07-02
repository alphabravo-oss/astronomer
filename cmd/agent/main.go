package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"k8s.io/client-go/dynamic"
	metricsv "k8s.io/metrics/pkg/client/clientset/versioned"

	"github.com/alphabravocompany/astronomer-go/internal/agent"
	"github.com/alphabravocompany/astronomer-go/internal/agent2"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
	"github.com/alphabravocompany/astronomer-go/pkg/version"
)

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

	connect2Cmd := &cobra.Command{
		Use:   "connect2",
		Short: "Connect using the remotedialer-based tunnel (migration target)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runConnect2(logger)
		},
	}

	rootCmd.AddCommand(connectCmd)
	rootCmd.AddCommand(connect2Cmd)

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

// runConnect2 is the entry point for the new tunnel. It uses the same env
// vars as `connect` (ASTRONOMER_SERVER_URL / ASTRONOMER_CLUSTER_ID /
// ASTRONOMER_AGENT_TOKEN) but does not need any of the per-feature handlers —
// remotedialer ferries dial requests directly to the agent's local network.
func runConnect2(logger *slog.Logger) error {
	cfg, err := agent.LoadAgentConfig()
	if err != nil {
		logger.Error("failed to load config", "error", err)
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

func runConnect(logger *slog.Logger) error {
	cfg, err := agent.LoadAgentConfig()
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

		// Shared guard so decommission can halt the pull reconcile loop before
		// tearing down the agent (otherwise Phase-2 self-apply re-creates it).
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
			// Wire the tunnel as the connection watcher so the subscriber's
			// replay loop re-emits cached informer state on every WS reconnect
			// (L12 defense-in-depth — mirrors the MirrorSubscriber wiring below).
			subscriber.SetConnectionWatcher(tunnel)
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
			health.Start(ctx, tunnel.Send)
		}()

		// Fleet-style PULL reconcile loop (gated OFF by default). When the
		// PullReconcileEnabled flag is set, the agent becomes the LOCAL applier
		// of its own desired state: it pulls the rendered manifest set from the
		// management plane over the existing tunnel and server-side-applies +
		// prunes it, bounded strictly to the astronomer-* owned namespaces. When
		// the flag is off, nothing here runs and v0.1.0 behavior is unchanged.
		if cfg.PullReconcileEnabled {
			if reconciler, rErr := agent.NewReconcileHandler(restConfig, cfg.ClusterID, logger); rErr != nil {
				logger.Warn("pull reconcile: handler init failed; loop disabled", "error", rErr)
			} else {
				reconciler.SetPauseGuard(pauseGuard)
				// The response handler routes server replies (and unsolicited
				// pushes) into the reconcile loop.
				tunnel.RegisterHandler(protocol.MsgDesiredStateResponse, reconciler.HandleDesiredStateResponse)
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
					interval := time.Duration(cfg.PullReconcileInterval) * time.Second
					reconciler.Run(ctx, interval, tunnel.Send)
				}()
			}
		}

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
