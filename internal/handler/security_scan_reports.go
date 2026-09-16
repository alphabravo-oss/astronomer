package handler

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/scanner"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// fetchClusterScanReport returns (report, true, nil) when the report exists
// in the cluster, (nil, false, nil) when cis-operator hasn't generated it
// yet, and (nil, false, err) for unexpected errors.
func (h *SecurityHandler) fetchClusterScanReport(ctx context.Context, clusterID uuid.UUID, scanName string) (map[string]any, bool, error) {
	reportName, found, err := h.resolveClusterScanReportName(ctx, clusterID, scanName)
	if err != nil || !found {
		return nil, found, err
	}
	path := fmt.Sprintf("/apis/cis.cattle.io/v1/clusterscanreports/%s", reportName)
	resp, err := h.k8s.Do(ctx, clusterID.String(), http.MethodGet, path, nil, requestHeaders(""))
	if err != nil {
		return nil, false, err
	}
	if resp.StatusCode == http.StatusNotFound {
		return nil, false, nil
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return nil, false, responseError(resp)
	}
	var out map[string]any
	if err := parseJSONResponse(resp, &out); err != nil {
		return nil, false, err
	}
	return out, true, nil
}

func (h *SecurityHandler) resolveClusterScanReportName(ctx context.Context, clusterID uuid.UUID, scanName string) (string, bool, error) {
	if name, found, err := h.fetchClusterScanReportNameFromScan(ctx, clusterID, scanName); err != nil || found {
		return name, found, err
	}
	return h.findClusterScanReportNameByOwner(ctx, clusterID, scanName)
}

func (h *SecurityHandler) fetchClusterScanReportNameFromScan(ctx context.Context, clusterID uuid.UUID, scanName string) (string, bool, error) {
	path := fmt.Sprintf("/apis/cis.cattle.io/v1/clusterscans/%s", scanName)
	resp, err := h.k8s.Do(ctx, clusterID.String(), http.MethodGet, path, nil, requestHeaders(""))
	if err != nil {
		return "", false, err
	}
	if resp.StatusCode == http.StatusNotFound {
		return "", false, nil
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return "", false, responseError(resp)
	}
	var scan struct {
		Status struct {
			ReportName string `json:"reportName"`
		} `json:"status"`
	}
	if err := parseJSONResponse(resp, &scan); err != nil {
		return "", false, err
	}
	if strings.TrimSpace(scan.Status.ReportName) == "" {
		return "", false, nil
	}
	return scan.Status.ReportName, true, nil
}

func (h *SecurityHandler) findClusterScanReportNameByOwner(ctx context.Context, clusterID uuid.UUID, scanName string) (string, bool, error) {
	resp, err := h.k8s.Do(ctx, clusterID.String(), http.MethodGet,
		"/apis/cis.cattle.io/v1/clusterscanreports", nil, requestHeaders(""))
	if err != nil {
		return "", false, err
	}
	if resp.StatusCode == http.StatusNotFound {
		return "", false, nil
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return "", false, responseError(resp)
	}
	var list struct {
		Items []struct {
			Metadata struct {
				Name            string `json:"name"`
				OwnerReferences []struct {
					Name string `json:"name"`
				} `json:"ownerReferences"`
			} `json:"metadata"`
		} `json:"items"`
	}
	if err := parseJSONResponse(resp, &list); err != nil {
		return "", false, err
	}
	for _, item := range list.Items {
		if item.Metadata.Name == scanName {
			return item.Metadata.Name, true, nil
		}
		for _, owner := range item.Metadata.OwnerReferences {
			if owner.Name == scanName {
				return item.Metadata.Name, true, nil
			}
		}
	}
	return "", false, nil
}

// GetScanFull handles GET /api/v1/security/scans/{id}/ — returns the row
// with `findings` parsed out of JSONB so the UI doesn't have to re-decode
// it client-side.
func (h *SecurityHandler) GetScanFull(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid scan ID")
		return
	}
	scan, err := h.loadVisibleEstateScan(r.Context(), id)
	if err != nil {
		if errors.Is(err, errAuthorizationNotConfigured) {
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.Forbidden, "Failed to retrieve user permissions")
			return
		}
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Security scan not found")
		return
	}
	RespondJSON(w, http.StatusOK, scanWithFindings(scan))
}

// ExportScanCSV handles GET /api/v1/security/scans/{id}/report.csv — flattens
// the JSONB findings array into a CSV download for compliance evidence
// archives. Empty when the scan is still running.
func (h *SecurityHandler) ExportScanCSV(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid scan ID")
		return
	}
	scan, err := h.loadVisibleEstateScan(r.Context(), id)
	if err != nil {
		if errors.Is(err, errAuthorizationNotConfigured) {
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.Forbidden, "Failed to retrieve user permissions")
			return
		}
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Security scan not found")
		return
	}
	findings, _ := parseCISFindings(scan.Findings)
	if err := recordMandatoryAudit(r, h.queries, "compliance.report.export", "security_scan", scan.ID.String(), scan.ClusterScanName, map[string]any{
		"cluster_id": scan.ClusterID.String(),
		"scan_type":  scan.ScanType,
	}); err != nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.AuditUnavailable, "Mandatory audit storage is unavailable")
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="cis-scan-%s.csv"`, id.String()))
	cw := csv.NewWriter(w)
	defer cw.Flush()
	_ = cw.Write([]string{"test_id", "severity", "status", "description", "remediation"})
	for _, f := range findings {
		_ = cw.Write([]string{f.TestID, f.Severity, f.Status, f.Description, f.Remediation})
	}
}

// CISFinding is the normalized shape we store in `findings` and surface to
// the UI / CSV. It's intentionally minimal — the cis-operator report has
// dozens of fields, but most users only care about pass/fail + remediation.
type CISFinding struct {
	TestID      string `json:"test_id"`
	Severity    string `json:"severity"`
	Status      string `json:"status"`
	Description string `json:"description"`
	Remediation string `json:"remediation"`
}

func parseCISFindings(raw json.RawMessage) ([]CISFinding, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var out []CISFinding
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// scanWithFindings expands the JSONB findings column into a typed slice on
// the response so the UI can iterate without re-parsing.
func scanWithFindings(scan sqlc.SecurityScanResult) map[string]any {
	findings, _ := parseCISFindings(scan.Findings)
	out := map[string]any{
		"id":                scan.ID.String(),
		"cluster_id":        scan.ClusterID.String(),
		"scan_type":         scan.ScanType,
		"status":            scan.Status,
		"summary":           json.RawMessage(scan.Summary),
		"results":           json.RawMessage(scan.Results),
		"started_at":        scan.StartedAt.UTC().Format(time.RFC3339),
		"cluster_scan_name": scan.ClusterScanName,
		"passed":            scan.Passed,
		"failed":            scan.Failed,
		"warned":            scan.Warned,
		"skipped":           scan.Skipped,
		"findings":          findings,
		"created_at":        scan.CreatedAt.UTC().Format(time.RFC3339),
		"updated_at":        scan.UpdatedAt.UTC().Format(time.RFC3339),
		"poll_attempt":      scan.PollAttempt,
		"terminal_reason":   scan.TerminalReason,
	}
	if scan.CompletedAt.Valid {
		out["completed_at"] = scan.CompletedAt.Time.UTC().Format(time.RFC3339)
	} else {
		out["completed_at"] = nil
	}
	return out
}

// CISCounts is the flattened pass/fail/warn/skip totals from a
// cis-operator ClusterScanReport. Exposed for unit testing of the report
// parser.
type CISCounts struct {
	Total int32
	Pass  int32
	Fail  int32
	Warn  int32
	Skip  int32
}

// FlattenCISReport is the public entry point exercised by the unit tests.
// Given a raw ClusterScanReport object (already JSON-decoded into a map),
// it returns the totals + a slice of normalized findings, plus the summary
// and results JSON we want to persist on the row.
func FlattenCISReport(report map[string]any) (CISCounts, []CISFinding, json.RawMessage, json.RawMessage) {
	counts, findingsRaw, summary, results := flattenCISReport(report)
	var findings []CISFinding
	_ = json.Unmarshal(findingsRaw, &findings)
	return counts, findings, summary, results
}

// flattenCISReport is the shared parser. cis-operator's ClusterScanReport
// stores the actual benchmark output under either:
//   - `spec.reportJSON` (a string-encoded JSON blob, current shape), or
//   - `spec.report` (an inlined object, on some forks).
//
// Inside that payload we expect top-level pass/fail/warn/skip counts plus
// a `results[]` (or `tests[]`) slice of sections, each with an inner
// `checks[]` (or `results[]` / `tests[]`) holding the individual test
// records. We flatten everything into our normalized CISFinding shape so
// the UI doesn't have to learn cis-operator's schema.
func flattenCISReport(report map[string]any) (CISCounts, json.RawMessage, json.RawMessage, json.RawMessage) {
	var counts CISCounts
	findings := make([]CISFinding, 0)

	spec, _ := report["spec"].(map[string]any)
	payload := scanner.DecodeCISReportJSON(spec)

	if v, ok := scanner.NumericField(payload, "total"); ok {
		counts.Total = v
	}
	if v, ok := scanner.NumericField(payload, "pass"); ok {
		counts.Pass = v
	}
	if v, ok := scanner.NumericField(payload, "fail"); ok {
		counts.Fail = v
	}
	if v, ok := scanner.NumericField(payload, "warn"); ok {
		counts.Warn = v
	}
	if v, ok := scanner.NumericField(payload, "skip"); ok {
		counts.Skip = v
	}

	sections, _ := payload["results"].([]any)
	if len(sections) == 0 {
		sections, _ = payload["tests"].([]any)
	}
	for _, raw := range sections {
		section, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		inner, _ := section["checks"].([]any)
		if len(inner) == 0 {
			inner, _ = section["results"].([]any)
		}
		if len(inner) == 0 {
			inner, _ = section["tests"].([]any)
		}
		for _, t := range inner {
			test, ok := t.(map[string]any)
			if !ok {
				continue
			}
			findings = append(findings, CISFinding{
				TestID:      scanner.StringField(test, "id", "test_number", "number"),
				Severity:    scanner.StringField(test, "scored_severity", "severity"),
				Status:      scanner.StringField(test, "state", "status"),
				Description: scanner.StringField(test, "test_desc", "description", "desc"),
				Remediation: scanner.StringField(test, "remediation"),
			})
		}
	}

	if counts.Total == 0 && len(findings) > 0 {
		counts.Total = int32(len(findings))
	}

	summary := map[string]any{
		"total":   counts.Total,
		"pass":    counts.Pass,
		"fail":    counts.Fail,
		"warn":    counts.Warn,
		"skip":    counts.Skip,
		"updated": time.Now().UTC().Format(time.RFC3339),
	}
	summaryRaw, _ := json.Marshal(summary)
	resultsRaw, _ := json.Marshal(map[string]any{
		"source":  "cis-operator",
		"profile": scanner.StringField(spec, "scanProfileName"),
	})
	findingsRaw, _ := json.Marshal(findings)
	return counts, findingsRaw, summaryRaw, resultsRaw
}
