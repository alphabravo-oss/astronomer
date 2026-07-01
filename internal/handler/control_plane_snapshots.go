// Control-plane (etcd) DR snapshot handler (migration 125).
//
// This is the DISASTER-RECOVERY counterpart to the Velero workload
// snapshots in cluster_snapshots.go. Where Velero backs up workloads +
// PVs into an object store, this feature snapshots the Kubernetes
// control plane's own etcd datastore so an operator can rebuild a
// cluster whose control plane was lost.
//
// SELF-MANAGED ONLY. k3s / RKE2 / kubeadm run their own etcd on nodes we
// can reach through the tunnel, so we can drive the distro's native
// snapshot command via a one-shot privileged Job. Managed control planes
// (EKS / GKE / AKS) hide etcd behind the cloud provider — there is no
// reachable etcd to snapshot — so TriggerSnapshot refuses them with a
// 409 and writes no row.
//
// Routes owned by this handler (all under /api/v1, gated behind the
// feature.control_plane_snapshots flag at the routes layer):
//
//   POST /clusters/{cluster_id}/control-plane-snapshots/            — trigger
//   GET  /clusters/{cluster_id}/control-plane-snapshots/            — list (paged)
//   GET  /clusters/{cluster_id}/control-plane-snapshots/{id}/       — get
//   POST /clusters/{cluster_id}/control-plane-snapshots/{id}/restore/ — RUNBOOK
//
// Restore is intentionally NOT automated: restoring an etcd snapshot is
// an OFFLINE node operation (stop the server, run the distro's
// --cluster-reset restore, restart) that cannot be performed safely from
// a running cluster. The restore endpoint returns a distribution-aware
// runbook instead of attempting anything.

package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
)

// controlPlaneSnapshotJobImage is the image the one-shot snapshot Job
// runs. It only needs `nsenter` (a busybox applet) — the actual snapshot
// binary (k3s / rke2 / etcdctl) is executed from the HOST via nsenter,
// so the image itself carries no distro tooling.
const controlPlaneSnapshotJobImage = "busybox:1.36"

// controlPlaneSnapshotJobNamespace is the namespace the snapshot Job is
// created in. kube-system exists on every distribution and already
// tolerates privileged system workloads.
const controlPlaneSnapshotJobNamespace = "kube-system"

// ControlPlaneSnapshotQuerier is the narrow DB surface the handler needs.
// Defined locally so unit tests can supply a fake without the full
// *sqlc.Queries. Satisfied by *sqlc.Queries in production.
type ControlPlaneSnapshotQuerier interface {
	GetClusterByID(ctx context.Context, id uuid.UUID) (sqlc.Cluster, error)

	CreateControlPlaneSnapshot(ctx context.Context, arg sqlc.CreateControlPlaneSnapshotParams) (sqlc.ControlPlaneSnapshot, error)
	GetControlPlaneSnapshotByID(ctx context.Context, id uuid.UUID) (sqlc.ControlPlaneSnapshot, error)
	ListControlPlaneSnapshotsByCluster(ctx context.Context, arg sqlc.ListControlPlaneSnapshotsByClusterParams) ([]sqlc.ControlPlaneSnapshot, error)
	CountControlPlaneSnapshotsByCluster(ctx context.Context, clusterID uuid.UUID) (int64, error)
	MarkControlPlaneSnapshotStatus(ctx context.Context, arg sqlc.MarkControlPlaneSnapshotStatusParams) error
	MarkControlPlaneSnapshotFailed(ctx context.Context, arg sqlc.MarkControlPlaneSnapshotFailedParams) error
}

// ControlPlaneSnapshotHandler owns the /control-plane-snapshots/* routes.
// The K8sRequester is the same tunnel-backed requester ResourceHandler /
// ClusterSnapshotsHandler use to drive the member cluster's apiserver;
// it's attached post-construction via SetRequester so test wiring stays
// minimal (a nil requester degrades to a clear apply error).
type ControlPlaneSnapshotHandler struct {
	queries   ControlPlaneSnapshotQuerier
	requester K8sRequester
}

// NewControlPlaneSnapshotHandler wires the handler against the querier.
func NewControlPlaneSnapshotHandler(queries ControlPlaneSnapshotQuerier) *ControlPlaneSnapshotHandler {
	return &ControlPlaneSnapshotHandler{queries: queries}
}

// SetRequester attaches the tunnel-backed K8sRequester used to POST the
// snapshot Job to the member cluster.
func (h *ControlPlaneSnapshotHandler) SetRequester(r K8sRequester) {
	if h == nil {
		return
	}
	h.requester = r
}

// ----------------------------------------------------------------------
// Distribution eligibility
// ----------------------------------------------------------------------

// controlPlaneSnapshotDistro normalizes a cluster's recorded distribution
// into one of the self-managed families whose etcd we can snapshot, and
// reports whether the distribution is eligible at all.
//
// ok=false for managed control planes (EKS/GKE/AKS), OpenShift, and any
// unknown/empty value — refusing by default keeps the safe behavior
// (flag off / unknown distro == no snapshot attempt).
func controlPlaneSnapshotDistro(distribution string) (family string, ok bool) {
	d := strings.ToLower(strings.TrimSpace(distribution))
	switch {
	case strings.Contains(d, "k3s"), strings.Contains(d, "k3d"):
		return "k3s", true
	case strings.Contains(d, "rke2"):
		return "rke2", true
	case strings.Contains(d, "kubeadm"):
		return "kubeadm", true
	default:
		return "", false
	}
}

// ----------------------------------------------------------------------
// DTOs
// ----------------------------------------------------------------------

// ControlPlaneSnapshotResponse is the wire DTO for a snapshot row.
type ControlPlaneSnapshotResponse struct {
	ID          uuid.UUID  `json:"id"`
	ClusterID   uuid.UUID  `json:"cluster_id"`
	Name        string     `json:"name"`
	Status      string     `json:"status"`
	Location    string     `json:"location"`
	SizeBytes   *int64     `json:"size_bytes,omitempty"`
	RequestedBy *uuid.UUID `json:"requested_by_id,omitempty"`
	Error       string     `json:"error,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

func controlPlaneSnapshotToResponse(row sqlc.ControlPlaneSnapshot) ControlPlaneSnapshotResponse {
	out := ControlPlaneSnapshotResponse{
		ID:        row.ID,
		ClusterID: row.ClusterID,
		Name:      row.Name,
		Status:    row.Status,
		Location:  row.Location,
		Error:     row.Error,
		CreatedAt: row.CreatedAt,
	}
	if row.SizeBytes.Valid {
		v := row.SizeBytes.Int64
		out.SizeBytes = &v
	}
	if row.RequestedByID.Valid {
		id := uuid.UUID(row.RequestedByID.Bytes)
		out.RequestedBy = &id
	}
	if row.CompletedAt.Valid {
		t := row.CompletedAt.Time
		out.CompletedAt = &t
	}
	return out
}

// ----------------------------------------------------------------------
// POST /control-plane-snapshots/ — trigger
// ----------------------------------------------------------------------

// TriggerSnapshot records a snapshot row and applies a one-shot
// privileged Job that runs the distribution's etcd-snapshot command on a
// control-plane node.
//
//  1. Resolve cluster + reject managed control planes (409).
//  2. Validate/derive the snapshot name + location.
//  3. Insert the row (status=pending).
//  4. Apply the Job via the tunnel requester. On success mark running;
//     on failure mark failed and surface the error (still 202 — the row
//     is persisted so the operator sees the attempt + reason).
func (h *ControlPlaneSnapshotHandler) TriggerSnapshot(w http.ResponseWriter, r *http.Request) {
	clusterID, err := uuid.Parse(chi.URLParam(r, "cluster_id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid cluster ID")
		return
	}
	cluster, err := h.queries.GetClusterByID(r.Context(), clusterID)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster not found")
		return
	}

	family, ok := controlPlaneSnapshotDistro(cluster.Distribution)
	if !ok {
		RespondRequestError(w, r, http.StatusConflict, apierror.Conflict,
			fmt.Sprintf("Control-plane (etcd) snapshots are only supported on self-managed distributions (k3s, RKE2, kubeadm). Cluster distribution %q is a managed or unsupported control plane with no reachable etcd.", cluster.Distribution))
		return
	}

	var req struct {
		Name     string `json:"name,omitempty"`
		Location string `json:"location,omitempty"`
	}
	// Body is optional — an empty POST triggers a snapshot with defaults.
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = newControlPlaneSnapshotName(cluster.Name)
	}
	if !validVeleroResourceName(name) {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "name must be a valid RFC 1123 subdomain (1-253 chars)")
		return
	}
	location := strings.ToLower(strings.TrimSpace(req.Location))
	if location == "" {
		location = "local"
	}
	if location != "local" && location != "s3" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "location must be 'local' or 's3'")
		return
	}

	row, err := h.queries.CreateControlPlaneSnapshot(r.Context(), sqlc.CreateControlPlaneSnapshotParams{
		ClusterID:     clusterID,
		Name:          name,
		Status:        "pending",
		Location:      location,
		RequestedByID: currentUserUUID(r),
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CreateError, "Failed to create snapshot row")
		return
	}

	applyErr := h.ApplySnapshotJob(r.Context(), clusterID.String(), row.ID.String(), name, family, location)
	if applyErr != nil {
		_ = h.queries.MarkControlPlaneSnapshotFailed(r.Context(), sqlc.MarkControlPlaneSnapshotFailedParams{
			ID:    row.ID,
			Error: applyErr.Error(),
		})
		row.Status = "failed"
		row.Error = applyErr.Error()
		recordAudit(r, h.queries, "cluster.control_plane_snapshot.triggered", "control_plane_snapshot", row.ID.String(), cluster.Name, map[string]any{
			"cluster_id":   clusterID.String(),
			"distribution": family,
			"name":         name,
			"apply_error":  applyErr.Error(),
		})
		RespondJSON(w, http.StatusAccepted, controlPlaneSnapshotToResponse(row))
		return
	}

	if err := h.queries.MarkControlPlaneSnapshotStatus(r.Context(), sqlc.MarkControlPlaneSnapshotStatusParams{
		ID:     row.ID,
		Status: "running",
		Error:  "",
	}); err == nil {
		row.Status = "running"
	}

	recordAudit(r, h.queries, "cluster.control_plane_snapshot.triggered", "control_plane_snapshot", row.ID.String(), cluster.Name, map[string]any{
		"cluster_id":   clusterID.String(),
		"distribution": family,
		"name":         name,
		"location":     location,
	})
	RespondJSON(w, http.StatusAccepted, controlPlaneSnapshotToResponse(row))
}

// ----------------------------------------------------------------------
// GET /control-plane-snapshots/ — list (paged)
// ----------------------------------------------------------------------

// ListSnapshots returns the cluster's snapshot rows newest-first, paged
// via ?limit= (default 50, max 200) and ?offset=. The DB is the source
// of truth — we never re-poll the member cluster on list.
func (h *ControlPlaneSnapshotHandler) ListSnapshots(w http.ResponseWriter, r *http.Request) {
	clusterID, err := uuid.Parse(chi.URLParam(r, "cluster_id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid cluster ID")
		return
	}
	if _, err := h.queries.GetClusterByID(r.Context(), clusterID); err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster not found")
		return
	}

	limit, offset := parseLimitOffset(r, 50, 200)
	rows, err := h.queries.ListControlPlaneSnapshotsByCluster(r.Context(), sqlc.ListControlPlaneSnapshotsByClusterParams{
		ClusterID: clusterID,
		Limit:     limit,
		Offset:    offset,
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list snapshots")
		return
	}
	total, err := h.queries.CountControlPlaneSnapshotsByCluster(r.Context(), clusterID)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to count snapshots")
		return
	}
	out := make([]ControlPlaneSnapshotResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, controlPlaneSnapshotToResponse(row))
	}
	RespondJSON(w, http.StatusOK, map[string]any{
		"items":  out,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
}

// GetSnapshot returns a single snapshot row scoped to the cluster.
func (h *ControlPlaneSnapshotHandler) GetSnapshot(w http.ResponseWriter, r *http.Request) {
	clusterID, snapshotID, ok := parseControlPlaneSnapshotIDs(w, r)
	if !ok {
		return
	}
	row, err := h.queries.GetControlPlaneSnapshotByID(r.Context(), snapshotID)
	if err != nil || row.ClusterID != clusterID {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Snapshot not found")
		return
	}
	RespondJSON(w, http.StatusOK, controlPlaneSnapshotToResponse(row))
}

// ----------------------------------------------------------------------
// POST /control-plane-snapshots/{id}/restore/ — RUNBOOK (no automation)
// ----------------------------------------------------------------------

// ControlPlaneRestoreRunbook is the guidance payload the restore endpoint
// returns. There is deliberately no automation: restoring etcd is an
// offline node operation that a running cluster cannot safely drive.
type ControlPlaneRestoreRunbook struct {
	Automated    bool     `json:"automated"`
	Distribution string   `json:"distribution"`
	SnapshotName string   `json:"snapshot_name"`
	Summary      string   `json:"summary"`
	Steps        []string `json:"steps"`
	Warning      string   `json:"warning"`
	DocsURL      string   `json:"docs_url"`
}

// Restore returns a distribution-aware runbook rather than performing a
// restore. Responds 200 with automated=false so clients render the
// guidance instead of polling for a restore that will never exist.
func (h *ControlPlaneSnapshotHandler) Restore(w http.ResponseWriter, r *http.Request) {
	clusterID, snapshotID, ok := parseControlPlaneSnapshotIDs(w, r)
	if !ok {
		return
	}
	row, err := h.queries.GetControlPlaneSnapshotByID(r.Context(), snapshotID)
	if err != nil || row.ClusterID != clusterID {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Snapshot not found")
		return
	}
	cluster, err := h.queries.GetClusterByID(r.Context(), clusterID)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster not found")
		return
	}
	family, _ := controlPlaneSnapshotDistro(cluster.Distribution)
	RespondJSON(w, http.StatusOK, controlPlaneRestoreRunbook(family, row.Name))
}

// controlPlaneRestoreRunbook renders the offline-restore guidance for the
// given distribution family. Unknown families fall back to a generic
// etcd runbook.
func controlPlaneRestoreRunbook(family, snapshotName string) ControlPlaneRestoreRunbook {
	rb := ControlPlaneRestoreRunbook{
		Automated:    false,
		Distribution: family,
		SnapshotName: snapshotName,
		Warning:      "Restoring etcd is destructive and OFFLINE: it rewinds the entire cluster state to the snapshot. Perform it on the control-plane node(s) directly, not through this API. Take a fresh snapshot first.",
	}
	switch family {
	case "k3s":
		rb.Summary = "Restore this k3s control plane from the etcd snapshot on the server node."
		rb.Steps = []string{
			"SSH to the k3s server (control-plane) node.",
			"Stop the k3s service: `systemctl stop k3s`.",
			fmt.Sprintf("Restore from the snapshot: `k3s server --cluster-reset --cluster-reset-restore-path=/var/lib/rancher/k3s/server/db/snapshots/%s`.", snapshotName),
			"Wait for the reset to complete, then start k3s: `systemctl start k3s`.",
			"On any additional server nodes, delete /var/lib/rancher/k3s/server/db and rejoin them to the reset cluster.",
		}
		rb.DocsURL = "https://docs.k3s.io/datastore/backup-restore"
	case "rke2":
		rb.Summary = "Restore this RKE2 control plane from the etcd snapshot on the server node."
		rb.Steps = []string{
			"SSH to the RKE2 server (control-plane) node.",
			"Stop the RKE2 service: `systemctl stop rke2-server`.",
			fmt.Sprintf("Restore from the snapshot: `rke2 server --cluster-reset --cluster-reset-restore-path=/var/lib/rancher/rke2/server/db/snapshots/%s`.", snapshotName),
			"Start RKE2: `systemctl start rke2-server`.",
			"On additional server nodes, clear /var/lib/rancher/rke2/server/db and rejoin them.",
		}
		rb.DocsURL = "https://docs.rke2.io/backup_restore"
	case "kubeadm":
		rb.Summary = "Restore this kubeadm control plane from the etcd snapshot on each control-plane node."
		rb.Steps = []string{
			"SSH to a control-plane node and stop the kubelet + static pods (move /etc/kubernetes/manifests aside).",
			fmt.Sprintf("Restore the snapshot into a fresh data dir: `ETCDCTL_API=3 etcdctl snapshot restore /var/lib/etcd-snapshots/%s.db --data-dir=/var/lib/etcd-restore`.", snapshotName),
			"Point the etcd static pod at the restored data dir (update the hostPath in etcd.yaml) and restore the manifests directory.",
			"Repeat on every control-plane node using the SAME snapshot, then bring the kubelets back up.",
		}
		rb.DocsURL = "https://kubernetes.io/docs/tasks/administer-cluster/configure-upgrade-etcd/#restoring-an-etcd-cluster"
	default:
		rb.Summary = "Restore the control plane's etcd datastore from the snapshot (offline, on the control-plane node)."
		rb.Steps = []string{
			"Stop the control-plane / etcd process on the node.",
			"Restore the etcd snapshot into a fresh data directory with `etcdctl snapshot restore`.",
			"Repoint etcd at the restored data directory and restart the control plane.",
		}
		rb.DocsURL = "https://kubernetes.io/docs/tasks/administer-cluster/configure-upgrade-etcd/"
	}
	return rb
}

// ----------------------------------------------------------------------
// Job apply (shared by the handler trigger + the worker sweep callback)
// ----------------------------------------------------------------------

// ApplySnapshotJob renders and POSTs the one-shot privileged snapshot Job
// to the member cluster's apiserver via the tunnel requester. Pure
// transport: it does NOT mutate DB rows (callers own status). Exported so
// the worker sweep can invoke it through a wired callback (avoiding a
// tasks → handler import cycle).
func (h *ControlPlaneSnapshotHandler) ApplySnapshotJob(ctx context.Context, clusterID, snapshotID, name, family, location string) error {
	if h == nil || h.requester == nil {
		return fmt.Errorf("tunnel requester not configured")
	}
	body, err := json.Marshal(renderControlPlaneSnapshotJob(controlPlaneSnapshotJobRender{
		SnapshotID: snapshotID,
		Name:       name,
		Family:     family,
		Location:   location,
		Namespace:  controlPlaneSnapshotJobNamespace,
	}))
	if err != nil {
		return fmt.Errorf("marshal snapshot job: %w", err)
	}
	path := fmt.Sprintf("/apis/batch/v1/namespaces/%s/jobs", controlPlaneSnapshotJobNamespace)
	resp, err := h.requester.Do(ctx, clusterID, http.MethodPost, path, body, requestHeaders("application/json"))
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusConflict {
		// A Job with this name already exists (retry of the same
		// snapshot) — treat as applied.
		return nil
	}
	return ensureSuccess(resp)
}

// ReadSnapshotJobStatus GETs the snapshot Job through the tunnel and maps its
// batch/v1 Job conditions to a coarse phase so the worker sweep can move a
// row off "running": "succeeded" (Complete), "failed" (Failed, detail carries
// the reason), "running" (still going), or "gone" (Job not found — its TTL
// expired before we polled). Exported so the worker reaches it via a wired
// callback (avoiding a tasks → handler import cycle). Pure transport: it does
// NOT mutate DB rows.
//
// ponytail: no snapshot-size capture — Complete is truthful enough for a DR
// registry; size would need pod-log scraping, add if operators ask.
func (h *ControlPlaneSnapshotHandler) ReadSnapshotJobStatus(ctx context.Context, clusterID, snapshotID string) (phase, detail string, err error) {
	if h == nil || h.requester == nil {
		return "", "", fmt.Errorf("tunnel requester not configured")
	}
	jobName := controlPlaneSnapshotJobName(snapshotID)
	path := fmt.Sprintf("/apis/batch/v1/namespaces/%s/jobs/%s", controlPlaneSnapshotJobNamespace, jobName)
	resp, err := h.requester.Do(ctx, clusterID, http.MethodGet, path, nil, requestHeaders(""))
	if err != nil {
		return "", "", err
	}
	if resp.StatusCode == http.StatusNotFound {
		return "gone", "", nil
	}
	if resp.StatusCode >= 400 {
		return "", "", ensureSuccess(resp)
	}
	var job struct {
		Status struct {
			Conditions []struct {
				Type    string `json:"type"`
				Status  string `json:"status"`
				Reason  string `json:"reason"`
				Message string `json:"message"`
			} `json:"conditions"`
		} `json:"status"`
	}
	if err := parseJSONResponse(resp, &job); err != nil {
		return "", "", err
	}
	for _, c := range job.Status.Conditions {
		if c.Status != "True" {
			continue
		}
		switch c.Type {
		case "Complete":
			return "succeeded", "", nil
		case "Failed":
			d := strings.TrimSpace(c.Reason + " " + c.Message)
			if d == "" {
				d = "snapshot Job failed"
			}
			return "failed", d, nil
		}
	}
	return "running", "", nil
}

// controlPlaneSnapshotJobRender bundles the inputs for the snapshot Job.
type controlPlaneSnapshotJobRender struct {
	SnapshotID string
	Name       string
	Family     string
	Location   string
	Namespace  string
}

// renderControlPlaneSnapshotJob builds the unstructured batch/v1 Job that
// runs the distribution's etcd-snapshot command on a control-plane node.
//
// The Job is pinned to a control-plane node (nodeSelector +
// control-plane/master tolerations), runs a single privileged container
// with hostPID, and executes the snapshot command in the HOST namespaces
// via `nsenter --target 1` so it uses the host's own k3s / rke2 / etcdctl
// binaries and etcd data. restartPolicy=Never with a small backoffLimit;
// ttlSecondsAfterFinished garbage-collects the Job object after it ends.
func renderControlPlaneSnapshotJob(in controlPlaneSnapshotJobRender) map[string]any {
	jobName := controlPlaneSnapshotJobName(in.SnapshotID)
	hostCmd := snapshotHostCommand(in.Family, in.Name, in.Location)

	container := map[string]any{
		"name":    "etcd-snapshot",
		"image":   controlPlaneSnapshotJobImage,
		"command": []any{"nsenter", "--target", "1", "--mount", "--uts", "--ipc", "--net", "--pid", "--", "sh", "-c", hostCmd},
		"securityContext": map[string]any{
			"privileged": true,
			"runAsUser":  0,
		},
	}

	podSpec := map[string]any{
		"hostPID":       true,
		"restartPolicy": "Never",
		"nodeSelector": map[string]any{
			"node-role.kubernetes.io/control-plane": "true",
		},
		"tolerations": []any{
			map[string]any{"key": "node-role.kubernetes.io/control-plane", "operator": "Exists", "effect": "NoSchedule"},
			map[string]any{"key": "node-role.kubernetes.io/master", "operator": "Exists", "effect": "NoSchedule"},
			map[string]any{"key": "CriticalAddonsOnly", "operator": "Exists"},
		},
		"containers": []any{container},
	}

	return map[string]any{
		"apiVersion": "batch/v1",
		"kind":       "Job",
		"metadata": map[string]any{
			"name":      jobName,
			"namespace": in.Namespace,
			"labels": map[string]any{
				"app.kubernetes.io/managed-by":         "astronomer-go",
				"astronomer.io/control-plane-snapshot": in.SnapshotID,
			},
		},
		"spec": map[string]any{
			"backoffLimit":            2,
			"ttlSecondsAfterFinished": 3600,
			"template": map[string]any{
				"metadata": map[string]any{
					"labels": map[string]any{
						"app.kubernetes.io/managed-by":         "astronomer-go",
						"astronomer.io/control-plane-snapshot": in.SnapshotID,
					},
				},
				"spec": podSpec,
			},
		},
	}
}

// snapshotHostCommand returns the shell command run in the host
// namespaces for the given distribution family. location is recorded for
// operator intent; the actual object-store upload for k3s/rke2 is
// governed by the server's own --etcd-s3 configuration, so we always run
// the native save (which respects that config) rather than passing
// unconfigured s3 flags.
func snapshotHostCommand(family, name, location string) string {
	switch family {
	case "k3s":
		return fmt.Sprintf("k3s etcd-snapshot save --name %s", shellQuote(name))
	case "rke2":
		return fmt.Sprintf("rke2 etcd-snapshot save --name %s", shellQuote(name))
	case "kubeadm":
		return fmt.Sprintf("mkdir -p /var/lib/etcd-snapshots && ETCDCTL_API=3 etcdctl "+
			"--endpoints=https://127.0.0.1:2379 "+
			"--cacert=/etc/kubernetes/pki/etcd/ca.crt "+
			"--cert=/etc/kubernetes/pki/etcd/server.crt "+
			"--key=/etc/kubernetes/pki/etcd/server.key "+
			"snapshot save /var/lib/etcd-snapshots/%s.db", shellQuote(name))
	default:
		// Should be unreachable — callers gate on controlPlaneSnapshotDistro.
		return "echo 'unsupported distribution for control-plane snapshot' >&2; exit 1"
	}
}

// shellQuote single-quotes a value for safe embedding in an `sh -c`
// string. Names are already RFC-1123-validated (no quotes) at the
// handler edge; this is defense-in-depth for the worker-scheduled path.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// ----------------------------------------------------------------------
// Naming + param helpers
// ----------------------------------------------------------------------

// newControlPlaneSnapshotName builds a default snapshot name:
// "<cluster>-cp-<timestamp>". Reuses sanitizeForName/truncateName from
// cluster_snapshots.go for RFC-1123 safety.
func newControlPlaneSnapshotName(cluster string) string {
	stamp := time.Now().UTC().Format("20060102t150405")
	return truncateName(sanitizeForName(cluster)+"-cp-"+stamp, 253)
}

// controlPlaneSnapshotJobName derives a DNS-1123-label Job name (≤63) from
// the snapshot ID so re-triggers of the same snapshot collide (409 →
// treated as applied) rather than spawning duplicate Jobs.
func controlPlaneSnapshotJobName(snapshotID string) string {
	id := strings.ToLower(strings.ReplaceAll(snapshotID, "-", ""))
	if len(id) > 12 {
		id = id[:12]
	}
	if id == "" {
		id = "unknown"
	}
	return "cp-snapshot-" + id
}

func parseControlPlaneSnapshotIDs(w http.ResponseWriter, r *http.Request) (uuid.UUID, uuid.UUID, bool) {
	clusterID, err := uuid.Parse(chi.URLParam(r, "cluster_id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid cluster ID")
		return uuid.Nil, uuid.Nil, false
	}
	snapshotID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid snapshot ID")
		return uuid.Nil, uuid.Nil, false
	}
	return clusterID, snapshotID, true
}

// parseLimitOffset reads ?limit=/?offset= with a default + hard cap on
// limit and a floor of 0 on offset.
func parseLimitOffset(r *http.Request, def, max int32) (int32, int32) {
	limit := def
	if v := strings.TrimSpace(r.URL.Query().Get("limit")); v != "" {
		if n, err := parseInt32(v); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > max {
		limit = max
	}
	var offset int32
	if v := strings.TrimSpace(r.URL.Query().Get("offset")); v != "" {
		if n, err := parseInt32(v); err == nil && n > 0 {
			offset = n
		}
	}
	return limit, offset
}

func parseInt32(s string) (int32, error) {
	var n int32
	_, err := fmt.Sscan(s, &n)
	return n, err
}
