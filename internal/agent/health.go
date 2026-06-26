package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	metricsv "k8s.io/metrics/pkg/client/clientset/versioned"

	agenttemplate "github.com/alphabravocompany/astronomer-go/deploy/agent"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
	"github.com/alphabravocompany/astronomer-go/pkg/version"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// HealthReporter sends periodic health data to the server.
type HealthReporter struct {
	client            *kubernetes.Clientset
	metricsClient     metricsv.Interface
	log               *slog.Logger
	heartbeatInterval time.Duration
	metricsInterval   time.Duration
	agentVersion      string
	agentBuildSHA     string
	privilegeProfile  string
	enabledFeatures   []string
	deniedFeatures    []string
	clusterID         string
	startedAt         time.Time

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
		startedAt:         time.Now(),
	}
}

// SetClusterID sets the cluster ID reported by the /healthz JSON endpoint.
func (hr *HealthReporter) SetClusterID(id string) {
	hr.clusterID = id
}

// SetMetricsClient sets an optional metrics client for CPU/memory reporting.
func (hr *HealthReporter) SetMetricsClient(mc metricsv.Interface) {
	hr.metricsClient = mc
}

// SetAgentVersion sets the agent version reported in heartbeats.
func (hr *HealthReporter) SetAgentVersion(v string) {
	hr.agentVersion = v
}

// SetAgentBuildSHA sets the build commit reported in heartbeats.
func (hr *HealthReporter) SetAgentBuildSHA(v string) {
	hr.agentBuildSHA = v
}

// SetPrivilegeProfile sets the installed RBAC/capability profile reported in heartbeats.
func (hr *HealthReporter) SetPrivilegeProfile(profile string) {
	hr.privilegeProfile = normalizeAgentPrivilegeProfile(profile)
	hr.enabledFeatures, hr.deniedFeatures = capabilityFeaturesForProfile(hr.privilegeProfile)
}

// SetConnected updates the tunnel connection status (used by readiness probe).
func (hr *HealthReporter) SetConnected(c bool) {
	hr.connected.Store(c)
}

// Start begins periodic health reporting. It blocks until the context is
// cancelled. Two independent tickers fire: HEARTBEAT (lightweight liveness +
// inventory) and METRICS (detailed cluster utilization).
func (hr *HealthReporter) Start(ctx context.Context, sendFn func(*protocol.Message) error) {
	heartbeatTicker := time.NewTicker(hr.heartbeatInterval)
	defer heartbeatTicker.Stop()
	metricsTicker := time.NewTicker(hr.metricsInterval)
	defer metricsTicker.Stop()

	// Send an initial heartbeat immediately so the server registers liveness
	// without waiting one full interval.
	hr.sendHeartbeat(ctx, sendFn)
	hr.sendMetrics(ctx, sendFn)

	for {
		select {
		case <-ctx.Done():
			return
		case <-heartbeatTicker.C:
			hr.sendHeartbeat(ctx, sendFn)
		case <-metricsTicker.C:
			hr.sendMetrics(ctx, sendFn)
		}
	}
}

// sendMetrics gathers detailed cluster metrics and emits a METRICS message.
func (hr *HealthReporter) sendMetrics(ctx context.Context, sendFn func(*protocol.Message) error) {
	m, err := hr.collectMetricsPayload(ctx)
	if err != nil {
		hr.log.Error("failed to collect metrics", "error", err)
		return
	}
	payload, err := json.Marshal(m)
	if err != nil {
		hr.log.Error("failed to marshal metrics", "error", err)
		return
	}
	if err := sendFn(&protocol.Message{
		Type:      protocol.MsgMetrics,
		Timestamp: time.Now().UTC(),
		Payload:   payload,
	}); err != nil {
		hr.log.Error("failed to send metrics", "error", err)
	}
}

// collectMetricsPayload assembles a MetricsPayload, best-effort. If the
// metrics.k8s.io API is unreachable the payload still ships with
// MetricsAvailable=false so observability tools can flag the gap.
func (hr *HealthReporter) collectMetricsPayload(ctx context.Context) (*protocol.MetricsPayload, error) {
	out := &protocol.MetricsPayload{
		Timestamp:        time.Now().UTC().Format(time.RFC3339),
		MetricsAvailable: true,
	}

	nodes, err := hr.client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list nodes: %w", err)
	}
	out.ClusterNodeCount = len(nodes.Items)

	pods, err := hr.client.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list pods: %w", err)
	}
	out.ClusterPodCount = len(pods.Items)

	// Per-namespace pod counts.
	nsCounts := make(map[string]int)
	for _, p := range pods.Items {
		nsCounts[p.Namespace]++
	}
	out.Namespaces = make([]protocol.NamespaceMetrics, 0, len(nsCounts))

	if hr.metricsClient == nil {
		out.MetricsAvailable = false
		for ns, c := range nsCounts {
			out.Namespaces = append(out.Namespaces, protocol.NamespaceMetrics{Name: ns, PodCount: c})
		}
		return out, nil
	}

	nodeMetrics, err := hr.metricsClient.MetricsV1beta1().NodeMetricses().List(ctx, metav1.ListOptions{})
	if err != nil {
		out.MetricsAvailable = false
		for ns, c := range nsCounts {
			out.Namespaces = append(out.Namespaces, protocol.NamespaceMetrics{Name: ns, PodCount: c})
		}
		return out, nil
	}

	// Map node name -> capacity for per-node percentage.
	capByNode := make(map[string]struct{ cpu, mem int64 }, len(nodes.Items))
	var totalCPUCap, totalMemCap int64
	for _, n := range nodes.Items {
		cpu := n.Status.Allocatable.Cpu().MilliValue()
		mem := n.Status.Allocatable.Memory().Value()
		capByNode[n.Name] = struct{ cpu, mem int64 }{cpu, mem}
		totalCPUCap += cpu
		totalMemCap += mem
	}

	var totalCPUUse, totalMemUse int64
	for _, nm := range nodeMetrics.Items {
		cpuUse := nm.Usage.Cpu().MilliValue()
		memUse := nm.Usage.Memory().Value()
		totalCPUUse += cpuUse
		totalMemUse += memUse

		entry := protocol.NodeMetrics{
			Name:              nm.Name,
			CPUUsageMillicore: cpuUse,
			MemoryUsageBytes:  memUse,
		}
		if cap, ok := capByNode[nm.Name]; ok {
			entry.CPUCapacityMilli = cap.cpu
			entry.MemoryCapacity = cap.mem
			if cap.cpu > 0 {
				entry.CPUPercent = float64(cpuUse) / float64(cap.cpu) * 100.0
			}
			if cap.mem > 0 {
				entry.MemoryPercent = float64(memUse) / float64(cap.mem) * 100.0
			}
		}
		out.Nodes = append(out.Nodes, entry)
	}
	if totalCPUCap > 0 {
		out.ClusterCPUUsage = float64(totalCPUUse) / float64(totalCPUCap) * 100.0
	}
	if totalMemCap > 0 {
		out.ClusterMemoryUsage = float64(totalMemUse) / float64(totalMemCap) * 100.0
	}

	// Try per-namespace pod metrics for richer detail; fall back to counts.
	podMetrics, err := hr.metricsClient.MetricsV1beta1().PodMetricses("").List(ctx, metav1.ListOptions{})
	if err == nil {
		nsAgg := make(map[string]*protocol.NamespaceMetrics, len(nsCounts))
		for ns, c := range nsCounts {
			nsAgg[ns] = &protocol.NamespaceMetrics{Name: ns, PodCount: c}
		}
		for _, pm := range podMetrics.Items {
			entry, ok := nsAgg[pm.Namespace]
			if !ok {
				entry = &protocol.NamespaceMetrics{Name: pm.Namespace}
				nsAgg[pm.Namespace] = entry
			}
			for _, c := range pm.Containers {
				entry.CPUUsageMilli += c.Usage.Cpu().MilliValue()
				entry.MemoryUsageBytes += c.Usage.Memory().Value()
			}
		}
		for _, e := range nsAgg {
			out.Namespaces = append(out.Namespaces, *e)
		}
	} else {
		for ns, c := range nsCounts {
			out.Namespaces = append(out.Namespaces, protocol.NamespaceMetrics{Name: ns, PodCount: c})
		}
	}

	return out, nil
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
		SchemaVersion:          protocol.HeartbeatSchemaVersion,
		Timestamp:              time.Now().UTC().Format(time.RFC3339),
		AgentVersion:           hr.agentVersion,
		AgentBuildSHA:          defaultAgentValue(hr.agentBuildSHA, version.GitCommit),
		PrivilegeProfile:       defaultAgentValue(hr.privilegeProfile, "admin"),
		EnabledFeatures:        append([]string{}, hr.enabledFeatures...),
		DeniedFeatures:         append([]string{}, hr.deniedFeatures...),
		LastSuccessfulAction:   "heartbeat.collect",
		LastSuccessfulActionAt: time.Now().UTC().Format(time.RFC3339),
	}
	if len(hb.EnabledFeatures) == 0 && len(hb.DeniedFeatures) == 0 {
		hb.EnabledFeatures, hb.DeniedFeatures = capabilityFeaturesForProfile(hb.PrivilegeProfile)
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
	hb.AvailableAPIs = hr.collectAvailableAPIs(ctx)

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

func (hr *HealthReporter) collectAvailableAPIs(ctx context.Context) []string {
	if hr == nil || hr.client == nil {
		return nil
	}
	groups, err := hr.client.Discovery().ServerGroups()
	if err != nil {
		if hr.log != nil {
			hr.log.Debug("api discovery unavailable", "error", err)
		}
		return nil
	}
	apis := []string{"v1"}
	for _, group := range groups.Groups {
		if group.PreferredVersion.GroupVersion != "" {
			apis = append(apis, group.PreferredVersion.GroupVersion)
		}
	}
	sort.Strings(apis)
	return apis
}

// collectMetrics attempts to collect CPU/Memory metrics from the metrics API.
func (hr *HealthReporter) collectMetrics(ctx context.Context, hb *protocol.HeartbeatPayload) {
	if hr.metricsClient == nil {
		hb.DegradedReasons = append(hb.DegradedReasons, "metrics API client is not configured")
		return
	}

	nodeMetrics, err := hr.metricsClient.MetricsV1beta1().NodeMetricses().List(ctx, metav1.ListOptions{})
	if err != nil {
		hr.log.Debug("metrics API unavailable", "error", err)
		hb.DegradedReasons = append(hb.DegradedReasons, "metrics API unavailable")
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

// normalizeAgentPrivilegeProfile delegates to the canonical normalizer so the
// self-reported heartbeat capability advertisement uses the same semantics:
// an unspecified profile defaults to least-privilege viewer, while an
// explicit-but-unrecognized value also fails closed to viewer.
func normalizeAgentPrivilegeProfile(profile string) string {
	return agenttemplate.NormalizePrivilegeProfile(profile)
}

// ProfileAllowsSecrets reports whether the given privilege profile grants secret
// access. Single source of truth: a profile allows secrets iff "secrets" is not
// in its denied-capability list. Used to gate the agent's Secret informer so
// read-only profiles don't error-loop on a Forbidden watch.
func ProfileAllowsSecrets(profile string) bool {
	_, denied := capabilityFeaturesForProfile(profile)
	for _, d := range denied {
		if d == "secrets" {
			return false
		}
	}
	return true
}

func capabilityFeaturesForProfile(profile string) ([]string, []string) {
	switch normalizeAgentPrivilegeProfile(profile) {
	case "viewer":
		return []string{"logs", "watch"}, []string{"exec", "helm", "mutate", "secrets", "service_proxy"}
	case "namespace-viewer":
		return []string{"logs", "namespace_scoped", "watch"}, []string{"cluster_scope", "exec", "helm", "mutate", "secrets", "service_proxy"}
	case "operator":
		return []string{"exec", "helm", "logs", "mutate", "service_proxy", "watch"}, nil
	case "namespace-operator":
		return []string{"exec", "logs", "mutate", "namespace_scoped", "service_proxy", "watch"}, []string{"cluster_scope", "helm", "secrets"}
	case "custom":
		return []string{"custom_rbac"}, []string{"capability_inference"}
	default:
		return []string{"cluster_admin", "exec", "helm", "logs", "mutate", "rbac", "service_proxy", "watch"}, nil
	}
}

func defaultAgentValue(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
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
//
// The /healthz response is JSON for observability tooling. /readyz is also JSON
// and reports both liveness and tunnel-connection status. Both endpoints
// expose the same shape:
//
//	{"status":"ok","cluster_id":"<uuid>","uptime_seconds":<int>}
func (hr *HealthReporter) healthMux() *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		hr.writeHealthJSON(w, http.StatusOK, "ok")
	})

	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		if hr.connected.Load() {
			hr.writeHealthJSON(w, http.StatusOK, "ok")
		} else {
			hr.writeHealthJSON(w, http.StatusServiceUnavailable, "not_connected")
		}
	})
	mux.Handle("/metrics", promhttp.Handler())

	return mux
}

func (hr *HealthReporter) writeHealthJSON(w http.ResponseWriter, status int, state string) {
	body := map[string]any{
		"status":         state,
		"cluster_id":     hr.clusterID,
		"uptime_seconds": int64(time.Since(hr.startedAt).Seconds()),
	}
	if hr.agentVersion != "" {
		body["agent_version"] = hr.agentVersion
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
