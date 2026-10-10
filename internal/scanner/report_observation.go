package scanner

import (
	"context"
	"errors"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// ReportTimeObservation records only whether the original upstream field
// parses. A parsed field may still be future, old or untrusted; it grants no
// scanner/DB-used/job witness or clean posture. Missing/invalid timestamps never
// copy the legacy scanned_at receipt fallback into freshness authority.
type ReportTimeObservation struct {
	UpstreamTime *time.Time
	TimeState    string
}

func observeReportTime(raw string) ReportTimeObservation {
	if raw == "" {
		return ReportTimeObservation{TimeState: "missing"}
	}
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return ReportTimeObservation{TimeState: "invalid"}
	}
	utc := parsed.UTC()
	return ReportTimeObservation{UpstreamTime: &utc, TimeState: "valid"}
}

func reportObservationParameters(report uuid.UUID, uid, timestamp string) sqlc.UpsertImageVulnerabilityObservationParams {
	observation := observeReportTime(timestamp)
	// This report-supplied UID is observation data only. An oversized field
	// remains unknown instead of preventing known CVEs from being recorded.
	if len(uid) > 128 {
		uid = ""
	}
	args := sqlc.UpsertImageVulnerabilityObservationParams{ReportID: report, UpstreamReportUid: uid, TimeState: observation.TimeState}
	if observation.UpstreamTime != nil {
		args.UpstreamTime = pgtype.Timestamptz{Time: *observation.UpstreamTime, Valid: true}
	}
	return args
}

func rollbackReportObservation(rollback func(context.Context) error) {
	// The failing request may already be canceled. A fresh finite cleanup
	// context preserves the transaction rollback opportunity before return;
	// actual backend rollback/drain must still be measured in PostgreSQL.
	cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = rollback(cleanup)
}

func persistReportObservation(ctx context.Context, queries Querier, report uuid.UUID, uid, timestamp string) error {
	args := reportObservationParameters(report, uid, timestamp)
	if _, err := queries.UpsertImageVulnerabilityObservation(ctx, args); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("scanner: upstream observation write failed")
	}
	return nil
}
