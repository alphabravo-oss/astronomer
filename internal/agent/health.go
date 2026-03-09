package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	metricsv "k8s.io/metrics/pkg/client/clientset/versioned"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// HealthReporter sends periodic health data to the server.
type HealthReporter struct {
	client            *kubernetes.Clientset
	metricsClient     metricsv.Interface
	log               *slog.Logger
	heartbeatInterval time.Duration
	metricsInterval   time.Duration
	agentVersion      string

	// connected tracks whether the tunnel is up (for readiness probes).
	connected atomic.Bool
}

// NewHealthReporter creates a new HealthReporter.
func NewHealthReporter(client *kubernetes.Clientset, log *slog.Logger, heartbeatSec, metricsSec int) *HealthReporter {
	if heartbeatSec <= 0 {
		heartbeatSec = 30
	}
	if metricsSec <= 0 {
		metricsSec = 60
	}
	return &HealthReporter{
		client:            client,
		log:               log,
		heartbeatInterval: time.Duration(heartbeatSec) * time.Second,
		metricsInterval:   time.Duration(metricsSec) * time.Second,
	}
}

// SetMetricsClient sets an optional metrics client for CPU/memory reporting.
func (hr *HealthReporter) SetMetricsClient(mc metricsv.Interface) {
	hr.metricsClient = mc
}

// SetAgentVersion sets the agent version reported in heartbeats.
func (hr *HealthReporter) SetAgentVersion(v string) {
	hr.agentVersion = v
}

// SetConnected updates the tunnel connection status (used by readiness probe).
func (hr *HealthReporter) SetConnected(c bool) {
	hr.connected.Store(c)
}

// Start begins periodic health reporting. It blocks until the context is cancelled.
func (hr *HealthReporter) Start(ctx context.Context, sendFn func(*protocol.Message) error) {
	heartbeatTicker := time.NewTicker(hr.heartbeatInterval)
	defer heartbeatTicker.Stop()

	// Send an initial heartbeat immediately.
	hr.sendHeartbeat(ctx, sendFn)

	for {
		select {
		case <-ctx.Done():
			return
		case <-heartbeatTicker.C:
			hr.sendHeartbeat(ctx, sendFn)
		}
	}
}

func (hr *HealthReporter) sendHeartbeat(ctx context.Context, sendFn func(*protocol.Message) error) {
	hb, err := hr.collectHeartbeat(ctx)
	if err != nil {
		hr.log.Error("failed to collect heartbeat", "error", err)
		return
	}

	payload, err := json.Marshal(hb)
	if err != nil {
		hr.log.Error("failed to marshal heartbeat", "error", err)
		return
	}

	if err := sendFn(&protocol.Message{
		Type:    protocol.MsgHeartbeat,
		Payload: payload,
	}); err != nil {
		hr.log.Error("failed to send heartbeat", "error", err)
	}
}

// collectHeartbeat gathers cluster-level health data.
func (hr *HealthReporter) collectHeartbeat(ctx context.Context) (*protocol.HeartbeatPayload, error) {
	hb := &protocol.HeartbeatPayload{
		Timestamp:    time.Now().UTC().Format(time.RFC3339),
		AgentVersion: hr.agentVersion,
	}

	// K8s version.
	serverVersion, err := hr.client.Discovery().ServerVersion()
	if err != nil {
		return nil, fmt.Errorf("get server version: %w", err)
	}
	hb.KubernetesVersion = serverVersion.GitVersion

	// Node count and distribution detection.
	nodes, err := hr.client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list nodes: %w", err)
	}
	hb.NodeCount = len(nodes.Items)
	hb.Distribution = detectDistribution(nodes.Items)

	// Pod count (all namespaces).
	pods, err := hr.client.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list pods: %w", err)
	}
	hb.PodCount = len(pods.Items)

	// CPU/Memory from metrics API (best-effort).
	hr.collectMetrics(ctx, hb)

	return hb, nil
}

// collectMetrics attempts to collect CPU/Memory metrics from the metrics API.
func (hr *HealthReporter) collectMetrics(ctx context.Context, hb *protocol.HeartbeatPayload) {
	if hr.metricsClient == nil {
		return
	}

	nodeMetrics, err := hr.metricsClient.MetricsV1beta1().NodeMetricses().List(ctx, metav1.ListOptions{})
	if err != nil {
		hr.log.Debug("metrics API unavailable", "error", err)
		return
	}

	var totalCPUUsage, totalMemUsage int64
	for _, nm := range nodeMetrics.Items {
		totalCPUUsage += nm.Usage.Cpu().MilliValue()
		totalMemUsage += nm.Usage.Memory().Value()
	}

	// Get allocatable resources from nodes to calculate percentages.
	nodes, err := hr.client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return
	}

	var totalCPUCapacity, totalMemCapacity int64
	for _, node := range nodes.Items {
		totalCPUCapacity += node.Status.Allocatable.Cpu().MilliValue()
		totalMemCapacity += node.Status.Allocatable.Memory().Value()
	}

	if totalCPUCapacity > 0 {
		hb.CPUUsagePercent = float64(totalCPUUsage) / float64(totalCPUCapacity) * 100.0
	}
	if totalMemCapacity > 0 {
		hb.MemoryUsagePercent = float64(totalMemUsage) / float64(totalMemCapacity) * 100.0
	}
}

// detectDistribution inspects node labels to determine the K8s distribution.
func detectDistribution(nodes []corev1.Node) string {
	if len(nodes) == 0 {
		return "unknown"
	}

	// Check first node's labels and provider ID.
	node := nodes[0]
	labels := node.Labels
	providerID := node.Spec.ProviderID

	// EKS
	if _, ok := labels["eks.amazonaws.com/nodegroup"]; ok {
		return "eks"
	}
	if strings.HasPrefix(providerID, "aws://") {
		return "eks"
	}

	// GKE
	if _, ok := labels["cloud.google.com/gke-nodepool"]; ok {
		return "gke"
	}
	if strings.HasPrefix(providerID, "gce://") {
		return "gke"
	}

	// AKS
	if _, ok := labels["kubernetes.azure.com/cluster"]; ok {
		return "aks"
	}
	if strings.HasPrefix(providerID, "azure://") {
		return "aks"
	}

	// k3s
	if _, ok := labels["node.kubernetes.io/instance-type"]; ok {
		if strings.Contains(labels["node.kubernetes.io/instance-type"], "k3s") {
			return "k3s"
		}
	}
	for key := range labels {
		if strings.Contains(key, "k3s") {
			return "k3s"
		}
	}

	// kind
	if _, ok := labels["kubernetes.io/hostname"]; ok {
		if strings.Contains(labels["kubernetes.io/hostname"], "kind") {
			return "kind"
		}
	}

	return "unknown"
}

// ServeHealth runs a basic /healthz and /readyz HTTP server for K8s probes.
func (hr *HealthReporter) ServeHealth(ctx context.Context, addr string) {
	mux := hr.healthMux()

	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	hr.log.Info("starting health server", "addr", addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		hr.log.Error("health server error", "error", err)
	}
}

// healthMux returns the http.ServeMux with health endpoints, exposed for testing.
func (hr *HealthReporter) healthMux() *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		if hr.connected.Load() {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("not connected"))
		}
	})

	return mux
}
