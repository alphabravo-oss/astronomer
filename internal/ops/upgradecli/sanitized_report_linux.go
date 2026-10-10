//go:build linux

package upgradecli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/ops/upgrade"
)

// SanitizedReportV1 retains the legacy public shape. It deliberately excludes
// private paths, namespace/revision/storage UIDs, command output, credentials,
// raw API endpoints and checkpoint state. A report grants no stage authority.
type SanitizedReportV1 struct {
	SchemaVersion         int          `json:"schema_version"`
	Release               string       `json:"release"`
	Namespace             string       `json:"namespace"`
	TargetVersion         string       `json:"target_version"`
	Mode                  upgrade.Mode `json:"mode"`
	Result                string       `json:"result"`
	APIIdentitySHA256     string       `json:"api_identity_sha256"`
	ReleaseManifestSHA256 string       `json:"release_manifest_sha256"`
	BackupManifestSHA256  string       `json:"backup_manifest_sha256"`
	DatabaseRestoreMode   string       `json:"database_restore_mode"`
	RollbackAttempted     bool         `json:"rollback_attempted"`
	CompletedAt           string       `json:"completed_at"`
}

// SanitizedReport derives the public report from the already returned Run
// outcome and exact retained private input/checksum bytes. The shell first
// creates this report after its readable backup gate; failures before that gate
// remain in private originals/checkpoints and cannot invent a backup digest.
// This only serializes v1 bytes: the compatible CLI owns their public filename
// and publication. It does not replay commands or authorize resume/replacement.
func (i *Invocation) SanitizedReport(ctx context.Context) ([]byte, error) {
	if i == nil || ctx == nil {
		return nil, errors.New("explicit completed invocation report context required")
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.closed || !i.completed || i.plan.Validate() != nil || i.driver == nil || i.driver.release == nil || i.driver.backup == nil || i.lastResult.Mode != i.plan.Mode {
		return nil, errors.New("completed supported invocation required for sanitized report")
	}
	bounded, cancel := context.WithTimeout(ctx, i.plan.CleanupTimeout)
	defer cancel()
	if err := bounded.Err(); err != nil {
		return nil, err
	}
	release := i.driver.release
	if err := release.manifestBinding(); err != nil {
		return nil, err
	}
	backup := i.driver.backup
	backup.mu.Lock()
	defer backup.mu.Unlock()
	if !backup.complete {
		return nil, errors.New("readable sealed backup required for legacy public report")
	}
	if err := backup.verifyInventory(bounded); err != nil {
		return nil, err
	}
	result := "failed"
	if i.lastResult.ExitCode == 0 && i.lastResult.Result == "passed" {
		result = "passed"
	}
	endpoint := sha256.Sum256([]byte(i.plan.Identity.APIServer))
	report := SanitizedReportV1{SchemaVersion: 1, Release: i.plan.Identity.Release, Namespace: i.plan.Identity.Namespace, TargetVersion: i.plan.Target, Mode: i.plan.Mode, Result: result,
		APIIdentitySHA256: "sha256:" + hex.EncodeToString(endpoint[:]), ReleaseManifestSHA256: "sha256:" + hex.EncodeToString(release.manifestHash[:]), BackupManifestSHA256: backup.inventory.SHA256,
		DatabaseRestoreMode: "verified_copy", RollbackAttempted: i.lastResult.RollbackAttempted, CompletedAt: time.Now().UTC().Format(time.RFC3339)}
	if backup.external != nil {
		report.DatabaseRestoreMode = "operator_confirmed_external"
	} else if i.driver.replacement != nil && i.driver.replacement.replaced {
		report.DatabaseRestoreMode = "live_replaced"
	}
	raw, err := json.Marshal(report)
	if err != nil {
		return nil, errors.New("sanitized report serialization failed")
	}
	if err := bounded.Err(); err != nil {
		return nil, err
	}
	return append(raw, '\n'), nil
}
