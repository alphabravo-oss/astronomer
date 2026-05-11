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
}

// SupportBundleHandler wraps the GET /api/v1/support-bundle/ endpoint.
type SupportBundleHandler struct {
	queries   SupportBundleQuerier
	k8s       kubernetes.Interface
	namespace string
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
