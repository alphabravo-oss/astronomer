package scanner

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
)

// Existing fakes retain their original report/CVE behavior. No optional
// interface assertion may silently omit the production observation write.
func (f *fakeQuerier) UpsertImageVulnerabilityObservation(_ context.Context, arg sqlc.UpsertImageVulnerabilityObservationParams) (sqlc.ImageVulnerabilityObservation, error) {
	return sqlc.ImageVulnerabilityObservation{ReportID: arg.ReportID, UpstreamReportUid: arg.UpstreamReportUid, UpstreamTime: arg.UpstreamTime, TimeState: arg.TimeState}, nil
}
func (failingQuerier) UpsertImageVulnerabilityObservation(context.Context, sqlc.UpsertImageVulnerabilityObservationParams) (sqlc.ImageVulnerabilityObservation, error) {
	return sqlc.ImageVulnerabilityObservation{}, errors.New("unexpected observation")
}

type observationIngestQuerier struct {
	*fakeQuerier
	observed sqlc.UpsertImageVulnerabilityObservationParams
	fail     bool
	calls    int
}

func (f *observationIngestQuerier) UpsertImageVulnerabilityObservation(_ context.Context, arg sqlc.UpsertImageVulnerabilityObservationParams) (sqlc.ImageVulnerabilityObservation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.observed = arg
	if f.fail {
		return sqlc.ImageVulnerabilityObservation{}, errors.New("database-response-private-value")
	}
	return sqlc.ImageVulnerabilityObservation{ReportID: arg.ReportID, UpstreamReportUid: arg.UpstreamReportUid, UpstreamTime: arg.UpstreamTime, TimeState: arg.TimeState}, nil
}

func TestReportObservationIngestSeparatesUpstreamClockAndKeepsPositiveFindings(t *testing.T) {
	for _, tc := range []struct{ name, timestamp, state string }{
		{"absent", "", "missing"}, {"invalid", "invalid", "invalid"},
		{"valid", "2026-10-10T12:00:00Z", "valid"}, {"future", "2099-10-10T12:00:00Z", "valid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := &observationIngestQuerier{fakeQuerier: newFakeQuerier()}
			report := sampleReport("observed", 1)
			report.Report.UpdateTimestamp = tc.timestamp
			report.Metadata.UID = "report-uid"
			before := time.Now().UTC()
			if err := NewIngester(q, nil, nil, nil).Ingest(t.Context(), uuid.New(), report); err != nil {
				t.Fatal(err)
			}
			q.mu.Lock()
			defer q.mu.Unlock()
			if q.calls != 1 || q.observed.TimeState != tc.state || q.observed.UpstreamReportUid != "report-uid" || q.observed.UpstreamTime.Valid != (tc.state == "valid") {
				t.Fatal("original observation lost or receipt time substituted")
			}
			if len(q.cves[q.observed.ReportID]) != 1 {
				t.Fatal("positive vulnerability disappeared")
			}
			for _, stored := range q.reportsByKey {
				if tc.state != "valid" && (stored.ScannedAt.Before(before) || stored.ScannedAt.After(time.Now().UTC())) {
					t.Fatal("legacy receipt fallback changed")
				}
			}
		})
	}
}

func TestReportObservationFailureStopsBeforeReplacingKnownCVEs(t *testing.T) {
	q := &observationIngestQuerier{fakeQuerier: newFakeQuerier()}
	cluster := uuid.New()
	report := sampleReport("known", 1)
	ingest := NewIngester(q, nil, nil, nil)
	if err := ingest.Ingest(t.Context(), cluster, report); err != nil {
		t.Fatal(err)
	}
	q.mu.Lock()
	q.fail = true
	deletes := q.deleteCalls
	reportID := q.observed.ReportID
	q.mu.Unlock()
	err := ingest.Ingest(t.Context(), cluster, sampleReport("known", 0))
	if err == nil || strings.Contains(err.Error(), "database-response-private-value") {
		t.Fatal("observation failure missing or raw database error exposed")
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.deleteCalls != deletes || len(q.cves[reportID]) != 1 {
		t.Fatal("known CVEs were deleted before observation succeeded")
	}
	// This fake has no transaction. Actual report-row rollback and summary
	// preservation require the separately owned PostgreSQL17/18 fixture.
}

func TestReportObservationOversizedUIDIsUnknownWithoutHidingFindings(t *testing.T) {
	args := reportObservationParameters(uuid.New(), strings.Repeat("u", 129), "")
	if args.UpstreamReportUid != "" || args.UpstreamTime.Valid || args.TimeState != "missing" {
		t.Fatal("oversized UID or receipt fallback accepted as provenance")
	}
}

func TestReportObservationRollbackUsesFreshBoundedContext(t *testing.T) {
	called := false
	rollbackReportObservation(func(ctx context.Context) error {
		called = true
		deadline, ok := ctx.Deadline()
		if ctx.Err() != nil || !ok || time.Until(deadline) > 5*time.Second {
			t.Fatal("rollback lacks fresh finite cleanup context")
		}
		return nil
	})
	if !called {
		t.Fatal("rollback not attempted")
	}
}
