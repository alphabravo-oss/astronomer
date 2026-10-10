package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

type imageDatabaseStatusReaderFixture struct {
	calls  int
	status protocol.ImageDatabaseStatusV1
	err    error
}

func (reader *imageDatabaseStatusReaderFixture) ReadImageDatabaseStatus(context.Context, uuid.UUID, uuid.UUID) (protocol.ImageDatabaseStatusV1, error) {
	reader.calls++
	return reader.status, reader.err
}
func databaseStatusRequest(cluster, report uuid.UUID) *http.Request {
	request := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/"+cluster.String()+"/vulnerabilities/reports/"+report.String()+"/database-status/", nil)
	route := chi.NewRouteContext()
	route.URLParams.Add("cluster_id", cluster.String())
	route.URLParams.Add("id", report.String())
	return request.WithContext(context.WithValue(request.Context(), chi.RouteCtxKey, route))
}
func TestImageDatabaseStatusReadCannotCrossTenantOrHideKnownFindings(t *testing.T) {
	cluster, report := uuid.New(), uuid.New()
	q := newStubVulnQuerier()
	q.reports[report] = sqlc.ImageVulnerabilityReport{ID: report, ClusterID: cluster, HighCount: 1}
	handler := NewImageVulnHandler(q)
	reader := &imageDatabaseStatusReaderFixture{status: protocol.ImageDatabaseStatusV1{ReportID: report.String(), ScanState: "pending", MainState: "fresh", JavaState: "unknown", JavaApplicability: "unknown", EligibleForCleanPosture: true}}
	handler.SetDatabaseStatusReader(reader)
	denied := httptest.NewRecorder()
	handler.ClusterReportDatabaseStatus(denied, databaseStatusRequest(uuid.New(), report))
	if denied.Code != http.StatusNotFound || reader.calls != 0 {
		t.Fatal("tenant mismatch reached provenance reader")
	}
	actual := httptest.NewRecorder()
	handler.ClusterReportDatabaseStatus(actual, databaseStatusRequest(cluster, report))
	var envelope struct {
		Data protocol.ImageDatabaseStatusV1 `json:"data"`
	}
	if actual.Code != 200 || json.Unmarshal(actual.Body.Bytes(), &envelope) != nil || envelope.Data.FindingCount != 1 || envelope.Data.ScanState != "pending" || envelope.Data.EligibleForCleanPosture {
		t.Fatal("pending/unknown status hid positive finding or granted clean posture")
	}
}
func TestImageDatabaseStatusUnavailableAndWrongReportStayUnknownAndRedacted(t *testing.T) {
	for _, name := range []string{"disabled", "reader-error", "wrong-report"} {
		t.Run(name, func(t *testing.T) {
			cluster, report := uuid.New(), uuid.New()
			q := newStubVulnQuerier()
			q.reports[report] = sqlc.ImageVulnerabilityReport{ID: report, ClusterID: cluster, LowCount: 1}
			handler := NewImageVulnHandler(q)
			if name != "disabled" {
				reader := &imageDatabaseStatusReaderFixture{status: protocol.ImageDatabaseStatusV1{ReportID: uuid.NewString(), EligibleForCleanPosture: true}}
				if name == "reader-error" {
					reader.err = errors.New("private-registry-token")
				}
				handler.SetDatabaseStatusReader(reader)
			}
			actual := httptest.NewRecorder()
			handler.ClusterReportDatabaseStatus(actual, databaseStatusRequest(cluster, report))
			var envelope struct {
				Data protocol.ImageDatabaseStatusV1 `json:"data"`
			}
			if actual.Code != 200 || json.Unmarshal(actual.Body.Bytes(), &envelope) != nil || envelope.Data.FindingCount != 1 || envelope.Data.EligibleForCleanPosture || strings.Contains(actual.Body.String(), "private-registry") {
				t.Fatal("unavailable status hid findings, granted clean posture or exposed child error")
			}
		})
	}
}
