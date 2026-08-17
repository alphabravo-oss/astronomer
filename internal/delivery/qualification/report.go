// Package qualification provides deterministic, bounded qualification
// workloads for the delivery control plane. The reports deliberately identify
// simulation evidence separately from live, multi-replica evidence: a fast
// local pass is a release prerequisite, never a substitute for the 24-hour
// production-like soak and failure drills.
package qualification

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"syscall"
	"time"
)

const ReportSchemaVersion = "astronomer-delivery-qualification/v1"

type Environment struct {
	Commit       string `json:"commit"`
	Dirty        bool   `json:"dirty"`
	GoVersion    string `json:"go_version"`
	OS           string `json:"os"`
	Architecture string `json:"architecture"`
	CPUs         int    `json:"cpus"`
}

type Dataset struct {
	Clusters               int `json:"clusters"`
	ConnectedAgents        int `json:"connected_agents"`
	ServerReplicas         int `json:"server_replicas"`
	WorkerReplicas         int `json:"worker_replicas"`
	Deployments            int `json:"deployments"`
	StatusEventsPerCluster int `json:"status_events_per_cluster"`
}

type Distribution struct {
	Unit    string  `json:"unit"`
	Samples int     `json:"samples"`
	Min     float64 `json:"min"`
	P50     float64 `json:"p50"`
	P95     float64 `json:"p95"`
	P99     float64 `json:"p99"`
	Max     float64 `json:"max"`
}

type Resources struct {
	HeapAllocPeakBytes  uint64 `json:"heap_alloc_peak_bytes"`
	HeapSysPeakBytes    uint64 `json:"heap_sys_peak_bytes"`
	ProcessRSSPeakBytes uint64 `json:"process_rss_peak_bytes,omitempty"`
	DiskAvailableBefore uint64 `json:"disk_available_before_bytes,omitempty"`
	DiskAvailableAfter  uint64 `json:"disk_available_after_bytes,omitempty"`
}

type ScaleReport struct {
	SchemaVersion   string                  `json:"schema_version"`
	Kind            string                  `json:"kind"`
	EvidenceScope   string                  `json:"evidence_scope"`
	ReleaseEligible bool                    `json:"release_eligible"`
	Status          string                  `json:"status"`
	StartedAt       time.Time               `json:"started_at"`
	CompletedAt     time.Time               `json:"completed_at"`
	DurationMS      int64                   `json:"duration_ms"`
	Command         []string                `json:"command"`
	Environment     Environment             `json:"environment"`
	Dataset         Dataset                 `json:"dataset"`
	Metrics         map[string]Distribution `json:"metrics"`
	Invariants      map[string]bool         `json:"invariants"`
	Resources       Resources               `json:"resources"`
	Errors          []string                `json:"errors"`
	Limitations     []string                `json:"limitations"`
}

type SoakSample struct {
	Sequence    int                     `json:"sequence"`
	StartedAt   time.Time               `json:"started_at"`
	CompletedAt time.Time               `json:"completed_at"`
	Status      string                  `json:"status"`
	Metrics     map[string]Distribution `json:"metrics"`
	Resources   Resources               `json:"resources"`
}

type SoakReport struct {
	SchemaVersion   string          `json:"schema_version"`
	Kind            string          `json:"kind"`
	EvidenceScope   string          `json:"evidence_scope"`
	ReleaseEligible bool            `json:"release_eligible"`
	Status          string          `json:"status"`
	StartedAt       time.Time       `json:"started_at"`
	CompletedAt     time.Time       `json:"completed_at,omitempty"`
	Requested       string          `json:"requested_duration"`
	Observed        string          `json:"observed_duration"`
	Interval        string          `json:"interval"`
	Command         []string        `json:"command"`
	Environment     Environment     `json:"environment"`
	Dataset         Dataset         `json:"dataset"`
	Samples         []SoakSample    `json:"samples"`
	Invariants      map[string]bool `json:"invariants"`
	Resources       Resources       `json:"resources"`
	Errors          []string        `json:"errors"`
	Limitations     []string        `json:"limitations"`
}

func CurrentEnvironment(commit string, dirty bool) Environment {
	return Environment{Commit: commit, Dirty: dirty, GoVersion: runtime.Version(), OS: runtime.GOOS, Architecture: runtime.GOARCH, CPUs: runtime.NumCPU()}
}

func distribution(unit string, values []time.Duration) Distribution {
	numbers := make([]float64, len(values))
	for index, value := range values {
		numbers[index] = float64(value.Nanoseconds()) / float64(time.Millisecond)
	}
	return numberDistribution(unit, numbers)
}

func numberDistribution(unit string, values []float64) Distribution {
	result := Distribution{Unit: unit, Samples: len(values)}
	if len(values) == 0 {
		return result
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	result.Min, result.Max = sorted[0], sorted[len(sorted)-1]
	result.P50 = percentile(sorted, 50)
	result.P95 = percentile(sorted, 95)
	result.P99 = percentile(sorted, 99)
	return result
}

func percentile(sorted []float64, percentile float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	index := int(math.Ceil(percentile/100*float64(len(sorted)))) - 1
	if index < 0 {
		index = 0
	}
	if index >= len(sorted) {
		index = len(sorted) - 1
	}
	return sorted[index]
}

func sampleResources(path string) Resources {
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	result := Resources{HeapAllocPeakBytes: memory.HeapAlloc, HeapSysPeakBytes: memory.HeapSys, ProcessRSSPeakBytes: processHighWaterBytes()}
	var filesystem syscall.Statfs_t
	if err := syscall.Statfs(path, &filesystem); err == nil {
		result.DiskAvailableBefore = filesystem.Bavail * uint64(filesystem.Bsize)
		result.DiskAvailableAfter = result.DiskAvailableBefore
	}
	return result
}

func mergeResourcePeaks(left, right Resources) Resources {
	left.HeapAllocPeakBytes = max(left.HeapAllocPeakBytes, right.HeapAllocPeakBytes)
	left.HeapSysPeakBytes = max(left.HeapSysPeakBytes, right.HeapSysPeakBytes)
	left.ProcessRSSPeakBytes = max(left.ProcessRSSPeakBytes, right.ProcessRSSPeakBytes)
	if left.DiskAvailableBefore == 0 {
		left.DiskAvailableBefore = right.DiskAvailableBefore
	}
	left.DiskAvailableAfter = right.DiskAvailableAfter
	return left
}

func processHighWaterBytes() uint64 {
	if runtime.GOOS != "linux" {
		return 0
	}
	raw, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return 0
	}
	var kib uint64
	for _, line := range splitLines(string(raw)) {
		if _, err := fmt.Sscanf(line, "VmHWM: %d kB", &kib); err == nil {
			return kib * 1024
		}
	}
	return 0
}

func splitLines(value string) []string {
	result := make([]string, 0, 32)
	start := 0
	for index := range value {
		if value[index] == '\n' {
			result = append(result, value[start:index])
			start = index + 1
		}
	}
	return result
}

// WriteJSONAtomic keeps an interrupted soak from leaving a report that looks
// complete. The destination must be an explicit regular-file path; symlinks
// are rejected so a qualification run cannot overwrite an unrelated target.
func WriteJSONAtomic(path string, value any) error {
	if path == "" {
		return errors.New("output path is required")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve output path: %w", err)
	}
	if info, statErr := os.Lstat(absolute); statErr == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return errors.New("output path must be a regular file, not a symlink")
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return fmt.Errorf("inspect output path: %w", statErr)
	}
	directory := filepath.Dir(absolute)
	if info, statErr := os.Stat(directory); statErr != nil || !info.IsDir() {
		return fmt.Errorf("output directory must already exist: %s", directory)
	}
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode report: %w", err)
	}
	raw = append(raw, '\n')
	temporary, err := os.CreateTemp(directory, ".qualification-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary report: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(raw); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write report: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync report: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, absolute); err != nil {
		return fmt.Errorf("publish report: %w", err)
	}
	return nil
}
