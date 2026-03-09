package tasks

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/hibiken/asynq"
)

// SecurityScanPayload contains parameters for a security scan.
type SecurityScanPayload struct {
	ClusterID string `json:"cluster_id"`
	ScanType  string `json:"scan_type,omitempty"` // e.g. "vulnerability", "compliance", "full"
}

// NewSecurityScanTask creates a new security scan task.
func NewSecurityScanTask(payload SecurityScanPayload) (*asynq.Task, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal security scan payload: %w", err)
	}
	return asynq.NewTask("security:scan", data, asynq.MaxRetry(1)), nil
}

// HandleSecurityScan runs security scans on the specified cluster.
func HandleSecurityScan(ctx context.Context, t *asynq.Task) error {
	var p SecurityScanPayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return fmt.Errorf("unmarshal security scan payload: %w", err)
	}

	if p.ClusterID == "" {
		return fmt.Errorf("cluster_id is required")
	}

	scanType := p.ScanType
	if scanType == "" {
		scanType = "full"
	}

	slog.InfoContext(ctx, "running security scan",
		"cluster_id", p.ClusterID,
		"scan_type", scanType,
	)

	// TODO: Connect to cluster, run security scan (vulnerability/compliance), store results.

	slog.InfoContext(ctx, "security scan complete", "cluster_id", p.ClusterID, "scan_type", scanType)
	return nil
}
