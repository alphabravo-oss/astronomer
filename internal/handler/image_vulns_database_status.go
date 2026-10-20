package handler

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/api/apiutil"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

type ImageDatabaseStatusReader interface {
	ReadImageDatabaseStatus(context.Context, uuid.UUID, uuid.UUID) (protocol.ImageDatabaseStatusV1, error)
}

func (h *ImageVulnHandler) SetDatabaseStatusReader(reader ImageDatabaseStatusReader) {
	if h != nil {
		h.databaseStatus = reader
	}
}

// The same cluster-read route policy applies before this handler. Report/URL
// tenant equality is checked BEFORE calling the constructor-owned reader.
func (h *ImageVulnHandler) ClusterReportDatabaseStatus(w http.ResponseWriter, r *http.Request) {
	cluster, valid := parseClusterID(w, r)
	if !valid {
		return
	}
	reportID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		apiutil.WriteError(w, r, http.StatusBadRequest, apiutil.InvalidID, "Invalid report ID")
		return
	}
	report, err := h.queries.GetImageVulnerabilityReportByID(r.Context(), reportID)
	if err != nil || report.ClusterID != cluster {
		apiutil.WriteError(w, r, http.StatusNotFound, apiutil.NotFound, "Vulnerability report not found")
		return
	}
	known := int64(report.CriticalCount) + int64(report.HighCount) + int64(report.MediumCount) + int64(report.LowCount) + int64(report.UnknownCount)
	status := protocol.ImageDatabaseStatusV1{ReportID: reportID.String(), ScanState: "complete", MainState: "unknown", JavaState: "unknown", JavaApplicability: "unknown", Reason: "owned-scan-provenance-unavailable", FindingCount: max(known, 0)}
	if h.databaseStatus != nil {
		actual, err := h.databaseStatus.ReadImageDatabaseStatus(r.Context(), cluster, reportID)
		if err != nil || actual.ReportID != reportID.String() {
			status.ScanState = "unavailable"
			status.Reason = "database-status-unavailable"
		} else {
			status = actual
			status.FindingCount = max(status.FindingCount, known)
		}
	}
	// A prior known finding cannot disappear on pending/error/no-witness status.
	// Do not infer clean from empty findings, receipt time or scan completion.
	if status.FindingCount > 0 || !status.CurrentWorkloadChecked || status.JavaApplicability == "unknown" {
		status.EligibleForCleanPosture = false
	}
	RespondJSON(w, http.StatusOK, status)
}
