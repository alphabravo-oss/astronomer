package server

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/metadata"
	"k8s.io/client-go/rest"
	metricsv "k8s.io/metrics/pkg/client/clientset/versioned"

	"github.com/alphabravocompany/astronomer-go/internal/agent"
	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
	"github.com/alphabravocompany/astronomer-go/pkg/version"
)

// localAgentDialURL is the WebSocket URL the embedded local agent dials. The
// server listens on :8000 inside the pod (see cmd/server/main.go); pointing at
// 127.0.0.1 keeps the connection on the loopback interface and avoids any
// dependency on the in-cluster service mesh / DNS.
const localAgentDialURL = "ws://127.0.0.1:8000"

// localClusterName is the canonical name for the management cluster row,
// matching Rancher's convention. The user-visible UI surfaces this directly.
const localClusterName = "local"

// localRegistrationTokenTTL bounds how long the in-process registration token
// is valid for. The embedded agent uses it on every (re)connect, so it must
// outlive any plausible reconnect backoff window. 30 days is generous; the
// token never leaves this pod.
const localRegistrationTokenTTL = 30 * 24 * time.Hour

// EnsureLocalCluster idempotently creates (or fetches) the singleton local
// cluster row and refreshes its k8s-derived metadata fields (kubernetes
// version, node count, distribution, api server URL).
//
// Multiple server replicas calling this concurrently are safe: the partial
// unique index clusters_one_local guarantees at most one row, and the CTE in
// the EnsureLocalCluster query returns whichever row wins.
func EnsureLocalCluster(ctx context.Context, queries *sqlc.Queries, k8sClient *kubernetes.Clientset, restConfig *rest.Config) (*sqlc.Cluster, error) {
	if queries == nil {
		return nil, fmt.Errorf("queries is nil")
	}

	// Discover k8s metadata (best-effort: empty strings are acceptable on a
	// barely-running cluster). The server should still come up if Discovery
	// is slow or partial.
	var (
		gitVersion   string
		distribution string
		nodeCount    int32
		apiHost      string
	)
	if restConfig != nil {
		apiHost = restConfig.Host
	}
	if k8sClient != nil {
		if sv, err := k8sClient.Discovery().ServerVersion(); err == nil {
			gitVersion = sv.GitVersion
			if strings.Contains(strings.ToLower(gitVersion), "k3s") {
				distribution = "k3s"
			} else if strings.Contains(strings.ToLower(gitVersion), "rke2") {
				distribution = "rke2"
			} else {
				distribution = "kubernetes"
			}
		}
		if nodes, err := k8sClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{}); err == nil {
			nodeCount = int32(len(nodes.Items))
		}
	}

	row, err := queries.EnsureLocalCluster(ctx, sqlc.EnsureLocalClusterParams{
		Name:              localClusterName,
		DisplayName:       localClusterName,
		Description:       "Astronomer management cluster (auto-registered)",
		Status:            "pending",
		ApiServerUrl:      apiHost,
		Distribution:      distribution,
		KubernetesVersion: gitVersion,
		NodeCount:         nodeCount,
	})
	if err != nil {
		return nil, fmt.Errorf("ensure local cluster: %w", err)
	}

	// Mirror the EnsureLocalClusterRow into the canonical Cluster struct so
	// callers don't have to know about sqlc's per-query row types. The shapes
	// are identical because the query returns clusters.*.
	cluster := &sqlc.Cluster{
		ID:                row.ID,
		Name:              row.Name,
		DisplayName:       row.DisplayName,
		Description:       row.Description,
		Status:            row.Status,
		ApiServerUrl:      row.ApiServerUrl,
		CaCertificate:     row.CaCertificate,
		Environment:       row.Environment,
		Region:            row.Region,
		Provider:          row.Provider,
		Labels:            row.Labels,
		Annotations:       row.Annotations,
		Distribution:      row.Distribution,
		AgentVersion:      row.AgentVersion,
		LastHeartbeat:     row.LastHeartbeat,
		KubernetesVersion: row.KubernetesVersion,
		NodeCount:         row.NodeCount,
		CreatedByID:       row.CreatedByID,
		CreatedAt:         row.CreatedAt,
		UpdatedAt:         row.UpdatedAt,
		IsLocal:           row.IsLocal,
	}
	return cluster, nil
}

// StartLocalAgent spins up an embedded agent goroutine that connects to the
// server's own WebSocket tunnel endpoint. From the Hub's perspective this is
// indistinguishable from an externally-installed agent, so all existing
// k8s-proxy / exec / logs / RBAC paths "just work" without special-casing.
//
// The goroutine runs until ctx is cancelled. dial+reconnect logic is provided
// by agent.TunnelClient (jittered exponential backoff). If rest.InClusterConfig
// fails (running outside a cluster, e.g. tests or local dev), this function
// logs a warning and returns nil — the server still comes up, just without
// the local cluster's data plane.
func StartLocalAgent(ctx context.Context, logger *slog.Logger, queries *sqlc.Queries, clusterID uuid.UUID) error {
	if logger == nil {
		logger = slog.Default()
	}

	// Build an in-cluster k8s client. Outside a cluster (laptop dev) this will
	// fail; we degrade gracefully.
	restCfg, err := rest.InClusterConfig()
	if err != nil {
		logger.Warn("local agent disabled: not running in-cluster", "error", err)
		return nil
	}

	clientset, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return fmt.Errorf("create local agent clientset: %w", err)
	}

	// Issue a long-lived registration token in-process. The token never leaves
	// the pod, but it must be valid against the same hub validator so the
	// CONNECT handshake succeeds end-to-end.
	tokenStr, err := generateLocalAgentToken()
	if err != nil {
		return fmt.Errorf("generate local agent token: %w", err)
	}
	if _, err := queries.CreateClusterRegistrationToken(ctx, sqlc.CreateClusterRegistrationTokenParams{
		ClusterID: clusterID,
		TokenHash: auth.HashOpaqueToken(tokenStr),
		ExpiresAt: time.Now().Add(localRegistrationTokenTTL),
	}); err != nil {
		return fmt.Errorf("persist local agent registration token: %w", err)
	}

	cfg := &agent.AgentConfig{
		ServerURL:         localAgentDialURL,
		ClusterID:         clusterID.String(),
		AgentToken:        tokenStr,
		AgentID:           "local-agent-" + uuid.NewString(),
		CredentialSource:  agent.CredentialSourceEnvironment,
		ReconnectBackoff:  5,
		MaxReconnect:      300,
		HeartbeatInterval: 30,
		MetricsInterval:   60,
		HealthAddr:        "", // health probe server is owned by the main process
	}

	tunnelClient := agent.NewTunnelClient(cfg, logger.With("component", "local-agent"))

	// Wire the same handlers cmd/agent/main.go installs. We register them
	// against the same TunnelClient instance the local goroutine uses.
	k8sProxy, err := agent.NewK8sProxyWithConfig(restCfg, logger)
	if err != nil {
		return fmt.Errorf("create local k8s proxy: %w", err)
	}
	// Streaming variant for the embedded local agent —
	// large list responses (e.g. /apis/.../resources) get chunked instead
	// of pinned to a single 16 MiB WS frame.
	tunnelClient.RegisterHandler(protocol.MsgK8sRequest, agent.AdaptStreamingHandler(tunnelClient, k8sProxy.HandleRequestStreaming))

	execHandler := agent.NewExecHandler(clientset, restCfg, logger)
	tunnelClient.RegisterHandler(protocol.MsgExecStart, agent.AdaptStreamingHandler(tunnelClient, execHandler.HandleExecStart))
	tunnelClient.RegisterHandler(protocol.MsgExecInput, agent.AdaptVoidHandler(execHandler.HandleExecInput))
	tunnelClient.RegisterHandler(protocol.MsgExecResize, agent.AdaptVoidHandler(execHandler.HandleExecResize))
	tunnelClient.RegisterHandler(protocol.MsgExecEnd, agent.AdaptVoidHandler(func(msg *protocol.Message) error {
		execHandler.CloseSession(msg.StreamID)
		return nil
	}))

	logHandler := agent.NewLogHandler(clientset, logger)
	tunnelClient.RegisterHandler(protocol.MsgLogStart, agent.AdaptStreamingHandler(tunnelClient, logHandler.HandleLogStart))
	tunnelClient.RegisterHandler(protocol.MsgLogStop, agent.AdaptVoidHandler(logHandler.HandleLogStop))

	rbacSyncer := agent.NewRBACSyncer(clientset, logger)
	tunnelClient.RegisterHandler(protocol.MsgRBACSyncRequest, agent.AdaptStreamingHandler(tunnelClient, rbacSyncer.HandleSyncRequest))

	svcProxy := agent.NewServiceProxy(logger)
	tunnelClient.RegisterHandler(protocol.MsgServiceProxyRequest, svcProxy.HandleRequest)

	helm := agent.NewHelmHandler(logger)
	tunnelClient.RegisterHandler(protocol.MsgHelmInstall, helm.HandleInstall)
	tunnelClient.RegisterHandler(protocol.MsgHelmUpgrade, helm.HandleUpgrade)
	tunnelClient.RegisterHandler(protocol.MsgHelmUninstall, helm.HandleUninstall)
	tunnelClient.RegisterHandler(protocol.MsgHelmRollback, helm.HandleRollback)
	tunnelClient.RegisterHandler(protocol.MsgHelmStatus, helm.HandleStatus)
	tunnelClient.RegisterHandler(protocol.MsgHelmHistory, helm.HandleHistory)

	// Health reporter — drives the heartbeat / metrics tickers that update the
	// cluster row's last_heartbeat / agent_version / k8s_version columns.
	health := agent.NewHealthReporter(clientset, logger, cfg.HeartbeatInterval, cfg.MetricsInterval)
	health.SetAgentVersion(version.Version)
	health.SetClusterID(cfg.ClusterID)
	if mc, err := metricsv.NewForConfig(restCfg); err == nil {
		health.SetMetricsClient(mc)
	} else {
		logger.Debug("local agent metrics client unavailable", "error", err)
	}

	go func() {
		// Wait until the tunnel reports connected so the first heartbeat isn't
		// dropped on the floor by the send-buffer fast path and so the informer
		// subscriber only starts once outbound STATE_UPDATE sends can succeed.
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for !tunnelClient.IsConnected() {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
		subscriber := agent.NewStateSubscriber(clientset, tunnelClient, logger.With("component", "local-agent"))
		// P4.6 informer expansion: metadata-only informers (extra built-in
		// kinds, Helm release Secrets, discover-if-present CRDs).
		if mc, mErr := metadata.NewForConfig(restCfg); mErr == nil {
			subscriber.SetMetadataClient(mc)
		} else {
			logger.Warn("local agent: metadata client init failed; expanded informer set disabled", "error", mErr)
		}
		go subscriber.Run(ctx)
		health.SetConnected(true)
		health.Start(ctx, tunnelClient.Send)
	}()

	go func() {
		logger.Info("starting embedded local agent",
			"cluster_id", cfg.ClusterID,
			"agent_id", cfg.AgentID,
			"server_url", cfg.ServerURL,
		)
		// Outer retry loop: NewApp returns before httpServer.Serve is called,
		// so the very first dial will hit "connection refused". TunnelClient's
		// internal reconnect only starts AFTER one successful CONNECT. Wrap the
		// initial attempt in a backoff loop so we wait until the server is
		// actually listening, then hand off to TunnelClient's reconnect logic.
		backoff := 500 * time.Millisecond
		for {
			if ctx.Err() != nil {
				return
			}
			err := tunnelClient.Connect(ctx)
			if err == nil {
				logger.Info("local agent shut down cleanly")
				return
			}
			logger.Warn("local agent tunnel exited; retrying", "error", err, "backoff", backoff.String())
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			if backoff < 30*time.Second {
				backoff *= 2
			}
		}
	}()

	return nil
}

// generateLocalAgentToken returns a 32-byte cryptographically random URL-safe
// token suitable for the cluster_registration_tokens table.
func generateLocalAgentToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(b), nil
}
