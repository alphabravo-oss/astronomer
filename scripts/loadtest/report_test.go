package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCertificationFailsWithoutMetadataAndDrillEvidence(t *testing.T) {
	t.Setenv("LOADTEST_DRILL_EVIDENCE_DIR", t.TempDir())
	cfg := &config{
		clusters: 1, duration: time.Minute, skipAgents: true, certification: true,
		day2FailureDrill: []string{"tunnel_owner_failure"},
	}
	report := newReport(cfg, newRecorder())
	if report.Verdict != "fail" {
		t.Fatalf("verdict = %q, reasons = %v", report.Verdict, report.Reasons)
	}
	joined := strings.Join(report.Reasons, "\n")
	if !strings.Contains(joined, "certification metadata missing") || !strings.Contains(joined, "tunnel_owner_failure") {
		t.Fatalf("reasons = %v", report.Reasons)
	}
}

func TestWriteFileCreatesMachineReportAndDigest(t *testing.T) {
	setCertificationMetadata(t)
	cfg := &config{server: "https://astronomer.example", profileName: "estate-100", clusters: 1, rps: 1, duration: time.Second, skipAgents: true}
	recorder := newRecorder()
	recorder.RecordHTTP("cluster_list", 200, 10*time.Millisecond, nil)
	report := newReport(cfg, recorder)
	path := filepath.Join(t.TempDir(), "report.md")
	if err := report.WriteFile(path); err != nil {
		t.Fatal(err)
	}
	for _, artifact := range []string{path, path + ".json", path + ".sha256"} {
		if info, err := os.Stat(artifact); err != nil || info.Size() == 0 {
			t.Fatalf("artifact %s: info=%v err=%v", artifact, info, err)
		}
	}
	raw, err := os.ReadFile(path + ".json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"schema_version": "astronomer-scale-report-v2"`) ||
		!strings.Contains(string(raw), `"generated_at":`) {
		t.Fatalf("machine report = %s", raw)
	}
	var machine struct {
		Profile  string                `json:"profile"`
		Metadata certificationMetadata `json:"metadata"`
	}
	if err := json.Unmarshal(raw, &machine); err != nil {
		t.Fatal(err)
	}
	if machine.Profile != "estate-100" || machine.Metadata.Commit != "test" ||
		machine.Metadata.RunID != "test" || machine.Metadata.DrillEvidenceRunID != "test" ||
		machine.Metadata.Images != "test" || machine.Metadata.Environment != "test" {
		t.Fatalf("machine provenance = %+v", machine)
	}
}

func TestDrillEvidenceRequiresExactFreshProvenance(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOADTEST_DRILL_EVIDENCE_DIR", dir)
	now := time.Date(2026, time.August, 24, 12, 0, 0, 0, time.UTC)
	metadata := certificationMetadata{
		Commit: "commit-a", DrillEvidenceRunID: "run-42", Images: "sha256:" + strings.Repeat("a", 64), Environment: "scale-certification",
		DrillEvidenceWorkflow: trustedDrillEvidenceWorkflow, DrillEvidenceConclusion: "success",
	}
	valid := map[string]any{
		"schema_version":  drillEvidenceSchemaVersion,
		"drill":           "worker_failure_recovery",
		"status":          "pass",
		"commit":          metadata.Commit,
		"profile":         "estate-500",
		"source_workflow": ".github/workflows/day2-drill-execution.yaml",
		"source_run_id":   "run-raw-41", "source_commit": metadata.Commit,
		"source_conclusion": "success", "source_event": "workflow_dispatch",
		"images":                  metadata.Images,
		"environment":             metadata.Environment,
		"execution_evidence_path": "worker_failure_recovery.log",
		"started_at":              now.Add(-time.Hour).Format(time.RFC3339),
		"completed_at":            now.Add(-30 * time.Minute).Format(time.RFC3339),
	}
	executionRaw := []byte("real bounded worker failure execution\n")
	valid["execution_evidence_sha256"] = fmt.Sprintf("%x", sha256.Sum256(executionRaw))
	valid["driver_result"] = map[string]any{
		"schema_version": 1, "drill": "worker_failure_recovery", "status": "pass",
		"target":       map[string]any{"environment": metadata.Environment, "profile": "estate-500", "release_manifest_digest": metadata.Images},
		"precondition": map[string]any{"observed_at": now.Add(-55 * time.Minute).Format(time.RFC3339), "metric": "worker_ready", "value": 3},
		"injection":    map[string]any{"operation_id": "op-1", "injected_at": now.Add(-50 * time.Minute).Format(time.RFC3339), "effect_observed_at": now.Add(-49 * time.Minute).Format(time.RFC3339), "metric": "worker_ready", "value": 2},
		"recovery":     map[string]any{"recovered_at": now.Add(-40 * time.Minute).Format(time.RFC3339), "metric": "worker_ready", "value": 3},
		"cleanup":      map[string]any{"completed_at": now.Add(-35 * time.Minute).Format(time.RFC3339), "restored": true},
	}
	write := func(t *testing.T, evidence map[string]any) drillEvidenceResult {
		t.Helper()
		raw, err := json.Marshal(evidence)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "worker_failure_recovery.json"), raw, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "worker_failure_recovery.log"), executionRaw, 0o600); err != nil {
			t.Fatal(err)
		}
		manifest := map[string]any{
			"schema_version":           drillManifestSchemaVersion,
			"qualification_workflow":   trustedDrillEvidenceWorkflow,
			"qualification_run_id":     metadata.DrillEvidenceRunID,
			"qualification_conclusion": "success", "qualification_commit": metadata.Commit,
			"raw_execution_workflow": ".github/workflows/day2-drill-execution.yaml",
			"raw_execution_run_id":   "run-raw-41", "raw_execution_conclusion": "success",
			"raw_execution_commit": metadata.Commit, "raw_execution_event": "workflow_dispatch",
			"raw_artifact": "raw-run-41", "artifact": "drill-evidence-run-42",
			"completed_at": now.Add(-20 * time.Minute).Format(time.RFC3339),
			"files": []map[string]string{{
				"path":   "worker_failure_recovery.json",
				"sha256": fmt.Sprintf("%x", sha256.Sum256(raw)),
			}, {"path": "worker_failure_recovery.log", "sha256": fmt.Sprintf("%x", sha256.Sum256(executionRaw))}},
		}
		manifestRaw, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "drill-evidence-manifest.json"), manifestRaw, 0o600); err != nil {
			t.Fatal(err)
		}
		metadata.DrillEvidenceManifestSHA256 = fmt.Sprintf("%x", sha256.Sum256(manifestRaw))
		return loadDrillEvidence("worker_failure_recovery", "estate-500", metadata, now)
	}
	clone := func() map[string]any {
		out := make(map[string]any, len(valid))
		for key, value := range valid {
			out[key] = value
		}
		return out
	}

	if result := write(t, clone()); result.Status != "pass" {
		t.Fatalf("valid evidence rejected: %+v", result)
	}
	failingConclusion := metadata
	failingConclusion.DrillEvidenceConclusion = "failure"
	if result := loadDrillEvidence("worker_failure_recovery", "estate-500", failingConclusion, now); result.Status == "pass" {
		t.Fatal("non-success source run conclusion was accepted")
	}
	badDriver := clone()
	driverRaw, err := json.Marshal(valid["driver_result"])
	if err != nil {
		t.Fatal(err)
	}
	var driver map[string]any
	if err := json.Unmarshal(driverRaw, &driver); err != nil {
		t.Fatal(err)
	}
	driver["cleanup"].(map[string]any)["restored"] = false
	badDriver["driver_result"] = driver
	if result := write(t, badDriver); result.Status == "pass" {
		t.Fatal("unrestored driver lifecycle was accepted")
	}
	if result := write(t, clone()); result.Status != "pass" {
		t.Fatalf("valid evidence did not recover after negative case: %+v", result)
	}
	if err := os.WriteFile(filepath.Join(dir, "worker_failure_recovery.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if result := loadDrillEvidence("worker_failure_recovery", "estate-500", metadata, now); result.Status == "pass" {
		t.Fatal("file modified after manifest signing was accepted")
	}
	for _, tc := range []struct {
		name  string
		field string
		value any
	}{
		{name: "schema", field: "schema_version", value: "legacy"},
		{name: "commit", field: "commit", value: "commit-b"},
		{name: "profile", field: "profile", value: "estate-100"},
		{name: "run", field: "source_run_id", value: "run-40"},
		{name: "images", field: "images", value: "images-b"},
		{name: "environment", field: "environment", value: "staging"},
		{name: "status", field: "status", value: "fail"},
		{name: "future", field: "completed_at", value: now.Add(6 * time.Minute).Format(time.RFC3339)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			evidence := clone()
			evidence[tc.field] = tc.value
			if result := write(t, evidence); result.Status == "pass" {
				t.Fatalf("mismatched %s accepted", tc.field)
			}
		})
	}
	stale := clone()
	stale["started_at"] = now.Add(-32 * 24 * time.Hour).Format(time.RFC3339)
	stale["completed_at"] = now.Add(-31 * 24 * time.Hour).Format(time.RFC3339)
	if result := write(t, stale); result.Status != "stale" {
		t.Fatalf("stale evidence result = %+v", result)
	}
}

func TestReportFailsHeapAndOpenFDLeakBudgets(t *testing.T) {
	cfg := &config{skipAgents: true}
	recorder := newRecorder()
	recorder.scrapeSeries["server_heap_bytes"] = points(100, 100, 100, 100, 100, 100, 200, 200)
	recorder.scrapeSeries["server_open_fds"] = points(20, 20, 20, 20, 20, 20, 100, 100)
	report := newReport(cfg, recorder)
	if report.Verdict != "fail" {
		t.Fatalf("verdict=%q reasons=%v", report.Verdict, report.Reasons)
	}
	reasons := strings.Join(report.Reasons, "\n")
	if !strings.Contains(reasons, "server heap steady-state") || !strings.Contains(reasons, "server open-FD steady-state") {
		t.Fatalf("reasons=%v", report.Reasons)
	}
}

func TestCertificationRequiresAllLeakEvidence(t *testing.T) {
	setCertificationMetadata(t)
	cfg := &config{skipAgents: true, certification: true}
	report := newReport(cfg, newRecorder())
	if report.Verdict != "fail" {
		t.Fatalf("verdict=%q reasons=%v", report.Verdict, report.Reasons)
	}
	for _, name := range []string{"server_goroutines", "server_heap_bytes", "server_open_fds"} {
		if !strings.Contains(strings.Join(report.Reasons, "\n"), name) {
			t.Fatalf("missing %s evidence reason: %v", name, report.Reasons)
		}
	}
}

func TestCertificationPassesStableLeakEvidence(t *testing.T) {
	setCertificationMetadata(t)
	cfg, recorder := passingCertificationFixture()
	for name, value := range map[string]float64{
		"server_goroutines": 100,
		"server_heap_bytes": 1000,
		"server_open_fds":   50,
	} {
		recorder.scrapeSeries[name] = points(value, value, value, value, value, value, value, value)
	}
	report := newReport(cfg, recorder)
	if report.Verdict != "pass" {
		t.Fatalf("verdict=%q reasons=%v", report.Verdict, report.Reasons)
	}
}

func TestCertificationFailsClosedOnTrafficAndCardinalityGaps(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*config, *recorder)
		want   string
	}{
		{name: "zero requests", mutate: func(_ *config, rec *recorder) {
			rec.httpCount = map[string]int{}
			rec.httpSamples = map[string][]time.Duration{}
			rec.httpStatus = map[string]map[int]int{}
		}, want: "zero HTTP requests"},
		{name: "404", mutate: func(_ *config, rec *recorder) {
			rec.httpStatus["cluster_pods"] = map[int]int{404: 1}
		}, want: "HTTP failure ratio"},
		{name: "500", mutate: func(_ *config, rec *recorder) {
			rec.httpStatus["cluster_pods"] = map[int]int{500: 1}
		}, want: "HTTP failure ratio"},
		{name: "transport error", mutate: func(_ *config, rec *recorder) {
			rec.httpErrors["cluster_pods"] = 1
		}, want: "HTTP failure ratio"},
		{name: "missing scenario", mutate: func(_ *config, rec *recorder) {
			delete(rec.httpCount, "cluster_services")
			delete(rec.httpSamples, "cluster_services")
			delete(rec.httpStatus, "cluster_services")
		}, want: "required scenario has no samples: cluster_services"},
		{name: "subtarget throughput", mutate: func(cfg *config, _ *recorder) {
			cfg.rps = 100
		}, want: "recorded requests"},
		{name: "early termination", mutate: func(_ *config, rec *recorder) {
			rec.endedAt = rec.startedAt.Add(100 * time.Millisecond)
		}, want: "observed duration"},
		{name: "missing cardinality", mutate: func(_ *config, rec *recorder) {
			rec.resourceCardinality["DeploymentList"] = 0
		}, want: "DeploymentList cardinality"},
		{name: "missing audit metric", mutate: func(_ *config, rec *recorder) {
			delete(rec.scrapeSeries, "audit_dropped_total")
		}, want: "certification audit evidence audit_dropped_total"},
		{name: "unobserved durable intent", mutate: func(_ *config, rec *recorder) {
			rec.auditConservation.IntentsObserved = 0
		}, want: "mandatory-audit conservation failed"},
		{name: "audit window ended early", mutate: func(_ *config, rec *recorder) {
			rec.auditConservation.WindowEndedAt = rec.auditConservation.WindowStartedAt.Add(100 * time.Millisecond)
			rec.auditConservation.LastAcceptedAt = rec.auditConservation.FirstAcceptedAt
		}, want: "mandatory-audit activity covered"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			setCertificationMetadata(t)
			cfg, rec := passingCertificationFixture()
			tc.mutate(cfg, rec)
			report := newReport(cfg, rec)
			if report.Verdict != "fail" || !strings.Contains(strings.Join(report.Reasons, "\n"), tc.want) {
				t.Fatalf("verdict=%q reasons=%v, want %q", report.Verdict, report.Reasons, tc.want)
			}
		})
	}
}

func TestCertificationFailsClosedOnInvalidComponentReplicaMetadata(t *testing.T) {
	setCertificationMetadata(t)
	t.Setenv("LOADTEST_COMPONENT_REPLICAS", `{"server":3,"worker":0}`)
	cfg, rec := passingCertificationFixture()
	report := newReport(cfg, rec)
	if report.Verdict != "fail" || !strings.Contains(strings.Join(report.Reasons, "\n"), "component replica metadata invalid") {
		t.Fatalf("verdict=%q reasons=%v", report.Verdict, report.Reasons)
	}
}

func passingCertificationFixture() (*config, *recorder) {
	cfg := &config{
		certification: true, clusters: 1, rps: 5, duration: time.Second,
		fixtureClusterIDs: []string{"11111111-1111-4111-8111-111111111111"},
		resources:         scaleResources{PodsPerCluster: 1, DeploymentsPerCluster: 1, ServicesPerCluster: 1},
		mandatoryAudit:    mandatoryAuditProfile{RatePerSecond: 1, MaxOperations: 1},
	}
	rec := newRecorder()
	now := time.Now()
	rec.startedAt, rec.endedAt = now.Add(-time.Second), now
	rec.scrapeSeries["agent_connections"] = points(1)
	rec.resourceCardinality["PodList"] = 1
	rec.resourceCardinality["DeploymentList"] = 1
	rec.resourceCardinality["ServiceList"] = 1
	rec.auditConservation = auditConservation{
		RunID: "test-run", Attempted: 1, Accepted: 1, IntentsObserved: 1,
		CanonicalRows: 1, Reconciled: true, RequestAcceptedAt: map[string]time.Time{"request": now},
		WindowStartedAt: now.Add(-time.Second), WindowEndedAt: now,
		FirstAcceptedAt: now.Add(-time.Second), LastAcceptedAt: now,
	}
	for _, scenario := range defaultScenarios() {
		rec.httpCount[scenario.name] = 1
		rec.httpSamples[scenario.name] = []time.Duration{time.Millisecond}
		rec.httpStatus[scenario.name] = map[int]int{200: 1}
	}
	for name, value := range map[string]float64{
		"server_goroutines": 100, "server_heap_bytes": 1000, "server_open_fds": 50,
	} {
		rec.scrapeSeries[name] = points(value, value, value, value, value, value, value, value)
	}
	for _, name := range []string{
		"audit_dropped_total", "audit_write_failures_total", "audit_outbox_active_rows", "audit_outbox_dead_rows",
	} {
		rec.scrapeSeries[name] = points(0, 0)
	}
	return cfg, rec
}

func setCertificationMetadata(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"LOADTEST_COMMIT", "LOADTEST_RUN_ID", "LOADTEST_DRILL_EVIDENCE_RUN_ID",
		"LOADTEST_ENVIRONMENT", "LOADTEST_IMAGES", "LOADTEST_CHART_VALUES",
		"LOADTEST_KUBERNETES_VERSION", "LOADTEST_POSTGRES_VERSION",
		"LOADTEST_REDIS_VERSION", "LOADTEST_HARDWARE",
		"LOADTEST_COMPONENT_REPLICAS",
		"LOADTEST_DRILL_EVIDENCE_MANIFEST_SHA256",
	} {
		t.Setenv(key, "test")
	}
	t.Setenv("LOADTEST_COMPONENT_REPLICAS", `{"server":3,"worker":3,"tunnel":3,"audit":3}`)
	t.Setenv("LOADTEST_DRILL_EVIDENCE_WORKFLOW", trustedDrillEvidenceWorkflow)
	t.Setenv("LOADTEST_DRILL_EVIDENCE_CONCLUSION", "success")
}
