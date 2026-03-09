package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/alphabravocompany/astronomer-go/internal/agent"
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

	rootCmd.AddCommand(connectCmd)

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func runConnect(logger *slog.Logger) error {
	cfg, err := agent.LoadAgentConfig()
	if err != nil {
		logger.Error("failed to load config", "error", err)
		return err
	}

	tunnel := agent.NewTunnelClient(cfg, logger)

	// Register K8s proxy handler.
	k8s, err := agent.NewK8sProxy(logger)
	if err != nil {
		logger.Warn("k8s proxy unavailable (not running in cluster?)", "error", err)
	} else {
		tunnel.RegisterHandler(protocol.MsgK8sRequest, k8s.HandleRequest)
	}

	// Register Helm handlers.
	helm := agent.NewHelmHandler(logger)
	tunnel.RegisterHandler(protocol.MsgHelmInstall, helm.HandleInstall)
	tunnel.RegisterHandler(protocol.MsgHelmUpgrade, helm.HandleUpgrade)
	tunnel.RegisterHandler(protocol.MsgHelmUninstall, helm.HandleUninstall)
	tunnel.RegisterHandler(protocol.MsgHelmRollback, helm.HandleRollback)
	tunnel.RegisterHandler(protocol.MsgHelmStatus, helm.HandleStatus)

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
