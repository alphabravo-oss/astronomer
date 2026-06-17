// Package handler — management-plane log tail endpoint.
//
// Companion to the chart-side Fluent Bit DaemonSet
// (deploy/chart/templates/management-logging-*.yaml). The DaemonSet is
// the durable long-term path that ships every server/worker log line to
// the operator's external sink (Loki / Elasticsearch / Splunk / HTTP);
// this endpoint is the "show me what's happening right now in the
// dashboard" complement that reads the in-cluster Pod logs directly via
// the kubernetes API.
//
// Wired into routes.go as:
//
//	GET /api/v1/admin/management-logs/?component=server&since=<RFC3339>
//
// Superuser-gated inside the handler (same pattern as admin_drill.go).
// Caps a single request at 1000 lines / 1 MiB payload to keep the
// dashboard responsive on chatty pods.
package handler

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
)

// ManagementLogsQuerier is the slice of sqlc.Queries the handler reads
// to verify the caller is a superuser. Narrow on purpose so tests can
// satisfy it with a tiny fake. CreateAuditLogV1 is satisfied separately
// via the recordAudit helper (which inspects the value via type
// assertion).
type ManagementLogsQuerier interface {
	GetUserByID(ctx context.Context, id uuid.UUID) (sqlc.User, error)
}

// Handler-side caps. The chart's managementLogging.handler.{maxLines,
// maxBytes} are operator-overridable via the wrapper constructor below;
// these constants are the safety net even when the chart is misset.
const (
	defaultMaxLines = 1000
	defaultMaxBytes = 1 << 20 // 1 MiB
	// Cap how long we'll spend pulling logs from kubelet for a single
	// request. The dashboard polls this endpoint and we don't want a
	// slow read to wedge the chi handler pool.
	logFetchTimeout = 10 * time.Second
)

// ManagementLogsHandler wraps GET /api/v1/admin/management-logs/.
//
// k8s and namespace are optional. When either is unset the handler
// returns 503 service-unavailable — the same shape AdminDrillHandler
// uses when its store is unwired. That keeps boot-time wiring
// permissive (no panics in test fakes / laptop dev where
// InClusterConfig fails).
type ManagementLogsHandler struct {
	queries   ManagementLogsQuerier
	k8s       kubernetes.Interface
	namespace string
	// releaseName is the helm release name, used to filter pods to the
	// management-plane components only (server / worker / agent). When
	// empty we fall back to label-based selection.
	releaseName string

	maxLines int
	maxBytes int
}

// NewManagementLogsHandler returns a usable handler. queries may be
// nil for degenerate test fakes; the handler then renders 503 on the
// gate. k8sClient + namespace are optional — when unset the read path
// renders 503 with a clear "logs unavailable" code so the operator UI
// can degrade gracefully.
func NewManagementLogsHandler(queries ManagementLogsQuerier, k8sClient kubernetes.Interface, namespace, releaseName string) *ManagementLogsHandler {
	return &ManagementLogsHandler{
		queries:     queries,
		k8s:         k8sClient,
		namespace:   namespace,
		releaseName: releaseName,
		maxLines:    defaultMaxLines,
		maxBytes:    defaultMaxBytes,
	}
}

// SetCaps overrides the per-request line / byte caps. Mirrors the
// chart's managementLogging.handler.{maxLines,maxBytes}. Values <= 0
// fall back to the package defaults.
func (h *ManagementLogsHandler) SetCaps(maxLines, maxBytes int) {
	if h == nil {
		return
	}
	if maxLines > 0 {
		h.maxLines = maxLines
	}
	if maxBytes > 0 {
		h.maxBytes = maxBytes
	}
}

// ManagementLogLine is the wire shape for a single line. Timestamp is
// parsed off the kubelet `timestamps=true` prefix when present;
// otherwise it's "" and the dashboard falls back to the response time.
type ManagementLogLine struct {
	Timestamp string `json:"timestamp"`
	Pod       string `json:"pod"`
	Container string `json:"container"`
	Message   string `json:"message"`
}

// ManagementLogsResponse is the top-level payload. `truncated` flips
// when the handler hit either maxLines or maxBytes and stopped reading.
type ManagementLogsResponse struct {
	Component string              `json:"component"`
	Since     string              `json:"since,omitempty"`
	Lines     []ManagementLogLine `json:"lines"`
	Truncated bool                `json:"truncated"`
}

// allowedComponents is the closed set of components the endpoint will
// tail. Adding a new entry here requires updating the chart's
// managementLogging.includeFoo flag too.
var allowedComponents = map[string]bool{
	"server": true,
	"worker": true,
	"agent":  true,
}

// Tail handles GET /api/v1/admin/management-logs/.
//
// Query parameters:
//
//	component  required — one of: server | worker | agent
//	since      optional — RFC3339 timestamp; lines older than this are
//	           dropped. When unset the handler returns the last N lines
//	           (bounded by maxLines).
//	tail_lines optional — int; overrides maxLines downward (cannot exceed
//	           the configured cap).
func (h *ManagementLogsHandler) Tail(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	if h.k8s == nil || h.namespace == "" {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.LogsUnavailable,
			"management log tail is unavailable: the server has no in-cluster Kubernetes client")

		return
	}

	component := strings.TrimSpace(r.URL.Query().Get("component"))
	if component == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ComponentRequired,
			"the component query parameter is required (server | worker | agent)")

		return
	}
	if !allowedComponents[component] {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ComponentInvalid,
			fmt.Sprintf("component must be one of: server, worker, agent (got %q)", component))

		return
	}

	var sinceTime *time.Time
	if s := strings.TrimSpace(r.URL.Query().Get("since")); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			// Try RFC3339 with nano precision before giving up.
			if t2, err2 := time.Parse(time.RFC3339Nano, s); err2 == nil {
				t = t2
			} else {
				RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidSince,
					"the since query parameter must be an RFC3339 timestamp")

				return
			}
		}
		sinceTime = &t
	}

	tailLines := h.maxLines
	if t := strings.TrimSpace(r.URL.Query().Get("tail_lines")); t != "" {
		if v, err := strconv.Atoi(t); err == nil && v > 0 {
			if v > h.maxLines {
				v = h.maxLines
			}
			tailLines = v
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), logFetchTimeout)
	defer cancel()

	pods, err := h.listComponentPods(ctx, component)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadGateway, apierror.K8sError,
			fmt.Sprintf("failed to list pods for component %q: %v", component, err))

		return
	}
	if len(pods) == 0 {
		// Cleanly return an empty result instead of a 404 — the UI can
		// distinguish "no logs yet" from "wrong component" via the
		// allowedComponents check above.
		RespondJSON(w, http.StatusOK, ManagementLogsResponse{
			Component: component,
			Lines:     []ManagementLogLine{},
		})
		return
	}

	resp := ManagementLogsResponse{Component: component}
	if sinceTime != nil {
		resp.Since = sinceTime.UTC().Format(time.RFC3339)
	}

	bytesRemaining := h.maxBytes
	linesRemaining := tailLines
	for _, pod := range pods {
		if linesRemaining <= 0 || bytesRemaining <= 0 {
			resp.Truncated = true
			break
		}
		for _, c := range pod.Spec.Containers {
			if linesRemaining <= 0 || bytesRemaining <= 0 {
				resp.Truncated = true
				break
			}
			perPodLimit := int64(linesRemaining)
			opts := &corev1.PodLogOptions{
				Container:  c.Name,
				Timestamps: true,
				TailLines:  &perPodLimit,
			}
			if sinceTime != nil {
				st := metav1.NewTime(*sinceTime)
				opts.SinceTime = &st
				// kubelet refuses both TailLines and SinceTime together
				// on some versions; prefer SinceTime when the caller
				// explicitly asked for a window.
				opts.TailLines = nil
			}
			req := h.k8s.CoreV1().Pods(h.namespace).GetLogs(pod.Name, opts)
			stream, err := req.Stream(ctx)
			if err != nil {
				// One bad container shouldn't fail the whole request —
				// log via an in-band marker line so the UI can show the
				// gap (the alternative is a 502 that hides real lines
				// from the OTHER pods in the component).
				resp.Lines = append(resp.Lines, ManagementLogLine{
					Pod:       pod.Name,
					Container: c.Name,
					Message:   fmt.Sprintf("[astronomer] failed to read logs: %v", err),
				})
				continue
			}
			added, truncated := scanLogStream(stream, pod.Name, c.Name, &resp.Lines, &linesRemaining, &bytesRemaining)
			_ = stream.Close()
			_ = added
			if truncated {
				resp.Truncated = true
				break
			}
		}
	}

	// Sort newest-last (chronological) by timestamp so the UI can render
	// straight into a scrollback widget. Lines without a parseable
	// timestamp sort before timestamped ones — this only matters at the
	// very start when kubelet hasn't backfilled the stream prefix yet.
	sort.SliceStable(resp.Lines, func(i, j int) bool {
		return resp.Lines[i].Timestamp < resp.Lines[j].Timestamp
	})

	RespondJSON(w, http.StatusOK, resp)
}

// scanLogStream pulls lines off the kubelet stream, splits the
// kubelet `timestamps=true` prefix into the dedicated Timestamp field,
// and appends to out until linesRemaining or bytesRemaining hits zero.
// Returns the number of lines appended and a `truncated` flag.
func scanLogStream(r io.Reader, pod, container string, out *[]ManagementLogLine, linesRemaining, bytesRemaining *int) (int, bool) {
	added := 0
	scanner := bufio.NewScanner(r)
	// Allow long lines up to 1 MiB — Go's default is 64 KiB which is
	// way too small for json-formatted log lines with embedded stack
	// traces.
	scanner.Buffer(make([]byte, 64*1024), 1<<20)
	for scanner.Scan() {
		if *linesRemaining <= 0 || *bytesRemaining <= 0 {
			return added, true
		}
		raw := scanner.Text()
		ts := ""
		msg := raw
		// kubelet emits "<RFC3339Nano> <message>" when timestamps=true.
		if sp := strings.IndexByte(raw, ' '); sp > 0 {
			if _, err := time.Parse(time.RFC3339Nano, raw[:sp]); err == nil {
				ts = raw[:sp]
				msg = raw[sp+1:]
			}
		}
		cost := len(raw) + 1
		if cost > *bytesRemaining {
			return added, true
		}
		*out = append(*out, ManagementLogLine{
			Timestamp: ts,
			Pod:       pod,
			Container: container,
			Message:   msg,
		})
		*linesRemaining--
		*bytesRemaining -= cost
		added++
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		*out = append(*out, ManagementLogLine{
			Pod:       pod,
			Container: container,
			Message:   fmt.Sprintf("[astronomer] log stream truncated: %v", err),
		})
	}
	return added, false
}

// listComponentPods lists pods in the release namespace whose
// app.kubernetes.io/component label matches the requested component.
// Fall back to a name-prefix filter when the label selector returns
// nothing (older releases that didn't carry the canonical labels).
func (h *ManagementLogsHandler) listComponentPods(ctx context.Context, component string) ([]corev1.Pod, error) {
	selector := fmt.Sprintf("app.kubernetes.io/component=%s,app.kubernetes.io/part-of=astronomer", component)
	pods, err := h.k8s.CoreV1().Pods(h.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: selector,
	})
	if err != nil {
		return nil, err
	}
	if len(pods.Items) > 0 {
		return pods.Items, nil
	}
	// Fall back to a name-prefix scan. Useful in legacy installs that
	// missed the labels migration AND in tests where the fake client
	// doesn't index labels.
	all, err := h.k8s.CoreV1().Pods(h.namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	prefix := ""
	if h.releaseName != "" {
		prefix = fmt.Sprintf("%s-%s", h.releaseName, component)
	}
	out := make([]corev1.Pod, 0, len(all.Items))
	for _, p := range all.Items {
		if prefix != "" && strings.HasPrefix(p.Name, prefix) {
			out = append(out, p)
			continue
		}
		// Last-resort: the pod's name contains the component name and
		// the namespace is the release namespace. This is broad
		// enough to catch hand-rolled installs.
		if strings.Contains(p.Name, "-"+component) || strings.HasPrefix(p.Name, component+"-") {
			out = append(out, p)
		}
	}
	return out, nil
}

// gate enforces superuser-only access and emits the audit row. Mirrors
// the pattern in admin_drill.go / admin_queues.go.
func (h *ManagementLogsHandler) gate(w http.ResponseWriter, r *http.Request) bool {
	if _, ok := requireSuperuser(w, r, h.queries, superuserGateConfig{
		StoreUnavailableMessage: "Admin store not configured",
		ForbiddenMessage:        "Management log tail requires superuser privileges",
	}); !ok {
		return false
	}
	recordAudit(r, h.queries, "admin.management_logs.viewed", "platform", "", "management-logs", map[string]any{
		"path":      r.URL.Path,
		"component": r.URL.Query().Get("component"),
	})
	return true
}
