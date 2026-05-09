package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"
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
	if err := agent2.ConnectAndServe(ctx, logger, cfg.ServerURL, cfg.ClusterID, cfg.AgentToken); err != nil && err != context.Canceled {
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
		tunnel.RegisterHandler(protocol.MsgK8sRequest, k8s.HandleRequest)
		// Streaming variant for long-lived k8s responses (Watch). Uses the
		// existing AdaptStreamingHandler pattern: handler emits frames via
		// sendFn and returns nil so the dispatcher doesn't expect a single
		// reply.
		tunnel.RegisterHandler(protocol.MsgK8sStreamRequest,
			agent.AdaptStreamingHandler(tunnel, k8s.HandleStreamRequest))

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

		// Health reporter (heartbeat + metrics tickers + JSON probes).
		health := agent.NewHealthReporter(client, logger, cfg.HeartbeatInterval, cfg.MetricsInterval)
		health.SetAgentVersion(version.Version)
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
			subscriber.Run(ctx)
		}()

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
