// Sprint 081 — image scan PROGRESS indicator.
//
// The history + diff cards (image_vulns_history.go) answer "what
// happened in the past". This file answers "what's happening RIGHT
// NOW" — is trivy-operator actively scanning, how many of the
// cluster's pods have been scanned, when was the last scan.
//
// Single endpoint:
//
//   GET /api/v1/clusters/{id}/vulnerabilities/progress/
//
// Returns:
//   {
//     "scanning":             true|false,
//     "active_jobs":          int,        // trivy scan Jobs in flight
//     "completed_jobs":       int,        // Jobs in Complete
//     "failed_jobs":          int,        // Jobs in Failed
//     "reports_count":        int,        // image_vulnerability_reports rows
//     "trivy_operator_ready": bool,
//     "last_scan_age_seconds": int|null,
//   }
//
// Implementation: queries the trivy-system namespace via the agent
// tunnel (the same k8s passthrough the rest of the dashboard uses).
// Trivy-operator's per-resource scans show up as Jobs named
// `scan-vulnerabilityreport-<hash>`; the namespace is fixed at
// `trivy-system` because the platform baseline installs it there.
//
// Cheap by design: one k8s LIST + one DB aggregate, no joins. The
// React side polls fast while scanning, slow otherwise.

package handler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

type progressJobsList struct {
	Items []progressJobItem `json:"items"`
}

type progressJobItem struct {
	Metadata struct {
		Name              string `json:"name"`
		CreationTimestamp string `json:"creationTimestamp"`
	} `json:"metadata"`
	Status struct {
		Active     int32 `json:"active"`
		Succeeded  int32 `json:"succeeded"`
		Failed     int32 `json:"failed"`
		Conditions []struct {
			Type   string `json:"type"`
			Status string `json:"status"`
		} `json:"conditions"`
	} `json:"status"`
}

type progressDeploymentList struct {
	Items []struct {
		Status struct {
			AvailableReplicas int32 `json:"availableReplicas"`
			ReadyReplicas     int32 `json:"readyReplicas"`
		} `json:"status"`
	} `json:"items"`
}

// ClusterProgress handles GET /api/v1/clusters/{id}/vulnerabilities/progress/.
func (h *ImageVulnHandler) ClusterProgress(w http.ResponseWriter, r *http.Request) {
	clusterID, ok := parseClusterID(w, r)
	if !ok {
		return
	}

	resp := map[string]any{
		"scanning":              false,
		"active_jobs":           0,
		"completed_jobs":        0,
		"failed_jobs":           0,
		"reports_count":         int64(0),
		"trivy_operator_ready":  false,
		"last_scan_age_seconds": nil,
	}

	// DB-side aggregate first — always works, even if the tunnel is
	// briefly down. Gives "reports_count" + "last_scan_age_seconds"
	// straight away.
	if agg, err := h.queries.AggregateClusterVulnerabilities(r.Context(), clusterID); err == nil {
		resp["reports_count"] = agg.ReportCount
		if agg.LastScannedAt.Valid {
			resp["last_scan_age_seconds"] = int(time.Since(agg.LastScannedAt.Time).Seconds())
		}
	}

	// Live state via the agent tunnel. Errors collapse to "operator
	// unreachable" rather than 500ing — the page still renders with
	// the DB-side data.
	if h.k8s != nil {
		active, complete, failed := countTrivyJobs(r.Context(), h.k8s, clusterID)
		resp["active_jobs"] = active
		resp["completed_jobs"] = complete
		resp["failed_jobs"] = failed
		resp["scanning"] = active > 0
		resp["trivy_operator_ready"] = trivyOperatorReady(r.Context(), h.k8s, clusterID)
	}

	RespondJSON(w, http.StatusOK, resp)
}

// countTrivyJobs lists scan-vulnerabilityreport-* Jobs in the
// trivy-system namespace and tallies them by terminal condition.
// Returns (active, complete, failed).
func countTrivyJobs(ctx context.Context, k K8sRequester, clusterID uuid.UUID) (int, int, int) {
	resp, err := k.Do(ctx, clusterID.String(), http.MethodGet,
		"/apis/batch/v1/namespaces/"+trivyOperatorNamespace+"/jobs", nil, nil)
	if err != nil || resp == nil || resp.StatusCode >= 400 {
		return 0, 0, 0
	}
	body := decodeK8sProgressBody(resp)
	if len(body) == 0 {
		return 0, 0, 0
	}
	var jobs progressJobsList
	if err := json.Unmarshal(body, &jobs); err != nil {
		return 0, 0, 0
	}

	active, complete, failed := 0, 0, 0
	for _, j := range jobs.Items {
		if !strings.HasPrefix(j.Metadata.Name, "scan-vulnerabilityreport-") {
			continue
		}
		// Prefer terminal Conditions — .status.active flips to 0
		// faster than .status.conditions appears, so we can
		// otherwise undercount in-flight jobs as completed.
		isComplete := false
		isFailed := false
		for _, c := range j.Status.Conditions {
			if c.Status != "True" {
				continue
			}
			switch c.Type {
			case "Complete":
				isComplete = true
			case "Failed":
				isFailed = true
			}
		}
		switch {
		case isFailed:
			failed++
		case isComplete:
			complete++
		default:
			active++
		}
	}
	return active, complete, failed
}

// trivyOperatorReady checks the trivy-operator Deployment's
// availableReplicas. Anything >0 means "operator is up + scanning".
func trivyOperatorReady(ctx context.Context, k K8sRequester, clusterID uuid.UUID) bool {
	resp, err := k.Do(ctx, clusterID.String(), http.MethodGet,
		"/apis/apps/v1/namespaces/"+trivyOperatorNamespace+"/deployments?fieldSelector=metadata.name="+trivyOperatorService,
		nil, nil)
	if err != nil || resp == nil || resp.StatusCode >= 400 {
		return false
	}
	body := decodeK8sProgressBody(resp)
	if len(body) == 0 {
		return false
	}
	var deploys progressDeploymentList
	if err := json.Unmarshal(body, &deploys); err != nil {
		return false
	}
	for _, d := range deploys.Items {
		if d.Status.AvailableReplicas > 0 || d.Status.ReadyReplicas > 0 {
			return true
		}
	}
	return false
}

// decodeK8sProgressBody handles the base64-encoded body shape the
// K8sResponsePayload returns. The tunnel proxy base64-encodes the
// body so the JSON envelope stays valid; we decode here and fall
// back to the raw bytes if decoding fails (tests pass raw JSON for
// fakery convenience).
func decodeK8sProgressBody(resp *protocol.K8sResponsePayload) []byte {
	if resp == nil || resp.Body == "" {
		return nil
	}
	if decoded, err := base64.StdEncoding.DecodeString(resp.Body); err == nil {
		return decoded
	}
	return []byte(resp.Body)
}
