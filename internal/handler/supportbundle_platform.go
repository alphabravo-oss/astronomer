package handler

import (
	"archive/zip"
	"context"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/redaction"
	"github.com/alphabravocompany/astronomer-go/pkg/version"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

func (h *SupportBundleHandler) writeMeta(ctx context.Context, zw *zip.Writer, log *sectionLog) {
	type meta struct {
		GeneratedAt   string `json:"generated_at"`
		ServerVersion string `json:"server_version"`
		ServerCommit  string `json:"server_commit"`
		ServerBuilt   string `json:"server_built"`
		Namespace     string `json:"release_namespace"`
	}
	m := meta{
		GeneratedAt:   time.Now().UTC().Format(time.RFC3339),
		ServerVersion: version.Version,
		ServerCommit:  version.GitCommit,
		ServerBuilt:   version.BuildDate,
		Namespace:     h.namespace,
	}
	log.section("meta.json", writeBundleJSON(zw, "meta.json", m))
}

func (h *SupportBundleHandler) writePlatformConfig(ctx context.Context, zw *zip.Writer, log *sectionLog) {
	cfg, err := h.queries.GetPlatformConfig(ctx)
	if err != nil {
		log.section("platform-config.json", err)
		return
	}
	log.section("platform-config.json", writeBundleJSON(zw, "platform-config.json", cfg))
}

func (h *SupportBundleHandler) writeClusters(ctx context.Context, zw *zip.Writer, log *sectionLog) {
	rows, err := h.queries.ListClusters(ctx, sqlc.ListClustersParams{Limit: 500, Offset: 0})
	if err != nil {
		log.section("clusters.json", err)
		return
	}
	ids := make([]uuid.UUID, len(rows))
	for i, cluster := range rows {
		ids[i] = cluster.ID
	}
	livenessRows, err := h.queries.ListClusterLivenessForClusters(ctx, ids)
	if err != nil {
		log.section("clusters.json", err)
		return
	}
	liveness := make(map[uuid.UUID]pgtype.Timestamptz, len(livenessRows))
	for _, row := range livenessRows {
		liveness[row.ClusterID] = row.LastHeartbeat
	}
	redacted := make([]map[string]any, 0, len(rows))
	for _, c := range rows {
		// CaCertificate is technically public but it's bulky and rarely
		// useful for triage; replace with a length-tagged placeholder.
		entry := map[string]any{
			"id":                 c.ID.String(),
			"name":               c.Name,
			"display_name":       c.DisplayName,
			"description":        c.Description,
			"status":             c.Status,
			"api_server_url":     c.ApiServerUrl,
			"ca_certificate":     redaction.ByteCount(c.CaCertificate),
			"environment":        c.Environment,
			"region":             c.Region,
			"provider":           c.Provider,
			"distribution":       c.Distribution,
			"agent_version":      c.AgentVersion,
			"kubernetes_version": c.KubernetesVersion,
			"node_count":         c.NodeCount,
			"created_at":         c.CreatedAt,
			"updated_at":         c.UpdatedAt,
		}
		if heartbeat := liveness[c.ID]; heartbeat.Valid {
			entry["last_heartbeat"] = heartbeat.Time
		}
		redacted = append(redacted, entry)
	}
	log.section("clusters.json", writeBundleJSON(zw, "clusters.json", redacted))
}

func (h *SupportBundleHandler) writeUsers(ctx context.Context, zw *zip.Writer, log *sectionLog) {
	users, err := h.queries.ListUsers(ctx, sqlc.ListUsersParams{Limit: 500, Offset: 0})
	if err != nil {
		log.section("users.json", err)
		return
	}
	redacted := make([]map[string]any, 0, len(users))
	for _, u := range users {
		entry := map[string]any{
			"id":                   u.ID.String(),
			"username":             u.Username,
			"email":                u.Email,
			"first_name":           u.FirstName,
			"last_name":            u.LastName,
			"is_active":            u.IsActive,
			"is_staff":             u.IsStaff,
			"is_superuser":         u.IsSuperuser,
			"must_change_password": u.MustChangePassword,
			"password":             "[redacted bcrypt hash]",
			"date_joined":          u.DateJoined,
			"created_at":           u.CreatedAt,
		}
		if u.LastLogin.Valid {
			entry["last_login"] = u.LastLogin.Time
		}
		redacted = append(redacted, entry)
	}
	log.section("users.json", writeBundleJSON(zw, "users.json", redacted))
}

func (h *SupportBundleHandler) writeAuditLog(ctx context.Context, zw *zip.Writer, log *sectionLog) {
	rows, err := h.queries.ListAuditLogV1(ctx, sqlc.ListAuditLogsParams{Limit: 500, Offset: 0})
	if err != nil {
		log.section("audit-log-recent.json", err)
		return
	}
	log.section("audit-log-recent.json", writeBundleJSON(zw, "audit-log-recent.json", rows))
}
