// Package handler — support bundle.
//
// Generates a downloadable .zip of platform diagnostics suitable for sharing
// with whoever is debugging an Astronomer install. Two design rules:
//
//  1. The bundle MUST NOT contain plaintext credentials. Anything that could
//     wrap a secret (password hashes, API tokens, Argo auth tokens, CA
//     certificates) is replaced with `[redacted N bytes]`.
//
//  2. The bundle MUST stream — we don't want to buffer 10MB of pod logs in
//     RAM. Everything writes directly to an archive/zip.Writer that wraps
//     the response writer.
package handler

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
	"github.com/alphabravocompany/astronomer-go/pkg/version"
)

// SupportBundleQuerier is the slice of sqlc Queries that the bundle reads.
type SupportBundleQuerier interface {
	ListClusters(ctx context.Context, arg sqlc.ListClustersParams) ([]sqlc.Cluster, error)
	ListUsers(ctx context.Context, arg sqlc.ListUsersParams) ([]sqlc.User, error)
	GetUserByID(ctx context.Context, id uuid.UUID) (sqlc.User, error)
	GetPlatformConfig(ctx context.Context) (sqlc.PlatformConfiguration, error)
	ListArgoCDInstances(ctx context.Context, arg sqlc.ListArgoCDInstancesParams) ([]sqlc.ArgocdInstance, error)
	ListAuditLogV1(ctx context.Context, arg sqlc.ListAuditLogsParams) ([]sqlc.AuditLog, error)
	// ListActiveConnections drives the agent-connections bundle section
	// (FEATURES-051126 T11) — last-seen + cluster_id are exactly what an
	// L3 engineer needs when triaging a "why are these clusters offline?"
	// question.
	ListActiveConnections(ctx context.Context) ([]sqlc.AgentConnection, error)
}

// SupportBundleAsynqInspector is the slice of asynq.Inspector the bundle
// needs to capture queue + DLQ state. *asynq.Inspector satisfies this. Kept
// behind an interface so tests can inject a fake; nil means "skip the
// asynq sections cleanly".
type SupportBundleAsynqInspector interface {
	Queues() ([]string, error)
	GetQueueInfo(qname string) (*asynq.QueueInfo, error)
	ListArchivedTasks(qname string, opts ...asynq.ListOption) ([]*asynq.TaskInfo, error)
}

// SupportBundleDBPooler exposes the minimum surface needed to run the
// raw SELECT against schema_migrations. *pgxpool.Pool satisfies this.
type SupportBundleDBPooler interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// SupportBundleHandler wraps the GET /api/v1/support-bundle/ endpoint.
type SupportBundleHandler struct {
	queries   SupportBundleQuerier
	k8s       kubernetes.Interface
	namespace string
	// Optional, wired via setters; nil-safe section writers degrade
	// gracefully when these are absent.
	inspector SupportBundleAsynqInspector
	db        SupportBundleDBPooler
}

// SetAsynqInspector wires the asynq queue inspector. Enables the
// asynq-queues.json section (FEATURES-051126 T11). nil disables it.
func (h *SupportBundleHandler) SetAsynqInspector(insp SupportBundleAsynqInspector) {
	if h == nil {
		return
	}
	h.inspector = insp
}

// SetDBPool wires the raw DB pool. Enables the schema-migrations.json
// section (which reads a non-sqlc table). nil disables it.
func (h *SupportBundleHandler) SetDBPool(pool SupportBundleDBPooler) {
	if h == nil {
		return
	}
	h.db = pool
}

// NewSupportBundleHandler returns a handler. k8sClient and namespace are
// optional: when nil/empty, pod-state and pod-logs sections are skipped
// and the bundle just contains DB-derived sections.
func NewSupportBundleHandler(queries SupportBundleQuerier, k8sClient kubernetes.Interface, namespace string) *SupportBundleHandler {
	return &SupportBundleHandler{
		queries:   queries,
		k8s:       k8sClient,
		namespace: namespace,
	}
}

// Download streams the bundle as a zip. Only superusers can call it: it
// surfaces audit-log entries and platform internals that aren't safe to
// share with non-admins.
func (h *SupportBundleHandler) Download(w http.ResponseWriter, r *http.Request) {
	caller, ok := middleware.GetAuthenticatedUser(r.Context())
	if !ok {
		RespondError(w, http.StatusUnauthorized, "authentication_required", "Authentication required")
		return
	}
	callerID, err := uuid.Parse(caller.ID)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "internal_error", "Invalid user ID")
		return
	}
	dbUser, err := h.queries.GetUserByID(r.Context(), callerID)
	if err != nil {
		RespondError(w, http.StatusForbidden, "forbidden", "Caller not found")
		return
	}
	if !dbUser.IsSuperuser {
		RespondError(w, http.StatusForbidden, "forbidden",
			"Support bundle download requires superuser privileges")
		return
	}

	// Read-only superuser endpoint that exposes platform internals — leave
	// an explicit audit trail. The mutating-HTTP audit middleware skips
	// GET, so this trail wouldn't otherwise exist.
	recordAudit(r, h.queries, "admin.support_bundle.downloaded",
		"platform", "", "support-bundle", nil)

	filename := fmt.Sprintf("astronomer-support-bundle-%s.zip",
		time.Now().UTC().Format("20060102-150405"))
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)

	zw := zip.NewWriter(w)
	defer zw.Close()

	// Each writer is best-effort: a per-section failure shouldn't doom the
	// whole bundle. We collect errors into a manifest file at the end so the
	// caller can see what's missing.
	collected := newSectionLog()

	h.writeMeta(r.Context(), zw, collected)
	h.writePlatformConfig(r.Context(), zw, collected)
	h.writeClusters(r.Context(), zw, collected)
	h.writeUsers(r.Context(), zw, collected)
	h.writeArgoCDInstances(r.Context(), zw, collected)
	h.writeAuditLog(r.Context(), zw, collected)
	h.writePods(r.Context(), zw, collected)
	h.writePodLogs(r.Context(), zw, collected)
	// FEATURES-051126 T11 — extra context an L3 engineer needs without
	// shell access to the cluster:
	h.writeEvents(r.Context(), zw, collected)
	h.writeHelmRelease(r.Context(), zw, collected)
	h.writeSchemaMigrations(r.Context(), zw, collected)
	h.writeAsynqQueues(r.Context(), zw, collected)
	h.writeAgentConnections(r.Context(), zw, collected)
	h.writeReadme(zw, collected)
}

// ── individual section writers ──────────────────────────────────────────

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
			"ca_certificate":     redactBytes(c.CaCertificate),
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
		if c.LastHeartbeat.Valid {
			entry["last_heartbeat"] = c.LastHeartbeat.Time
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

func (h *SupportBundleHandler) writeArgoCDInstances(ctx context.Context, zw *zip.Writer, log *sectionLog) {
	rows, err := h.queries.ListArgoCDInstances(ctx, sqlc.ListArgoCDInstancesParams{Limit: 100, Offset: 0})
	if err != nil {
		log.section("argocd-instances.json", err)
		return
	}
	redacted := make([]map[string]any, 0, len(rows))
	for _, a := range rows {
		redacted = append(redacted, map[string]any{
			"id":                   a.ID.String(),
			"name":                 a.Name,
			"cluster_id":           a.ClusterID.String(),
			"api_url":              a.ApiUrl,
			"verify_ssl":           a.VerifySsl,
			"auth_token_encrypted": redactBytes(a.AuthTokenEncrypted),
			"created_at":           a.CreatedAt,
		})
	}
	log.section("argocd-instances.json", writeBundleJSON(zw, "argocd-instances.json", redacted))
}

func (h *SupportBundleHandler) writeAuditLog(ctx context.Context, zw *zip.Writer, log *sectionLog) {
	rows, err := h.queries.ListAuditLogV1(ctx, sqlc.ListAuditLogsParams{Limit: 500, Offset: 0})
	if err != nil {
		log.section("audit-log-recent.json", err)
		return
	}
	log.section("audit-log-recent.json", writeBundleJSON(zw, "audit-log-recent.json", rows))
}

func (h *SupportBundleHandler) writePods(ctx context.Context, zw *zip.Writer, log *sectionLog) {
	if h.k8s == nil || h.namespace == "" {
		log.skipped("pods.json", "k8s client not wired")
		return
	}
	listCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	pods, err := h.k8s.CoreV1().Pods(h.namespace).List(listCtx, metav1.ListOptions{})
	if err != nil {
		log.section("pods.json", err)
		return
	}
	out := make([]map[string]any, 0, len(pods.Items))
	for _, p := range pods.Items {
		out = append(out, map[string]any{
			"name":              p.Name,
			"phase":             string(p.Status.Phase),
			"node":              p.Spec.NodeName,
			"start_time":        p.Status.StartTime,
			"container_statuses": summarizeContainers(p.Status.ContainerStatuses),
			"creation_timestamp": p.CreationTimestamp,
		})
	}
	log.section("pods.json", writeBundleJSON(zw, "pods.json", out))
}

func (h *SupportBundleHandler) writePodLogs(ctx context.Context, zw *zip.Writer, log *sectionLog) {
	if h.k8s == nil || h.namespace == "" {
		log.skipped("pod-logs/", "k8s client not wired")
		return
	}
	listCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	pods, err := h.k8s.CoreV1().Pods(h.namespace).List(listCtx, metav1.ListOptions{})
	cancel()
	if err != nil {
		log.section("pod-logs/", err)
		return
	}
	tailLines := int64(200)
	for _, p := range pods.Items {
		for _, c := range p.Spec.Containers {
			name := fmt.Sprintf("pod-logs/%s_%s.log", p.Name, c.Name)
			logsCtx, lcancel := context.WithTimeout(ctx, 15*time.Second)
			rc, err := h.k8s.CoreV1().Pods(h.namespace).GetLogs(p.Name, &corev1.PodLogOptions{
				Container: c.Name,
				TailLines: &tailLines,
			}).Stream(logsCtx)
			if err != nil {
				log.section(name, err)
				lcancel()
				continue
			}
			fw, err := zw.Create(name)
			if err != nil {
				rc.Close()
				lcancel()
				log.section(name, err)
				continue
			}
			_, copyErr := io.Copy(fw, rc)
			rc.Close()
			lcancel()
			log.section(name, copyErr)
		}
	}
}

// writeEvents captures the namespace's k8s Events for the last 24h or so
// (k8s default retention is 1h-ish but we'll grab whatever's there). One
// of the things support engineers ask for first when something's broken.
func (h *SupportBundleHandler) writeEvents(ctx context.Context, zw *zip.Writer, log *sectionLog) {
	if h.k8s == nil || h.namespace == "" {
		log.skipped("events.json", "k8s client not wired")
		return
	}
	lctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	events, err := h.k8s.CoreV1().Events(h.namespace).List(lctx, metav1.ListOptions{Limit: 500})
	if err != nil {
		log.section("events.json", err)
		return
	}
	out := make([]map[string]any, 0, len(events.Items))
	for _, e := range events.Items {
		out = append(out, map[string]any{
			"type":            e.Type,
			"reason":          e.Reason,
			"message":         e.Message,
			"object_kind":     e.InvolvedObject.Kind,
			"object_name":     e.InvolvedObject.Name,
			"first_timestamp": e.FirstTimestamp,
			"last_timestamp":  e.LastTimestamp,
			"count":           e.Count,
		})
	}
	log.section("events.json", writeBundleJSON(zw, "events.json", out))
}

// writeHelmRelease snapshots the chart's helm release secret (kind
// helm.sh/release.v1) so support engineers can see exactly what
// values + manifest version the install is running. We strip the binary
// blob (the compressed JSON release payload itself is large + opaque)
// and surface the labels which carry version + status.
func (h *SupportBundleHandler) writeHelmRelease(ctx context.Context, zw *zip.Writer, log *sectionLog) {
	if h.k8s == nil || h.namespace == "" {
		log.skipped("helm-releases.json", "k8s client not wired")
		return
	}
	lctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	secrets, err := h.k8s.CoreV1().Secrets(h.namespace).List(lctx, metav1.ListOptions{
		FieldSelector: "type=helm.sh/release.v1",
	})
	if err != nil {
		log.section("helm-releases.json", err)
		return
	}
	out := make([]map[string]any, 0, len(secrets.Items))
	for _, s := range secrets.Items {
		entry := map[string]any{
			"name":              s.Name,
			"created":           s.CreationTimestamp,
			"labels":            s.Labels,
			"data_size_bytes":   sumSecretBytes(s),
		}
		out = append(out, entry)
	}
	log.section("helm-releases.json", writeBundleJSON(zw, "helm-releases.json", out))
}

// writeSchemaMigrations surfaces the migrate-binary state table. The dirty
// flag is what an L3 engineer needs to see when a release is stuck on
// migration recovery — same signal T13 added to the preflight Job.
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
					"id":         t.ID,
					"type":       t.Type,
					"retried":    t.Retried,
					"last_err":   t.LastErr,
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
	lctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	rows, err := h.queries.ListActiveConnections(lctx)
	if err != nil {
		log.section("agent-connections.json", err)
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
	b.WriteString(fmt.Sprintf("Generated: %s\n", time.Now().UTC().Format(time.RFC3339)))
	b.WriteString(fmt.Sprintf("Server:    %s (%s)\n\n", version.Version, version.GitCommit))
	b.WriteString("Contents:\n")
	for _, line := range log.lines {
		b.WriteString("  - " + line + "\n")
	}
	b.WriteString("\nRedactions:\n")
	b.WriteString("  - users.password         → [redacted bcrypt hash]\n")
	b.WriteString("  - clusters.ca_certificate → [redacted N bytes]\n")
	b.WriteString("  - argocd_instances.auth_token_encrypted → [redacted N bytes]\n")
	b.WriteString("\nThis bundle may still contain other sensitive information " +
		"(emails, audit-log payloads, pod log contents).\n")
	b.WriteString("Share only with people authorized to triage this install.\n")
	fw, err := zw.Create("README.txt")
	if err == nil {
		_, _ = fw.Write([]byte(b.String()))
	}
}

// ── helpers ────────────────────────────────────────────────────────────

type sectionLog struct {
	lines []string
}

func newSectionLog() *sectionLog { return &sectionLog{} }

func (s *sectionLog) section(name string, err error) {
	if err == nil {
		s.lines = append(s.lines, name+"  OK")
		return
	}
	s.lines = append(s.lines, name+"  FAILED: "+err.Error())
}

func (s *sectionLog) skipped(name, reason string) {
	s.lines = append(s.lines, name+"  SKIPPED: "+reason)
}

func writeBundleJSON(zw *zip.Writer, name string, payload any) error {
	fw, err := zw.Create(name)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(fw)
	enc.SetIndent("", "  ")
	return enc.Encode(payload)
}

func redactBytes(s string) string {
	if s == "" {
		return ""
	}
	return fmt.Sprintf("[redacted %d bytes]", len(s))
}

// sumSecretBytes is a cheap "how big is this helm release blob" probe for
// the helm-releases.json section. We don't include the actual data —
// helm releases are compressed JSON manifests that can run to hundreds
// of kilobytes and would balloon the bundle.
func sumSecretBytes(s corev1.Secret) int {
	total := 0
	for _, v := range s.Data {
		total += len(v)
	}
	return total
}

func summarizeContainers(statuses []corev1.ContainerStatus) []map[string]any {
	out := make([]map[string]any, 0, len(statuses))
	for _, cs := range statuses {
		entry := map[string]any{
			"name":          cs.Name,
			"image":         cs.Image,
			"ready":         cs.Ready,
			"restart_count": cs.RestartCount,
		}
		if cs.State.Waiting != nil {
			entry["state"] = "Waiting"
			entry["reason"] = cs.State.Waiting.Reason
		} else if cs.State.Running != nil {
			entry["state"] = "Running"
			entry["started_at"] = cs.State.Running.StartedAt
		} else if cs.State.Terminated != nil {
			entry["state"] = "Terminated"
			entry["reason"] = cs.State.Terminated.Reason
			entry["exit_code"] = cs.State.Terminated.ExitCode
		}
		out = append(out, entry)
	}
	return out
}
