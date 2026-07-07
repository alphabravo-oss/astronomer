// Compliance posture rollup (T1.2).
//
// The CISO-facing single-pane-of-glass. We collect everything Rancher
// does (CIS, image-vuln, network-policy coverage, audit retention)
// and more, but until this handler shipped there was no fused
// fleet-wide score. This produces one.
//
// Composite score (0–100):
//
//	posture = CIS    × 0.30
//	        + Vulns  × 0.30
//	        + NetPol × 0.25
//	        + Audit  × 0.15
//
// Sub-score computation (each clamped to [0, 100]):
//
//	CIS    — per-cluster: 100 × passed / (passed + failed) on the
//	         latest cis_scan in security_scan_results; if a cluster
//	         has no CIS scan in the last 30 days the cluster's
//	         sub-score is 50 (informational "unknown") so a fleet of
//	         unsanned clusters still reads as half-ready rather than
//	         artificially high or zero. Fleet sub-score = unweighted
//	         mean over clusters.
//
//	Vulns  — per-cluster: 100 - clamp(critical × 5 + high × 1, 0, 100).
//	         Reflects the "1 critical is roughly as bad as 5 highs"
//	         heuristic Rancher uses on their fleet score. Fleet
//	         sub-score = unweighted mean.
//
//	NetPol — per-cluster: 100 if the cluster has at least one
//	         cluster_security_policies row attached (sprint-set
//	         netpol baseline applied), 0 otherwise. Coarse but
//	         deterministic, and matches Rancher's "applied / not
//	         applied" gate.
//
//	Audit  — fleet-level boolean: 100 if AuditRetentionMonths >= 12
//	         (the SOC 2 prep floor), else 60. The retention sentinel
//	         lives in RuntimeDependencies.AuditLogRetentionMonths so
//	         we don't need a DB hit per request.
//
// Determinism: scoring is a pure function of the (clusters, scans,
// vulns, policies, audit-retention) snapshot. A unit test pins this
// so future edits don't drift the score on a known fixture.
//
// Out of scope: 7-day sparkline (frontend would derive from posture
// time-series — that's a separate snapshot table) and the per-cluster
// click-through anchor (a frontend concern).
package handler

import (
	"context"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
)

// PostureScores is the API response shape.
type PostureScores struct {
	OverallScore    float64          `json:"overall_score"`
	CISScore        float64          `json:"cis_score"`
	VulnsScore      float64          `json:"vulns_score"`
	NetPolScore     float64          `json:"netpol_score"`
	AuditScore      float64          `json:"audit_score"`
	ClusterCount    int              `json:"cluster_count"`
	Clusters        []ClusterPosture `json:"clusters"`
	TopIssues       []PostureIssue   `json:"top_issues"`
	ComputedAt      time.Time        `json:"computed_at"`
	RetentionMonths int              `json:"audit_retention_months"`
}

// ClusterPosture is the per-cluster breakdown used by the per-row
// frontend bar chart.
type ClusterPosture struct {
	ClusterID    uuid.UUID `json:"cluster_id"`
	ClusterName  string    `json:"cluster_name"`
	OverallScore float64   `json:"overall_score"`
	CISScore     float64   `json:"cis_score"`
	VulnsScore   float64   `json:"vulns_score"`
	NetPolScore  float64   `json:"netpol_score"`
}

// PostureIssue is one top-issue surfaced to the operator.
type PostureIssue struct {
	ClusterID   uuid.UUID `json:"cluster_id"`
	ClusterName string    `json:"cluster_name"`
	Severity    string    `json:"severity"`
	Category    string    `json:"category"`
	Message     string    `json:"message"`
}

// CompliancePostureQuerier is the slice of *sqlc.Queries the handler
// touches. Local interface so a unit test can substitute a fake.
//
// All the scoring inputs are fetched with fleet-wide BATCH queries
// (one round-trip each) rather than ~3 queries per cluster, so a large
// fleet costs a constant number of DB hits.
type CompliancePostureQuerier interface {
	ListClusters(ctx context.Context, arg sqlc.ListClustersParams) ([]sqlc.Cluster, error)
	LatestCISScanPerCluster(ctx context.Context) ([]sqlc.LatestCISScanPerClusterRow, error)
	AggregateVulnerabilitiesPerCluster(ctx context.Context) ([]sqlc.AggregateVulnerabilitiesPerClusterRow, error)
	ListClusterIDsWithSecurityPolicy(ctx context.Context) ([]uuid.UUID, error)
}

// CompliancePostureHandler computes the fleet posture rollup. Read-only.
type CompliancePostureHandler struct {
	q                 CompliancePostureQuerier
	auditRetentionMos int
	now               func() time.Time
}

// NewCompliancePostureHandler wires the handler. auditRetentionMonths
// comes from the same RuntimeDependencies field the audit retention
// sweeper uses; passing nil-now defaults to time.Now.UTC.
func NewCompliancePostureHandler(q CompliancePostureQuerier, auditRetentionMonths int) *CompliancePostureHandler {
	return &CompliancePostureHandler{
		q:                 q,
		auditRetentionMos: auditRetentionMonths,
		now:               func() time.Time { return time.Now().UTC() },
	}
}

// withNow lets tests freeze the clock so CIS-staleness checks are
// deterministic. Not part of the public API.
func (h *CompliancePostureHandler) withNow(fn func() time.Time) *CompliancePostureHandler {
	h.now = fn
	return h
}

// Get is the HTTP handler. Returns 200 with the full posture rollup.
// Empty fleet returns 200 with 0 clusters and overall_score=0.
func (h *CompliancePostureHandler) Get(w http.ResponseWriter, r *http.Request) {
	// Page through the whole fleet so no cluster is silently truncated
	// regardless of fleet size — the rollup's value is being exhaustive.
	const pageSize int32 = 500
	clusters := make([]sqlc.Cluster, 0)
	for offset := int32(0); ; offset += pageSize {
		page, err := h.q.ListClusters(r.Context(), sqlc.ListClustersParams{Limit: pageSize, Offset: offset})
		if err != nil {
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list clusters")
			return
		}
		clusters = append(clusters, page...)
		if int32(len(page)) < pageSize {
			break
		}
	}
	out := h.compute(r.Context(), clusters)
	RespondJSON(w, http.StatusOK, out)
}

// compute is the pure-data aggregator. Pulled out of Get so unit
// tests can drive it directly with a fake querier.
func (h *CompliancePostureHandler) compute(ctx context.Context, clusters []sqlc.Cluster) PostureScores {
	out := PostureScores{
		ComputedAt:      h.now(),
		RetentionMonths: h.auditRetentionMos,
		ClusterCount:    len(clusters),
		Clusters:        make([]ClusterPosture, 0, len(clusters)),
	}

	if len(clusters) == 0 {
		out.AuditScore = h.auditSubScore()
		out.OverallScore = weightedOverall(0, 0, 0, out.AuditScore)
		return out
	}

	// Fetch every scoring input in a fixed number of BATCH queries built
	// ONCE here, then score each cluster from the in-memory maps. This
	// replaces the old ~3-queries-per-cluster fan-out.
	cisByCluster, vulnByCluster, hasPolicy, policyOK := h.batchInputs(ctx)

	var cisSum, vulnSum, netpolSum float64
	for _, c := range clusters {
		cisRow, cisOK := cisByCluster[c.ID]
		vulnRow, vulnOK := vulnByCluster[c.ID]
		cp := ClusterPosture{
			ClusterID:   c.ID,
			ClusterName: c.Name,
			CISScore:    h.cisSubScore(cisRow, cisOK),
			VulnsScore:  h.vulnsSubScore(vulnRow, vulnOK, &out.TopIssues, c.ID, c.Name),
			NetPolScore: h.netpolSubScore(hasPolicy[c.ID], policyOK, &out.TopIssues, c.ID, c.Name),
		}
		cp.OverallScore = weightedOverall(cp.CISScore, cp.VulnsScore, cp.NetPolScore, h.auditSubScore())
		out.Clusters = append(out.Clusters, cp)
		cisSum += cp.CISScore
		vulnSum += cp.VulnsScore
		netpolSum += cp.NetPolScore
	}
	n := float64(len(clusters))
	out.CISScore = cisSum / n
	out.VulnsScore = vulnSum / n
	out.NetPolScore = netpolSum / n
	out.AuditScore = h.auditSubScore()
	out.OverallScore = weightedOverall(out.CISScore, out.VulnsScore, out.NetPolScore, out.AuditScore)

	// Top issues: keep the 10 highest-severity entries. Already
	// populated by the per-cluster helpers; truncate here.
	if len(out.TopIssues) > 10 {
		out.TopIssues = out.TopIssues[:10]
	}
	return out
}

// weightedOverall applies the documented weights to the four
// sub-scores. Public-test-visible so the scoring formula is pinned.
func weightedOverall(cis, vulns, netpol, audit float64) float64 {
	return clamp(cis*0.30+vulns*0.30+netpol*0.25+audit*0.15, 0, 100)
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// cisStaleWindow is how long after a CIS scan we still consider the
// score authoritative; older than this and the cluster falls into the
// 50-pt "unknown" bucket.
const cisStaleWindow = 30 * 24 * time.Hour

// batchInputs runs the fixed set of fleet-wide queries the scoring needs
// and returns them as lookup maps keyed by cluster_id, plus a flag for
// whether the security-policy query succeeded (so netpol can fall back to
// the "unknown" 50 bucket on a query error rather than scoring every
// cluster as "no policy"). A failure of the CIS or vuln query degrades to
// an empty map, which the per-cluster helpers already read as the
// "unknown" 50 bucket — matching the old per-cluster error behaviour.
func (h *CompliancePostureHandler) batchInputs(ctx context.Context) (
	cisByCluster map[uuid.UUID]sqlc.LatestCISScanPerClusterRow,
	vulnByCluster map[uuid.UUID]sqlc.AggregateVulnerabilitiesPerClusterRow,
	hasPolicy map[uuid.UUID]bool,
	policyOK bool,
) {
	cisByCluster = make(map[uuid.UUID]sqlc.LatestCISScanPerClusterRow)
	if rows, err := h.q.LatestCISScanPerCluster(ctx); err == nil {
		for _, row := range rows {
			cisByCluster[row.ClusterID] = row
		}
	}

	vulnByCluster = make(map[uuid.UUID]sqlc.AggregateVulnerabilitiesPerClusterRow)
	if rows, err := h.q.AggregateVulnerabilitiesPerCluster(ctx); err == nil {
		for _, row := range rows {
			vulnByCluster[row.ClusterID] = row
		}
	}

	hasPolicy = make(map[uuid.UUID]bool)
	if ids, err := h.q.ListClusterIDsWithSecurityPolicy(ctx); err == nil {
		policyOK = true
		for _, id := range ids {
			hasPolicy[id] = true
		}
	}
	return cisByCluster, vulnByCluster, hasPolicy, policyOK
}

// cisSubScore scores one cluster from its prebuilt latest-CIS-scan row.
// ok is false when the cluster has no CIS scan at all → the 50 "unknown"
// bucket.
func (h *CompliancePostureHandler) cisSubScore(s sqlc.LatestCISScanPerClusterRow, ok bool) float64 {
	if !ok {
		return 50.0
	}
	if s.CompletedAt.Valid && h.now().Sub(s.CompletedAt.Time) > cisStaleWindow {
		return 50.0
	}
	total := s.Passed + s.Failed
	if total <= 0 {
		return 50.0
	}
	return clamp(100.0*float64(s.Passed)/float64(total), 0, 100)
}

// vulnsSubScore scores one cluster from its prebuilt vuln aggregate. ok
// is false when the cluster has no vuln reports → the 50 "unknown" bucket.
func (h *CompliancePostureHandler) vulnsSubScore(agg sqlc.AggregateVulnerabilitiesPerClusterRow, ok bool, issues *[]PostureIssue, id uuid.UUID, name string) float64 {
	if !ok || agg.ReportCount == 0 {
		return 50.0
	}
	penalty := float64(agg.Critical)*5 + float64(agg.High)
	score := clamp(100-penalty, 0, 100)
	if agg.Critical > 0 {
		*issues = append(*issues, PostureIssue{
			ClusterID:   id,
			ClusterName: name,
			Severity:    "critical",
			Category:    "image_vulnerabilities",
			Message:     "cluster has open critical CVEs in image scans",
		})
	}
	return score
}

// netpolSubScore scores one cluster from the prebuilt has-policy set. This
// consults the whole-fleet DISTINCT cluster_id set, so a cluster whose
// policy would sort past any paged window is scored correctly. policyOK is
// false only when the underlying query errored, in which case we fall back
// to the 50 "unknown" bucket rather than scoring the cluster as unprotected.
func (h *CompliancePostureHandler) netpolSubScore(inSet, policyOK bool, issues *[]PostureIssue, id uuid.UUID, name string) float64 {
	if !policyOK {
		return 50.0
	}
	if inSet {
		return 100.0
	}
	*issues = append(*issues, PostureIssue{
		ClusterID:   id,
		ClusterName: name,
		Severity:    "warning",
		Category:    "network_policy_coverage",
		Message:     "cluster has no cluster_security_policies row — netpol baseline not applied",
	})
	return 0.0
}

// auditSubScore is fleet-level. 100 when audit_log retention meets
// the SOC 2 prep floor (12 months); 60 otherwise to surface the
// drift without zeroing the score (operators in dev/preview may run
// shorter retention deliberately).
func (h *CompliancePostureHandler) auditSubScore() float64 {
	if h.auditRetentionMos >= 12 {
		return 100.0
	}
	return 60.0
}
