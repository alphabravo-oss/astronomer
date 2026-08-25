package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type exportSecurityQuerier struct {
	SecurityQuerier
	scan     sqlc.SecurityScanResult
	auditErr error
	audits   []sqlc.CreateAuditLogV1Params
}

func (q *exportSecurityQuerier) GetSecurityScanResultByID(context.Context, uuid.UUID) (sqlc.SecurityScanResult, error) {
	return q.scan, nil
}

func (q *exportSecurityQuerier) CreateAuditLogV1(_ context.Context, arg sqlc.CreateAuditLogV1Params) error {
	q.audits = append(q.audits, arg)
	return q.auditErr
}

func exportScanRequest(scanID uuid.UUID) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/security/scans/"+scanID.String()+"/report.csv", nil)
	routeContext := chi.NewRouteContext()
	routeContext.URLParams.Add("id", scanID.String())
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeContext))
}

func TestExportScanCSVFailsBeforeHeadersWhenMandatoryAuditFails(t *testing.T) {
	scanID := uuid.New()
	queries := &exportSecurityQuerier{
		scan:     sqlc.SecurityScanResult{ID: scanID, ClusterID: uuid.New(), ScanType: "cis", Findings: []byte(`[]`)},
		auditErr: errors.New("audit database unavailable"),
	}
	recorder := httptest.NewRecorder()
	NewSecurityHandler(queries).ExportScanCSV(recorder, exportScanRequest(scanID))

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusServiceUnavailable, recorder.Body.String())
	}
	if got := recorder.Header().Get("Content-Disposition"); got != "" {
		t.Fatalf("Content-Disposition = %q; CSV headers were written before audit persistence", got)
	}
	if strings.Contains(recorder.Body.String(), "test_id,severity") {
		t.Fatalf("CSV body was disclosed despite audit failure: %s", recorder.Body.String())
	}
	if len(queries.audits) != 1 || queries.audits[0].Action != "compliance.report.export" {
		t.Fatalf("audit attempts = %+v", queries.audits)
	}
}

func TestExportScanCSVRecordsContentFreeAuditBeforeDownload(t *testing.T) {
	scanID := uuid.New()
	queries := &exportSecurityQuerier{
		scan: sqlc.SecurityScanResult{
			ID: scanID, ClusterID: uuid.New(), ScanType: "cis", ClusterScanName: "scan-safe",
			Findings: []byte(`[{"test_id":"1.1","severity":"high","status":"fail","description":"sensitive finding","remediation":"fix it"}]`),
		},
	}
	recorder := httptest.NewRecorder()
	NewSecurityHandler(queries).ExportScanCSV(recorder, exportScanRequest(scanID))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	if len(queries.audits) != 1 {
		t.Fatalf("audit rows = %d, want 1", len(queries.audits))
	}
	auditDetail := string(queries.audits[0].Detail)
	for _, forbidden := range []string{"sensitive finding", "fix it", "test_id"} {
		if strings.Contains(auditDetail, forbidden) {
			t.Fatalf("audit detail contains finding content %q: %s", forbidden, auditDetail)
		}
	}
	if !strings.Contains(recorder.Body.String(), "sensitive finding") {
		t.Fatalf("successful CSV omitted finding: %s", recorder.Body.String())
	}
}
