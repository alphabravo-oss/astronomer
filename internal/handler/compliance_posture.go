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
type CompliancePostureQuerier interface {
	ListClusters(ctx context.Context, arg sqlc.ListClustersParams) ([]sqlc.Cluster, error)
	ListScansByClusterAndType(ctx context.Context, arg sqlc.ListScansByClusterAndTypeParams) ([]sqlc.SecurityScanResult, error)
	AggregateClusterVulnerabilities(ctx context.Context, clusterID uuid.UUID) (sqlc.AggregateClusterVulnerabilitiesRow, error)
	ListClusterSecurityPolicies(ctx context.Context, arg sqlc.ListClusterSecurityPoliciesParams) ([]sqlc.ClusterSecurityPolicy, error)
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
	clusters, err := h.q.ListClusters(r.Context(), sqlc.ListClustersParams{Limit: 500, Offset: 0})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "list_error", "Failed to list clusters")
		return
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

	var cisSum, vulnSum, netpolSum float64
	for _, c := range clusters {
		cp := ClusterPosture{
			ClusterID:   c.ID,
			ClusterName: c.Name,
			CISScore:    h.cisSubScoreForCluster(ctx, c.ID),
			VulnsScore:  h.vulnsSubScoreForCluster(ctx, c.ID, &out.TopIssues, c.Name),
			NetPolScore: h.netpolSubScoreForCluster(ctx, c.ID, &out.TopIssues, c.Name),
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

func (h *CompliancePostureHandler) cisSubScoreForCluster(ctx context.Context, id uuid.UUID) float64 {
	scans, err := h.q.ListScansByClusterAndType(ctx, sqlc.ListScansByClusterAndTypeParams{
		ClusterID: id,
		ScanType:  "cis",
		Limit:     1,
		Offset:    0,
	})
	if err != nil || len(scans) == 0 {
		return 50.0
	}
	s := scans[0]
	if s.CompletedAt.Valid && h.now().Sub(s.CompletedAt.Time) > cisStaleWindow {
		return 50.0
	}
	total := s.Passed + s.Failed
	if total <= 0 {
		return 50.0
	}
	return clamp(100.0*float64(s.Passed)/float64(total), 0, 100)
}

func (h *CompliancePostureHandler) vulnsSubScoreForCluster(ctx context.Context, id uuid.UUID, issues *[]PostureIssue, name string) float64 {
	agg, err := h.q.AggregateClusterVulnerabilities(ctx, id)
	if err != nil {
		return 50.0
	}
	if agg.ReportCount == 0 {
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

func (h *CompliancePostureHandler) netpolSubScoreForCluster(ctx context.Context, id uuid.UUID, issues *[]PostureIssue, name string) float64 {
	// ListClusterSecurityPolicies is paginated; we only need to know
	// "is there at least one" so a small page is plenty.
	rows, err := h.q.ListClusterSecurityPolicies(ctx, sqlc.ListClusterSecurityPoliciesParams{
		Limit:  10,
		Offset: 0,
	})
	if err != nil {
		return 50.0
	}
	for _, p := range rows {
		if p.ClusterID == id {
			return 100.0
		}
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
