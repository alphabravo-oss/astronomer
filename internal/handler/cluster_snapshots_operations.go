package handler

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
	"github.com/hibiken/asynq"
	"github.com/robfig/cron/v3"
)

func (h *ClusterSnapshotsHandler) VeleroStatus(w http.ResponseWriter, r *http.Request) {
	clusterID, ok := parseClusterID(w, r)
	if !ok {
		return
	}
	if _, err := h.queries.GetClusterByID(r.Context(), clusterID); err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster not found")
		return
	}
	if h.requester == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.TunnelUnwired, "Tunnel requester not configured")
		return
	}

	bsls, err := listVeleroBSLs(r.Context(), h.requester, clusterID.String(), defaultVeleroNamespace)
	if err != nil {
		// Hard failure (cluster unreachable). Surface as 200 with
		// installed=false + reason — the dashboard caller treats this
		// as "show install prompt". A 5xx here would imply server-side
		// bug, but the actual cause is a member-cluster availability
		// problem.
		out := VeleroStatusResponse{
			Installed: false,
			Namespace: defaultVeleroNamespace,
			Reason:    err.Error(),
		}
		veleroInstallStatus.WithLabelValues(observability.MetricValues(clusterID.String(), "unreachable")...).Set(0)
		RespondJSON(w, http.StatusOK, out)
		return
	}
	if len(bsls) == 0 {
		out := VeleroStatusResponse{
			Installed: false,
			Namespace: defaultVeleroNamespace,
			Reason:    "no BackupStorageLocation CRDs found",
		}
		veleroInstallStatus.WithLabelValues(observability.MetricValues(clusterID.String(), "missing")...).Set(0)
		RespondJSON(w, http.StatusOK, out)
		return
	}

	summaries := make([]VeleroBSLSummary, 0, len(bsls))
	anyReady := false
	for _, item := range bsls {
		s := summarizeBSL(item)
		summaries = append(summaries, s)
		if strings.EqualFold(s.Phase, "Available") {
			anyReady = true
		}
	}
	out := VeleroStatusResponse{
		Installed:        true,
		Namespace:        defaultVeleroNamespace,
		StorageReady:     anyReady,
		StorageLocations: summaries,
	}
	if anyReady {
		veleroInstallStatus.WithLabelValues(observability.MetricValues(clusterID.String(), "ready")...).Set(1)
	} else {
		veleroInstallStatus.WithLabelValues(observability.MetricValues(clusterID.String(), "unavailable")...).Set(0)
	}
	RespondJSON(w, http.StatusOK, out)
}

func summarizeBSL(item map[string]any) VeleroBSLSummary {
	out := VeleroBSLSummary{}
	if meta, ok := item["metadata"].(map[string]any); ok {
		if n, ok := meta["name"].(string); ok {
			out.Name = n
		}
	}
	if spec, ok := item["spec"].(map[string]any); ok {
		if p, ok := spec["provider"].(string); ok {
			out.Provider = p
		}
		if d, ok := spec["default"].(bool); ok {
			out.Default = d
		}
		if os, ok := spec["objectStorage"].(map[string]any); ok {
			if b, ok := os["bucket"].(string); ok {
				out.Bucket = b
			}
		}
	}
	if status, ok := item["status"].(map[string]any); ok {
		if p, ok := status["phase"].(string); ok {
			out.Phase = p
		}
	}
	return out
}

// resolveBSLStore finds the object store a snapshot reads from among a
// cluster's BackupStorageLocations. `name` is the snapshot spec's
// storageLocation; an empty name means Velero's default BSL (the one flagged
// spec.default, or — matching Velero's own fallback — the sole BSL, or one
// literally named "default"). Returns the resolved summary and true only when
// a store could be identified.
func resolveBSLStore(bsls []map[string]any, name string) (VeleroBSLSummary, bool) {
	name = strings.TrimSpace(name)
	if name != "" {
		for _, item := range bsls {
			if s := summarizeBSL(item); s.Name == name {
				return s, true
			}
		}
		return VeleroBSLSummary{}, false
	}
	for _, item := range bsls {
		if s := summarizeBSL(item); s.Default {
			return s, true
		}
	}
	if len(bsls) == 1 {
		return summarizeBSL(bsls[0]), true
	}
	for _, item := range bsls {
		if s := summarizeBSL(item); s.Name == "default" {
			return s, true
		}
	}
	return VeleroBSLSummary{}, false
}

// targetHasMatchingStore reports whether any of the target cluster's BSLs
// points at the same object store as `src` (same bucket; provider must agree
// when both sides report one). Matching is case-insensitive.
func targetHasMatchingStore(targetBSLs []map[string]any, src VeleroBSLSummary) bool {
	srcBucket := strings.TrimSpace(src.Bucket)
	for _, item := range targetBSLs {
		t := summarizeBSL(item)
		if !strings.EqualFold(strings.TrimSpace(t.Bucket), srcBucket) {
			continue
		}
		if src.Provider != "" && t.Provider != "" && !strings.EqualFold(t.Provider, src.Provider) {
			continue
		}
		return true
	}
	return false
}

// ----------------------------------------------------------------------
// Naming + validation helpers
// ----------------------------------------------------------------------

// newVeleroBackupName produces a deterministic-ish name: "<cluster>-<ts>-<rand>".
// Velero names must be ≤253 chars and RFC 1123 subdomains; the cluster
// name is RFC-1123 too so concatenation stays safe.
func newVeleroBackupName(cluster string) string {
	suffix, _ := randomHex(4)
	stamp := time.Now().UTC().Format("20060102t150405")
	cluster = sanitizeForName(cluster)
	name := cluster + "-" + stamp + "-" + suffix
	return truncateName(name, 253)
}

func newVeleroRestoreName(backup string) string {
	suffix, _ := randomHex(3)
	stamp := time.Now().UTC().Format("20060102t150405")
	backup = sanitizeForName(backup)
	name := backup + "-restore-" + stamp + "-" + suffix
	return truncateName(name, 253)
}

func sanitizeForName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return "snapshot"
	}
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
			out = append(out, c)
		case c == '-':
			out = append(out, c)
		default:
			out = append(out, '-')
		}
	}
	// trim leading / trailing dashes — RFC-1123 wants alphanumeric ends.
	return strings.Trim(string(out), "-")
}

func truncateName(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return strings.TrimRight(s[:max], "-")
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// validVeleroResourceName enforces RFC-1123 subdomain rules at the
// handler edge: 1–253 chars, lowercase alphanumeric or '-'/'.', start
// + end alphanumeric. Velero (via the K8s api server) rejects anything
// else; surfacing the 400 here keeps the error message friendly.
func validVeleroResourceName(s string) bool {
	if s == "" || len(s) > 253 {
		return false
	}
	for i, c := range s {
		isAlnum := (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')
		isHyphen := c == '-' || c == '.'
		if !isAlnum && !isHyphen {
			return false
		}
		if (i == 0 || i == len(s)-1) && !isAlnum {
			return false
		}
	}
	return true
}

// parseSnapshotTTLDuration parses a Velero-style TTL string ("168h", "30m"). Returns
// (0, false) when the input is empty or unparseable so the caller can
// fall through to leaving expires_at NULL.
func parseSnapshotTTLDuration(s string) (time.Duration, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, false
	}
	return d, true
}

// parseCronExpression validates a standard 5-field cron schedule using
// robfig/cron's "Standard" parser (the same parser asynq.Scheduler
// uses for @every-style specs). Returns the parsed Schedule so the
// dispatcher worker can call .Next() against it.
func parseCronExpression(expr string) (cron.Schedule, error) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return nil, fmt.Errorf("cron schedule is required")
	}
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	return parser.Parse(expr)
}

// ----------------------------------------------------------------------
// Bridge — exposes a no-op enqueueing surface for worker callback wiring.
// ----------------------------------------------------------------------

// SetTaskQueue is provided for symmetry with the other handlers — the
// snapshot handler currently doesn't enqueue tasks directly (the poller
// runs on its own cadence), but the setter is here so future synchronous
// triggers can wire in without an API break.
func (h *ClusterSnapshotsHandler) SetTaskQueue(_ interface {
	Enqueue(task *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error)
}) {
}
