package handler

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func (h *SupportBundleHandler) Generate(ctx context.Context, output io.Writer) error {
	if h == nil || h.queries == nil {
		return errors.New("support bundle generator is not configured")
	}
	zw := zip.NewWriter(output)

	// Each writer is best-effort: a per-section failure shouldn't doom the
	// whole bundle. We collect errors into a manifest file at the end so the
	// caller can see what's missing.
	collected := newSectionLog()

	h.writeMeta(ctx, zw, collected)
	h.writePlatformConfig(ctx, zw, collected)
	h.writeClusters(ctx, zw, collected)
	h.writeUsers(ctx, zw, collected)
	h.writeAuditLog(ctx, zw, collected)
	h.writeAuditPipelineHealth(ctx, zw, collected)
	h.writePods(ctx, zw, collected)
	h.writePodLogs(ctx, zw, collected)
	// Extra context an L3 engineer needs without
	// shell access to the cluster:
	h.writeEvents(ctx, zw, collected)
	h.writeHelmRelease(ctx, zw, collected)
	h.writeNetworkPolicies(ctx, zw, collected)
	h.writeIngressCertificates(ctx, zw, collected)
	h.writeSchemaMigrations(ctx, zw, collected)
	h.writeDeliveryDiagnostics(ctx, zw, collected)
	h.writeAsynqQueues(ctx, zw, collected)
	h.writeAgentConnections(ctx, zw, collected)
	h.writeCharlieStatus(ctx, zw, collected)
	h.writeReadme(zw, collected)
	if err := zw.Close(); err != nil {
		return fmt.Errorf("finish support bundle: %w", err)
	}
	return ctx.Err()
}

func supportBundleStatusURL(id uuid.UUID) string {
	return "/api/v1/support-bundles/" + id.String() + "/"
}

func supportBundleResponseFromCreate(row sqlc.CreateSupportBundleOperationRow) SupportBundleOperationResponse {
	return supportBundleOperationResponse(row.ID, row.Status, row.AttemptCount, row.ErrorCode, row.Filename, row.ArtifactSha256, row.ArtifactSize, row.ExpiresAt, row.CompletedAt, row.CreatedAt, row.UpdatedAt)
}

func supportBundleResponseFromRow(row sqlc.GetSupportBundleOperationRow) SupportBundleOperationResponse {
	return supportBundleOperationResponse(row.ID, row.Status, row.AttemptCount, row.ErrorCode, row.Filename, row.ArtifactSha256, row.ArtifactSize, row.ExpiresAt, row.CompletedAt, row.CreatedAt, row.UpdatedAt)
}

func supportBundleOperationResponse(id uuid.UUID, status string, attempts int32, errorCode, filename string, sha pgtype.Text, size int64, expires time.Time, completed pgtype.Timestamptz, created, updated time.Time) SupportBundleOperationResponse {
	response := SupportBundleOperationResponse{ID: id.String(), Status: status, AttemptCount: attempts, ErrorCode: errorCode, Filename: filename, Size: size, ExpiresAt: expires.UTC(), CreatedAt: created.UTC(), UpdatedAt: updated.UTC(), StatusURL: supportBundleStatusURL(id)}
	if sha.Valid {
		response.SHA256 = sha.String
	}
	if completed.Valid {
		at := completed.Time.UTC()
		response.CompletedAt = &at
	}
	if status == "succeeded" && expires.After(time.Now().UTC()) {
		response.DownloadURL = supportBundleStatusURL(id) + "download/"
	}
	return response
}

func (h *SupportBundleHandler) writeAuditPipelineHealth(ctx context.Context, zw *zip.Writer, log *sectionLog) {
	queries, ok := h.queries.(supportBundleAuditOutboxQuerier)
	if !ok {
		log.skipped("audit-pipeline-health.json", "transactional audit outbox health is not wired")
		return
	}
	health, err := queries.GetAuditOutboxHealth(ctx)
	if err != nil {
		log.section("audit-pipeline-health.json", err)
		return
	}
	state := "healthy"
	if health.DeadCount > 0 {
		state = "degraded"
	} else if health.PendingCount > 0 {
		state = "draining"
	}
	payload := map[string]any{
		"state":             state,
		"pending_count":     health.PendingCount,
		"dead_count":        health.DeadCount,
		"delivered_count":   health.DeliveredCount,
		"oldest_pending_at": health.OldestPendingAt,
		"last_delivered_at": health.LastDeliveredAt,
	}
	log.section("audit-pipeline-health.json", writeBundleJSON(zw, "audit-pipeline-health.json", payload))
}

func (h *SupportBundleHandler) writeCharlieStatus(ctx context.Context, zw *zip.Writer, log *sectionLog) {
	queries, ok := h.queries.(supportBundleCharlieQuerier)
	if !ok {
		log.skipped("charlie-status.json", "Charlie metadata store not wired")
		return
	}
	connection, err := queries.GetLatestCharlieConnection(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		log.skipped("charlie-status.json", "Charlie has never been configured")
		return
	}
	if err != nil {
		log.section("charlie-status.json", err)
		return
	}
	rules, rulesErr := queries.ListCharlieTriggerRules(ctx, connection.ID)
	findings, findingsErr := queries.ListCharlieFindings(ctx, sqlc.ListCharlieFindingsParams{
		ConnectionID: connection.ID, PageLimit: 500,
	})
	ruleSummary := make([]map[string]any, 0, len(rules))
	if rulesErr == nil {
		for _, rule := range rules {
			ruleSummary = append(ruleSummary, map[string]any{
				"name": rule.Name, "category": rule.Category, "enabled": rule.Enabled,
				"minimum_severity": rule.MinimumSeverity, "window_seconds": rule.WindowSeconds,
				"cooldown_seconds": rule.CooldownSeconds, "mode_ceiling": rule.ModeCeiling,
			})
		}
	}
	findingCounts := map[string]int{}
	if findingsErr == nil {
		for _, finding := range findings {
			findingCounts[finding.Status+":"+finding.Severity]++
		}
	}
	payload := map[string]any{
		"configured": true, "active": connection.Active,
		"emergency_disabled": connection.EmergencyDisabled,
		"requested_mode":     connection.RequestedMode, "verified_mode": connection.VerifiedMode,
		"verified_mode_revision": connection.VerifiedModeRevision,
		"onboarding_state":       connection.OnboardingState, "health_state": connection.HealthState,
		"last_error_code":                   connection.LastErrorCode,
		"agent_protocol_version":            connection.AgentProtocolVersion,
		"chart_version":                     connection.ChartVersion,
		"leader_present":                    strings.TrimSpace(connection.LeaderInstanceID) != "",
		"fencing_epoch":                     connection.FencingEpoch,
		"last_verified_at":                  timestamptzString(connection.LastVerifiedAt),
		"last_connected_at":                 timestamptzString(connection.LastConnectedAt),
		"last_rotated_at":                   timestamptzString(connection.LastRotatedAt),
		"certificate_expires_at":            timeString(connection.CertificateExpiresAt),
		"enrollment_credentials_expires_at": timeString(connection.EnrollmentCredentialsExpiresAt),
		"artifact_credential_expires_at":    timeString(connection.ArtifactCredentialExpiresAt),
		"onboarding_package_expires_at":     timeString(connection.OnboardingPackageExpiresAt),
		"trigger_rules":                     ruleSummary, "finding_counts": findingCounts,
	}
	if rulesErr != nil {
		payload["trigger_rules_status"] = "unavailable"
	}
	if findingsErr != nil {
		payload["finding_counts_status"] = "unavailable"
	}
	log.section("charlie-status.json", writeBundleJSON(zw, "charlie-status.json", payload))
}

func timeString(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}

// ── individual section writers ──────────────────────────────────────────
