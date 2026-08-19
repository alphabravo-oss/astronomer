package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
)

// Management-cluster sizer floors and mode subtractors. Requests (not limits)
// are what leftover/usable subtract; Gi/Mi are binary as in the design.
const (
	sizerGrafanaCPUMilli = 250
	sizerGrafanaMemBytes = 256 << 20 // 256Mi

	sizerSingleBinaryCPUMilli = 1100
	sizerSingleBinaryMemBytes = 2112 << 20 // 2112Mi

	sizerSimpleScalableCPUMilli = 3600
	sizerSimpleScalableMemBytes = 8256 << 20 // 8256Mi

	sizerSingleNodeSmallCPUMilli = 2000
	sizerSingleNodeSmallMemBytes = 4 << 30 // 4Gi

	sizerReserveCPUMilliPerNode = 500
	sizerReserveMemBytesPerNode = 512 << 20 // 512Mi

	sizerSeriesPerCluster          = 15_000
	sizerLogBytesPerClusterPerDay  = 2 << 30 // 2GiB
	sizerSingleBinaryMaxClusters   = 5
	sizerSingleBinaryMaxLogBytes   = 10 << 30 // 10GiB
	sizerSimpleScalableMaxClusters = 25
	sizerSimpleScalableMaxLogBytes = 50 << 30 // 50GiB

	sizerWALSingleBinaryBytes    = 10 << 30 // 10Gi
	sizerWALSimpleScalableBytes  = 20 << 30 // 20Gi
	sizerGrafanaQueryTimeoutSec  = 60
	sizerPodListCap              = 5000
	sizerPodListPage             = 500
	sizerAgentConnectedFreshness = 5 * time.Minute

	sizerModeSingleBinary   = "singleBinary"
	sizerModeSimpleScalable = "simpleScalable"

	sizerDefaultStorageClass = "default"
)

type sizerResource struct {
	CPUMillicores int64 `json:"cpuMillicores"`
	MemoryBytes   int64 `json:"memoryBytes"`
}

type sizerNodes struct {
	Count                    int   `json:"count"`
	ReadySchedulableCount    int   `json:"readySchedulableCount"`
	CPUAllocatableMillicores int64 `json:"cpuAllocatableMillicores"`
	MemoryAllocatableBytes   int64 `json:"memoryAllocatableBytes"`
}

type sizerStorageClass struct {
	Name                 string `json:"name"`
	AllowVolumeExpansion bool   `json:"allowVolumeExpansion"`
	RWO                  bool   `json:"rwo"`
	WALCapacityKnown     bool   `json:"walCapacityKnown"`
	WALCapacityBytes     int64  `json:"walCapacityBytes"`
}

type sizerObjectStorage struct {
	Configured         bool   `json:"configured"`
	StorageConfigID    string `json:"storageConfigId"`
	ComputedLokiPrefix string `json:"computedLokiPrefix"`
}

type sizerThanos struct {
	Status   string `json:"status"`
	QueryURL string `json:"queryUrl"`
}

type sizerEstimates struct {
	PrometheusSeries int64   `json:"prometheusSeries"`
	LogBytesPerDay   int64   `json:"logBytesPerDay"`
	LogMBps          float64 `json:"logMBps"`
}

type sizerGrafanaVerdict struct {
	Result   string   `json:"result"`
	Warnings []string `json:"warnings"`
	Reasons  []string `json:"reasons"`
}

type sizerLokiVerdict struct {
	Result   string   `json:"result"`
	Mode     *string  `json:"mode"`
	Reasons  []string `json:"reasons"`
	Warnings []string `json:"warnings,omitempty"`
}

type sizerThanosReceiveVerdict struct {
	Result  string   `json:"result"`
	Reasons []string `json:"reasons"`
}

type sizerVerdicts struct {
	Grafana       sizerGrafanaVerdict       `json:"grafana"`
	Loki          sizerLokiVerdict          `json:"loki"`
	ThanosReceive sizerThanosReceiveVerdict `json:"thanosReceive"`
}

type sizerCaps struct {
	LokiIngestionRateMBPerTenant  int `json:"lokiIngestionRateMBPerTenant"`
	LokiIngestionBurstMBPerTenant int `json:"lokiIngestionBurstMBPerTenant"`
	LokiGlobalBudgetMBPerSec      int `json:"lokiGlobalBudgetMBPerSec"`
	LokiMaxGlobalStreamsPerTenant int `json:"lokiMaxGlobalStreamsPerTenant"`
	GrafanaQueryTimeoutSeconds    int `json:"grafanaQueryTimeoutSeconds"`
}

// MonitoringSizerResponse is the camelCase GET /settings/monitoring/sizer/ body.
type MonitoringSizerResponse struct {
	ManagementClusterID string             `json:"managementClusterId"`
	IsLocal             bool               `json:"isLocal"`
	KubernetesVersion   string             `json:"kubernetesVersion"`
	Nodes               sizerNodes         `json:"nodes"`
	RequestsInUse       sizerResource      `json:"requestsInUse"`
	PodListTruncated    bool               `json:"podListTruncated"`
	Leftover            sizerResource      `json:"leftover"`
	Reserve             sizerResource      `json:"reserve"`
	Usable              sizerResource      `json:"usable"`
	StorageClass        sizerStorageClass  `json:"storageClass"`
	ObjectStorage       sizerObjectStorage `json:"objectStorage"`
	ConnectedClusters   int                `json:"connectedClusters"`
	Thanos              sizerThanos        `json:"thanos"`
	Estimates           sizerEstimates     `json:"estimates"`
	SkipDiskCheck       bool               `json:"skipDiskCheck"`
	Verdicts            sizerVerdicts      `json:"verdicts"`
	Caps                sizerCaps          `json:"caps"`
}

// sizerEvalInput is the cluster-free snapshot the first-match-wins procedure
// consumes. Collectors fill it; tests construct it directly.
type sizerEvalInput struct {
	NodesUnreadable          bool
	NodeCount                int
	ReadySchedulableCount    int
	CPUAllocatableMillicores int64
	MemoryAllocatableBytes   int64
	CPURequestsMillicores    int64
	MemoryRequestsBytes      int64
	PodListTruncated         bool
	StorageClassMissing      bool
	StorageClassRWO          bool
	ObjectStorageOK          bool
	ConnectedClusters        int
	ObservedPrometheusSeries int64
	ObservedLogBytesPerDay   int64
	WALCapacityKnown         bool
	WALCapacityBytes         int64
	// ClustersUnreadable is set when adopted-cluster listing failed or the
	// querier cannot list clusters. Loki must not treat that as 0 members
	// (which would match singleBinary).
	ClustersUnreadable bool
}

type sizerEvalResult struct {
	Leftover  sizerResource
	Reserve   sizerResource
	Usable    sizerResource
	Estimates sizerEstimates
	Verdicts  sizerVerdicts
	Caps      sizerCaps
}

type sizerClusterLister interface {
	ListClusters(ctx context.Context, arg sqlc.ListClustersParams) ([]sqlc.Cluster, error)
}

var _ sizerClusterLister = (*sqlc.Queries)(nil)

// GetMonitoringSizer handles GET /api/v1/settings/monitoring/sizer/.
// Read-only: it never creates or deletes PVCs. walCapacityKnown is true only
// when sharedLoki metadata already cached an install-time probe.
func (h *MonitoringHandler) GetMonitoringSizer(w http.ResponseWriter, r *http.Request) {
	if !h.authz.authorizeGlobalAction(w, r, rbac.ResourceMonitoring, rbac.VerbRead) {
		return
	}
	skipDiskCheck := queryBool(r, "skipDiskCheck")
	storageClassName := strings.TrimSpace(r.URL.Query().Get("storageClass"))
	storageConfigID := strings.TrimSpace(r.URL.Query().Get("storageConfigId"))

	collected := h.collectSizerSnapshot(r.Context(), storageClassName, storageConfigID)
	eval := evaluateSizer(collected.input)
	resp := MonitoringSizerResponse{
		ManagementClusterID: collected.managementClusterID,
		IsLocal:             collected.isLocal,
		KubernetesVersion:   collected.kubernetesVersion,
		Nodes: sizerNodes{
			Count:                    collected.input.NodeCount,
			ReadySchedulableCount:    collected.input.ReadySchedulableCount,
			CPUAllocatableMillicores: collected.input.CPUAllocatableMillicores,
			MemoryAllocatableBytes:   collected.input.MemoryAllocatableBytes,
		},
		RequestsInUse: sizerResource{
			CPUMillicores: collected.input.CPURequestsMillicores,
			MemoryBytes:   collected.input.MemoryRequestsBytes,
		},
		PodListTruncated:  collected.input.PodListTruncated,
		Leftover:          eval.Leftover,
		Reserve:           eval.Reserve,
		Usable:            eval.Usable,
		StorageClass:      collected.storageClass,
		ObjectStorage:     collected.objectStorage,
		ConnectedClusters: collected.input.ConnectedClusters,
		Thanos:            collected.thanos,
		Estimates:         eval.Estimates,
		SkipDiskCheck:     skipDiskCheck,
		Verdicts:          eval.Verdicts,
		Caps:              eval.Caps,
	}
	resp.StorageClass.WALCapacityKnown = collected.input.WALCapacityKnown
	resp.StorageClass.WALCapacityBytes = collected.input.WALCapacityBytes

	if h.log != nil {
		h.log.Info("observability sizer",
			"event", "observability_sizer",
			"grafana_result", resp.Verdicts.Grafana.Result,
			"loki_result", resp.Verdicts.Loki.Result,
			"loki_mode", stringPtrValue(resp.Verdicts.Loki.Mode),
			"leftover_cpu_millicores", resp.Leftover.CPUMillicores,
			"leftover_memory_bytes", resp.Leftover.MemoryBytes,
			"usable_cpu_millicores", resp.Usable.CPUMillicores,
			"usable_memory_bytes", resp.Usable.MemoryBytes,
			"pod_list_truncated", resp.PodListTruncated,
			"connected_clusters", resp.ConnectedClusters,
		)
	}
	RespondJSON(w, http.StatusOK, resp)
}

func stringPtrValue(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

// evaluateSizer is the pure first-match-wins procedure. It does not touch
// a cluster, disks, or secrets.
func evaluateSizer(in sizerEvalInput) sizerEvalResult {
	leftoverCPU := in.CPUAllocatableMillicores - in.CPURequestsMillicores
	if leftoverCPU < 0 {
		leftoverCPU = 0
	}
	leftoverMem := in.MemoryAllocatableBytes - in.MemoryRequestsBytes
	if leftoverMem < 0 {
		leftoverMem = 0
	}
	ready := in.ReadySchedulableCount
	if ready < 0 {
		ready = 0
	}
	reserveCPU := int64(sizerReserveCPUMilliPerNode) * int64(ready)
	reserveMem := int64(sizerReserveMemBytesPerNode) * int64(ready)
	usableCPU := leftoverCPU - reserveCPU
	if usableCPU < 0 {
		usableCPU = 0
	}
	usableMem := leftoverMem - reserveMem
	if usableMem < 0 {
		usableMem = 0
	}

	clusters := in.ConnectedClusters
	if clusters < 0 {
		clusters = 0
	}
	series := int64(clusters) * sizerSeriesPerCluster
	if in.ObservedPrometheusSeries > series {
		series = in.ObservedPrometheusSeries
	}
	logBytes := int64(clusters) * sizerLogBytesPerClusterPerDay
	if in.ObservedLogBytesPerDay > logBytes {
		logBytes = in.ObservedLogBytesPerDay
	}
	logMBps := float64(logBytes) / 86400.0 / 1_000_000.0

	out := sizerEvalResult{
		Leftover:  sizerResource{CPUMillicores: leftoverCPU, MemoryBytes: leftoverMem},
		Reserve:   sizerResource{CPUMillicores: reserveCPU, MemoryBytes: reserveMem},
		Usable:    sizerResource{CPUMillicores: usableCPU, MemoryBytes: usableMem},
		Estimates: sizerEstimates{PrometheusSeries: series, LogBytesPerDay: logBytes, LogMBps: logMBps},
	}
	out.Verdicts.Grafana = evaluateGrafanaSizer(in.NodesUnreadable, leftoverCPU, leftoverMem, usableCPU, usableMem, in.PodListTruncated)
	out.Verdicts.Loki = evaluateLokiSizer(in, leftoverCPU, leftoverMem, usableCPU, usableMem, clusters, logBytes)
	out.Verdicts.ThanosReceive = sizerThanosReceiveVerdict{
		Result:  "fail",
		Reasons: []string{"receive_not_offered"},
	}
	out.Caps = sizerCapsForMode(out.Verdicts.Loki)
	return out
}

func evaluateGrafanaSizer(nodesUnreadable bool, leftoverCPU, leftoverMem, usableCPU, usableMem int64, truncated bool) sizerGrafanaVerdict {
	v := sizerGrafanaVerdict{Warnings: []string{}, Reasons: []string{}}
	if nodesUnreadable {
		v.Result = "fail"
		v.Reasons = []string{"nodes_unreadable"}
		return v
	}
	if leftoverCPU < sizerGrafanaCPUMilli || leftoverMem < sizerGrafanaMemBytes {
		v.Result = "fail"
		v.Reasons = []string{"below_grafana_floor"}
		return v
	}
	v.Result = "pass"
	if usableCPU < sizerGrafanaCPUMilli || usableMem < sizerGrafanaMemBytes {
		v.Warnings = append(v.Warnings, "tight_fit")
	}
	if truncated {
		v.Warnings = append(v.Warnings, "pod_list_truncated")
	}
	return v
}

func evaluateLokiSizer(in sizerEvalInput, leftoverCPU, leftoverMem, usableCPU, usableMem int64, clusters int, logBytes int64) sizerLokiVerdict {
	fail := func(reason string) sizerLokiVerdict {
		return sizerLokiVerdict{Result: "fail", Mode: nil, Reasons: []string{reason}}
	}
	if !in.ObjectStorageOK {
		return fail("object_storage_missing")
	}
	if in.StorageClassMissing || !in.StorageClassRWO {
		return fail("storage_class_not_rwo")
	}
	if in.PodListTruncated {
		return fail("pod_list_truncated")
	}
	if in.ReadySchedulableCount == 1 && (leftoverCPU < sizerSingleNodeSmallCPUMilli || leftoverMem < sizerSingleNodeSmallMemBytes) {
		return fail("single_node_small")
	}
	if usableCPU < sizerSingleBinaryCPUMilli || usableMem < sizerSingleBinaryMemBytes {
		return fail("below_singlebinary_floor")
	}
	if in.ClustersUnreadable {
		return fail("clusters_unreadable")
	}

	var mode string
	switch {
	case clusters <= sizerSingleBinaryMaxClusters && logBytes <= sizerSingleBinaryMaxLogBytes:
		mode = sizerModeSingleBinary
	case in.ReadySchedulableCount >= 2 &&
		usableCPU >= sizerSimpleScalableCPUMilli && usableMem >= sizerSimpleScalableMemBytes &&
		clusters <= sizerSimpleScalableMaxClusters && logBytes <= sizerSimpleScalableMaxLogBytes:
		mode = sizerModeSimpleScalable
	default:
		return fail("above_hosted_scale")
	}

	pass := sizerLokiVerdict{Result: "pass", Mode: stringPtr(mode), Reasons: []string{}}
	need := int64(sizerWALSingleBinaryBytes)
	if mode == sizerModeSimpleScalable {
		need = sizerWALSimpleScalableBytes
	}
	if in.WALCapacityKnown {
		if in.WALCapacityBytes < need {
			return fail("wal_too_small")
		}
		return pass
	}
	// GET/preview: never probe disks. Unknown WAL is a warning, not a fail.
	pass.Warnings = []string{"wal_capacity_unchecked"}
	return pass
}

func sizerCapsForMode(loki sizerLokiVerdict) sizerCaps {
	caps := sizerCaps{GrafanaQueryTimeoutSeconds: sizerGrafanaQueryTimeoutSec}
	if loki.Result != "pass" || loki.Mode == nil {
		return caps
	}
	switch *loki.Mode {
	case sizerModeSingleBinary:
		caps.LokiIngestionRateMBPerTenant = 1
		caps.LokiIngestionBurstMBPerTenant = 2
		caps.LokiGlobalBudgetMBPerSec = 8
		caps.LokiMaxGlobalStreamsPerTenant = 5_000
	case sizerModeSimpleScalable:
		caps.LokiIngestionRateMBPerTenant = 2
		caps.LokiIngestionBurstMBPerTenant = 4
		caps.LokiGlobalBudgetMBPerSec = 32
		caps.LokiMaxGlobalStreamsPerTenant = 20_000
	}
	return caps
}

// computedLokiPrefix is join(storageCfg.Prefix, "loki") with slashes
// normalized. Empty prefix → "loki".
func computedLokiPrefix(prefix string) string {
	prefix = strings.Trim(strings.TrimSpace(prefix), "/")
	if prefix == "" {
		return "loki"
	}
	return prefix + "/loki"
}

type sizerSnapshot struct {
	input               sizerEvalInput
	managementClusterID string
	isLocal             bool
	kubernetesVersion   string
	storageClass        sizerStorageClass
	objectStorage       sizerObjectStorage
	thanos              sizerThanos
}

func (h *MonitoringHandler) collectSizerSnapshot(ctx context.Context, storageClassName, storageConfigID string) sizerSnapshot {
	snap := sizerSnapshot{
		storageClass: sizerStorageClass{Name: defaultString(storageClassName, sizerDefaultStorageClass)},
		thanos:       sizerThanos{Status: "not_configured"},
	}
	var thanosMeta, lokiMeta map[string]any
	if h != nil && h.queries != nil {
		if b, err := h.queries.GetDefaultMonitoringBackend(ctx); err == nil {
			authCfg := h.readAuthConfig(b)
			thanosMeta = mapFromMapValue(authCfg["sharedThanos"])
			if len(thanosMeta) == 0 {
				thanosMeta = sharedThanosMetadata(b)
			}
			lokiMeta = mapFromMapValue(authCfg["sharedLoki"])
			if len(lokiMeta) == 0 {
				lokiMeta = sharedStackMetadata(b, "sharedLoki")
			}
			snap.thanos.Status = defaultString(stringFromMap(thanosMeta, "status"), "not_configured")
			snap.thanos.QueryURL = strings.TrimSpace(b.QueryUrl)
			if snap.thanos.QueryURL == "" {
				release := defaultString(stringFromMap(thanosMeta, "releaseName"), "thanos")
				ns := defaultString(stringFromMap(thanosMeta, "namespace"), "monitoring")
				snap.thanos.QueryURL = fmt.Sprintf("http://%s-query-frontend.%s.svc.cluster.local:9090", release, ns)
			}
			if storageConfigID == "" {
				storageConfigID = stringFromMap(thanosMeta, "storageConfigId")
			}
			if storageConfigID == "" {
				storageConfigID = stringFromMap(lokiMeta, "storageConfigId")
			}
			if boolFromAny(lokiMeta["walCapacityKnown"]) {
				snap.input.WALCapacityKnown = true
				snap.input.WALCapacityBytes = int64FromAny(lokiMeta["walCapacityBytes"])
			}
		}
	}

	clusters, listErr := h.sizerListAllClusters(ctx)
	if listErr != nil {
		snap.input.ClustersUnreadable = true
	} else {
		snap.input.ConnectedClusters = countConnectedAdoptedClusters(clusters, time.Now())
	}
	if cluster, ok := sizerPickManagementCluster(clusters, thanosMeta, lokiMeta); ok {
		snap.managementClusterID = cluster.ID.String()
		snap.isLocal = cluster.IsLocal
		snap.kubernetesVersion = cluster.KubernetesVersion
	}

	snap.objectStorage = h.collectSizerObjectStorage(ctx, storageConfigID)
	snap.input.ObjectStorageOK = snap.objectStorage.Configured

	if snap.managementClusterID == "" || h == nil || h.requester == nil {
		snap.input.NodesUnreadable = true
		snap.storageClass.Name = defaultString(storageClassName, sizerDefaultStorageClass)
		if storageClassName == "" {
			snap.input.StorageClassMissing = true
			snap.input.StorageClassRWO = false
		}
	} else {
		nodes, err := h.sizerListReadyNodes(ctx, snap.managementClusterID)
		if err != nil {
			snap.input.NodesUnreadable = true
		} else {
			snap.input.NodeCount = nodes.total
			snap.input.ReadySchedulableCount = nodes.ready
			snap.input.CPUAllocatableMillicores = nodes.cpuMilli
			snap.input.MemoryAllocatableBytes = nodes.memBytes
			if snap.kubernetesVersion == "" {
				snap.kubernetesVersion = nodes.kubeVersion
			}
		}
		reqCPU, reqMem, truncated, perr := h.sizerSumPodRequests(ctx, snap.managementClusterID)
		if perr != nil {
			// Cannot trust leftover; fail-closed for Loki, warn Grafana.
			snap.input.PodListTruncated = true
		} else {
			snap.input.CPURequestsMillicores = reqCPU
			snap.input.MemoryRequestsBytes = reqMem
			snap.input.PodListTruncated = truncated
		}
		sc := h.sizerGetStorageClass(ctx, snap.managementClusterID, storageClassName)
		snap.storageClass.Name = sc.name
		snap.storageClass.AllowVolumeExpansion = sc.expand
		snap.storageClass.RWO = sc.rwo
		snap.input.StorageClassMissing = sc.missing
		snap.input.StorageClassRWO = sc.rwo
	}

	snap.input.ObservedPrometheusSeries = h.sizerObservedPrometheusSeries(ctx, snap.managementClusterID)
	if lokiRunning(lokiMeta) {
		snap.input.ObservedLogBytesPerDay = h.sizerObservedLokiBytesPerDay(ctx, snap.managementClusterID)
	}
	return snap
}

func lokiRunning(meta map[string]any) bool {
	switch strings.ToLower(stringFromMap(meta, "status")) {
	case "healthy", "degraded", "degraded_capacity", "drifted":
		return true
	default:
		return false
	}
}

func sizerPickManagementCluster(clusters []sqlc.Cluster, thanosMeta, lokiMeta map[string]any) (sqlc.Cluster, bool) {
	for _, c := range clusters {
		if c.IsLocal {
			return c, true
		}
	}
	id := stringFromMap(thanosMeta, "managementClusterId")
	if id == "" {
		id = stringFromMap(lokiMeta, "managementClusterId")
	}
	if id == "" {
		return sqlc.Cluster{}, false
	}
	for _, c := range clusters {
		if c.ID.String() == id {
			return c, true
		}
	}
	parsed, err := uuid.Parse(id)
	if err != nil {
		return sqlc.Cluster{}, false
	}
	return sqlc.Cluster{ID: parsed}, true
}

func (h *MonitoringHandler) sizerListAllClusters(ctx context.Context) ([]sqlc.Cluster, error) {
	if h == nil || h.queries == nil {
		return nil, fmt.Errorf("monitoring store not configured")
	}
	lister, ok := h.queries.(sizerClusterLister)
	if !ok {
		return nil, fmt.Errorf("cluster lister not configured")
	}
	const page int32 = 500
	var all []sqlc.Cluster
	for offset := int32(0); ; offset += page {
		rows, err := lister.ListClusters(ctx, sqlc.ListClustersParams{Limit: page, Offset: offset})
		if err != nil {
			return nil, err
		}
		all = append(all, rows...)
		if int32(len(rows)) < page {
			return all, nil
		}
	}
}

// countConnectedAdoptedClusters counts member clusters with a fresh agent
// heartbeat. The local management plane is excluded: Loki attach/log
// estimates are about member shippers, and including is_local would push a
// 5-member fleet over the SingleBinary cluster cap.
func countConnectedAdoptedClusters(clusters []sqlc.Cluster, now time.Time) int {
	n := 0
	for _, c := range clusters {
		if c.IsLocal {
			continue
		}
		if !c.LastHeartbeat.Valid {
			continue
		}
		if now.Sub(c.LastHeartbeat.Time) >= sizerAgentConnectedFreshness {
			continue
		}
		n++
	}
	return n
}

func (h *MonitoringHandler) collectSizerObjectStorage(ctx context.Context, storageConfigID string) sizerObjectStorage {
	out := sizerObjectStorage{StorageConfigID: storageConfigID, ComputedLokiPrefix: "loki"}
	if h == nil || h.queries == nil || storageConfigID == "" {
		return out
	}
	id, err := uuid.Parse(storageConfigID)
	if err != nil {
		return out
	}
	cfg, err := h.queries.GetBackupStorageConfigByID(ctx, id)
	if err != nil {
		return out
	}
	out.ComputedLokiPrefix = computedLokiPrefix(cfg.Prefix)
	if cfg.Bucket == "" {
		return out
	}
	if _, _, err := h.storageCredentials(cfg); err != nil {
		return out
	}
	out.Configured = true
	return out
}

type sizerNodeSum struct {
	total       int
	ready       int
	cpuMilli    int64
	memBytes    int64
	kubeVersion string
}

func (h *MonitoringHandler) sizerListReadyNodes(ctx context.Context, clusterID string) (sizerNodeSum, error) {
	var list corev1.NodeList
	if err := h.sizerGetJSON(ctx, clusterID, "/api/v1/nodes", &list); err != nil {
		return sizerNodeSum{}, err
	}
	sum := sizerNodeSum{total: len(list.Items)}
	for _, n := range list.Items {
		if !nodeReadySchedulable(n) {
			continue
		}
		sum.ready++
		sum.cpuMilli += n.Status.Allocatable.Cpu().MilliValue()
		sum.memBytes += n.Status.Allocatable.Memory().Value()
		if sum.kubeVersion == "" {
			sum.kubeVersion = n.Status.NodeInfo.KubeletVersion
		}
	}
	return sum, nil
}

func nodeReadySchedulable(n corev1.Node) bool {
	if n.Spec.Unschedulable {
		return false
	}
	for _, c := range n.Status.Conditions {
		if c.Type == corev1.NodeReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

func (h *MonitoringHandler) sizerSumPodRequests(ctx context.Context, clusterID string) (cpuMilli, memBytes int64, truncated bool, err error) {
	cont := ""
	seen := 0
	for {
		path := fmt.Sprintf("/api/v1/pods?limit=%d", sizerPodListPage)
		if cont != "" {
			path += "&continue=" + url.QueryEscape(cont)
		}
		var list corev1.PodList
		if getErr := h.sizerGetJSON(ctx, clusterID, path, &list); getErr != nil {
			return cpuMilli, memBytes, true, getErr
		}
		for i := range list.Items {
			if seen >= sizerPodListCap {
				return cpuMilli, memBytes, true, nil
			}
			c, m := podRequestTotals(list.Items[i])
			cpuMilli += c
			memBytes += m
			seen++
		}
		if list.Continue == "" {
			return cpuMilli, memBytes, false, nil
		}
		if seen >= sizerPodListCap {
			return cpuMilli, memBytes, true, nil
		}
		cont = list.Continue
	}
}

func resourceRequestMilliBytes(list corev1.ResourceList) (cpuMilli, memBytes int64) {
	if len(list) == 0 {
		return 0, 0
	}
	return list.Cpu().MilliValue(), list.Memory().Value()
}

func initContainerIsSidecar(c corev1.Container) bool {
	return c.RestartPolicy != nil && *c.RestartPolicy == corev1.ContainerRestartPolicyAlways
}

// podRequestTotals matches kubelet effective requests: max(non-sidecar init)
// + sum(app containers) + sum(sidecar inits). Limits and missing requests are 0.
func podRequestTotals(pod corev1.Pod) (cpuMilli, memBytes int64) {
	var maxInitCPU, maxInitMem int64
	for _, c := range pod.Spec.InitContainers {
		cpu, mem := resourceRequestMilliBytes(c.Resources.Requests)
		if initContainerIsSidecar(c) {
			cpuMilli += cpu
			memBytes += mem
			continue
		}
		if cpu > maxInitCPU {
			maxInitCPU = cpu
		}
		if mem > maxInitMem {
			maxInitMem = mem
		}
	}
	cpuMilli += maxInitCPU
	memBytes += maxInitMem
	for _, c := range pod.Spec.Containers {
		cpu, mem := resourceRequestMilliBytes(c.Resources.Requests)
		cpuMilli += cpu
		memBytes += mem
	}
	return cpuMilli, memBytes
}

type sizerStorageClassInfo struct {
	name    string
	expand  bool
	rwo     bool
	missing bool
}

func (h *MonitoringHandler) sizerGetStorageClass(ctx context.Context, clusterID, name string) sizerStorageClassInfo {
	name = strings.TrimSpace(name)
	if name != "" && name != sizerDefaultStorageClass {
		info, err := h.sizerLoadStorageClass(ctx, clusterID, name)
		if err != nil {
			return sizerStorageClassInfo{name: name, missing: true}
		}
		return info
	}
	if name == "" {
		name = sizerDefaultStorageClass
	}
	info, err := h.sizerLoadStorageClass(ctx, clusterID, name)
	if err == nil {
		return info
	}
	if def, ok := h.sizerDefaultStorageClass(ctx, clusterID); ok {
		return def
	}
	return sizerStorageClassInfo{name: name, missing: true}
}

func (h *MonitoringHandler) sizerLoadStorageClass(ctx context.Context, clusterID, name string) (sizerStorageClassInfo, error) {
	var sc storageClassWire
	path := "/apis/storage.k8s.io/v1/storageclasses/" + url.PathEscape(name)
	if err := h.sizerGetJSON(ctx, clusterID, path, &sc); err != nil {
		return sizerStorageClassInfo{}, err
	}
	return storageClassFromWire(sc), nil
}

func (h *MonitoringHandler) sizerDefaultStorageClass(ctx context.Context, clusterID string) (sizerStorageClassInfo, bool) {
	var list struct {
		Items []storageClassWire `json:"items"`
	}
	if err := h.sizerGetJSON(ctx, clusterID, "/apis/storage.k8s.io/v1/storageclasses", &list); err != nil {
		return sizerStorageClassInfo{}, false
	}
	for _, sc := range list.Items {
		if sc.Metadata.Annotations["storageclass.kubernetes.io/is-default-class"] == "true" {
			return storageClassFromWire(sc), true
		}
	}
	return sizerStorageClassInfo{}, false
}

// storageClassWire is the StorageClass JSON plus optional accessModes (not on
// the official object; present → honor it, absent → treat as RWO so k3s
// local-path can reach single_node_small rather than storage_class_not_rwo).
type storageClassWire struct {
	Metadata struct {
		Name        string            `json:"name"`
		Annotations map[string]string `json:"annotations"`
	} `json:"metadata"`
	AllowVolumeExpansion *bool    `json:"allowVolumeExpansion"`
	AccessModes          []string `json:"accessModes"`
	Provisioner          string   `json:"provisioner"`
}

func storageClassFromWire(sc storageClassWire) sizerStorageClassInfo {
	info := sizerStorageClassInfo{name: sc.Metadata.Name, rwo: true}
	if sc.AllowVolumeExpansion != nil {
		info.expand = *sc.AllowVolumeExpansion
	}
	if len(sc.AccessModes) == 0 {
		return info
	}
	info.rwo = false
	for _, m := range sc.AccessModes {
		if m == string(corev1.ReadWriteOnce) {
			info.rwo = true
			break
		}
	}
	return info
}

func (h *MonitoringHandler) sizerGetJSON(ctx context.Context, clusterID, path string, out any) error {
	if h == nil || h.requester == nil {
		return fmt.Errorf("kubernetes requester not configured")
	}
	resp, err := h.requester.Do(ctx, clusterID, http.MethodGet, path, nil, requestHeaders(""))
	if err != nil {
		return err
	}
	if err := ensureSuccess(resp); err != nil {
		return err
	}
	return parseJSONResponse(resp, out)
}

func (h *MonitoringHandler) sizerObservedPrometheusSeries(ctx context.Context, clusterID string) int64 {
	if clusterID == "" {
		return 0
	}
	qctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	client, _, ok, err := h.backendClient(qctx, clusterID)
	if err != nil || !ok || client == nil {
		return 0
	}
	v, err := client.QueryScalar(qctx, "sum(prometheus_tsdb_head_series)")
	if err != nil || v <= 0 {
		return 0
	}
	return int64(v)
}

func (h *MonitoringHandler) sizerObservedLokiBytesPerDay(ctx context.Context, clusterID string) int64 {
	if clusterID == "" {
		return 0
	}
	qctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	client, _, ok, err := h.backendClient(qctx, clusterID)
	if err != nil || !ok || client == nil {
		return 0
	}
	v, err := client.QueryScalar(qctx, "sum(rate(loki_distributor_bytes_received_total[24h]))")
	if err != nil || v <= 0 {
		return 0
	}
	return int64(v * 86400)
}

func boolFromAny(v any) bool {
	switch n := v.(type) {
	case bool:
		return n
	case string:
		return strings.EqualFold(n, "true")
	default:
		return false
	}
}

func int64FromAny(v any) int64 {
	switch n := v.(type) {
	case int:
		return int64(n)
	case int32:
		return int64(n)
	case int64:
		return n
	case float64:
		return int64(n)
	case json.Number:
		i, _ := n.Int64()
		return i
	case string:
		var parsed int64
		_, _ = fmt.Sscan(n, &parsed)
		return parsed
	default:
		return 0
	}
}
