package handler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

func sizerAuthedRequest(method, target string) *http.Request {
	req := httptest.NewRequest(method, target, nil)
	ctx := middleware.SetAuthenticatedUserForTest(req.Context(), &middleware.AuthenticatedUser{
		ID:         uuid.NewString(),
		AuthMethod: "jwt",
	})
	return req.WithContext(ctx)
}

func sizerOKStorage() sizerEvalInput {
	return sizerEvalInput{
		StorageClassRWO: true,
		ObjectStorageOK: true,
	}
}

func TestEvaluateSizerFixtures(t *testing.T) {
	t.Parallel()

	type want struct {
		grafana     string
		loki        string
		lokiMode    string
		lokiReason  string
		leftoverCPU int64
		leftoverMem int64
		usableCPU   int64
		usableMem   int64
		series      int64
		logBytes    int64
		rateMB      int
		burstMB     int
		globalMB    int
		streams     int
		lokiWarning string
	}

	cases := []struct {
		name string
		in   sizerEvalInput
		want want
	}{
		{
			name: "1-node 4CPU/8Gi leftover 1900m/3.5Gi, 3 clusters",
			in: func() sizerEvalInput {
				in := sizerOKStorage()
				in.NodeCount = 1
				in.ReadySchedulableCount = 1
				in.CPUAllocatableMillicores = 4000
				in.MemoryAllocatableBytes = 8 << 30
				in.CPURequestsMillicores = 2100
				in.MemoryRequestsBytes = (4 << 30) + (512 << 20) // 4.5Gi
				in.ConnectedClusters = 3
				return in
			}(),
			want: want{
				grafana:     "pass",
				loki:        "fail",
				lokiReason:  "single_node_small",
				leftoverCPU: 1900,
				leftoverMem: (8 << 30) - ((4 << 30) + (512 << 20)),
				usableCPU:   1400,
				usableMem:   ((8 << 30) - ((4 << 30) + (512 << 20))) - (512 << 20),
				series:      3 * sizerSeriesPerCluster,
				logBytes:    3 * sizerLogBytesPerClusterPerDay,
			},
		},
		{
			name: "1-node 16CPU/64Gi, 3 clusters → singleBinary",
			in: func() sizerEvalInput {
				in := sizerOKStorage()
				in.NodeCount = 1
				in.ReadySchedulableCount = 1
				in.CPUAllocatableMillicores = 16000
				in.MemoryAllocatableBytes = 64 << 30
				in.CPURequestsMillicores = 3000
				in.MemoryRequestsBytes = 6 << 30
				in.ConnectedClusters = 3
				return in
			}(),
			want: want{
				grafana:     "pass",
				loki:        "pass",
				lokiMode:    sizerModeSingleBinary,
				leftoverCPU: 13000,
				leftoverMem: (64 << 30) - (6 << 30),
				usableCPU:   12500,
				usableMem:   ((64 << 30) - (6 << 30)) - (512 << 20),
				series:      3 * sizerSeriesPerCluster,
				logBytes:    3 * sizerLogBytesPerClusterPerDay,
				rateMB:      1,
				burstMB:     2,
				globalMB:    8,
				streams:     5000,
				lokiWarning: "wal_capacity_unchecked",
			},
		},
		{
			name: "3-node 24CPU/96Gi, 12 clusters → simpleScalable",
			in: func() sizerEvalInput {
				in := sizerOKStorage()
				in.NodeCount = 3
				in.ReadySchedulableCount = 3
				in.CPUAllocatableMillicores = 24000
				in.MemoryAllocatableBytes = 96 << 30
				in.CPURequestsMillicores = 6000
				in.MemoryRequestsBytes = 16 << 30
				in.ConnectedClusters = 12
				return in
			}(),
			want: want{
				grafana:     "pass",
				loki:        "pass",
				lokiMode:    sizerModeSimpleScalable,
				leftoverCPU: 18000,
				leftoverMem: (96 << 30) - (16 << 30),
				usableCPU:   16500,
				usableMem:   ((96 << 30) - (16 << 30)) - 3*(512<<20),
				series:      12 * sizerSeriesPerCluster,
				logBytes:    12 * sizerLogBytesPerClusterPerDay,
				rateMB:      2,
				burstMB:     4,
				globalMB:    32,
				streams:     20000,
				lokiWarning: "wal_capacity_unchecked",
			},
		},
		{
			name: "3-node 40 clusters → above_hosted_scale",
			in: func() sizerEvalInput {
				in := sizerOKStorage()
				in.NodeCount = 3
				in.ReadySchedulableCount = 3
				in.CPUAllocatableMillicores = 24000
				in.MemoryAllocatableBytes = 96 << 30
				in.CPURequestsMillicores = 6000
				in.MemoryRequestsBytes = 16 << 30
				in.ConnectedClusters = 40
				return in
			}(),
			want: want{
				grafana:     "pass",
				loki:        "fail",
				lokiReason:  "above_hosted_scale",
				leftoverCPU: 18000,
				leftoverMem: (96 << 30) - (16 << 30),
				usableCPU:   16500,
				usableMem:   ((96 << 30) - (16 << 30)) - 3*(512<<20),
				series:      40 * sizerSeriesPerCluster,
				logBytes:    40 * sizerLogBytesPerClusterPerDay,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := evaluateSizer(tc.in)
			if got.Verdicts.Grafana.Result != tc.want.grafana {
				t.Errorf("grafana result = %q, want %q reasons=%v", got.Verdicts.Grafana.Result, tc.want.grafana, got.Verdicts.Grafana.Reasons)
			}
			if got.Verdicts.Loki.Result != tc.want.loki {
				t.Errorf("loki result = %q, want %q reasons=%v", got.Verdicts.Loki.Result, tc.want.loki, got.Verdicts.Loki.Reasons)
			}
			gotMode := ""
			if got.Verdicts.Loki.Mode != nil {
				gotMode = *got.Verdicts.Loki.Mode
			}
			if gotMode != tc.want.lokiMode {
				t.Errorf("loki mode = %q, want %q", gotMode, tc.want.lokiMode)
			}
			if tc.want.loki == "fail" {
				if got.Verdicts.Loki.Mode != nil {
					t.Errorf("loki mode want null on fail, got %q", gotMode)
				}
				if len(got.Verdicts.Loki.Reasons) != 1 || got.Verdicts.Loki.Reasons[0] != tc.want.lokiReason {
					t.Errorf("loki reasons = %v, want [%s]", got.Verdicts.Loki.Reasons, tc.want.lokiReason)
				}
			}
			if gotMode == sizerModeSimpleScalable && tc.want.lokiMode == sizerModeSingleBinary {
				t.Fatal("selected SimpleScalable when fixture requires singleBinary")
			}
			if got.Leftover.CPUMillicores != tc.want.leftoverCPU || got.Leftover.MemoryBytes != tc.want.leftoverMem {
				t.Errorf("leftover = %d/%d, want %d/%d", got.Leftover.CPUMillicores, got.Leftover.MemoryBytes, tc.want.leftoverCPU, tc.want.leftoverMem)
			}
			if got.Usable.CPUMillicores != tc.want.usableCPU || got.Usable.MemoryBytes != tc.want.usableMem {
				t.Errorf("usable = %d/%d, want %d/%d", got.Usable.CPUMillicores, got.Usable.MemoryBytes, tc.want.usableCPU, tc.want.usableMem)
			}
			if got.Estimates.PrometheusSeries != tc.want.series {
				t.Errorf("prometheusSeries = %d, want %d", got.Estimates.PrometheusSeries, tc.want.series)
			}
			if got.Estimates.LogBytesPerDay != tc.want.logBytes {
				t.Errorf("logBytesPerDay = %d, want %d", got.Estimates.LogBytesPerDay, tc.want.logBytes)
			}
			wantMBps := float64(tc.want.logBytes) / 86400.0 / 1_000_000.0
			if got.Estimates.LogMBps != wantMBps {
				t.Errorf("logMBps = %v, want %v", got.Estimates.LogMBps, wantMBps)
			}
			if got.Verdicts.ThanosReceive.Result != "fail" || len(got.Verdicts.ThanosReceive.Reasons) != 1 || got.Verdicts.ThanosReceive.Reasons[0] != "receive_not_offered" {
				t.Errorf("thanosReceive = %+v, want fail receive_not_offered", got.Verdicts.ThanosReceive)
			}
			if got.Caps.GrafanaQueryTimeoutSeconds != 60 {
				t.Errorf("grafanaQueryTimeoutSeconds = %d, want 60", got.Caps.GrafanaQueryTimeoutSeconds)
			}
			if got.Caps.LokiIngestionRateMBPerTenant != tc.want.rateMB ||
				got.Caps.LokiIngestionBurstMBPerTenant != tc.want.burstMB ||
				got.Caps.LokiGlobalBudgetMBPerSec != tc.want.globalMB ||
				got.Caps.LokiMaxGlobalStreamsPerTenant != tc.want.streams {
				t.Errorf("loki caps rate/burst/global/streams = %d/%d/%d/%d, want %d/%d/%d/%d",
					got.Caps.LokiIngestionRateMBPerTenant, got.Caps.LokiIngestionBurstMBPerTenant,
					got.Caps.LokiGlobalBudgetMBPerSec, got.Caps.LokiMaxGlobalStreamsPerTenant,
					tc.want.rateMB, tc.want.burstMB, tc.want.globalMB, tc.want.streams)
			}
			if tc.want.lokiWarning != "" {
				if !stringSliceContains(got.Verdicts.Loki.Warnings, tc.want.lokiWarning) {
					t.Errorf("loki warnings = %v, want %q", got.Verdicts.Loki.Warnings, tc.want.lokiWarning)
				}
			}
		})
	}
}

func TestEvaluateSizerLogMBpsUnits(t *testing.T) {
	// 2 GiB/day/cluster = 2 * 2^30 / 86400 / 1e6 ≈ 0.0248 MB/s
	in := sizerOKStorage()
	in.ReadySchedulableCount = 3
	in.CPUAllocatableMillicores = 24000
	in.MemoryAllocatableBytes = 96 << 30
	in.ConnectedClusters = 1
	got := evaluateSizer(in)
	want := float64(2<<30) / 86400.0 / 1_000_000.0
	if got.Estimates.LogMBps != want {
		t.Fatalf("logMBps = %v, want %v", got.Estimates.LogMBps, want)
	}
	if got.Estimates.LogMBps < 0.024 || got.Estimates.LogMBps > 0.026 {
		t.Fatalf("logMBps = %v, want ~0.0248 for 2GiB/day", got.Estimates.LogMBps)
	}
}

func TestEvaluateSizerFirstMatchWins(t *testing.T) {
	t.Parallel()
	small := sizerEvalInput{
		ReadySchedulableCount:    1,
		CPUAllocatableMillicores: 4000,
		MemoryAllocatableBytes:   8 << 30,
		CPURequestsMillicores:    2100,
		MemoryRequestsBytes:      (4 << 30) + (512 << 20),
		ConnectedClusters:        3,
	}
	t.Run("object_storage_missing before single_node_small", func(t *testing.T) {
		in := small
		in.StorageClassRWO = true
		got := evaluateSizer(in)
		if got.Verdicts.Loki.Result != "fail" || len(got.Verdicts.Loki.Reasons) != 1 || got.Verdicts.Loki.Reasons[0] != "object_storage_missing" {
			t.Fatalf("reasons = %v, want object_storage_missing", got.Verdicts.Loki.Reasons)
		}
	})
	t.Run("storage_class_not_rwo before single_node_small", func(t *testing.T) {
		in := small
		in.ObjectStorageOK = true
		got := evaluateSizer(in)
		if got.Verdicts.Loki.Reasons[0] != "storage_class_not_rwo" {
			t.Fatalf("reasons = %v, want storage_class_not_rwo", got.Verdicts.Loki.Reasons)
		}
	})
	t.Run("pod_list_truncated before single_node_small", func(t *testing.T) {
		in := small
		in.ObjectStorageOK = true
		in.StorageClassRWO = true
		in.PodListTruncated = true
		got := evaluateSizer(in)
		if got.Verdicts.Loki.Reasons[0] != "pod_list_truncated" {
			t.Fatalf("reasons = %v, want pod_list_truncated", got.Verdicts.Loki.Reasons)
		}
		if !stringSliceContains(got.Verdicts.Grafana.Warnings, "pod_list_truncated") {
			t.Fatalf("grafana warnings = %v, want pod_list_truncated", got.Verdicts.Grafana.Warnings)
		}
	})
	t.Run("nodes_unreadable grafana fail", func(t *testing.T) {
		in := small
		in.NodesUnreadable = true
		in.ObjectStorageOK = true
		in.StorageClassRWO = true
		got := evaluateSizer(in)
		if got.Verdicts.Grafana.Result != "fail" || got.Verdicts.Grafana.Reasons[0] != "nodes_unreadable" {
			t.Fatalf("grafana = %+v", got.Verdicts.Grafana)
		}
	})
	t.Run("fat 1-node is not SimpleScalable", func(t *testing.T) {
		in := sizerOKStorage()
		in.ReadySchedulableCount = 1
		in.CPUAllocatableMillicores = 32000
		in.MemoryAllocatableBytes = 128 << 30
		in.ConnectedClusters = 12 // would pick simpleScalable if node count allowed
		got := evaluateSizer(in)
		if got.Verdicts.Loki.Result != "fail" || got.Verdicts.Loki.Reasons[0] != "above_hosted_scale" {
			t.Fatalf("1-node with 12 clusters should fail above_hosted_scale (cannot simpleScalable), got %+v", got.Verdicts.Loki)
		}
	})
	t.Run("wal cache too small fails after tentative mode", func(t *testing.T) {
		in := sizerOKStorage()
		in.ReadySchedulableCount = 1
		in.CPUAllocatableMillicores = 16000
		in.MemoryAllocatableBytes = 64 << 30
		in.ConnectedClusters = 3
		in.WALCapacityKnown = true
		in.WALCapacityBytes = 1 << 30
		got := evaluateSizer(in)
		if got.Verdicts.Loki.Result != "fail" || got.Verdicts.Loki.Reasons[0] != "wal_too_small" {
			t.Fatalf("got %+v, want wal_too_small", got.Verdicts.Loki)
		}
	})
	t.Run("clusters_unreadable before singleBinary", func(t *testing.T) {
		in := sizerOKStorage()
		in.ReadySchedulableCount = 3
		in.CPUAllocatableMillicores = 24000
		in.MemoryAllocatableBytes = 96 << 30
		in.ClustersUnreadable = true
		got := evaluateSizer(in)
		if got.Verdicts.Loki.Result != "fail" || got.Verdicts.Loki.Reasons[0] != "clusters_unreadable" {
			t.Fatalf("got %+v, want clusters_unreadable", got.Verdicts.Loki)
		}
	})
	t.Run("grafana tight_fit when leftover passes but usable does not", func(t *testing.T) {
		in := sizerEvalInput{
			ReadySchedulableCount:    1,
			CPUAllocatableMillicores: 800,
			MemoryAllocatableBytes:   512 << 20,
			CPURequestsMillicores:    500, // leftover 300m ≥ 250m
			MemoryRequestsBytes:      200 << 20,
		}
		got := evaluateSizer(in)
		if got.Verdicts.Grafana.Result != "pass" {
			t.Fatalf("grafana = %+v, want pass", got.Verdicts.Grafana)
		}
		if !stringSliceContains(got.Verdicts.Grafana.Warnings, "tight_fit") {
			t.Fatalf("warnings = %v, want tight_fit", got.Verdicts.Grafana.Warnings)
		}
	})
}

func TestComputedLokiPrefix(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want string }{
		{"", "loki"},
		{"/", "loki"},
		{"  ", "loki"},
		{"metrics", "metrics/loki"},
		{"metrics/", "metrics/loki"},
		{"/metrics", "metrics/loki"},
		{"thanos/blocks", "thanos/blocks/loki"},
	}
	for _, tc := range cases {
		if got := computedLokiPrefix(tc.in); got != tc.want {
			t.Errorf("computedLokiPrefix(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSizerResponseCamelCaseJSON(t *testing.T) {
	t.Parallel()
	in := sizerOKStorage()
	in.ReadySchedulableCount = 1
	in.CPUAllocatableMillicores = 4000
	in.MemoryAllocatableBytes = 8 << 30
	in.CPURequestsMillicores = 2100
	in.MemoryRequestsBytes = (4 << 30) + (512 << 20)
	in.ConnectedClusters = 3
	eval := evaluateSizer(in)
	resp := MonitoringSizerResponse{
		ManagementClusterID: "mg",
		IsLocal:             true,
		KubernetesVersion:   "v1.31.0",
		Nodes: sizerNodes{
			Count:                    1,
			ReadySchedulableCount:    1,
			CPUAllocatableMillicores: 4000,
			MemoryAllocatableBytes:   8 << 30,
		},
		RequestsInUse:     sizerResource{CPUMillicores: 2100, MemoryBytes: in.MemoryRequestsBytes},
		Leftover:          eval.Leftover,
		Reserve:           eval.Reserve,
		Usable:            eval.Usable,
		StorageClass:      sizerStorageClass{Name: "local-path", RWO: true},
		ObjectStorage:     sizerObjectStorage{Configured: true, StorageConfigID: "cfg", ComputedLokiPrefix: "loki"},
		ConnectedClusters: 3,
		Thanos:            sizerThanos{Status: "healthy", QueryURL: "http://thanos-query-frontend.monitoring.svc.cluster.local:9090"},
		Estimates:         eval.Estimates,
		Verdicts:          eval.Verdicts,
		Caps:              eval.Caps,
	}
	raw, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	for _, key := range []string{
		`"managementClusterId"`, `"isLocal"`, `"kubernetesVersion"`,
		`"readySchedulableCount"`, `"cpuAllocatableMillicores"`, `"memoryAllocatableBytes"`,
		`"requestsInUse"`, `"cpuMillicores"`, `"podListTruncated"`,
		`"storageClass"`, `"allowVolumeExpansion"`, `"walCapacityKnown"`, `"walCapacityBytes"`,
		`"objectStorage"`, `"storageConfigId"`, `"computedLokiPrefix"`,
		`"connectedClusters"`, `"queryUrl"`, `"prometheusSeries"`, `"logBytesPerDay"`, `"logMBps"`,
		`"skipDiskCheck"`, `"thanosReceive"`, `"lokiIngestionRateMBPerTenant"`,
		`"grafanaQueryTimeoutSeconds"`,
	} {
		if !strings.Contains(s, key) {
			t.Errorf("JSON missing camelCase key %s in %s", key, s)
		}
	}
	for _, snake := range []string{
		`"management_cluster_id"`, `"storage_config_id"`, `"log_mbps"`, `"thanos_receive"`,
	} {
		if strings.Contains(s, snake) {
			t.Errorf("JSON has snake_case %s", snake)
		}
	}
	if !strings.Contains(s, `"mode":null`) {
		t.Errorf("loki fail mode should be JSON null, got %s", s)
	}
}

func TestGetMonitoringSizerRequiresMonitoringRead(t *testing.T) {
	h := NewMonitoringHandler()
	h.SetAuthorization(rbac.NewEngine(), stubMonitoringRBACQuerier{bindings: denyMonitoring()})
	rec := httptest.NewRecorder()
	req := sizerAuthedRequest(http.MethodGet, "/api/v1/settings/monitoring/sizer/")
	h.GetMonitoringSizer(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", rec.Code, rec.Body.String())
	}

	h2 := NewMonitoringHandler()
	h2.SetAuthorization(rbac.NewEngine(), stubMonitoringRBACQuerier{bindings: grantMonitoring()})
	rec2 := httptest.NewRecorder()
	h2.GetMonitoringSizer(rec2, sizerAuthedRequest(http.MethodGet, "/api/v1/settings/monitoring/sizer/"))
	if rec2.Code == http.StatusForbidden {
		t.Fatalf("monitoring:read also 403: %s", rec2.Body.String())
	}
	if rec2.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec2.Code, rec2.Body.String())
	}
}

func TestGetMonitoringSizerNeverMutatesDisks(t *testing.T) {
	clusterID := uuid.MustParse(stackTestClusterID)
	storageID := uuid.MustParse(stackTestStorageID)
	q := &sizerTestQuerier{
		clusters: []sqlc.Cluster{{
			ID:                clusterID,
			Name:              "local",
			IsLocal:           true,
			KubernetesVersion: "v1.31.4",
			LastHeartbeat:     pgtype.Timestamptz{Time: time.Now(), Valid: true},
		}},
		backend: sqlc.MonitoringBackend{
			ID:         uuid.New(),
			AuthConfig: json.RawMessage(`{"sharedThanos":{"status":"healthy","managementClusterId":"` + clusterID.String() + `","storageConfigId":"` + storageID.String() + `"}}`),
		},
		storage: sqlc.BackupStorageConfig{
			ID:     storageID,
			Bucket: "metrics",
			Prefix: "",
		},
	}
	k8s := &sizerK8sFake{t: t}
	h := NewMonitoringHandlerWithQueries(q, k8s)
	h.SetAuthorization(rbac.NewEngine(), stubMonitoringRBACQuerier{bindings: grantMonitoring()})
	rec := httptest.NewRecorder()
	req := sizerAuthedRequest(http.MethodGet, "/api/v1/settings/monitoring/sizer/?skipDiskCheck=true")
	h.GetMonitoringSizer(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if len(k8s.calls) == 0 {
		t.Fatal("expected GET nodes/pods/storageclasses")
	}
	for _, call := range k8s.calls {
		if !strings.HasPrefix(call, http.MethodGet+" ") {
			t.Errorf("GET /sizer/ must not mutate disks, saw %s", call)
		}
		if strings.Contains(call, "/persistentvolumeclaims") {
			t.Errorf("GET /sizer/ touched PVCs: %s", call)
		}
	}
	var wrap struct {
		Data MonitoringSizerResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &wrap); err != nil {
		t.Fatalf("decode: %v body=%s", err, rec.Body.String())
	}
	if wrap.Data.SkipDiskCheck != true {
		t.Errorf("skipDiskCheck = %v, want true", wrap.Data.SkipDiskCheck)
	}
	if wrap.Data.StorageClass.WALCapacityKnown {
		t.Errorf("walCapacityKnown should be false without cache")
	}
	if wrap.Data.ObjectStorage.ComputedLokiPrefix != "loki" {
		t.Errorf("computedLokiPrefix = %q", wrap.Data.ObjectStorage.ComputedLokiPrefix)
	}
	if wrap.Data.Verdicts.ThanosReceive.Reasons[0] != "receive_not_offered" {
		t.Errorf("thanosReceive = %+v", wrap.Data.Verdicts.ThanosReceive)
	}
	body := rec.Body.String()
	for _, secret := range []string{"access_key", "secret_key", "encryptedCredentials", "encrypted_credentials"} {
		if strings.Contains(body, secret) {
			t.Errorf("response leaked %s: %s", secret, body)
		}
	}
}

func TestSizerCollectorsReadyNodesAndPodRequests(t *testing.T) {
	clusterID := uuid.New().String()
	k8s := &sizerK8sFake{
		t: t,
		nodes: []corev1.Node{
			sizerTestNode("ready", "4", "8Gi", true, false),
			sizerTestNode("unsched", "4", "8Gi", true, true),
			sizerTestNode("notready", "4", "8Gi", false, false),
		},
		pods: []corev1.Pod{
			sizerTestPod("p1", "100m", "128Mi", "200m", "256Mi"),
			sizerTestPod("p2", "", "", "1", "1Gi"), // limits only → 0
			sizerTestPod("p3", "50m", "64Mi", "", ""),
		},
		storage: storageClassWire{Metadata: struct {
			Name        string            `json:"name"`
			Annotations map[string]string `json:"annotations"`
		}{Name: "local-path", Annotations: map[string]string{"storageclass.kubernetes.io/is-default-class": "true"}}},
	}
	h := NewMonitoringHandlerWithRequester(k8s)
	sum, err := h.sizerListReadyNodes(context.Background(), clusterID)
	if err != nil {
		t.Fatal(err)
	}
	if sum.total != 3 || sum.ready != 1 {
		t.Fatalf("nodes total/ready = %d/%d, want 3/1", sum.total, sum.ready)
	}
	if sum.cpuMilli != 4000 || sum.memBytes != 8<<30 {
		t.Fatalf("allocatable = %d/%d, want 4000/%d", sum.cpuMilli, sum.memBytes, int64(8<<30))
	}
	cpu, mem, truncated, err := h.sizerSumPodRequests(context.Background(), clusterID)
	if err != nil {
		t.Fatal(err)
	}
	if truncated {
		t.Fatal("truncated")
	}
	if cpu != 150 || mem != (128<<20)+(64<<20) {
		t.Fatalf("requests = %d/%d, want 150m + 192Mi (limits ignored)", cpu, mem)
	}
}

func TestSizerPodListHonorsContinueAndCap(t *testing.T) {
	clusterID := uuid.New().String()
	pods := make([]corev1.Pod, sizerPodListCap+10)
	for i := range pods {
		pods[i] = sizerTestPod(fmt.Sprintf("p%d", i), "1m", "1Mi", "", "")
	}
	k8s := &sizerK8sFake{t: t, pods: pods}
	h := NewMonitoringHandlerWithRequester(k8s)
	_, _, truncated, err := h.sizerSumPodRequests(context.Background(), clusterID)
	if err != nil {
		t.Fatal(err)
	}
	if !truncated {
		t.Fatal("want podListTruncated at 5k cap")
	}
	var sawContinue bool
	for _, c := range k8s.calls {
		if strings.Contains(c, "continue=") {
			sawContinue = true
		}
	}
	if !sawContinue {
		t.Fatalf("paginated list must honor Continue, calls=%v", k8s.calls)
	}
}

func TestCountConnectedAdoptedClustersSkipsLocal(t *testing.T) {
	now := time.Now()
	hb := func(age time.Duration) pgtype.Timestamptz {
		return pgtype.Timestamptz{Time: now.Add(-age), Valid: true}
	}
	clusters := []sqlc.Cluster{
		{ID: uuid.New(), Name: "local", IsLocal: true, LastHeartbeat: hb(time.Minute)},
		{ID: uuid.New(), Name: "m1", LastHeartbeat: hb(time.Minute)},
		{ID: uuid.New(), Name: "m2", LastHeartbeat: hb(time.Minute)},
		{ID: uuid.New(), Name: "m3", LastHeartbeat: hb(time.Minute)},
		{ID: uuid.New(), Name: "m4", LastHeartbeat: hb(time.Minute)},
		{ID: uuid.New(), Name: "m5", LastHeartbeat: hb(time.Minute)},
		{ID: uuid.New(), Name: "stale", LastHeartbeat: hb(10 * time.Minute)},
		{ID: uuid.New(), Name: "nohb"},
	}
	if got := countConnectedAdoptedClusters(clusters, now); got != 5 {
		t.Fatalf("connectedClusters = %d, want 5 (local + stale + no heartbeat excluded)", got)
	}
}

func TestSizerListClustersFailClosed(t *testing.T) {
	storageID := uuid.New()
	clusterID := uuid.MustParse(stackTestClusterID)
	backend := sqlc.MonitoringBackend{
		ID:         uuid.New(),
		AuthConfig: json.RawMessage(`{"sharedThanos":{"status":"healthy","managementClusterId":"` + clusterID.String() + `","storageConfigId":"` + storageID.String() + `"}}`),
	}
	storage := sqlc.BackupStorageConfig{ID: storageID, Bucket: "metrics"}
	k8s := &sizerK8sFake{
		t:     t,
		nodes: []corev1.Node{sizerTestNode("ready", "16", "64Gi", true, false)},
		storage: storageClassWire{Metadata: struct {
			Name        string            `json:"name"`
			Annotations map[string]string `json:"annotations"`
		}{Name: "local-path", Annotations: map[string]string{"storageclass.kubernetes.io/is-default-class": "true"}}},
	}

	t.Run("missing lister", func(t *testing.T) {
		q := &sizerNoListQuerier{backend: backend, storage: storage}
		h := NewMonitoringHandlerWithQueries(q, k8s)
		if _, err := h.sizerListAllClusters(context.Background()); err == nil {
			t.Fatal("want error when querier cannot list clusters")
		}
		snap := h.collectSizerSnapshot(context.Background(), "", storageID.String())
		if !snap.input.ClustersUnreadable {
			t.Fatal("want ClustersUnreadable when ListClusters is missing")
		}
		got := evaluateSizer(snap.input)
		if got.Verdicts.Loki.Result == "pass" {
			t.Fatalf("Loki must not pass when fleet is unreadable: %+v", got.Verdicts.Loki)
		}
		if got.Verdicts.Loki.Reasons[0] != "clusters_unreadable" {
			t.Fatalf("reasons = %v, want clusters_unreadable", got.Verdicts.Loki.Reasons)
		}
	})
	t.Run("list error", func(t *testing.T) {
		q := &sizerErrListQuerier{
			sizerTestQuerier: sizerTestQuerier{backend: backend, storage: storage},
			err:              fmt.Errorf("db down"),
		}
		h := NewMonitoringHandlerWithQueries(q, k8s)
		if _, err := h.sizerListAllClusters(context.Background()); err == nil {
			t.Fatal("want list error")
		}
		snap := h.collectSizerSnapshot(context.Background(), "", storageID.String())
		if !snap.input.ClustersUnreadable {
			t.Fatal("want ClustersUnreadable on ListClusters error")
		}
		got := evaluateSizer(snap.input)
		if got.Verdicts.Loki.Reasons[0] != "clusters_unreadable" {
			t.Fatalf("reasons = %v, want clusters_unreadable", got.Verdicts.Loki.Reasons)
		}
	})
}

func TestPodRequestTotalsKubeletFormula(t *testing.T) {
	always := corev1.ContainerRestartPolicyAlways
	pod := corev1.Pod{Spec: corev1.PodSpec{
		InitContainers: []corev1.Container{
			{Name: "init-small", Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{
				corev1.ResourceCPU: resource.MustParse("100m"), corev1.ResourceMemory: resource.MustParse("64Mi"),
			}}},
			{Name: "init-big", Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{
				corev1.ResourceCPU: resource.MustParse("200m"), corev1.ResourceMemory: resource.MustParse("128Mi"),
			}}},
			{Name: "sidecar", RestartPolicy: &always, Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{
				corev1.ResourceCPU: resource.MustParse("50m"), corev1.ResourceMemory: resource.MustParse("32Mi"),
			}}},
		},
		Containers: []corev1.Container{
			{Name: "app", Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{
				corev1.ResourceCPU: resource.MustParse("80m"), corev1.ResourceMemory: resource.MustParse("40Mi"),
			}}},
		},
	}}
	cpu, mem := podRequestTotals(pod)
	// max(100,200) + 80 + 50 = 330m; max(64,128)+40+32 = 200Mi
	if cpu != 330 {
		t.Fatalf("cpu = %d, want 330 (max init + app + sidecar, not sum of inits)", cpu)
	}
	if mem != 200<<20 {
		t.Fatalf("mem = %d, want 200Mi", mem)
	}
}

func TestSizerStorageClassRWOFromAccessModes(t *testing.T) {
	got := storageClassFromWire(storageClassWire{
		Metadata: struct {
			Name        string            `json:"name"`
			Annotations map[string]string `json:"annotations"`
		}{Name: "nfs"},
		AccessModes: []string{"ReadWriteMany"},
	})
	if got.rwo {
		t.Fatal("RWX-only class must not count as RWO")
	}
	got = storageClassFromWire(storageClassWire{
		Metadata: struct {
			Name        string            `json:"name"`
			Annotations map[string]string `json:"annotations"`
		}{Name: "local-path"},
	})
	if !got.rwo {
		t.Fatal("class without accessModes is treated as RWO")
	}
}

type sizerTestQuerier struct {
	MonitoringQuerier
	clusters []sqlc.Cluster
	backend  sqlc.MonitoringBackend
	storage  sqlc.BackupStorageConfig
}

func (q *sizerTestQuerier) ListClusters(context.Context, sqlc.ListClustersParams) ([]sqlc.Cluster, error) {
	return q.clusters, nil
}

func (q *sizerTestQuerier) GetDefaultMonitoringBackend(context.Context) (sqlc.MonitoringBackend, error) {
	return q.backend, nil
}

func (q *sizerTestQuerier) GetBackupStorageConfigByID(context.Context, uuid.UUID) (sqlc.BackupStorageConfig, error) {
	return q.storage, nil
}

func (q *sizerTestQuerier) GetClusterMonitoringContext(context.Context, uuid.UUID) (sqlc.GetClusterMonitoringContextRow, error) {
	return sqlc.GetClusterMonitoringContextRow{}, fmt.Errorf("no context")
}

// sizerNoListQuerier satisfies MonitoringQuerier but not sizerClusterLister.
type sizerNoListQuerier struct {
	MonitoringQuerier
	backend sqlc.MonitoringBackend
	storage sqlc.BackupStorageConfig
}

func (q *sizerNoListQuerier) GetDefaultMonitoringBackend(context.Context) (sqlc.MonitoringBackend, error) {
	return q.backend, nil
}

func (q *sizerNoListQuerier) GetBackupStorageConfigByID(context.Context, uuid.UUID) (sqlc.BackupStorageConfig, error) {
	return q.storage, nil
}

func (q *sizerNoListQuerier) GetClusterMonitoringContext(context.Context, uuid.UUID) (sqlc.GetClusterMonitoringContextRow, error) {
	return sqlc.GetClusterMonitoringContextRow{}, fmt.Errorf("no context")
}

type sizerErrListQuerier struct {
	sizerTestQuerier
	err error
}

func (q *sizerErrListQuerier) ListClusters(context.Context, sqlc.ListClustersParams) ([]sqlc.Cluster, error) {
	return nil, q.err
}

type sizerK8sFake struct {
	t       *testing.T
	calls   []string
	nodes   []corev1.Node
	pods    []corev1.Pod
	storage storageClassWire
}

func (f *sizerK8sFake) Do(_ context.Context, _, method, path string, _ []byte, _ map[string]string) (*protocol.K8sResponsePayload, error) {
	f.calls = append(f.calls, method+" "+path)
	if strings.Contains(path, "persistentvolumeclaims") {
		return sizerWALProbeResponse(method), nil
	}
	if method == http.MethodDelete && (strings.Contains(path, "/ingresses/") || strings.Contains(path, "/certificates/") || strings.Contains(path, "/httproutes/") || strings.Contains(path, "/referencegrants/")) {
		return &protocol.K8sResponsePayload{StatusCode: http.StatusNotFound}, nil
	}
	if method != http.MethodGet {
		f.t.Errorf("sizer issued mutating call %s %s", method, path)
		return &protocol.K8sResponsePayload{StatusCode: http.StatusMethodNotAllowed}, nil
	}
	switch {
	case path == "/api/v1/nodes":
		return sizerK8sJSON(corev1.NodeList{Items: f.nodes}), nil
	case strings.HasPrefix(path, "/api/v1/pods"):
		return sizerK8sJSON(f.podPage(path)), nil
	case path == "/apis/storage.k8s.io/v1/storageclasses":
		items := []storageClassWire{f.storage}
		return sizerK8sJSON(map[string]any{"items": items}), nil
	case strings.HasPrefix(path, "/apis/storage.k8s.io/v1/storageclasses/"):
		name := strings.TrimPrefix(path, "/apis/storage.k8s.io/v1/storageclasses/")
		if f.storage.Metadata.Name != "" && f.storage.Metadata.Name != name && name == sizerDefaultStorageClass {
			return &protocol.K8sResponsePayload{StatusCode: http.StatusNotFound, Body: base64.StdEncoding.EncodeToString([]byte(`{"kind":"Status"}`))}, nil
		}
		sc := f.storage
		if sc.Metadata.Name == "" {
			sc.Metadata.Name = name
		}
		return sizerK8sJSON(sc), nil
	default:
		return &protocol.K8sResponsePayload{StatusCode: http.StatusNotFound}, nil
	}
}

func sizerWALProbeResponse(method string) *protocol.K8sResponsePayload {
	switch method {
	case http.MethodPost:
		return sizerK8sJSON(map[string]any{"status": map[string]any{"phase": "Bound"}})
	case http.MethodGet:
		return sizerK8sJSON(map[string]any{"status": map[string]any{"phase": "Bound"}})
	case http.MethodDelete:
		return &protocol.K8sResponsePayload{StatusCode: http.StatusOK}
	default:
		return &protocol.K8sResponsePayload{StatusCode: http.StatusMethodNotAllowed}
	}
}

func (f *sizerK8sFake) podPage(path string) corev1.PodList {
	u := path
	cont := ""
	if i := strings.Index(u, "continue="); i >= 0 {
		cont = u[i+len("continue="):]
		if amp := strings.IndexByte(cont, '&'); amp >= 0 {
			cont = cont[:amp]
		}
	}
	start := 0
	if cont != "" {
		fmt.Sscanf(cont, "%d", &start)
	}
	end := start + sizerPodListPage
	if end > len(f.pods) {
		end = len(f.pods)
	}
	if start > len(f.pods) {
		start = len(f.pods)
		end = start
	}
	list := corev1.PodList{Items: f.pods[start:end]}
	if end < len(f.pods) {
		list.Continue = fmt.Sprintf("%d", end)
	}
	return list
}

func sizerK8sJSON(v any) *protocol.K8sResponsePayload {
	raw, _ := json.Marshal(v)
	return &protocol.K8sResponsePayload{
		StatusCode: http.StatusOK,
		Body:       base64.StdEncoding.EncodeToString(raw),
	}
}

func sizerTestNode(name, cpu, mem string, ready, unsched bool) corev1.Node {
	status := corev1.ConditionFalse
	if ready {
		status = corev1.ConditionTrue
	}
	return corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec:       corev1.NodeSpec{Unschedulable: unsched},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse(cpu),
				corev1.ResourceMemory: resource.MustParse(mem),
			},
			Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: status}},
			NodeInfo:   corev1.NodeSystemInfo{KubeletVersion: "v1.31.4"},
		},
	}
}

func sizerTestPod(name, reqCPU, reqMem, limCPU, limMem string) corev1.Pod {
	reqs := corev1.ResourceList{}
	lims := corev1.ResourceList{}
	if reqCPU != "" {
		reqs[corev1.ResourceCPU] = resource.MustParse(reqCPU)
	}
	if reqMem != "" {
		reqs[corev1.ResourceMemory] = resource.MustParse(reqMem)
	}
	if limCPU != "" {
		lims[corev1.ResourceCPU] = resource.MustParse(limCPU)
	}
	if limMem != "" {
		lims[corev1.ResourceMemory] = resource.MustParse(limMem)
	}
	return corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{
				Name:      "c",
				Resources: corev1.ResourceRequirements{Requests: reqs, Limits: lims},
			}},
		},
	}
}
