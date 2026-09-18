package sqlc

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// CVSSScoreFloat returns the typed score and ok=false when NULL.
func (v ImageVulnerability) CVSSScoreFloat() (float64, bool) {
	if !v.CvssScore.Valid {
		return 0, false
	}
	f, err := v.CvssScore.Float64Value()
	if err != nil || !f.Valid {
		return 0, false
	}
	return f.Float64, true
}

type InsertImageVulnerabilityParams struct {
	ReportID         uuid.UUID      `json:"report_id"`
	VulnerabilityID  string         `json:"vulnerability_id"`
	Severity         string         `json:"severity"`
	PkgName          string         `json:"pkg_name"`
	InstalledVersion string         `json:"installed_version"`
	FixedVersion     string         `json:"fixed_version"`
	PrimaryLink      string         `json:"primary_link"`
	CvssScore        pgtype.Numeric `json:"cvss_score"`
	Title            string         `json:"title"`
	Description      string         `json:"description"`
}

const insertImageVulnerability = `-- name: InsertImageVulnerability :exec
INSERT INTO image_vulnerabilities (
    report_id, vulnerability_id, severity, pkg_name,
    installed_version, fixed_version, primary_link, cvss_score,
    title, description
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
ON CONFLICT (report_id, vulnerability_id, pkg_name, installed_version) DO NOTHING`

func (q *Queries) InsertImageVulnerability(ctx context.Context, arg InsertImageVulnerabilityParams) error {
	_, err := q.db.Exec(ctx, insertImageVulnerability,
		arg.ReportID,
		arg.VulnerabilityID,
		arg.Severity,
		arg.PkgName,
		arg.InstalledVersion,
		arg.FixedVersion,
		arg.PrimaryLink,
		arg.CvssScore,
		arg.Title,
		arg.Description,
	)
	return err
}

func (q *Queries) BatchInsertImageVulnerabilities(ctx context.Context, rows []InsertImageVulnerabilityParams) error {
	if len(rows) == 0 {
		return nil
	}
	batch := &pgx.Batch{}
	for _, r := range rows {
		batch.Queue(insertImageVulnerability,
			r.ReportID,
			r.VulnerabilityID,
			r.Severity,
			r.PkgName,
			r.InstalledVersion,
			r.FixedVersion,
			r.PrimaryLink,
			r.CvssScore,
			r.Title,
			r.Description,
		)
	}
	br := q.db.(pgxBatchSender).SendBatch(ctx, batch)
	defer br.Close()
	for range rows {
		if _, err := br.Exec(); err != nil {
			return err
		}
	}
	return br.Close()
}

type pgxBatchSender interface {
	SendBatch(ctx context.Context, b *pgx.Batch) pgx.BatchResults
}
