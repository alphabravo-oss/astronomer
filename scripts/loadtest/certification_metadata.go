package main

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"sort"
	"strings"
)

type certificationMetadata struct {
	Commit                      string `json:"commit"`
	RunID                       string `json:"run_id"`
	DrillEvidenceRunID          string `json:"drill_evidence_run_id"`
	DrillEvidenceWorkflow       string `json:"drill_evidence_workflow"`
	DrillEvidenceConclusion     string `json:"drill_evidence_conclusion"`
	DrillEvidenceManifestSHA256 string `json:"drill_evidence_manifest_sha256"`
	Environment                 string `json:"environment"`
	Images                      string `json:"images"`
	ChartValues                 string `json:"chart_values"`
	KubernetesVersion           string `json:"kubernetes_version"`
	PostgresVersion             string `json:"postgres_version"`
	RedisVersion                string `json:"redis_version"`
	Hardware                    string `json:"hardware"`
	ComponentReplicas           string `json:"component_replicas"`
	GoVersion                   string `json:"go_version"`
}

func loadCertificationMetadata() certificationMetadata {
	return certificationMetadata{
		Commit: os.Getenv("LOADTEST_COMMIT"), RunID: os.Getenv("LOADTEST_RUN_ID"),
		DrillEvidenceRunID: os.Getenv("LOADTEST_DRILL_EVIDENCE_RUN_ID"), Environment: os.Getenv("LOADTEST_ENVIRONMENT"),
		DrillEvidenceWorkflow:       os.Getenv("LOADTEST_DRILL_EVIDENCE_WORKFLOW"),
		DrillEvidenceConclusion:     os.Getenv("LOADTEST_DRILL_EVIDENCE_CONCLUSION"),
		DrillEvidenceManifestSHA256: os.Getenv("LOADTEST_DRILL_EVIDENCE_MANIFEST_SHA256"),
		Images:                      os.Getenv("LOADTEST_IMAGES"),
		ChartValues:                 os.Getenv("LOADTEST_CHART_VALUES"), KubernetesVersion: os.Getenv("LOADTEST_KUBERNETES_VERSION"),
		PostgresVersion: os.Getenv("LOADTEST_POSTGRES_VERSION"), RedisVersion: os.Getenv("LOADTEST_REDIS_VERSION"),
		Hardware: os.Getenv("LOADTEST_HARDWARE"), GoVersion: runtime.Version(),
		ComponentReplicas: os.Getenv("LOADTEST_COMPONENT_REPLICAS"),
	}
}

func (m certificationMetadata) missing() []string {
	values := map[string]string{
		"LOADTEST_COMMIT": m.Commit, "LOADTEST_RUN_ID": m.RunID,
		"LOADTEST_DRILL_EVIDENCE_RUN_ID": m.DrillEvidenceRunID, "LOADTEST_ENVIRONMENT": m.Environment,
		"LOADTEST_DRILL_EVIDENCE_WORKFLOW":        m.DrillEvidenceWorkflow,
		"LOADTEST_DRILL_EVIDENCE_CONCLUSION":      m.DrillEvidenceConclusion,
		"LOADTEST_DRILL_EVIDENCE_MANIFEST_SHA256": m.DrillEvidenceManifestSHA256,
		"LOADTEST_IMAGES":                         m.Images,
		"LOADTEST_CHART_VALUES":                   m.ChartValues, "LOADTEST_KUBERNETES_VERSION": m.KubernetesVersion,
		"LOADTEST_POSTGRES_VERSION": m.PostgresVersion, "LOADTEST_REDIS_VERSION": m.RedisVersion,
		"LOADTEST_HARDWARE":           m.Hardware,
		"LOADTEST_COMPONENT_REPLICAS": m.ComponentReplicas,
	}
	missing := []string{}
	for key, value := range values {
		if strings.TrimSpace(value) == "" {
			missing = append(missing, key)
		}
	}
	sort.Strings(missing)
	return missing
}

func (m certificationMetadata) componentReplicaCounts() (map[string]int, error) {
	var replicas map[string]int
	if err := json.Unmarshal([]byte(m.ComponentReplicas), &replicas); err != nil {
		return nil, fmt.Errorf("LOADTEST_COMPONENT_REPLICAS must be a JSON object: %w", err)
	}
	for _, component := range []string{"server", "worker", "tunnel", "audit"} {
		if replicas[component] <= 0 {
			return nil, fmt.Errorf("LOADTEST_COMPONENT_REPLICAS.%s must be a positive integer", component)
		}
	}
	return replicas, nil
}
