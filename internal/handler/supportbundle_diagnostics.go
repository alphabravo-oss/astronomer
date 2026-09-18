package handler

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/pkg/version"
	"github.com/hibiken/asynq"
)

func (h *SupportBundleHandler) writeSchemaMigrations(ctx context.Context, zw *zip.Writer, log *sectionLog) {
	if h.db == nil {
		log.skipped("schema-migrations.json", "db pool not wired")
		return
	}
	lctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var version int64
	var dirty bool
	err := h.db.QueryRow(lctx, "SELECT version, dirty FROM schema_migrations").Scan(&version, &dirty)
	if err != nil {
		log.section("schema-migrations.json", err)
		return
	}
	payload := map[string]any{"version": version, "dirty": dirty}
	log.section("schema-migrations.json", writeBundleJSON(zw, "schema-migrations.json", payload))
}

// writeDeliveryDiagnostics emits bounded, secret-free delivery and catalog
// state. It intentionally excludes source URLs, credentials, values,
// manifests, condition messages, and Kubernetes Secret-shaped objects.
func (h *SupportBundleHandler) writeDeliveryDiagnostics(ctx context.Context, zw *zip.Writer, log *sectionLog) {
	if h.db == nil {
		log.skipped("delivery-diagnostics.json", "db pool not wired")
		return
	}
	lctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	const query = `
SELECT jsonb_build_object(
  'catalog', jsonb_build_object(
    'catalogs', (SELECT count(*) FROM delivery_catalogs),
    'entries', (SELECT count(*) FROM catalog_blessed_charts WHERE slug <> ''),
    'last_success_at', (SELECT max(last_synced_at) FROM delivery_catalogs),
    'last_attempt_at', (SELECT max(last_sync_attempted_at) FROM delivery_catalogs),
    'degraded', (SELECT count(*) FROM delivery_catalogs WHERE last_sync_error <> '' OR verification_status IN ('failed','revoked'))
  ),
  'delivery', jsonb_build_object(
    'sources', (SELECT count(*) FROM delivery_sources),
    'targets', (SELECT count(*) FROM delivery_targets WHERE deletion_state <> 'deleted'),
    'rollouts', (SELECT COALESCE(jsonb_object_agg(state, count), '{}'::jsonb) FROM (SELECT state, count(*) AS count FROM delivery_rollouts GROUP BY state) s),
    'deployments', (SELECT COALESCE(jsonb_object_agg(phase, count), '{}'::jsonb) FROM (SELECT phase, count(*) AS count FROM cluster_deployments GROUP BY phase) d),
    'stale_deployments', (SELECT count(*) FROM cluster_deployments WHERE phase NOT IN ('pending','removed') AND (last_observed_at IS NULL OR last_observed_at < clock_timestamp() - interval '5 minutes')),
    'flux_not_ready', (SELECT count(*) FROM delivery_controller_inventory WHERE NOT ready)
  )
)`
	var payload []byte
	if err := h.db.QueryRow(lctx, query).Scan(&payload); err != nil {
		log.section("delivery-diagnostics.json", err)
		return
	}
	log.section("delivery-diagnostics.json", writeBundleJSON(zw, "delivery-diagnostics.json", json.RawMessage(payload)))
}

// writeAsynqQueues captures live queue depth + the last batch of dead-
// letter task IDs. The DLQ is the single most useful artifact when
// triaging "why isn't my install reconciling".
func (h *SupportBundleHandler) writeAsynqQueues(ctx context.Context, zw *zip.Writer, log *sectionLog) {
	if h.inspector == nil {
		log.skipped("asynq-queues.json", "asynq inspector not wired")
		return
	}
	queues, err := h.inspector.Queues()
	if err != nil {
		log.section("asynq-queues.json", err)
		return
	}
	out := map[string]any{}
	for _, q := range queues {
		info, ierr := h.inspector.GetQueueInfo(q)
		if ierr != nil {
			out[q] = map[string]any{"error": ierr.Error()}
			continue
		}
		queueOut := map[string]any{
			"size":      info.Size,
			"active":    info.Active,
			"pending":   info.Pending,
			"scheduled": info.Scheduled,
			"retry":     info.Retry,
			"archived":  info.Archived,
			"completed": info.Completed,
		}
		// First 50 DLQ entries — full task payloads can contain secrets,
		// so we just surface IDs + types + last error.
		archived, aerr := h.inspector.ListArchivedTasks(q, asynq.PageSize(50))
		if aerr == nil {
			dlq := make([]map[string]any, 0, len(archived))
			for _, t := range archived {
				dlq = append(dlq, map[string]any{
					"id":             t.ID,
					"type":           t.Type,
					"retried":        t.Retried,
					"last_err":       t.LastErr,
					"last_failed_at": t.LastFailedAt,
				})
			}
			queueOut["archived_tasks"] = dlq
		}
		out[q] = queueOut
	}
	log.section("asynq-queues.json", writeBundleJSON(zw, "asynq-queues.json", out))
}

// writeAgentConnections snapshots the active rows from agent_connections.
// Each row carries cluster_id + last_ping_at, which is what an engineer
// needs to answer "why does the dashboard say this cluster is offline?".
// IP addresses are kept; tokens are redacted (they're not stored on this
// table anyway, but defense in depth).
func (h *SupportBundleHandler) writeAgentConnections(ctx context.Context, zw *zip.Writer, log *sectionLog) {
	const maxSupportBundleAgentConnections = 5_000
	lctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	rows, err := h.queries.ListActiveConnections(lctx, maxSupportBundleAgentConnections+1)
	if err != nil {
		log.section("agent-connections.json", err)
		return
	}
	if len(rows) > maxSupportBundleAgentConnections {
		log.section("agent-connections.json", fmt.Errorf("active connection count exceeds support-bundle limit of %d", maxSupportBundleAgentConnections))
		return
	}
	out := make([]map[string]any, 0, len(rows))
	for _, c := range rows {
		out = append(out, map[string]any{
			"id":              c.ID.String(),
			"cluster_id":      c.ClusterID.String(),
			"agent_id":        c.AgentID,
			"agent_version":   c.AgentVersion,
			"status":          c.Status,
			"connected_at":    c.ConnectedAt,
			"last_ping":       c.LastPing,
			"disconnected_at": c.DisconnectedAt,
		})
	}
	log.section("agent-connections.json", writeBundleJSON(zw, "agent-connections.json", out))
}

func (h *SupportBundleHandler) writeReadme(zw *zip.Writer, log *sectionLog) {
	var b strings.Builder
	b.WriteString("Astronomer support bundle\n")
	b.WriteString("=========================\n\n")
	fmt.Fprintf(&b, "Generated: %s\n", time.Now().UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "Server:    %s (%s)\n\n", version.Version, version.GitCommit)
	b.WriteString("Contents:\n")
	for _, line := range log.lines {
		b.WriteString("  - " + line + "\n")
	}
	b.WriteString("\nRedactions:\n")
	b.WriteString("  - sensitive JSON keys and credential-shaped values → [redacted]\n")
	b.WriteString("  - private keys and kubeconfig-shaped values → [redacted private key] / [redacted kubeconfig]\n")
	b.WriteString("  - sensitive pod log lines → [redacted sensitive log line]\n")
	b.WriteString("\nThis bundle may still contain other sensitive information " +
		"(emails, resource names, and non-secret operational metadata).\n")
	b.WriteString("Share only with people authorized to triage this install.\n")
	fw, err := zw.Create("README.txt")
	if err == nil {
		_, _ = fw.Write([]byte(b.String()))
	}
}

// ── helpers ────────────────────────────────────────────────────────────
