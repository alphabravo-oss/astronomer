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

// inventoryPageSize bounds one page of the fallback inventory LISTs (the path
// taken before the informer caches have synced, or when they never do). The
// pair that matters is ResourceVersion:"0" + Limit: with a revision of "0" the
// apiserver serves the request from its watch cache instead of taking an etcd
// quorum read, and it deliberately ignores the limit while doing so — so on a
// current apiserver these calls cost one cache read and return no continue
// token. The paging loop is what keeps the fallback bounded on the older /
// cache-disabled apiservers that do honour the limit, and what makes a later
// switch to a consistent read a one-line change rather than a reintroduced
// full-cluster allocation.
const inventoryPageSize = 500

// InventorySource supplies cluster inventory from an already-synced informer
// cache so the periodic collectors below do not have to ask the apiserver for
// data the agent is already watching. *StateSubscriber implements it.
//
// Every method returns ok=false when its cache is unavailable (not yet synced,
// not registered, or RBAC-denied). That is NOT the same as "zero": the
// collectors must fall back to the apiserver rather than report a fabricated
// count, otherwise an agent whose Pod informer is Forbidden would quietly
// heartbeat "0 pods" forever instead of surfacing the degradation.
type InventorySource interface {
	InventoryNodes() ([]*corev1.Node, bool)
	InventoryPodCount() (int, bool)
	InventoryPodsByNamespace() (map[string]int, bool)
}

// HealthReporter sends periodic health data to the server.
type HealthReporter struct {
	client kubernetes.Interface
	// impersonation caches the agent's SelfSubjectAccessReview answer to
	// "may I impersonate the pinned astronomer proxy subject?", advertised in
	// EnabledFeatures / DeniedFeatures. See impersonation_probe.go.
	impersonation     impersonationProbe
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

	// connected tracks whether the tunnel is up (for readiness probes) and
	// gates periodic collection: there is no point paying for cluster
	// inventory that cannot be delivered.
	connected atomic.Bool

	// inventory is the optional informer-backed inventory source, wired after
	// the state subscriber exists (cmd/agent/main.go). Held as an atomic
	// pointer because it is installed from the subscriber's goroutine while
	// the health tickers read it from theirs. nil = apiserver fallback.
	inventory atomic.Pointer[InventorySource]
}

// NewHealthReporter creates a new HealthReporter.
func NewHealthReporter(client kubernetes.Interface, log *slog.Logger, heartbeatSec, metricsSec int) *HealthReporter {
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

// SetInventorySource wires the informer-backed inventory source. Safe to call
// at any time, including while the tickers are running: until it is set (or
// while the caches are unsynced) the collectors use the paged apiserver
// fallback, so the reported payload is identical either way.
func (hr *HealthReporter) SetInventorySource(src InventorySource) {
	if hr == nil || src == nil {
		return
	}
	hr.inventory.Store(&src)
}

// nodes returns the cluster's Nodes, preferring the informer cache. The
// fallback List is paged and watch-cache-served (see inventoryPageSize).
func (hr *HealthReporter) nodes(ctx context.Context) ([]*corev1.Node, error) {
	if src := hr.inventory.Load(); src != nil {
		if nodes, ok := (*src).InventoryNodes(); ok {
			return nodes, nil
		}
	}
	if hr.client == nil {
		return nil, fmt.Errorf("no kubernetes client")
	}
	var nodes []*corev1.Node
	opts := metav1.ListOptions{ResourceVersion: "0", Limit: inventoryPageSize}
	for {
		page, err := hr.client.CoreV1().Nodes().List(ctx, opts)
		if err != nil {
			return nil, err
		}
		for i := range page.Items {
			nodes = append(nodes, &page.Items[i])
		}
		if page.Continue == "" {
			return nodes, nil
		}
		// A continue token already encodes the revision it resumes from, and
		// the apiserver rejects a request carrying both.
		opts = metav1.ListOptions{Limit: inventoryPageSize, Continue: page.Continue}
	}
}

// podsByNamespace returns pod counts per namespace, preferring the informer
// cache. The fallback pages so a 10k-pod cluster is never materialized in one
// allocation; only the counts survive each page.
func (hr *HealthReporter) podsByNamespace(ctx context.Context) (map[string]int, error) {
	if src := hr.inventory.Load(); src != nil {
		if counts, ok := (*src).InventoryPodsByNamespace(); ok {
			return counts, nil
		}
	}
	if hr.client == nil {
		return nil, fmt.Errorf("no kubernetes client")
	}
	counts := make(map[string]int)
	opts := metav1.ListOptions{ResourceVersion: "0", Limit: inventoryPageSize}
	for {
		page, err := hr.client.CoreV1().Pods(metav1.NamespaceAll).List(ctx, opts)
		if err != nil {
			return nil, err
		}
		for i := range page.Items {
			counts[page.Items[i].Namespace]++
		}
		if page.Continue == "" {
			return counts, nil
		}
		opts = metav1.ListOptions{Limit: inventoryPageSize, Continue: page.Continue}
	}
}

// podCount returns the cluster-wide pod count. The informer path answers from
// cache keys alone; the fallback shares the paged reader above.
func (hr *HealthReporter) podCount(ctx context.Context) (int, error) {
	if src := hr.inventory.Load(); src != nil {
		if count, ok := (*src).InventoryPodCount(); ok {
			return count, nil
		}
	}
	counts, err := hr.podsByNamespace(ctx)
	if err != nil {
		return 0, err
	}
	total := 0
	for _, c := range counts {
		total += c
	}
	return total, nil
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
	hr.tick(ctx, sendFn, hr.sendHeartbeat)
	hr.tick(ctx, sendFn, hr.sendMetrics)

	for {
		select {
		case <-ctx.Done():
			return
		case <-heartbeatTicker.C:
			hr.tick(ctx, sendFn, hr.sendHeartbeat)
		case <-metricsTicker.C:
			hr.tick(ctx, sendFn, hr.sendMetrics)
		}
	}
}

// tick runs one collector, but only while the tunnel is up. Collection is the
// expensive half (cluster inventory, metrics API) and its product is a single
// frame that a disconnected tunnel would drop anyway, so a disconnected agent
// does no collection work at all — it stops charging the apiserver of a cluster
// whose management plane it cannot reach.
//
// The tickers keep running rather than backing off: the skip costs one atomic
// load, and a running ticker resumes reporting within one interval of the
// reconnect instead of trailing a backoff curve.
func (hr *HealthReporter) tick(ctx context.Context, sendFn func(*protocol.Message) error, collect func(context.Context, func(*protocol.Message) error)) {
	if !hr.connected.Load() {
		hr.log.Debug("skipping health collection while tunnel is disconnected")
		return
	}
	collect(ctx, sendFn)
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

	nodes, err := hr.nodes(ctx)
	if err != nil {
		return nil, fmt.Errorf("list nodes: %w", err)
	}
	out.ClusterNodeCount = len(nodes)

	// Per-namespace pod counts. Only the counts are needed, so neither the
	// informer path nor the fallback ever holds the cluster's Pod objects.
	nsCounts, err := hr.podsByNamespace(ctx)
	if err != nil {
		return nil, fmt.Errorf("list pods: %w", err)
	}
	for _, c := range nsCounts {
		out.ClusterPodCount += c
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
	capByNode := make(map[string]struct{ cpu, mem int64 }, len(nodes))
	var totalCPUCap, totalMemCap int64
	for _, n := range nodes {
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
	// A partial inventory failure no longer suppresses the beat (H11): we emit
	// a minimal liveness frame carrying DegradedReasons instead of nothing.
	if len(hb.DegradedReasons) > 0 {
		hr.log.Warn("emitting degraded heartbeat", "reasons", hb.DegradedReasons)
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
		PrivilegeProfile:       normalizeAgentPrivilegeProfile(hr.privilegeProfile),
		EnabledFeatures:        append([]string{}, hr.enabledFeatures...),
		DeniedFeatures:         append([]string{}, hr.deniedFeatures...),
		LastSuccessfulAction:   "heartbeat.collect",
		LastSuccessfulActionAt: time.Now().UTC().Format(time.RFC3339),
	}
	if len(hb.EnabledFeatures) == 0 && len(hb.DeniedFeatures) == 0 {
		hb.EnabledFeatures, hb.DeniedFeatures = capabilityFeaturesForProfile(hb.PrivilegeProfile)
	}
	// Impersonation capability handshake. Appended AFTER the profile fallback
	// above so it cannot make the lists non-empty and suppress it. The answer
	// comes from an hourly-cached SelfSubjectAccessReview, not from the profile
	// name — the server must gate `enforce` on what the apiserver actually
	// grants this agent, not on what we think the manifest said.
	applyImpersonationCapability(hb, hr.impersonation.allowedNow(ctx, hr.client))

	// Inventory collection is best-effort and DECOUPLED from liveness (H11):
	// a failed apiserver List must not suppress the heartbeat. On a partial
	// failure we record a DegradedReasons entry, leave the corresponding
	// inventory field at its zero value (the server keeps the last-good value,
	// L11), and still emit the (minimal) frame so last_heartbeat stays fresh
	// and the cluster is not falsely flipped to disconnected.

	// K8s version.
	if serverVersion, err := hr.client.Discovery().ServerVersion(); err != nil {
		hb.DegradedReasons = append(hb.DegradedReasons, fmt.Sprintf("server_version_unavailable: %v", err))
	} else {
		hb.KubernetesVersion = serverVersion.GitVersion
	}

	// Node count and distribution detection. The node set is fetched ONCE per
	// beat and handed to collectMetrics, which previously listed it a second
	// time for the same allocatable totals.
	nodes, nodesErr := hr.nodes(ctx)
	if nodesErr != nil {
		hb.DegradedReasons = append(hb.DegradedReasons, fmt.Sprintf("list_nodes_failed: %v", nodesErr))
	} else {
		hb.NodeCount = len(nodes)
		hb.Distribution = detectDistribution(nodes)
	}
	hb.AvailableAPIs = hr.collectAvailableAPIs(ctx)

	// Pod count (all namespaces).
	if podCount, err := hr.podCount(ctx); err != nil {
		hb.DegradedReasons = append(hb.DegradedReasons, fmt.Sprintf("list_pods_failed: %v", err))
	} else {
		hb.PodCount = podCount
	}

	// CPU/Memory from metrics API (best-effort).
	hr.collectMetrics(ctx, hb, nodes)

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
// nodes is the set already collected for this beat (nil when that collection
// failed, in which case the percentages are left at zero exactly as they were
// when this function did its own — third — Nodes list).
func (hr *HealthReporter) collectMetrics(ctx context.Context, hb *protocol.HeartbeatPayload, nodes []*corev1.Node) {
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

	// Allocatable resources come from the node set collected for this beat.
	if len(nodes) == 0 {
		return
	}

	var totalCPUCapacity, totalMemCapacity int64
	for _, node := range nodes {
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
// The nodes are informer-owned when the inventory source is wired, so this must
// stay read-only.
func detectDistribution(nodes []*corev1.Node) string {
	if len(nodes) == 0 || nodes[0] == nil {
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

// ProfileAllowsSecrets reports whether the normalized built-in profile is
// explicitly known to grant secret access. Custom and unknown profiles remain
// disabled because their permissions cannot be inferred safely. Used to gate
// the agent's Secret informer so omission or ambiguous RBAC never widens the
// sensitive watch surface.
func ProfileAllowsSecrets(profile string) bool {
	switch normalizeAgentPrivilegeProfile(profile) {
	case agenttemplate.PrivilegeProfileAdmin, agenttemplate.PrivilegeProfileOperator:
		return true
	default:
		return false
	}
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
