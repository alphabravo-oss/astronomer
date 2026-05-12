// Package handler — compliance export writers.
//
// This file holds the per-CSV / per-JSON writer helpers the
// compliance bundle handler stitches together. Each writer takes a
// tightly-scoped querier interface plus the io.Writer the encoder
// writes through, so the handler tests can mock each section
// independently and the writers stay testable without lifting the
// full handler.
//
// Design rules — read these before adding a new section:
//
//  1. Stream row-by-row. The audit_log table can be in the millions
//     of rows; loading it all into memory is not an option. Use
//     ListAuditLogV1ForRange (keyset-paginated, ASC) and write each
//     page into the csv.Writer before fetching the next.
//
//  2. csv.Writer.UseCRLF stays at its default (false). The default
//     quoting rules handle newlines inside JSON detail cells, so
//     don't disable them.
//
//  3. Timestamps are RFC3339 UTC. Never local time.
//
//  4. JSONB columns (`detail`, `scopes`, `labels`, etc.) get written
//     as a single compact JSON string in one CSV cell. Quote-escape
//     is handled by csv.Writer.
//
//  5. Per-section errors must NOT doom the whole bundle. Errors get
//     surfaced via the manifest the README writer collects; the
//     writer signature returns the error so the caller can log /
//     count it but the ZIP keeps going.
package handler

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// ── tightly-scoped querier interfaces ────────────────────────────────────

// AuditExporter is the audit_log surface the streaming writer needs.
// It's a one-method interface so a test fake can paginate against an
// in-memory slice without satisfying the entire sqlc.Querier surface.
type AuditExporter interface {
	ListAuditLogV1ForRange(ctx context.Context, arg sqlc.ListAuditLogV1ForRangeParams) ([]sqlc.AuditLog, error)
}

// AuditCounter returns the row count for a date range — drives the
// inline-vs-async path decision.
type AuditCounter interface {
	CountAuditLogV1ForRange(ctx context.Context, from, to time.Time) (int64, error)
}

// RBACSnapshotQuerier reads every role binding across the three RBAC
// tables joined with role names + source.
type RBACSnapshotQuerier interface {
	ListAllRoleBindingsWithRoleNames(ctx context.Context) ([]sqlc.ListAllRoleBindingsWithRoleNamesRow, error)
	// GetUserByID resolves the binding user_id to a human-readable
	// (username, email) pair. Best-effort: a missing user (deleted,
	// or a group-scoped binding with NULL user_id) just leaves the
	// fields empty.
	GetUserByID(ctx context.Context, id uuid.UUID) (sqlc.User, error)
}

// ClusterInventoryQuerier reads the clusters table for the inventory
// CSV. Uses the existing ListClusters query.
type ClusterInventoryQuerier interface {
	ListClusters(ctx context.Context, arg sqlc.ListClustersParams) ([]sqlc.Cluster, error)
	GetClusterAgentTokenByClusterID(ctx context.Context, clusterID uuid.UUID) (sqlc.ClusterAgentToken, error)
}

// AccessTokenQuerier reads api_tokens with the migration-044 columns
// joined to user identity. The compliance-specific projection strips
// the bcrypt-style token_hash.
type AccessTokenQuerier interface {
	ListAPITokensForCompliance(ctx context.Context) ([]sqlc.ComplianceAPITokenRow, error)
}

// BackupDrillHistoryQuerier reads every drill row, oldest first, so
// the export reflects the historical proof trail.
type BackupDrillHistoryQuerier interface {
	ListBackupDrillResults(ctx context.Context, arg sqlc.ListBackupDrillResultsParams) ([]sqlc.BackupDrillResult, error)
	CountBackupDrillResults(ctx context.Context) (int64, error)
}

// ProjectPolicyQuerier reads every project with its policy fields
// (pod_security_profile, network_policy_mode, resource_quota_*) plus
// the per-project bindings used for the "members" snapshot.
type ProjectPolicyQuerier interface {
	ListAllProjectsForCompliance(ctx context.Context) ([]sqlc.ComplianceProjectRow, error)
	ListProjectRoleBindingsByProject(ctx context.Context, arg sqlc.ListProjectRoleBindingsByProjectParams) ([]sqlc.ProjectRoleBinding, error)
}

// ── filter constants ────────────────────────────────────────────────────

// authEventActionPrefixes is the allow-list of audit `action` strings
// the auth-events.csv writer projects from the full audit log. These
// cover:
//
//   - auth.*               — login / logout / token issuance / failures
//   - auth.totp.*          — 2FA enrollment + use
//   - auth.group_sync.*    — IdP-driven role binding add/remove
//   - admin.user.*         — admin-initiated user mutations (create,
//     lock, force-logout, password reset)
//   - admin.group_mapping.* — admin CRUD over identity_group_mappings
//
// Matching is prefix-based: any action that starts with one of these
// strings is included. The reviewer can spot-check this list against
// the constants in internal/audit/*.go in a single pass.
var authEventActionPrefixes = []string{
	"auth.",
	"admin.user.",
	"admin.group_mapping.",
}

// isAuthEventAction returns true for any action the auth-events.csv
// writer should include. Prefix-based to keep the list short and
// catch siblings like auth.totp.enrolled / auth.group_sync.removed
// without spelling them all out.
func isAuthEventAction(action string) bool {
	for _, p := range authEventActionPrefixes {
		if len(action) >= len(p) && action[:len(p)] == p {
			return true
		}
	}
	return false
}

// ── pagination tuning ───────────────────────────────────────────────────

// auditExportPageSize is the keyset page size the streamer uses. A
// few thousand keeps the round-trip count low without ballooning the
// peak heap on the page-buffer side. The chosen value (5000) gives
// roughly 1MB of in-flight serialised rows in the worst case, which
// is fine for both the inline path's response buffer and the async
// path's gzip stream.
const auditExportPageSize = 5000

// ── CSV header rows ─────────────────────────────────────────────────────

var auditLogCSVHeader = []string{
	"id", "created_at", "schema_version", "source",
	"correlation_id", "request_id",
	"user_id", "actor_auth_method",
	"action", "resource_type", "resource_id", "resource_name",
	"http_method", "path", "status_code", "duration_ms",
	"ip_address", "user_agent",
	"detail",
}

var rbacSnapshotCSVHeader = []string{
	"scope", "binding_id",
	"user_id", "username", "email",
	"group",
	"role_id", "role_name",
	"cluster_id", "project_id",
	"source", "created_at",
}

var clusterInventoryCSVHeader = []string{
	"id", "name", "display_name", "environment",
	"region", "provider", "distribution",
	"status", "labels",
	"registered_at", "decommissioned_at",
	"last_heartbeat", "agent_token_last_used_at", "agent_token_rotation_count",
}

var accessTokensCSVHeader = []string{
	"id", "user_id", "username", "email",
	"name", "prefix", "scopes",
	"allowed_cidrs", "last_seen_remote_ip",
	"created_at", "expires_at", "last_used_at", "is_revoked",
}

var backupDrillHistoryCSVHeader = []string{
	"id", "started_at", "finished_at",
	"status", "schema_version", "backup_key", "error_message",
}

// ── writers ─────────────────────────────────────────────────────────────

// WriteAuditLogCSV streams every audit row in [from, to) into w as
// CSV. The detail JSONB column is written as a compact JSON string
// in a single cell (csv.Writer quotes embedded newlines). Returns
// the row count + any error.
func WriteAuditLogCSV(ctx context.Context, w io.Writer, from, to time.Time, q AuditExporter) (int64, error) {
	cw := csv.NewWriter(w)
	defer cw.Flush()

	if err := cw.Write(auditLogCSVHeader); err != nil {
		return 0, err
	}

	var afterCreatedAt = from
	var afterID uuid.UUID // zero on the first call — uuid.Nil is less than every real uuid
	var total int64
	for {
		page, err := q.ListAuditLogV1ForRange(ctx, sqlc.ListAuditLogV1ForRangeParams{
			From:           from,
			To:             to,
			AfterCreatedAt: afterCreatedAt,
			AfterID:        afterID,
			Limit:          auditExportPageSize,
		})
		if err != nil {
			return total, err
		}
		if len(page) == 0 {
			break
		}
		for i := range page {
			if err := cw.Write(auditRowToCSV(page[i])); err != nil {
				return total, err
			}
			total++
		}
		last := page[len(page)-1]
		afterCreatedAt = last.CreatedAt
		afterID = last.ID
		// Short page → we've drained the range.
		if len(page) < auditExportPageSize {
			break
		}
		// Flush periodically so the response-buffered inline path
		// trickles data to the client instead of buffering the whole
		// thing in memory.
		cw.Flush()
		if err := cw.Error(); err != nil {
			return total, err
		}
	}
	return total, nil
}

// WriteAuthEventsCSV is the narrowed-view sibling of WriteAuditLogCSV:
// same shape, but only rows whose `action` matches one of the
// authEventActionPrefixes get emitted. The same streamer underneath
// — the filter is applied row-by-row so we don't materialise the
// full audit log first.
func WriteAuthEventsCSV(ctx context.Context, w io.Writer, from, to time.Time, q AuditExporter) (int64, error) {
	cw := csv.NewWriter(w)
	defer cw.Flush()

	if err := cw.Write(auditLogCSVHeader); err != nil {
		return 0, err
	}

	var afterCreatedAt = from
	var afterID uuid.UUID
	var total int64
	for {
		page, err := q.ListAuditLogV1ForRange(ctx, sqlc.ListAuditLogV1ForRangeParams{
			From:           from,
			To:             to,
			AfterCreatedAt: afterCreatedAt,
			AfterID:        afterID,
			Limit:          auditExportPageSize,
		})
		if err != nil {
			return total, err
		}
		if len(page) == 0 {
			break
		}
		for i := range page {
			if !isAuthEventAction(page[i].Action) {
				continue
			}
			if err := cw.Write(auditRowToCSV(page[i])); err != nil {
				return total, err
			}
			total++
		}
		last := page[len(page)-1]
		afterCreatedAt = last.CreatedAt
		afterID = last.ID
		if len(page) < auditExportPageSize {
			break
		}
		cw.Flush()
		if err := cw.Error(); err != nil {
			return total, err
		}
	}
	return total, nil
}

// auditRowToCSV flattens a sqlc.AuditLog into the auditLogCSVHeader
// columns. The detail JSONB is written as a compact JSON string (no
// pretty-printing) so the CSV stays one-row-per-record.
func auditRowToCSV(r sqlc.AuditLog) []string {
	userID := ""
	if r.UserID.Valid {
		userID = uuid.UUID(r.UserID.Bytes).String()
	}
	ip := ""
	if r.IpAddress != nil {
		ip = r.IpAddress.String()
	}
	detail := ""
	if len(r.Detail) > 0 {
		// The column is stored as JSONB; emit the canonical compact form
		// so empty objects render as `{}` instead of an empty string.
		var v any
		if err := json.Unmarshal(r.Detail, &v); err == nil {
			if buf, mErr := json.Marshal(v); mErr == nil {
				detail = string(buf)
			} else {
				detail = string(r.Detail)
			}
		} else {
			detail = string(r.Detail)
		}
	}
	return []string{
		r.ID.String(),
		r.CreatedAt.UTC().Format(time.RFC3339Nano),
		r.SchemaVersion,
		r.Source,
		r.CorrelationID,
		r.RequestID,
		userID,
		r.ActorAuthMethod,
		r.Action,
		r.ResourceType,
		r.ResourceID,
		r.ResourceName,
		r.HttpMethod,
		r.Path,
		strconv.Itoa(int(r.StatusCode)),
		strconv.FormatInt(r.DurationMs, 10),
		ip,
		r.UserAgent,
		detail,
	}
}

// WriteRBACSnapshotCSV emits every role binding across the three
// scope tables. Each row carries (scope, role_name, source) so an
// auditor can answer "who has admin?" + "did SSO grant it or did
// someone click a button?". User identity is joined in best-effort —
// a deleted user row leaves the username/email columns empty (the
// binding row itself is the authoritative artifact).
func WriteRBACSnapshotCSV(ctx context.Context, w io.Writer, q RBACSnapshotQuerier) (int64, error) {
	cw := csv.NewWriter(w)
	defer cw.Flush()

	if err := cw.Write(rbacSnapshotCSVHeader); err != nil {
		return 0, err
	}

	rows, err := q.ListAllRoleBindingsWithRoleNames(ctx)
	if err != nil {
		return 0, err
	}

	// Resolve each distinct user_id once — a typical install has
	// O(100) users and O(10K) bindings, so caching keeps the
	// snapshot read O(users) not O(bindings).
	userCache := map[uuid.UUID]sqlc.User{}
	resolve := func(id pgtype.UUID) (string, string) {
		if !id.Valid {
			return "", ""
		}
		uid := uuid.UUID(id.Bytes)
		if u, ok := userCache[uid]; ok {
			return u.Username, u.Email
		}
		u, err := q.GetUserByID(ctx, uid)
		if err != nil {
			// Cache the empty result so we don't re-query missing
			// users on every binding row.
			userCache[uid] = sqlc.User{}
			return "", ""
		}
		userCache[uid] = u
		return u.Username, u.Email
	}

	var total int64
	for _, r := range rows {
		username, email := resolve(r.UserID)
		userIDStr := ""
		if r.UserID.Valid {
			userIDStr = uuid.UUID(r.UserID.Bytes).String()
		}
		clusterIDStr := ""
		if r.ClusterID.Valid {
			clusterIDStr = uuid.UUID(r.ClusterID.Bytes).String()
		}
		projectIDStr := ""
		if r.ProjectID.Valid {
			projectIDStr = uuid.UUID(r.ProjectID.Bytes).String()
		}
		if err := cw.Write([]string{
			r.Scope,
			r.BindingID.String(),
			userIDStr,
			username,
			email,
			r.Group,
			r.RoleID.String(),
			r.RoleName,
			clusterIDStr,
			projectIDStr,
			r.Source,
			r.CreatedAt.UTC().Format(time.RFC3339Nano),
		}); err != nil {
			return total, err
		}
		total++
	}
	return total, nil
}

// WriteClusterInventoryCSV emits the per-cluster registry + decommission
// timestamps + a coarse agent-token rotation signal. We don't have a
// rotation-history table, so the "rotation count" column reflects
// what we *do* know: 1 when a token row exists, 0 when not (the
// cluster has never connected an agent). The agent-token last_used_at
// is the more useful column anyway — it answers "is this cluster
// reachable today?".
func WriteClusterInventoryCSV(ctx context.Context, w io.Writer, q ClusterInventoryQuerier) (int64, error) {
	cw := csv.NewWriter(w)
	defer cw.Flush()

	if err := cw.Write(clusterInventoryCSVHeader); err != nil {
		return 0, err
	}

	// ListClusters paginates; pull in chunks until exhausted.
	const pageSize = 500
	var offset int32
	var total int64
	for {
		rows, err := q.ListClusters(ctx, sqlc.ListClustersParams{Limit: pageSize, Offset: offset})
		if err != nil {
			return total, err
		}
		if len(rows) == 0 {
			break
		}
		for _, c := range rows {
			labels := ""
			if len(c.Labels) > 0 {
				labels = string(c.Labels)
			}
			lastHeartbeat := ""
			if c.LastHeartbeat.Valid {
				lastHeartbeat = c.LastHeartbeat.Time.UTC().Format(time.RFC3339Nano)
			}
			decommissioned := ""
			if c.DecommissionedAt.Valid {
				decommissioned = c.DecommissionedAt.Time.UTC().Format(time.RFC3339Nano)
			}

			// Per-cluster agent token lookup is best-effort — a fresh
			// cluster with no agent will pgx.ErrNoRows and we just
			// leave the columns empty.
			rotation := "0"
			tokenLastUsed := ""
			if tok, err := q.GetClusterAgentTokenByClusterID(ctx, c.ID); err == nil {
				rotation = "1"
				if tok.LastUsedAt.Valid {
					tokenLastUsed = tok.LastUsedAt.Time.UTC().Format(time.RFC3339Nano)
				}
			}

			if err := cw.Write([]string{
				c.ID.String(),
				c.Name,
				c.DisplayName,
				c.Environment,
				c.Region,
				c.Provider,
				c.Distribution,
				c.Status,
				labels,
				c.CreatedAt.UTC().Format(time.RFC3339Nano),
				decommissioned,
				lastHeartbeat,
				tokenLastUsed,
				rotation,
			}); err != nil {
				return total, err
			}
			total++
		}
		if len(rows) < pageSize {
			break
		}
		offset += int32(len(rows))
	}
	return total, nil
}

// WriteAccessTokensCSV emits every API token (including revoked) with
// the migration-044 hardening columns. The token_hash column is
// explicitly absent — see ComplianceAPITokenRow's struct comment for
// the SOC 2 CC6.7 rationale.
func WriteAccessTokensCSV(ctx context.Context, w io.Writer, q AccessTokenQuerier) (int64, error) {
	cw := csv.NewWriter(w)
	defer cw.Flush()

	if err := cw.Write(accessTokensCSVHeader); err != nil {
		return 0, err
	}

	rows, err := q.ListAPITokensForCompliance(ctx)
	if err != nil {
		return 0, err
	}

	var total int64
	for _, t := range rows {
		scopes := ""
		if len(t.Scopes) > 0 {
			scopes = string(t.Scopes)
		}
		expires := ""
		if t.ExpiresAt.Valid {
			expires = t.ExpiresAt.Time.UTC().Format(time.RFC3339Nano)
		}
		lastUsed := ""
		if t.LastUsedAt.Valid {
			lastUsed = t.LastUsedAt.Time.UTC().Format(time.RFC3339Nano)
		}
		if err := cw.Write([]string{
			t.ID.String(),
			t.UserID.String(),
			t.Username,
			t.Email,
			t.Name,
			t.Prefix,
			scopes,
			t.AllowedCIDRs,
			t.LastSeenRemoteIP,
			t.CreatedAt.UTC().Format(time.RFC3339Nano),
			expires,
			lastUsed,
			strconv.FormatBool(t.IsRevoked),
		}); err != nil {
			return total, err
		}
		total++
	}
	return total, nil
}

// WriteBackupDrillHistoryCSV emits every backup_drill_results row.
// Mirrors what the /admin/backup-drill/history/ endpoint returns but
// without pagination — the whole history is small (one row per
// weekly drill, so ~50 rows/year).
func WriteBackupDrillHistoryCSV(ctx context.Context, w io.Writer, q BackupDrillHistoryQuerier) (int64, error) {
	cw := csv.NewWriter(w)
	defer cw.Flush()

	if err := cw.Write(backupDrillHistoryCSVHeader); err != nil {
		return 0, err
	}

	total, err := q.CountBackupDrillResults(ctx)
	if err != nil {
		// Don't fail the writer — fall back to "fetch a generous
		// upper bound" instead. The drill table is small enough that
		// the count failure is almost always a transient connection
		// issue, not a real "too many rows" case.
		total = 10000
	}
	if total <= 0 {
		// Empty table is a valid state pre-first-drill.
		return 0, nil
	}
	rows, err := q.ListBackupDrillResults(ctx, sqlc.ListBackupDrillResultsParams{
		Limit:  int32(total),
		Offset: 0,
	})
	if err != nil {
		return 0, err
	}
	var written int64
	for _, r := range rows {
		finished := ""
		if r.FinishedAt.Valid {
			finished = r.FinishedAt.Time.UTC().Format(time.RFC3339Nano)
		}
		schemaVer := ""
		if r.SchemaVersion.Valid {
			schemaVer = strconv.Itoa(int(r.SchemaVersion.Int32))
		}
		if err := cw.Write([]string{
			r.ID.String(),
			r.StartedAt.UTC().Format(time.RFC3339Nano),
			finished,
			r.Status,
			schemaVer,
			r.BackupKey,
			r.ErrorMessage,
		}); err != nil {
			return written, err
		}
		written++
	}
	return written, nil
}

// ── policy-snapshot.json writer ─────────────────────────────────────────

// PolicySnapshotEntry is the per-project entry the JSON writer emits.
// Members is the list of user-scoped project_role_bindings — the
// compliance bundle's notion of "who has access to this project".
// Group-scoped bindings (UserID null) are written as `group:<name>`
// strings so an auditor reading the JSON sees the IdP groups
// alongside the individual humans.
type PolicySnapshotEntry struct {
	ID                       string   `json:"id"`
	Name                     string   `json:"name"`
	DisplayName              string   `json:"display_name"`
	ClusterID                string   `json:"cluster_id"`
	PodSecurityProfile       string   `json:"pod_security_profile"`
	NetworkPolicyMode        string   `json:"network_policy_mode"`
	ResourceQuotaCPULimit    string   `json:"resource_quota_cpu_limit"`
	ResourceQuotaMemoryLimit string   `json:"resource_quota_memory_limit"`
	ResourceQuotaPodCount    int32    `json:"resource_quota_pod_count"`
	Members                  []string `json:"members"`
}

// WritePolicySnapshotJSON emits the policy-snapshot.json file —
// pretty-printed JSON with one entry per project. Members come from
// the per-project role bindings; user_ids are resolved best-effort
// to "<username> (<email>)" strings so the file is readable without a
// db.
func WritePolicySnapshotJSON(ctx context.Context, w io.Writer, q ProjectPolicyQuerier, users RBACSnapshotQuerier) error {
	projects, err := q.ListAllProjectsForCompliance(ctx)
	if err != nil {
		return err
	}

	out := make([]PolicySnapshotEntry, 0, len(projects))
	userCache := map[uuid.UUID]string{}
	resolve := func(id pgtype.UUID) string {
		if !id.Valid {
			return ""
		}
		uid := uuid.UUID(id.Bytes)
		if v, ok := userCache[uid]; ok {
			return v
		}
		if users == nil {
			userCache[uid] = uid.String()
			return uid.String()
		}
		u, err := users.GetUserByID(ctx, uid)
		if err != nil {
			userCache[uid] = uid.String()
			return uid.String()
		}
		label := u.Username
		if u.Email != "" {
			label = fmt.Sprintf("%s (%s)", u.Username, u.Email)
		}
		userCache[uid] = label
		return label
	}

	for _, p := range projects {
		entry := PolicySnapshotEntry{
			ID:                       p.ID.String(),
			Name:                     p.Name,
			DisplayName:              p.DisplayName,
			ClusterID:                p.ClusterID.String(),
			PodSecurityProfile:       p.PodSecurityProfile,
			NetworkPolicyMode:        p.NetworkPolicyMode,
			ResourceQuotaCPULimit:    p.ResourceQuotaCpuLimit,
			ResourceQuotaMemoryLimit: p.ResourceQuotaMemoryLimit,
			ResourceQuotaPodCount:    p.ResourceQuotaPodCount,
			Members:                  []string{},
		}
		// 200 per page should comfortably hold the members of any
		// realistic project — exceeding it just truncates, which is
		// fine because the rbac-snapshot.csv is the canonical record.
		bindings, err := q.ListProjectRoleBindingsByProject(ctx, sqlc.ListProjectRoleBindingsByProjectParams{
			ProjectID: p.ID,
			Limit:     200,
			Offset:    0,
		})
		if err == nil {
			for _, b := range bindings {
				if b.UserID.Valid {
					if label := resolve(b.UserID); label != "" {
						entry.Members = append(entry.Members, label)
					}
				} else if b.Group != "" {
					entry.Members = append(entry.Members, "group:"+b.Group)
				}
			}
		}
		out = append(out, entry)
	}

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}
